// pi: auth/oauth/callback-server.ts.
//
// Loopback OAuth redirect handler shared by the browser sign-in flows. pi
// uses node:http; pig uses net/http directly. The 2026-09-12 port-leak fix
// (close the raw listener before Server.Close so a close racing Serve
// startup cannot leak the bind) is baked into Close, and Go's
// http.Server.Close already drops spare keep-alive connections — pi's
// chatgpt flow needs an explicit closeAllConnections for the same hazard.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OAuthCallbackServerOptions configures one shared callback server (pi:
// OAuthCallbackServerOptions<T>).
type OAuthCallbackServerOptions[T any] struct {
	// ProviderName is used on the browser page, for example `OpenAI`.
	ProviderName string
	// Host is the address to listen on.
	Host string
	// Port is the port to listen on; 0 picks a free port.
	Port int
	Path string
	// RedirectHost is the host in the redirect URI when it differs from
	// Host, for example `localhost`.
	RedirectHost string
	// State is the expected state parameter; empty skips the state check
	// (pi: omit when the provider does not send one — no provider sends an
	// empty state, so the empty-string encoding is safe).
	State string
	// Complete finishes the sign-in with the received code before the
	// browser page is sent, so the page can show exchange failures. Pass an
	// identity function to exchange the code later.
	Complete func(ctx context.Context, code string) (T, error)
	// Ctx is pi's signal: aborting cancels the wait ("Login cancelled"),
	// and an already-cancelled Ctx fails the start.
	Ctx context.Context
	// Timeout fails the wait after this long; zero waits indefinitely (pi:
	// timeoutMs).
	Timeout time.Duration
}

// OAuthCallbackOutcome is one resolution of the callback wait. Cancel
// resolves with Cancelled set and a zero Value (pi: wait() → undefined).
type OAuthCallbackOutcome[T any] struct {
	Value     T
	Err       error
	Cancelled bool
}

// OAuthCallbackServer is one loopback callback listener (pi:
// OAuthCallbackServer<T>).
type OAuthCallbackServer[T any] struct {
	redirectURI string
	outcomeCh   chan OAuthCallbackOutcome[T]

	mu         sync.Mutex
	claimed    bool
	settled    bool
	timer      *time.Timer
	closeCh    chan struct{}
	listener   net.Listener
	httpServer *http.Server
}

// finish resolves the wait exactly once; it returns false when the wait was
// already settled.
func (s *OAuthCallbackServer[T]) finish(outcome OAuthCallbackOutcome[T]) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return false
	}
	s.settled = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.closeCh != nil {
		close(s.closeCh)
		s.closeCh = nil
	}
	s.outcomeCh <- outcome
	return true
}

// RedirectURI is the loopback URL the provider redirects to (pi:
// redirectUri).
func (s *OAuthCallbackServer[T]) RedirectURI() string { return s.redirectURI }

// OutcomeChan resolves with the single wait outcome (pi: wait()).
func (s *OAuthCallbackServer[T]) OutcomeChan() <-chan OAuthCallbackOutcome[T] { return s.outcomeCh }

// Wait blocks until the callback completes or the wait is cancelled; a
// cancelled wait returns the zero value and a nil error (pi: wait() →
// undefined).
func (s *OAuthCallbackServer[T]) Wait() (T, error) {
	outcome := <-s.outcomeCh
	if outcome.Cancelled {
		var zero T
		return zero, nil
	}
	return outcome.Value, outcome.Err
}

// Cancel stops waiting for the browser unless a callback is already being
// completed (pi: cancel).
func (s *OAuthCallbackServer[T]) Cancel() {
	s.mu.Lock()
	claimed := s.claimed
	s.mu.Unlock()
	if !claimed {
		s.finish(OAuthCallbackOutcome[T]{Cancelled: true})
	}
}

// Close stops waiting (with an error, matching pi) and releases the port
// (pi: close).
func (s *OAuthCallbackServer[T]) Close() {
	s.finish(OAuthCallbackOutcome[T]{Err: errors.New("OAuth callback server closed")})
	// Close the raw listener first: http.Server.Close only releases
	// listeners Serve has registered, so a close racing Serve startup would
	// leak the port and break the next bind (regression-locked by
	// TestOAuthCallbackServerPortLeak in oauth_callback_server_test.go,
	// carried over from the anthropic/codex ports of the 2026-09-12 fix).
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
}

// StartOAuthCallbackServer listens and serves the callback route (pi:
// startOAuthCallbackServer). The listen is synchronous: an occupied port
// fails the call, and each flow decides whether to degrade to manual input
// (anthropic/codex catch) or propagate (openrouter).
func StartOAuthCallbackServer[T any](options OAuthCallbackServerOptions[T]) (*OAuthCallbackServer[T], error) {
	if options.Ctx != nil && options.Ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}
	server := &OAuthCallbackServer[T]{
		outcomeCh: make(chan OAuthCallbackOutcome[T], 1),
		closeCh:   make(chan struct{}),
	}
	finish := server.finish

	sendPage := func(w http.ResponseWriter, status int, html string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(html))
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
		if r.Method != http.MethodGet || r.URL.Path != options.Path {
			sendPage(w, http.StatusNotFound, OAuthErrorHTML("Callback route not found.", ""))
			return
		}
		query := r.URL.Query()
		if options.State != "" && query.Get("state") != options.State {
			sendPage(w, http.StatusBadRequest, OAuthErrorHTML("State mismatch.", ""))
			return
		}
		server.mu.Lock()
		done := server.claimed || server.settled
		server.mu.Unlock()
		if done {
			sendPage(w, http.StatusConflict, OAuthErrorHTML("This sign-in has already been handled.", ""))
			return
		}
		if errParam := query.Get("error"); errParam != "" {
			description := query.Get("error_description")
			if description == "" {
				description = errParam
			}
			sendPage(w, http.StatusBadRequest, OAuthErrorHTML(options.ProviderName+" authorization failed.", description))
			finish(OAuthCallbackOutcome[T]{Err: fmt.Errorf("%s authorization failed: %s", options.ProviderName, description)})
			return
		}
		code := query.Get("code")
		if code == "" {
			sendPage(w, http.StatusBadRequest, OAuthErrorHTML("Missing authorization code.", ""))
			return
		}
		server.mu.Lock()
		server.claimed = true
		server.mu.Unlock()
		ctx := options.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		value, err := options.Complete(ctx, code)
		if err != nil {
			sendPage(w, http.StatusBadGateway, OAuthErrorHTML(options.ProviderName+" sign-in failed.", err.Error()))
			finish(OAuthCallbackOutcome[T]{Err: err})
			return
		}
		sendPage(w, http.StatusOK, OAuthSuccessHTML("Signed in to "+options.ProviderName+". You may now close this page."))
		finish(OAuthCallbackOutcome[T]{Value: value})
	})

	listener, err := net.Listen("tcp", net.JoinHostPort(options.Host, strconv.Itoa(options.Port)))
	if err != nil {
		return nil, err
	}
	server.listener = listener
	server.httpServer = &http.Server{Handler: mux}
	go func() {
		if serveErr := server.httpServer.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			finish(OAuthCallbackOutcome[T]{Err: serveErr})
		}
	}()

	if options.Ctx != nil {
		closeCh := server.closeCh
		go func() {
			select {
			case <-options.Ctx.Done():
				finish(OAuthCallbackOutcome[T]{Err: errors.New("Login cancelled")})
			case <-closeCh:
			}
		}()
	}
	if options.Timeout > 0 {
		server.mu.Lock()
		providerName := options.ProviderName
		server.timer = time.AfterFunc(options.Timeout, func() {
			finish(OAuthCallbackOutcome[T]{Err: fmt.Errorf("%s sign-in timed out", providerName)})
		})
		server.mu.Unlock()
	}

	redirectHost := options.RedirectHost
	if redirectHost == "" {
		redirectHost = options.Host
	}
	if strings.Contains(redirectHost, ":") {
		redirectHost = "[" + redirectHost + "]"
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server.redirectURI = fmt.Sprintf("http://%s:%d%s", redirectHost, port, options.Path)
	return server, nil
}

// OAuthCallbackOrManual is the result of waiting for the browser callback or
// a pasted code (pi: the { type: "callback", value } | { type: "manual",
// input } union).
type OAuthCallbackOrManual[T any] struct {
	// Type is "callback" or "manual".
	Type string
	// Value is the completed callback value when Type is "callback".
	Value T
	// Input is the pasted text when Type is "manual".
	Input string
}

// WaitForCallbackOrManualInput waits for the browser callback, or for the
// user to paste the code or redirect URL when the browser cannot reach the
// loopback server (for example over SSH). Without a callback server only the
// manual prompt is used (pi: waitForCallbackOrManualInput). pig's
// AuthInteraction has no per-prompt abort, so a prompt that returns after
// the callback settles is simply ignored (declared deviation — the UI layer
// cancels its own dialog).
func WaitForCallbackOrManualInput[T any](interaction AuthInteraction, callback *OAuthCallbackServer[T], message string, placeholder string) (OAuthCallbackOrManual[T], error) {
	type manualOutcome struct {
		input string
		err   error
	}
	manualCh := make(chan manualOutcome, 1)
	go func() {
		input, promptErr := interaction.Prompt(AuthPrompt{
			Type:        "manual_code",
			Message:     message,
			Placeholder: placeholder,
		})
		manualCh <- manualOutcome{input: input, err: promptErr}
		// pi: the manual promise's then/catch both cancel the callback wait.
		if callback != nil {
			callback.Cancel()
		}
	}()

	var manual manualOutcome
	if callback == nil {
		manual = <-manualCh
		if manual.err != nil {
			return OAuthCallbackOrManual[T]{}, manual.err
		}
		return OAuthCallbackOrManual[T]{Type: "manual", Input: manual.input}, nil
	}
	outcome := <-callback.outcomeCh
	// pi reads manualError right after await wait(): a manual failure that
	// already surfaced wins over the callback result.
	haveManual := false
	select {
	case manual = <-manualCh:
		haveManual = true
	default:
	}
	if haveManual && manual.err != nil {
		return OAuthCallbackOrManual[T]{}, manual.err
	}
	if outcome.Err != nil {
		return OAuthCallbackOrManual[T]{}, outcome.Err
	}
	if !outcome.Cancelled {
		return OAuthCallbackOrManual[T]{Type: "callback", Value: outcome.Value}, nil
	}
	// Cancelled: fall through to the manual prompt (pi: await manual). The
	// prompt result may already have surfaced above.
	if !haveManual {
		manual = <-manualCh
	}
	if manual.err != nil {
		return OAuthCallbackOrManual[T]{}, manual.err
	}
	return OAuthCallbackOrManual[T]{Type: "manual", Input: manual.input}, nil
}
