// pi: providers/cloudflare-stream.ts — materializes Cloudflare account/
// gateway endpoint placeholders from the resolved provider env before
// dispatch.
package providers

import (
	"context"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

const (
	cloudflareAccountIDEnv = "CLOUDFLARE_ACCOUNT_ID"
	cloudflareGatewayIDEnv = "CLOUDFLARE_GATEWAY_ID"
)

// ResolveCloudflareModel rewrites a model's baseUrl placeholders from the
// provider env, returning the model unchanged when nothing resolves (pi:
// resolveCloudflareModel).
func ResolveCloudflareModel(model *types.Model, env types.ProviderEnv) *types.Model {
	if env == nil {
		return model
	}
	baseURL := model.BaseURL
	if accountID, ok := env[cloudflareAccountIDEnv]; ok {
		baseURL = strings.ReplaceAll(baseURL, "{CLOUDFLARE_ACCOUNT_ID}", accountID)
	}
	if gatewayID, ok := env[cloudflareGatewayIDEnv]; ok {
		baseURL = strings.ReplaceAll(baseURL, "{CLOUDFLARE_GATEWAY_ID}", gatewayID)
	}
	if baseURL == model.BaseURL {
		return model
	}
	resolved := *model
	resolved.BaseURL = baseURL
	return &resolved
}

// CloudflareStreams wraps an API implementation so Cloudflare account/gateway
// endpoint placeholders materialize from the resolved provider env before
// dispatch (pi: cloudflareStreams).
func CloudflareStreams(streams types.ProviderStreams) types.ProviderStreams {
	return &cloudflareStreams{inner: streams}
}

type cloudflareStreams struct {
	inner types.ProviderStreams
}

func (s *cloudflareStreams) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	return s.inner.Stream(ctx, ResolveCloudflareModel(model, envOfStream(options)), llmContext, options)
}

func (s *cloudflareStreams) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	return s.inner.StreamSimple(ctx, ResolveCloudflareModel(model, envOfSimple(options)), llmContext, options)
}

func envOfStream(options *types.StreamOptions) types.ProviderEnv {
	if options == nil {
		return nil
	}
	return options.Env
}

func envOfSimple(options *types.SimpleStreamOptions) types.ProviderEnv {
	if options == nil {
		return nil
	}
	return options.Env
}
