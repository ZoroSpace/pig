// pi: auth/oauth/openai-chatgpt.ts.
//
// OpenAI Responses API token sharing through Sign in with ChatGPT. This
// public-client flow uses no client secret and sends the resulting user
// access token directly to api.openai.com. pi keeps a flow-local callback
// server here (it needs the issued client_id from the callback query, and
// its page texts differ from the shared server's); pig mirrors that 1:1.
// All OAuth HTTP traffic goes through the chatGPTOAuthDo seam so tests can
// stub it (pi: vi.stubGlobal("fetch")).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Every login registers a new client with this ID; OpenAI returns the issued
// client ID in the callback. AGENT_NAME_HINT is a protocol value sent to
// OpenAI and stays at pi's spelling (TUI-LOGO protocol-value policy).
const (
	chatGPTDynamicClientID = "dynamic_agent_client"
	chatGPTAgentNameHint   = "Pi"
	chatGPTAuthorizeURL    = "https://auth.openai.com/api/accounts/authorize"
	chatGPTTokenURL        = "https://auth.openai.com/api/accounts/oauth/token"
	chatGPTResource        = "https://api.openai.com/v1"
	chatGPTCallbackHost    = "127.0.0.1"
	chatGPTCallbackPort    = 1455
	chatGPTCallbackPath    = "/auth/callback"
	chatGPTRedirectURI     = "http://127.0.0.1:1455/auth/callback"
	chatGPTDirectScope     = "chatgpt.tokens.use.direct"
	chatGPTScope           = "openid profile email offline_access resource.invoke " + chatGPTDirectScope
	// Refresh this long before the real expiry so a request never starts
	// with a token about to expire.
	chatGPTExpiryMarginMs = 3 * 60 * 1000
)

// chatGPTUUIDPattern matches the device ID (pi: UUID_PATTERN).
var chatGPTUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// chatGPTOAuthDo is the HTTP transport seam for the token endpoint (pi: the
// global fetch stubbed by tests).
var chatGPTOAuthDo = func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	client := &http.Client{}
	return client.Do(request)
}

// chatGPTRandomValue makes a 32-byte base64url token (pi: randomValue).
func chatGPTRandomValue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// chatGPTAuthorizationResult is what a callback or a pasted redirect URL
// yields (pi: AuthorizationResult).
type chatGPTAuthorizationResult struct {
	code     string
	clientID string
}

// chatGPTAuthorizationResultFromCallback validates the callback query (pi:
// authorizationResultFromCallback).
func chatGPTAuthorizationResultFromCallback(query url.Values, expectedState string) (chatGPTAuthorizationResult, error) {
	code := query.Get("code")
	if code == "" {
		return chatGPTAuthorizationResult{}, errors.New("Missing authorization code")
	}
	state := query.Get("state")
	if state == "" {
		return chatGPTAuthorizationResult{}, errors.New("Missing OAuth state")
	}
	if state != expectedState {
		return chatGPTAuthorizationResult{}, errors.New("OAuth state mismatch")
	}
	clientID := strings.TrimSpace(query.Get("client_id"))
	if clientID == "" {
		return chatGPTAuthorizationResult{}, errors.New("OpenAI OAuth registration callback did not contain an issued client ID")
	}
	return chatGPTAuthorizationResult{code: code, clientID: clientID}, nil
}

// chatGPTAuthorizationResultFromManualInput validates a pasted final
// redirect URL (pi: authorizationResultFromManualInput).
func chatGPTAuthorizationResultFromManualInput(input string, expectedState string) (chatGPTAuthorizationResult, error) {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return chatGPTAuthorizationResult{}, errors.New("Paste the full callback URL from the browser")
	}
	expected, _ := url.Parse(chatGPTRedirectURI)
	if parsed.Scheme != expected.Scheme || parsed.Host != expected.Host || parsed.Path != expected.Path {
		return chatGPTAuthorizationResult{}, fmt.Errorf("The pasted callback URL must start with %s", chatGPTRedirectURI)
	}
	if errParam := parsed.Query().Get("error"); errParam != "" {
		return chatGPTAuthorizationResult{}, fmt.Errorf("ChatGPT authorization failed: %s", errParam)
	}
	return chatGPTAuthorizationResultFromCallback(parsed.Query(), expectedState)
}

// chatGPTCallbackServer is the flow-local loopback listener (pi:
// startCallbackServer). pi models the result as a promise; pig uses a
// buffered channel.
type chatGPTCallbackServer struct {
	listener   net.Listener
	httpServer *http.Server
	waitCh     chan chatGPTAuthorizationResult
	errCh      chan error
	closeOnce  sync.Once
}

func (s *chatGPTCallbackServer) close() {
	s.closeOnce.Do(func() {
		// Close the raw listener first (see OAuthCallbackServer.Close); Go's
		// http.Server.Close also drops spare keep-alive connections, which
		// is pi's closeAllConnections hazard: a browser spare connection
		// would otherwise deliver the NEXT login's callback to this server.
		if s.listener != nil {
			_ = s.listener.Close()
		}
		if s.httpServer != nil {
			_ = s.httpServer.Close()
		}
	})
}

// startChatGPTCallbackServer listens on 1455 and serves the callback route
// (pi: startCallbackServer). A bind failure returns an error and the flow
// degrades to manual entry.
func startChatGPTCallbackServer(expectedState string) (*chatGPTCallbackServer, error) {
	server := &chatGPTCallbackServer{
		waitCh: make(chan chatGPTAuthorizationResult, 1),
		errCh:  make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(OAuthErrorHTML("Internal error while processing the callback.", "")))
			}
		}()
		sendPage := func(status int, html string) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(html))
		}
		if r.URL.Path != chatGPTCallbackPath {
			sendPage(http.StatusNotFound, OAuthErrorHTML("Callback route not found.", ""))
			return
		}
		query := r.URL.Query()
		if errParam := query.Get("error"); errParam != "" {
			sendPage(http.StatusBadRequest, OAuthErrorHTML("ChatGPT was not connected.", "Error: "+errParam))
			server.errCh <- fmt.Errorf("ChatGPT authorization failed: %s", errParam)
			return
		}
		result, err := chatGPTAuthorizationResultFromCallback(query, expectedState)
		if err != nil {
			sendPage(http.StatusBadRequest, OAuthErrorHTML(err.Error(), ""))
			return
		}
		sendPage(http.StatusOK, OAuthSuccessHTML("ChatGPT authentication completed. You can close this window."))
		server.waitCh <- result
	})
	listener, err := net.Listen("tcp", net.JoinHostPort(chatGPTCallbackHost, fmt.Sprintf("%d", chatGPTCallbackPort)))
	if err != nil {
		return nil, err
	}
	server.listener = listener
	server.httpServer = &http.Server{Handler: mux}
	go func() { _ = server.httpServer.Serve(listener) }()
	return server, nil
}

// chatGPTRequestToken posts one form request to the token endpoint (pi:
// requestToken).
func chatGPTRequestToken(ctx context.Context, fields map[string]string) (map[string]any, error) {
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	response, err := chatGPTOAuthDo(ctx, http.MethodPost, chatGPTTokenURL, "application/x-www-form-urlencoded", values.Encode())
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenAI OAuth token request failed (%d): %s", response.StatusCode, string(body))
	}
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil || parsed == nil {
		return nil, errors.New("OpenAI OAuth token response must be an object")
	}
	return parsed, nil
}

// chatGPTRequireTokenString validates one token-response string field (pi:
// requireTokenString).
func chatGPTRequireTokenString(token map[string]any, field string) (string, error) {
	value, _ := token[field].(string)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("OpenAI OAuth token response has invalid %s", field)
	}
	return value, nil
}

// chatGPTCredentialFromTokenResponse builds the stored credential (pi:
// credentialFromTokenResponse). The issued client ID and granted scopes ride
// OAuthCredential.Extra so they round-trip through auth.json under the same
// keys pi writes (pi: named clientId/scopes fields).
func chatGPTCredentialFromTokenResponse(token map[string]any, clientID string) (*OAuthCredential, error) {
	access, err := chatGPTRequireTokenString(token, "access_token")
	if err != nil {
		return nil, err
	}
	refresh, err := chatGPTRequireTokenString(token, "refresh_token")
	if err != nil {
		return nil, err
	}
	scope, err := chatGPTRequireTokenString(token, "scope")
	if err != nil {
		return nil, err
	}
	expiresIn, isNumber := token["expires_in"].(float64)
	if !isNumber || expiresIn <= 0 {
		return nil, errors.New("OpenAI OAuth token response has invalid expires_in")
	}
	var scopes []string
	for _, entry := range strings.Fields(scope) {
		if entry != "" {
			scopes = append(scopes, entry)
		}
	}
	hasDirectScope := false
	for _, entry := range scopes {
		if entry == chatGPTDirectScope {
			hasDirectScope = true
		}
	}
	if !hasDirectScope {
		return nil, fmt.Errorf("OpenAI OAuth grant did not include %s", chatGPTDirectScope)
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Access:  access,
			Refresh: refresh,
			Expires: time.Now().UnixMilli() + int64(expiresIn)*1000 - chatGPTExpiryMarginMs,
			Extra: map[string]any{
				"clientId": clientID,
				"scopes":   scopes,
			},
		},
	}, nil
}

// chatGPTExchangeAuthorizationCode trades the callback code for tokens (pi:
// exchangeAuthorizationCode).
func chatGPTExchangeAuthorizationCode(ctx context.Context, code string, verifier string, clientID string) (*OAuthCredential, error) {
	token, err := chatGPTRequestToken(ctx, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  chatGPTRedirectURI,
		"resource":      chatGPTResource,
	})
	if err != nil {
		return nil, err
	}
	// Pi does not use the ID token to identify the user or read profile
	// data. Keep the presence check as part of the token-response contract.
	if idToken, _ := token["id_token"].(string); strings.TrimSpace(idToken) == "" {
		return nil, errors.New("OpenAI OAuth token response did not contain an ID token")
	}
	return chatGPTCredentialFromTokenResponse(token, clientID)
}

// chatGPTRefreshAccessToken exchanges the refresh token (pi:
// refreshAccessToken).
func chatGPTRefreshAccessToken(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	clientID, _ := credential.Extra["clientId"].(string)
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("Stored OpenAI OAuth credential does not contain an issued client ID; reconnect ChatGPT")
	}
	token, err := chatGPTRequestToken(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     clientID,
		"refresh_token": credential.Refresh,
		"resource":      chatGPTResource,
	})
	if err != nil {
		return nil, err
	}
	return chatGPTCredentialFromTokenResponse(token, clientID)
}

// chatGPTAgentHostID validates the app device ID; OpenAI identifies each
// installation ("agent host") by a stable URI such as `urn:uuid:<uuid>` (pi:
// agentHostId).
func chatGPTAgentHostID(options *LoginOptions) (string, error) {
	var deviceID string
	if options != nil && options.GetDeviceId != nil {
		deviceID = options.GetDeviceId()
	}
	if !chatGPTUUIDPattern.MatchString(deviceID) {
		return "", errors.New("Sign in with ChatGPT requires a device ID (UUID) for this installation")
	}
	return "urn:uuid:" + strings.ToLower(deviceID), nil
}

// loginOpenAIChatGPT runs the Sign in with ChatGPT flow (pi:
// loginOpenAIChatGPT).
func loginOpenAIChatGPT(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	hostID, err := chatGPTAgentHostID(options)
	if err != nil {
		return nil, err
	}
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := chatGPTRandomValue()
	if err != nil {
		return nil, err
	}
	nonce, err := chatGPTRandomValue()
	if err != nil {
		return nil, err
	}
	var callback *chatGPTCallbackServer
	if server, serverErr := startChatGPTCallbackServer(state); serverErr == nil {
		callback = server
		defer callback.close()
	} else {
		interaction.Notify(AuthEvent{
			Type: "info",
			Message: fmt.Sprintf("Could not listen on %s; paste the final redirect URL to continue. %s",
				chatGPTRedirectURI, serverErr.Error()),
		})
	}

	params := url.Values{}
	params.Set("client_id", chatGPTDynamicClientID)
	params.Set("agent_name_hint", chatGPTAgentNameHint)
	params.Set("ext_agent_host_id", hostID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", chatGPTRedirectURI)
	params.Set("resource", chatGPTResource)
	params.Set("scope", chatGPTScope)
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("nonce", nonce)
	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          chatGPTAuthorizeURL + "?" + params.Encode(),
		Instructions: "Complete sign-in in your browser. If the callback does not complete, paste the final redirect URL here.",
	})

	type chatGPTManualOutcome struct {
		result chatGPTAuthorizationResult
		err    error
	}
	manualCh := make(chan chatGPTManualOutcome, 1)
	go func() {
		input, promptErr := interaction.Prompt(AuthPrompt{
			Type:        "manual_code",
			Message:     "Complete login in your browser, or paste the final redirect URL here:",
			Placeholder: chatGPTRedirectURI,
		})
		if promptErr != nil {
			manualCh <- chatGPTManualOutcome{err: promptErr}
			return
		}
		result, parseErr := chatGPTAuthorizationResultFromManualInput(input, state)
		manualCh <- chatGPTManualOutcome{result: result, err: parseErr}
	}()

	var result chatGPTAuthorizationResult
	select {
	case out := <-manualCh:
		if out.err != nil {
			return nil, out.err
		}
		result = out.result
	case serverResult := <-callback.waitCh:
		result = serverResult
	case serverErr := <-callback.errCh:
		return nil, serverErr
	}
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}

	interaction.Notify(AuthEvent{Type: "progress", Message: "Exchanging authorization code for tokens..."})
	return chatGPTExchangeAuthorizationCode(ctx, result.code, verifier, result.clientID)
}

// OpenAIChatGPTOAuth is the OpenAI (ChatGPT subscription) OAuth auth (pi:
// openaiChatGPTOAuth).
type OpenAIChatGPTOAuth struct{}

func (OpenAIChatGPTOAuth) Name() string         { return "OpenAI (ChatGPT subscription)" }
func (OpenAIChatGPTOAuth) IsSubscription() bool { return true }
func (OpenAIChatGPTOAuth) LoginLabel() string   { return "Sign in with ChatGPT" }

// Login runs the provider-owned login flow.
func (OpenAIChatGPTOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return loginOpenAIChatGPT(ctx, interaction, options)
}

// Refresh exchanges the refresh token (pi: refresh).
func (OpenAIChatGPTOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return chatGPTRefreshAccessToken(ctx, credential)
}

// ToAuth derives request auth from a valid credential (pi: toAuth).
func (OpenAIChatGPTOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
