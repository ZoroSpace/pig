// oauth_meta_test.go — pi: test/meta-oauth.test.ts. Pig adaptations:
//   - pi stubs the global fetch and drives the poll loop with vi.useFakeTimers;
//     pig stubs the metaOAuthDo transport seam and the shared
//     deviceCodeNow/deviceCodeSleep clock seams (oauthFakeClock, same as the
//     codex tests).
package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func metaTestJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status) + " (meta)",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// stubMetaOAuthDo swaps the transport seam (pi: vi.stubGlobal("fetch")).
func stubMetaOAuthDo(t *testing.T, handler func(method, requestURL, contentType, body string, header http.Header) (*http.Response, error)) {
	t.Helper()
	previous := metaOAuthDo
	metaOAuthDo = func(request *http.Request) (*http.Response, error) {
		var body string
		if request.Body != nil {
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			body = string(raw)
		}
		return handler(request.Method, request.URL.String(), request.Header.Get("Content-Type"), body, request.Header)
	}
	t.Cleanup(func() { metaOAuthDo = previous })
}

// stubMetaOAuthClock wires the fake clock into the poll-loop seams and
// returns the sleep-registration channel (pi: advanceTimersByTimeAsync).
func stubMetaOAuthClock(t *testing.T, clock *oauthFakeClock) chan int64 {
	t.Helper()
	previousNow := deviceCodeNow
	previousSleep := deviceCodeSleep
	sleepCalls := make(chan int64, 16)
	deviceCodeNow = clock.nowFunc
	deviceCodeSleep = func(ctx context.Context, ms int64) error {
		return clock.sleep(ctx, ms, sleepCalls)
	}
	t.Cleanup(func() {
		deviceCodeNow = previousNow
		deviceCodeSleep = previousSleep
	})
	return sleepCalls
}

func metaTestAwaitEvent(t *testing.T, count func() int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for count() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the device_code event")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMetaOAuthLoginMintsAPIKey(t *testing.T) {
	start := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	clock := &oauthFakeClock{mu: sync.Mutex{}, now: start}
	sleepCalls := stubMetaOAuthClock(t, clock)
	pollResponses := []*http.Response{
		metaTestJSONResponse(400, `{"error":"authorization_pending"}`),
		metaTestJSONResponse(200, `{"access_token":"identity-token","token_type":"Bearer"}`),
	}
	stubMetaOAuthDo(t, func(method, requestURL, contentType, body string, header http.Header) (*http.Response, error) {
		switch requestURL {
		case metaDeviceAuthorizationURL:
			if method != http.MethodPost {
				t.Errorf("device authorization method = %s, want POST", method)
			}
			if !strings.Contains(body, "client_id="+metaClientID) {
				t.Errorf("device authorization body missing client_id: %s", body)
			}
			return metaTestJSONResponse(200,
				`{"device_code":"device-code-123","user_code":"ABCD-1234",`+
					`"verification_uri":"https://auth.meta.com/oauth/device/",`+
					`"verification_uri_complete":"https://auth.meta.com/oauth/device/?code=ABCD-1234",`+
					`"interval":5,"expires_in":600}`), nil
		case metaDeviceTokenURL:
			if !strings.Contains(body, "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") ||
				!strings.Contains(body, "device_code=device-code-123") ||
				!strings.Contains(body, "client_id="+metaClientID) {
				t.Errorf("token poll body mismatch: %s", body)
			}
			if len(pollResponses) == 0 {
				t.Fatalf("unexpected extra token poll")
			}
			response := pollResponses[0]
			pollResponses = pollResponses[1:]
			return response, nil
		case metaAPIKeyMintURL:
			if got := header.Get("Authorization"); got != "Bearer identity-token" {
				t.Errorf("mint Authorization = %q, want Bearer identity-token", got)
			}
			return metaTestJSONResponse(200, `{"api_key":"LLM|minted-key"}`), nil
		default:
			t.Fatalf("unexpected fetch URL: %s", requestURL)
			return nil, nil
		}
	})

	var eventMu sync.Mutex
	events := []AuthEvent{}
	appendEvent := func(event AuthEvent) {
		eventMu.Lock()
		defer eventMu.Unlock()
		events = append(events, event)
	}
	countEvents := func() int {
		eventMu.Lock()
		defer eventMu.Unlock()
		return len(events)
	}
	getEvent := func(i int) AuthEvent {
		eventMu.Lock()
		defer eventMu.Unlock()
		return events[i]
	}
	interaction := &oauthTestInteraction{notify: appendEvent}
	resultCh := make(chan loginOutcome, 1)
	go func() {
		credential, err := MetaOAuth{}.Login(context.Background(), interaction)
		resultCh <- loginOutcome{credential: credential, err: err}
	}()

	// pi flushes microtasks until the device_code notify fires; pig's notify
	// happens before the first poll sleep, so wait on it.
	metaTestAwaitEvent(t, countEvents)

	if countEvents() != 1 {
		t.Fatalf("events = %+v, want only the device_code event so far", events)
	}
	event := getEvent(0)
	if event.Type != "device_code" || event.UserCode != "ABCD-1234" ||
		event.VerificationURI != "https://auth.meta.com/oauth/device/?code=ABCD-1234" ||
		event.IntervalSeconds == nil || *event.IntervalSeconds != 5 ||
		event.ExpiresInSeconds == nil || *event.ExpiresInSeconds != 600 {
		t.Fatalf("device_code event mismatch: %+v", event)
	}

	// pi: await vi.advanceTimersByTimeAsync(10000) — pig advances between the
	// two 5s poll sleeps because the second timer only registers after the
	// first poll returns.
	<-sleepCalls
	clock.advance(5 * time.Second)
	<-sleepCalls
	clock.advance(5 * time.Second)
	outcome := codexTestAwait(t, resultCh)
	if outcome.err != nil {
		t.Fatalf("login error: %v", outcome.err)
	}
	want := &OAuthCredential{OAuthCredentials: OAuthCredentials{
		Refresh: "identity-token",
		Access:  "LLM|minted-key",
		Expires: start.Add(10*time.Second).UnixMilli() + 24*60*60*1000,
	}}
	if outcome.credential.Refresh != want.Refresh || outcome.credential.Access != want.Access ||
		outcome.credential.Expires != want.Expires {
		t.Fatalf("credential = %+v, want %+v", outcome.credential, want)
	}
}

func TestMetaOAuthRefreshReMintsAPIKey(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	clock := &oauthFakeClock{mu: sync.Mutex{}, now: now}
	stubMetaOAuthClock(t, clock)
	stubMetaOAuthDo(t, func(method, requestURL, contentType, body string, header http.Header) (*http.Response, error) {
		if requestURL != metaAPIKeyMintURL {
			t.Fatalf("refresh should only hit the mint endpoint, got %s", requestURL)
		}
		if got := header.Get("Authorization"); got != "Bearer identity-token" {
			t.Errorf("mint Authorization = %q, want Bearer identity-token", got)
		}
		return metaTestJSONResponse(200, `{"api_key":"LLM|fresh-key"}`), nil
	})

	credential, err := MetaOAuth{}.Refresh(context.Background(), &OAuthCredential{OAuthCredentials: OAuthCredentials{
		Refresh: "identity-token",
		Access:  "LLM|old-key",
		Expires: 1,
	}})
	if err != nil {
		t.Fatalf("refresh error: %v", err)
	}
	want := &OAuthCredential{OAuthCredentials: OAuthCredentials{
		Refresh: "identity-token",
		Access:  "LLM|fresh-key",
		Expires: now.UnixMilli() + 24*60*60*1000,
	}}
	if credential.Refresh != want.Refresh || credential.Access != want.Access || credential.Expires != want.Expires {
		t.Fatalf("credential = %+v, want %+v", credential, want)
	}
}

func TestMetaOAuthReportsSetupURLWhenNoKeyIssued(t *testing.T) {
	stubMetaOAuthDo(t, func(method, requestURL, contentType, body string, header http.Header) (*http.Response, error) {
		return metaTestJSONResponse(200, `{"require_payment":true,"action_url":"https://dev.meta.ai/billing"}`), nil
	})
	_, err := MetaOAuth{}.Refresh(context.Background(), &OAuthCredential{OAuthCredentials: OAuthCredentials{
		Refresh: "identity-token",
		Expires: 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "Complete setup at https://dev.meta.ai/billing") {
		t.Fatalf("error = %v, want setup URL guidance", err)
	}
}

func TestMetaOAuthToAuth(t *testing.T) {
	auth, err := MetaOAuth{}.ToAuth(&OAuthCredential{OAuthCredentials: OAuthCredentials{
		Refresh: "identity-token",
		Access:  "LLM|key",
		Expires: 1,
	}})
	if err != nil {
		t.Fatalf("toAuth error: %v", err)
	}
	if auth.APIKey != "LLM|key" {
		t.Fatalf("apiKey = %q, want %q", auth.APIKey, "LLM|key")
	}
}
