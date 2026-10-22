package ai

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

type identityCaptureStreams struct {
	last *SimpleStreamOptions
}

func (s *identityCaptureStreams) Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	return s.StreamSimple(ctx, model, llmContext, &SimpleStreamOptions{StreamOptions: *options})
}

func (s *identityCaptureStreams) StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	s.last = options
	stream := NewAssistantMessageEventStream()
	message := &AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{NewTextContent("ok")},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		StopReason: StopReasonStop,
		Timestamp:  1,
	}
	stream.Push(AssistantMessageEvent{Type: EventDone, Message: message})
	return stream
}

func identityTestModels(t *testing.T, defaultIdentity *types.ClientIdentity, providerID string, baseURL string) (MutableModels, *identityCaptureStreams, *Model) {
	t.Helper()
	model := Model{ID: "identity-model", API: ApiOpenAICompletions, Provider: types.ProviderID(providerID), BaseURL: baseURL, Input: []ModelInput{ModelInputText}}
	streams := &identityCaptureStreams{}
	provider := &stubProvider{
		id:      providerID,
		streams: streams,
		models:  []Model{model},
		auth:    auth.ProviderAuth{APIKey: &stubAuth{key: "key"}},
		baseURL: baseURL,
	}
	models := NewModels(&CreateModelsOptions{Identity: defaultIdentity})
	models.SetProvider(provider)
	return models, streams, &model
}

func TestModelsIdentityMergeAndOpenCodeSession(t *testing.T) {
	models, streams, model := identityTestModels(t, &types.ClientIdentity{
		UserAgent: types.UserAgentShort(),
		Version:   "models-version",
	}, "opencode", "https://opencode.ai/zen/v1")

	options := &ModelsSimpleStreamOptions{
		SimpleStreamOptions: SimpleStreamOptions{
			StreamOptions: StreamOptions{
				ProviderRequestOptions: ProviderRequestOptions{
					Identity: &types.ClientIdentity{Version: "request-version"},
				},
				SessionID: "session-1",
			},
		},
	}
	models.StreamSimple(context.Background(), model, &Context{Messages: []Message{}}, options).Result()
	if streams.last == nil || streams.last.Identity == nil {
		t.Fatal("identity was not forwarded")
	}
	identity := streams.last.Identity
	if identity.UserAgent == nil || !identity.UserAgent.Short {
		t.Fatalf("userAgent = %+v, want models short mode", identity.UserAgent)
	}
	if identity.Version != "request-version" {
		t.Fatalf("version = %q, want request override", identity.Version)
	}
	if value := streams.last.Headers["x-opencode-session"]; value == nil || *value != "session-1" {
		t.Fatalf("x-opencode-session = %v", value)
	}
}

func TestWithClientIdentity(t *testing.T) {
	models, streams, model := identityTestModels(t, nil, "openrouter", "https://openrouter.ai/api/v1")
	wrapped := WithClientIdentity(models, &types.ClientIdentity{
		UserAgent:   types.UserAgentShort(),
		Attribution: &types.AttributionSetting{Attribution: &types.PIAttribution},
	})
	wrapped.StreamSimple(context.Background(), model, &Context{Messages: []Message{}}, &ModelsSimpleStreamOptions{}).Result()
	if streams.last.Identity == nil || streams.last.Identity.Attribution == nil {
		t.Fatalf("wrapped identity = %+v", streams.last.Identity)
	}
	if got := streams.last.Identity.Attribution.Attribution; got == nil || got.Name != "pi" {
		t.Fatalf("wrapped attribution = %+v", got)
	}

	// Request identity overrides field by field, including an explicit null
	// attribution.
	wrapped.StreamSimple(context.Background(), model, &Context{Messages: []Message{}}, &ModelsSimpleStreamOptions{
		SimpleStreamOptions: SimpleStreamOptions{StreamOptions: StreamOptions{ProviderRequestOptions: ProviderRequestOptions{
			Identity: &types.ClientIdentity{Attribution: &types.AttributionSetting{}},
		}}},
	}).Result()
	if streams.last.Identity.Attribution == nil || streams.last.Identity.Attribution.Attribution != nil {
		t.Fatalf("request attribution = %+v, want explicit null", streams.last.Identity.Attribution)
	}
	if streams.last.Identity.UserAgent == nil || !streams.last.Identity.UserAgent.Short {
		t.Fatalf("userAgent = %+v, want wrapper value", streams.last.Identity.UserAgent)
	}

	mutable, ok := wrapped.(MutableModels)
	if !ok {
		t.Fatal("wrapping a MutableModels must stay mutable")
	}
	mutable.DeleteProvider("openrouter")
	if models.GetProvider("openrouter") != nil {
		t.Fatal("deleteProvider did not delegate")
	}
}
