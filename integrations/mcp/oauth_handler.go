package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

// Transport-facing auth provider (go-sdk auth.OAuthHandler), ported from pi's
// createMcpAuthProvider + adaptOAuthProvider (extensions/mcp/oauth.ts +
// packages/mcp/src/oauth/flow.ts). Never starts a browser flow: sends the
// stored access token, proactively refreshes it when it is about to expire,
// refreshes again after a 401, and fails with an error wrapping
// ErrSignInRequired when the user has to sign in (including step-up
// challenges — a refresh cannot widen scope).

// Access tokens this close to expiry are refreshed before they are sent (pi:
// REFRESH_SKEW_MS).
const oauthRefreshSkew = 30 * time.Second

// Bounds each request to the authorization server, so an unresponsive one
// cannot hold the refresh lock, delay a tool call, delay shutdown, or keep a
// sign-in waiting for long (pi: OAUTH_REQUEST_TIMEOUT_MS — S17 f10993bc7
// widened REFRESH_REQUEST_TIMEOUT_MS from refreshes to every OAuth request).
const oauthRequestTimeout = 15 * time.Second

var _ auth.OAuthHandler = (*OAuthTransportHandler)(nil)

// staticTokenSource serves one fixed token.
type staticTokenSource struct{ token string }

func (s staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token}, nil
}

// OAuthTransportHandler implements github.com/modelcontextprotocol/go-sdk/auth
// OAuthHandler for one OAuth-protected server. It is safe for concurrent use.
type OAuthTransportHandler struct {
	deps   flowDeps
	server string
	// serverURL is the MCP server URL the credentials are stored under.
	serverURL string
	// settings resolves the oauth.* config lazily, so a secret that fails to
	// resolve fails the refresh instead of the whole connection setup (pi:
	// settings()).
	settings func() OAuthSettings
	// onChallenge records the server's last WWW-Authenticate challenge so
	// /mcp sign-in can reuse its resource metadata URL and scope.
	onChallenge func(OAuthChallenge)
	// providerToken, when set, sends a /login provider's token instead of
	// OAuth state (pi: auth.provider). Each request re-reads it so the
	// provider's own refreshes apply.
	providerName  string
	providerToken func(ctx context.Context) (string, error)

	mu         sync.Mutex
	refreshing bool
	// closing marks "the session is being torn down": TokenSource then hands
	// out the stored token without a proactive refresh, so Close never waits
	// on a network refresh (S17 f10993bc7 — pi's transport reuses the last
	// request's token; pig sits outside the go-sdk transport, so the closest
	// equivalent is "stored token, no refresh").
	closing atomic.Bool
}

func newOAuthTransportHandler(deps flowDeps, server, serverURL string, settings func() OAuthSettings, onChallenge func(OAuthChallenge)) *OAuthTransportHandler {
	return &OAuthTransportHandler{
		deps:        deps,
		server:      server,
		serverURL:   serverURL,
		settings:    settings,
		onChallenge: onChallenge,
	}
}

// newProviderTokenHandler builds the auth.provider face: per-request token
// from the provider, no refresh and no stored state (pi: `{ token }`).
func newProviderTokenHandler(server, providerName string, providerToken func(ctx context.Context) (string, error)) *OAuthTransportHandler {
	return &OAuthTransportHandler{
		server:        server,
		providerName:  providerName,
		providerToken: providerToken,
	}
}

// tokenState returns the stored tokens, refreshed when they are about to
// expire. Failures fall through with the old token: the request goes out and
// a 401 decides what happens (pi: token()). With closing set the proactive
// refresh is skipped: close reuses the stored token instead of asking the
// auth provider, which could refresh over the network (S17 f10993bc7).
func (h *OAuthTransportHandler) tokenState(ctx context.Context, closing bool) *OAuthTokens {
	state, err := h.deps.store.Load(h.server, h.serverURL)
	if err != nil || state == nil || state.Tokens == nil {
		return nil
	}
	if state.TokensExpireAt != 0 && time.Until(time.UnixMilli(state.TokensExpireAt)) > oauthRefreshSkew {
		return state.Tokens
	}
	if state.Tokens.RefreshToken == "" || closing {
		return state.Tokens
	}
	// Failures fall through.
	_ = h.silentRefresh(ctx, nil, "")
	state, err = h.deps.store.Load(h.server, h.serverURL)
	if err != nil || state == nil {
		return nil
	}
	return state.Tokens
}

// TokenSource implements auth.OAuthHandler: the token for outgoing requests.
// A nil source sends no Authorization header.
func (h *OAuthTransportHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if h.providerToken != nil {
		token, err := h.providerToken(ctx)
		if err != nil {
			return nil, err
		}
		if token == "" {
			return nil, nil
		}
		return staticTokenSource{token: token}, nil
	}
	if tokens := h.tokenState(ctx, h.closing.Load()); tokens != nil && tokens.AccessToken != "" {
		return staticTokenSource{token: tokens.AccessToken}, nil
	}
	return nil, nil
}

// Authorize implements auth.OAuthHandler: called by the transport on 401 (or
// a 403 with an insufficient_scope challenge) once per request. A nil return
// makes the transport retry the request with whatever credentials this left
// behind.
func (h *OAuthTransportHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	challenge := parseChallenge(resp.Header.Values("WWW-Authenticate"))
	if h.onChallenge != nil {
		h.onChallenge(challenge)
	}
	if resp.StatusCode == http.StatusForbidden && challenge.Error != "insufficient_scope" {
		// Not a step-up challenge: retry once and let the response surface.
		return nil
	}
	if h.providerToken != nil {
		return providerSignInRequiredError(h.server, h.providerName)
	}
	// A refresh keeps the granted scope, so more scope needs a new sign-in
	// (pi: onUnauthorized's insufficient_scope branch).
	if challenge.Error == "insufficient_scope" {
		return signInRequiredError(h.server)
	}
	return h.silentRefresh(ctx, &challenge, req.Header.Get("Authorization"))
}

// silentRefresh refreshes the stored tokens (browser flow never). Concurrent
// 401s share one refresh: a request whose token was already replaced is just
// retried, since with rotating refresh tokens a second refresh with the old
// refresh token would discard the new grant (pi adaptOAuthProvider).
func (h *OAuthTransportHandler) silentRefresh(ctx context.Context, challenge *OAuthChallenge, staleAuthHeader string) error {
	h.mu.Lock()
	if h.refreshing {
		h.mu.Unlock()
		// Wait for the in-flight refresh to settle, then let the retry ride
		// on whatever it stored.
		for h.refreshing {
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	h.refreshing = true
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.refreshing = false
		h.mu.Unlock()
	}()

	state, err := h.deps.store.Load(h.server, h.serverURL)
	if err != nil {
		return err
	}
	if state == nil || state.Tokens == nil {
		return signInRequiredError(h.server)
	}
	if staleAuthHeader != "" && "Bearer "+state.Tokens.AccessToken != staleAuthHeader {
		// Another request already refreshed; the retry carries the new token.
		return nil
	}
	if state.Tokens.RefreshToken == "" {
		return signInRequiredError(h.server)
	}

	settings := h.settings()
	ch := OAuthChallenge{}
	if challenge != nil {
		ch = *challenge
	}
	deps := h.deps
	deps.client = &http.Client{Timeout: oauthRequestTimeout}
	input := flowInput{
		serverURL:   h.serverURL,
		settings:    settings,
		challenge:   ch,
		state:       state,
		redirectURL: registeredRedirectURL(state.ClientInformation),
	}
	_, _, err = runFlow(ctx, deps, input, false)
	if err != nil {
		if errors.Is(err, ErrSignInRequired) {
			return err
		}
		return fmt.Errorf("refreshing MCP OAuth tokens: %w", err)
	}
	return nil
}

// markClosing flips the closing flag: subsequent TokenSource calls skip the
// proactive refresh (S17 f10993bc7 — see the closing field comment).
func (h *OAuthTransportHandler) markClosing() {
	h.closing.Store(true)
}
