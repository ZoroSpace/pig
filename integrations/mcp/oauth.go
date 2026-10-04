package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// OAuth authorization-code flow for remote MCP servers, ported from pi
// (packages/mcp/src/oauth/{flow,discovery,errors}.ts + extensions/mcp/
// oauth.ts, which adapt modelcontextprotocol/typescript-sdk). Discovery and
// registration primitives come from go-sdk's oauthex package; the flow
// orchestration is pig-native so pi's semantics survive: connections never
// start a browser flow on their own — the transport handler (oauth_handler.go)
// only refreshes stored tokens and otherwise fails with an error wrapping
// ErrSignInRequired, and the user signs in through /mcp (SignInServer in
// oauth_signin.go).

// ErrSignInRequired reports that a server needs an interactive sign-in
// (stored credentials missing, expired, or rejected). The manager maps it to
// the "needs-auth" server status.
var ErrSignInRequired = errors.New("requires sign-in")

func signInRequiredError(server string) error {
	return fmt.Errorf("MCP server %q requires sign-in. Run /mcp to sign in: %w", server, ErrSignInRequired)
}

func providerSignInRequiredError(server, provider string) error {
	return fmt.Errorf("MCP server %q requires sign-in. Run /login %s to sign in: %w", server, provider, ErrSignInRequired)
}

// OAuthSettings are the per-server oauth.* config values with the client
// secret already resolved.
type OAuthSettings struct {
	ClientID     string
	ClientSecret string
	// CallbackPort pins the loopback callback server's port (0 = free port).
	CallbackPort int
	// CallbackURL is a registered loopback redirect URI sent exactly as
	// written; empty = the callback server's dynamic URL.
	CallbackURL string
	Scope       string
	ClientName  string
	// AuthServerMetadataURL overrides discovery (pi d850edee9): the document
	// is trusted as configured and its issuer is not checked.
	AuthServerMetadataURL string
}

// OAuthChallenge is the parsed WWW-Authenticate challenge of the 401/403 that
// asked for (re-)authorization (pi: OAuthChallenge).
type OAuthChallenge struct {
	ResourceMetadataURL string
	Scope               string
	Error               string
	ErrorDescription    string
}

// SignInPrompt is the user-interaction channel of a browser sign-in (pi:
// McpSignInPrompt). ShowAuthorizationURL displays the URL and opens the
// browser. PromptForRedirectURL asks for the browser-address-bar URL for when
// the browser cannot reach the loopback callback (SSH); it must return
// promptly once ctx is cancelled — the callback arrival cancels it.
type SignInPrompt interface {
	ShowAuthorizationURL(url string)
	PromptForRedirectURL(ctx context.Context) (string, error)
}

// ErrSignInCancelled reports that the user cancelled a sign-in (pi:
// McpSignInCancelledError).
var ErrSignInCancelled = errors.New("sign-in cancelled")

// oauthTokenError is a token-endpoint or registration failure (pi:
// OAuthError/OAuthRegistrationError).
type oauthTokenError struct {
	code            string
	description     string
	uri             string
	status          int
	body            string
	registrationErr bool
}

func (e *oauthTokenError) Error() string {
	if e.registrationErr {
		return fmt.Sprintf("OAuth dynamic client registration failed with status %d: %s", e.status, e.body)
	}
	if e.description != "" && e.description != e.code {
		return e.description
	}
	return e.code
}

// invalidGrant reports that the server rejected the grant itself (RFC 6749
// §5.2) — the stored tokens are dead.
func (e *oauthTokenError) invalidGrant() bool { return e.code == "invalid_grant" }

// invalidClient reports that the client registration was rejected — the
// stored client information is dead (pi invalidates "all" and retries).
func (e *oauthTokenError) invalidClient() bool {
	return e.code == "invalid_client" || e.code == "unauthorized_client"
}

// ---- endpoints ----

func isLoopbackHost(hostname string) bool {
	hostname = strings.Trim(hostname, "[]")
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

// secureEndpoint refuses to send OAuth credentials to a non-HTTPS endpoint
// (loopback excepted; pi: secureEndpoint).
func secureEndpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth endpoint %q: %w", raw, err)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("refusing to send OAuth credentials to non-HTTPS endpoint %s", raw)
	}
	return u, nil
}

func pathSuffix(pathname string) string {
	return strings.TrimSuffix(pathname, "/")
}

// ---- WWW-Authenticate challenge parsing (pi: parseWwwAuthenticate) ----

func parseChallenge(headers []string) OAuthChallenge {
	challenges, err := oauthex.ParseWWWAuthenticate(headers)
	if err != nil {
		return OAuthChallenge{}
	}
	for _, c := range challenges {
		if c.Scheme != "bearer" && c.Scheme != "dpop" {
			continue
		}
		challenge := OAuthChallenge{
			ResourceMetadataURL: c.Params["resource_metadata"],
			Scope:               c.Params["scope"],
			Error:               c.Params["error"],
			ErrorDescription:    c.Params["error_description"],
		}
		if challenge.ResourceMetadataURL != "" {
			if u, err := url.Parse(challenge.ResourceMetadataURL); err != nil || u.Scheme == "" || u.Host == "" {
				challenge.ResourceMetadataURL = ""
			}
		}
		return challenge
	}
	return OAuthChallenge{}
}

// ---- discovery (pi: discovery.ts, on go-sdk oauthex primitives) ----

// oauthEndpoints is the discovered/selected set of authorization server
// endpoints used by the flow.
type oauthEndpoints struct {
	AuthURL                           string
	TokenURL                          string
	RegistrationURL                   string
	Issuer                            string
	TokenEndpointAuthMethods          []string
	AuthorizationResponseIssParameter bool
}

func endpointsFromMeta(asm *oauthex.AuthServerMeta) *oauthEndpoints {
	return &oauthEndpoints{
		AuthURL:                           asm.AuthorizationEndpoint,
		TokenURL:                          asm.TokenEndpoint,
		RegistrationURL:                   asm.RegistrationEndpoint,
		Issuer:                            asm.Issuer,
		TokenEndpointAuthMethods:          asm.TokenEndpointAuthMethodsSupported,
		AuthorizationResponseIssParameter: asm.AuthorizationResponseIssParameterSupported,
	}
}

// discoveryStatus extracts the HTTP status from an oauthex discovery error
// (getJSON hides it behind `bad status %d`); ok=false when the error is not a
// status error.
func discoveryStatus(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	msg := err.Error()
	idx := strings.Index(msg, "bad status ")
	if idx < 0 {
		return 0, false
	}
	code, _, _ := strings.Cut(msg[idx+len("bad status "):], " ")
	status, convErr := strconv.Atoi(strings.TrimSpace(code))
	return status, convErr == nil
}

// isDiscoveryMiss reports whether a discovery error means "not here" (4xx and
// 502; pi: isDiscoveryMiss) versus a real failure.
func isDiscoveryMiss(err error) bool {
	status, ok := discoveryStatus(err)
	return ok && ((status >= 400 && status < 500) || status == 502)
}

// discoverProtectedResourceMetadata tries the challenge-advertised URL, then
// the path-suffixed well-known URL, then the root one (pi:
// discoverProtectedResourceMetadata). A nil result with nil error means no
// metadata was found anywhere.
func discoverProtectedResourceMetadata(ctx context.Context, serverURL string, challenge OAuthChallenge, client *http.Client) (*oauthex.ProtectedResourceMetadata, error) {
	server, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP server URL %q: %w", serverURL, err)
	}
	var candidates []string
	if challenge.ResourceMetadataURL != "" {
		candidates = append(candidates, challenge.ResourceMetadataURL)
	}
	if server.Path != "" && server.Path != "/" {
		u := *server
		u.Path = "/.well-known/oauth-protected-resource" + pathSuffix(server.Path)
		u.RawQuery = ""
		candidates = append(candidates, u.String())
	}
	root := *server
	root.Path = ""
	root.RawPath = ""
	root.RawQuery = ""
	root.Fragment = ""
	candidates = append(candidates, root.Scheme+"://"+root.Host+"/.well-known/oauth-protected-resource")

	var lastErr error
	for _, candidate := range candidates {
		prm, err := oauthex.GetProtectedResourceMetadata(ctx, candidate, serverURL, client)
		if err != nil {
			if isDiscoveryMiss(err) {
				lastErr = err
				continue
			}
			return nil, err
		}
		if len(prm.AuthorizationServers) == 0 {
			return nil, errors.New("protected resource metadata has no authorization servers specified")
		}
		return prm, nil
	}
	return nil, lastErr
}

// discoveryOutcome bundles what resolveEndpoints learned.
type discoveryOutcome struct {
	endpoints *oauthEndpoints
	resource  string
	// scopesSupported is the protected resource metadata's scopes_supported,
	// a scope source when neither the config nor the challenge names one.
	scopesSupported []string
}

// resolveEndpoints walks the discovery chain (or takes the configured
// metadata document) and selects the RFC 8707 resource indicator. With no PRM
// anywhere, the server root doubles as the authorization server (2025-03-26
// fallback; pi: discoverOAuthServerInfo's catch-and-continue).
func resolveEndpoints(ctx context.Context, serverURL string, settings OAuthSettings, challenge OAuthChallenge, client *http.Client) (discoveryOutcome, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var prm *oauthex.ProtectedResourceMetadata
	prm, prmErr := discoverProtectedResourceMetadata(ctx, serverURL, challenge, client)
	if prmErr != nil && !isDiscoveryMiss(prmErr) {
		return discoveryOutcome{}, prmErr
	}
	resource, err := selectResource(serverURL, prmResource(prm))
	if err != nil {
		return discoveryOutcome{}, err
	}

	if settings.AuthServerMetadataURL != "" {
		// Trusted as configured: the document's issuer is not checked (pi
		// d850edee9), but the URL itself must be secure.
		if _, err := secureEndpoint(settings.AuthServerMetadataURL); err != nil {
			return discoveryOutcome{}, err
		}
		asm, err := oauthex.GetAuthServerMeta(ctx, settings.AuthServerMetadataURL, settings.AuthServerMetadataURL, client)
		if err != nil {
			return discoveryOutcome{}, fmt.Errorf("loading authorization server metadata: %w", err)
		}
		if asm == nil {
			return discoveryOutcome{}, fmt.Errorf("HTTP 4xx loading authorization server metadata from %s", settings.AuthServerMetadataURL)
		}
		return discoveryOutcome{endpoints: endpointsFromMeta(asm), resource: resource}, nil
	}

	asURL := ""
	if prm != nil && len(prm.AuthorizationServers) > 0 {
		asURL = prm.AuthorizationServers[0]
	} else {
		server, err := url.Parse(serverURL)
		if err != nil {
			return discoveryOutcome{}, err
		}
		asURL = server.Scheme + "://" + server.Host
	}
	asm, err := discoverAuthServerMeta(ctx, asURL, client)
	if err != nil {
		return discoveryOutcome{}, err
	}
	out := discoveryOutcome{resource: resource, scopesSupported: prmScopesSupported(prm)}
	if asm != nil {
		out.endpoints = endpointsFromMeta(asm)
		// Identity is the URL the metadata was found under; keep it even when
		// the document spells the issuer differently (pi trims trailing
		// slashes on both sides for the equality check, which
		// GetAuthServerMeta already enforced per candidate URL).
		out.endpoints.Issuer = asURL
	} else {
		// 2025-03-26 fallback: predefined endpoints on the server root.
		out.endpoints = &oauthEndpoints{
			AuthURL:         asURL + "/authorize",
			TokenURL:        asURL + "/token",
			RegistrationURL: asURL + "/register",
			Issuer:          asURL,
		}
	}
	return out, nil
}

func prmResource(prm *oauthex.ProtectedResourceMetadata) string {
	if prm == nil {
		return ""
	}
	return prm.Resource
}

func prmScopesSupported(prm *oauthex.ProtectedResourceMetadata) []string {
	if prm == nil {
		return nil
	}
	return prm.ScopesSupported
}

// discoverAuthServerMeta tries the RFC 8414 / OIDC discovery URL pairs (pi:
// discoverAuthorizationServerMetadata + buildAuthorizationServerDiscoveryUrls).
// A nil result with nil error means the server publishes no metadata.
func discoverAuthServerMeta(ctx context.Context, asURL string, client *http.Client) (*oauthex.AuthServerMeta, error) {
	base, err := url.Parse(asURL)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization server URL %q: %w", asURL, err)
	}
	suffix := pathSuffix(base.Path)
	candidates := []string{
		base.Scheme + "://" + base.Host + "/.well-known/oauth-authorization-server" + suffix,
		base.Scheme + "://" + base.Host + "/.well-known/openid-configuration" + suffix,
	}
	if suffix != "" {
		candidates = append(candidates, base.Scheme+"://"+base.Host+suffix+"/.well-known/openid-configuration")
	}
	var lastErr error
	for _, candidate := range candidates {
		asm, err := oauthex.GetAuthServerMeta(ctx, candidate, asURL, client)
		if err != nil {
			if isDiscoveryMiss(err) {
				lastErr = err
				continue
			}
			return nil, err // issuer mismatch, missing PKCE, bad schemes: real errors
		}
		return asm, nil
	}
	return nil, lastErr
}

// selectResource validates the PRM resource against the server URL (pi:
// selectResource) and returns it as the resource indicator.
func selectResource(serverURL string, resource string) (string, error) {
	if resource == "" {
		return "", nil
	}
	requested, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	configured, err := url.Parse(resource)
	if err != nil {
		return "", fmt.Errorf("invalid protected resource %q: %w", resource, err)
	}
	if requested.Scheme != configured.Scheme || requested.Host != configured.Host {
		return "", fmt.Errorf("protected resource %s does not match MCP server %s", resource, serverURL)
	}
	if !strings.HasPrefix(withTrailingSlash(requested.Path), withTrailingSlash(configured.Path)) {
		return "", fmt.Errorf("protected resource %s does not match MCP server %s", resource, serverURL)
	}
	return resource, nil
}

func withTrailingSlash(path string) string {
	if strings.HasSuffix(path, "/") {
		return path
	}
	return path + "/"
}

// ---- scopes (pi: mergeScopes / stepUpScope / withScope) ----

// mergeScopes returns the scopes of all lists, each once.
func mergeScopes(scopes ...string) string {
	seen := map[string]bool{}
	var merged []string
	for _, scope := range scopes {
		for _, s := range strings.Fields(scope) {
			if !seen[s] {
				seen[s] = true
				merged = append(merged, s)
			}
		}
	}
	return strings.Join(merged, " ")
}

// stepUpScope unions the granted and challenged scopes: a challenge may list
// only the missing scopes, and a token with just those would lose access the
// old one had (SEP-2350; pi: stepUpScope).
func stepUpScope(granted, challenged string) string {
	if challenged == "" {
		return ""
	}
	return mergeScopes(granted, challenged)
}

// withScope records the requested scope when the response omits it: a token
// response without `scope` grants the requested scope (RFC 6749 §5.1); a
// refresh without `scope` keeps the grant's scope (RFC 6749 §6).
func withScope(tokens *OAuthTokens, scope string) *OAuthTokens {
	if tokens != nil && tokens.Scope == "" && scope != "" {
		tokens.Scope = scope
	}
	return tokens
}

// ---- PKCE and state ----

func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomHex() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

// ---- client authentication at the token endpoint (pi:
// selectClientAuthMethod / applyClientAuthentication) ----

func tokenAuthMethod(info *OAuthClientInformation, supported []string) string {
	hinted := ""
	if info != nil {
		hinted = info.TokenAuthMethod
	}
	if (hinted == "client_secret_basic" || hinted == "client_secret_post" || hinted == "none") &&
		(len(supported) == 0 || slices.Contains(supported, hinted)) {
		return hinted
	}
	hasSecret := info != nil && info.ClientSecret != ""
	switch {
	case len(supported) == 0:
		if hasSecret {
			return "client_secret_basic"
		}
		return "none"
	case hasSecret && slices.Contains(supported, "client_secret_basic"):
		return "client_secret_basic"
	case hasSecret && slices.Contains(supported, "client_secret_post"):
		return "client_secret_post"
	case slices.Contains(supported, "none"):
		return "none"
	case hasSecret:
		return "client_secret_post"
	default:
		return "none"
	}
}

// applyTokenAuth writes the client authentication into the request.
func applyTokenAuth(method string, info *OAuthClientInformation, headers map[string]string, params url.Values) error {
	switch method {
	case "client_secret_basic":
		if info == nil || info.ClientSecret == "" {
			return errors.New("client_secret_basic requires a client secret")
		}
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(info.ClientID+":"+info.ClientSecret))
	default: // "none" and "client_secret_post" identify via params
		if info != nil {
			params.Set("client_id", info.ClientID)
			if method == "client_secret_post" && info.ClientSecret != "" {
				params.Set("client_secret", info.ClientSecret)
			}
		}
	}
	return nil
}

// ---- token responses (pi: parseOAuthTokens) ----

// parseTokenResponse decodes a token endpoint response. OAuth error bodies are
// surfaced before HTTP status: servers may report errors with any status (pi:
// tokenRequest). Absent/null/"" expires_in means no expiry (`Number(null)` is
// 0 in JS, which would mark the token expired at once — 8ce69e9d2 semantics).
func parseTokenResponse(status int, body []byte) (*OAuthTokens, error) {
	var value struct {
		Error            string          `json:"error"`
		ErrorDescription string          `json:"error_description"`
		ErrorURI         string          `json:"error_uri"`
		AccessToken      string          `json:"access_token"`
		TokenType        string          `json:"token_type"`
		ExpiresIn        json.RawMessage `json:"expires_in"`
		Scope            string          `json:"scope"`
		RefreshToken     string          `json:"refresh_token"`
		IDToken          string          `json:"id_token"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, &oauthTokenError{code: "server_error", status: status, body: string(body),
			description: fmt.Sprintf("HTTP %d: %s", status, truncateBody(body))}
	}
	if value.Error != "" {
		return nil, &oauthTokenError{code: value.Error,
			description: orDefault(value.ErrorDescription, value.Error), uri: value.ErrorURI}
	}
	if status < 200 || status >= 300 {
		return nil, &oauthTokenError{code: "server_error", status: status, body: string(body),
			description: fmt.Sprintf("HTTP %d: %s", status, truncateBody(body))}
	}
	if value.AccessToken == "" || value.TokenType == "" {
		return nil, &oauthTokenError{code: "server_error", status: status, body: string(body),
			description: "invalid OAuth token response"}
	}
	tokens := &OAuthTokens{
		AccessToken:  value.AccessToken,
		TokenType:    value.TokenType,
		Scope:        value.Scope,
		RefreshToken: value.RefreshToken,
		IDToken:      value.IDToken,
	}
	var expires float64
	if len(value.ExpiresIn) > 0 && json.Unmarshal(value.ExpiresIn, &expires) == nil && expires > 0 {
		tokens.ExpiresIn = int64(expires)
	}
	return tokens, nil
}

func truncateBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 512 {
		return text[:512]
	}
	return text
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// ---- token requests (pi: tokenRequest / exchangeAuthorizationCode /
// refreshAuthorization) ----

// tokenRequest POSTs one token-endpoint request.
func tokenRequest(ctx context.Context, endpoint string, headers map[string]string, params url.Values, client *http.Client) (*OAuthTokens, error) {
	if _, err := secureEndpoint(endpoint); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseTokenResponse(resp.StatusCode, body)
}

// exchangeAuthorizationCode exchanges the authorization code for tokens.
func exchangeAuthorizationCode(ctx context.Context, endpoints *oauthEndpoints, info *OAuthClientInformation, code, verifier, redirectURL, resource string, client *http.Client) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURL},
	}
	if resource != "" {
		params.Set("resource", resource)
	}
	headers := map[string]string{}
	if err := applyTokenAuth(tokenAuthMethod(info, endpoints.TokenEndpointAuthMethods), info, headers, params); err != nil {
		return nil, err
	}
	return tokenRequest(ctx, endpoints.TokenURL, headers, params, client)
}

// refreshTokens exchanges a refresh token for tokens.
func refreshTokens(ctx context.Context, endpoints *oauthEndpoints, info *OAuthClientInformation, refreshToken, resource string, client *http.Client) (*OAuthTokens, error) {
	params := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	if resource != "" {
		params.Set("resource", resource)
	}
	headers := map[string]string{}
	if err := applyTokenAuth(tokenAuthMethod(info, endpoints.TokenEndpointAuthMethods), info, headers, params); err != nil {
		return nil, err
	}
	tokens, err := tokenRequest(ctx, endpoints.TokenURL, headers, params, client)
	if err != nil {
		return nil, err
	}
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}
	return tokens, nil
}

// registerClient runs dynamic client registration (pi: registerClient).
func registerClient(ctx context.Context, endpoint string, metadata *oauthex.ClientRegistrationMetadata, client *http.Client) (*OAuthClientInformation, error) {
	if _, err := secureEndpoint(endpoint); err != nil {
		return nil, err
	}
	resp, err := oauthex.RegisterClient(ctx, endpoint, metadata, client)
	if err != nil {
		status, body := registrationFailure(err)
		if status > 0 {
			return nil, &oauthTokenError{status: status, body: body, registrationErr: true}
		}
		return nil, err
	}
	info := &OAuthClientInformation{
		ClientID:         resp.ClientID,
		ClientSecret:     resp.ClientSecret,
		TokenAuthMethod:  resp.TokenEndpointAuthMethod,
		RedirectURIs:     resp.RedirectURIs,
		ClientIDIssuedAt: resp.ClientIDIssuedAt.Unix(),
	}
	if info.TokenAuthMethod == "" {
		info.TokenAuthMethod = orDefault2(info.ClientSecret != "", "client_secret_post", "none")
	}
	return info, nil
}

func orDefault2(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// registrationFailure digs an HTTP status out of an oauthex.RegisterClient
// error when there is one (its message embeds `bad status %d`).
func registrationFailure(err error) (int, string) {
	msg := err.Error()
	if idx := strings.Index(msg, "bad status "); idx >= 0 {
		code, _, _ := strings.Cut(msg[idx+len("bad status "):], " ")
		status, convErr := strconv.Atoi(strings.TrimSpace(code))
		if convErr == nil {
			return status, msg
		}
	}
	return 0, ""
}

// ---- the flow ----

// flowDeps carries the per-server context of one flow run.
type flowDeps struct {
	store      *CredentialStore
	serverName string
	client     *http.Client // nil = default
}

// flowInput is one authorization attempt's inputs.
type flowInput struct {
	serverURL string
	settings  OAuthSettings
	challenge OAuthChallenge
	// state is the loaded stored state; runFlow mutates and saves it.
	state *OAuthServerState
	// skipRefresh goes straight to the authorization redirect, for step-up:
	// a refresh keeps the old scope (pi: skipRefresh).
	skipRefresh bool
	// scope is the challenged/requested scope for this run (pi:
	// options.scope); empty falls through to PRM scopes_supported and then
	// the configured scope.
	scope string
	// redirectURL is where the callback is served (sign-in) or the registered
	// redirect URI (refresh path bookkeeping).
	redirectURL string
}

// pendingAuth is the in-memory state of a started browser authorization,
// kept for the code exchange once the callback arrives (pi persists
// codeVerifier/oauthState; pig keeps them live for the sign-in's duration).
type pendingAuth struct {
	authURL   string
	endpoints *oauthEndpoints
	resource  string
	scope     string
	verifier  string
	state     string
}

// flowKind mirrors pi's OAuthFlowResult plus the non-interactive outcome.
type flowKind int

const (
	flowAuthorized flowKind = iota
	flowRedirect            // browser flow started; auth URL produced
)

// runFlow executes the authorization flow (pi: runFlow + authorizeMcp retry
// wrapper). On flowAuthorized the stored state has fresh tokens. The
// pending/out parameters carry what a following code exchange needs.
func runFlow(ctx context.Context, deps flowDeps, input flowInput, allowRedirect bool) (flowKind, *pendingAuth, error) {
	for attempt := 0; ; attempt++ {
		kind, pending, err := runFlowOnce(ctx, deps, input, allowRedirect)
		if err == nil {
			return kind, pending, nil
		}
		var tokenErr *oauthTokenError
		if attempt == 0 && errors.As(err, &tokenErr) {
			if tokenErr.invalidClient() {
				// The stored client registration is dead: forget everything
				// and retry once (pi: invalidateCredentials("all")).
				input.state.ClientInformation = nil
				input.state.Tokens = nil
				input.state.TokensExpireAt = 0
				_ = deps.store.Save(deps.serverName, input.serverURL, input.state)
				continue
			}
			if tokenErr.invalidGrant() {
				// The grant is dead: forget the tokens and retry once (pi:
				// invalidateCredentials("tokens")).
				input.state.Tokens = nil
				input.state.TokensExpireAt = 0
				_ = deps.store.Save(deps.serverName, input.serverURL, input.state)
				continue
			}
		}
		return 0, nil, err
	}
}

func runFlowOnce(ctx context.Context, deps flowDeps, input flowInput, allowRedirect bool) (flowKind, *pendingAuth, error) {
	outcome, err := resolveEndpoints(ctx, input.serverURL, input.settings, input.challenge, deps.client)
	if err != nil {
		return 0, nil, err
	}
	state := input.state

	// pi: options.scope || prm.scopes_supported || provider.clientMetadata.scope.
	scope := input.scope
	if scope == "" && len(outcome.scopesSupported) > 0 {
		scope = strings.Join(outcome.scopesSupported, " ")
	}
	if scope == "" {
		scope = input.settings.Scope
	}

	client := state.ClientInformation
	if client == nil && input.settings.ClientID != "" {
		client = &OAuthClientInformation{ClientID: input.settings.ClientID, ClientSecret: input.settings.ClientSecret}
	}

	// Refresh path: a valid refresh token keeps the grant alive without user
	// interaction (skipped for step-up — a refresh cannot widen scope).
	if !input.skipRefresh && state.Tokens != nil && state.Tokens.RefreshToken != "" {
		tokens, err := refreshTokens(ctx, outcome.endpoints, client, state.Tokens.RefreshToken, outcome.resource, deps.client)
		if err == nil {
			saveTokens(deps, input, state, withScope(tokens, state.Tokens.Scope), outcome, scope)
			return flowAuthorized, nil, nil
		}
		var tokenErr *oauthTokenError
		if errors.As(err, &tokenErr) {
			if !tokenErr.invalidGrant() && !tokenErr.invalidClient() && tokenErr.code != "server_error" {
				// e.g. an insecure endpoint: retrying cannot help.
				return 0, nil, err
			}
			// server_error falls through to the browser flow; grant/client
			// errors are retried by the runFlow wrapper.
			if tokenErr.code == "server_error" {
				// pi lets server_error fall through to the redirect path.
			}
		} else {
			return 0, nil, err
		}
	}

	if !allowRedirect {
		return 0, nil, signInRequiredError(deps.serverName)
	}

	// Browser flow: register when needed, then build the authorization URL.
	if client == nil {
		if outcome.endpoints.RegistrationURL == "" {
			return 0, nil, errors.New("authorization server does not support dynamic client registration")
		}
		metadata := &oauthex.ClientRegistrationMetadata{
			ClientName:              orDefault(input.settings.ClientName, "pig"),
			RedirectURIs:            []string{input.redirectURL},
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		}
		if scope != "" {
			metadata.Scope = scope
		}
		registered, err := registerClient(ctx, outcome.endpoints.RegistrationURL, metadata, deps.client)
		if err != nil {
			return 0, nil, err
		}
		state.ClientInformation = registered
		client = registered
		_ = deps.store.Save(deps.serverName, input.serverURL, state)
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return 0, nil, err
	}
	flowState := randomHex()
	authURL, err := buildAuthorizationURL(outcome.endpoints.AuthURL, client.ClientID, challenge, input.redirectURL, flowState, scope, outcome.resource)
	if err != nil {
		return 0, nil, err
	}
	pending := &pendingAuth{
		authURL:   authURL,
		endpoints: outcome.endpoints,
		resource:  outcome.resource,
		scope:     scope,
		verifier:  verifier,
		state:     flowState,
	}
	return flowRedirect, pending, nil
}

// completeExchange exchanges a callback's authorization code (pi: the
// authorizeMcp call with authorizationCode). On a dead client registration it
// re-registers once and retries (pi: invalidateCredentials("all") + re-run).
func completeExchange(ctx context.Context, deps flowDeps, input flowInput, pending *pendingAuth, code, iss string) error {
	state := input.state
	for attempt := 0; ; attempt++ {
		client := state.ClientInformation
		if client == nil && input.settings.ClientID != "" {
			client = &OAuthClientInformation{ClientID: input.settings.ClientID, ClientSecret: input.settings.ClientSecret}
		}
		if client == nil {
			return errors.New("OAuth client information is missing during code exchange")
		}
		// RFC 9207: never send a code from another authorization server.
		if pending.endpoints.AuthorizationResponseIssParameter || iss != "" {
			if iss != pending.endpoints.Issuer {
				return fmt.Errorf("OAuth issuer mismatch: expected %q, received %q", pending.endpoints.Issuer, iss)
			}
		}
		tokens, err := exchangeAuthorizationCode(ctx, pending.endpoints, client, code, pending.verifier, input.redirectURL, pending.resource, deps.client)
		if err == nil {
			outcome := discoveryOutcome{endpoints: pending.endpoints, resource: pending.resource}
			saveTokens(deps, input, state, withScope(tokens, pending.scope), outcome, pending.scope)
			return nil
		}
		var tokenErr *oauthTokenError
		if attempt == 0 && errors.As(err, &tokenErr) && tokenErr.invalidClient() && state.ClientInformation != nil {
			// Re-register and retry once.
			state.ClientInformation = nil
			continue
		}
		return err
	}
}

func saveTokens(deps flowDeps, input flowInput, state *OAuthServerState, tokens *OAuthTokens, outcome discoveryOutcome, scope string) {
	state.ServerURL = input.serverURL
	state.Tokens = tokens
	if tokens != nil && tokens.ExpiresIn > 0 {
		state.TokensExpireAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second).UnixMilli()
	} else {
		state.TokensExpireAt = 0
	}
	state.TokenEndpoint = outcome.endpoints.TokenURL
	state.Resource = outcome.resource
	if scope != "" {
		state.Scope = scope
	}
	_ = deps.store.Save(deps.serverName, input.serverURL, state)
}

// buildAuthorizationURL assembles the authorization redirect (pi:
// startAuthorization).
func buildAuthorizationURL(authEndpoint, clientID, challenge, redirectURL, state, scope, resource string) (string, error) {
	u, err := url.Parse(authEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid authorization endpoint %q: %w", authEndpoint, err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("redirect_uri", redirectURL)
	if state != "" {
		q.Set("state", state)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	if slices.Contains(strings.Fields(scope), "offline_access") {
		q.Set("prompt", "consent")
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
