// pi: api/cloudflare.ts — Cloudflare endpoint URL constants. The
// {CLOUDFLARE_ACCOUNT_ID} / {CLOUDFLARE_GATEWAY_ID} placeholders materialize
// from the resolved provider env (pi: providers/cloudflare-stream.ts
// resolveCloudflareModel, ported in ai/providers/cloudflare_stream.go).
package api

const (
	// CloudflareWorkersAIBaseURL is the Workers AI direct endpoint.
	CloudflareWorkersAIBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"

	// CloudflareAIGatewayCompatBaseURL is the AI Gateway Unified API.
	// https://developers.cloudflare.com/ai-gateway/usage/unified-api/
	CloudflareAIGatewayCompatBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"

	// CloudflareAIGatewayOpenAIBaseURL is the AI Gateway → OpenAI passthrough.
	// Used until /compat supports /v1/responses.
	CloudflareAIGatewayOpenAIBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"

	// CloudflareAIGatewayAnthropicBaseURL is the AI Gateway → Anthropic
	// passthrough.
	CloudflareAIGatewayAnthropicBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)
