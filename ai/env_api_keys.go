// pi: env-api-keys.ts.
//
// Ported in full on 2026-09-13, triggered by the first live probe key
// (GEMINI_API_KEY): ai.StreamSimple dispatched to the google adapter with
// options.APIKey empty, so every live probe failed with "No API key for
// provider: google" (pi fills the key via compat.ts withEnvApiKey →
// getEnvApiKey before dispatch; see docs/fix/2026-09-13-env-api-keys-fill.md).
package ai

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

const (
	AnthropicAuthTokenEnv  = "ANTHROPIC_AUTH_TOKEN"
	AnthropicOAuthTokenEnv = "ANTHROPIC_OAUTH_TOKEN"
	AnthropicAPIKeyEnv     = "ANTHROPIC_API_KEY"
)

// AmbientAuthMarker is the pseudo API key GetEnvApiKey returns when a provider
// is authenticated through ambient credentials (Vertex ADC, Bedrock IAM).
// withEnvApiKey (compat_registry.go) leaves options untouched for it — those
// adapters resolve ambient auth themselves (pi: AMBIENT_AUTH_MARKER in
// compat.ts; google-vertex's GCP_VERTEX_CREDENTIALS_MARKER matches its value).
const AmbientAuthMarker = "<authenticated>"

// vertexAdcCredentialsExists caches the default ADC path probe (pi:
// cachedVertexAdcCredentialsExists). pi skips caching only in a browser
// environment where fs is never available — Go always has the filesystem, so
// the result caches unconditionally. An explicit GOOGLE_APPLICATION_CREDENTIALS
// path is probed fresh on every call (pi: early return before the cache).
var vertexAdcCredentialsExists struct {
	sync.Mutex
	computed bool
	value    bool
}

// hasVertexAdcCredentials reports whether Vertex Application Default
// Credentials are present: either the GOOGLE_APPLICATION_CREDENTIALS file —
// which pi reads from the scoped env only for this early path — or the
// default ~/.config/gcloud/application_default_credentials.json (pi:
// hasVertexAdcCredentials; the async-import/browser guards have no Go
// counterpart).
func hasVertexAdcCredentials(env types.ProviderEnv) bool {
	if env != nil {
		if explicitCredentialsPath := env["GOOGLE_APPLICATION_CREDENTIALS"]; explicitCredentialsPath != "" {
			_, err := os.Stat(explicitCredentialsPath)
			return err == nil
		}
	}

	vertexAdcCredentialsExists.Lock()
	defer vertexAdcCredentialsExists.Unlock()
	if !vertexAdcCredentialsExists.computed {
		home, err := os.UserHomeDir()
		exists := false
		if err == nil {
			_, err = os.Stat(filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"))
			exists = err == nil
		}
		vertexAdcCredentialsExists.value = exists
		vertexAdcCredentialsExists.computed = true
	}
	return vertexAdcCredentialsExists.value
}

// getApiKeyEnvVars maps a provider to its candidate API key env vars (pi:
// getApiKeyEnvVars). github-copilot and anthropic are special-cased before
// the map, mirroring pi.
func getApiKeyEnvVars(provider types.ProviderID) []string {
	if provider == "github-copilot" {
		return []string{"COPILOT_GITHUB_TOKEN"}
	}

	// ANTHROPIC_AUTH_TOKEN participates in env discovery/status, but
	// GetEnvApiKey skips it because requests must pass it as
	// Authorization: Bearer.
	if provider == "anthropic" {
		return []string{AnthropicAuthTokenEnv, AnthropicOAuthTokenEnv, AnthropicAPIKeyEnv}
	}

	envVar, ok := apiKeyEnvVarsMap[provider]
	if !ok {
		return nil
	}
	return []string{envVar}
}

// apiKeyEnvVarsMap is pi's envMap record; entry order follows pi's source.
var apiKeyEnvVarsMap = map[types.ProviderID]string{
	"ant-ling":                   "ANT_LING_API_KEY",
	"qwen-token-plan":            "QWEN_TOKEN_PLAN_API_KEY",
	"qwen-token-plan-cn":         "QWEN_TOKEN_PLAN_CN_API_KEY",
	"qwen-token-plan-individual": "QWEN_TOKEN_PLAN_API_KEY",
	"openai":                     "OPENAI_API_KEY",
	"azure-openai-responses":     "AZURE_OPENAI_API_KEY",
	"nvidia":                     "NVIDIA_API_KEY",
	"deepseek":                   "DEEPSEEK_API_KEY",
	"google":                     "GEMINI_API_KEY",
	"google-vertex":              "GOOGLE_CLOUD_API_KEY",
	"groq":                       "GROQ_API_KEY",
	"cerebras":                   "CEREBRAS_API_KEY",
	"xai":                        "XAI_API_KEY",
	"radius":                     "RADIUS_API_KEY",
	"openrouter":                 "OPENROUTER_API_KEY",
	"vercel-ai-gateway":          "AI_GATEWAY_API_KEY",
	"zai":                        "ZAI_API_KEY",
	"zai-coding-cn":              "ZAI_CODING_CN_API_KEY",
	"mistral":                    "MISTRAL_API_KEY",
	"minimax":                    "MINIMAX_API_KEY",
	"minimax-cn":                 "MINIMAX_CN_API_KEY",
	"moonshotai":                 "MOONSHOT_API_KEY",
	"moonshotai-cn":              "MOONSHOT_API_KEY",
	"huggingface":                "HF_TOKEN",
	"fireworks":                  "FIREWORKS_API_KEY",
	"together":                   "TOGETHER_API_KEY",
	"baseten":                    "BASETEN_API_KEY",
	"opencode":                   "OPENCODE_API_KEY",
	"opencode-go":                "OPENCODE_API_KEY",
	"kimi-coding":                "KIMI_API_KEY",
	"cloudflare-workers-ai":      "CLOUDFLARE_API_KEY",
	"cloudflare-ai-gateway":      "CLOUDFLARE_API_KEY",
	"xiaomi":                     "XIAOMI_API_KEY",
	"xiaomi-token-plan-cn":       "XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"xiaomi-token-plan-ams":      "XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"xiaomi-token-plan-sgp":      "XIAOMI_TOKEN_PLAN_SGP_API_KEY",
}

// FindEnvKeys reports the configured environment variables that can provide an
// API key for a provider. Only actual API key variables are reported; ambient
// credential sources (AWS profiles/IAM, Google ADC) are intentionally excluded
// (pi: findEnvKeys).
func FindEnvKeys(provider types.ProviderID, env types.ProviderEnv) []string {
	envVars := getApiKeyEnvVars(provider)
	if envVars == nil {
		return nil
	}

	var found []string
	for _, envVar := range envVars {
		if utils.GetProviderEnvValue(envVar, env) != "" {
			found = append(found, envVar)
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// GetEnvApiKey returns the API key for a provider from its well-known
// environment variables, e.g. GEMINI_API_KEY (pi: getEnvApiKey). Providers
// that require OAuth tokens do not resolve here, and ambient-credential
// providers return AmbientAuthMarker instead of a key.
func GetEnvApiKey(provider types.ProviderID, env types.ProviderEnv) string {
	envKeys := FindEnvKeys(provider, env)
	if len(envKeys) > 0 {
		var apiKeyEnv string
		if provider == "anthropic" {
			// ANTHROPIC_AUTH_TOKEN is discoverable but not an API key: skip it
			// in favor of OAUTH_TOKEN/API_KEY. pi's envKeys.find(...) yields
			// undefined when only AUTH_TOKEN is set → no key at all.
			for _, key := range envKeys {
				if key != AnthropicAuthTokenEnv {
					apiKeyEnv = key
					break
				}
			}
		} else {
			apiKeyEnv = envKeys[0]
		}
		if apiKeyEnv != "" {
			return utils.GetProviderEnvValue(apiKeyEnv, env)
		}
	}

	// Vertex AI supports either an explicit API key or Application Default
	// Credentials. Auth is configured via `gcloud auth application-default login`.
	if provider == "google-vertex" {
		hasCredentials := hasVertexAdcCredentials(env)
		hasProject := utils.GetProviderEnvValue("GOOGLE_CLOUD_PROJECT", env) != "" ||
			utils.GetProviderEnvValue("GCLOUD_PROJECT", env) != ""
		hasLocation := utils.GetProviderEnvValue("GOOGLE_CLOUD_LOCATION", env) != ""

		if hasCredentials && hasProject && hasLocation {
			return AmbientAuthMarker
		}
	}

	if provider == "amazon-bedrock" {
		// Amazon Bedrock supports multiple credential sources:
		// 1. AWS_PROFILE - named profile from ~/.aws/credentials
		// 2. AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY - standard IAM keys
		// 3. AWS_BEARER_TOKEN_BEDROCK - Bedrock bearer token
		// 4. AWS_CONTAINER_CREDENTIALS_RELATIVE_URI - ECS task roles
		// 5. AWS_CONTAINER_CREDENTIALS_FULL_URI - ECS task roles (full URI)
		// 6. AWS_WEB_IDENTITY_TOKEN_FILE - IRSA (IAM Roles for Service Accounts)
		if utils.GetProviderEnvValue("AWS_PROFILE", env) != "" ||
			(utils.GetProviderEnvValue("AWS_ACCESS_KEY_ID", env) != "" && utils.GetProviderEnvValue("AWS_SECRET_ACCESS_KEY", env) != "") ||
			utils.GetProviderEnvValue("AWS_BEARER_TOKEN_BEDROCK", env) != "" ||
			utils.GetProviderEnvValue("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", env) != "" ||
			utils.GetProviderEnvValue("AWS_CONTAINER_CREDENTIALS_FULL_URI", env) != "" ||
			utils.GetProviderEnvValue("AWS_WEB_IDENTITY_TOKEN_FILE", env) != "" {
			return AmbientAuthMarker
		}
	}

	return ""
}
