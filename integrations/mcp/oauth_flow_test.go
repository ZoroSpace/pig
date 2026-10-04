package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockOAuthServer is a fake MCP server + authorization server for e2e flow
// tests: PRM/ASM discovery, dynamic client registration, a token endpoint,
// and a protected MCP endpoint that answers 401 until a valid bearer token
// arrives. All on one httptest server (loopback, so plain http is allowed).
type mockOAuthServer struct {
	t *testing.T
	s *httptest.Server

	mu           sync.Mutex
	clients      int             // dynamic registrations
	tokens       int             // token endpoint hits (exchanges + refreshes)
	refreshes    int             // refresh-grant hits
	tokenErr     string          // forced token endpoint error body
	accessTokens map[string]bool // issued access tokens
}

func newMockOAuthServer(t *testing.T) *mockOAuthServer {
	m := &mockOAuthServer{t: t, accessTokens: map[string]bool{}}
	mux := http.NewServeMux()

	// Protected resource metadata (path-suffixed form; resource = MCP URL).
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q],"scopes_supported":["mcp:read"]}`, m.serverURL(), m.s.URL)
	})
	// Authorization server metadata (the server root doubles as the AS).
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"registration_endpoint":%q,`+
			`"response_types_supported":["code"],"grant_types_supported":["authorization_code","refresh_token"],`+
			`"code_challenge_methods_supported":["S256"],"token_endpoint_auth_methods_supported":["none"],`+
			`"authorization_response_iss_parameter_supported":true}`,
			m.s.URL, m.s.URL+"/authorize", m.s.URL+"/token", m.s.URL+"/register")
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.clients++
		id := m.clients
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"client_id":"client-%d","redirect_uris":["http://127.0.0.1/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, id)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.tokens++
		grant := r.PostForm.Get("grant_type")
		// A forced error applies to refresh grants only, so tests can kill the
		// stored grant without breaking the browser exchange that follows.
		if grant == "refresh_token" && m.tokenErr != "" {
			w.WriteHeader(400)
			fmt.Fprint(w, m.tokenErr)
			return
		}
		switch grant {
		case "authorization_code":
			if r.PostForm.Get("code_verifier") == "" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":"invalid_request","error_description":"missing verifier"}`)
				return
			}
			access := fmt.Sprintf("access-%d", m.tokens)
			m.accessTokens[access] = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600,"scope":"mcp:read","refresh_token":"refresh-%d"}`, access, m.tokens)
		case "refresh_token":
			m.refreshes++
			access := fmt.Sprintf("access-%d", m.tokens)
			m.accessTokens[access] = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, access)
		default:
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"unsupported_grant_type"}`)
		}
	})
	// The protected MCP endpoint: 401 with a challenge until a token we
	// issued arrives; then a minimal JSON-RPC face (initialize + tools/list)
	// so a full client handshake succeeds.
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		m.mu.Lock()
		valid := token != "" && m.accessTokens[token]
		m.mu.Unlock()
		if !valid {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q`, m.serverURL()+"/.well-known/oauth-protected-resource/mcp"))
			w.WriteHeader(401)
			fmt.Fprint(w, "unauthorized")
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(405) // no server-initiated stream
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if len(msg.ID) == 0 {
			w.WriteHeader(202) // notification
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch msg.Method {
		case "initialize":
			version := msg.Params.ProtocolVersion
			if version == "" {
				version = "2025-06-18"
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":%q,"capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mock","version":"0"}}}`, msg.ID, version)
		case "tools/list":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}`, msg.ID)
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"method not found"}}`, msg.ID)
		}
	})

	m.s = httptest.NewServer(mux)
	return m
}

func (m *mockOAuthServer) serverURL() string { return m.s.URL + "/mcp" }

func (m *mockOAuthServer) close() { m.s.Close() }

func (m *mockOAuthServer) counts() (tokens, refreshes, clients int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens, m.refreshes, m.clients
}

func (m *mockOAuthServer) forceTokenError(body string) {
	m.mu.Lock()
	m.tokenErr = body
	m.mu.Unlock()
}

// scriptedPrompt is a SignInPrompt with two modes: browser (simulates the
// browser by hitting the loopback callback with a code+state+iss redirect,
// then waits for the flow to finish via context cancellation) and paste
// (builds the redirect URL from the shown authorization URL and returns it).
type scriptedPrompt struct {
	t       *testing.T
	mock    *mockOAuthServer
	browser bool
	authURL string
}

func (p *scriptedPrompt) ShowAuthorizationURL(raw string) {
	p.authURL = raw
	if !p.browser {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		p.t.Fatalf("auth URL: %v", err)
	}
	target := fmt.Sprintf("%s?code=the-code&state=%s&iss=%s",
		u.Query().Get("redirect_uri"), url.QueryEscape(u.Query().Get("state")), url.QueryEscape(p.mock.s.URL))
	resp, err := http.Get(target) //nolint:noctx // test-local callback server
	if err != nil {
		p.t.Logf("browser callback: %v", err)
		return
	}
	resp.Body.Close()
}

func (p *scriptedPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	if p.browser {
		// The callback wins the race; the flow cancels the prompt.
		<-ctx.Done()
		return "", ErrSignInCancelled
	}
	if p.authURL == "" {
		return "", errors.New("no authorization URL was shown")
	}
	u, err := url.Parse(p.authURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?code=the-code&state=%s&iss=%s",
		u.Query().Get("redirect_uri"), url.QueryEscape(u.Query().Get("state")), url.QueryEscape(p.mock.s.URL)), nil
}

func TestSignInBrowserFlow(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	deps := flowDeps{store: store, serverName: "docs"}

	prompt := &scriptedPrompt{t: t, mock: mock, browser: true}
	if err := signInMcpServer(context.Background(), deps, mock.serverURL(), OAuthSettings{}, OAuthChallenge{}, prompt, nil); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	state, err := store.Load("docs", mock.serverURL())
	if err != nil || state == nil || state.Tokens == nil {
		t.Fatalf("no stored tokens: %v %v", state, err)
	}
	if !strings.HasPrefix(state.Tokens.AccessToken, "access-") || state.Tokens.RefreshToken == "" {
		t.Fatalf("tokens: %+v", state.Tokens)
	}
	if state.TokenEndpoint != mock.s.URL+"/token" {
		t.Fatalf("token endpoint not persisted: %q", state.TokenEndpoint)
	}
	// The PRM scopes_supported become the requested scope (RFC 6749 §5.1:
	// the response omits scope only in refreshes; the exchange echoes it).
	if state.Scope != "mcp:read" {
		t.Fatalf("scope: %q", state.Scope)
	}
	if state.TokensExpireAt == 0 {
		t.Fatal("expiry not recorded")
	}
}

func TestSignInPastedRedirectURL(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	deps := flowDeps{store: store, serverName: "docs"}

	prompt := &scriptedPrompt{t: t, mock: mock}
	if err := signInMcpServer(context.Background(), deps, mock.serverURL(), OAuthSettings{}, OAuthChallenge{}, prompt, nil); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	state, _ := store.Load("docs", mock.serverURL())
	if state == nil || state.Tokens == nil || !strings.HasPrefix(state.Tokens.AccessToken, "access-") {
		t.Fatalf("tokens after paste sign-in: %+v", state)
	}
}

func TestSignInRefreshFirstNoBrowser(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	deps := flowDeps{store: store, serverName: "docs"}

	// Pre-store a live grant: sign-in must refresh silently and never show a
	// URL.
	if err := store.Save("docs", mock.serverURL(), &OAuthServerState{
		ServerURL:     mock.serverURL(),
		TokenEndpoint: mock.s.URL + "/token",
		Tokens:        &OAuthTokens{AccessToken: "stale", TokenType: "Bearer", RefreshToken: "refresh-0", Scope: "mcp:read"},
	}); err != nil {
		t.Fatal(err)
	}

	prompt := &scriptedPrompt{t: t, mock: mock, browser: true}
	if err := signInMcpServer(context.Background(), deps, mock.serverURL(), OAuthSettings{}, OAuthChallenge{}, prompt, nil); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	if prompt.authURL != "" {
		t.Fatalf("refresh-first must not open a browser, showed %s", prompt.authURL)
	}
	if _, refreshes, _ := mock.counts(); refreshes != 1 {
		t.Fatalf("refreshes: %d", refreshes)
	}
	state, _ := store.Load("docs", mock.serverURL())
	if !strings.HasPrefix(state.Tokens.AccessToken, "access-") || state.Tokens.AccessToken == "stale" {
		t.Fatalf("tokens not refreshed: %+v", state.Tokens)
	}
}

func TestSignInInvalidGrantFallsToBrowser(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	deps := flowDeps{store: store, serverName: "docs"}

	if err := store.Save("docs", mock.serverURL(), &OAuthServerState{
		ServerURL:     mock.serverURL(),
		TokenEndpoint: mock.s.URL + "/token",
		Tokens:        &OAuthTokens{AccessToken: "stale", TokenType: "Bearer", RefreshToken: "dead", Scope: "mcp:read"},
	}); err != nil {
		t.Fatal(err)
	}
	// The refresh hits the forced invalid_grant; the flow clears the dead
	// grant and completes the browser flow.
	mock.forceTokenError(`{"error":"invalid_grant","error_description":"token revoked"}`)

	prompt := &scriptedPrompt{t: t, mock: mock, browser: true}
	if err := signInMcpServer(context.Background(), deps, mock.serverURL(), OAuthSettings{}, OAuthChallenge{}, prompt, nil); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	if prompt.authURL == "" {
		t.Fatal("expected the browser flow after the dead grant")
	}
	state, _ := store.Load("docs", mock.serverURL())
	if !strings.HasPrefix(state.Tokens.AccessToken, "access-") {
		t.Fatalf("fresh tokens missing: %+v", state.Tokens)
	}
}

func TestHandlerSilentRefreshAfter401(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	if err := store.Save("docs", mock.serverURL(), &OAuthServerState{
		ServerURL:     mock.serverURL(),
		TokenEndpoint: mock.s.URL + "/token",
		Tokens:        &OAuthTokens{AccessToken: "stale", TokenType: "Bearer", RefreshToken: "refresh-0", Scope: "mcp:read"},
	}); err != nil {
		t.Fatal(err)
	}

	handler := newOAuthTransportHandler(flowDeps{store: store, serverName: "docs"}, "docs", mock.serverURL(),
		func() OAuthSettings { return OAuthSettings{} }, nil)
	resp := &http.Response{StatusCode: 401, Header: http.Header{}}
	if err := handler.Authorize(context.Background(), &http.Request{Header: http.Header{}}, resp); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, refreshes, _ := mock.counts(); refreshes != 1 {
		t.Fatalf("refreshes: %d", refreshes)
	}
	state, _ := store.Load("docs", mock.serverURL())
	if !strings.HasPrefix(state.Tokens.AccessToken, "access-") {
		t.Fatalf("rotated tokens not stored: %+v", state.Tokens)
	}
}

func TestHandlerNoTokensSignInRequired(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	handler := newOAuthTransportHandler(flowDeps{store: store, serverName: "docs"}, "docs", mock.serverURL(),
		func() OAuthSettings { return OAuthSettings{} }, nil)

	// No tokens: TokenSource sends nothing and Authorize demands a sign-in.
	src, err := handler.TokenSource(context.Background())
	if err != nil || src != nil {
		t.Fatalf("token source: %v %v", src, err)
	}
	resp := &http.Response{StatusCode: 401, Header: http.Header{}}
	err = handler.Authorize(context.Background(), &http.Request{Header: http.Header{}}, resp)
	if !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("authorize: %v", err)
	}
	if !strings.Contains(err.Error(), `/mcp`) {
		t.Fatalf("error should point at /mcp: %v", err)
	}
}

func TestHandlerInsufficientScopeNeedsSignIn(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	if err := store.Save("docs", mock.serverURL(), &OAuthServerState{
		ServerURL: mock.serverURL(),
		Tokens:    &OAuthTokens{AccessToken: "at", TokenType: "Bearer", RefreshToken: "rt"},
	}); err != nil {
		t.Fatal(err)
	}
	var seen OAuthChallenge
	handler := newOAuthTransportHandler(flowDeps{store: store, serverName: "docs"}, "docs", mock.serverURL(),
		func() OAuthSettings { return OAuthSettings{} }, func(ch OAuthChallenge) { seen = ch })

	// A 403 without insufficient_scope is retried without error.
	resp := &http.Response{StatusCode: 403, Header: http.Header{}}
	if err := handler.Authorize(context.Background(), &http.Request{Header: http.Header{}}, resp); err != nil {
		t.Fatalf("plain 403: %v", err)
	}
	// insufficient_scope: a refresh cannot widen scope → sign-in required.
	// (Build the header through Add so the key is canonicalized; a literal
	// "WWW-Authenticate" map key is invisible to Header.Values.)
	resp = &http.Response{StatusCode: 403, Header: http.Header{}}
	resp.Header.Add("WWW-Authenticate", `Bearer error="insufficient_scope", scope="mcp:write"`)
	if err := handler.Authorize(context.Background(), &http.Request{Header: http.Header{}}, resp); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("insufficient_scope: %v", err)
	}
	if seen.Scope != "mcp:write" {
		t.Fatalf("challenge not surfaced: %+v", seen)
	}
}

func TestHandlerProactiveRefreshOnExpiry(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")
	if err := store.Save("docs", mock.serverURL(), &OAuthServerState{
		ServerURL:     mock.serverURL(),
		TokenEndpoint: mock.s.URL + "/token",
		// Expired one minute ago: TokenSource must refresh before sending.
		TokensExpireAt: time.Now().Add(-time.Minute).UnixMilli(),
		Tokens:         &OAuthTokens{AccessToken: "old", TokenType: "Bearer", RefreshToken: "refresh-0", Scope: "mcp:read"},
	}); err != nil {
		t.Fatal(err)
	}

	handler := newOAuthTransportHandler(flowDeps{store: store, serverName: "docs"}, "docs", mock.serverURL(),
		func() OAuthSettings { return OAuthSettings{} }, nil)
	src, err := handler.TokenSource(context.Background())
	if err != nil || src == nil {
		t.Fatalf("token source: %v %v", src, err)
	}
	token, err := src.Token()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token.AccessToken, "access-") {
		t.Fatalf("expected a refreshed token, got %q", token.AccessToken)
	}
	if _, refreshes, _ := mock.counts(); refreshes != 1 {
		t.Fatalf("refreshes: %d", refreshes)
	}
}

func TestHandlerProviderToken(t *testing.T) {
	calls := 0
	handler := newProviderTokenHandler("docs", "anthropic", func(ctx context.Context) (string, error) {
		calls++
		return "provider-key", nil
	})
	src, err := handler.TokenSource(context.Background())
	if err != nil || src == nil {
		t.Fatalf("token source: %v %v", src, err)
	}
	token, err := src.Token()
	if err != nil || token.AccessToken != "provider-key" {
		t.Fatalf("token: %+v %v", token, err)
	}
	// A 401 surfaces a /login hint, never an OAuth flow.
	resp := &http.Response{StatusCode: 401, Header: http.Header{}}
	err = handler.Authorize(context.Background(), &http.Request{Header: http.Header{}}, resp)
	if !errors.Is(err, ErrSignInRequired) || !strings.Contains(err.Error(), "/login anthropic") {
		t.Fatalf("authorize: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider token calls: %d", calls)
	}
}

func TestManagerNeedsAuthThenSignIn(t *testing.T) {
	mock := newMockOAuthServer(t)
	defer mock.close()
	store := NewCredentialStore(t.TempDir()+"/mcp-auth.json", "")

	manager := NewManager(&Config{MCPServers: map[string]ServerConfig{
		"docs": {URL: mock.serverURL()},
	}}, ManagerOptions{CredentialStore: store, ClientVersion: "test", ConnectTimeout: 5 * time.Second})

	// First use: connect 401s with no stored credentials → needs-auth.
	if _, err := manager.ListServerTools(context.Background(), "docs"); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("expected sign-in-required error, got %v", err)
	}
	status := manager.Status()
	if len(status) != 1 || status[0].Status != "needs-auth" || status[0].OAuthURL == "" {
		t.Fatalf("status: %+v", status)
	}

	// Sign in (simulated browser), then the tools list works and the server
	// is connected.
	prompt := &scriptedPrompt{t: t, mock: mock, browser: true}
	if err := manager.SignIn(context.Background(), "docs", prompt, nil); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	tools, err := manager.ListServerTools(context.Background(), "docs")
	if err != nil || len(tools) != 0 {
		// The mock has no tools/list face; an empty list without error is the
		// connected outcome we assert.
		t.Fatalf("list after sign-in: %v %v", tools, err)
	}
	status = manager.Status()
	if status[0].Status != statusConnected {
		t.Fatalf("status after sign-in: %+v", status)
	}

	// Sign out: credentials removed, connection dropped, needs-auth again.
	removed, err := manager.SignOut("docs")
	if err != nil || !removed {
		t.Fatalf("sign out: %v %v", removed, err)
	}
	if state, _ := store.Load("docs", mock.serverURL()); state != nil {
		t.Fatalf("credentials not removed: %+v", state)
	}
	status = manager.Status()
	if status[0].Status != statusNeedsAuth {
		t.Fatalf("status after sign-out: %+v", status)
	}
}

func TestManagerSignInNonOAuthServer(t *testing.T) {
	manager := NewManager(&Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "echo"},
	}}, ManagerOptions{})
	err := manager.SignIn(context.Background(), "fs", &scriptedPrompt{t: t}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not use OAuth") {
		t.Fatalf("sign-in on stdio server: %v", err)
	}
	if removed, err := manager.SignOut("fs"); err != nil || removed {
		t.Fatalf("sign-out on stdio server: %v %v", removed, err)
	}
}
