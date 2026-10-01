// pi: auth/oauth/openrouter.ts.
//
// OpenRouter OAuth PKCE flow. OpenRouter exchanges an authorization code for
// a permanent, user-controlled API key rather than an expiring
// access/refresh token pair. The callback is handled by a one-shot loopback
// server on an ephemeral port, raced against a manual prompt so
// remote/headless sessions can paste the redirect URL when the browser
// cannot reach the loopback server. All OAuth HTTP traffic goes through the
// openRouterOAuthDo seam so tests can stub it (pi: vi.stubGlobal("fetch")).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/utils"
)

const (
	openRouterAuthorizeURL         = "https://openrouter.ai/auth"
	openRouterTokenURL             = "https://openrouter.ai/api/v1/auth/keys"
	openRouterLoginTimeout         = 5 * time.Minute
	openRouterTokenExchangeTimeout = 30 * time.Second
)

// openRouterOAuthDo is the HTTP transport seam for the token exchange (pi:
// the global fetch stubbed by tests).
var openRouterOAuthDo = func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{}
	return client.Do(request)
}

// getOpenRouterCallbackHost resolves the callback bind host (pi:
// getCallbackHost; pig reads the PIG_ spelling and falls back to the pi
// spelling for shared shells).
func getOpenRouterCallbackHost() string {
	if host := utils.GetProviderEnvValue("PIG_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	if host := utils.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

// openRouterRandomUUID generates a random UUIDv4 (pi: crypto.randomUUID).
func openRouterRandomUUID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return ""
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40 // version 4
	buffer[8] = (buffer[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

// openRouterParseAuthorizationInput parses a pasted redirect URL / code (pi:
// parseAuthorizationInput): a URL's query first, then bare query strings,
// then the value itself. OpenRouter sends no state.
func openRouterParseAuthorizationInput(input string) string {
	value := strings.TrimSpace(input)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return parsed.Query().Get("code")
	}
	if strings.Contains(value, "code=") {
		if parsed, err := url.ParseQuery(value); err == nil {
			return parsed.Get("code")
		}
	}
	return value
}

// openRouterErrorDetail extracts a human-readable failure reason from the
// token-error body (pi: errorDetail).
func openRouterErrorDetail(body map[string]any) string {
	if detail, ok := body["error_description"].(string); ok {
		return detail
	}
	if detail, ok := body["message"].(string); ok {
		return detail
	}
	if detail, ok := body["error"].(string); ok {
		return detail
	}
	if obj, ok := body["error"].(map[string]any); ok {
		if detail, ok := obj["message"].(string); ok {
			return detail
		}
	}
	return ""
}

// openRouterExchangeAuthorizationCode trades the callback code for a
// permanent API key (pi: exchangeAuthorizationCode).
func openRouterExchangeAuthorizationCode(ctx context.Context, code string, verifier string) (*OAuthCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.New("Login cancelled")
	}
	requestBody, err := json.Marshal(map[string]string{
		"code":                  code,
		"code_verifier":         verifier,
		"code_challenge_method": "S256",
	})
	if err != nil {
		return nil, err
	}
	// pi: AbortSignal with a 30s timeout layered over the login signal.
	exchangeCtx, cancel := context.WithTimeout(ctx, openRouterTokenExchangeTimeout)
	defer cancel()
	response, err := openRouterOAuthDo(exchangeCtx, http.MethodPost, openRouterTokenURL, "application/json", string(requestBody))
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		if exchangeCtx.Err() != nil {
			return nil, errors.New("OpenRouter OAuth token exchange timed out")
		}
		return nil, err
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(response.Body)
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil || readErr != nil {
		parsed = nil
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil, errors.New("OpenRouter OAuth returned invalid JSON")
		}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := openRouterErrorDetail(parsed)
		message := fmt.Sprintf("OpenRouter OAuth key exchange failed (HTTP %d)", response.StatusCode)
		if detail != "" {
			message += ": " + detail
		}
		return nil, errors.New(message)
	}
	key, _ := parsed["key"].(string)
	if key == "" {
		return nil, errors.New(`OpenRouter OAuth response carries no "key"`)
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Access: key,
			// pi: Number.MAX_SAFE_INTEGER — the key never expires.
			Expires: math.MaxInt64,
		},
	}, nil
}

// loginOpenRouter runs the browser flow with a manual-paste fallback (pi:
// loginOpenRouter).
func loginOpenRouter(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	// OpenRouter sends no `state`; the random path keeps stray requests from
	// completing the sign-in. pi does not catch a listen failure here: the
	// ephemeral port makes one practically impossible, so propagate.
	callback, err := StartOAuthCallbackServer[*OAuthCredential](OAuthCallbackServerOptions[*OAuthCredential]{
		ProviderName: "OpenRouter",
		Host:         getOpenRouterCallbackHost(),
		Port:         0,
		Path:         "/oauth/callback/" + openRouterRandomUUID(),
		Complete: func(callbackCtx context.Context, callbackCode string) (*OAuthCredential, error) {
			return openRouterExchangeAuthorizationCode(callbackCtx, callbackCode, verifier)
		},
		Ctx:     ctx,
		Timeout: openRouterLoginTimeout,
	})
	if err != nil {
		return nil, err
	}
	defer callback.Close()

	authorizeParams := url.Values{}
	authorizeParams.Set("callback_url", callback.RedirectURI())
	authorizeParams.Set("code_challenge", challenge)
	authorizeParams.Set("code_challenge_method", "S256")
	interaction.Notify(AuthEvent{Type: "progress", Message: "Listening for OpenRouter OAuth callback on " + callback.RedirectURI()})
	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          openRouterAuthorizeURL + "?" + authorizeParams.Encode(),
		Instructions: "Complete sign-in in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	result, err := WaitForCallbackOrManualInput[*OAuthCredential](interaction, callback,
		"Complete sign-in in your browser, or paste the authorization code / redirect URL here:", callback.RedirectURI())
	if err != nil {
		return nil, err
	}
	if result.Type == "callback" {
		return result.Value, nil
	}
	code := openRouterParseAuthorizationInput(result.Input)
	if code == "" {
		return nil, errors.New("Missing authorization code")
	}
	interaction.Notify(AuthEvent{Type: "progress", Message: "Exchanging authorization code for an API key..."})
	return openRouterExchangeAuthorizationCode(ctx, code, verifier)
}

// OpenRouterOAuth is the OpenRouter OAuth auth (pi: openRouterOAuth).
type OpenRouterOAuth struct{}

func (OpenRouterOAuth) Name() string         { return "OpenRouter OAuth" }
func (OpenRouterOAuth) IsSubscription() bool { return false }
func (OpenRouterOAuth) LoginLabel() string   { return "Sign in with OpenRouter" }

// Login runs the provider-owned login flow.
func (OpenRouterOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return loginOpenRouter(ctx, interaction, options)
}

// Refresh is a no-op: the stored credential is a permanent API key (pi:
// refresh returns the credential unchanged).
func (OpenRouterOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return credential, nil
}

// ToAuth derives request auth from a valid credential.
func (OpenRouterOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
