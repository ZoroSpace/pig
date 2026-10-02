// pi: api/anthropic-messages.ts — the workload identity federation half of
// the createClient path (#10242). pi delegates the exchange to the Anthropic
// SDK's `config: {authentication: {type: "oidc_federation"}}` support; pig's
// adapter is hand-written HTTP, so the SDK's exchange, token parsing, and
// two-tier TokenCache are reproduced natively here against the same wire
// contract (POST {base}/v1/oauth/token, RFC 7523 jwt-bearer grant).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// Wire constants of the SDK's lib/credentials module that the exchange
// depends on.
const (
	anthropicFederationGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	anthropicFederationEndpoint  = "/v1/oauth/token"
	// oauth-2025-04-20 is required on authenticated API requests using an
	// OAuth bearer token; oidc-federation-2026-04-01 routes the exchange to
	// the federation service and must not be sent on other grants.
	anthropicOAuthAPIBetaHeader   = "oauth-2025-04-20"
	anthropicFederationBetaHeader = "oidc-federation-2026-04-01"
	anthropicFederationUserAgent  = "anthropic-sdk-typescript/0.124.0 oidcFederationProvider"

	anthropicIdentityTokenMaxBytes = 16 * 1024

	// TokenCache refresh policy: serve stale and refresh in the background
	// with 30–120s remaining, block below 30s (pi: the SDK's TokenCache with
	// ADVISORY/MANDATORY_REFRESH_THRESHOLD_IN_SECONDS).
	anthropicTokenAdvisoryRefreshSeconds  = 120
	anthropicTokenMandatoryRefreshSeconds = 30
	anthropicTokenAdvisoryBackoffSeconds  = 5
)

// anthropicFederationConfig is the federation configuration carried from the
// resolved provider env (pi: the ClientOptions["config"] object).
type anthropicFederationConfig struct {
	OrganizationID    string
	WorkspaceID       string
	FederationRuleID  string
	ServiceAccountID  string
	IdentityTokenFile string
}

// federationCacheKey identifies one cache entry (pi: JSON.stringify([baseUrl,
// federation]) — the SDK client whose token cache the per-request withOptions
// clones share).
func federationCacheKey(baseURL string, cfg *anthropicFederationConfig) string {
	encoded, _ := json.Marshal([...]any{baseURL, cfg})
	return string(encoded)
}

// hasAnthropicRequestAuth reports whether the request already carries auth
// (pi: hasRequestAuth).
func hasAnthropicRequestAuth(apiKey string, headers types.ProviderHeaders) bool {
	return apiKey != "" ||
		hasHeader(headers, "authorization") ||
		hasHeader(headers, "x-api-key") ||
		hasHeader(headers, "cf-aig-authorization")
}

// getAnthropicFederation builds the federation config from the ANTHROPIC_* env
// vars. Only for the anthropic provider, since the exchange is an Anthropic
// API endpoint, and only when no key or auth header was resolved (pi:
// getAnthropicFederation).
func getAnthropicFederation(model *types.Model, apiKey string, headers types.ProviderHeaders, env types.ProviderEnv) *anthropicFederationConfig {
	if model == nil || model.Provider != types.ProviderID("anthropic") || hasAnthropicRequestAuth(apiKey, headers) {
		return nil
	}
	federationRuleID := utils.GetProviderEnvValue(ai.AnthropicFederationRuleIDEnv, env)
	organizationID := utils.GetProviderEnvValue(ai.AnthropicOrganizationIDEnv, env)
	identityTokenFile := utils.GetProviderEnvValue(ai.AnthropicIdentityTokenFileEnv, env)
	if federationRuleID == "" || organizationID == "" || identityTokenFile == "" {
		return nil
	}
	return &anthropicFederationConfig{
		OrganizationID:    organizationID,
		WorkspaceID:       utils.GetProviderEnvValue(ai.AnthropicWorkspaceIDEnv, env),
		FederationRuleID:  federationRuleID,
		ServiceAccountID:  utils.GetProviderEnvValue(ai.AnthropicServiceAccountIDEnv, env),
		IdentityTokenFile: identityTokenFile,
	}
}

// anthropicFederatedToken is one exchanged access token.
type anthropicFederatedToken struct {
	token     string
	expiresAt int64 // unix seconds
}

// anthropicFederationTokenCache is the process-level federation token cache
// (pi: the federationClient package var — one Anthropic client per federation
// config whose SDK TokenCache the per-request withOptions clones share). The
// refresh policy mirrors the SDK's TokenCache: >120s remaining serves the
// cached token, 30–120s serves stale and refreshes in the background (with a
// 5s error backoff), <30s blocks and joins any in-flight exchange.
type anthropicFederationTokenCache struct {
	mu                sync.Mutex
	key               string
	fetch             types.FetchFunction
	cached            *anthropicFederatedToken
	refreshing        chan struct{}
	refreshErr        error
	lastAdvisoryError time.Time
}

// theAnthropicFederationTokenCache is pi's module-level federationClient.
var theAnthropicFederationTokenCache anthropicFederationTokenCache

// sameFetchIdentity compares two fetch functions by code pointer (Go funcs
// are only comparable to nil; pi compares the fetch reference identity).
func sameFetchIdentity(a, b types.FetchFunction) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

// getToken returns a valid access token, exchanging/refreshing as needed. It
// resets the entry when the key or fetch changes (pi: a different federation
// config or fetch creates a new client).
func (c *anthropicFederationTokenCache) getToken(ctx context.Context, key string, fetch types.FetchFunction, exchange func(ctx context.Context) (*anthropicFederatedToken, error)) (string, error) {
	c.mu.Lock()
	if c.key != key || !sameFetchIdentity(c.fetch, fetch) {
		c.key = key
		c.fetch = fetch
		c.cached = nil
		c.refreshing = nil
		c.refreshErr = nil
	}
	for {
		if c.cached == nil {
			token, err := c.refresh(ctx, exchange)
			if err != nil {
				c.mu.Unlock()
				return "", err
			}
			c.mu.Unlock()
			return token, nil
		}
		remaining := c.cached.expiresAt - time.Now().Unix()
		if remaining > anthropicTokenAdvisoryRefreshSeconds {
			token := c.cached.token
			c.mu.Unlock()
			return token, nil
		}
		if remaining > anthropicTokenMandatoryRefreshSeconds {
			c.backgroundRefresh(ctx, exchange)
			token := c.cached.token
			c.mu.Unlock()
			return token, nil
		}
		token, err := c.refresh(ctx, exchange)
		if err != nil {
			c.mu.Unlock()
			return "", err
		}
		c.mu.Unlock()
		return token, nil
	}
}

// refresh performs a mandatory refresh, joining any in-flight exchange.
// Caller must hold c.mu; the lock is released while the exchange runs and
// reacquired before returning.
func (c *anthropicFederationTokenCache) refresh(ctx context.Context, exchange func(ctx context.Context) (*anthropicFederatedToken, error)) (string, error) {
	if c.refreshing != nil {
		ch := c.refreshing
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
		if c.cached != nil {
			return c.cached.token, nil
		}
		return "", c.refreshErr
	}
	c.refreshErr = nil
	ch := make(chan struct{})
	c.refreshing = ch
	c.mu.Unlock()

	token, err := exchange(ctx)

	c.mu.Lock()
	close(ch)
	c.refreshing = nil
	if err != nil {
		c.refreshErr = err
		return "", err
	}
	c.cached = token
	return token.token, nil
}

// backgroundRefresh kicks off a non-blocking refresh inside the advisory
// window; failures keep the stale token and back off for 5s (pi:
// backgroundRefresh). Caller must hold c.mu.
func (c *anthropicFederationTokenCache) backgroundRefresh(ctx context.Context, exchange func(ctx context.Context) (*anthropicFederatedToken, error)) {
	if c.refreshing != nil {
		return
	}
	if time.Since(c.lastAdvisoryError) < anthropicTokenAdvisoryBackoffSeconds*time.Second {
		return
	}
	c.refreshErr = nil
	ch := make(chan struct{})
	c.refreshing = ch
	go func() {
		token, err := exchange(ctx)
		c.mu.Lock()
		defer c.mu.Unlock()
		close(ch)
		c.refreshing = nil
		if err != nil {
			// Advisory failure: keep serving the stale cached token.
			c.lastAdvisoryError = time.Now()
			c.refreshErr = err
			return
		}
		c.cached = token
	}()
}

// anthropicBetaListContains reports whether a comma-separated anthropic-beta
// header value already contains the feature.
func anthropicBetaListContains(headerValue string, feature string) bool {
	for _, entry := range strings.Split(headerValue, ",") {
		if strings.TrimSpace(entry) == feature {
			return true
		}
	}
	return false
}

// requireAnthropicSecureTokenEndpoint rejects base URLs that would send the
// JWT assertion over cleartext HTTP; loopback hosts are allowed for local
// development (pi: requireSecureTokenEndpoint).
func requireAnthropicSecureTokenEndpoint(baseURL string) error {
	if baseURL == "" {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("Invalid token endpoint base URL %q: %s", baseURL, err)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.Trim(host, "[]")
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("Refusing to send credential over non-https token endpoint %q", baseURL)
}

// readAnthropicIdentityToken reads a JWT from the file on every call,
// supporting automatic rotation (pi: identityTokenFromFile).
func readAnthropicIdentityToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("Identity token file path is empty")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("Failed to read identity token file at %s: %s", path, err)
	}
	token := strings.TrimSpace(string(content))
	if token == "" {
		return "", fmt.Errorf("Identity token file at %s is empty", path)
	}
	return token, nil
}

// parseAnthropicTokenResponse validates a token-endpoint JSON response (pi:
// parseTokenResponse).
func parseAnthropicTokenResponse(body []byte) (*anthropicFederatedToken, error) {
	var data struct {
		AccessToken string  `json:"access_token"`
		TokenType   string  `json:"token_type"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("Token endpoint returned non-JSON response: %s", redactAnthropicTokenErrorBody(body))
	}
	if data.AccessToken == "" {
		return nil, fmt.Errorf("Token endpoint response missing access_token: %s", redactAnthropicTokenErrorBody(body))
	}
	if data.TokenType != "" && strings.ToLower(data.TokenType) != "bearer" {
		return nil, fmt.Errorf("Token endpoint response: unsupported token_type %q (want Bearer)", data.TokenType)
	}
	if math.IsNaN(data.ExpiresIn) || math.IsInf(data.ExpiresIn, 0) || data.ExpiresIn == 0 {
		return nil, fmt.Errorf("Token endpoint response missing required fields: %s", redactAnthropicTokenErrorBody(body))
	}
	return &anthropicFederatedToken{
		token:     data.AccessToken,
		expiresAt: time.Now().Unix() + int64(data.ExpiresIn),
	}, nil
}

// anthropicTokenErrorSafeKeys are the RFC 6749 §5.2 standard error fields;
// anything else in an error body is potentially echoed input and is dropped
// (pi: SAFE_ERROR_KEYS + redactSensitive).
var anthropicTokenErrorSafeKeys = map[string]bool{
	"error": true, "error_description": true, "error_uri": true,
}

// redactAnthropicTokenErrorBody truncates a string body and reduces a JSON
// object body to the safe error fields (pi: redactSensitive).
func redactAnthropicTokenErrorBody(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil && parsed != nil {
		redacted := map[string]any{}
		for key, value := range parsed {
			if anthropicTokenErrorSafeKeys[key] {
				redacted[key] = value
			}
		}
		encoded, err := json.Marshal(redacted)
		if err == nil {
			return truncateAnthropicErrorText(string(encoded))
		}
	}
	return truncateAnthropicErrorText(string(body))
}

func truncateAnthropicErrorText(text string) string {
	const max = 2000
	if len(text) > max {
		return text[:max]
	}
	return text
}

// exchangeAnthropicFederatedToken performs one fresh jwt-bearer exchange (pi:
// the SDK's oidcFederationProvider). Federation grants return no refresh
// token; callers re-exchange the assertion on expiry.
func exchangeAnthropicFederatedToken(ctx context.Context, baseURL string, cfg *anthropicFederationConfig, fetch types.FetchFunction) (*anthropicFederatedToken, error) {
	if err := requireAnthropicSecureTokenEndpoint(baseURL); err != nil {
		return nil, err
	}
	assertion, err := readAnthropicIdentityToken(cfg.IdentityTokenFile)
	if err != nil {
		return nil, err
	}
	// The token endpoint enforces a 16 KiB assertion limit; surface a clear
	// client-side error so misconfigured projected-token sources are
	// diagnosable without a server round-trip.
	if len(assertion) > anthropicIdentityTokenMaxBytes {
		return nil, fmt.Errorf("Identity token is %d KiB, exceeds the 16 KiB assertion limit", (len(assertion)+1023)/1024)
	}
	body := map[string]any{
		"grant_type":         anthropicFederationGrantType,
		"assertion":          assertion,
		"federation_rule_id": cfg.FederationRuleID,
		"organization_id":    cfg.OrganizationID,
	}
	if cfg.ServiceAccountID != "" {
		body["service_account_id"] = cfg.ServiceAccountID
	}
	if cfg.WorkspaceID != "" {
		body["workspace_id"] = cfg.WorkspaceID
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSuffix(baseURL, "/") + anthropicFederationEndpoint
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("anthropic-beta", anthropicOAuthAPIBetaHeader+","+anthropicFederationBetaHeader)
	request.Header.Set("User-Agent", anthropicFederationUserAgent)

	var response *http.Response
	if fetch != nil {
		response, err = fetch(request)
	} else {
		response, err = http.DefaultClient.Do(request)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to reach token endpoint %s: %s", endpoint, err)
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if readErr != nil {
		return nil, fmt.Errorf("Failed to read token endpoint response: %s", readErr)
	}
	requestID := response.Header.Get("Request-Id")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := fmt.Sprintf("Token exchange failed with status %d", response.StatusCode)
		if requestID != "" {
			message += fmt.Sprintf(" (request-id %s)", requestID)
		}
		message += ": " + redactAnthropicTokenErrorBody(responseBody)
		if response.StatusCode == 401 {
			hintMiddle := ""
			if cfg.WorkspaceID == "" {
				hintMiddle = "If your federation rule is scoped to multiple workspaces, set the ANTHROPIC_WORKSPACE_ID environment variable, the 'workspace_id' config key, or the `workspaceId` option. "
			}
			message += " Ensure your federation rule matches your identity token. " + hintMiddle +
				"View your authentication events in the Workload identity page of Claude Console for more details."
		}
		return nil, fmt.Errorf("%s", message)
	}
	token, err := parseAnthropicTokenResponse(responseBody)
	if err != nil {
		if requestID != "" {
			return nil, fmt.Errorf("%s (request-id %s)", err, requestID)
		}
		return nil, err
	}
	return token, nil
}
