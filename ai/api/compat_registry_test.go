// Regression tests for the builtin API registrar hook: SetBuiltinApiRegistrar
// must chain hooks across adapter inits (pi: compat.ts's single
// registerBuiltInApiProviders loop registers every builtin implementation on
// reset; a single overwrite-based hook silently dropped all but the
// last-registered adapter — the openai-codex adapter was the fifth registrant
// and exposed the bug).
package api

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func TestResetApiProvidersReRegistersAllBuiltins(t *testing.T) {
	// After a clear + reset, every builtin api adapter must be present again.
	ai.ResetApiProviders()

	for _, api := range []types.Api{
		types.ApiOpenAICompletions,
		types.ApiAnthropicMessages,
		types.ApiOpenAIResponses,
		types.ApiAzureOpenAIResponses,
		types.ApiOpenAICodexResponses,
	} {
		if _, ok := ai.GetApiProvider(api); !ok {
			t.Fatalf("api %q missing after ResetApiProviders", api)
		}
	}
}

func TestResetApiProvidersRestoresAfterOverride(t *testing.T) {
	// An extension-style override for a builtin api id is not clobbered by
	// RegisterBuiltInApiProvider, and ResetApiProviders restores the builtin.
	ai.ResetApiProviders()

	ai.RegisterApiProvider(types.ApiOpenAICodexResponses, stubCodexStreams{}, "test-stub")
	if _, ok := ai.GetApiProvider(types.ApiOpenAICodexResponses); !ok {
		t.Fatal("override missing")
	}

	ai.ResetApiProviders()
	streams, ok := ai.GetApiProvider(types.ApiOpenAICodexResponses)
	if !ok {
		t.Fatal("builtin missing after reset")
	}
	if _, isStub := streams.(stubCodexStreams); isStub {
		t.Fatal("override survived ResetApiProviders")
	}
}

// stubCodexStreams is a ProviderStreams test double.
type stubCodexStreams struct{}

func (stubCodexStreams) Stream(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.StreamOptions) *types.AssistantMessageEventStream {
	return types.NewAssistantMessageEventStream()
}

func (stubCodexStreams) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.Context, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	return types.NewAssistantMessageEventStream()
}

var _ types.ProviderStreams = stubCodexStreams{}
