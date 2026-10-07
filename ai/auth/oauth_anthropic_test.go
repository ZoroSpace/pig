// pi: test/anthropic-oauth.test.ts.
package auth

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// oauthTestInteraction stubs the login interaction (pi: the inline
// notify/prompt objects).
type oauthTestInteraction struct {
	notify func(AuthEvent)
	prompt func(AuthPrompt) (string, error)
}

func (i *oauthTestInteraction) Prompt(prompt AuthPrompt) (string, error) {
	return i.prompt(prompt)
}

func (i *oauthTestInteraction) Notify(event AuthEvent) {
	if i.notify != nil {
		i.notify(event)
	}
}

// stubAnthropicPostJSON swaps the token-endpoint transport seam (pi:
// vi.stubGlobal("fetch")).
func stubAnthropicPostJSON(t *testing.T, stub func(url string, body map[string]any) (string, error)) {
	t.Helper()
	previous := anthropicOAuthPostJSON
	anthropicOAuthPostJSON = func(_ context.Context, url string, body map[string]any) (string, error) {
		return stub(url, body)
	}
	t.Cleanup(func() { anthropicOAuthPostJSON = previous })
}

func TestAnthropicOAuthManualCallbackLogin(t *testing.T) {
	var authURL string
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if requestURL != "https://platform.claude.com/v1/oauth/token" {
			t.Errorf("url = %q", requestURL)
		}
		if body["grant_type"] != "authorization_code" {
			t.Errorf("grant_type = %v", body["grant_type"])
		}
		if body["code"] != "manual-code" {
			t.Errorf("code = %v", body["code"])
		}
		if body["redirect_uri"] != "http://localhost:53692/callback" {
			t.Errorf("redirect_uri = %v", body["redirect_uri"])
		}
		return `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, nil
	})

	interaction := &oauthTestInteraction{
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				authURL = event.URL
			}
		},
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type == "select" {
				return "browser", nil
			}
			if prompt.Type != "manual_code" {
				return "", &oauthTestUnexpectedPrompt{prompt.Type}
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				return "", err
			}
			state := parsed.Query().Get("state")
			redirectURI := parsed.Query().Get("redirect_uri")
			if state == "" || redirectURI == "" {
				return "", &oauthTestFailure{"Missing OAuth state or redirect_uri in auth URL"}
			}
			return redirectURI + "?code=manual-code&state=" + state, nil
		},
	}

	credentials, err := AnthropicOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credentials.Access != "access-token" || credentials.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credentials.OAuthCredentials)
	}
	if authURL == "" {
		t.Error("expected an auth_url event")
	}
}

// TestAnthropicOAuthFallsBackToFreeCallbackPort covers pi #10571: when the
// preferred port cannot be bound, login falls back to an OS-assigned free
// loopback port and the authorize URL and token exchange use the bound
// redirect URI.
func TestAnthropicOAuthFallsBackToFreeCallbackPort(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:53692")
	if err != nil {
		t.Skipf("cannot occupy port 53692: %v", err)
	}
	defer blocker.Close()

	var authURL string
	var exchangedRedirectURI string
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if requestURL != "https://platform.claude.com/v1/oauth/token" {
			t.Errorf("url = %q", requestURL)
		}
		exchangedRedirectURI, _ = body["redirect_uri"].(string)
		return `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, nil
	})

	interaction := &oauthTestInteraction{
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				authURL = event.URL
			}
		},
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type == "select" {
				return "browser", nil
			}
			if prompt.Type != "manual_code" {
				return "", &oauthTestUnexpectedPrompt{prompt.Type}
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				return "", err
			}
			state := parsed.Query().Get("state")
			redirectURI := parsed.Query().Get("redirect_uri")
			if state == "" || redirectURI == "" {
				return "", &oauthTestFailure{"Missing OAuth state or redirect_uri in auth URL"}
			}
			return redirectURI + "?code=manual-code&state=" + state, nil
		},
	}

	credentials, err := (AnthropicOAuth{}).Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credentials.Access != "access-token" || credentials.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credentials.OAuthCredentials)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	redirectURI := parsed.Query().Get("redirect_uri")
	bound, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Hostname() != "localhost" || bound.Path != "/callback" {
		t.Errorf("redirect URI = %q, want localhost/callback", redirectURI)
	}
	if bound.Port() == "53692" {
		t.Errorf("redirect URI = %q, want a fallback port", redirectURI)
	}
	if exchangedRedirectURI != redirectURI {
		t.Errorf("exchange redirect_uri = %q, want %q", exchangedRedirectURI, redirectURI)
	}
}

type oauthTestUnexpectedPrompt struct{ promptType string }

func (e *oauthTestUnexpectedPrompt) Error() string { return "Unexpected prompt: " + e.promptType }

type oauthTestFailure struct{ message string }

func (e *oauthTestFailure) Error() string { return e.message }

// S11-1 (#10194): the login flow asks for the method first and the copy-code
// branch exchanges the pasted code against the platform.claude.com redirect.
func TestAnthropicOAuthCopyCodeLoginFlow(t *testing.T) {
	var selectPrompts []AuthPrompt
	var authURL string
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if requestURL != "https://platform.claude.com/v1/oauth/token" {
			t.Errorf("url = %q", requestURL)
		}
		if body["grant_type"] != "authorization_code" {
			t.Errorf("grant_type = %v", body["grant_type"])
		}
		if body["code"] != "copied-code" {
			t.Errorf("code = %v", body["code"])
		}
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Errorf("auth url: %v", err)
		} else if body["state"] != parsed.Query().Get("state") {
			t.Errorf("state = %v, want %q", body["state"], parsed.Query().Get("state"))
		}
		if body["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
			t.Errorf("redirect_uri = %v", body["redirect_uri"])
		}
		return `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`, nil
	})

	interaction := &oauthTestInteraction{
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				authURL = event.URL
			}
		},
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type == "select" {
				selectPrompts = append(selectPrompts, prompt)
				return "copy_code", nil
			}
			if prompt.Type != "manual_code" {
				return "", &oauthTestUnexpectedPrompt{prompt.Type}
			}
			parsed, err := url.Parse(authURL)
			if err != nil {
				return "", err
			}
			if prompt.Placeholder != "code#state" {
				t.Errorf("placeholder = %q", prompt.Placeholder)
			}
			return "copied-code#" + parsed.Query().Get("state"), nil
		},
	}

	credentials, err := AnthropicOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credentials.Access != "access-token" || credentials.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credentials.OAuthCredentials)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("redirect_uri"); got != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("auth redirect_uri = %q", got)
	}
	if len(selectPrompts) != 1 {
		t.Fatalf("select prompts = %d, want 1", len(selectPrompts))
	}
	select_ := selectPrompts[0]
	if select_.Message != "Select Anthropic login method:" {
		t.Errorf("select message = %q", select_.Message)
	}
	if len(select_.Options) != 2 ||
		select_.Options[0].ID != "browser" || select_.Options[0].Label != "Browser login (default)" ||
		select_.Options[1].ID != "copy_code" || select_.Options[1].Label != "Copy code login (headless)" {
		t.Errorf("select options = %+v", select_.Options)
	}
}

// S11-1 (#10194): cancelling the method select cancels the login.
func TestAnthropicOAuthLoginMethodSelectCancel(t *testing.T) {
	interaction := &oauthTestInteraction{
		notify: func(AuthEvent) {},
		prompt: func(AuthPrompt) (string, error) {
			return "", &oauthTestFailure{"Login cancelled"}
		},
	}
	_, err := AnthropicOAuth{}.Login(context.Background(), interaction, nil)
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("err = %v, want Login cancelled", err)
	}
}

func TestAnthropicOAuthRefreshOmitsScope(t *testing.T) {
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if requestURL != "https://platform.claude.com/v1/oauth/token" {
			t.Errorf("url = %q", requestURL)
		}
		if body["grant_type"] != "refresh_token" {
			t.Errorf("grant_type = %v", body["grant_type"])
		}
		if clientID, _ := body["client_id"].(string); clientID == "" {
			t.Errorf("client_id = %v, want non-empty", body["client_id"])
		}
		if body["refresh_token"] != "refresh-token" {
			t.Errorf("refresh_token = %v", body["refresh_token"])
		}
		if _, hasScope := body["scope"]; hasScope {
			t.Errorf("body = %v, want it to omit scope", body)
		}
		return `{"access_token":"new-access-token","refresh_token":"new-refresh-token","expires_in":3600}`, nil
	})

	credentials, err := AnthropicOAuth{}.Refresh(context.Background(), &OAuthCredential{
		OAuthCredentials: OAuthCredentials{Access: "old-access-token", Refresh: "refresh-token"},
	})
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if credentials.Access != "new-access-token" || credentials.Refresh != "new-refresh-token" {
		t.Errorf("credentials = %+v", credentials.OAuthCredentials)
	}
}

func TestAnthropicOAuthManualCodePromptPath(t *testing.T) {
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if strings.Contains(requestURL, "/oauth/token") {
			return `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, nil
		}
		return "", &oauthTestFailure{"Unexpected fetch: " + requestURL}
	})

	var events []AuthEvent
	var prompts []AuthPrompt
	interaction := &oauthTestInteraction{
		notify: func(event AuthEvent) { events = append(events, event) },
		prompt: func(prompt AuthPrompt) (string, error) {
			prompts = append(prompts, prompt)
			if prompt.Type == "select" {
				return "browser", nil
			}
			if prompt.Type == "manual_code" {
				return "the-code", nil
			}
			return "", &oauthTestUnexpectedPrompt{prompt.Type}
		},
	}

	credential, err := AnthropicOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != "access" || credential.Refresh != "refresh" {
		t.Errorf("credential = %+v", credential.OAuthCredentials)
	}
	hasAuthURL := false
	for _, event := range events {
		if event.Type == "auth_url" {
			hasAuthURL = true
		}
	}
	if !hasAuthURL {
		t.Error("expected an auth_url event")
	}
	hasManualPrompt := false
	for _, prompt := range prompts {
		if prompt.Type == "manual_code" {
			hasManualPrompt = true
		}
	}
	if !hasManualPrompt {
		t.Error("expected a manual_code prompt")
	}
	// pi additionally asserts the prompt's signal is aborted once login
	// settles; pig's AuthInteraction has no per-prompt signal (declared
	// deviation in oauth_anthropic.go).
}

// TestAnthropicOAuthBrowserCallbackShowsSignInPage drives the browser
// callback path (pi: "completes login through the browser callback and shows
// the sign-in page"): the shared server completes the exchange before
// responding, so the page can report failures.
func TestAnthropicOAuthBrowserCallbackShowsSignInPage(t *testing.T) {
	var exchangedCode string
	stubAnthropicPostJSON(t, func(requestURL string, body map[string]any) (string, error) {
		if strings.Contains(requestURL, "/oauth/token") {
			if code, ok := body["code"].(string); ok {
				exchangedCode = code
			}
			return `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`, nil
		}
		return "", &oauthTestFailure{"Unexpected fetch: " + requestURL}
	})

	callbackPage := make(chan *http.Response, 1)
	interaction := &oauthTestInteraction{
		notify: func(event AuthEvent) {
			if event.Type != "auth_url" {
				return
			}
			parsed, err := url.Parse(event.URL)
			if err != nil {
				t.Error(err)
				return
			}
			state := parsed.Query().Get("state")
			response, err := http.Get("http://127.0.0.1:53692/callback?code=browser-code&state=" + state)
			if err != nil {
				t.Error(err)
				return
			}
			callbackPage <- response
		},
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type == "select" {
				return "browser", nil
			}
			// pig has no per-prompt abort; hold the prompt open so the
			// browser callback drives the flow (pi rejects the prompt via
			// its signal once login settles).
			select {}
		},
	}

	credential, err := (AnthropicOAuth{}).Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != "access" || credential.Refresh != "refresh" {
		t.Errorf("credential = %+v", credential.OAuthCredentials)
	}
	if exchangedCode != "browser-code" {
		t.Errorf("exchanged code = %q, want browser-code", exchangedCode)
	}
	select {
	case response := <-callbackPage:
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Signed in to Anthropic.") {
			t.Errorf("callback page = %d %q", response.StatusCode, string(body))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback page never responded")
	}
}

// TestAnthropicOAuthClientIDDecoded guards the base64-obfuscated client id
// payload (pi: decode("OWQxYzI1...")).
func TestAnthropicOAuthClientIDDecoded(t *testing.T) {
	if anthropicOAuthClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("client id = %q", anthropicOAuthClientID)
	}
	encoded, _ := json.Marshal(anthropicOAuthClientID)
	if string(encoded) == "" {
		t.Fatal("empty client id")
	}
}
