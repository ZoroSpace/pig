// pi: src/compat.ts — the api-provider registry (registerApiProvider /
// getApiProvider / getApiProviders / unregisterApiProviders / clearApiProviders
// / registerBuiltInApiProviders).
//
// Segment deviations (declared):
//   - pi's ApiProviderInternal wraps stream/streamSimple with model.api
//     mismatch checks; pig's composer dispatches by looking the api up
//     directly, so the registry stores ProviderStreams verbatim.
//   - pi registers its builtin API implementations at compat.ts module load;
//     pig's builtin adapters self-register via package init() in ai/api (the
//     layering rule makes ai unable to import ai/api). Only implemented
//     adapters register: openai-completions (Phase 2 decision); the others
//     (anthropic-messages, openai-responses family, google, mistral, bedrock,
//     pi-messages) register as they are ported.
package ai

import (
	"context"
	"fmt"
	"sync"

	"github.com/ZoroSpace/pig/ai/types"
)

type registeredApiProvider struct {
	streams  ProviderStreams
	sourceID string
}

var apiProviderRegistry = struct {
	sync.Mutex
	entries map[Api]registeredApiProvider
}{entries: map[Api]registeredApiProvider{}}

// RegisterApiProvider adds one API implementation, optionally tagged with a
// source id for grouped removal (pi: registerApiProvider).
func RegisterApiProvider(api Api, streams ProviderStreams, sourceID string) {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	apiProviderRegistry.entries[api] = registeredApiProvider{streams: streams, sourceID: sourceID}
}

// GetApiProvider returns the registered implementation for api (pi:
// getApiProvider).
func GetApiProvider(api Api) (ProviderStreams, bool) {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	entry, ok := apiProviderRegistry.entries[api]
	if !ok {
		return nil, false
	}
	return entry.streams, true
}

// GetApiProviders lists all registered implementations (pi: getApiProviders).
func GetApiProviders() map[Api]ProviderStreams {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	out := make(map[Api]ProviderStreams, len(apiProviderRegistry.entries))
	for api, entry := range apiProviderRegistry.entries {
		out[api] = entry.streams
	}
	return out
}

// UnregisterApiProviders removes every registration tagged with sourceID (pi:
// unregisterApiProviders).
func UnregisterApiProviders(sourceID string) {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	for api, entry := range apiProviderRegistry.entries {
		if entry.sourceID != "" && entry.sourceID == sourceID {
			delete(apiProviderRegistry.entries, api)
		}
	}
}

// ClearApiProviders removes every registration, including builtins (pi:
// clearApiProviders).
func ClearApiProviders() {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	apiProviderRegistry.entries = map[Api]registeredApiProvider{}
}

// RegisterBuiltInApiProvider registers a builtin API implementation without
// clobbering existing entries: compat may load after a test or extension has
// already registered an override for the builtin api id (pi:
// registerBuiltInApiProviders loop body).
func RegisterBuiltInApiProvider(api Api, streams ProviderStreams) {
	apiProviderRegistry.Lock()
	defer apiProviderRegistry.Unlock()
	if _, exists := apiProviderRegistry.entries[api]; exists {
		return
	}
	apiProviderRegistry.entries[api] = registeredApiProvider{streams: streams}
}

// builtinApiRegistrar is the hook ai/api sets at init time so ResetApiProviders
// can re-run the builtin registration after a clear (pi: compat.ts owns both
// clearApiProviders and registerBuiltInApiProviders; pig's builtin registration
// lives in ai/api because of the layering rule, see the file header).
var builtinApiRegistrar func()

// SetBuiltinApiRegistrar records the builtin registration hook. Called by
// adapter packages (ai/api) at init time. Multiple adapters each register
// their own hook: calls chain (all hooks run on ResetApiProviders) because
// pi's compat.ts owns a single registerBuiltInApiProviders loop over every
// builtin implementation, which a single overwrite-based hook cannot express.
func SetBuiltinApiRegistrar(fn func()) {
	if builtinApiRegistrar != nil {
		prev := builtinApiRegistrar
		added := fn
		fn = func() { prev(); added() }
	}
	builtinApiRegistrar = fn
}

// ResetApiProviders clears every registration (including extensions') and
// re-registers the builtin adapters (pi: resetApiProviders).
func ResetApiProviders() {
	ClearApiProviders()
	if builtinApiRegistrar != nil {
		builtinApiRegistrar()
	}
}

// StreamSimple streams one simple request through the registered api provider
// for the model (pi: compat.ts streamSimple; used as the default agent stream
// function). Segment deviations (declared in the core/compaction batch):
// pi's getBuiltinProviderForModel short-circuit (cloudflare-legacy auth shape
// and withEnvApiKey env fallback) arrives with the env-api-keys batch — pig's
// builtin adapters are already self-registered into the same registry.
func StreamSimple(ctx context.Context, model *Model, llmContext *Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	provider, ok := GetApiProvider(model.API)
	if !ok {
		stream := NewAssistantMessageEventStream()
		message := types.AssistantMessage{
			Role:         types.RoleAssistant,
			Content:      []types.Content{},
			API:          model.API,
			Provider:     model.Provider,
			Model:        model.ID,
			StopReason:   types.StopReasonError,
			ErrorMessage: fmt.Sprintf("No API provider registered for api: %s", model.API),
		}
		stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &message})
		stream.EndWith(message)
		return stream
	}
	return provider.StreamSimple(ctx, model, llmContext, options)
}
