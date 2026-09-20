// pi: providers/opencode-headers.ts (regression #9326) — adds OpenCode's
// required per-conversation routing header before API dispatch.
package providers

import (
	"context"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

const opencodeSessionHeader = "x-opencode-session"

func hasOpenCodeHeader(headers types.ProviderHeaders, name string) bool {
	expected := strings.ToLower(name)
	for key := range headers {
		if strings.ToLower(key) == expected {
			return true
		}
	}
	return false
}

// addSessionHeader clones options.Headers with the session header added.
func addSessionHeader(headers types.ProviderHeaders, sessionID string) types.ProviderHeaders {
	cloned := types.ProviderHeaders{}
	for key, value := range headers {
		cloned[key] = value
	}
	cloned[opencodeSessionHeader] = lo.ToPtr(sessionID)
	return cloned
}

// wrapStreamOptions returns a copy of options with the session header added
// when a SessionID is present and the caller did not already set one
// (case-insensitive); the caller's struct is never mutated (pi spreads the
// options object).
func wrapStreamOptions(options *types.StreamOptions) *types.StreamOptions {
	if options.SessionID == "" || hasOpenCodeHeader(options.Headers, opencodeSessionHeader) {
		return options
	}
	next := *options
	next.Headers = addSessionHeader(options.Headers, options.SessionID)
	return &next
}

func wrapSimpleStreamOptions(options *types.SimpleStreamOptions) *types.SimpleStreamOptions {
	if options.SessionID == "" || hasOpenCodeHeader(options.Headers, opencodeSessionHeader) {
		return options
	}
	next := *options
	next.Headers = addSessionHeader(options.Headers, options.SessionID)
	return &next
}

// openCodeSessionStreams wraps both stream entries of a ProviderStreams pair
// with the session-header injection. Each wrapper copies the whole options
// struct so every field survives (pi spreads the options object).
type openCodeSessionStreams struct {
	inner types.ProviderStreams
}

func (s openCodeSessionStreams) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	return s.inner.Stream(ctx, model, llmContext, wrapStreamOptions(options))
}

func (s openCodeSessionStreams) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	return s.inner.StreamSimple(ctx, model, llmContext, wrapSimpleStreamOptions(options))
}

// WithOpenCodeSessionHeader wraps a ProviderStreams pair so every request
// carries x-opencode-session by default (pi: withOpenCodeSessionHeader).
func WithOpenCodeSessionHeader(streams types.ProviderStreams) types.ProviderStreams {
	return openCodeSessionStreams{inner: streams}
}
