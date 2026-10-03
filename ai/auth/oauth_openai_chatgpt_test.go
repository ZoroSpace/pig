// pi: test/openai-chatgpt-oauth.test.ts.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const chatGPTTestDeviceID = "e61bbe28-07ef-466d-8e5d-a344f94ab305"

const chatGPTTestRequiredScope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

// stubChatGPTOAuthDo swaps the token-endpoint transport seam (pi:
// vi.stubGlobal("fetch")).
func stubChatGPTOAuthDo(t *testing.T, stub func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error)) {
	t.Helper()
	previous := chatGPTOAuthDo
	chatGPTOAuthDo = stub
	t.Cleanup(func() { chatGPTOAuthDo = previous })
}

func chatGPTTestJSONResponse(t *testing.T, status int, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func chatGPTTestTokenJSON(t *testing.T, scope string) string {
	t.Helper()
	if scope == "" {
		scope = chatGPTTestRequiredScope
	}
	encoded, err := json.Marshal(map[string]any{
		"access_token":  "access-token",
		"refresh_token": "refresh-token",
		"expires_in":    3600,
		"id_token":      "id-token",
		"scope":         scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

type chatGPTTestInteraction struct {
	authorizeURL     *url.URL
	callbackClientID string
	t                *testing.T
}

func (i *chatGPTTestInteraction) Notify(event AuthEvent) {
	if event.Type != "auth_url" {
		return
	}
	parsed, err := url.Parse(event.URL)
	if err != nil {
		i.t.Fatal(err)
	}
	i.authorizeURL = parsed
}

func (i *chatGPTTestInteraction) Prompt(prompt AuthPrompt) (string, error) {
	if prompt.Type != "manual_code" {
		return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
	}
	if i.authorizeURL == nil {
		return "", fmt.Errorf("Authorization URL was not emitted before the callback prompt")
	}
	callback, err := url.Parse(i.authorizeURL.Query().Get("redirect_uri"))
	if err != nil {
		return "", err
	}
	query := callback.Query()
	query.Set("code", "authorization-code")
	query.Set("state", i.authorizeURL.Query().Get("state"))
	if i.callbackClientID != "" {
		query.Set("client_id", i.callbackClientID)
	}
	callback.RawQuery = query.Encode()
	return callback.String(), nil
}

func chatGPTTestConnectedCredential(t *testing.T) *OAuthCredential {
	t.Helper()
	scopes := strings.Fields(chatGPTTestRequiredScope)
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Access:  "old-access",
			Refresh: "old-refresh",
			Extra: map[string]any{
				"clientId": "oaiapp_existing",
				"scopes":   scopes,
			},
		},
	}
}

func TestChatGPTOAuthRegistersUserOwnedClientAndStoresIssuedID(t *testing.T) {
	var exchangeBody url.Values
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != chatGPTTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		values, err := url.ParseQuery(body)
		if err != nil {
			return nil, err
		}
		exchangeBody = values
		return chatGPTTestJSONResponse(t, 200, chatGPTTestTokenJSON(t, "")), nil
	})

	credential, err := loginOpenAIChatGPT(context.Background(), &chatGPTTestInteraction{
		callbackClientID: "oaiapp_issued",
		t:                t,
	}, &LoginOptions{GetDeviceId: func() string { return chatGPTTestDeviceID }})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != "access-token" || credential.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credential.OAuthCredentials)
	}
	if got, _ := credential.Extra["clientId"].(string); got != "oaiapp_issued" {
		t.Errorf("clientId = %q, want oaiapp_issued", got)
	}
	if got, _ := credential.Extra["scopes"].([]string); len(got) != len(strings.Fields(chatGPTTestRequiredScope)) {
		t.Errorf("scopes = %v", got)
	}
	if exchangeBody.Get("client_id") != "oaiapp_issued" {
		t.Errorf("exchange client_id = %q", exchangeBody.Get("client_id"))
	}
	if exchangeBody.Get("code") != "authorization-code" {
		t.Errorf("exchange code = %q", exchangeBody.Get("code"))
	}
	if exchangeBody.Get("resource") != "https://api.openai.com/v1" {
		t.Errorf("exchange resource = %q", exchangeBody.Get("resource"))
	}
	if exchangeBody.Get("code_verifier") == "" {
		t.Error("exchange code_verifier is empty")
	}
}

func TestChatGPTOAuthAuthorizeURLCarriesTheRegistrationParameters(t *testing.T) {
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != chatGPTTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		return chatGPTTestJSONResponse(t, 200, chatGPTTestTokenJSON(t, "")), nil
	})
	interaction := &chatGPTTestInteraction{callbackClientID: "oaiapp_issued", t: t}
	if _, err := loginOpenAIChatGPT(context.Background(), interaction, &LoginOptions{GetDeviceId: func() string { return chatGPTTestDeviceID }}); err != nil {
		t.Fatalf("login failed: %v", err)
	}
	query := interaction.authorizeURL.Query()
	for field, want := range map[string]string{
		"client_id":             "dynamic_agent_client",
		"agent_name_hint":       "Pi",
		"ext_agent_host_id":     "urn:uuid:" + chatGPTTestDeviceID,
		"scope":                 chatGPTTestRequiredScope,
		"redirect_uri":          chatGPTRedirectURI,
		"resource":              "https://api.openai.com/v1",
		"code_challenge_method": "S256",
	} {
		if got := query.Get(field); got != want {
			t.Errorf("authorize URL %s = %q, want %q", field, got, want)
		}
	}
	if query.Get("code_challenge") == "" || query.Get("state") == "" || query.Get("nonce") == "" {
		t.Error("authorize URL missing code_challenge/state/nonce")
	}
}

func TestChatGPTOAuthRejectsRegistrationWithoutIssuedClientID(t *testing.T) {
	tokenCalled := false
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		tokenCalled = true
		return chatGPTTestJSONResponse(t, 200, chatGPTTestTokenJSON(t, "")), nil
	})
	interaction := &chatGPTTestInteraction{t: t}
	_, err := loginOpenAIChatGPT(context.Background(), interaction, &LoginOptions{GetDeviceId: func() string { return chatGPTTestDeviceID }})
	if err == nil || !strings.Contains(err.Error(), "registration callback did not contain an issued client ID") {
		t.Fatalf("error = %v", err)
	}
	if tokenCalled {
		t.Error("token endpoint was called without an issued client ID")
	}
}

func TestChatGPTOAuthRejectsTokenResponseWithoutDirectScope(t *testing.T) {
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != chatGPTTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		return chatGPTTestJSONResponse(t, 200, chatGPTTestTokenJSON(t, "openid profile email offline_access resource.invoke")), nil
	})
	interaction := &chatGPTTestInteraction{callbackClientID: "oaiapp_issued", t: t}
	_, err := loginOpenAIChatGPT(context.Background(), interaction, &LoginOptions{GetDeviceId: func() string { return chatGPTTestDeviceID }})
	if err == nil || !strings.Contains(err.Error(), "grant did not include chatgpt.tokens.use.direct") {
		t.Fatalf("error = %v", err)
	}
}

func TestChatGPTOAuthRequiresDeviceIDBeforeStartingAuthorization(t *testing.T) {
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
	})
	interaction := &chatGPTTestInteraction{t: t}
	if _, err := loginOpenAIChatGPT(context.Background(), interaction, nil); err == nil || !strings.Contains(err.Error(), "requires a device ID") {
		t.Fatalf("nil-options error = %v", err)
	}
	if _, err := loginOpenAIChatGPT(context.Background(), interaction, &LoginOptions{GetDeviceId: func() string { return "not-a-uuid" }}); err == nil || !strings.Contains(err.Error(), "requires a device ID") {
		t.Fatalf("bad-uuid error = %v", err)
	}
	if interaction.authorizeURL != nil {
		t.Error("authorization started without a device ID")
	}
}

func TestChatGPTOAuthRefreshRequiresRefreshTokenRotation(t *testing.T) {
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != chatGPTTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		return chatGPTTestJSONResponse(t, 200, `{"access_token":"access","expires_in":3600,"id_token":"id","scope":"`+chatGPTTestRequiredScope+`"}`), nil
	})
	_, err := chatGPTRefreshAccessToken(context.Background(), chatGPTTestConnectedCredential(t))
	if err == nil || !strings.Contains(err.Error(), "token response has invalid refresh_token") {
		t.Fatalf("error = %v", err)
	}
}

func TestChatGPTOAuthRefreshUsesIssuedClientIDAndStoresReplacementScopes(t *testing.T) {
	var refreshBody url.Values
	stubChatGPTOAuthDo(t, func(_ context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != chatGPTTokenURL {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		values, err := url.ParseQuery(body)
		if err != nil {
			return nil, err
		}
		refreshBody = values
		return chatGPTTestJSONResponse(t, 200, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"id_token":"id","scope":"`+chatGPTTestRequiredScope+`"}`), nil
	})

	before := time.Now().UnixMilli()
	credential, err := chatGPTRefreshAccessToken(context.Background(), chatGPTTestConnectedCredential(t))
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	// expires_in is 3600 seconds; the credential expires 3 minutes early so
	// it is refreshed in time.
	if credential.Expires < before+(3600-180)*1000 || credential.Expires > after+(3600-180)*1000 {
		t.Errorf("expires = %d outside [%d, %d]", credential.Expires, before+(3600-180)*1000, after+(3600-180)*1000)
	}
	if refreshBody.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", refreshBody.Get("grant_type"))
	}
	if refreshBody.Get("client_id") != "oaiapp_existing" {
		t.Errorf("client_id = %q", refreshBody.Get("client_id"))
	}
	if refreshBody.Get("refresh_token") != "old-refresh" {
		t.Errorf("refresh_token = %q", refreshBody.Get("refresh_token"))
	}
	if refreshBody.Get("resource") != "https://api.openai.com/v1" {
		t.Errorf("resource = %q", refreshBody.Get("resource"))
	}
	if refreshBody.Has("scope") {
		t.Error("refresh request carried a scope")
	}
	if credential.Access != "new-access" || credential.Refresh != "new-refresh" {
		t.Errorf("credentials = %+v", credential.OAuthCredentials)
	}
	if got, _ := credential.Extra["clientId"].(string); got != "oaiapp_existing" {
		t.Errorf("clientId = %q, want oaiapp_existing", got)
	}
	if got, _ := credential.Extra["scopes"].([]string); len(got) != len(strings.Fields(chatGPTTestRequiredScope)) {
		t.Errorf("scopes = %v", got)
	}
}

// TestChatGPTOAuthFailsWhenCallbackPortIsTaken pins the fail-closed behavior
// (pi #10265): when port 1455 is held by another pending login, the login
// fails with a clear error instead of degrading to manual paste — the browser
// callback would reach the other listener and be rejected as a state mismatch.
func TestChatGPTOAuthFailsWhenCallbackPortIsTaken(t *testing.T) {
	listener, err := net.Listen("tcp", net.JoinHostPort(chatGPTCallbackHost, fmt.Sprintf("%d", chatGPTCallbackPort)))
	if err != nil {
		t.Skipf("cannot occupy port %d: %v", chatGPTCallbackPort, err)
	}
	defer func() { _ = listener.Close() }()

	interaction := &chatGPTFailClosedInteraction{}
	_, loginErr := loginOpenAIChatGPT(context.Background(), interaction, &LoginOptions{GetDeviceId: func() string { return chatGPTTestDeviceID }})
	if loginErr == nil {
		t.Fatal("login succeeded while the callback port was held by another listener")
	}
	if !strings.Contains(loginErr.Error(), fmt.Sprintf("Port %d is in use", chatGPTCallbackPort)) {
		t.Fatalf("login error = %v, want a port-in-use error", loginErr)
	}
	if interaction.notified != 0 || interaction.prompted != 0 {
		t.Fatalf("login notified %d events and prompted %d times; want fail-closed before either", interaction.notified, interaction.prompted)
	}
}

// chatGPTFailClosedInteraction records any interaction; the fail-closed path
// must reach neither Notify nor Prompt.
type chatGPTFailClosedInteraction struct {
	notified int
	prompted int
}

func (i *chatGPTFailClosedInteraction) Notify(event AuthEvent) { i.notified++ }

func (i *chatGPTFailClosedInteraction) Prompt(prompt AuthPrompt) (string, error) {
	i.prompted++
	return "", fmt.Errorf("no manual paste expected")
}
