package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Loopback OAuth callback server, ported from pi's packages/mcp/src/oauth/
// callback.ts. Requests are routed by the `state` query parameter; each
// pending authorization registers its expected path, so a server-specific
// redirect URI can tell authorization servers apart (RFC 9700 §4.4.2.2).

// OAuthCallback is the authorization response delivered for one state.
type OAuthCallback struct {
	Code string
	Iss  string // RFC 9207 issuer parameter, when present
}

type oauthPending struct {
	ch chan OAuthCallback
	// path is the exact redirect path this state waits on; empty = any
	// registered path.
	path string
}

// OAuthCallbackServer serves the loopback redirect. waitForCallback must be
// called before the user is sent to the authorization URL.
type OAuthCallbackServer struct {
	server   *http.Server
	listener net.Listener
	// redirectURL is the base redirect URI (host as registered, dynamic port
	// included).
	redirectURL string
	paths       []string

	mu      sync.Mutex
	pending map[string]*oauthPending
}

// ListenOAuthCallback binds the loopback callback server. host serves the
// listener (localhost is served on 127.0.0.1; browsers fall back to it when
// ::1 refuses), redirectHost is the host name used in redirectURL.
func ListenOAuthCallback(host, redirectHost string, port int, path string, extraPaths []string) (*OAuthCallbackServer, error) {
	if path == "" {
		path = "/callback"
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if redirectHost == "" {
		redirectHost = host
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	s := &OAuthCallbackServer{
		redirectURL: (&url.URL{Scheme: "http", Host: hostPort(redirectHost, listener.Addr().(*net.TCPAddr).Port), Path: path}).String(),
		paths:       append([]string{path}, extraPaths...),
		pending:     map[string]*oauthPending{},
	}
	s.server = &http.Server{Handler: s}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + fmt.Sprint(port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// RedirectURL is the redirect URI the server serves (with the bound port).
func (s *OAuthCallbackServer) RedirectURL() string { return s.redirectURL }

// Port is the bound port.
func (s *OAuthCallbackServer) Port() int { return s.listener.Addr().(*net.TCPAddr).Port }

// WaitForCallback registers a pending authorization and resolves with the
// browser response, an error page fault, or the timeout (pi:
// waitForCallback; default 5 minutes).
func (s *OAuthCallbackServer) WaitForCallback(state string, path string, timeout time.Duration) <-chan OAuthCallback {
	ch := make(chan OAuthCallback, 1)
	s.mu.Lock()
	if _, exists := s.pending[state]; exists {
		s.mu.Unlock()
		ch <- OAuthCallback{} // duplicate state: caller treats closed delivery as failure
		close(ch)
		return ch
	}
	pending := &oauthPending{ch: ch, path: path}
	s.pending[state] = pending
	s.mu.Unlock()
	// The timer never touches the delivery channel on the read side: the
	// handler and the timer race only through the pending map under the
	// lock, so exactly one of them sends and closes.
	time.AfterFunc(timeout, func() {
		s.mu.Lock()
		if s.pending[state] == pending {
			delete(s.pending, state)
			pending.ch <- OAuthCallback{}
			close(pending.ch)
		}
		s.mu.Unlock()
	})
	return ch
}

// Close shuts the server down and fails every pending authorization.
func (s *OAuthCallbackServer) Close() error {
	s.mu.Lock()
	pending := s.pending
	s.pending = map[string]*oauthPending{}
	s.mu.Unlock()
	for _, p := range pending {
		p.ch <- OAuthCallback{}
		close(p.ch)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

func (s *OAuthCallbackServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pathOK := false
	for _, p := range s.paths {
		if r.URL.Path == p {
			pathOK = true
			break
		}
	}
	if !pathOK {
		oauthReply(w, http.StatusNotFound, false, "Not found", "")
		return
	}
	state := r.URL.Query().Get("state")
	s.mu.Lock()
	pending := s.pending[state]
	if pending != nil && pending.path != "" && r.URL.Path != pending.path {
		s.mu.Unlock()
		oauthReply(w, http.StatusBadRequest, false, "Unexpected redirect URI", "")
		return
	}
	if pending != nil {
		delete(s.pending, state)
	}
	s.mu.Unlock()
	if pending == nil || state == "" {
		oauthReply(w, http.StatusBadRequest, false, "Invalid or expired OAuth state", "")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		description := orDefault(r.URL.Query().Get("error_description"), errParam)
		pending.ch <- OAuthCallback{}
		close(pending.ch)
		oauthReply(w, http.StatusOK, false, "Authorization failed. You may close this window.", description)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		pending.ch <- OAuthCallback{}
		close(pending.ch)
		oauthReply(w, http.StatusBadRequest, false, "Missing authorization code", "")
		return
	}
	pending.ch <- OAuthCallback{Code: code, Iss: r.URL.Query().Get("iss")}
	close(pending.ch)
	oauthReply(w, http.StatusOK, true, "Signed in to the MCP server. You may now close this page.", "")
}

func oauthReply(w http.ResponseWriter, status int, ok bool, message, details string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	color := "#1a7f37"
	title := "Authorization complete"
	if !ok {
		color = "#cf222e"
		title = message
	}
	body := ""
	if details != "" {
		body = fmt.Sprintf("<p>%s</p>", htmlEscape(details))
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>`+
		`<body style="font-family:system-ui,sans-serif;max-width:40em;margin:4em auto;padding:0 1em">`+
		`<h2 style="color:%s">%s</h2>%s<p>pig — MCP OAuth</p></body></html>`,
		htmlEscape(title), color, htmlEscape(title), body)
}

func htmlEscape(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;")
	return replacer.Replace(text)
}

var errCallbackClosed = errors.New("OAuth callback server closed")

// awaitCallback resolves one pending delivery channel; an empty callback
// (server closed, timeout, or error page) becomes an error.
func awaitCallback(ch <-chan OAuthCallback) (OAuthCallback, error) {
	cb := <-ch
	if cb.Code == "" {
		return cb, errCallbackClosed
	}
	return cb, nil
}
