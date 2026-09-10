// pi: proxy.ts — proxy stream function for apps that route LLM calls through
// a server. The server manages auth and proxies requests to LLM providers.
//
// Mapping notes:
//   - pi's `options.signal` becomes the leading ctx (AbortSignal → context
//     rule); the signal abort listener collapses into ctx cancellation
//     closing the response body.
//   - pi's ProxyMessageEventStream class restates the EventStream generic
//     with done/error terminals and the final message as result; pig's
//     AssistantMessageEventStream already encodes exactly that contract.
//   - The `partial` field is a live object shared into every event in pi;
//     pig snapshots it per event (same rule as the openai adapter's push).
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// ProxyAssistantMessageEvent is one proxy event. The server sends these with
// the partial field stripped to reduce bandwidth (pi: ProxyAssistantMessageEvent
// discriminated union; pig uses a single struct where exactly the fields named
// by Type are meaningful).
type ProxyAssistantMessageEvent struct {
	Type string `json:"type"`
	// ContentIndex is set on text_*/thinking_*/toolcall_* events.
	ContentIndex int `json:"contentIndex,omitempty"`
	// Delta is set on text_delta/thinking_delta/toolcall_delta.
	Delta string `json:"delta,omitempty"`
	// ContentSignature is the optional signature on text_end/thinking_end.
	ContentSignature string `json:"contentSignature,omitempty"`
	// ID and ToolName are set on toolcall_start.
	ID       string `json:"id,omitempty"`
	ToolName string `json:"toolName,omitempty"`
	// ToolCall is set on toolcall_end.
	ToolCall *types.ToolCall `json:"toolCall,omitempty"`
	// Reason is set on done (stop|length|toolUse) and error (aborted|error).
	Reason types.StopReason `json:"reason,omitempty"`
	// Usage is set on done/error.
	Usage types.Usage `json:"usage,omitempty"`
	// ErrorMessage is set on error.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// ProviderThinkingLevel is set on done/error.
	ProviderThinkingLevel string `json:"providerThinkingLevel,omitempty"`
}

// ProxySerializableStreamOptions is the subset of SimpleStreamOptions the
// proxy request serializes (pi: ProxySerializableStreamOptions Pick).
type ProxySerializableStreamOptions struct {
	Temperature     *float64               `json:"temperature,omitempty"`
	SamplingParams  map[string]any         `json:"samplingParams,omitempty"`
	MaxTokens       *int64                 `json:"maxTokens,omitempty"`
	Reasoning       types.ThinkingLevel    `json:"reasoning,omitempty"`
	CacheRetention  types.CacheRetention   `json:"cacheRetention,omitempty"`
	SessionID       string                 `json:"sessionId,omitempty"`
	Headers         types.ProviderHeaders  `json:"headers,omitempty"`
	Metadata        map[string]any         `json:"metadata,omitempty"`
	Transport       types.Transport        `json:"transport,omitempty"`
	ThinkingBudgets *types.ThinkingBudgets `json:"thinkingBudgets,omitempty"`
	MaxRetryDelayMs *int64                 `json:"maxRetryDelayMs,omitempty"`
}

// ProxyStreamOptions configures StreamProxy (pi: ProxyStreamOptions; pi's
// `signal?` member is absent — the local abort signal travels as the Go
// context parameter).
type ProxyStreamOptions struct {
	ProxySerializableStreamOptions

	// AuthToken is the auth token for the proxy server.
	AuthToken string
	// ProxyURL is the proxy server URL (e.g. "https://genai.example.com").
	ProxyURL string
}

// buildProxyRequestOptions narrows the options to the serializable subset
// (pi: buildProxyRequestOptions).
func buildProxyRequestOptions(options *ProxyStreamOptions) ProxySerializableStreamOptions {
	return ProxySerializableStreamOptions{
		Temperature:     options.Temperature,
		SamplingParams:  options.SamplingParams,
		MaxTokens:       options.MaxTokens,
		Reasoning:       options.Reasoning,
		CacheRetention:  options.CacheRetention,
		SessionID:       options.SessionID,
		Headers:         options.Headers,
		Metadata:        options.Metadata,
		Transport:       options.Transport,
		ThinkingBudgets: options.ThinkingBudgets,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
	}
}

// StreamProxy streams through a proxy server instead of calling LLM providers
// directly. The server strips the partial field from delta events to reduce
// bandwidth; we reconstruct the partial message client-side. Use this as the
// streamFn when creating an Agent that needs to go through a proxy (pi:
// streamProxy).
func StreamProxy(ctx context.Context, model *types.Model, llmContext *types.Context, options *ProxyStreamOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()

	go func() {
		// Initialize the partial message that we'll build up from events.
		partial := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			StopReason: types.StopReasonPending,
			Content:    []types.Content{},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage: types.Usage{
				Cost: types.Cost{},
			},
			Timestamp: time.Now().UnixMilli(),
		}

		fail := func(err error) {
			errorMessage := err.Error()
			reason := types.StopReasonError
			if ctx.Err() != nil {
				reason = types.StopReasonAborted
			}
			partial.StopReason = reason
			partial.ErrorMessage = errorMessage
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: reason, Message: partial})
			stream.End()
		}

		// push snapshots the shared partial per event: pi's single event loop
		// lets the consumer read the live partial safely, while pig's consumer
		// runs on another goroutine (same rule as the openai adapter's push).
		push := func(event types.AssistantMessageEvent) {
			if event.Partial != nil {
				snapshot := *event.Partial
				snapshot.Content = append([]types.Content(nil), event.Partial.Content...)
				event.Partial = &snapshot
			}
			if event.Message != nil {
				message := *event.Message
				message.Content = append([]types.Content(nil), event.Message.Content...)
				event.Message = &message
			}
			stream.Push(event)
		}

		body, err := json.Marshal(map[string]any{
			"model":   model,
			"context": llmContext,
			"options": buildProxyRequestOptions(options),
		})
		if err != nil {
			fail(err)
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, options.ProxyURL+"/api/stream", bytes.NewReader(body))
		if err != nil {
			fail(err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+options.AuthToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fail(err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errorMessage := fmt.Sprintf("Proxy error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
			var errorData struct {
				Error string `json:"error"`
			}
			if respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20)); readErr == nil {
				if json.Unmarshal(respBody, &errorData) == nil && errorData.Error != "" {
					errorMessage = "Proxy error: " + errorData.Error
				}
			}
			fail(errors.New(errorMessage))
			return
		}

		// partialJSONs holds the streaming tool-call argument scratch buffer
		// per content index (pi keeps it on the content object as partialJson,
		// a field pig's typed ToolCall cannot carry).
		partialJSONs := map[int]string{}
		sawTerminalEvent := false

		processLine := func(line string) error {
			if !strings.HasPrefix(line, "data: ") {
				return nil
			}
			data := strings.TrimSpace(line[len("data: "):])
			if data == "" {
				return nil
			}
			var proxyEvent ProxyAssistantMessageEvent
			if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
				return err
			}
			event, err := processProxyEvent(&proxyEvent, partial, partialJSONs)
			if err != nil {
				return err
			}
			if event != nil {
				if event.Type == types.EventDone || event.Type == types.EventError {
					sawTerminalEvent = true
				}
				push(*event)
			}
			return nil
		}

		// Line streaming over the body reader: pi decodes chunks and splits
		// on "\n" per read; bufio is the Go counterpart and inherently
		// handles the final event not being newline-terminated (the residual
		// buffer flush in pi).
		reader := bufio.NewReader(resp.Body)
		for {
			line, readErr := reader.ReadString('\n')
			if line != "" {
				if err := processLine(strings.TrimRight(line, "\r\n")); err != nil {
					fail(err)
					return
				}
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					break
				}
				fail(readErr)
				return
			}
			if ctx.Err() != nil {
				fail(errors.New("Request aborted by user"))
				return
			}
		}

		if ctx.Err() != nil {
			fail(errors.New("Request aborted by user"))
			return
		}

		if !sawTerminalEvent {
			// A clean EOF without a done/error event means the server dropped the
			// response mid-stream. Surface it as an error instead of leaving
			// consumers waiting on a result that never arrives.
			partial.StopReason = types.StopReasonError
			partial.ErrorMessage = "Connection closed by proxy server before the response completed"
			push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: partial})
		}

		stream.End()
	}()

	return stream
}

// contentAt returns the content at index, reporting ok=false for
// out-of-range or placeholder entries (pi: undefined holes fail the
// `content?.type === ...` guard).
func contentAt(content []types.Content, index int) (types.Content, bool) {
	if index < 0 || index >= len(content) {
		return nil, false
	}
	entry := content[index]
	if entry == nil {
		return nil, false
	}
	return entry, true
}

// setContentAt sets the content at index, growing the slice with nil holes
// when the index is beyond the end (pi assigns sparse array slots; they
// serialize as null the same way).
func setContentAt(content []types.Content, index int, entry types.Content) []types.Content {
	for len(content) <= index {
		content = append(content, nil)
	}
	content[index] = entry
	return content
}

// processProxyEvent applies a proxy event to the partial message and returns
// the reconstructed AssistantMessageEvent (pi: processProxyEvent). partial is
// mutated in place; the caller snapshots it into the pushed event. Errors are
// thrown in pi; pig returns them (pi's toolcall_end mismatch returns undefined
// without throwing — mirrored as (nil, nil)).
func processProxyEvent(proxyEvent *ProxyAssistantMessageEvent, partial *types.AssistantMessage, partialJSONs map[int]string) (*types.AssistantMessageEvent, error) {
	switch proxyEvent.Type {
	case "start":
		return &types.AssistantMessageEvent{Type: types.EventStart, Partial: partial}, nil

	case "text_start":
		partial.Content = setContentAt(partial.Content, proxyEvent.ContentIndex, types.NewTextContent(""))
		return &types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil

	case "text_delta":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			return nil, errors.New("Received text_delta for non-text content")
		}
		if content, ok := entry.(types.TextContent); ok {
			content.Text += proxyEvent.Delta
			partial.Content[proxyEvent.ContentIndex] = content
			return &types.AssistantMessageEvent{
				Type:         types.EventTextDelta,
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}, nil
		}
		return nil, errors.New("Received text_delta for non-text content")

	case "text_end":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			return nil, errors.New("Received text_end for non-text content")
		}
		if content, ok := entry.(types.TextContent); ok {
			// pi assigns the signature unconditionally (undefined drops the
			// key from JSON.stringify).
			content.TextSignature = proxyEvent.ContentSignature
			partial.Content[proxyEvent.ContentIndex] = content
			return &types.AssistantMessageEvent{
				Type:         types.EventTextEnd,
				ContentIndex: proxyEvent.ContentIndex,
				Content:      content.Text,
				Partial:      partial,
			}, nil
		}
		return nil, errors.New("Received text_end for non-text content")

	case "thinking_start":
		partial.Content = setContentAt(partial.Content, proxyEvent.ContentIndex, types.NewThinkingContent(""))
		return &types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil

	case "thinking_delta":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			return nil, errors.New("Received thinking_delta for non-thinking content")
		}
		if content, ok := entry.(types.ThinkingContent); ok {
			content.Thinking += proxyEvent.Delta
			partial.Content[proxyEvent.ContentIndex] = content
			return &types.AssistantMessageEvent{
				Type:         types.EventThinkingDelta,
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}, nil
		}
		return nil, errors.New("Received thinking_delta for non-thinking content")

	case "thinking_end":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			return nil, errors.New("Received thinking_end for non-thinking content")
		}
		if content, ok := entry.(types.ThinkingContent); ok {
			content.ThinkingSignature = proxyEvent.ContentSignature
			partial.Content[proxyEvent.ContentIndex] = content
			return &types.AssistantMessageEvent{
				Type:         types.EventThinkingEnd,
				ContentIndex: proxyEvent.ContentIndex,
				Content:      content.Thinking,
				Partial:      partial,
			}, nil
		}
		return nil, errors.New("Received thinking_end for non-thinking content")

	case "toolcall_start":
		partial.Content = setContentAt(partial.Content, proxyEvent.ContentIndex, types.ToolCall{
			Kind:      types.KindToolCall,
			ID:        proxyEvent.ID,
			Name:      proxyEvent.ToolName,
			Arguments: map[string]any{},
		})
		partialJSONs[proxyEvent.ContentIndex] = ""
		return &types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: proxyEvent.ContentIndex, Partial: partial}, nil

	case "toolcall_delta":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			return nil, errors.New("Received toolcall_delta for non-toolCall content")
		}
		if content, ok := entry.(types.ToolCall); ok {
			partialJSONs[proxyEvent.ContentIndex] += proxyEvent.Delta
			buffer := partialJSONs[proxyEvent.ContentIndex]
			content.Arguments = utils.ParseStreamingJSON(&buffer)
			if content.Arguments == nil {
				content.Arguments = map[string]any{}
			}
			partialJSONs[proxyEvent.ContentIndex] = buffer
			partial.Content[proxyEvent.ContentIndex] = content // Trigger reactivity (pi; a no-op for pig values)
			return &types.AssistantMessageEvent{
				Type:         types.EventToolCallDelta,
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}, nil
		}
		return nil, errors.New("Received toolcall_delta for non-toolCall content")

	case "toolcall_end":
		entry, ok := contentAt(partial.Content, proxyEvent.ContentIndex)
		if !ok {
			// pi returns undefined here without throwing.
			return nil, nil
		}
		if content, ok := entry.(types.ToolCall); ok {
			// pi: Object.assign(content, proxyEvent.toolCall) then delete
			// partialJson. pig replaces the content wholesale; Kind stays
			// "toolCall" on both sides.
			if proxyEvent.ToolCall != nil {
				assigned := *proxyEvent.ToolCall
				assigned.Kind = types.KindToolCall
				content = assigned
			}
			delete(partialJSONs, proxyEvent.ContentIndex)
			partial.Content[proxyEvent.ContentIndex] = content
			return &types.AssistantMessageEvent{
				Type:         types.EventToolCallEnd,
				ContentIndex: proxyEvent.ContentIndex,
				ToolCall:     &content,
				Partial:      partial,
			}, nil
		}
		return nil, nil

	case "done":
		partial.StopReason = proxyEvent.Reason
		partial.Usage = proxyEvent.Usage
		if proxyEvent.ProviderThinkingLevel != "" {
			partial.ProviderThinkingLevel = proxyEvent.ProviderThinkingLevel
		}
		return &types.AssistantMessageEvent{Type: types.EventDone, Reason: proxyEvent.Reason, Message: partial}, nil

	case "error":
		partial.StopReason = proxyEvent.Reason
		partial.ErrorMessage = proxyEvent.ErrorMessage
		partial.Usage = proxyEvent.Usage
		if proxyEvent.ProviderThinkingLevel != "" {
			partial.ProviderThinkingLevel = proxyEvent.ProviderThinkingLevel
		}
		return &types.AssistantMessageEvent{Type: types.EventError, Reason: proxyEvent.Reason, Message: partial}, nil

	default:
		// pi's `_exhaustiveCheck: never` plus console.warn; pig has no
		// exhaustiveness check over a string discriminant and no logger in
		// this package, so unknown types are ignored (both return undefined).
		return nil, nil
	}
}
