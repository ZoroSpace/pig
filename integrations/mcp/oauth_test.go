package mcp

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseTokenResponse(t *testing.T) {
	tokens, err := parseTokenResponse(200, []byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600,"scope":"read","refresh_token":"rt"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tokens.AccessToken != "at" || tokens.RefreshToken != "rt" || tokens.ExpiresIn != 3600 || tokens.Scope != "read" {
		t.Fatalf("tokens: %+v", tokens)
	}

	// OAuth error bodies surface before HTTP status (servers may report
	// errors with any status).
	_, err = parseTokenResponse(400, []byte(`{"error":"invalid_grant","error_description":"token expired"}`))
	var tokenErr *oauthTokenError
	if !errors.As(err, &tokenErr) || tokenErr.invalidGrant() != true {
		t.Fatalf("expected invalid_grant error, got %v", err)
	}
	if tokenErr.Error() != "token expired" {
		t.Fatalf("description: %q", tokenErr.Error())
	}

	// Absent/null/"" expires_in means no expiry (8ce69e9d2; Number(null)=0).
	for _, raw := range []string{`{"access_token":"a","token_type":"B"}`, `{"access_token":"a","token_type":"B","expires_in":null}`, `{"access_token":"a","token_type":"B","expires_in":""}`} {
		tokens, err := parseTokenResponse(200, []byte(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if tokens.ExpiresIn != 0 {
			t.Fatalf("%s: expires_in should be absent, got %d", raw, tokens.ExpiresIn)
		}
	}

	if _, err := parseTokenResponse(200, []byte(`{"access_token":"","token_type":"Bearer"}`)); err == nil {
		t.Fatal("expected error for missing access_token")
	}
	if _, err := parseTokenResponse(500, []byte("boom")); err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestParseChallenge(t *testing.T) {
	challenge := parseChallenge([]string{`Bearer realm="mcp", error="insufficient_scope", scope="read write", resource_metadata="https://as.example.com/.well-known/oauth-protected-resource"`})
	if challenge.Error != "insufficient_scope" || challenge.Scope != "read write" {
		t.Fatalf("challenge: %+v", challenge)
	}
	if challenge.ResourceMetadataURL != "https://as.example.com/.well-known/oauth-protected-resource" {
		t.Fatalf("resource metadata URL: %q", challenge.ResourceMetadataURL)
	}
	if got := parseChallenge([]string{`Basic realm="x"`}); got.Error != "" {
		t.Fatalf("non-bearer challenge should be ignored: %+v", got)
	}
	if got := parseChallenge(nil); got != (OAuthChallenge{}) {
		t.Fatalf("no header: %+v", got)
	}
}

func TestSelectResource(t *testing.T) {
	if resource, err := selectResource("https://mcp.example.com/mcp", ""); err != nil || resource != "" {
		t.Fatalf("no PRM: %q %v", resource, err)
	}
	resource, err := selectResource("https://mcp.example.com/mcp", "https://mcp.example.com/mcp")
	if err != nil || resource != "https://mcp.example.com/mcp" {
		t.Fatalf("exact match: %q %v", resource, err)
	}
	if _, err := selectResource("https://mcp.example.com/mcp", "https://other.example.com/mcp"); err == nil {
		t.Fatal("origin mismatch should error")
	}
	if _, err := selectResource("https://mcp.example.com/other/path", "https://mcp.example.com/mcp"); err == nil {
		t.Fatal("path prefix mismatch should error")
	}
	if resource, err := selectResource("https://mcp.example.com/mcp/deep", "https://mcp.example.com/mcp"); err != nil || resource == "" {
		t.Fatalf("nested path should match: %q %v", resource, err)
	}
}

func TestTokenAuthMethod(t *testing.T) {
	cases := []struct {
		info      *OAuthClientInformation
		supported []string
		want      string
	}{
		{nil, nil, "none"},
		{&OAuthClientInformation{ClientID: "c"}, []string{"none"}, "none"},
		{&OAuthClientInformation{ClientID: "c", ClientSecret: "s"}, nil, "client_secret_basic"},
		{&OAuthClientInformation{ClientID: "c", ClientSecret: "s"}, []string{"client_secret_post", "none"}, "client_secret_post"},
		{&OAuthClientInformation{ClientID: "c", ClientSecret: "s"}, []string{"none", "client_secret_basic"}, "client_secret_basic"},
		{&OAuthClientInformation{ClientID: "c", TokenAuthMethod: "none"}, []string{"client_secret_basic"}, "none"},
	}
	for i, tc := range cases {
		if got := tokenAuthMethod(tc.info, tc.supported); got != tc.want {
			t.Fatalf("case %d: got %q want %q", i, got, tc.want)
		}
	}
}

func TestApplyTokenAuth(t *testing.T) {
	headers := map[string]string{}
	params := url.Values{}
	if err := applyTokenAuth("client_secret_basic", &OAuthClientInformation{ClientID: "c", ClientSecret: "s"}, headers, params); err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] == "" || params.Get("client_id") != "" {
		t.Fatalf("basic auth: headers=%v params=%v", headers, params)
	}
	headers, params = map[string]string{}, url.Values{}
	if err := applyTokenAuth("none", &OAuthClientInformation{ClientID: "c"}, headers, params); err != nil {
		t.Fatal(err)
	}
	if params.Get("client_id") != "c" {
		t.Fatalf("public client must identify via params: %v", params)
	}
	if err := applyTokenAuth("client_secret_basic", &OAuthClientInformation{ClientID: "c"}, map[string]string{}, url.Values{}); err == nil {
		t.Fatal("basic without secret should error")
	}
}

func TestScopeHelpers(t *testing.T) {
	if got := mergeScopes("read write", "write admin"); got != "read write admin" {
		t.Fatalf("mergeScopes: %q", got)
	}
	if got := stepUpScope("", "read"); got != "read" {
		t.Fatalf("stepUp no granted: %q", got)
	}
	if got := stepUpScope("read write", "admin"); got != "read write admin" {
		t.Fatalf("stepUp union: %q", got)
	}
	if got := stepUpScope("read", ""); got != "" {
		t.Fatalf("no challenge: %q", got)
	}
	if got := withScope(&OAuthTokens{AccessToken: "a"}, "requested"); got.Scope != "requested" {
		t.Fatalf("withScope fills absent scope: %+v", got)
	}
	if got := withScope(&OAuthTokens{AccessToken: "a", Scope: "granted"}, "requested"); got.Scope != "granted" {
		t.Fatalf("withScope keeps granted scope: %+v", got)
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	raw, err := buildAuthorizationURL("https://as.example.com/authorize", "client-1", "challenge", "http://127.0.0.1:9999/callback", "state123", "read write", "https://mcp.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "https" || u.Host != "as.example.com" || u.Path != "/authorize" {
		t.Fatalf("base: %s", raw)
	}
	for key, want := range map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"code_challenge":        "challenge",
		"code_challenge_method": "S256",
		"redirect_uri":          "http://127.0.0.1:9999/callback",
		"state":                 "state123",
		"scope":                 "read write",
		"resource":              "https://mcp.example.com/mcp",
	} {
		if q.Get(key) != want {
			t.Fatalf("%s: got %q want %q", key, q.Get(key), want)
		}
	}
	if q.Get("prompt") != "" {
		t.Fatalf("prompt without offline_access: %v", q)
	}
	raw, _ = buildAuthorizationURL("https://as.example.com/authorize", "c", "ch", "http://127.0.0.1/callback", "s", "offline_access", "")
	if !strings.Contains(raw, "prompt=consent") {
		t.Fatalf("offline_access must set prompt=consent: %s", raw)
	}
}

func TestSecureEndpoint(t *testing.T) {
	if _, err := secureEndpoint("http://api.example.com/token"); err == nil {
		t.Fatal("non-https remote endpoint must be refused")
	}
	for _, ok := range []string{"https://api.example.com/token", "http://127.0.0.1:9000/token", "http://localhost/token"} {
		if _, err := secureEndpoint(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}

func TestResponseFromRedirectURL(t *testing.T) {
	const redirect = "http://127.0.0.1:8765/callback"
	cb, err := responseFromRedirectURL("http://127.0.0.1:8765/callback?code=xyz&state=st1", "st1", redirect)
	if err != nil || cb.Code != "xyz" {
		t.Fatalf("valid redirect: %+v %v", cb, err)
	}
	cb, err = responseFromRedirectURL("  http://127.0.0.1:8765/callback?code=xyz&state=st1&iss=https://as  ", "st1", redirect)
	if err != nil || cb.Iss != "https://as" {
		t.Fatalf("iss param: %+v %v", cb, err)
	}
	if _, err := responseFromRedirectURL("http://127.0.0.1:8765/other?code=x&state=st1", "st1", redirect); err == nil {
		t.Fatal("path mismatch must error")
	}
	if _, err := responseFromRedirectURL("http://127.0.0.1:8765/callback?code=x&state=other", "st1", redirect); err == nil {
		t.Fatal("state mismatch must error")
	}
	if _, err := responseFromRedirectURL("http://127.0.0.1:8765/callback?state=st1", "st1", redirect); err == nil {
		t.Fatal("missing code must error")
	}
	if _, err := responseFromRedirectURL("http://127.0.0.1:8765/callback?error=access_denied", "st1", redirect); err == nil || err.Error() != "access_denied" {
		t.Fatalf("error response: %v", err)
	}
}

func TestCallbackServerRouting(t *testing.T) {
	server, err := ListenOAuthCallback("127.0.0.1", "localhost", 0, "/callback", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if !strings.Contains(server.RedirectURL(), "localhost:") || !strings.HasSuffix(server.RedirectURL(), "/callback") {
		t.Fatalf("redirect URL: %s", server.RedirectURL())
	}

	// Success path: code + iss delivered, routed by state.
	pending := server.WaitForCallback("state-1", "/callback", 5*time.Second)
	redirect := server.RedirectURL() + "?code=c1&state=state-1&iss=https://as"
	resp, err := http.Get(redirect) //nolint:noctx // test-local server
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cb, err := awaitCallback(pending)
	if err != nil || cb.Code != "c1" || cb.Iss != "https://as" {
		t.Fatalf("callback: %+v %v", cb, err)
	}

	// Unknown state → 400.
	resp, err = http.Get(server.RedirectURL() + "?code=x&state=unknown") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("unknown state status: %d", resp.StatusCode)
	}

	// Authorization error response → empty callback (caller surfaces the
	// error via the page).
	pending = server.WaitForCallback("state-2", "/callback", 5*time.Second)
	resp, _ = http.Get(server.RedirectURL() + "?error=access_denied&error_description=nope&state=state-2") //nolint:noctx
	resp.Body.Close()
	if cb, err := awaitCallback(pending); err == nil {
		t.Fatalf("error response should not deliver a code: %+v", cb)
	}

	// Wrong path → not delivered; the pending state survives for the real
	// redirect URI.
	pending = server.WaitForCallback("state-3", "/callback", 5*time.Second)
	resp, _ = http.Get(strings.Replace(server.RedirectURL(), "/callback", "/other", 1) + "?code=x&state=state-3") //nolint:noctx
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("unregistered path status: %d", resp.StatusCode)
	}
	resp, _ = http.Get(server.RedirectURL() + "?code=c3&state=state-3") //nolint:noctx
	resp.Body.Close()
	if cb, err := awaitCallback(pending); err != nil || cb.Code != "c3" {
		t.Fatalf("delivery after wrong-path attempt: %+v %v", cb, err)
	}

	// Close fails pending waits.
	pending = server.WaitForCallback("state-4", "/callback", 5*time.Second)
	if err := server.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := awaitCallback(pending); err == nil {
		t.Fatal("pending wait should fail on close")
	}
}

func TestDiscoveryStatusAndMiss(t *testing.T) {
	status, ok := discoveryStatus(errors.New("GetProtectedResourceMetadata(\"x\"): bad status 404"))
	if !ok || status != 404 {
		t.Fatalf("discoveryStatus: %d %v", status, ok)
	}
	if !isDiscoveryMiss(errors.New("bad status 502")) || !isDiscoveryMiss(errors.New("bad status 404")) {
		t.Fatal("4xx and 502 are discovery misses")
	}
	if isDiscoveryMiss(errors.New("bad status 500")) || isDiscoveryMiss(errors.New("metadata issuer mismatch")) {
		t.Fatal("500 and validation errors are not misses")
	}
}
