// pi: test/openrouter-oauth.test.ts (flow tests; the models integration pair
// lives in oauth_openrouter_ext_test.go because it crosses into package ai).
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// stubOpenRouterOAuthDo swaps the token-endpoint transport seam (pi:
// vi.stubGlobal("fetch")).
func stubOpenRouterOAuthDo(t *testing.T, stub func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error)) {
	t.Helper()
	previous := openRouterOAuthDo
	openRouterOAuthDo = stub
	t.Cleanup(func() { openRouterOAuthDo = previous })
}

func openRouterTestJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func openRouterTestGetPage(t *testing.T, requestURL string) (int, string) {
	t.Helper()
	response, err := http.Get(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func openRouterTestReadPage(ch chan *http.Response, t *testing.T) (int, string) {
	t.Helper()
	select {
	case response := <-ch:
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(body)
	case <-time.After(5 * time.Second):
		t.Fatal("callback page never responded")
		return 0, ""
	}
}

// openRouterFlowInteraction drives a flow from the test goroutine: Notify
// hands the advertise callback URL over a channel (avoiding data races), and
// Prompt either returns a fixed result, delegates to promptFn, or blocks
// forever (pi: prompt: () => new Promise<string>(() => {})).
type openRouterFlowInteraction struct {
	authorizeCh chan string
	promptFn    func(AuthPrompt) (string, error)
}

func (i *openRouterFlowInteraction) Notify(event AuthEvent) {
	if event.Type != "auth_url" {
		return
	}
	i.authorizeCh <- event.URL
}

func (i *openRouterFlowInteraction) Prompt(prompt AuthPrompt) (string, error) {
	if i.promptFn != nil {
		return i.promptFn(prompt)
	}
	select {}
}

func openRouterTestAuthorizeURL(t *testing.T, interaction *openRouterFlowInteraction) *url.URL {
	t.Helper()
	select {
	case authorizeURL := <-interaction.authorizeCh:
		parsed, err := url.Parse(authorizeURL)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	case <-time.After(5 * time.Second):
		t.Fatal("no auth_url event")
		return nil
	}
}

// openRouterTestCallbackURL builds the loopback URL the browser would hit,
// with the given code attached.
func openRouterTestCallbackURL(authorize *url.URL, code string) string {
	callback, err := url.Parse(authorize.Query().Get("callback_url"))
	if err != nil {
		return ""
	}
	query := callback.Query()
	query.Set("code", code)
	callback.RawQuery = query.Encode()
	return callback.String()
}

func TestOpenRouterOAuthRunsPKCEOnOneShotCallback(t *testing.T) {
	var exchangeBody map[string]any
	exchangeCount := 0
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != openRouterTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		if err := json.Unmarshal([]byte(body), &exchangeBody); err != nil {
			return nil, err
		}
		exchangeCount++
		return openRouterTestJSONResponse(200, `{"key":"sk-or-test"}`), nil
	})

	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	callbackDone := make(chan *http.Response, 1)
	loginErrCh := make(chan error, 1)
	loginCredential := make(chan *OAuthCredential, 1)
	go func() {
		credential, loginErr := OpenRouterOAuth{}.Login(context.Background(), interaction, nil)
		if loginErr != nil {
			loginErrCh <- loginErr
			return
		}
		loginCredential <- credential
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	go func() {
		response, err := http.Get(openRouterTestCallbackURL(authorize, "authorization-code"))
		if err != nil {
			t.Error(err)
			return
		}
		callbackDone <- response
	}()

	select {
	case credential := <-loginCredential:
		if credential.Access != "sk-or-test" || credential.Refresh != "" || credential.Expires != 1<<63-1 {
			t.Errorf("credential = %+v", credential.OAuthCredentials)
		}
	case loginErr := <-loginErrCh:
		t.Fatalf("login failed: %v", loginErr)
	case <-time.After(5 * time.Second):
		t.Fatal("login never resolved")
	}
	status, body := openRouterTestReadPage(callbackDone, t)
	if status != http.StatusOK || !strings.Contains(body, "Signed in to OpenRouter.") {
		t.Errorf("callback page = %d %q", status, body)
	}
	if authorize.Scheme != "https" || authorize.Host != "openrouter.ai" || authorize.Path != "/auth" {
		t.Errorf("authorize URL = %q", authorize)
	}
	if got := authorize.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q", got)
	}

	callback, err := url.Parse(authorize.Query().Get("callback_url"))
	if err != nil {
		t.Fatal(err)
	}
	if callback.Hostname() != "127.0.0.1" {
		t.Errorf("callback host = %q", callback.Hostname())
	}
	if matched, _ := regexp.MatchString(`^/oauth/callback/[0-9a-f-]+$`, callback.Path); !matched {
		t.Errorf("callback path = %q", callback.Path)
	}

	if exchangeBody["code"] != "authorization-code" || exchangeBody["code_challenge_method"] != "S256" {
		t.Errorf("exchange body = %v", exchangeBody)
	}
	verifier, _ := exchangeBody["code_verifier"].(string)
	if verifier == "" {
		t.Fatal("exchange body has no code_verifier")
	}
	digest := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); authorize.Query().Get("code_challenge") != want {
		t.Error("code_challenge does not match the verifier digest")
	}
	if exchangeCount != 1 {
		t.Errorf("token exchange count = %d, want 1", exchangeCount)
	}
}

func TestOpenRouterOAuthReportsExchangeFailuresThroughPageAndLogin(t *testing.T) {
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return openRouterTestJSONResponse(403, `{"error":{"message":"invalid code"}}`), nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	callbackDone := make(chan *http.Response, 1)
	loginErrCh := make(chan error, 1)
	go func() {
		if _, loginErr := (OpenRouterOAuth{}).Login(context.Background(), interaction, nil); loginErr != nil {
			loginErrCh <- loginErr
		}
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	go func() {
		response, err := http.Get(openRouterTestCallbackURL(authorize, "bad-code"))
		if err != nil {
			t.Error(err)
			return
		}
		callbackDone <- response
	}()
	select {
	case loginErr := <-loginErrCh:
		if loginErr.Error() != "OpenRouter OAuth key exchange failed (HTTP 403): invalid code" {
			t.Fatalf("login error = %v", loginErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login never failed")
	}
	if status, _ := openRouterTestReadPage(callbackDone, t); status != http.StatusBadGateway {
		t.Errorf("callback page = %d, want 502", status)
	}
}

func TestOpenRouterOAuthAllowsOnlyOneExchangePerCallback(t *testing.T) {
	exchangeStarted := make(chan chan *http.Response, 1)
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		finish := make(chan *http.Response)
		exchangeStarted <- finish
		return <-finish, nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	callbackDone := make(chan *http.Response, 1)
	loginErrCh := make(chan error, 1)
	loginCredential := make(chan *OAuthCredential, 1)
	go func() {
		credential, loginErr := OpenRouterOAuth{}.Login(context.Background(), interaction, nil)
		if loginErr != nil {
			loginErrCh <- loginErr
			return
		}
		loginCredential <- credential
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	go func() {
		response, err := http.Get(openRouterTestCallbackURL(authorize, "authorization-code"))
		if err != nil {
			t.Error(err)
			return
		}
		callbackDone <- response
	}()
	var finish chan *http.Response
	select {
	case finish = <-exchangeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("token exchange never started")
	}
	// A second callback while the first exchange is in flight → 409.
	status, _ := openRouterTestGetPage(t, openRouterTestCallbackURL(authorize, "authorization-code"))
	if status != http.StatusConflict {
		t.Errorf("second callback = %d, want 409", status)
	}
	finish <- openRouterTestJSONResponse(200, `{"key":"sk-or-test"}`)
	select {
	case <-loginCredential:
	case loginErr := <-loginErrCh:
		t.Fatalf("login failed: %v", loginErr)
	case <-time.After(5 * time.Second):
		t.Fatal("login never resolved")
	}
	if status, _ := openRouterTestReadPage(callbackDone, t); status != http.StatusOK {
		t.Errorf("first callback = %d, want 200", status)
	}
}

func TestOpenRouterOAuthRejectsResponseWithoutKey(t *testing.T) {
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return openRouterTestJSONResponse(200, `{"user_id":"user-1"}`), nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	callbackDone := make(chan *http.Response, 1)
	loginErrCh := make(chan error, 1)
	go func() {
		if _, loginErr := (OpenRouterOAuth{}).Login(context.Background(), interaction, nil); loginErr != nil {
			loginErrCh <- loginErr
		}
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	go func() {
		response, err := http.Get(openRouterTestCallbackURL(authorize, "code-without-key"))
		if err != nil {
			t.Error(err)
			return
		}
		callbackDone <- response
	}()
	select {
	case loginErr := <-loginErrCh:
		if loginErr.Error() != `OpenRouter OAuth response carries no "key"` {
			t.Fatalf("login error = %v", loginErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login never failed")
	}
	if status, _ := openRouterTestReadPage(callbackDone, t); status != http.StatusBadGateway {
		t.Errorf("callback page = %d, want 502", status)
	}
}

func TestOpenRouterOAuthMintsKeyFromPastedRedirectURL(t *testing.T) {
	var exchangeBody map[string]any
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != openRouterTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		if err := json.Unmarshal([]byte(body), &exchangeBody); err != nil {
			return nil, err
		}
		return openRouterTestJSONResponse(200, `{"key":"sk-or-manual"}`), nil
	})
	// The manual prompt consumes the advertised callback URL and pastes it
	// back with a code attached.
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	interaction.promptFn = func(AuthPrompt) (string, error) {
		authorize, parseErr := url.Parse(<-interaction.authorizeCh)
		if parseErr != nil {
			return "", parseErr
		}
		return authorize.Query().Get("callback_url") + "?code=manual-code", nil
	}
	credential, err := (OpenRouterOAuth{}).Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != "sk-or-manual" || credential.Refresh != "" || credential.Expires != 1<<63-1 {
		t.Errorf("credential = %+v", credential.OAuthCredentials)
	}
	if exchangeBody["code"] != "manual-code" || exchangeBody["code_challenge_method"] != "S256" {
		t.Errorf("exchange body = %v", exchangeBody)
	}
}

func TestOpenRouterOAuthAcceptsBareAuthorizationCode(t *testing.T) {
	var exchangeBody map[string]any
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != openRouterTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		if err := json.Unmarshal([]byte(body), &exchangeBody); err != nil {
			return nil, err
		}
		return openRouterTestJSONResponse(200, `{"key":"sk-or-manual"}`), nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1), promptFn: func(AuthPrompt) (string, error) {
		return "  manual-code  ", nil
	}}
	credential, err := OpenRouterOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != "sk-or-manual" {
		t.Errorf("access = %q", credential.Access)
	}
	if exchangeBody["code"] != "manual-code" {
		t.Errorf("exchange code = %v", exchangeBody["code"])
	}
}

func TestOpenRouterOAuthFailsWhenManualPromptCancelled(t *testing.T) {
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return openRouterTestJSONResponse(200, `{"key":"sk-or-unexpected"}`), nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1), promptFn: func(AuthPrompt) (string, error) {
		return "", fmt.Errorf("Login cancelled")
	}}
	if _, err := (OpenRouterOAuth{}).Login(context.Background(), interaction, nil); err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v, want Login cancelled", err)
	}
}

func TestOpenRouterOAuthRejectsEmptyManualInput(t *testing.T) {
	stubOpenRouterOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return openRouterTestJSONResponse(200, `{"key":"sk-or-unexpected"}`), nil
	})
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1), promptFn: func(AuthPrompt) (string, error) {
		return "   ", nil
	}}
	if _, err := (OpenRouterOAuth{}).Login(context.Background(), interaction, nil); err == nil || err.Error() != "Missing authorization code" {
		t.Fatalf("error = %v, want Missing authorization code", err)
	}
}

func TestOpenRouterOAuthClosesPendingCallbackOnCancel(t *testing.T) {
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	loginErrCh := make(chan error, 1)
	go func() {
		_, loginErr := OpenRouterOAuth{}.Login(ctx, interaction, nil)
		loginErrCh <- loginErr
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	cancel()
	select {
	case loginErr := <-loginErrCh:
		if loginErr.Error() != "Login cancelled" {
			t.Fatalf("error = %v, want Login cancelled", loginErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("login never failed")
	}
	// The listener is gone: the callback URL now refuses connections.
	if _, err := http.Get(authorize.Query().Get("callback_url")); err == nil {
		t.Error("expected the callback server to be closed")
	}
}

func TestOpenRouterOAuthRejectsWhenLoginAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1), promptFn: func(AuthPrompt) (string, error) {
		return "", nil
	}}
	if _, err := (OpenRouterOAuth{}).Login(ctx, interaction, nil); err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("error = %v, want Login cancelled", err)
	}
}

func TestOpenRouterOAuthUsesConfiguredCallbackHost(t *testing.T) {
	t.Setenv("PI_OAUTH_CALLBACK_HOST", "localhost")
	interaction := &openRouterFlowInteraction{authorizeCh: make(chan string, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	loginErrCh := make(chan error, 1)
	go func() {
		_, loginErr := OpenRouterOAuth{}.Login(ctx, interaction, nil)
		loginErrCh <- loginErr
	}()
	authorize := openRouterTestAuthorizeURL(t, interaction)
	callback, err := url.Parse(authorize.Query().Get("callback_url"))
	if err != nil {
		t.Fatal(err)
	}
	if callback.Hostname() != "localhost" {
		t.Fatalf("callback host = %q, want localhost", callback.Hostname())
	}
	cancel()
	<-loginErrCh
}
