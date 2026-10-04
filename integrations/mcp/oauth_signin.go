package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Interactive sign-in for one MCP server, ported from pi's signInMcpServer
// (extensions/mcp/oauth.ts). Uses the stored refresh token when possible;
// otherwise runs the browser authorization-code flow against a loopback
// callback, racing the browser with a pasted redirect URL for when the
// browser cannot reach this machine (SSH).

const oauthCallbackTimeout = 5 * time.Minute

// callbackSettings derives where the loopback callback listens and the
// redirect URI it serves (pi: callbackSettings). A configured URI with a port
// is sent exactly as written, since servers compare redirect URIs as strings.
func callbackSettings(settings OAuthSettings) (host, redirectHost, path string, port int, fixedRedirectURL string) {
	raw := settings.CallbackURL
	if raw == "" {
		raw = "http://127.0.0.1/callback"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "127.0.0.1", "127.0.0.1", "/callback", settings.CallbackPort, ""
	}
	address := strings.Trim(u.Hostname(), "[]")
	port = 0
	if u.Port() != "" {
		port = parsePort(u.Port())
	} else {
		port = settings.CallbackPort
	}
	if u.Port() != "" || settings.CallbackPort != 0 {
		fixed := *u
		fixed.Host = hostPort(address, port)
		fixedRedirectURL = fixed.String()
	}
	// localhost is served on 127.0.0.1.
	if address == "localhost" {
		host = "127.0.0.1"
	} else {
		host = address
	}
	return host, address, u.Path, port, fixedRedirectURL
}

func parsePort(text string) int {
	port := 0
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0
		}
		port = port*10 + int(c-'0')
	}
	return port
}

// registeredRedirectURL returns the first redirect URI of the stored client
// registration.
func registeredRedirectURL(client *OAuthClientInformation) string {
	if client == nil || len(client.RedirectURIs) == 0 {
		return ""
	}
	return client.RedirectURIs[0]
}

// signInMcpServer signs a server in (pi: signInMcpServer). The manager wraps
// this with reconnect + challenge bookkeeping.
func signInMcpServer(ctx context.Context, deps flowDeps, serverURL string, settings OAuthSettings, challenge OAuthChallenge, prompt SignInPrompt, openURL func(string)) error {
	stored, err := deps.store.Load(deps.serverName, serverURL)
	if err != nil {
		return err
	}
	if stored == nil {
		stored = &OAuthServerState{ServerURL: serverURL}
	}
	stepUp := challenge.Error == "insufficient_scope"
	host, redirectHost, path, _, fixedRedirectURL := callbackSettings(settings)

	// Reuse the port of the registered redirect URI so the registered client
	// stays valid (pi: preferredPort).
	preferred := settings.CallbackPort
	if preferred == 0 {
		if registered := registeredRedirectURL(stored.ClientInformation); registered != "" {
			if u, err := url.Parse(registered); err == nil && u.Port() != "" {
				preferred = parsePort(u.Port())
			}
		}
	}
	callback, err := ListenOAuthCallback(host, redirectHost, preferred, path, nil)
	if err != nil {
		if preferred == 0 || fixedRedirectURL != "" {
			return fmt.Errorf("starting the OAuth callback server: %w", err)
		}
		// Configured without a fixed port: retry on a free port (pi:
		// listenForCallback's `required` logic).
		callback, err = ListenOAuthCallback(host, redirectHost, 0, path, nil)
		if err != nil {
			return fmt.Errorf("starting the OAuth callback server: %w", err)
		}
	}
	defer callback.Close()

	redirectURL := fixedRedirectURL
	if redirectURL == "" {
		redirectURL = callback.RedirectURL()
	}

	// Every sign-in gets a fresh state, and a registered client cannot use
	// another redirect URI — drop stale registrations whose redirect URI no
	// longer matches (pi: keepClient block; a pre-registered clientId is kept
	// regardless).
	if settings.ClientID == "" && stored.ClientInformation != nil {
		if registered := registeredRedirectURL(stored.ClientInformation); registered != redirectURL {
			stored.ClientInformation = nil
			stored.Tokens = nil
			stored.TokensExpireAt = 0
		}
	}
	_ = deps.store.Save(deps.serverName, serverURL, stored)

	// A server asking for more scope gets it on top of the configured scope
	// and, since the challenge may list only the missing scopes, on top of
	// the scope granted so far (pi: mergeScopes + stepUpScope).
	grantedScope := ""
	if stored.Tokens != nil {
		grantedScope = stored.Tokens.Scope
	}
	challenged := challenge.Scope
	if stepUp {
		challenged = stepUpScope(grantedScope, challenge.Scope)
	}
	scope := mergeScopes(settings.Scope, challenged)

	input := flowInput{
		serverURL:   serverURL,
		settings:    settings,
		challenge:   challenge,
		state:       stored,
		skipRefresh: stepUp,
		redirectURL: redirectURL,
		scope:       scope,
	}
	kind, pending, err := runFlow(ctx, deps, input, true)
	if err != nil {
		return err
	}
	if kind == flowAuthorized {
		return nil // the refresh path reauthorized without user interaction
	}
	if pending == nil || pending.authURL == "" {
		return errors.New("OAuth flow did not produce an authorization URL")
	}

	if openURL != nil {
		openURL(pending.authURL)
	}
	// Register the pending state before the user (or the browser) is sent to
	// the authorization URL — a redirect that arrives early must find its
	// waiter (pi registers waitForCallback before the exchange too).
	callbackCh := callback.WaitForCallback(pending.state, redirectPath(redirectURL), oauthCallbackTimeout)
	prompt.ShowAuthorizationURL(pending.authURL)

	// The redirect URI the flow picked (may be the callback's dynamic one).
	promptCtx, cancelPrompt := context.WithCancel(ctx)
	defer cancelPrompt()
	type pasteResult struct {
		url string
		err error
	}
	paste := make(chan pasteResult, 1)
	go func() {
		value, err := prompt.PromptForRedirectURL(promptCtx)
		paste <- pasteResult{value, err}
	}()

	var code, iss string
	select {
	case cb, ok := <-callbackCh:
		cancelPrompt()
		if !ok || cb.Code == "" {
			return errors.New("the OAuth callback failed or timed out")
		}
		code, iss = cb.Code, cb.Iss
	case result := <-paste:
		if result.err != nil {
			if errors.Is(result.err, context.Canceled) || errors.Is(result.err, ErrSignInCancelled) {
				return ErrSignInCancelled
			}
			return result.err
		}
		parsed, err := responseFromRedirectURL(result.url, pending.state, redirectURL)
		if err != nil {
			return err
		}
		code, iss = parsed.Code, parsed.Iss
	}
	return completeExchange(ctx, deps, input, pending, code, iss)
}

func redirectPath(redirectURL string) string {
	if u, err := url.Parse(redirectURL); err == nil {
		return u.Path
	}
	return ""
}

// responseFromRedirectURL validates a pasted browser-address-bar URL (pi:
// responseFromRedirectUrl): the redirect URI must match exactly, the state
// must belong to this sign-in, and an error response is surfaced.
func responseFromRedirectURL(input, state, redirectURL string) (OAuthCallback, error) {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil {
		return OAuthCallback{}, errors.New("expected the full redirect URL from the browser address bar")
	}
	expected, err := url.Parse(redirectURL)
	if err != nil {
		return OAuthCallback{}, err
	}
	if parsed.Scheme != expected.Scheme || parsed.Host != expected.Host || parsed.Path != expected.Path {
		return OAuthCallback{}, errors.New("the redirect URL does not match this sign-in's redirect URI")
	}
	if errParam := parsed.Query().Get("error"); errParam != "" {
		return OAuthCallback{}, errors.New(orDefault(parsed.Query().Get("error_description"), errParam))
	}
	if parsed.Query().Get("state") != state {
		return OAuthCallback{}, errors.New("the redirect URL belongs to a different sign-in")
	}
	code := parsed.Query().Get("code")
	if code == "" {
		return OAuthCallback{}, errors.New("the redirect URL does not contain an authorization code")
	}
	return OAuthCallback{Code: code, Iss: parsed.Query().Get("iss")}, nil
}
