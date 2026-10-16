// pi: providers/openrouter.ts.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

const (
	openRouterBaseURL              = "https://openrouter.ai/api/v1"
	openRouterUserModelsTimeoutMs  = 15_000
	openRouterUserModelsMaxBodyLen = 4 << 20
)

// openRouterFetch is the transport for GET /models/user; tests replace it (pi:
// the global fetch the test stubs).
var openRouterFetch ai.FetchFunction = func(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

// OpenRouterProvider is the built-in OpenRouter provider (pi:
// openrouterProvider).
func OpenRouterProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "openrouter",
		Name:    "OpenRouter",
		BaseURL: openRouterBaseURL,
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenRouter API key", []string{"OPENROUTER_API_KEY"}),
			// pi: lazyOAuth({ name: "OpenRouter OAuth", loginLabel:
			// "Sign in with OpenRouter", load: loadOpenRouterOAuth }) —
			// filled in by S10-2 (Go flows are statically linked).
			OAuth: auth.OpenRouterOAuth{},
		},
		// pi: models = OPENROUTER_MODELS + OPENROUTER_IMAGE_MODELS +
		// OPENROUTER_CLASSIFIER_MODELS — the #9948 mixed catalog; chat-only
		// narrowing happens at Models.GetModels (pi: getModels filter).
		Models: append(append(GetBuiltinModels(ai.ProviderOpenRouter),
			GetBuiltinImageModels(ai.ProviderOpenRouter)...),
			GetBuiltinClassifierModels(ai.ProviderOpenRouter)...),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}

// PersonalizeOpenRouterCatalog restricts an OpenRouter catalog to the chat
// models the key may use, with the key's limits and prices from GET
// /models/user (pi: personalizeOpenRouterCatalog). Image and classifier
// models are returned unchanged. A missing limit keeps the catalog value. The
// request uses the provider's configured base URL, so regional endpoints such
// as `https://us.openrouter.ai/api/v1` apply their region's filtering. When
// the endpoint rejects the key (401/403) or does not exist (404), for example
// with a proxy configured in models.json, the catalog is returned unchanged.
func PersonalizeOpenRouterCatalog(catalog []ai.Model, refreshCtx *ai.RefreshModelsContext) ([]ai.Model, error) {
	key := openRouterCredentialKey(refreshCtx.Credential)
	if key == "" {
		return catalog, nil
	}
	base := openRouterBaseURL
	if refreshCtx.BaseURL != "" {
		base = refreshCtx.BaseURL
	}
	url := strings.TrimRight(base, "/") + "/models/user"

	requestCtx, cancel := context.WithTimeout(refreshCtx.Context, openRouterUserModelsTimeoutMs*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+key)

	resp, err := openRouterFetch(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		return catalog, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenRouter model list request failed: %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, openRouterUserModelsMaxBodyLen))
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil || body.Data == nil {
		return nil, fmt.Errorf("Invalid OpenRouter model list response")
	}
	allowed := map[string]*OpenRouterModel{}
	for _, raw := range body.Data {
		var entry OpenRouterModel
		if err := json.Unmarshal(raw, &entry); err != nil || entry.ID == "" {
			continue
		}
		allowed[entry.ID] = &entry
	}

	out := make([]ai.Model, 0, len(catalog))
	for i := range catalog {
		model := catalog[i]
		if !model.IsModelType(ai.ModelTypeChat) {
			out = append(out, model)
			continue
		}
		entry := allowed[model.ID]
		if entry == nil {
			continue
		}
		// `||`: OpenRouter reports a missing limit as absent, null or 0, like
		// the generator's fallback.
		if entry.TopProvider != nil && entry.TopProvider.ContextLength != nil && *entry.TopProvider.ContextLength != 0 {
			model.ContextWindow = *entry.TopProvider.ContextLength
		} else if entry.ContextLength != nil && *entry.ContextLength != 0 {
			model.ContextWindow = *entry.ContextLength
		}
		if entry.TopProvider != nil && entry.TopProvider.MaxCompletionTokens != nil && *entry.TopProvider.MaxCompletionTokens != 0 {
			model.MaxTokens = *entry.TopProvider.MaxCompletionTokens
		}
		if entry.Pricing != nil {
			model.Cost = OpenRouterCost(entry.Pricing)
		}
		out = append(out, model)
	}
	return out, nil
}

// openRouterCredentialKey resolves the bearer key from the refresh credential
// (pi: credential.type === "oauth" ? credential.access : credential.key).
func openRouterCredentialKey(credential auth.Credential) string {
	switch c := credential.(type) {
	case *auth.OAuthCredential:
		if c != nil {
			return c.Access
		}
	case *auth.ApiKeyCredential:
		if c != nil {
			return c.Key
		}
	}
	return ""
}
