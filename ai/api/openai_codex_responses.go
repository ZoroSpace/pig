// pi: api/openai-codex-responses.ts.
//
// The ChatGPT Codex Responses wire adapter: the openai-responses event
// protocol over chatgpt.com/backend-api with ChatGPT OAuth tokens, SSE +
// WebSocket transports, zstd request compression, and Codex-specific retry
// and fallback semantics. The request body is built as a plain JSON map and
// both transports are parsed locally (shared reducer: ProcessResponsesStream).
//
// Transport deviations (declared):
//   - pi constructs a runtime WebSocket via the global WebSocket class and
//     stubs it in tests; pig dials via gorilla/websocket behind the
//     codexDialWebSocket seam variable (tests override the var, the closest
//     analog of stubbing a global).
//   - pi's connectWebSocket deletes the "OpenAI-Beta" header from a
//     lowercased headers record with a case-sensitive key — a no-op, so the
//     header actually ships on the handshake. pig never deletes it.
//   - "WebSocket transport is not available in this runtime" (pi: missing
//     global WebSocket) is unreachable in Go; no counterpart.
package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/klauspost/compress/zstd"
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// ============================================================================
// Configuration (pi: Configuration)
// ============================================================================

const defaultCodexBaseURL = "https://chatgpt.com/backend-api"
const codexJWTClaimPath = "https://api.openai.com/auth"
const codexDefaultMaxRetries = 0
const codexBaseDelayMs = 1000
const codexDefaultMaxRetryDelayMs = 60_000
const codexDefaultWebsocketConnectTimeoutMs = 15_000

// The Codex backend accepts zstd-compressed request bodies on the SSE
// responses endpoint (the same endpoint the official Codex client compresses
// against). pi sets ZSTD_c_compressionLevel=3 (zstd's default level); pig
// maps it to klauspost's SpeedBetterCompression (declared deviation: the
// library's level scale differs, the wire format is identical zstd).
const codexRequestCompressionZstdLevel = zstd.SpeedBetterCompression

// codexToolCallProviders lists the providers whose tool-call ids keep their
// call_id|item_id pair shape on replay (pi: CODEX_TOOL_CALL_PROVIDERS — same
// membership as OPENAI_TOOL_CALL_PROVIDERS, so the shared set is reused).
var codexToolCallProviders = openAIResponsesToolCallProviders

const codexWebsocketMessageTooBigCloseCode = 1009
const codexWebsocketConnectionLimitReachedCode = "websocket_connection_limit_reached"
const codexPreviousResponseNotFoundCode = "previous_response_not_found"

const codexOpenAIBetaResponsesWebsockets = "responses_websockets=2026-02-06"
const codexSessionWebsocketCacheTTLMs = 5 * 60 * 1000
const codexSessionWebsocketMaxAgeMs = 55 * 60 * 1000

// codexResponseStatuses are the response.status values the Codex backend may
// report on terminal events (pi: CODEX_RESPONSE_STATUSES); anything else
// normalizes to undefined (pig: "").
var codexResponseStatuses = map[string]struct{}{
	"completed":   {},
	"incomplete":  {},
	"failed":      {},
	"cancelled":   {},
	"queued":      {},
	"in_progress": {},
}

// ============================================================================
// Types (pi: Types)
// ============================================================================

// OpenAICodexResponsesOptions are the openai-codex-responses request options
// (pi: OpenAICodexResponsesOptions extends StreamOptions).
type OpenAICodexResponsesOptions struct {
	types.StreamOptions
	// ReasoningEffort is the reasoning effort level (pi allows none through
	// max; "none" maps via thinkingLevelMap.off). Empty means unset.
	ReasoningEffort types.ModelThinkingLevel
	// ReasoningSummary is the reasoning summary mode; pi also allows null,
	// which pig folds into "" (both default to "auto").
	ReasoningSummary string
	// ServiceTier is the service_tier request value; empty means unset.
	ServiceTier string
	// TextVerbosity is the text.verbosity request value; empty defaults to
	// "low" (pi: options?.textVerbosity || "low").
	TextVerbosity string
	// ToolChoice is the tool_choice request value ("auto" | "none" |
	// "required"); defaults to "auto" (pi: options?.toolChoice ?? "auto").
	ToolChoice string
}

// codexRequestBody is the Responses request body shape (pi: RequestBody);
// built as a plain JSON map, the type only documents the fixed keys.
type codexRequestBody = map[string]any

// errCodexAborted stands for pi's `throw new Error("Request was aborted")`.
var errCodexAborted = errors.New("Request was aborted")

// assertCodexSuccessfulOutput mirrors pi's assertSuccessfulOutput: the Codex
// stream must end with a terminal stop reason.
func assertCodexSuccessfulOutput(output *types.AssistantMessage) error {
	if output.StopReason == types.StopReasonPending {
		return errors.New("Codex stream ended without a stop reason")
	}
	if output.StopReason == types.StopReasonError || output.StopReason == types.StopReasonAborted {
		if output.ErrorMessage != "" {
			return errors.New(output.ErrorMessage)
		}
		return errors.New("An unknown error occurred")
	}
	return nil
}

// ============================================================================
// Codex error types (pi: CodexApiError / CodexProtocolError)
// ============================================================================

// codexApiError is pi's CodexApiError: a Codex-reported API failure carrying
// the error code. Kept as a distinct type so the WebSocket orchestration can
// tell transport failures (fallback to SSE) from non-transport failures.
type codexApiError struct {
	message string
	code    string
	payload any
}

func (e *codexApiError) Error() string { return e.message }

// codexProtocolError is pi's CodexProtocolError: a malformed stream payload.
type codexProtocolError struct {
	message string
	payload any
}

func (e *codexProtocolError) Error() string { return e.message }

func isCodexNonTransportError(err error) bool {
	var apiErr *codexApiError
	var protoErr *codexProtocolError
	return errors.As(err, &apiErr) || errors.As(err, &protoErr)
}

func isCodexWebsocketConnectionLimitReachedError(err error) bool {
	var apiErr *codexApiError
	return errors.As(err, &apiErr) && apiErr != nil && apiErr.code == codexWebsocketConnectionLimitReachedCode
}

func isCodexPreviousResponseNotFoundError(err error) bool {
	var apiErr *codexApiError
	return errors.As(err, &apiErr) && apiErr != nil && apiErr.code == codexPreviousResponseNotFoundCode
}

// codexRetryDelayExceededError is pi's RetryDelayExceededError.
type codexRetryDelayExceededError struct {
	message string
}

func (e *codexRetryDelayExceededError) Error() string { return e.message }

func isCodexRetryDelayExceededError(err error) bool {
	var delayErr *codexRetryDelayExceededError
	return errors.As(err, &delayErr)
}

// runCodexReduction drives the shared reducer while converting the codex
// event pipeline's panics (codexApiError / codexProtocolError from
// mapCodexEvents and the SSE/WS decoders; Go iterators cannot yield errors)
// back into errors.
func runCodexReduction(events func(yield func(*responsesStreamEvent) bool), output *types.AssistantMessage, stream *types.AssistantMessageEventStream, model *types.Model, options *OpenAIResponsesStreamOptions) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch v := r.(type) {
			case *codexApiError:
				err = v
			case *codexProtocolError:
				err = v
			case error:
				err = v
			default:
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	return ProcessResponsesStream(events, output, stream, model, options)
}

// ============================================================================
// Retry Helpers (pi: Retry Helpers)
// ============================================================================

var codexTerminalRateLimitRe = regexp.MustCompile(`(?i)GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)

func isCodexTerminalRateLimitError(errorText string) bool {
	return codexTerminalRateLimitRe.MatchString(errorText)
}

var codexRetryableRe = regexp.MustCompile(`(?i)rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)

func isCodexRetryableError(status int, errorText string) bool {
	if status == 429 && isCodexTerminalRateLimitError(errorText) {
		return false
	}
	if status == 429 || status == 500 || status == 502 || status == 503 || status == 504 {
		return true
	}
	return codexRetryableRe.MatchString(errorText)
}

// codexRetryAfterDelayMs reads the retry delay from response headers (pi:
// getRetryAfterDelayMs). ok is false when no usable header is present.
func codexRetryAfterDelayMs(headers http.Header) (delayMs int64, ok bool) {
	if retryAfterMs := headers.Get("retry-after-ms"); retryAfterMs != "" {
		if millis, err := strconv.ParseFloat(retryAfterMs, 64); err == nil && !isJSNonFinite(millis) {
			return maxInt64(0, int64(millis)), true
		}
	}

	retryAfter := headers.Get("retry-after")
	if retryAfter == "" {
		return 0, false
	}

	if seconds, err := strconv.ParseFloat(retryAfter, 64); err == nil && !isJSNonFinite(seconds) {
		return maxInt64(0, int64(seconds*1000)), true
	}

	if date, err := http.ParseTime(retryAfter); err == nil {
		return maxInt64(0, time.Until(date).Milliseconds()), true
	}

	return 0, false
}

// isJSNonFinite mirrors Number.isFinite's NaN/Infinity rejection (Go's
// ParseFloat accepts the spellings JS does not).
func isJSNonFinite(f float64) bool {
	return f != f || f > 1.7976931348623157e308 || f < -1.7976931348623157e308
}

// validateCodexRetryDelayMs rejects server-requested delays above the cap
// (pi: validateRetryDelayMs).
func validateCodexRetryDelayMs(delayMs int64, maxRetryDelayMs *int64) (int64, error) {
	maxDelay := int64(codexDefaultMaxRetryDelayMs)
	if maxRetryDelayMs != nil {
		maxDelay = *maxRetryDelayMs
	}
	if maxDelay > 0 && delayMs > maxDelay {
		return 0, &codexRetryDelayExceededError{
			message: fmt.Sprintf("Server requested %ds retry delay (max: %ds)", ceilDivMs(delayMs), ceilDivMs(maxDelay)),
		}
	}
	return delayMs, nil
}

// ceilDivMs mirrors Math.ceil(delayMs / 1000).
func ceilDivMs(ms int64) int64 {
	return (ms + 999) / 1000
}

// codexSleep is the abort-aware retry sleep (pi: sleep). Overridden in tests
// to observe scheduled delays without real waiting (pi: vi.useFakeTimers).
var codexSleep = func(ctx context.Context, d time.Duration) error {
	if ctx == nil {
		time.Sleep(d)
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errCodexAborted
	case <-timer.C:
		return nil
	}
}

// normalizeCodexTimeoutMs validates a timeout option (pi: normalizeTimeoutMs).
// A negative value throws; zero means "no timeout" (pi: Math.floor(0) = 0,
// callers check > 0).
func normalizeCodexTimeoutMs(valueMs int64) (time.Duration, error) {
	if valueMs < 0 {
		return 0, fmt.Errorf("Invalid timeoutMs: %d", valueMs)
	}
	return time.Duration(valueMs) * time.Millisecond, nil
}

// ============================================================================
// Request Compression (pi: Request Compression)
// ============================================================================

// compressRequestBodyZstd zstd-compresses the request body for the SSE path.
// pi returns null when compression is unavailable (browser/Vite builds); pig
// only fails on a compressor error and falls back to the uncompressed body.
func compressRequestBodyZstd(bodyJSON string) []byte {
	var out bytes.Buffer
	writer, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(codexRequestCompressionZstdLevel))
	if err != nil {
		return nil
	}
	if _, err := writer.Write([]byte(bodyJSON)); err != nil {
		return nil
	}
	if err := writer.Close(); err != nil {
		return nil
	}
	return out.Bytes()
}

// ============================================================================
// Main Stream Function (pi: Main Stream Function)
// ============================================================================

// OpenAICodexResponses implements ProviderStreams for the openai-codex-responses
// API (pi: the { stream, streamSimple } module object).
type OpenAICodexResponses struct{}

var _ types.ProviderStreams = OpenAICodexResponses{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiOpenAICodexResponses, OpenAICodexResponses{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiOpenAICodexResponses, OpenAICodexResponses{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation (pi: options.signal).
func (OpenAICodexResponses) Stream(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &OpenAICodexResponsesOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamOpenAICodexResponses(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (OpenAICodexResponses) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	if options.APIKey == "" {
		// pi throws synchronously; pig returns the failure in the stream
		// (see types.StreamFunction contract).
		return immediateErrorStream(model, fmt.Errorf("No API key for provider: %s", model.Provider))
	}
	base := BuildBaseOptions(model, llmContext, options, options.APIKey)
	opts := &OpenAICodexResponsesOptions{StreamOptions: base}
	opts.ToolChoice = string(options.ToolChoice)
	if options.Reasoning != "" {
		clamped := ai.ClampThinkingLevel(model, types.ModelThinkingLevel(options.Reasoning))
		if clamped != types.ModelThinkingOff {
			opts.ReasoningEffort = clamped
		}
	}
	return streamOpenAICodexResponses(ctx, model, llmContext, opts)
}

// formatCodexError renders a provider failure (pi:
// formatProviderError(normalizeProviderError(error)) — no prefix, unlike the
// openai-responses adapter's "OpenAI API error").
func formatCodexError(err error) string {
	return utils.FormatProviderError(utils.NormalizeProviderError(err), "")
}

// streamOpenAICodexResponses is the adapter's core stream implementation
// (pi: the stream StreamFunction body).
func streamOpenAICodexResponses(ctx context.Context, model *types.Model, llmContext *types.Context, options *OpenAICodexResponsesOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()

	go func() {
		output := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      types.Usage{},
			StopReason: types.StopReasonPending,
			Timestamp:  time.Now().UnixMilli(),
		}

		push := func(event types.AssistantMessageEvent) {
			if event.Partial != nil {
				partial := *event.Partial
				event.Partial = &partial
			}
			if event.Message != nil {
				message := *event.Message
				event.Message = &message
			}
			stream.Push(event)
		}
		fail := func(err error) {
			// pi: output.stopReason = options?.signal?.aborted ? "aborted" :
			// "error"; pi also strips the streaming scratch buffers
			// (partialJson / customInput) from content blocks, which pig's
			// value snapshots never carried.
			if ctx.Err() != nil {
				output.StopReason = types.StopReasonAborted
			} else {
				output.StopReason = types.StopReasonError
			}
			output.ErrorMessage = formatCodexError(err)
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
			stream.EndWith(*output)
		}

		// pi's try/catch: internal logic errors panic and are recovered here.
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					fail(err)
				} else {
					fail(fmt.Errorf("%v", r))
				}
			}
		}()

		streamErr := func() error {
			// pi reads options.apiKey directly (the ChatGPT token is always
			// explicit; no ambient key resolution).
			apiKey := options.APIKey
			if apiKey == "" {
				return fmt.Errorf("No API key for provider: %s", model.Provider)
			}
			accountID, err := extractCodexAccountID(apiKey)
			if err != nil {
				return err
			}
			grammarToolInputProperties := createCodexGrammarToolInputProperties(model, llmContext)
			cacheRetention := resolveCacheRetention(options.CacheRetention, options.Env)
			cacheSessionID := options.SessionID
			if cacheRetention == types.CacheRetentionNone {
				cacheSessionID = ""
			}
			codexSessionID := ClampOpenAIPromptCacheKey(cacheSessionID)
			body := buildCodexRequestBody(model, llmContext, options, codexSessionID, grammarToolInputProperties)
			if options.OnPayload != nil {
				if next := options.OnPayload(body, model); next != nil {
					if nextBody, ok := next.(map[string]any); ok {
						body = nextBody
					}
				}
			}
			websocketRequestID := codexSessionID
			if websocketRequestID == "" {
				websocketRequestID = utils.UUIDv7()
			}
			sseHeaders := buildCodexSSEHeaders(model.Headers, options.Headers, accountID, apiKey, codexSessionID)
			websocketHeaders := buildCodexWebsocketHeaders(model.Headers, options.Headers, accountID, apiKey, websocketRequestID)
			bodyJSON, err := json.Marshal(body)
			if err != nil {
				return err
			}
			httpTimeout, err := normalizeCodexTimeoutMs(options.TimeoutMs)
			if err != nil {
				return err
			}
			websocketConnectTimeout := time.Duration(codexDefaultWebsocketConnectTimeoutMs) * time.Millisecond
			if options.WebsocketConnectTimeoutMs != nil {
				wsTimeout, err := normalizeCodexTimeoutMs(*options.WebsocketConnectTimeoutMs)
				if err != nil {
					return err
				}
				websocketConnectTimeout = wsTimeout
			}
			transport := string(options.Transport)
			if transport == "" {
				transport = "auto"
			}
			startEmitted := false
			websocketDisabledForSession := transport != "sse" && isCodexWebsocketSseFallbackActive(cacheSessionID)
			if websocketDisabledForSession {
				recordCodexWebsocketSseFallback(cacheSessionID)
			}

			if transport != "sse" && !websocketDisabledForSession {
				websocketStarted := false
				retriedWebsocketConnectionLimit := false
				retriedMissingWebsocketContinuation := false
				for {
					websocketStarted = false
					wsErr := func() error {
						err := processCodexWebsocketStream(ctx, resolveCodexWebsocketURL(model.BaseURL), body, codexHeadersToHTTPHeader(websocketHeaders), output, stream, model, func() {
							websocketStarted = true
							if !startEmitted {
								startEmitted = true
								push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})
							}
						}, httpTimeout, websocketConnectTimeout, cacheSessionID, accountID, grammarToolInputProperties, options)
						if err != nil {
							return err
						}
						if ctx.Err() != nil {
							return errCodexAborted
						}
						return assertCodexSuccessfulOutput(output)
					}()
					if wsErr == nil {
						// pi pushes done inside the try and returns; pig's outer
						// post-closure block performs the single done push.
						return nil
					}
					aborted := ctx.Err() != nil || errors.Is(wsErr, errCodexAborted)
					connectionLimitBeforeStart := !websocketStarted && isCodexWebsocketConnectionLimitReachedError(wsErr)
					previousResponseNotFound := isCodexPreviousResponseNotFoundError(wsErr)
					if !aborted && previousResponseNotFound && !retriedMissingWebsocketContinuation {
						retriedMissingWebsocketContinuation = true
						continue
					}
					if !aborted && connectionLimitBeforeStart && !retriedWebsocketConnectionLimit {
						retriedWebsocketConnectionLimit = true
						continue
					}
					if aborted || (isCodexNonTransportError(wsErr) && !connectionLimitBeforeStart) {
						return wsErr
					}
					fallbackTransport := ""
					if !websocketStarted {
						fallbackTransport = "sse"
					}
					phase := "before_message_stream_start"
					if websocketStarted {
						phase = "after_message_stream_start"
					}
					utils.AppendAssistantMessageDiagnostic(output, utils.CreateAssistantMessageDiagnostic("provider_transport_failure", wsErr, map[string]any{
						"configuredTransport": transport,
						"fallbackTransport":   fallbackTransport,
						"eventsEmitted":       websocketStarted,
						"phase":               phase,
						"requestBytes":        len(bodyJSON),
					}))
					recordCodexWebsocketFailure(cacheSessionID, wsErr)
					if websocketStarted {
						return wsErr
					}
					recordCodexWebsocketSseFallback(cacheSessionID)
					break
				}
			}

			// Compress the request body once for the SSE path. The Codex backend
			// decodes Content-Encoding: zstd; the WebSocket transport above sends the
			// uncompressed JSON frame, matching the official Codex client.
			sseBody := bodyJSON
			if compressed := compressRequestBodyZstd(string(bodyJSON)); compressed != nil {
				sseHeaders["content-encoding"] = "zstd"
				sseBody = compressed
			}

			// Fetch with retry logic for rate limits and transient errors
			var response *http.Response
			var lastError error
			maxRetries := codexDefaultMaxRetries
			if options.MaxRetries != nil {
				maxRetries = *options.MaxRetries
			}

		retryLoop:
			for attempt := 0; attempt <= maxRetries; attempt++ {
				if ctx.Err() != nil {
					return errCodexAborted
				}
				continueLoop := false
				tryErr := func() error {
					resp, err := codexFetchOnce(ctx, options.Fetch, resolveCodexURL(model.BaseURL), sseHeaders, sseBody, httpTimeout)
					if err != nil {
						return err
					}
					if options.OnResponse != nil {
						options.OnResponse(types.ProviderResponse{Status: resp.StatusCode, Headers: utils.HeadersToRecord(resp.Header)}, model)
					}
					if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
						response = resp
						return nil
					}
					bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					resp.Body.Close()
					if readErr != nil {
						return readErr
					}
					errorText := string(bodyBytes)
					if attempt < maxRetries && isCodexRetryableError(resp.StatusCode, errorText) {
						if delayMs, ok := codexRetryAfterDelayMs(resp.Header); ok {
							if _, err := validateCodexRetryDelayMs(delayMs, options.MaxRetryDelayMs); err != nil {
								return err
							}
							if err := codexSleep(ctx, time.Duration(delayMs)*time.Millisecond); err != nil {
								return err
							}
						} else {
							if err := codexSleep(ctx, time.Duration(codexBaseDelayMs<<attempt)*time.Millisecond); err != nil {
								return err
							}
						}
						continueLoop = true
						return nil
					}
					// Parse error for friendly message on final attempt or non-retryable error
					info := parseCodexErrorResponse(resp.StatusCode, resp.Status, errorText)
					if info.friendlyMessage != "" {
						return errors.New(info.friendlyMessage)
					}
					return errors.New(info.message)
				}()
				if tryErr == nil {
					if continueLoop {
						continue
					}
					break retryLoop
				}
				// catch: aborts propagate; other errors retry with backoff
				// unless they are retry-delay rejections or usage-limit
				// failures (pi's catch clause verbatim).
				if errors.Is(tryErr, errCodexAborted) {
					return errCodexAborted
				}
				lastError = tryErr
				if attempt < maxRetries && !isCodexRetryDelayExceededError(lastError) && !strings.Contains(lastError.Error(), "usage limit") {
					if err := codexSleep(ctx, time.Duration(codexBaseDelayMs<<attempt)*time.Millisecond); err != nil {
						return err
					}
					continue
				}
				return lastError
			}

			if response == nil {
				if lastError != nil {
					return lastError
				}
				return errors.New("Failed after retries")
			}

			if !startEmitted {
				startEmitted = true
				push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})
			}
			if err := processCodexStream(ctx, response, output, stream, model, grammarToolInputProperties, options); err != nil {
				// pi's parseSSE throws "Request was aborted" through the
				// reduction on cancellation.
				if ctx.Err() != nil {
					return errCodexAborted
				}
				return err
			}

			if ctx.Err() != nil {
				return errCodexAborted
			}

			return assertCodexSuccessfulOutput(output)
		}()

		if streamErr == nil {
			push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
			stream.EndWith(*output)
		} else {
			fail(streamErr)
		}
	}()

	return stream
}

// codexCancelBody releases the request context after the response body is
// consumed (pi: combinedSignal.cleanup() in the fetch finally — the header
// timeout governs the headers phase only; body reads follow the parent
// signal alone).
type codexCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *codexCancelBody) Close() error {
	b.once.Do(b.cancel)
	return b.ReadCloser.Close()
}

// codexFetchOnce performs one POST (pi: (options?.fetch ?? globalThis.fetch)
// with the combined signal). headerTimeout bounds the headers phase only;
// status handling stays with the caller.
func codexFetchOnce(ctx context.Context, fetch types.FetchFunction, requestURL string, headers map[string]string, body []byte, headerTimeout time.Duration) (*http.Response, error) {
	reqCtx, reqCancel := context.WithCancel(ctx)
	var timer *time.Timer
	if headerTimeout > 0 {
		timer = time.AfterFunc(headerTimeout, reqCancel)
	}
	resp, err := func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for name, value := range headers {
			// Direct map assignment preserves the header name case the Codex
			// backend expects (same convention as openai_responses.go).
			req.Header[name] = []string{value}
		}
		if fetch != nil {
			return fetch(req)
		}
		return http.DefaultClient.Do(req)
	}()
	if err != nil {
		if timer != nil {
			timer.Stop()
		}
		reqCancel()
		if headerTimeout > 0 && ctx.Err() == nil {
			return nil, fmt.Errorf("Codex SSE response headers timed out after %dms", headerTimeout.Milliseconds())
		}
		return nil, err
	}
	if timer != nil {
		timer.Stop()
	}
	resp.Body = &codexCancelBody{ReadCloser: resp.Body, cancel: reqCancel}
	return resp, nil
}

// processCodexStream drives the SSE response through the shared reducer (pi:
// processStream).
func processCodexStream(ctx context.Context, resp *http.Response, output *types.AssistantMessage, stream *types.AssistantMessageEventStream, model *types.Model, grammarToolInputProperties map[string]string, options *OpenAICodexResponsesOptions) error {
	return runCodexReduction(mapCodexEvents(decodeCodexSSE(ctx, resp), output), output, stream, model, &OpenAIResponsesStreamOptions{
		ServiceTier:                options.ServiceTier,
		GrammarToolInputProperties: grammarToolInputProperties,
		ResolveServiceTier:         resolveCodexServiceTier,
		ApplyServiceTierPricing: func(usage *types.Usage, serviceTier string) {
			applyResponsesServiceTierPricing(usage, serviceTier, model)
		},
	})
}

// ============================================================================
// Request Building (pi: Request Building)
// ============================================================================

func buildCodexRequestBody(model *types.Model, llmContext *types.Context, options *OpenAICodexResponsesOptions, cacheSessionID string, grammarToolInputProperties map[string]string) codexRequestBody {
	supportsStrictMode := true
	supportsOpenAIGrammarTools := false
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok {
		if compat != nil {
			if compat.SupportsStrictMode != nil {
				supportsStrictMode = *compat.SupportsStrictMode
			}
			if compat.SupportsOpenAIGrammarTools != nil {
				supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
			}
		}
	}
	deferredToolsMode := ""
	supportsAdditionalTools := false
	supportsToolSearch := false
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil {
		if compat.SupportsAdditionalTools != nil {
			supportsAdditionalTools = *compat.SupportsAdditionalTools
		}
		if compat.SupportsToolSearch != nil {
			supportsToolSearch = *compat.SupportsToolSearch
		}
	}
	if supportsAdditionalTools {
		deferredToolsMode = "additional-tools"
	} else if supportsToolSearch {
		deferredToolsMode = "tool-search"
	}
	toolPlacement := utils.SplitDeferredTools(llmContext, deferredToolsMode != "", nil)
	instructions := llmContext.SystemPrompt
	if instructions == "" {
		instructions = "You are a helpful assistant."
	}
	textVerbosity := options.TextVerbosity
	if textVerbosity == "" {
		textVerbosity = "low"
	}
	toolChoice := options.ToolChoice
	if toolChoice == "" {
		toolChoice = "auto"
	}
	messages := ConvertResponsesMessages(model, llmContext, codexToolCallProviders, &ConvertResponsesMessagesOptions{
		IncludeSystemPrompt:        lo.ToPtr(false),
		GrammarToolInputProperties: grammarToolInputProperties,
		DeferredTools:              toolPlacement.Deferred,
		DeferredToolNames:          toolPlacement.DeferredNames,
		DeferredToolsMode:          deferredToolsMode,
		ToolOptions: &ConvertResponsesToolsOptions{
			// pi: strict: null — Codex rejects implicit strict defaults.
			StrictNull:                 true,
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: &supportsOpenAIGrammarTools,
		},
	})

	body := codexRequestBody{
		"model":               model.ID,
		"store":               false,
		"stream":              true,
		"instructions":        instructions,
		"input":               messages,
		"text":                map[string]any{"verbosity": textVerbosity},
		"include":             []any{"reasoning.encrypted_content"},
		"tool_choice":         toolChoice,
		"parallel_tool_calls": true,
	}
	// pi: prompt_cache_key: cacheSessionId — omitted (undefined) when no
	// session cache is requested.
	if cacheSessionID != "" {
		body["prompt_cache_key"] = cacheSessionID
	}

	if options.Temperature != nil {
		body["temperature"] = *options.Temperature
	}

	if options.ServiceTier != "" {
		body["service_tier"] = options.ServiceTier
	}

	if len(toolPlacement.Immediate) > 0 {
		body["tools"] = ConvertResponsesTools(toolPlacement.Immediate, &ConvertResponsesToolsOptions{
			StrictNull:                 true,
			SupportsStrictMode:         &supportsStrictMode,
			SupportsOpenAIGrammarTools: &supportsOpenAIGrammarTools,
		})
	}

	if options.ReasoningEffort != "" {
		// pi: effort = reasoningEffort === "none"
		//   ? (thinkingLevelMap?.off ?? "none")
		//   : (thinkingLevelMap?.[effort] ?? effort);
		// A null map entry (including off: null) suppresses the reasoning block.
		var effort any
		if options.ReasoningEffort == "none" {
			if mapped, ok := model.ThinkingLevelMap[types.ModelThinkingOff]; ok && mapped != nil {
				effort = *mapped
			} else {
				effort = "none"
			}
		} else if mapped, ok := model.ThinkingLevelMap[options.ReasoningEffort]; ok {
			if mapped != nil {
				effort = *mapped
			}
			// mapped == nil → effort stays nil → gated below
		} else {
			effort = string(options.ReasoningEffort)
		}
		if effort != nil {
			summary := options.ReasoningSummary
			if summary == "" {
				summary = "auto"
			}
			body["reasoning"] = map[string]any{"effort": effort, "summary": summary}
		}
	}

	return body
}

// createCodexGrammarToolInputProperties resolves the grammar-tool input
// property map (pi: createGrammarToolInputProperties(context.tools,
// model.compat?.supportsOpenAIGrammarTools ?? false)).
func createCodexGrammarToolInputProperties(model *types.Model, llmContext *types.Context) map[string]string {
	supportsOpenAIGrammarTools := false
	if compat, ok := model.Compat.(*types.OpenAIResponsesCompat); ok && compat != nil && compat.SupportsOpenAIGrammarTools != nil {
		supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
	}
	return CreateGrammarToolInputProperties(llmContext.Tools, supportsOpenAIGrammarTools)
}

// ============================================================================
// Service Tier (pi: getServiceTierCostMultiplier / applyServiceTierPricing /
// resolveCodexServiceTier)
// ============================================================================

// The cost multiplier and pricing application are identical to the
// openai-responses adapter's (applyResponsesServiceTierPricing); only the
// tier resolution is Codex-specific.

func resolveCodexServiceTier(responseServiceTier, requestServiceTier string) string {
	if responseServiceTier == "default" && (requestServiceTier == "flex" || requestServiceTier == "priority") {
		return requestServiceTier
	}
	if responseServiceTier == "" {
		return requestServiceTier
	}
	return responseServiceTier
}

// ============================================================================
// URL Resolution (pi: resolveCodexUrl / resolveCodexWebSocketUrl)
// ============================================================================

func resolveCodexURL(baseURL string) string {
	raw := baseURL
	if strings.TrimSpace(raw) == "" {
		raw = defaultCodexBaseURL
	}
	normalized := strings.TrimRight(raw, "/")
	if strings.HasSuffix(normalized, "/codex/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/codex") {
		return normalized + "/responses"
	}
	return normalized + "/codex/responses"
}

func resolveCodexWebsocketURL(baseURL string) string {
	parsed, err := url.Parse(resolveCodexURL(baseURL))
	if err != nil {
		return resolveCodexURL(baseURL)
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	return parsed.String()
}

// ============================================================================
// Response Processing (pi: Response Processing)
// ============================================================================

// mapCodexEvents adapts raw Codex stream events into Responses stream events
// (pi: mapCodexEvents). Codex error events and response.failed become
// codexApiError panics (Go iterators cannot yield errors; runCodexReduction
// recovers them); terminal events are normalized to response.completed and
// end the stream.
func mapCodexEvents(events func(yield func(*responsesStreamEvent) bool), output *types.AssistantMessage) func(yield func(*responsesStreamEvent) bool) {
	return func(yield func(*responsesStreamEvent) bool) {
		for event := range events {
			switch event.Type {
			case "":
				continue
			case "error":
				code, message := extractCodexEventError(event)
				fallback := event.Code
				if fallback == "" {
					fallback = message
				}
				if fallback == "" {
					// pi: JSON.stringify(event)
					raw, _ := json.Marshal(event)
					fallback = string(raw)
				}
				panic(&codexApiError{
					message: fmt.Sprintf("Codex error: %s", fallback),
					code:    code,
					payload: event,
				})
			case "response.failed":
				code := ""
				message := ""
				if event.Response != nil && event.Response.Error != nil {
					code = event.Response.Error.Code
					message = event.Response.Error.Message
				}
				if message == "" {
					message = "Codex response failed"
				}
				panic(&codexApiError{message: message, code: code, payload: event})
			case "response.done", "response.completed", "response.incomplete":
				if event.Response != nil {
					if event.Response.EndTurn != nil {
						output.EndTurn = event.Response.EndTurn
					}
					// pi: normalizeCodexStatus — unknown statuses normalize to
					// undefined (pig: "").
					if _, ok := codexResponseStatuses[event.Response.Status]; !ok {
						event.Response.Status = ""
					}
				}
				event.Type = "response.completed"
				if !yield(event) {
					return
				}
				return
			}
			if !yield(event) {
				return
			}
		}
	}
}

// extractCodexEventError pulls the error code/message from a Codex error
// event (pi: extractCodexEventError — top-level keys first, then the nested
// error object).
func extractCodexEventError(event *responsesStreamEvent) (code, message string) {
	if event.Code != "" {
		code = event.Code
	} else if event.NestedError != nil {
		code = event.NestedError.Code
	}
	if event.Message != "" {
		message = event.Message
	} else if event.NestedError != nil {
		message = event.NestedError.Message
	}
	return code, message
}

// ============================================================================
// SSE Parsing (pi: SSE Parsing)
// ============================================================================

// decodeCodexSSE parses the SSE response body into decoded stream events
// (pi: parseSSE). Frames split on blank lines; only data: lines carry
// payload; [DONE] ends the stream; a residual frame at EOF still processes
// (pi issue #9047). Malformed JSON raises codexProtocolError (pi throws;
// unlike the openai-responses decoder, which synthesizes an error event).
func decodeCodexSSE(ctx context.Context, resp *http.Response) func(yield func(*responsesStreamEvent) bool) {
	return func(yield func(*responsesStreamEvent) bool) {
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var pending []string
		flush := func() bool {
			if len(pending) == 0 {
				return true
			}
			data := strings.TrimSpace(strings.Join(pending, "\n"))
			pending = nil
			if data == "" || data == "[DONE]" {
				return true
			}
			var event responsesStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				panic(&codexProtocolError{
					message: fmt.Sprintf("Invalid Codex SSE JSON: %s", utils.FormatThrownValue(err)),
					payload: data,
				})
			}
			return yield(&event)
		}
		for {
			if ctx.Err() != nil {
				return
			}
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimRight(line, "\r\n")
				if strings.HasPrefix(trimmed, "data:") {
					pending = append(pending, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
				} else if trimmed == "" {
					if !flush() {
						return
					}
				}
			}
			if err != nil {
				// pi: done && buffer.trim() — the residual frame terminates
				// without a trailing blank line.
				if flushErr := flush(); !flushErr {
					return
				}
				return
			}
		}
	}
}

// ============================================================================
// WebSocket Parsing (pi: WebSocket Parsing)
// ============================================================================

// codexWSConn is the transport seam behind pi's global WebSocket. Read
// blocks until one text/binary message arrives, the idle timeout fires, or
// ctx is done; Close is fire-and-forget; Reusable mirrors readyState OPEN.
type codexWSConn interface {
	Send(text string) error
	Read(ctx context.Context, idleTimeout time.Duration) (string, error)
	Close(code int, reason string)
	Reusable() bool
}

// errCodexWSIdleTimeout reports a read-idle timeout (pi: the parseWebSocket
// idle timer throwing "WebSocket idle timeout after Nms").
var errCodexWSIdleTimeout = errors.New("websocket idle timeout")

// codexWSCloseError is pi's WebSocketCloseError.
type codexWSCloseError struct {
	message  string
	code     int
	hasCode  bool
	reason   string
	wasClean bool
}

func (e *codexWSCloseError) Error() string { return e.message }

// codexDialWebSocket dials the Codex WebSocket endpoint (pi: the global
// WebSocket constructor; tests override this var, mirroring
// vi.stubGlobal("WebSocket", ...)).
var codexDialWebSocket = func(ctx context.Context, requestURL string, headers http.Header) (codexWSConn, error) {
	dialer := &websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, requestURL, headers)
	if err != nil {
		return nil, err
	}
	return &codexGorillaConn{conn: conn}, nil
}

// codexHeadersToHTTPHeader converts the internal header map to http.Header
// for the WebSocket handshake (gorilla dials with an http.Header).
func codexHeadersToHTTPHeader(headers map[string]string) http.Header {
	out := make(http.Header, len(headers))
	for name, value := range headers {
		out[name] = []string{value}
	}
	return out
}

// connectCodexWebSocket wraps the dialer with the connect timeout (pi:
// connectWebSocket's connectTimeoutMs + signal handling).
func connectCodexWebSocket(ctx context.Context, requestURL string, headers http.Header, connectTimeoutMs time.Duration) (codexWSConn, error) {
	dialCtx := ctx
	if connectTimeoutMs > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, connectTimeoutMs)
		defer cancel()
	}
	conn, err := codexDialWebSocket(dialCtx, requestURL, headers)
	if err != nil {
		if dialCtx.Err() != nil && ctx.Err() == nil {
			return nil, fmt.Errorf("WebSocket connect timeout after %dms", connectTimeoutMs.Milliseconds())
		}
		return nil, err
	}
	return conn, nil
}

// codexGorillaConn is the gorilla-backed codexWSConn.
type codexGorillaConn struct {
	conn   *websocket.Conn
	closed atomic.Bool
}

func (c *codexGorillaConn) Send(text string) error {
	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
		c.closed.Store(true)
		return err
	}
	return nil
}

func (c *codexGorillaConn) Read(ctx context.Context, idleTimeout time.Duration) (string, error) {
	if idleTimeout > 0 {
		c.conn.SetReadDeadline(time.Now().Add(idleTimeout))
	} else {
		c.conn.SetReadDeadline(time.Time{})
	}
	// Abort watchdog: gorilla reads do not observe ctx; closing the conn
	// unblocks the read (the stream is being torn down anyway).
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			c.conn.Close()
		case <-watchDone:
		}
	}()
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		c.closed.Store(true)
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() || errors.Is(err, os.ErrDeadlineExceeded) {
			return "", errCodexWSIdleTimeout
		}
		return "", err
	}
	return string(data), nil
}

func (c *codexGorillaConn) Close(code int, reason string) {
	if c.closed.Swap(true) {
		return
	}
	deadline := time.Now().Add(time.Second)
	if code == 0 {
		code = websocket.CloseNormalClosure
	}
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), deadline)
	_ = c.conn.Close()
}

func (c *codexGorillaConn) Reusable() bool {
	return !c.closed.Load()
}

// closeCodexWebsocketSilently is pi's closeWebSocketSilently.
func closeCodexWebsocketSilently(conn codexWSConn, code int, reason string) {
	if conn == nil {
		return
	}
	conn.Close(code, reason)
}

// extractCodexWebsocketError renders a WS error event (pi:
// extractWebSocketError).
func extractCodexWebsocketError(err error) error {
	if err == nil {
		return errors.New("WebSocket error")
	}
	return err
}

// extractCodexWebsocketCloseError renders a WS close as an error (pi:
// extractWebSocketCloseError).
func extractCodexWebsocketCloseError(err error) error {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return newCodexWSCloseError(closeErr.Code, closeErr.Text)
	}
	var wrapper *codexWSCloseError
	if errors.As(err, &wrapper) {
		return wrapper
	}
	return err
}

func newCodexWSCloseError(code int, reason string) *codexWSCloseError {
	codeText := ""
	if code != 0 {
		codeText = " " + strconv.Itoa(code)
	}
	reasonText := ""
	if reason != "" {
		reasonText = " " + reason
	}
	if reasonText == "" && code == codexWebsocketMessageTooBigCloseCode {
		reasonText = " message too big"
	}
	message := strings.TrimSpace("WebSocket closed" + codeText + reasonText)
	hasCode := code != 0
	return &codexWSCloseError{message: message, code: code, hasCode: hasCode, reason: reason, wasClean: true}
}

// CodexWebSocketDebugStats are the per-session WebSocket debug counters (pi:
// OpenAICodexWebSocketDebugStats).
type CodexWebSocketDebugStats struct {
	Requests                int
	ConnectionsCreated      int
	ConnectionsReused       int
	CachedContextRequests   int
	StoreTrueRequests       int
	FullContextRequests     int
	DeltaRequests           int
	LastInputItems          int
	LastDeltaInputItems     *int
	LastPreviousResponseID  *string
	WebSocketFailures       int
	SSEFallbacks            int
	WebsocketFallbackActive *bool
	LastWebSocketError      *string
}

// codexCachedWSContinuation is the cached-connection continuation state (pi:
// CachedWebSocketContinuationState).
type codexCachedWSContinuation struct {
	lastRequestBody   codexRequestBody
	lastResponseID    string
	lastResponseItems []any
}

// codexCachedWSConnection is one pooled connection (pi:
// CachedWebSocketConnection).
type codexCachedWSConnection struct {
	conn         codexWSConn
	busy         bool
	createdAt    time.Time
	idleTimer    *time.Timer
	continuation *codexCachedWSContinuation
}

var (
	codexWSStateMu             sync.Mutex
	codexWSSessionCache        = map[string]map[string]*codexCachedWSConnection{}
	codexWSDebugStats          = map[string]*CodexWebSocketDebugStats{}
	codexWSSseFallbackSessions = map[string]struct{}{}
)

func init() {
	ai.RegisterSessionResourceCleanup(closeCodexWebsocketSessionsForCleanup)
}

// closeCodexWebsocketSessionsForCleanup adapts the session cleanup hook
// (pi: registerSessionResourceCleanup(closeOpenAICodexWebSocketSessions)).
func closeCodexWebsocketSessionsForCleanup(sessionID string) {
	CloseOpenAICodexWebSocketSessions(sessionID)
}

// GetOpenAICodexWebSocketDebugStats returns a copy of the session's debug
// stats, or nil (pi: getOpenAICodexWebSocketDebugStats).
func GetOpenAICodexWebSocketDebugStats(sessionID string) *CodexWebSocketDebugStats {
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	stats, ok := codexWSDebugStats[sessionID]
	if !ok {
		return nil
	}
	copied := *stats
	return &copied
}

// ResetOpenAICodexWebSocketDebugStats clears debug stats and SSE-fallback
// markers; with no argument it clears everything (pi:
// resetOpenAICodexWebSocketDebugStats).
func ResetOpenAICodexWebSocketDebugStats(sessionIDs ...string) {
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	if len(sessionIDs) == 0 {
		codexWSDebugStats = map[string]*CodexWebSocketDebugStats{}
		codexWSSseFallbackSessions = map[string]struct{}{}
		return
	}
	for _, sessionID := range sessionIDs {
		delete(codexWSDebugStats, sessionID)
		delete(codexWSSseFallbackSessions, sessionID)
	}
}

// CloseOpenAICodexWebSocketSessions closes pooled sockets; with no argument
// it closes everything (pi: closeOpenAICodexWebSocketSessions).
func CloseOpenAICodexWebSocketSessions(sessionIDs ...string) {
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	closeEntry := func(entry *codexCachedWSConnection) {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
		closeCodexWebsocketSilently(entry.conn, 1000, "debug_close")
	}
	if len(sessionIDs) > 0 {
		for _, sessionID := range sessionIDs {
			for _, entry := range codexWSSessionCache[sessionID] {
				closeEntry(entry)
			}
			delete(codexWSSessionCache, sessionID)
		}
		return
	}
	for sessionID, accountEntries := range codexWSSessionCache {
		for _, entry := range accountEntries {
			closeEntry(entry)
		}
		delete(codexWSSessionCache, sessionID)
	}
}

func getOrCreateCodexWSDebugStats(sessionID string) *CodexWebSocketDebugStats {
	stats, ok := codexWSDebugStats[sessionID]
	if !ok {
		stats = &CodexWebSocketDebugStats{}
		codexWSDebugStats[sessionID] = stats
	}
	return stats
}

func isCodexWebsocketSseFallbackActive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	_, ok := codexWSSseFallbackSessions[sessionID]
	return ok
}

func recordCodexWebsocketSseFallback(sessionID string) {
	if sessionID == "" {
		return
	}
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	stats := getOrCreateCodexWSDebugStats(sessionID)
	stats.SSEFallbacks++
	active := false
	if _, ok := codexWSSseFallbackSessions[sessionID]; ok {
		active = true
	}
	stats.WebsocketFallbackActive = &active
}

func recordCodexWebsocketFailure(sessionID string, err error) {
	if sessionID == "" {
		return
	}
	codexWSStateMu.Lock()
	defer codexWSStateMu.Unlock()
	codexWSSseFallbackSessions[sessionID] = struct{}{}

	stats := getOrCreateCodexWSDebugStats(sessionID)
	stats.WebSocketFailures++
	lastError := utils.FormatThrownValue(err)
	stats.LastWebSocketError = &lastError
	active := true
	stats.WebsocketFallbackActive = &active
}

// isCodexWebsocketReusable reports whether the socket is still open (pi:
// isWebSocketReusable — readyState missing is treated as reusable).
func isCodexWebsocketReusable(conn codexWSConn) bool {
	return conn == nil || conn.Reusable()
}

func isCodexWebsocketSessionExpired(entry *codexCachedWSConnection) bool {
	return time.Since(entry.createdAt) >= time.Duration(codexSessionWebsocketMaxAgeMs)*time.Millisecond
}

// scheduleCodexSessionWebsocketExpiry arms the idle-close timer (pi:
// scheduleSessionWebSocketExpiry).
func scheduleCodexSessionWebsocketExpiry(sessionID, accountID string, entry *codexCachedWSConnection) {
	if entry.idleTimer != nil {
		entry.idleTimer.Stop()
	}
	entry.idleTimer = time.AfterFunc(time.Duration(codexSessionWebsocketCacheTTLMs)*time.Millisecond, func() {
		codexWSStateMu.Lock()
		defer codexWSStateMu.Unlock()
		if entry.busy {
			return
		}
		closeCodexWebsocketSilently(entry.conn, 1000, "idle_timeout")
		if accountEntries, ok := codexWSSessionCache[sessionID]; ok && accountEntries[accountID] == entry {
			delete(accountEntries, accountID)
			if len(accountEntries) == 0 {
				delete(codexWSSessionCache, sessionID)
			}
		}
	})
}

// codexAcquiredWebsocket is acquireWebSocket's result.
type codexAcquiredWebsocket struct {
	conn   codexWSConn
	entry  *codexCachedWSConnection
	reused bool
	// release returns the socket to the pool when keep is set and the
	// connection is reusable; otherwise it closes it (pi: release).
	release func(keep bool)
}

// acquireCodexWebsocket pools connections per (session, account) (pi:
// acquireWebSocket).
func acquireCodexWebsocket(ctx context.Context, requestURL string, headers http.Header, sessionID, accountID string, connectTimeoutMs time.Duration) (*codexAcquiredWebsocket, error) {
	if sessionID == "" {
		conn, err := connectCodexWebSocket(ctx, requestURL, headers, connectTimeoutMs)
		if err != nil {
			return nil, err
		}
		return &codexAcquiredWebsocket{
			conn: conn,
			release: func(bool) {
				closeCodexWebsocketSilently(conn, 1000, "done")
			},
		}, nil
	}

	codexWSStateMu.Lock()
	accountEntries := codexWSSessionCache[sessionID]
	var cached *codexCachedWSConnection
	if accountEntries != nil {
		cached = accountEntries[accountID]
	}
	if cached != nil {
		if cached.idleTimer != nil {
			cached.idleTimer.Stop()
			cached.idleTimer = nil
		}
		if !cached.busy && isCodexWebsocketSessionExpired(cached) {
			closeCodexWebsocketSilently(cached.conn, 1000, "connection_age_limit")
			delete(accountEntries, accountID)
			if len(accountEntries) == 0 {
				delete(codexWSSessionCache, sessionID)
			}
		} else if !cached.busy && isCodexWebsocketReusable(cached.conn) {
			cached.busy = true
			codexWSStateMu.Unlock()
			return &codexAcquiredWebsocket{
				conn:   cached.conn,
				entry:  cached,
				reused: true,
				release: func(keep bool) {
					codexWSStateMu.Lock()
					defer codexWSStateMu.Unlock()
					if !keep || !isCodexWebsocketReusable(cached.conn) {
						closeCodexWebsocketSilently(cached.conn, 1000, "done")
						if currentEntries, ok := codexWSSessionCache[sessionID]; ok {
							if currentEntries[accountID] == cached {
								delete(currentEntries, accountID)
							}
							if len(currentEntries) == 0 {
								delete(codexWSSessionCache, sessionID)
							}
						}
						return
					}
					cached.busy = false
					scheduleCodexSessionWebsocketExpiry(sessionID, accountID, cached)
				},
			}, nil
		}
		if cached.busy {
			codexWSStateMu.Unlock()
			conn, err := connectCodexWebSocket(ctx, requestURL, headers, connectTimeoutMs)
			if err != nil {
				return nil, err
			}
			return &codexAcquiredWebsocket{
				conn: conn,
				release: func(bool) {
					closeCodexWebsocketSilently(conn, 1000, "done")
				},
			}, nil
		}
		if !isCodexWebsocketReusable(cached.conn) {
			closeCodexWebsocketSilently(cached.conn, 1000, "done")
			delete(accountEntries, accountID)
			if len(accountEntries) == 0 {
				delete(codexWSSessionCache, sessionID)
			}
		}
	}
	codexWSStateMu.Unlock()

	conn, err := connectCodexWebSocket(ctx, requestURL, headers, connectTimeoutMs)
	if err != nil {
		return nil, err
	}
	codexWSStateMu.Lock()
	entry := &codexCachedWSConnection{conn: conn, busy: true, createdAt: time.Now()}
	accountEntries = codexWSSessionCache[sessionID]
	if accountEntries == nil {
		accountEntries = map[string]*codexCachedWSConnection{}
		codexWSSessionCache[sessionID] = accountEntries
	}
	accountEntries[accountID] = entry
	codexWSStateMu.Unlock()
	return &codexAcquiredWebsocket{
		conn:  conn,
		entry: entry,
		release: func(keep bool) {
			codexWSStateMu.Lock()
			defer codexWSStateMu.Unlock()
			if !keep || !isCodexWebsocketReusable(entry.conn) {
				closeCodexWebsocketSilently(entry.conn, 1000, "done")
				if entry.idleTimer != nil {
					entry.idleTimer.Stop()
				}
				if currentEntries, ok := codexWSSessionCache[sessionID]; ok {
					if currentEntries[accountID] == entry {
						delete(currentEntries, accountID)
					}
					if len(currentEntries) == 0 {
						delete(codexWSSessionCache, sessionID)
					}
				}
				return
			}
			entry.busy = false
			scheduleCodexSessionWebsocketExpiry(sessionID, accountID, entry)
		},
	}, nil
}

// readCodexWebsocketMessages reads WS frames as decoded stream events (pi:
// parseWebSocket). The stream ends only on a terminal response event; close
// before completion and idle timeouts surface as errors (panics recovered by
// runCodexReduction, matching pi's throws).
func readCodexWebsocketMessages(conn codexWSConn, ctx context.Context, idleTimeoutMs int64) func(yield func(*responsesStreamEvent) bool) {
	return func(yield func(*responsesStreamEvent) bool) {
		sawCompletion := false
		var failed error
		idleTimeout := time.Duration(0)
		if idleTimeoutMs > 0 {
			idleTimeout = time.Duration(idleTimeoutMs) * time.Millisecond
		}
		for {
			if ctx.Err() != nil {
				failed = errCodexAborted
				break
			}
			text, err := conn.Read(ctx, idleTimeout)
			if err != nil {
				if ctx.Err() != nil {
					failed = errCodexAborted
				} else if errors.Is(err, errCodexWSIdleTimeout) {
					failed = fmt.Errorf("WebSocket idle timeout after %dms", idleTimeoutMs)
					closeCodexWebsocketSilently(conn, 1000, "idle_timeout")
				} else if sawCompletion {
					// pi: a close after completion ends the stream silently.
					failed = nil
				} else {
					failed = extractCodexWebsocketCloseError(extractCodexWebsocketError(err))
				}
				break
			}
			var event responsesStreamEvent
			if jsonErr := json.Unmarshal([]byte(text), &event); jsonErr != nil {
				panic(&codexProtocolError{
					message: fmt.Sprintf("Invalid Codex WebSocket JSON: %s", utils.FormatThrownValue(jsonErr)),
					payload: text,
				})
			}
			if event.Type == "response.completed" || event.Type == "response.done" || event.Type == "response.incomplete" {
				sawCompletion = true
			}
			if !yield(&event) {
				return
			}
			if sawCompletion {
				// pi: the completion event marks done; the queue drains and
				// the loop exits after yielding it.
				break
			}
		}

		if failed != nil {
			panic(failed)
		}
		if !sawCompletion {
			panic(errors.New("WebSocket stream closed before response.completed"))
		}
	}
}

func codexRequestBodyWithoutInput(body codexRequestBody) codexRequestBody {
	rest := make(codexRequestBody, len(body))
	for key, value := range body {
		if key == "input" || key == "previous_response_id" {
			continue
		}
		rest[key] = value
	}
	return rest
}

func codexJSONString(value any) string {
	// Go's encoder sorts map keys deterministically, which is what the
	// equality comparisons below need (pi: JSON.stringify's insertion order).
	data, err := json.Marshal(value)
	if err != nil {
		return "<unserializable>"
	}
	return string(data)
}

func codexResponseInputsEqual(a, b any) bool {
	if a == nil {
		a = []any{}
	}
	if b == nil {
		b = []any{}
	}
	return codexJSONString(a) == codexJSONString(b)
}

func codexRequestBodiesMatchExceptInput(a, b codexRequestBody) bool {
	return codexJSONString(codexRequestBodyWithoutInput(a)) == codexJSONString(codexRequestBodyWithoutInput(b))
}

// getCachedCodexWebsocketInputDelta computes the send-only-the-new-items
// delta against the cached continuation (pi: getCachedWebSocketInputDelta).
func getCachedCodexWebsocketInputDelta(body codexRequestBody, continuation *codexCachedWSContinuation) []any {
	if !codexRequestBodiesMatchExceptInput(body, continuation.lastRequestBody) {
		return nil
	}

	currentInput, _ := body["input"].([]any)
	baseline := make([]any, 0, len(continuation.lastRequestBody["input"].([]any))+len(continuation.lastResponseItems))
	if baseInput, ok := continuation.lastRequestBody["input"].([]any); ok {
		baseline = append(baseline, baseInput...)
	}
	baseline = append(baseline, continuation.lastResponseItems...)
	if len(currentInput) < len(baseline) {
		return nil
	}

	prefix := currentInput[:len(baseline)]
	if !codexResponseInputsEqual(prefix, baseline) {
		return nil
	}

	return currentInput[len(baseline):]
}

func buildCachedCodexWebsocketRequestBody(entry *codexCachedWSConnection, body codexRequestBody) codexRequestBody {
	continuation := entry.continuation
	if continuation == nil {
		return body
	}

	delta := getCachedCodexWebsocketInputDelta(body, continuation)
	if delta == nil || continuation.lastResponseID == "" {
		entry.continuation = nil
		return body
	}

	newBody := make(codexRequestBody, len(body)+2)
	for key, value := range body {
		newBody[key] = value
	}
	newBody["previous_response_id"] = continuation.lastResponseID
	newBody["input"] = delta
	return newBody
}

// startCodexWebsocketOutputOnFirstEvent fires onStart before the first event
// flows (pi: startWebSocketOutputOnFirstEvent).
func startCodexWebsocketOutputOnFirstEvent(events func(yield func(*responsesStreamEvent) bool), onStart func()) func(yield func(*responsesStreamEvent) bool) {
	started := false
	return func(yield func(*responsesStreamEvent) bool) {
		for event := range events {
			if !started {
				started = true
				onStart()
			}
			if !yield(event) {
				return
			}
		}
	}
}

// processCodexWebsocketStream sends the request over the WebSocket transport
// and reduces the response (pi: processWebSocketStream).
func processCodexWebsocketStream(ctx context.Context, requestURL string, body codexRequestBody, headers http.Header, output *types.AssistantMessage, stream *types.AssistantMessageEventStream, model *types.Model, onStart func(), idleTimeout time.Duration, connectTimeout time.Duration, cacheSessionID, accountID string, grammarToolInputProperties map[string]string, options *OpenAICodexResponsesOptions) error {
	acquired, err := acquireCodexWebsocket(ctx, requestURL, headers, cacheSessionID, accountID, connectTimeout)
	if err != nil {
		return err
	}
	conn := acquired.conn
	entry := acquired.entry
	reused := acquired.reused
	keepConnection := true
	// pi compares the RAW options.transport here (not the resolved transport):
	// an unset transport resolves to "auto" for transport selection but does
	// NOT enable cached contexts.
	useCachedContext := options.Transport == types.TransportWebsocketCached || options.Transport == types.TransportAuto
	// ChatGPT Codex Responses rejects `store: true` ("Store must be set to false").
	// WebSocket continuation still works via connection-scoped previous_response_id state.
	requestBody := body
	if useCachedContext && entry != nil {
		requestBody = buildCachedCodexWebsocketRequestBody(entry, body)
	}
	var stats *CodexWebSocketDebugStats
	if cacheSessionID != "" {
		codexWSStateMu.Lock()
		stats = getOrCreateCodexWSDebugStats(cacheSessionID)
		codexWSStateMu.Unlock()
	}
	if stats != nil {
		stats.Requests++
		if reused {
			stats.ConnectionsReused++
		} else {
			stats.ConnectionsCreated++
		}
		if useCachedContext {
			stats.CachedContextRequests++
		}
		if store, ok := requestBody["store"].(bool); ok && store {
			stats.StoreTrueRequests++
		}
		if input, ok := requestBody["input"].([]any); ok {
			stats.LastInputItems = len(input)
		} else {
			stats.LastInputItems = 0
		}
		if _, ok := requestBody["previous_response_id"]; ok {
			stats.DeltaRequests++
			if input, ok := requestBody["input"].([]any); ok {
				lastDelta := len(input)
				stats.LastDeltaInputItems = &lastDelta
			}
			lastPrev, _ := requestBody["previous_response_id"].(string)
			stats.LastPreviousResponseID = &lastPrev
		} else {
			stats.FullContextRequests++
			stats.LastDeltaInputItems = nil
			stats.LastPreviousResponseID = nil
		}
	}

	runErr := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				if entry != nil {
					entry.continuation = nil
				}
				keepConnection = false
				switch v := r.(type) {
				case *codexApiError:
					err = v
				case *codexProtocolError:
					err = v
				case error:
					err = v
				default:
					err = fmt.Errorf("%v", r)
				}
			}
		}()
		frame := make(codexRequestBody, len(requestBody)+1)
		for key, value := range requestBody {
			frame[key] = value
		}
		frame["type"] = "response.create"
		frameJSON, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			return marshalErr
		}
		if err := conn.Send(string(frameJSON)); err != nil {
			return err
		}
		idleTimeoutMs := idleTimeout.Milliseconds()
		events := startCodexWebsocketOutputOnFirstEvent(
			mapCodexEvents(readCodexWebsocketMessages(conn, ctx, idleTimeoutMs), output),
			onStart,
		)
		if err := ProcessResponsesStream(events, output, stream, model, &OpenAIResponsesStreamOptions{
			ServiceTier:                options.ServiceTier,
			GrammarToolInputProperties: grammarToolInputProperties,
			ResolveServiceTier:         resolveCodexServiceTier,
			ApplyServiceTierPricing: func(usage *types.Usage, serviceTier string) {
				applyResponsesServiceTierPricing(usage, serviceTier, model)
			},
		}); err != nil {
			if entry != nil {
				entry.continuation = nil
			}
			keepConnection = false
			return err
		}
		if ctx.Err() != nil {
			keepConnection = false
		} else if useCachedContext && entry != nil && output.ResponseID != "" {
			// pi converts the live output object; the converter dispatches on value
			// message types, so dereference (pi: { messages: [output] }).
			responseItems := ConvertResponsesMessages(model, &types.Context{Messages: []types.Message{*output}}, codexToolCallProviders, &ConvertResponsesMessagesOptions{
				IncludeSystemPrompt:        lo.ToPtr(false),
				GrammarToolInputProperties: grammarToolInputProperties,
			})
			filtered := make([]any, 0, len(responseItems))
			for _, item := range responseItems {
				itemMap, ok := item.(map[string]any)
				if !ok {
					filtered = append(filtered, item)
					continue
				}
				if itemMap["type"] == "function_call_output" || itemMap["type"] == "custom_tool_call_output" {
					continue
				}
				filtered = append(filtered, item)
			}
			entry.continuation = &codexCachedWSContinuation{
				lastRequestBody:   body,
				lastResponseID:    output.ResponseID,
				lastResponseItems: filtered,
			}
		}
		return nil
	}()

	acquired.release(keepConnection)
	return runErr
}

// ============================================================================
// Error Handling (pi: Error Handling)
// ============================================================================

// codexParsedErrorResponse is parseErrorResponse's result.
type codexParsedErrorResponse struct {
	message         string
	friendlyMessage string
}

// parseCodexErrorResponse renders a non-OK response body (pi:
// parseErrorResponse).
func parseCodexErrorResponse(status int, statusText, raw string) codexParsedErrorResponse {
	message := raw
	if message == "" {
		message = statusText
	}
	if message == "" {
		message = "Request failed"
	}

	var parsed struct {
		Error *struct {
			Code     string `json:"code"`
			Type     string `json:"type"`
			Message  string `json:"message"`
			PlanType string `json:"plan_type"`
			ResetsAt int64  `json:"resets_at"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed.Error != nil {
		errObj := parsed.Error
		code := errObj.Code
		if code == "" {
			code = errObj.Type
		}
		if regexp.MustCompile(`(?i)usage_limit_reached|usage_not_included|rate_limit_exceeded`).MatchString(code) || status == 429 {
			plan := ""
			if errObj.PlanType != "" {
				plan = fmt.Sprintf(" (%s plan)", strings.ToLower(errObj.PlanType))
			}
			when := ""
			if errObj.ResetsAt > 0 {
				mins := maxInt64(0, (errObj.ResetsAt*1000-time.Now().UnixMilli())/60000)
				when = fmt.Sprintf(" Try again in ~%d min.", mins)
			}
			friendly := strings.TrimSpace(fmt.Sprintf("You have hit your ChatGPT usage limit%s.%s", plan, when))
			message = errObj.Message
			if message == "" {
				message = friendly
			}
			return codexParsedErrorResponse{message: message, friendlyMessage: friendly}
		}
		message = errObj.Message
		if message == "" {
			message = raw
		}
	}

	return codexParsedErrorResponse{message: message}
}

// ============================================================================
// Auth & Headers (pi: Auth & Headers)
// ============================================================================

// extractCodexAccountID pulls the ChatGPT account id from the OAuth token's
// JWT payload (pi: extractAccountId).
func extractCodexAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("Failed to extract accountId from token")
	}
	payload, err := decodeCodexJWTPayload(parts[1])
	if err != nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	var claim struct {
		Auth *struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if jsonErr := json.Unmarshal(payload, &claim); jsonErr != nil {
		return "", errors.New("Failed to extract accountId from token")
	}
	if claim.Auth == nil || claim.Auth.ChatGPTAccountID == "" {
		return "", errors.New("Failed to extract accountId from token")
	}
	return claim.Auth.ChatGPTAccountID, nil
}

// decodeCodexJWTPayload base64-decodes a JWT payload segment; JWTs are
// unpadded base64url while pi's atob is padded standard base64, so both
// spellings decode.
func decodeCodexJWTPayload(segment string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(segment); err == nil {
		return data, nil
	}
	if data, err := base64.RawStdEncoding.DecodeString(segment); err == nil {
		return data, nil
	}
	if data, err := base64.RawURLEncoding.DecodeString(segment); err == nil {
		return data, nil
	}
	return base64.URLEncoding.DecodeString(segment)
}

// buildCodexBaseHeaders assembles the shared Codex header set (pi:
// buildBaseCodexHeaders).
func buildCodexBaseHeaders(initHeaders map[string]string, additionalHeaders types.ProviderHeaders, accountID, token string) map[string]string {
	headers := make(map[string]string, len(initHeaders)+6)
	for name, value := range initHeaders {
		headers[name] = value
	}
	for name, value := range additionalHeaders {
		if value == nil {
			delete(headers, name)
			continue
		}
		headers[name] = *value
	}
	headers["Authorization"] = "Bearer " + token
	headers["chatgpt-account-id"] = accountID
	headers["originator"] = "pi"
	headers["User-Agent"] = utils.GetPiUserAgent()
	return headers
}

func buildCodexSSEHeaders(initHeaders map[string]string, additionalHeaders types.ProviderHeaders, accountID, token, sessionID string) map[string]string {
	headers := buildCodexBaseHeaders(initHeaders, additionalHeaders, accountID, token)
	headers["OpenAI-Beta"] = "responses=experimental"
	headers["accept"] = "text/event-stream"
	headers["content-type"] = "application/json"

	if sessionID != "" {
		headers["session-id"] = sessionID
		headers["x-client-request-id"] = sessionID
	}

	return headers
}

func buildCodexWebsocketHeaders(initHeaders map[string]string, additionalHeaders types.ProviderHeaders, accountID, token, requestID string) map[string]string {
	headers := buildCodexBaseHeaders(initHeaders, additionalHeaders, accountID, token)
	// pi deletes accept/content-type/OpenAI-Beta here defensively; the base
	// builder never sets them, so the deletes are no-ops on both runtimes.
	delete(headers, "accept")
	delete(headers, "content-type")
	delete(headers, "OpenAI-Beta")
	delete(headers, "openai-beta")
	headers["OpenAI-Beta"] = codexOpenAIBetaResponsesWebsockets
	headers["x-client-request-id"] = requestID
	headers["session-id"] = requestID
	return headers
}
