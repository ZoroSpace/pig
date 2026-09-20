// Ported from pi's test/opencode-provider-headers.test.ts (regression for
// pi #9326): the x-opencode-session header is injected for stream and
// streamSimple requests even without cache retention, and case-insensitive
// caller overrides survive.
package providers

import (
	gocontext "context"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func opencodeHeadersTestModel() *types.Model {
	return &types.Model{
		ID:            "test-model",
		Name:          "Test model",
		API:           types.Api("test-api"),
		Provider:      types.ProviderOpenCode,
		BaseURL:       "https://opencode.ai/zen/v1",
		Reasoning:     false,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

func completedOpenCodeStream() *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	message := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("ok")},
		API:        types.Api("test-api"),
		Provider:   "opencode",
		Model:      "test-model",
		Usage:      types.Usage{},
		StopReason: types.StopReasonStop,
		Timestamp:  0,
	}
	stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &message})
	stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &message})
	stream.EndWith(message)
	return stream
}

// recordingOpenCodeStreams captures the options every entry receives.
type recordingOpenCodeStreams struct {
	captured map[string]types.ProviderHeaders
}

func newRecordingOpenCodeStreams() (*recordingOpenCodeStreams, types.ProviderStreams) {
	rec := &recordingOpenCodeStreams{captured: map[string]types.ProviderHeaders{}}
	return rec, rec
}

func (r *recordingOpenCodeStreams) record(name string, options any) {
	switch opts := options.(type) {
	case *types.StreamOptions:
		if opts != nil {
			r.captured[name] = opts.Headers
		}
	case *types.SimpleStreamOptions:
		if opts != nil {
			r.captured[name] = opts.Headers
		}
	}
}

func (r *recordingOpenCodeStreams) Stream(_ gocontext.Context, _ *types.Model, _ *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	r.record("stream", options)
	return completedOpenCodeStream()
}

func (r *recordingOpenCodeStreams) StreamSimple(_ gocontext.Context, _ *types.Model, _ *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	r.record("streamSimple", options)
	return completedOpenCodeStream()
}

func TestOpenCodeSessionHeaderInjectedWithoutCacheRetention(t *testing.T) {
	// Regression for pi #9326.
	rec, inner := newRecordingOpenCodeStreams()
	streams := WithOpenCodeSessionHeader(inner)
	model := opencodeHeadersTestModel()
	transcript := &types.TranscriptContext{Messages: []types.Message{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 0},
	}}

	streams.StreamSimple(gocontext.Background(), model, transcript, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{SessionID: "conversation-1", CacheRetention: types.CacheRetentionNone},
	})
	if got := rec.captured["streamSimple"]["x-opencode-session"]; got == nil || *got != "conversation-1" {
		t.Fatalf("streamSimple headers = %v", rec.captured["streamSimple"])
	}

	streams.Stream(gocontext.Background(), model, transcript, &types.StreamOptions{
		SessionID: "conversation-1", CacheRetention: types.CacheRetentionNone,
	})
	if got := rec.captured["stream"]["x-opencode-session"]; got == nil || *got != "conversation-1" {
		t.Fatalf("stream headers = %v", rec.captured["stream"])
	}
}

func TestOpenCodeSessionHeaderPreservesCaseInsensitiveCallerOverride(t *testing.T) {
	rec, inner := newRecordingOpenCodeStreams()
	streams := WithOpenCodeSessionHeader(inner)
	model := opencodeHeadersTestModel()
	transcript := &types.TranscriptContext{Messages: []types.Message{}}

	streams.StreamSimple(gocontext.Background(), model, transcript, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			SessionID: "generated-value",
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: types.ProviderHeaders{"X-OpenCode-Session": lo.ToPtr("caller-value")},
			},
		},
	})
	if got := rec.captured["streamSimple"]["X-OpenCode-Session"]; got == nil || *got != "caller-value" {
		t.Fatalf("caller value should win: %v", rec.captured["streamSimple"])
	}
	if len(rec.captured["streamSimple"]) != 1 {
		t.Fatalf("no injected header should be added: %v", rec.captured["streamSimple"])
	}

	// A nil header value suppresses the default entirely.
	streams.StreamSimple(gocontext.Background(), model, transcript, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			SessionID: "generated-value",
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: types.ProviderHeaders{"X-OpenCode-Session": nil},
			},
		},
	})
	value, present := rec.captured["streamSimple"]["X-OpenCode-Session"]
	if !present || value != nil {
		t.Fatalf("nil override should suppress: %v", rec.captured["streamSimple"])
	}
}

func TestOpenCodeSessionHeaderNotFabricatedWithoutSessionID(t *testing.T) {
	rec, inner := newRecordingOpenCodeStreams()
	streams := WithOpenCodeSessionHeader(inner)
	model := opencodeHeadersTestModel()
	transcript := &types.TranscriptContext{Messages: []types.Message{}}

	streams.StreamSimple(gocontext.Background(), model, transcript, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: types.ProviderHeaders{"x-custom": lo.ToPtr("value")},
			},
		},
	})
	headers := rec.captured["streamSimple"]
	if len(headers) != 1 || headers["x-custom"] == nil || *headers["x-custom"] != "value" {
		t.Fatalf("headers = %v", headers)
	}
}
