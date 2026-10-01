// pi: test/oauth-callback-server.test.ts.
package auth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func callbackServerTestURL(redirectURI string, params map[string]string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	query := parsed.Query()
	for key, value := range params {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func callbackServerTestPage(t *testing.T, response *http.Response) (int, string) {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

// startCallbackServerTestServer builds a test server with Example defaults
// (pi: the describe-local start<T> helper).
func startCallbackServerTestServer(t *testing.T, mutate func(*OAuthCallbackServerOptions[string])) *OAuthCallbackServer[string] {
	t.Helper()
	options := OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/callback",
		State:        "expected-state",
		Complete: func(_ context.Context, code string) (string, error) {
			return "completed:" + code, nil
		},
	}
	if mutate != nil {
		mutate(&options)
	}
	server, err := StartOAuthCallbackServer[string](options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func TestOAuthCallbackServerIgnoresStrayRequestsAndResolves(t *testing.T) {
	server := startCallbackServerTestServer(t, nil)
	if !strings.HasPrefix(server.RedirectURI(), "http://127.0.0.1:") || !strings.HasSuffix(server.RedirectURI(), "/callback") {
		t.Fatalf("redirectUri = %q", server.RedirectURI())
	}

	// Wrong path → 404.
	wrongPath, err := http.Get(strings.TrimSuffix(server.RedirectURI(), "/callback") + "/other")
	if err != nil {
		t.Fatal(err)
	}
	status, body := callbackServerTestPage(t, wrongPath)
	if status != http.StatusNotFound {
		t.Errorf("wrong path = %d, want 404", status)
	}
	// Wrong state → 400 with the error page.
	wrongState, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "c", "state": "other"}))
	if err != nil {
		t.Fatal(err)
	}
	status, body = callbackServerTestPage(t, wrongState)
	if status != http.StatusBadRequest || !strings.Contains(body, "State mismatch.") {
		t.Errorf("wrong state = %d %q", status, body)
	}
	if contentType := wrongState.Header.Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", contentType)
	}
	// POST → 404.
	postResponse, err := http.PostForm(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "c", "state": "expected-state"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = callbackServerTestPage(t, postResponse)
	if status != http.StatusNotFound {
		t.Errorf("post = %d, want 404", status)
	}
	// Missing code → 400.
	missingCode, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"state": "expected-state"}))
	if err != nil {
		t.Fatal(err)
	}
	status, _ = callbackServerTestPage(t, missingCode)
	if status != http.StatusBadRequest {
		t.Errorf("missing code = %d, want 400", status)
	}

	// Success → 200 with the success page, and the wait resolves.
	success, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "the-code", "state": "expected-state"}))
	if err != nil {
		t.Fatal(err)
	}
	status, body = callbackServerTestPage(t, success)
	if status != http.StatusOK || !strings.Contains(body, "Authentication successful") || !strings.Contains(body, "Signed in to Example.") {
		t.Errorf("success = %d %q", status, body)
	}
	value, waitErr := server.Wait()
	if waitErr != nil || value != "completed:the-code" {
		t.Errorf("wait = %q, %v", value, waitErr)
	}
}

func TestOAuthCallbackServerUsesRedirectHostAndSkipsStateWhenNoneExpected(t *testing.T) {
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.RedirectHost = "localhost"
		options.State = ""
	})
	if !strings.HasPrefix(server.RedirectURI(), "http://localhost:") {
		t.Fatalf("redirectUri = %q", server.RedirectURI())
	}
	response, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "no-state"}))
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := callbackServerTestPage(t, response); status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	value, waitErr := server.Wait()
	if waitErr != nil || value != "completed:no-state" {
		t.Errorf("wait = %q, %v", value, waitErr)
	}
}

func TestOAuthCallbackServerShowsCompletionFailuresOnThePage(t *testing.T) {
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.Complete = func(context.Context, string) (string, error) {
			return "", fmt.Errorf("token exchange failed")
		}
	})
	response, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "c", "state": "expected-state"}))
	if err != nil {
		t.Fatal(err)
	}
	status, body := callbackServerTestPage(t, response)
	if status != http.StatusBadGateway || !strings.Contains(body, "Example sign-in failed.") || !strings.Contains(body, "token exchange failed") {
		t.Errorf("failure = %d %q", status, body)
	}
	if _, waitErr := server.Wait(); waitErr == nil || waitErr.Error() != "token exchange failed" {
		t.Errorf("wait error = %v", waitErr)
	}
}

func TestOAuthCallbackServerRejectsProviderErrorRedirects(t *testing.T) {
	server := startCallbackServerTestServer(t, nil)
	response, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{
		"error":             "access_denied",
		"error_description": "User denied access",
		"state":             "expected-state",
	}))
	if err != nil {
		t.Fatal(err)
	}
	status, body := callbackServerTestPage(t, response)
	if status != http.StatusBadRequest || !strings.Contains(body, "User denied access") {
		t.Errorf("error redirect = %d %q", status, body)
	}
	_, waitErr := server.Wait()
	if waitErr == nil || waitErr.Error() != "Example authorization failed: User denied access" {
		t.Errorf("wait error = %v", waitErr)
	}
}

func TestOAuthCallbackServerCompletesOnlyTheFirstCallback(t *testing.T) {
	exchangeStarted := make(chan chan string, 1)
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.Complete = func(context.Context, string) (string, error) {
			finish := make(chan string)
			exchangeStarted <- finish
			return <-finish, nil
		}
	})
	requestURL := callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "c", "state": "expected-state"})
	firstDone := make(chan *http.Response, 1)
	go func() {
		response, err := http.Get(requestURL)
		if err != nil {
			t.Error(err)
			return
		}
		firstDone <- response
	}()
	var finish chan string
	select {
	case finish = <-exchangeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("complete was never invoked")
	}
	// A second callback while the first is completing → 409.
	second, err := http.Get(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := callbackServerTestPage(t, second)
	if status != http.StatusConflict {
		t.Errorf("second callback = %d, want 409", status)
	}
	// A claimed callback keeps completing even when the caller switches to
	// manual input.
	server.Cancel()
	finish <- "done"
	first := <-firstDone
	if status, _ := callbackServerTestPage(t, first); status != http.StatusOK {
		t.Errorf("first callback = %d, want 200", status)
	}
	value, waitErr := server.Wait()
	if waitErr != nil || value != "done" {
		t.Errorf("wait = %q, %v", value, waitErr)
	}
}

func TestOAuthCallbackServerResolvesCancelledAfterCancel(t *testing.T) {
	server := startCallbackServerTestServer(t, nil)
	server.Cancel()
	value, waitErr := server.Wait()
	if waitErr != nil || value != "" {
		t.Errorf("wait after cancel = %q, %v", value, waitErr)
	}
	// A late callback lands after the wait settled → 409.
	response, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "c", "state": "expected-state"}))
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := callbackServerTestPage(t, response); status != http.StatusConflict {
		t.Errorf("late callback = %d, want 409", status)
	}
}

func TestOAuthCallbackServerRejectsOnAbortAndOnTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server, err := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/callback",
		Complete:     func(context.Context, string) (string, error) { return "", nil },
		Ctx:          ctx,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	cancel()
	if _, waitErr := server.Wait(); waitErr == nil || waitErr.Error() != "Login cancelled" {
		t.Errorf("abort wait error = %v", waitErr)
	}

	timedOut := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.Timeout = 10 * time.Millisecond
	})
	if _, waitErr := timedOut.Wait(); waitErr == nil || waitErr.Error() != "Example sign-in timed out" {
		t.Errorf("timeout wait error = %v", waitErr)
	}

	alreadyAborted, abort := context.WithCancel(context.Background())
	abort()
	if _, startErr := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/callback",
		Complete:     func(context.Context, string) (string, error) { return "", nil },
		Ctx:          alreadyAborted,
	}); startErr == nil || startErr.Error() != "Login cancelled" {
		t.Errorf("already-aborted start error = %v", startErr)
	}
}

func TestOAuthCallbackServerFailsOnOccupiedPort(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port
	if _, startErr := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         port,
		Path:         "/callback",
		Complete:     func(context.Context, string) (string, error) { return "", nil },
	}); startErr == nil {
		t.Fatal("expected the occupied port to fail the start")
	}
}

// TestOAuthCallbackServerPortLeak locks the 2026-09-12 fix on the shared
// server: closing immediately after start releases the port even when Serve
// has not registered the listener yet.
func TestOAuthCallbackServerPortLeak(t *testing.T) {
	first, err := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/callback",
		Complete:     func(context.Context, string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "Example",
		Host:         "127.0.0.1",
		Port:         0,
		Path:         "/callback",
		Complete:     func(context.Context, string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatalf("restarting the callback server failed: %v", err)
	}
	second.Close()
}

func TestWaitForCallbackOrManualInputReturnsBrowserCallback(t *testing.T) {
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.State = ""
		options.Complete = func(_ context.Context, code string) (string, error) { return code, nil }
	})
	promptReturned := make(chan struct{})
	releasePrompt := make(chan struct{})
	t.Cleanup(func() { close(releasePrompt) })
	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			close(promptReturned)
			// pig has no per-prompt abort; a prompt that returns after the
			// callback settles is ignored (declared deviation) — hold the
			// prompt open until the assertions are done.
			<-releasePrompt
			return "late-paste", nil
		},
	}
	resultDone := make(chan struct{})
	var result OAuthCallbackOrManual[string]
	var resultErr error
	go func() {
		result, resultErr = WaitForCallbackOrManualInput[string](interaction, server, "paste", server.RedirectURI())
		close(resultDone)
	}()
	<-promptReturned
	response, err := http.Get(callbackServerTestURL(server.RedirectURI(), map[string]string{"code": "from-browser"}))
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := callbackServerTestPage(t, response); status != http.StatusOK {
		t.Errorf("callback page = %d, want 200", status)
	}
	select {
	case <-resultDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the helper never resolved")
	}
	if resultErr != nil {
		t.Fatalf("helper error = %v", resultErr)
	}
	if result.Type != "callback" || result.Value != "from-browser" {
		t.Errorf("result = %+v", result)
	}
}

func TestWaitForCallbackOrManualInputReturnsPastedInput(t *testing.T) {
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.State = ""
		options.Complete = func(_ context.Context, code string) (string, error) { return code, nil }
	})
	interaction := &oauthTestInteraction{
		prompt: func(AuthPrompt) (string, error) { return "pasted", nil },
	}
	result, err := WaitForCallbackOrManualInput[string](interaction, server, "paste", server.RedirectURI())
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != "manual" || result.Input != "pasted" {
		t.Errorf("result = %+v", result)
	}
}

func TestWaitForCallbackOrManualInputUsesOnlyManualWithoutServer(t *testing.T) {
	interaction := &oauthTestInteraction{
		prompt: func(AuthPrompt) (string, error) { return "pasted", nil },
	}
	result, err := WaitForCallbackOrManualInput[string](interaction, nil, "paste", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	if result.Type != "manual" || result.Input != "pasted" {
		t.Errorf("result = %+v", result)
	}
}

func TestWaitForCallbackOrManualInputPropagatesManualFailures(t *testing.T) {
	server := startCallbackServerTestServer(t, func(options *OAuthCallbackServerOptions[string]) {
		options.State = ""
		options.Complete = func(_ context.Context, code string) (string, error) { return code, nil }
	})
	interaction := &oauthTestInteraction{
		prompt: func(AuthPrompt) (string, error) { return "", fmt.Errorf("prompt cancelled") },
	}
	_, err := WaitForCallbackOrManualInput[string](interaction, server, "paste", server.RedirectURI())
	if err == nil || err.Error() != "prompt cancelled" {
		t.Fatalf("error = %v, want prompt cancelled", err)
	}
}
