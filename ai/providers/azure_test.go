// pi: test/azure-openai-completions.test.ts (v1.0.3 #9645) — the azure
// provider's Chat Completions dispatch: DeepSeek V4 Pro over Azure Foundry,
// endpoint/deployment resolution, and the api map routing.
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// azureCompletionsSSE is the minimal chat.completions stream fixture (pi: the
// FakeOpenAI chat.completions.create async iterator).
const azureCompletionsSSE = "" +
	"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-v4-pro\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
	"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-v4-pro\",\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
	"data: [DONE]\n\n"

// azureCapture records the dispatched request path and payload (pi:
// mockState.lastParams / lastClientOptions / dispatchedTo).
type azureCapture struct {
	path string
	body map[string]any
}

// newAzureMockServer serves the completions SSE fixture on every request and
// captures the request path + body.
func newAzureMockServer(t *testing.T) (*httptest.Server, *azureCapture) {
	t.Helper()
	capture := &azureCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capture.body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, azureCompletionsSSE)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

// azureTestModel fetches one catalog model of the azure provider (pi:
// getModel("azure", ...)).
func azureTestModel(t *testing.T, id string) *types.Model {
	t.Helper()
	models, ok := ai.MODELS[ai.ProviderAzure]
	if !ok {
		t.Fatal("azure not in catalog")
	}
	model, ok := models[id]
	if !ok {
		t.Fatalf("model %q not in azure catalog", id)
	}
	return &model
}

// azureSimpleOptions builds SimpleStreamOptions with the mock endpoint (pi:
// { apiKey: "test-key", ... } plus the test's AZURE_OPENAI_BASE_URL env).
func azureSimpleOptions(srvURL string, env map[string]string) *types.SimpleStreamOptions {
	opts := &types.SimpleStreamOptions{}
	opts.APIKey = "test-key"
	opts.Env = types.ProviderEnv{"AZURE_OPENAI_BASE_URL": srvURL}
	for k, v := range env {
		opts.Env[k] = v
	}
	return opts
}

// runAzureSimple streams one simple request and returns the final message
// (pi: azure.streamSimple(...).result()).
func runAzureSimple(t *testing.T, provider ai.Provider, model *types.Model, llmContext *types.TranscriptContext, opts *types.SimpleStreamOptions) *types.AssistantMessage {
	t.Helper()
	result := provider.StreamSimple(context.Background(), model, llmContext, opts).Result()
	return &result
}

func TestAzureDeepSeekV4ProOverChatCompletions(t *testing.T) {
	provider := AzureProvider()

	t.Run("turns thinking on with reasoning_effort instead of DeepSeek's thinking field", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		transcript := utils.NormalizeContext(&types.Context{SystemPrompt: "sys"})
		opts := azureSimpleOptions(srv.URL, nil)
		opts.Reasoning = types.ThinkingLevelHigh
		runAzureSimple(t, provider, model, transcript, opts)
		if capture.path != "/chat/completions" {
			t.Fatalf("dispatched to %q, want /chat/completions", capture.path)
		}
		if got, _ := capture.body["reasoning_effort"].(string); got != "high" {
			t.Fatalf("reasoning_effort = %v, want high", capture.body["reasoning_effort"])
		}
		if _, present := capture.body["thinking"]; present {
			t.Fatalf("thinking param sent: %v", capture.body["thinking"])
		}
		// The deployment discards a `developer` system message once
		// reasoning_effort is set, without billing it, so the system prompt
		// has to go out under the system role (pi: "sends the system prompt
		// under the system role").
		messages, _ := capture.body["messages"].([]any)
		if len(messages) == 0 {
			t.Fatal("no messages in payload")
		}
		first, _ := messages[0].(map[string]any)
		if first["role"] != "system" {
			t.Fatalf("first message role = %v, want system", first["role"])
		}
	})

	t.Run("clamps thinking levels the deployment does not accept", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions(srv.URL, nil)
		opts.Reasoning = types.ThinkingLevelMax
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if got, _ := capture.body["reasoning_effort"].(string); got != "high" {
			t.Fatalf("reasoning_effort = %v, want high", capture.body["reasoning_effort"])
		}
	})

	t.Run("sends no reasoning_effort when no thinking level is requested", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}),
			azureSimpleOptions(srv.URL, nil))
		if _, present := capture.body["reasoning_effort"]; present {
			t.Fatalf("reasoning_effort sent: %v", capture.body["reasoning_effort"])
		}
		if _, present := capture.body["thinking"]; present {
			t.Fatalf("thinking param sent: %v", capture.body["thinking"])
		}
	})

	t.Run("omits prompt cache parameters when long retention comes from PI_CACHE_RETENTION", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions(srv.URL, map[string]string{"PI_CACHE_RETENTION": "long"})
		opts.SessionID = "session-env"
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if _, present := capture.body["prompt_cache_key"]; present {
			t.Fatalf("prompt_cache_key sent: %v", capture.body["prompt_cache_key"])
		}
		if _, present := capture.body["prompt_cache_retention"]; present {
			t.Fatalf("prompt_cache_retention sent: %v", capture.body["prompt_cache_retention"])
		}
	})

	t.Run("omits prompt cache parameters even when long retention is requested", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions(srv.URL, nil)
		opts.CacheRetention = types.CacheRetentionLong
		opts.SessionID = "session-1"
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if _, present := capture.body["prompt_cache_key"]; present {
			t.Fatalf("prompt_cache_key sent: %v", capture.body["prompt_cache_key"])
		}
		if _, present := capture.body["prompt_cache_retention"]; present {
			t.Fatalf("prompt_cache_retention sent: %v", capture.body["prompt_cache_retention"])
		}
	})

	t.Run("keeps mid-conversation system messages in place", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		// The deployment honours system messages sent mid-conversation, so
		// the transcript must not collapse them (pi: same fixture).
		transcript := utils.NormalizeContext(&types.Context{
			SystemPrompt: "first",
			Messages: []types.Message{
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hi")}}, Timestamp: time.Now().UnixMilli()},
				types.SystemMessage{Role: types.RoleSystem, Content: types.SystemContent{String: "second"}, Timestamp: time.Now().UnixMilli()},
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("again")}}, Timestamp: time.Now().UnixMilli()},
			},
		})
		runAzureSimple(t, provider, model, transcript, azureSimpleOptions(srv.URL, nil))
		messages, _ := capture.body["messages"].([]any)
		var roles []string
		for _, m := range messages {
			if mm, ok := m.(map[string]any); ok {
				roles = append(roles, mm["role"].(string))
			}
		}
		want := "[system user system user]"
		if got := strings.Join(roles, " "); got != strings.Join([]string{"system", "user", "system", "user"}, " ") {
			t.Fatalf("roles = %v, want %s", got, want)
		}
	})

	t.Run("replays reasoning_content on assistant turns so the cached prefix is unchanged", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		assistant := types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.Content{
				types.ThinkingContent{Kind: types.KindThinking, Thinking: "internal reasoning", ThinkingSignature: "reasoning_content"},
				types.TextContent{Kind: types.KindText, Text: "answer"},
			},
			Provider:   types.ProviderAzure,
			API:        types.ApiOpenAICompletions,
			Model:      "deepseek-v4-pro",
			Timestamp:  time.Now().UnixMilli(),
			Usage:      types.Usage{},
			StopReason: types.StopReasonStop,
		}
		transcript := utils.NormalizeContext(&types.Context{
			SystemPrompt: "sys",
			Messages: []types.Message{
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("first")}}, Timestamp: time.Now().UnixMilli()},
				assistant,
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("second")}}, Timestamp: time.Now().UnixMilli()},
			},
		})
		runAzureSimple(t, provider, model, transcript, azureSimpleOptions(srv.URL, nil))
		messages, _ := capture.body["messages"].([]any)
		var reasoningContent any
		for _, m := range messages {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "assistant" {
				reasoningContent = mm["reasoning_content"]
			}
		}
		if reasoningContent != "internal reasoning" {
			t.Fatalf("reasoning_content = %v, want internal reasoning", reasoningContent)
		}
	})
}

func TestAzureChatCompletionsEndpointResolution(t *testing.T) {
	provider := AzureProvider()

	t.Run("surfaces an unconfigured endpoint as an error event rather than throwing out of stream", func(t *testing.T) {
		_, capture := newAzureMockServer(t)
		_ = capture
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions("", nil)
		opts.Env = types.ProviderEnv{}
		result := runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if result.StopReason != types.StopReasonError {
			t.Fatalf("stopReason = %v, want error", result.StopReason)
		}
		if !strings.Contains(result.ErrorMessage, "Azure OpenAI base URL is required") {
			t.Fatalf("errorMessage = %q", result.ErrorMessage)
		}
	})

	// The id is persisted on the assistant message and read back by name, so
	// it has to stay a catalog id.
	t.Run("sends the model id as the request model", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		result := runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}),
			azureSimpleOptions(srv.URL, nil))
		if got, _ := capture.body["model"].(string); got != "deepseek-v4-pro" {
			t.Fatalf("request model = %v, want deepseek-v4-pro", capture.body["model"])
		}
		if result.Model != "deepseek-v4-pro" {
			t.Fatalf("result model = %q", result.Model)
		}
	})

	t.Run("sends the mapped deployment name while keeping the catalog id on the message", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions(srv.URL, map[string]string{
			"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "deepseek-v4-pro=my-deepseek",
		})
		result := runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if got, _ := capture.body["model"].(string); got != "my-deepseek" {
			t.Fatalf("request model = %v, want my-deepseek", capture.body["model"])
		}
		if result.Model != "deepseek-v4-pro" {
			t.Fatalf("result model = %q", result.Model)
		}
	})

	t.Run("passes the deployment name through a caller's onPayload", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		opts := azureSimpleOptions(srv.URL, map[string]string{
			"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "deepseek-v4-pro=my-deepseek",
		})
		seenModel := ""
		opts.OnPayload = func(payload any, model *types.Model) any {
			if params, ok := payload.(map[string]any); ok {
				seenModel, _ = params["model"].(string)
				params["temperature"] = 0.1
				return params
			}
			return nil
		}
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}), opts)
		if seenModel != "my-deepseek" {
			t.Fatalf("onPayload saw model = %q, want my-deepseek", seenModel)
		}
		if got, _ := capture.body["model"].(string); got != "my-deepseek" {
			t.Fatalf("request model = %v, want my-deepseek", capture.body["model"])
		}
		if got, _ := capture.body["temperature"].(float64); got != 0.1 {
			t.Fatalf("temperature = %v, want 0.1", capture.body["temperature"])
		}
	})
}

func TestAzureAPIMapRouting(t *testing.T) {
	provider := AzureProvider()

	t.Run("still routes Responses models to the Responses api", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "gpt-4o-mini")
		// The responses SSE is not the completions fixture, so the stream
		// errors after dispatch; only the route is asserted (pi:
		// dispatchedTo).
		result := runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}),
			azureSimpleOptions(srv.URL, nil))
		_ = result
		if capture.path != "/responses" {
			t.Fatalf("dispatched to %q, want /responses", capture.path)
		}
	})

	t.Run("routes openai-completions models to chat completions", func(t *testing.T) {
		srv, capture := newAzureMockServer(t)
		model := azureTestModel(t, "deepseek-v4-pro")
		runAzureSimple(t, provider, model, utils.NormalizeContext(&types.Context{}),
			azureSimpleOptions(srv.URL, nil))
		if capture.path != "/chat/completions" {
			t.Fatalf("dispatched to %q, want /chat/completions", capture.path)
		}
	})
}
