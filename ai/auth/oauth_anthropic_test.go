// pi: test/anthropic-oauth.test.ts.
package auth

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
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

	credentials, err := AnthropicOAuth{}.Login(context.Background(), interaction)
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

type oauthTestUnexpectedPrompt struct{ promptType string }

func (e *oauthTestUnexpectedPrompt) Error() string { return "Unexpected prompt: " + e.promptType }

type oauthTestFailure struct{ message string }

func (e *oauthTestFailure) Error() string { return e.message }

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
			if prompt.Type == "manual_code" {
				return "the-code", nil
			}
			return "", &oauthTestUnexpectedPrompt{prompt.Type}
		},
	}

	credential, err := AnthropicOAuth{}.Login(context.Background(), interaction)
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
