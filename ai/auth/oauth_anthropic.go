// pi: auth/oauth/anthropic.ts.
//
// Anthropic OAuth flow (Claude Pro/Max). pi uses Node's http.createServer for
// the OAuth callback server (CLI-only); pig uses net/http directly. The
// token-exchange HTTP call goes through the anthropicOAuthPostJSON seam so
// tests can stub it (pi: vi.stubGlobal("fetch")).
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// CLIENT_ID is transported base64-obfuscated in pi; decode at init.
var anthropicOAuthClientID = func() string {
	decoded, err := base64.StdEncoding.DecodeString("OWQxYzI1MGEtZTYxYi00NGQ5LTg4ZWQtNTk0NGQxOTYyZjVl")
	if err != nil {
		panic("auth: anthropic OAuth client id payload is invalid: " + err.Error())
	}
	return string(decoded)
}()

const (
	anthropicAuthorizeURL = "https://claude.ai/oauth/authorize"
	anthropicTokenURL     = "https://platform.claude.com/v1/oauth/token"
	anthropicCallbackPath = "/callback"
	// anthropicCallbackPort is preferred so the port can be forwarded into
	// containers or over SSH. Anthropic accepts any loopback port, so login
	// falls back to a free port when this one cannot be bound (#10571).
	anthropicRedirectURI  = "http://localhost:53692" + anthropicCallbackPath
	anthropicOAuthScopes  = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicCallbackPort = 53692
	// Claude Code's OAuth client also accepts this redirect_uri; the page
	// shows the authorization code to copy, which works when the browser runs
	// on another machine (pi: COPY_CODE_REDIRECT_URI, #10194).
	anthropicCopyCodeRedirectURI = "https://platform.claude.com/oauth/code/callback"
)

const (
	anthropicBrowserLoginMethod  = "browser"
	anthropicCopyCodeLoginMethod = "copy_code"
)

// anthropicOAuthPostJSON is the token-endpoint transport seam (pi: the global
// fetch stubbed by tests).
var anthropicOAuthPostJSON = postJSON

// getAnthropicCallbackHost resolves the callback bind host (pi:
// CALLBACK_HOST from PI_OAUTH_CALLBACK_HOST; pig reads the PIG_ spelling and
// falls back to the pi spelling for shared shells). A nil env reads process
// env.
func getAnthropicCallbackHost(env types.ProviderEnv) string {
	if host := utils.GetProviderEnvValue("PIG_OAUTH_CALLBACK_HOST", env); host != "" {
		return host
	}
	if host := utils.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", env); host != "" {
		return host
	}
	return "127.0.0.1"
}

// parseAuthorizationInput parses a pasted redirect URL / code (pi:
// parseAuthorizationInput): a URL's query first, then "#"-split codes, then
// bare query strings, then the value itself.
func parseAuthorizationInput(input string) (code string, state string) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "", ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		query := parsed.Query()
		return query.Get("code"), query.Get("state")
	}
	if strings.Contains(value, "#") {
		parts := strings.SplitN(value, "#", 2)
		return parts[0], parts[1]
	}
	if strings.Contains(value, "code=") {
		if parsed, err := url.ParseQuery(value); err == nil {
			return parsed.Get("code"), parsed.Get("state")
		}
	}
	return value, ""
}

// formatErrorDetails renders a thrown error for token-failure messages (pi:
// formatErrorDetails with name/code/errno/cause/stack detail parts; Go errors
// carry their chain in Error()).
func formatErrorDetails(err error) string {
	return err.Error()
}

// postJSON performs one JSON POST with a 30s timeout (pi: postJson; pi's
// AbortSignal.any([signal, timeout]) collapses into ctx + client timeout).
func postJSON(ctx context.Context, url string, body map[string]any) (string, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(encoded)))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP request failed. status=%d; url=%s; body=%s", response.StatusCode, url, string(responseBody))
	}
	return string(responseBody), nil
}

// exchangeAnthropicAuthorizationCode trades the callback code for tokens (pi:
// exchangeAuthorizationCode). Tokens expire 5 minutes before the server
// expiry.
func exchangeAnthropicAuthorizationCode(ctx context.Context, code string, state string, verifier string, redirectURI string) (*OAuthCredential, error) {
	responseBody, err := anthropicOAuthPostJSON(ctx, anthropicTokenURL, map[string]any{
		"grant_type":    "authorization_code",
		"client_id":     anthropicOAuthClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})
	if err != nil {
		return nil, fmt.Errorf("Token exchange request failed. url=%s; redirect_uri=%s; response_type=authorization_code; details=%s",
			anthropicTokenURL, redirectURI, formatErrorDetails(err))
	}
	var tokenData struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(responseBody), &tokenData); err != nil {
		return nil, fmt.Errorf("Token exchange returned invalid JSON. url=%s; body=%s; details=%s",
			anthropicTokenURL, responseBody, formatErrorDetails(err))
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: tokenData.RefreshToken,
			Access:  tokenData.AccessToken,
			Expires: time.Now().UnixMilli() + tokenData.ExpiresIn*1000 - 5*60*1000,
		},
	}, nil
}

// loginAnthropic runs the Claude Pro/Max login flow (pi: loginAnthropic).
// The callback wait races the manual prompt inside
// WaitForCallbackOrManualInput; pig's AuthInteraction has no per-prompt
// abort, so a prompt that returns after the callback settles is simply
// ignored (declared deviation — the UI layer cancels its own dialog).
func loginAnthropic(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	// The preferred port is tried first so forwarded setups keep working; a
	// bind failure (port taken, e.g. Hyper-V/WSL port exclusions) falls back
	// to an OS-assigned free loopback port. Without a callback server, login
	// continues with the pasted redirect URL (pi #10571: .catch(() =>
	// startCallbackServer(0)).catch(() => undefined)).
	startCallbackServer := func(port int) (*OAuthCallbackServer[string], error) {
		return StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
			ProviderName: "Anthropic",
			Host:         getAnthropicCallbackHost(nil),
			// The authorize URL keeps the localhost spelling regardless of the
			// bind host (pi: redirectHost).
			RedirectHost: "localhost",
			Port:         port,
			Path:         anthropicCallbackPath,
			State:        verifier,
			Complete:     func(_ context.Context, callbackCode string) (string, error) { return callbackCode, nil },
			Ctx:          ctx,
		})
	}
	var callback *OAuthCallbackServer[string]
	if server, serverErr := startCallbackServer(anthropicCallbackPort); serverErr == nil {
		callback = server
		defer callback.Close()
	} else if freeServer, freeErr := startCallbackServer(0); freeErr == nil {
		callback = freeServer
		defer callback.Close()
	}
	redirectURI := anthropicRedirectURI
	if callback != nil {
		redirectURI = callback.RedirectURI()
	}

	authParams := url.Values{}
	authParams.Set("code", "true")
	authParams.Set("client_id", anthropicOAuthClientID)
	authParams.Set("response_type", "code")
	authParams.Set("redirect_uri", redirectURI)
	authParams.Set("scope", anthropicOAuthScopes)
	authParams.Set("code_challenge", challenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("state", verifier)
	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          anthropicAuthorizeURL + "?" + authParams.Encode(),
		Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	result, err := WaitForCallbackOrManualInput[string](interaction, callback,
		"Complete login in your browser, or paste the authorization code / redirect URL here:", redirectURI)
	if err != nil {
		return nil, err
	}
	var code, state string
	if result.Type == "callback" {
		code = result.Value
		state = verifier
	} else {
		code, state = parseAuthorizationInput(result.Input)
		if state != "" && state != verifier {
			return nil, fmt.Errorf("OAuth state mismatch")
		}
		state = orDefaultString(state, verifier)
	}

	if code == "" {
		return nil, fmt.Errorf("Missing authorization code")
	}
	if state == "" {
		return nil, fmt.Errorf("Missing OAuth state")
	}
	interaction.Notify(AuthEvent{Type: "progress", Message: "Exchanging authorization code for tokens..."})
	return exchangeAnthropicAuthorizationCode(ctx, code, state, verifier, redirectURI)
}

func orDefaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// loginAnthropicCopyCode runs the headless copy-code login flow (pi:
// loginAnthropicCopyCode): the auth URL uses the platform.claude.com redirect
// page, and the user pastes the displayed "code#state" value.
func loginAnthropicCopyCode(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	authParams := url.Values{}
	authParams.Set("code", "true")
	authParams.Set("client_id", anthropicOAuthClientID)
	authParams.Set("response_type", "code")
	authParams.Set("redirect_uri", anthropicCopyCodeRedirectURI)
	authParams.Set("scope", anthropicOAuthScopes)
	authParams.Set("code_challenge", challenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("state", verifier)
	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          anthropicAuthorizeURL + "?" + authParams.Encode(),
		Instructions: "Complete login in your browser, then copy the code Anthropic shows and paste it here.",
	})

	input, err := interaction.Prompt(AuthPrompt{
		Type:        "manual_code",
		Message:     "Paste the code Anthropic shows after you sign in:",
		Placeholder: "code#state",
	})
	if err != nil {
		return nil, err
	}
	code, state := parseAuthorizationInput(input)
	if state != "" && state != verifier {
		return nil, fmt.Errorf("OAuth state mismatch")
	}
	if code == "" {
		return nil, fmt.Errorf("Missing authorization code")
	}
	interaction.Notify(AuthEvent{Type: "progress", Message: "Exchanging authorization code for tokens..."})
	return exchangeAnthropicAuthorizationCode(ctx, code, orDefaultString(state, verifier), verifier, anthropicCopyCodeRedirectURI)
}

// refreshAnthropicToken exchanges the refresh token (pi:
// refreshAnthropicToken).
func refreshAnthropicToken(ctx context.Context, refreshToken string) (*OAuthCredential, error) {
	responseBody, err := anthropicOAuthPostJSON(ctx, anthropicTokenURL, map[string]any{
		"grant_type":    "refresh_token",
		"client_id":     anthropicOAuthClientID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("Anthropic token refresh request failed. url=%s; details=%s",
			anthropicTokenURL, formatErrorDetails(err))
	}
	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(responseBody), &data); err != nil {
		return nil, fmt.Errorf("Anthropic token refresh returned invalid JSON. url=%s; body=%s; details=%s",
			anthropicTokenURL, responseBody, formatErrorDetails(err))
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: data.RefreshToken,
			Access:  data.AccessToken,
			Expires: time.Now().UnixMilli() + data.ExpiresIn*1000 - 5*60*1000,
		},
	}, nil
}

// AnthropicOAuth is the Claude Pro/Max OAuth auth (pi: anthropicOAuth).
type AnthropicOAuth struct{}

func (AnthropicOAuth) Name() string         { return "Anthropic (Claude Pro/Max)" }
func (AnthropicOAuth) IsSubscription() bool { return true }
func (AnthropicOAuth) LoginLabel() string   { return "" }

// Login runs the provider-owned login flow, asking for the method first (pi:
// login with the method select prompt — browser default, copy code headless).
func (AnthropicOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	method, err := interaction.Prompt(AuthPrompt{
		Type:    "select",
		Message: "Select Anthropic login method:",
		Options: []AuthPromptOption{
			{ID: anthropicBrowserLoginMethod, Label: "Browser login (default)"},
			{ID: anthropicCopyCodeLoginMethod, Label: "Copy code login (headless)"},
		},
	})
	if err != nil {
		return nil, err
	}
	switch method {
	case anthropicCopyCodeLoginMethod:
		return loginAnthropicCopyCode(ctx, interaction)
	case anthropicBrowserLoginMethod:
		return loginAnthropic(ctx, interaction, options)
	default:
		return nil, fmt.Errorf("Unknown Anthropic login method: %s", method)
	}
}

// Refresh exchanges the refresh token.
func (AnthropicOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return refreshAnthropicToken(ctx, credential.Refresh)
}

// ToAuth derives request auth from a valid credential.
func (AnthropicOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
