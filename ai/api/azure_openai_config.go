// pi: api/azure-openai-config.ts — Azure endpoint/deployment resolution
// shared by the azure provider's two dispatch paths (azure-openai-responses
// and openai-completions). Lives in package api so the providers package can
// consume it without importing the AzureOpenAI SDK surface (pi's reason for
// the extraction, v1.0.3 #9645).
package api

import (
	"errors"
	"net/url"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// defaultAzureAPIVersion is pi's DEFAULT_AZURE_API_VERSION.
const defaultAzureAPIVersion = "v1"

// AzureEndpointOptions are the Azure endpoint options shared by both Azure
// dispatch paths (pi: AzureEndpointOptions extends StreamOptions). Azure
// models ship without a baseUrl: one resource per user, resolved per request.
type AzureEndpointOptions struct {
	types.StreamOptions
	// AzureAPIVersion overrides the api-version query param. Default: env
	// AZURE_OPENAI_API_VERSION, else "v1".
	AzureAPIVersion string
	// AzureResourceName builds https://<name>.openai.azure.com/openai/v1
	// when no explicit base URL is available.
	AzureResourceName string
	// AzureBaseURL overrides the model's base URL.
	AzureBaseURL string
	// AzureDeploymentName overrides the deployment name (default: model id,
	// or the AZURE_OPENAI_DEPLOYMENT_NAME_MAP entry).
	AzureDeploymentName string
}

// parseDeploymentNameMap parses the AZURE_OPENAI_DEPLOYMENT_NAME_MAP value
// ("modelId=deployment,..." pairs) (pi: parseDeploymentNameMap).
func parseDeploymentNameMap(value string) map[string]string {
	out := map[string]string{}
	if value == "" {
		return out
	}
	for _, entry := range strings.Split(value, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out
}

// ResolveDeploymentName resolves the Azure deployment for one model (pi:
// resolveDeploymentName).
func ResolveDeploymentName(model *types.Model, options *AzureEndpointOptions) string {
	if options != nil && options.AzureDeploymentName != "" {
		return options.AzureDeploymentName
	}
	envValue := ""
	if options != nil {
		envValue = utils.GetProviderEnvValue("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", options.Env)
	}
	if mapped, ok := parseDeploymentNameMap(envValue)[model.ID]; ok {
		return mapped
	}
	return model.ID
}

// normalizeAzureBaseURL validates and normalizes the Azure OpenAI base URL
// (pi: normalizeAzureBaseUrl): Azure hosts get the /openai/v1 base path so
// the deployment/api-version conventions resolve correctly.
func normalizeAzureBaseURL(baseURL string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("Invalid Azure OpenAI base URL: " + baseURL)
	}

	host := parsed.Hostname()
	isAzureHost := strings.HasSuffix(host, ".openai.azure.com") ||
		strings.HasSuffix(host, ".cognitiveservices.azure.com") ||
		strings.HasSuffix(host, ".ai.azure.com")
	normalizedPath := strings.TrimRight(parsed.Path, "/")

	// Ensure Azure hosts have /openai/v1 as base path so the AzureOpenAI SDK
	// can append /deployments/<model>/... and ?api-version=v1 correctly.
	if isAzureHost {
		switch normalizedPath {
		case "", "/", "/openai", "/openai/v1/responses":
			parsed.Path = "/openai/v1"
			parsed.RawQuery = ""
		}
	}

	return strings.TrimRight(parsed.String(), "/"), nil
}

// buildAzureDefaultBaseURL builds the resource-name base URL (pi:
// buildDefaultBaseUrl).
func buildAzureDefaultBaseURL(resourceName string) string {
	return "https://" + resourceName + ".openai.azure.com/openai/v1"
}

// ResolveAzureBaseURL resolves the Azure endpoint for one model (pi:
// resolveAzureBaseUrl). Precedence: options, env, model.baseUrl; the model's
// base URL is the last fallback.
func ResolveAzureBaseURL(model *types.Model, options *AzureEndpointOptions) (string, error) {
	env := types.ProviderEnv(nil)
	if options != nil {
		env = options.Env
	}
	baseURL := ""
	if options != nil {
		baseURL = strings.TrimSpace(options.AzureBaseURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(utils.GetProviderEnvValue("AZURE_OPENAI_BASE_URL", env))
	}
	resourceName := ""
	if options != nil && options.AzureResourceName != "" {
		resourceName = options.AzureResourceName
	} else {
		resourceName = utils.GetProviderEnvValue("AZURE_OPENAI_RESOURCE_NAME", env)
	}

	resolvedBaseURL := baseURL
	if resolvedBaseURL == "" && resourceName != "" {
		resolvedBaseURL = buildAzureDefaultBaseURL(resourceName)
	}
	if resolvedBaseURL == "" {
		resolvedBaseURL = model.BaseURL
	}
	if resolvedBaseURL == "" {
		return "", errors.New(
			"Azure OpenAI base URL is required. Set AZURE_OPENAI_BASE_URL or AZURE_OPENAI_RESOURCE_NAME, or pass azureBaseUrl, azureResourceName, or model.baseUrl.")
	}

	return normalizeAzureBaseURL(resolvedBaseURL)
}

// resolvedAzureConfig is the resolved base URL + api-version pair (pi:
// resolveAzureConfig's return).
type resolvedAzureConfig struct {
	baseURL    string
	apiVersion string
}

// resolveAzureConfig resolves the base URL and api-version (pi:
// resolveAzureConfig).
func resolveAzureConfig(model *types.Model, options *AzureEndpointOptions) (*resolvedAzureConfig, error) {
	env := types.ProviderEnv(nil)
	if options != nil {
		env = options.Env
	}
	apiVersion := ""
	if options != nil {
		apiVersion = options.AzureAPIVersion
	}
	if apiVersion == "" {
		apiVersion = utils.GetProviderEnvValue("AZURE_OPENAI_API_VERSION", env)
	}
	if apiVersion == "" {
		apiVersion = defaultAzureAPIVersion
	}

	baseURL, err := ResolveAzureBaseURL(model, options)
	if err != nil {
		return nil, err
	}
	return &resolvedAzureConfig{baseURL: baseURL, apiVersion: apiVersion}, nil
}
