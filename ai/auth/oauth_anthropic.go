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
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	anthropicRedirectURI  = "http://localhost:53692" + anthropicCallbackPath
	anthropicOAuthScopes  = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthropicCallbackPort = "53692"
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

// anthropicCallbackResult is what the callback server hands to the login
// flow (pi: the { code, state } | null waitForCode resolution).
type anthropicCallbackResult struct {
	code  string
	state string
}

// anthropicCallbackServer is the localhost callback listener (pi:
// CallbackServerInfo).
type anthropicCallbackServer struct {
	server      *http.Server
	listener    net.Listener
	redirectURI string
	cancelWait  func()
	waitCh      chan anthropicCallbackResult
	settleOnce  sync.Once
}

// close releases the port even when Serve has not registered the listener
// yet (http.Server.Close only sees listeners after Serve starts; closing
// the raw listener first avoids a leaked bind that breaks the next login
// in the same process).
func (s *anthropicCallbackServer) close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.server != nil {
		_ = s.server.Close()
	}
}

func startAnthropicCallbackServer(ctx context.Context, expectedState string) (*anthropicCallbackServer, error) {
	host := getAnthropicCallbackHost(nil)
	waitCh := make(chan anthropicCallbackResult, 1)
	server := &anthropicCallbackServer{
		redirectURI: anthropicRedirectURI,
		waitCh:      waitCh,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(anthropicCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("Internal error"))
			}
		}()
		query := r.URL.Query()
		code := query.Get("code")
		state := query.Get("state")
		if errParam := query.Get("error"); errParam != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(OAuthErrorHTML("Anthropic authentication did not complete.", "Error: "+errParam)))
			return
		}
		if code == "" || state == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(OAuthErrorHTML("Missing code or state parameter.", "")))
			return
		}
		if state != expectedState {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(OAuthErrorHTML("State mismatch.", "")))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(OAuthSuccessHTML("Anthropic authentication completed. You can close this window.")))
		server.settle(anthropicCallbackResult{code: code, state: state})
	})
	httpServer := &http.Server{Addr: host + ":" + anthropicCallbackPort, Handler: mux}
	server.server = httpServer
	listener, err := net.Listen("tcp", host+":"+anthropicCallbackPort)
	if err != nil {
		return nil, err
	}
	server.cancelWait = func() {
		server.settleOnce.Do(func() {
			waitCh <- anthropicCallbackResult{}
		})
	}
	server.listener = listener
	go func() {
		_ = httpServer.Serve(listener)
	}()
	go func() {
		// Shut the listener down when the login flow ends.
		<-ctx.Done()
		server.close()
	}()
	return server, nil
}

func (s *anthropicCallbackServer) settle(result anthropicCallbackResult) {
	s.settleOnce.Do(func() {
		s.waitCh <- result
	})
}

// waitForCode resolves with the callback result, or the zero value after
// cancelWait (pi: waitForCode + cancelWait).
func (s *anthropicCallbackServer) waitForCode() anthropicCallbackResult {
	return <-s.waitCh
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
// pi races the callback server against a cancellable manual prompt; Go's
// AuthInteraction has no per-prompt abort, so a prompt that returns after the
// callback settles is simply ignored (declared deviation — the UI layer
// cancels its own dialog).
func loginAnthropic(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	flowCtx, cancelFlow := context.WithCancel(ctx)
	defer cancelFlow()
	server, err := startAnthropicCallbackServer(flowCtx, verifier)
	if err != nil {
		return nil, err
	}
	defer server.close()

	authParams := url.Values{}
	authParams.Set("code", "true")
	authParams.Set("client_id", anthropicOAuthClientID)
	authParams.Set("response_type", "code")
	authParams.Set("redirect_uri", anthropicRedirectURI)
	authParams.Set("scope", anthropicOAuthScopes)
	authParams.Set("code_challenge", challenge)
	authParams.Set("code_challenge_method", "S256")
	authParams.Set("state", verifier)
	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          anthropicAuthorizeURL + "?" + authParams.Encode(),
		Instructions: "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here.",
	})

	type manualOutcome struct {
		input string
		err   error
	}
	manualCh := make(chan manualOutcome, 1)
	go func() {
		input, promptErr := interaction.Prompt(AuthPrompt{
			Type:        "manual_code",
			Message:     "Complete login in your browser, or paste the authorization code / redirect URL here:",
			Placeholder: anthropicRedirectURI,
		})
		manualCh <- manualOutcome{input: input, err: promptErr}
	}()

	var code, state string
	select {
	case outcome := <-manualCh:
		server.cancelWait()
		if outcome.err != nil {
			return nil, outcome.err
		}
		callback := server.waitForCode()
		if callback.code != "" {
			code, state = callback.code, callback.state
			break
		}
		parsedCode, parsedState := parseAuthorizationInput(outcome.input)
		if parsedState != "" && parsedState != verifier {
			return nil, fmt.Errorf("OAuth state mismatch")
		}
		code, state = parsedCode, orDefaultString(parsedState, verifier)
	case callback := <-server.waitCh:
		code, state = callback.code, callback.state
	}

	if code == "" {
		return nil, fmt.Errorf("Missing authorization code")
	}
	if state == "" {
		return nil, fmt.Errorf("Missing OAuth state")
	}
	interaction.Notify(AuthEvent{Type: "progress", Message: "Exchanging authorization code for tokens..."})
	return exchangeAnthropicAuthorizationCode(ctx, code, state, verifier, anthropicRedirectURI)
}

func orDefaultString(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
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

// Login runs the provider-owned login flow.
func (AnthropicOAuth) Login(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	return loginAnthropic(ctx, interaction)
}

// Refresh exchanges the refresh token.
func (AnthropicOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return refreshAnthropicToken(ctx, credential.Refresh)
}

// ToAuth derives request auth from a valid credential.
func (AnthropicOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
