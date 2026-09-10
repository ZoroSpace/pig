// pi: env-api-keys.ts.
package ai

const (
	AnthropicAuthTokenEnv  = "ANTHROPIC_AUTH_TOKEN"
	AnthropicOAuthTokenEnv = "ANTHROPIC_OAUTH_TOKEN"
	AnthropicAPIKeyEnv     = "ANTHROPIC_API_KEY"
)

// The rest of pi env-api-keys.ts is missing here by design: getApiKeyEnvVars
// (per-provider env map), findEnvKeys, getEnvApiKey, and the Vertex ADC /
// Bedrock ambient-credential checks arrive with their consumers (provider
// auth status in coding-agent) as the provider factories land. Semantics to
// preserve then: ANTHROPIC_AUTH_TOKEN participates in env discovery/status,
// but getEnvApiKey skips it because requests must pass it as
// Authorization: Bearer.
