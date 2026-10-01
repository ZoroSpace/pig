// pi: test/openai-codex-oauth.test.ts.
//
// pi drives the poll loop with vi.useFakeTimers; pig stubs the
// deviceCodeNow/deviceCodeSleep seams with oauthFakeClock, which reproduces
// the fake-timer semantics: a sleep registers a timer at now+ms (signaling
// the test for determinism), advance fires timers whose deadline has
// passed, and ctx cancellation rejects a pending sleep with "Login
// cancelled".
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// oauthFakeClock is pi's fake timers for the device poll loop.
type oauthFakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*oauthFakeTimer
}

type oauthFakeTimer struct {
	deadline time.Time
	fired    chan struct{}
}

func (c *oauthFakeClock) nowFunc() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// sleep registers a timer at now+ms, then (for test determinism) signals
// the registration on registered before waiting for the fire or ctx
// cancellation (pi: setTimeout + advanceTimersByTimeAsync; abortableSleep
// rejects immediately when the signal is already aborted).
func (c *oauthFakeClock) sleep(ctx context.Context, ms int64, registered chan<- int64) error {
	c.mu.Lock()
	timer := &oauthFakeTimer{
		deadline: c.now.Add(time.Duration(ms) * time.Millisecond),
		fired:    make(chan struct{}),
	}
	if !timer.deadline.After(c.now) {
		close(timer.fired)
	} else {
		c.timers = append(c.timers, timer)
	}
	c.mu.Unlock()
	if registered != nil {
		registered <- ms
	}
	select {
	case <-ctx.Done():
		return errors.New("Login cancelled")
	case <-timer.fired:
		return nil
	}
}

// advance moves the clock and fires every timer whose deadline has passed
// (pi: vi.advanceTimersByTimeAsync).
func (c *oauthFakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	remaining := make([]*oauthFakeTimer, 0, len(c.timers))
	for _, timer := range c.timers {
		if !timer.deadline.After(c.now) {
			close(timer.fired)
		} else {
			remaining = append(remaining, timer)
		}
	}
	c.timers = remaining
	c.mu.Unlock()
}

// stubCodexOAuthClock wires the fake clock into the poll-loop seams.
func stubCodexOAuthClock(t *testing.T, clock *oauthFakeClock, sleepCalls chan int64) {
	t.Helper()
	previousNow := deviceCodeNow
	previousSleep := deviceCodeSleep
	deviceCodeNow = clock.nowFunc
	deviceCodeSleep = func(ctx context.Context, ms int64) error {
		return clock.sleep(ctx, ms, sleepCalls)
	}
	t.Cleanup(func() {
		deviceCodeNow = previousNow
		deviceCodeSleep = previousSleep
	})
}

// stubCodexOAuthDo swaps the OAuth transport seam (pi: vi.stubGlobal("fetch")).
func stubCodexOAuthDo(t *testing.T, handler func(method, requestURL, contentType, body string) (*http.Response, error)) {
	t.Helper()
	previous := codexOAuthDo
	codexOAuthDo = func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
		return handler(method, requestURL, contentType, body)
	}
	t.Cleanup(func() { codexOAuthDo = previous })
}

func codexTestResponse(status int, statusText, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, statusText),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func codexTestJSONResponse(status int, body string) *http.Response {
	return codexTestResponse(status, http.StatusText(status), "application/json", body)
}

// codexTestAccessToken builds a JWT with the claim pi's fixture carries
// (Buffer.toString("base64") is padded standard base64).
func codexTestAccessToken(accountID string) string {
	header := base64.StdEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadJSON, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	if err != nil {
		panic(err)
	}
	payload := base64.StdEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".signature"
}

// codexTestAssertJSONBody asserts a JSON request body equals the expected
// object (pi: expect(JSON.parse(...)).toEqual(...)).
func codexTestAssertJSONBody(t *testing.T, body string, expected map[string]any) {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Errorf("request body is not a JSON object: %q (%v)", body, err)
		return
	}
	if !reflect.DeepEqual(parsed, expected) {
		t.Errorf("request body = %v, want %v", parsed, expected)
	}
}

// codexTestDeviceAuthPending is pi's deviceAuthPendingResponse: a 403 whose
// error object carries the pending code.
func codexTestDeviceAuthPending() *http.Response {
	return codexTestJSONResponse(403, `{"error":{"message":"Device authorization is pending. Please try again.","type":"invalid_request_error","param":null,"code":"deviceauth_authorization_pending"}}`)
}

// codexTestLoginAsync runs Login on a goroutine and reports the outcome on a
// buffered channel.
func codexTestLoginAsync(ctx context.Context, interaction AuthInteraction) <-chan loginOutcome {
	resultCh := make(chan loginOutcome, 1)
	go func() {
		credential, err := OpenAICodexOAuth{}.Login(ctx, interaction, nil)
		resultCh <- loginOutcome{credential: credential, err: err}
	}()
	return resultCh
}

type loginOutcome struct {
	credential *OAuthCredential
	err        error
}

// codexTestAwait waits for the login outcome without hanging the suite.
func codexTestAwait(t *testing.T, resultCh <-chan loginOutcome) loginOutcome {
	t.Helper()
	select {
	case outcome := <-resultCh:
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("login did not settle within 5s (poll loop stuck?)")
		return loginOutcome{}
	}
}

// codexTestAwaitSleep waits for the next poll-loop sleep registration.
func codexTestAwaitSleep(t *testing.T, sleepCalls chan int64) int64 {
	t.Helper()
	select {
	case ms := <-sleepCalls:
		return ms
	case <-time.After(5 * time.Second):
		t.Fatal("device poll loop did not register a sleep within 5s")
		return 0
	}
}

func TestCodexOAuthDeviceCodeFlowLogsIn(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCodexOAuthClock(t, clock, sleepCalls)

	accessToken := codexTestAccessToken("account-123")
	var deviceInfos []AuthEvent
	var pollTimes []time.Time
	pollResponses := []func() *http.Response{
		func() *http.Response { return codexTestDeviceAuthPending() },
		func() *http.Response {
			return codexTestJSONResponse(200, `{"authorization_code":"oauth-code","code_challenge":"device-code-challenge","code_verifier":"device-code-verifier"}`)
		},
	}

	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			if method != http.MethodPost {
				t.Errorf("usercode method = %q, want POST", method)
			}
			if contentType != "application/json" {
				t.Errorf("usercode content-type = %q, want application/json", contentType)
			}
			codexTestAssertJSONBody(t, body, map[string]any{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"})
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"5"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			pollTimes = append(pollTimes, clock.nowFunc())
			if method != http.MethodPost {
				t.Errorf("device token method = %q, want POST", method)
			}
			codexTestAssertJSONBody(t, body, map[string]any{
				"device_auth_id": "device-auth-id",
				"user_code":      "ABCD-1234",
			})
			if len(pollResponses) == 0 {
				t.Error("Unexpected extra device auth poll")
				return codexTestDeviceAuthPending(), nil
			}
			response := pollResponses[0]
			pollResponses = pollResponses[1:]
			return response(), nil
		case "https://auth.openai.com/oauth/token":
			if contentType != "application/x-www-form-urlencoded" {
				t.Errorf("token content-type = %q, want application/x-www-form-urlencoded", contentType)
			}
			values, err := url.ParseQuery(body)
			if err != nil {
				t.Errorf("token body is not form-encoded: %q", body)
				return nil, err
			}
			for field, want := range map[string]string{
				"grant_type":    "authorization_code",
				"client_id":     "app_EMoamEEZ73f0CkXaXp7hrann",
				"code":          "oauth-code",
				"redirect_uri":  "https://auth.openai.com/deviceauth/callback",
				"code_verifier": "device-code-verifier",
			} {
				if got := values.Get(field); got != want {
					t.Errorf("token %s = %q, want %q", field, got, want)
				}
			}
			return codexTestJSONResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, accessToken)), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
			return "device_code", nil
		},
		notify: func(event AuthEvent) {
			if event.Type == "device_code" {
				deviceInfos = append(deviceInfos, event)
			}
		},
	}

	resultCh := codexTestLoginAsync(context.Background(), interaction)

	start := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	if ms := codexTestAwaitSleep(t, sleepCalls); ms != 5000 {
		t.Errorf("first sleep = %dms, want 5000ms (interval \"5\")", ms)
	}
	if len(deviceInfos) != 1 {
		t.Fatalf("device infos = %+v, want exactly one", deviceInfos)
	}
	info := deviceInfos[0]
	if info.UserCode != "ABCD-1234" || info.VerificationURI != "https://auth.openai.com/codex/device" {
		t.Errorf("device info = %+v", info)
	}
	if info.IntervalSeconds == nil || *info.IntervalSeconds != 5 {
		t.Errorf("intervalSeconds = %v, want 5", info.IntervalSeconds)
	}
	if info.ExpiresInSeconds == nil || *info.ExpiresInSeconds != 900 {
		t.Errorf("expiresInSeconds = %v, want 900", info.ExpiresInSeconds)
	}
	if len(pollTimes) != 1 || !pollTimes[0].Equal(start) {
		t.Fatalf("poll times = %v, want [%v]", pollTimes, start)
	}

	// 4999ms later the first poll interval has not elapsed yet.
	clock.advance(4999 * time.Millisecond)
	if len(pollTimes) != 1 {
		t.Fatalf("poll times after 4999ms = %v, want no extra poll", pollTimes)
	}
	clock.advance(1 * time.Millisecond)

	outcome := codexTestAwait(t, resultCh)
	if outcome.err != nil {
		t.Fatalf("login failed: %v", outcome.err)
	}
	if outcome.credential.Access != accessToken || outcome.credential.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", outcome.credential.OAuthCredentials)
	}
	if want := start.Add(5000*time.Millisecond).UnixMilli() + 3600*1000; outcome.credential.Expires != want {
		t.Errorf("expires = %d, want %d", outcome.credential.Expires, want)
	}
	if got, _ := outcome.credential.Extra["accountId"].(string); got != "account-123" {
		t.Errorf("accountId = %q, want account-123", got)
	}
	if len(pollTimes) != 2 || !pollTimes[1].Equal(start.Add(5*time.Second)) {
		t.Fatalf("poll times = %v, want [%v %v]", pollTimes, start, start.Add(5*time.Second))
	}
}

func TestCodexOAuthOffersBrowserLoginFirst(t *testing.T) {
	accessToken := codexTestAccessToken("account-456")
	var selectPrompts []AuthPrompt
	var deviceInfos []AuthEvent
	var sawAuthURL bool

	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			codexTestAssertJSONBody(t, body, map[string]any{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"})
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"WXYZ-7890","interval":"5"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			return codexTestJSONResponse(200, `{"authorization_code":"oauth-code","code_challenge":"device-code-challenge","code_verifier":"device-code-verifier"}`), nil
		case "https://auth.openai.com/oauth/token":
			return codexTestJSONResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, accessToken)), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", errors.New("Text prompt should not be used")
			}
			selectPrompts = append(selectPrompts, prompt)
			return "device_code", nil
		},
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				sawAuthURL = true
			}
			if event.Type == "device_code" {
				deviceInfos = append(deviceInfos, event)
			}
		},
	}

	credential, err := OpenAICodexOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != accessToken || credential.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credential.OAuthCredentials)
	}
	if got, _ := credential.Extra["accountId"].(string); got != "account-456" {
		t.Errorf("accountId = %q, want account-456", got)
	}
	if len(selectPrompts) != 1 {
		t.Fatalf("select prompts = %+v, want exactly one", selectPrompts)
	}
	prompt := selectPrompts[0]
	if prompt.Type != "select" || prompt.Message != "Select OpenAI Codex login method:" {
		t.Errorf("prompt = %+v", prompt)
	}
	wantOptions := []AuthPromptOption{
		{ID: "browser", Label: "Browser login (default)"},
		{ID: "device_code", Label: "Device code login (headless)"},
	}
	if !reflect.DeepEqual(prompt.Options, wantOptions) {
		t.Errorf("prompt options = %+v, want %+v", prompt.Options, wantOptions)
	}
	if sawAuthURL {
		t.Error("Browser login should not start")
	}
	if len(deviceInfos) != 1 || deviceInfos[0].UserCode != "WXYZ-7890" {
		t.Fatalf("device infos = %+v", deviceInfos)
	}
	if deviceInfos[0].VerificationURI != "https://auth.openai.com/codex/device" ||
		deviceInfos[0].IntervalSeconds == nil || *deviceInfos[0].IntervalSeconds != 5 ||
		deviceInfos[0].ExpiresInSeconds == nil || *deviceInfos[0].ExpiresInSeconds != 900 {
		t.Errorf("device info = %+v", deviceInfos[0])
	}
}

func TestCodexOAuthSelectionCancelled(t *testing.T) {
	interaction := &oauthTestInteraction{
		prompt: func(AuthPrompt) (string, error) {
			return "", errors.New("Login cancelled")
		},
		notify: func(AuthEvent) {},
	}
	_, err := OpenAICodexOAuth{}.Login(context.Background(), interaction, nil)
	if err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("login error = %v, want Login cancelled", err)
	}
}

func TestCodexOAuthDeviceFlowCancelWhileWaiting(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCodexOAuthClock(t, clock, sleepCalls)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			codexTestAssertJSONBody(t, body, map[string]any{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"})
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"5"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			return codexTestDeviceAuthPending(), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
			return "device_code", nil
		},
		notify: func(AuthEvent) {},
	}

	resultCh := codexTestLoginAsync(ctx, interaction)
	codexTestAwaitSleep(t, sleepCalls)
	cancel()

	outcome := codexTestAwait(t, resultCh)
	if outcome.err == nil || outcome.err.Error() != "Login cancelled" {
		t.Fatalf("login error = %v, want Login cancelled", outcome.err)
	}
	if outcome.credential != nil {
		t.Errorf("credential = %+v, want nil", outcome.credential)
	}
}

func TestCodexOAuthDeviceFlowTimesOutAfter15Minutes(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCodexOAuthClock(t, clock, sleepCalls)

	var pollTimes []time.Time
	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			codexTestAssertJSONBody(t, body, map[string]any{"client_id": "app_EMoamEEZ73f0CkXaXp7hrann"})
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"60"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			pollTimes = append(pollTimes, clock.nowFunc())
			return codexTestDeviceAuthPending(), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
			return "device_code", nil
		},
		notify: func(AuthEvent) {},
	}

	resultCh := codexTestLoginAsync(context.Background(), interaction)
	pendingMs := codexTestAwaitSleep(t, sleepCalls)
	if len(pollTimes) != 1 {
		t.Fatalf("poll times = %v, want exactly one before advancing", pollTimes)
	}

	// Drive the loop: each poll schedules the next 60s sleep; past the 15
	// minute deadline the flow times out (pi: advanceTimersByTimeAsync(15min)).
	for {
		if pendingMs >= 0 {
			clock.advance(time.Duration(pendingMs) * time.Millisecond)
			pendingMs = -1
			continue
		}
		select {
		case outcome := <-resultCh:
			if outcome.err == nil || outcome.err.Error() != "Device flow timed out" {
				t.Fatalf("login error = %v, want Device flow timed out", outcome.err)
			}
			return
		case ms := <-sleepCalls:
			pendingMs = ms
		case <-time.After(5 * time.Second):
			t.Fatal("device flow did not time out within 5s")
		}
	}
}

func TestCodexOAuthDeviceAuth403And404Pending(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCodexOAuthClock(t, clock, sleepCalls)

	accessToken := codexTestAccessToken("account-403-404")
	var pollTimes []time.Time
	pollResponses := []func() *http.Response{
		func() *http.Response {
			return codexTestJSONResponse(403, `{"error":"access_denied","error_description":"denied"}`)
		},
		func() *http.Response {
			return codexTestResponse(404, "Not Found", "text/plain", "not ready")
		},
		func() *http.Response {
			return codexTestJSONResponse(200, `{"authorization_code":"oauth-code","code_challenge":"device-code-challenge","code_verifier":"device-code-verifier"}`)
		},
	}

	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"1"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			pollTimes = append(pollTimes, clock.nowFunc())
			if len(pollResponses) == 0 {
				t.Error("Unexpected extra device auth poll")
				return codexTestDeviceAuthPending(), nil
			}
			response := pollResponses[0]
			pollResponses = pollResponses[1:]
			return response(), nil
		case "https://auth.openai.com/oauth/token":
			return codexTestJSONResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, accessToken)), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
			return "device_code", nil
		},
		notify: func(AuthEvent) {},
	}

	resultCh := codexTestLoginAsync(context.Background(), interaction)
	// Two pending polls each wait one interval before the completing poll.
	for i := 0; i < 2; i++ {
		ms := codexTestAwaitSleep(t, sleepCalls)
		clock.advance(time.Duration(ms) * time.Millisecond)
	}
	outcome := codexTestAwait(t, resultCh)
	if outcome.err != nil {
		t.Fatalf("login failed: %v", outcome.err)
	}
	if outcome.credential.Access != accessToken || outcome.credential.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", outcome.credential.OAuthCredentials)
	}
	if got, _ := outcome.credential.Extra["accountId"].(string); got != "account-403-404" {
		t.Errorf("accountId = %q, want account-403-404", got)
	}
	if len(pollTimes) != 3 {
		t.Fatalf("poll times = %v, want 3 polls", pollTimes)
	}
}

func TestCodexOAuthDeviceAuthPollFailureIncludesBody(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCodexOAuthClock(t, clock, sleepCalls)

	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		switch requestURL {
		case "https://auth.openai.com/api/accounts/deviceauth/usercode":
			return codexTestJSONResponse(200, `{"device_auth_id":"device-auth-id","user_code":"ABCD-1234","interval":"5"}`), nil
		case "https://auth.openai.com/api/accounts/deviceauth/token":
			return codexTestJSONResponse(500, `{"error":"server_error","error_description":"try again later"}`), nil
		default:
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type != "select" {
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
			return "device_code", nil
		},
		notify: func(AuthEvent) {},
	}

	resultCh := codexTestLoginAsync(context.Background(), interaction)
	outcome := codexTestAwait(t, resultCh)
	want := `OpenAI Codex device auth failed with status 500: {"error":"server_error","error_description":"try again later"}`
	if outcome.err == nil || outcome.err.Error() != want {
		t.Fatalf("login error = %v, want %q", outcome.err, want)
	}
}

func TestCodexOAuthRefreshFailureNotLogged(t *testing.T) {
	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != "https://auth.openai.com/oauth/token" {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		return codexTestJSONResponse(401, `{"error":{"message":"Could not validate your token. Please try signing in again.","type":"invalid_request_error"}}`), nil
	})

	_, err := OpenAICodexOAuth{}.Refresh(context.Background(), &OAuthCredential{
		OAuthCredentials: OAuthCredentials{Access: "invalid-access-token", Refresh: "invalid-refresh-token"},
	})
	if err == nil {
		t.Fatal("refresh succeeded, want failure")
	}
	// pi: /OpenAI Codex token refresh failed \(401\).*Could not validate your token/.
	if !strings.Contains(err.Error(), "OpenAI Codex token refresh failed (401)") ||
		!strings.Contains(err.Error(), "Could not validate your token") {
		t.Fatalf("refresh error = %v, want 401 + body message", err)
	}
	// pi also asserts console.error stays silent; the pig flow never writes
	// to stderr on refresh failures.
}

// --- pig-side coverage: browser path and the callback server (pi's suite
// --- only exercises the device flow; the branches below guard the browser
// --- login flow that pi ships untested).

// TestCodexOAuthBrowserLoginManualRedirect drives the browser flow through
// the manual-code path: the pasted redirect URL parses, the state matches,
// and the code exchanges with the localhost redirect URI.
func TestCodexOAuthBrowserLoginManualRedirect(t *testing.T) {
	accessToken := codexTestAccessToken("account-browser")
	var authURL string
	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != "https://auth.openai.com/oauth/token" {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		values, err := url.ParseQuery(body)
		if err != nil {
			return nil, err
		}
		if got := values.Get("redirect_uri"); got != "http://localhost:1455/auth/callback" {
			t.Errorf("redirect_uri = %q", got)
		}
		if got := values.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if got := values.Get("code"); got != "browser-code" {
			t.Errorf("code = %q", got)
		}
		if got := values.Get("client_id"); got != "app_EMoamEEZ73f0CkXaXp7hrann" {
			t.Errorf("client_id = %q", got)
		}
		if values.Get("code_verifier") == "" {
			t.Error("code_verifier is empty")
		}
		return codexTestJSONResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh-token","expires_in":3600}`, accessToken)), nil
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			switch prompt.Type {
			case "select":
				return "browser", nil
			case "manual_code":
				if prompt.Placeholder != "http://localhost:1455/auth/callback" {
					t.Errorf("prompt placeholder = %q", prompt.Placeholder)
				}
				parsed, err := url.Parse(authURL)
				if err != nil {
					return "", err
				}
				state := parsed.Query().Get("state")
				if state == "" {
					return "", errors.New("auth URL carries no state")
				}
				return "http://localhost:1455/auth/callback?code=browser-code&state=" + state, nil
			default:
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
		},
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				authURL = event.URL
			}
		},
	}

	credential, err := OpenAICodexOAuth{}.Login(context.Background(), interaction, nil)
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credential.Access != accessToken || credential.Refresh != "refresh-token" {
		t.Errorf("credentials = %+v", credential.OAuthCredentials)
	}
	if got, _ := credential.Extra["accountId"].(string); got != "account-browser" {
		t.Errorf("accountId = %q, want account-browser", got)
	}
	if authURL == "" {
		t.Error("expected an auth_url event")
	} else {
		parsed, err := url.Parse(authURL)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		for field, want := range map[string]string{
			"response_type":              "code",
			"client_id":                  "app_EMoamEEZ73f0CkXaXp7hrann",
			"redirect_uri":               "http://localhost:1455/auth/callback",
			"scope":                      "openid profile email offline_access",
			"code_challenge_method":      "S256",
			"id_token_add_organizations": "true",
			"codex_cli_simplified_flow":  "true",
			"originator":                 "pi",
		} {
			if got := query.Get(field); got != want {
				t.Errorf("auth URL %s = %q, want %q", field, got, want)
			}
		}
		if query.Get("code_challenge") == "" || query.Get("state") == "" {
			t.Error("auth URL missing code_challenge/state")
		}
	}
}

// TestCodexOAuthBrowserLoginStateMismatch covers the manual-input state
// check (pi: parsed.state !== state → "State mismatch").
func TestCodexOAuthBrowserLoginStateMismatch(t *testing.T) {
	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
	})

	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			if prompt.Type == "select" {
				return "browser", nil
			}
			return "http://localhost:1455/auth/callback?code=x&state=WRONG", nil
		},
		notify: func(event AuthEvent) {},
	}

	_, err := OpenAICodexOAuth{}.Login(context.Background(), interaction, nil)
	if err == nil || err.Error() != "State mismatch" {
		t.Fatalf("login error = %v, want State mismatch", err)
	}
}

// TestCodexOAuthCallbackServerPortTaken locks pi's new fallback: when port
// 1455 is occupied (pi: "Port 1455 is shared with the Codex CLI"), the
// browser flow degrades to the pasted redirect URL instead of failing (pi:
// "falls back to the pasted redirect URL when the fixed callback port is
// taken"). Occupy the port unless it already is.
func TestCodexOAuthCallbackServerPortTaken(t *testing.T) {
	blocker, err := net.Listen("tcp", getCodexCallbackHost()+":1455")
	if err != nil {
		t.Skip("port 1455 already occupied; nothing to block")
	}
	defer blocker.Close()

	accessToken := codexTestAccessToken("acct")
	stubCodexOAuthDo(t, func(method, requestURL, contentType, body string) (*http.Response, error) {
		if requestURL != "https://auth.openai.com/oauth/token" {
			return nil, fmt.Errorf("Unexpected fetch URL: %s", requestURL)
		}
		values, parseErr := url.ParseQuery(body)
		if parseErr != nil {
			return nil, parseErr
		}
		if got := values.Get("code"); got != "pasted-code" {
			t.Errorf("code = %q, want pasted-code", got)
		}
		if got := values.Get("redirect_uri"); got != "http://localhost:1455/auth/callback" {
			t.Errorf("redirect_uri = %q", got)
		}
		return codexTestJSONResponse(200, fmt.Sprintf(`{"access_token":%q,"refresh_token":"refresh","expires_in":3600}`, accessToken)), nil
	})

	var authURL string
	interaction := &oauthTestInteraction{
		prompt: func(prompt AuthPrompt) (string, error) {
			switch prompt.Type {
			case "select":
				return "browser", nil
			case "manual_code":
				parsed, parseErr := url.Parse(authURL)
				if parseErr != nil {
					return "", parseErr
				}
				state := parsed.Query().Get("state")
				return "http://localhost:1455/auth/callback?code=pasted-code&state=" + state, nil
			default:
				return "", fmt.Errorf("Unexpected prompt: %s", prompt.Type)
			}
		},
		notify: func(event AuthEvent) {
			if event.Type == "auth_url" {
				authURL = event.URL
			}
		},
	}

	credential, loginErr := OpenAICodexOAuth{}.Login(context.Background(), interaction, nil)
	if loginErr != nil {
		t.Fatalf("login failed: %v", loginErr)
	}
	if got, _ := credential.Extra["accountId"].(string); got != "acct" {
		t.Errorf("accountId = %q, want acct", got)
	}
}
