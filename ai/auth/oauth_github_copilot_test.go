// pi: test/github-copilot-oauth.test.ts.
//
// pi stubs global fetch (vi.stubGlobal) and drives the poll loop with
// vi.useFakeTimers; pig stubs the githubCopilotOAuthDo transport seam and the
// deviceCodeNow/deviceCodeSleep seams with oauthFakeClock (see
// oauth_openai_codex_test.go). The Models-integration tests (getAvailable
// filtering, login persisting through the credential store) live in
// ai/providers/github_copilot_test.go: package auth cannot import the
// providers package (layering).
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testCopilotAccessToken = "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;"
const testCopilotModelsURL = "https://api.individual.githubcopilot.com/models"

func copilotTestJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func copilotTestEmptyResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// copilotTestHandler is the single-endpoint fetch stub (pi: the fetchMock
// closures in github-copilot-oauth.test.ts).
type copilotTestHandler func(method, requestURL, body string) (*http.Response, error)

// stubCopilotTransport swaps the OAuth transport seam.
func stubCopilotTransport(t *testing.T, handler copilotTestHandler) {
	t.Helper()
	previous := githubCopilotOAuthDo
	githubCopilotOAuthDo = func(ctx context.Context, method, requestURL string, headers map[string]string, body string) (*http.Response, error) {
		return handler(method, requestURL, body)
	}
	t.Cleanup(func() { githubCopilotOAuthDo = previous })
}

// copilotTestLoginInteraction drives the prompts/notifications of a login
// flow (pi: loginGitHubCopilotForTest).
type copilotTestLoginInteraction struct {
	onDeviceCode func(event AuthEvent)
	onProgress   func(message string)
	promptResult string
	promptErr    error
}

func (i *copilotTestLoginInteraction) Prompt(prompt AuthPrompt) (string, error) {
	if prompt.Type != "text" {
		return "", fmt.Errorf("unexpected prompt: %s", prompt.Type)
	}
	return i.promptResult, i.promptErr
}

func (i *copilotTestLoginInteraction) Notify(event AuthEvent) {
	if event.Type == "device_code" && i.onDeviceCode != nil {
		i.onDeviceCode(event)
	}
	if event.Type == "progress" && i.onProgress != nil {
		i.onProgress(event.Message)
	}
}

// stubCopilotLoginFetch installs the standard login-fetch stub covering the
// device flow endpoints, the copilot token endpoint, the models catalog and
// the policy endpoint (pi: stubGitHubCopilotLoginFetch).
func stubCopilotLoginFetch(t *testing.T, models func() string, policy func(modelID string) (*http.Response, error)) {
	t.Helper()
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		switch {
		case strings.HasSuffix(requestURL, "/login/device/code"):
			return copilotTestJSONResponse(200, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
		case strings.HasSuffix(requestURL, "/login/oauth/access_token"):
			return copilotTestJSONResponse(200, `{"access_token":"ghu_refresh_token"}`), nil
		case strings.Contains(requestURL, "/copilot_internal/v2/token"):
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, testCopilotAccessToken)), nil
		case requestURL == testCopilotModelsURL:
			if models == nil {
				return nil, fmt.Errorf("unexpected models request: %s", requestURL)
			}
			return copilotTestJSONResponse(200, models()), nil
		case strings.HasPrefix(requestURL, testCopilotModelsURL+"/") && strings.HasSuffix(requestURL, "/policy"):
			if policy == nil {
				return nil, fmt.Errorf("unexpected policy request: %s", requestURL)
			}
			modelID := strings.TrimSuffix(strings.TrimPrefix(requestURL, testCopilotModelsURL+"/"), "/policy")
			return policy(modelID)
		default:
			return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
		}
	})
}

// copilotRefreshModelsForTest drives refresh with a stubbed models response
// (pi: refreshGitHubCopilotModelsForTest).
func copilotRefreshModelsForTest(t *testing.T, data string, proxyHost string) (*OAuthCredential, error) {
	t.Helper()
	if proxyHost == "" {
		proxyHost = "proxy.individual.githubcopilot.com"
	}
	accessToken := fmt.Sprintf("tid=test;exp=9999999999;proxy-ep=%s;", proxyHost)
	modelsURL := fmt.Sprintf("https://%s/models", strings.Replace(proxyHost, "proxy.", "api.", 1))
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		if strings.Contains(requestURL, "/copilot_internal/v2/token") {
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, accessToken)), nil
		}
		if requestURL == modelsURL {
			return copilotTestJSONResponse(200, data), nil
		}
		return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
	})
	return GitHubCopilotOAuth{}.Refresh(context.Background(), &OAuthCredential{
		OAuthCredentials: OAuthCredentials{Access: "old-access-token", Refresh: "ghu_refresh_token"},
	})
}

func copilotModelEntry(id string, picker bool, policyState string, toolCalls bool) string {
	entry := fmt.Sprintf(`{"id":%q,"model_picker_enabled":%t,`, id, picker)
	if policyState != "" {
		entry += fmt.Sprintf(`"policy":{"state":%q},`, policyState)
	}
	entry += fmt.Sprintf(`"capabilities":{"supports":{"tool_calls":%t}}}`, toolCalls)
	return entry
}

func requireCopilotModelID(t *testing.T, models []string, index int) string {
	t.Helper()
	if index >= len(models) {
		t.Fatalf("expected a GitHub Copilot model at index %d", index)
	}
	return models[index]
}

func TestGitHubCopilotRefreshFiltersToPickerCatalog(t *testing.T) {
	models := installCopilotTestCatalog(t)
	pickerModelID := requireCopilotModelID(t, models, 0)
	disabledModelID := requireCopilotModelID(t, models, 1)
	hiddenModelID := requireCopilotModelID(t, models, 2)

	data := fmt.Sprintf(`{"data":[%s,%s,%s]}`,
		copilotModelEntry(pickerModelID, true, "", true),
		copilotModelEntry(disabledModelID, true, "disabled", true),
		copilotModelEntry(hiddenModelID, false, "enabled", true),
	)
	credentials, err := copilotRefreshModelsForTest(t, data, "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	available, ok := GitHubCopilotAvailableModelIDs(credentials)
	if !ok {
		t.Fatal("expected availableModelIds on the refreshed credential")
	}
	if !reflect.DeepEqual(available, []string{pickerModelID}) {
		t.Errorf("availableModelIds = %v, want [%s]", available, pickerModelID)
	}
}

func TestGitHubCopilotRefreshFallsBackToEnabledPolicies(t *testing.T) {
	models := installCopilotTestCatalog(t)
	enabledModelID := requireCopilotModelID(t, models, 0)
	data := fmt.Sprintf(`{"data":[%s,%s,%s,%s]}`,
		copilotModelEntry(enabledModelID, false, "enabled", true),
		copilotModelEntry("policy-disabled-model", false, "disabled", true),
		copilotModelEntry("unconfigured-model", false, "", true),
		copilotModelEntry("tool-incapable-model", false, "enabled", false),
	)
	credentials, err := copilotRefreshModelsForTest(t, data, "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	available, ok := GitHubCopilotAvailableModelIDs(credentials)
	if !ok {
		t.Fatal("expected availableModelIds on the refreshed credential")
	}
	if !reflect.DeepEqual(available, []string{enabledModelID}) {
		t.Errorf("availableModelIds = %v, want [%s]", available, enabledModelID)
	}
}

func TestGitHubCopilotRefreshNoPolicyFallbackForBusiness(t *testing.T) {
	data := fmt.Sprintf(`{"data":[%s]}`,
		copilotModelEntry("gpt-4.1", false, "enabled", true),
	)
	credentials, err := copilotRefreshModelsForTest(t, data, "proxy.business.githubcopilot.com")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if available, ok := GitHubCopilotAvailableModelIDs(credentials); ok && len(available) > 0 {
		t.Errorf("availableModelIds = %v, want none", available)
	}
}

func TestGitHubCopilotRefreshDoesNotRetryCatalogThrottling(t *testing.T) {
	catalogRequests := 0
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		if strings.Contains(requestURL, "/copilot_internal/v2/token") {
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, testCopilotAccessToken)), nil
		}
		if requestURL == testCopilotModelsURL {
			catalogRequests++
			response := copilotTestJSONResponse(429, `{"error":"too many requests"}`)
			response.Header.Set("Retry-After", "0") // pi: jsonResponse(..., 429, { "Retry-After": "0" })
			return response, nil
		}
		return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
	})
	_, err := GitHubCopilotOAuth{}.Refresh(context.Background(), &OAuthCredential{
		OAuthCredentials: OAuthCredentials{Access: "old-access-token", Refresh: "ghu_refresh_token"},
	})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("refresh error = %v, want 429", err)
	}
	if catalogRequests != 1 {
		t.Errorf("catalog requests = %d, want 1 (no retry on refresh)", catalogRequests)
	}
}

func TestGitHubCopilotLoginReportsDeviceCode(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		switch {
		case strings.HasSuffix(requestURL, "/login/device/code"):
			return copilotTestJSONResponse(200, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
		case strings.HasSuffix(requestURL, "/login/oauth/access_token"):
			return copilotTestJSONResponse(200, `{"access_token":"ghu_refresh_token"}`), nil
		case strings.Contains(requestURL, "/copilot_internal/v2/token"):
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, testCopilotAccessToken)), nil
		case strings.HasSuffix(requestURL, "/models"):
			return copilotTestJSONResponse(200, `{"data":[]}`), nil
		case strings.Contains(requestURL, "/models/") && strings.HasSuffix(requestURL, "/policy"):
			return copilotTestEmptyResponse(200), nil
		default:
			return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
		}
	})

	var deviceCodeEvent *AuthEvent
	interaction := &copilotTestLoginInteraction{
		onDeviceCode: func(event AuthEvent) { captured := event; deviceCodeEvent = &captured },
	}
	done := make(chan struct{})
	var credential *OAuthCredential
	var loginErr error
	go func() {
		credential, loginErr = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()
	// The waitBeforeFirstPoll sleep registers only after the device-code
	// notification was delivered, so receiving here proves onDeviceCode ran.
	<-sleepCalls
	if deviceCodeEvent == nil {
		t.Fatal("onDeviceCode not called")
	}
	if deviceCodeEvent.UserCode != "ABCD-EFGH" ||
		deviceCodeEvent.VerificationURI != "https://github.com/login/device" ||
		deviceCodeEvent.IntervalSeconds == nil || *deviceCodeEvent.IntervalSeconds != 1 ||
		deviceCodeEvent.ExpiresInSeconds == nil || *deviceCodeEvent.ExpiresInSeconds != 900 {
		t.Errorf("device_code event = %+v", deviceCodeEvent)
	}
	clock.advance(time.Second)
	<-done
	if loginErr != nil {
		t.Fatalf("login: %v", loginErr)
	}
	if credential == nil || credential.Access != testCopilotAccessToken {
		t.Errorf("credential = %+v", credential)
	}
}

func TestGitHubCopilotLoginEnablesUnconfiguredKnownModels(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	models := installCopilotTestCatalog(t)
	configuredModelID := requireCopilotModelID(t, models, 0)
	unconfiguredModelID := requireCopilotModelID(t, models, 1)
	catalogRequests := 0
	var policyModelIDs []string
	stubCopilotLoginFetch(t, func() string {
		catalogRequests++
		return fmt.Sprintf(`{"data":[%s,%s,%s,%s]}`,
			copilotModelEntry(configuredModelID, true, "enabled", true),
			copilotModelEntry(unconfiguredModelID, true, "unconfigured", true),
			copilotModelEntry("remote-only-model", true, "unconfigured", true),
			copilotModelEntry("tool-incapable-model", true, "unconfigured", false),
		)
	}, func(modelID string) (*http.Response, error) {
		policyModelIDs = append(policyModelIDs, modelID)
		return copilotTestEmptyResponse(200), nil
	})

	interaction := &copilotTestLoginInteraction{}
	done := make(chan struct{})
	var loginErr error
	go func() {
		_, loginErr = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()
	awaitCopilotSleep(t, sleepCalls) // waitBeforeFirstPoll registered
	clock.advance(time.Second)
	<-done
	if loginErr != nil {
		t.Fatalf("login: %v", loginErr)
	}
	if catalogRequests != 1 {
		t.Errorf("catalog requests = %d, want 1", catalogRequests)
	}
	if !reflect.DeepEqual(policyModelIDs, []string{unconfiguredModelID}) {
		t.Errorf("policy updates = %v, want [%s]", policyModelIDs, unconfiguredModelID)
	}
}

func TestGitHubCopilotLoginRetriesThrottledPolicyAfterRetryAfter(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	models := installCopilotTestCatalog(t)
	modelID := requireCopilotModelID(t, models, 0)
	policyRequests := 0
	stubCopilotLoginFetch(t, func() string {
		return fmt.Sprintf(`{"data":[%s]}`, copilotModelEntry(modelID, true, "unconfigured", true))
	}, func(string) (*http.Response, error) {
		policyRequests++
		if policyRequests == 1 {
			// pi: jsonResponse(..., 429, { "Retry-After": "1" }). The header is
			// load-bearing: without it the default 500ms backoff fires inside
			// the 999ms advance below, releasing the login goroutine while the
			// test reads policyRequests unsynchronized (2026-09-24 CI data
			// race; with the 1000ms timer the goroutine stays parked in
			// deviceCodeSleep and the sleepCalls send orders every read).
			response := copilotTestJSONResponse(429, `{"error":"too many requests"}`)
			response.Header.Set("Retry-After", "1")
			return response, nil
		}
		return copilotTestEmptyResponse(200), nil
	})

	interaction := &copilotTestLoginInteraction{}
	done := make(chan struct{})
	go func() {
		_, _ = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()
	awaitCopilotSleep(t, sleepCalls) // waitBeforeFirstPoll registered
	clock.advance(time.Second)
	awaitCopilotSleep(t, sleepCalls) // retry sleep registered => first policy request happened
	if policyRequests != 1 {
		t.Fatalf("policy requests = %d, want 1", policyRequests)
	}
	clock.advance(999 * time.Millisecond)
	if policyRequests != 1 {
		t.Fatalf("policy requests after 999ms = %d, want 1", policyRequests)
	}
	clock.advance(time.Millisecond)
	<-done
	if policyRequests != 2 {
		t.Errorf("policy requests = %d, want 2", policyRequests)
	}
}

func TestGitHubCopilotLoginContinuesPolicyAfterTransportFailure(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	models := installCopilotTestCatalog(t)
	firstModelID := requireCopilotModelID(t, models, 0)
	secondModelID := requireCopilotModelID(t, models, 1)
	var policyModelIDs []string
	stubCopilotLoginFetch(t, func() string {
		return fmt.Sprintf(`{"data":[%s,%s]}`,
			copilotModelEntry(firstModelID, true, "unconfigured", true),
			copilotModelEntry(secondModelID, true, "unconfigured", true),
		)
	}, func(modelID string) (*http.Response, error) {
		policyModelIDs = append(policyModelIDs, modelID)
		if len(policyModelIDs) == 1 {
			return nil, fmt.Errorf("fetch failed")
		}
		return copilotTestEmptyResponse(200), nil
	})

	interaction := &copilotTestLoginInteraction{}
	done := make(chan struct{})
	var loginErr error
	go func() {
		_, loginErr = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()
	awaitCopilotSleep(t, sleepCalls) // waitBeforeFirstPoll registered
	clock.advance(time.Second)
	<-done
	if loginErr != nil {
		t.Fatalf("login: %v", loginErr)
	}
	if !reflect.DeepEqual(policyModelIDs, []string{firstModelID, secondModelID}) {
		t.Errorf("policy updates = %v, want [%s %s]", policyModelIDs, firstModelID, secondModelID)
	}
}

func TestGitHubCopilotLoginRejectsUntrustedVerificationURI(t *testing.T) {
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		if strings.HasSuffix(requestURL, "/login/device/code") {
			return copilotTestJSONResponse(200, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"$(id>/tmp/pwned)","interval":1,"expires_in":900}`), nil
		}
		return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
	})
	called := false
	interaction := &copilotTestLoginInteraction{onDeviceCode: func(AuthEvent) { called = true }}
	_, err := GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
	if err == nil || !strings.Contains(err.Error(), "Untrusted verification_uri") {
		t.Fatalf("login error = %v, want Untrusted verification_uri", err)
	}
	if called {
		t.Error("onDeviceCode should not have been called")
	}
}

func TestGitHubCopilotLoginNormalizesVerificationURI(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	rawVerificationURI := "https://github.com/login/\x1b]8;;evil"
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		switch {
		case strings.HasSuffix(requestURL, "/login/device/code"):
			// json.Marshal (not %q): Go's %q escapes ESC as \x1b, which is not
			// a valid JSON escape; encoding/json emits \u001b.
			uriJSON, _ := json.Marshal(rawVerificationURI)
			payload := fmt.Sprintf(`{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":%s,"interval":1,"expires_in":900}`, uriJSON)
			return copilotTestJSONResponse(200, payload), nil
		case strings.HasSuffix(requestURL, "/login/oauth/access_token"):
			return copilotTestJSONResponse(200, `{"access_token":"ghu_refresh_token"}`), nil
		case strings.Contains(requestURL, "/copilot_internal/v2/token"):
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, testCopilotAccessToken)), nil
		case strings.HasSuffix(requestURL, "/models"):
			return copilotTestJSONResponse(200, `{"data":[]}`), nil
		case strings.Contains(requestURL, "/models/") && strings.HasSuffix(requestURL, "/policy"):
			return copilotTestEmptyResponse(200), nil
		default:
			return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
		}
	})

	var deviceCodeEvent *AuthEvent
	interaction := &copilotTestLoginInteraction{
		onDeviceCode: func(event AuthEvent) { captured := event; deviceCodeEvent = &captured },
	}
	done := make(chan struct{})
	go func() {
		_, _ = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()
	<-sleepCalls
	if deviceCodeEvent == nil {
		t.Fatal("onDeviceCode not called")
	}
	// url.Parse keeps the raw path; pi's new URL(...).href percent-encodes
	// the control characters. pig's url.Parse.String() roundtrip also
	// escapes the ESC byte.
	if deviceCodeEvent.VerificationURI == rawVerificationURI {
		t.Error("verification_uri was not normalized")
	}
	clock.advance(time.Second)
	<-done
}

func TestGitHubCopilotLoginPollTimingAndSlowDown(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	var accessTokenPollTimes []time.Time
	responses := []string{
		`{"error":"authorization_pending","error_description":"pending"}`,
		`{"error":"slow_down","error_description":"slow down","interval":7}`,
		`{"access_token":"ghu_refresh_token"}`,
	}
	responseIdx := 0
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		switch {
		case strings.HasSuffix(requestURL, "/login/device/code"):
			if method != http.MethodPost {
				t.Errorf("device code method = %s, want POST", method)
			}
			if !strings.Contains(body, "client_id=") || !strings.Contains(body, "scope=read%3Auser") {
				t.Errorf("device code body = %s", body)
			}
			return copilotTestJSONResponse(200, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":5,"expires_in":900}`), nil
		case strings.HasSuffix(requestURL, "/login/oauth/access_token"):
			accessTokenPollTimes = append(accessTokenPollTimes, clock.nowFunc())
			if !strings.Contains(body, "client_id=") ||
				!strings.Contains(body, "device_code=device-code") ||
				!strings.Contains(body, "grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code") {
				t.Errorf("access token body = %s", body)
			}
			if responseIdx >= len(responses) {
				return nil, fmt.Errorf("unexpected extra access token poll")
			}
			response := responses[responseIdx]
			responseIdx++
			return copilotTestJSONResponse(200, response), nil
		case strings.Contains(requestURL, "/copilot_internal/v2/token"):
			return copilotTestJSONResponse(200, fmt.Sprintf(`{"token":%q,"expires_at":9999999999}`, testCopilotAccessToken)), nil
		case strings.HasSuffix(requestURL, "/models"):
			return copilotTestJSONResponse(200, `{"data":[]}`), nil
		case strings.Contains(requestURL, "/models/") && strings.HasSuffix(requestURL, "/policy"):
			return copilotTestEmptyResponse(200), nil
		default:
			return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
		}
	})

	start := clock.nowFunc()
	interaction := &copilotTestLoginInteraction{}
	done := make(chan struct{})
	go func() {
		_, _ = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()

	// Each awaitCopilotSleep-before-advance pair makes the fake-timer
	// timeline deterministic: the sleep registered, then the advance fires
	// it, then the next registration proves the poll happened.
	awaitCopilotSleep(t, sleepCalls) // waitBeforeFirstPoll (5000ms)
	clock.advance(4999 * time.Millisecond)
	if len(accessTokenPollTimes) != 0 {
		t.Fatalf("polls after 4999ms = %d, want 0", len(accessTokenPollTimes))
	}
	clock.advance(time.Millisecond)  // poll #1: pending
	awaitCopilotSleep(t, sleepCalls) // second sleep (5000ms) registered
	if len(accessTokenPollTimes) != 1 {
		t.Fatalf("polls after 5000ms = %d, want 1", len(accessTokenPollTimes))
	}
	clock.advance(4999 * time.Millisecond)
	if len(accessTokenPollTimes) != 1 {
		t.Fatalf("polls after 9999ms = %d, want 1", len(accessTokenPollTimes))
	}
	clock.advance(time.Millisecond)  // poll #2: slow_down with interval 7
	awaitCopilotSleep(t, sleepCalls) // third sleep (7000ms) registered
	if len(accessTokenPollTimes) != 2 {
		t.Fatalf("polls after 10000ms = %d, want 2", len(accessTokenPollTimes))
	}
	// slow_down carried a server-provided interval of 7 seconds.
	clock.advance(6999 * time.Millisecond)
	if len(accessTokenPollTimes) != 2 {
		t.Fatalf("polls after 16999ms = %d, want 2", len(accessTokenPollTimes))
	}
	clock.advance(time.Millisecond) // poll #3: complete
	<-done

	want := []time.Time{
		start.Add(5000 * time.Millisecond),
		start.Add(10000 * time.Millisecond),
		start.Add(17000 * time.Millisecond),
	}
	if !reflect.DeepEqual(accessTokenPollTimes, want) {
		t.Errorf("poll times = %v, want %v", accessTokenPollTimes, want)
	}
}

func TestGitHubCopilotLoginTimesOutAfterRepeatedSlowDown(t *testing.T) {
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	var accessTokenPollTimes []time.Time
	responses := []string{
		`{"error":"slow_down","error_description":"slow down"}`,
		`{"error":"slow_down","error_description":"still too fast"}`,
		`{"error":"authorization_pending","error_description":"pending"}`,
	}
	responseIdx := 0
	stubCopilotTransport(t, func(method, requestURL, body string) (*http.Response, error) {
		switch {
		case strings.HasSuffix(requestURL, "/login/device/code"):
			return copilotTestJSONResponse(200, `{"device_code":"device-code","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":5,"expires_in":25}`), nil
		case strings.HasSuffix(requestURL, "/login/oauth/access_token"):
			accessTokenPollTimes = append(accessTokenPollTimes, clock.nowFunc())
			if responseIdx >= len(responses) {
				return nil, fmt.Errorf("unexpected extra access token poll")
			}
			response := responses[responseIdx]
			responseIdx++
			return copilotTestJSONResponse(200, response), nil
		default:
			return nil, fmt.Errorf("unexpected fetch URL: %s", requestURL)
		}
	})

	start := clock.nowFunc()
	interaction := &copilotTestLoginInteraction{}
	done := make(chan struct{})
	var loginErr error
	go func() {
		_, loginErr = GitHubCopilotOAuth{}.Login(context.Background(), interaction, nil)
		close(done)
	}()

	awaitCopilotSleep(t, sleepCalls) // waitBeforeFirstPoll (5000ms)
	clock.advance(4999 * time.Millisecond)
	if len(accessTokenPollTimes) != 0 {
		t.Fatalf("polls = %v, want none yet", accessTokenPollTimes)
	}
	clock.advance(time.Millisecond) // poll @5000: slow_down -> interval 10000
	awaitCopilotSleep(t, sleepCalls)
	if len(accessTokenPollTimes) != 1 {
		t.Fatalf("polls = %v, want one at start+5000", accessTokenPollTimes)
	}
	clock.advance(9999 * time.Millisecond)
	if len(accessTokenPollTimes) != 1 {
		t.Fatalf("polls = %v, want one at start+5000", accessTokenPollTimes)
	}
	clock.advance(time.Millisecond) // poll @15000: slow_down -> interval 15000
	awaitCopilotSleep(t, sleepCalls)
	if len(accessTokenPollTimes) != 2 {
		t.Fatalf("polls = %v, want two", accessTokenPollTimes)
	}
	clock.advance(9999 * time.Millisecond)
	if len(accessTokenPollTimes) != 2 {
		t.Fatalf("polls = %v, want two", accessTokenPollTimes)
	}
	clock.advance(time.Millisecond) // sleep fires past the deadline -> timeout
	<-done
	if loginErr == nil || !strings.Contains(loginErr.Error(), "Device flow timed out after one or more slow_down responses") {
		t.Fatalf("login error = %v, want slow_down timeout", loginErr)
	}
	want := []time.Time{
		start.Add(5000 * time.Millisecond),
		start.Add(15000 * time.Millisecond),
	}
	if !reflect.DeepEqual(accessTokenPollTimes, want) {
		t.Errorf("poll times = %v, want %v", accessTokenPollTimes, want)
	}
}

func TestGitHubCopilotToAuthDerivesBaseURLFromToken(t *testing.T) {
	credential := &OAuthCredential{OAuthCredentials: OAuthCredentials{
		Access:  testCopilotAccessToken,
		Refresh: "ghu_refresh_token",
	}}
	modelAuth, err := GitHubCopilotOAuth{}.ToAuth(credential)
	if err != nil {
		t.Fatalf("toAuth: %v", err)
	}
	if modelAuth.APIKey != testCopilotAccessToken {
		t.Errorf("apiKey = %s", modelAuth.APIKey)
	}
	if modelAuth.BaseURL != "https://api.individual.githubcopilot.com" {
		t.Errorf("baseUrl = %s", modelAuth.BaseURL)
	}
}

// awaitCopilotSleep waits for the poll loop to register its next sleep, with
// a bounded deadline so a stalled flow fails the test instead of hanging it.
func awaitCopilotSleep(t *testing.T, sleepCalls chan int64) int64 {
	t.Helper()
	select {
	case ms := <-sleepCalls:
		return ms
	case <-time.After(5 * time.Second):
		t.Fatal("device poll loop did not register a sleep within 5s")
		return 0
	}
}

// stubCopilotOAuthClock wires the fake clock into the poll-loop seams (pi:
// vi.useFakeTimers; see oauth_openai_codex_test.go).
func stubCopilotOAuthClock(t *testing.T, clock *oauthFakeClock, sleepCalls chan int64) {
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

// installCopilotTestCatalog stubs the known-models hook with a synthetic
// three-model catalog. pi's tests index into the real provider catalog
// (githubCopilotProvider().getModels()), which package auth cannot import
// (layering); the real-catalog integration is covered in
// ai/providers/github_copilot_test.go.
func installCopilotTestCatalog(t *testing.T) []string {
	t.Helper()
	ids := []string{"copilot-model-0", "copilot-model-1", "copilot-model-2"}
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	previous := githubCopilotKnownModelIDs
	githubCopilotKnownModelIDs = func() map[string]bool { return set }
	t.Cleanup(func() { githubCopilotKnownModelIDs = previous })
	return ids
}

func TestGitHubCopilotLoginStopsPolicyWhenBudgetExceeded(t *testing.T) {
	// pi: "stops policy updates and persists authentication when the retry
	// delay exceeds the login budget" — a Retry-After longer than the 5s
	// budget returns the 429 immediately, the batch breaks, and login still
	// succeeds. (The Models-persistence half is covered by the generic
	// credential-sync tests; see ai/providers/github_copilot_test.go.)
	clock := &oauthFakeClock{now: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	sleepCalls := make(chan int64, 64)
	stubCopilotOAuthClock(t, clock, sleepCalls)

	ids := installCopilotTestCatalog(t)
	var policyModelIDs []string
	stubCopilotLoginFetch(t, func() string {
		return fmt.Sprintf(`{"data":[%s,%s]}`,
			copilotModelEntry(ids[0], true, "unconfigured", true),
			copilotModelEntry(ids[1], true, "unconfigured", true),
		)
	}, func(modelID string) (*http.Response, error) {
		policyModelIDs = append(policyModelIDs, modelID)
		return copilotTestJSONResponse(429, `{"error":"too many requests"}`), nil
	})
	// Override the policy stub's Retry-After header: stubCopilotLoginFetch
	// builds responses without headers, so re-stub the transport for the
	// policy endpoint with a 5s Retry-After.
	previous := githubCopilotOAuthDo
	githubCopilotOAuthDo = func(ctx context.Context, method, requestURL string, headers map[string]string, body string) (*http.Response, error) {
		response, err := previous(ctx, method, requestURL, headers, body)
		if err == nil && response.StatusCode == http.StatusTooManyRequests {
			response.Header.Set("Retry-After", "5")
		}
		return response, err
	}
	t.Cleanup(func() { githubCopilotOAuthDo = previous })

	done := make(chan struct{})
	var credential *OAuthCredential
	var loginErr error
	go func() {
		credential, loginErr = GitHubCopilotOAuth{}.Login(context.Background(), &copilotTestLoginInteraction{}, nil)
		close(done)
	}()
	<-sleepCalls // waitBeforeFirstPoll registered
	clock.advance(time.Second)
	<-done
	if loginErr != nil {
		t.Fatalf("login: %v", loginErr)
	}
	if !reflect.DeepEqual(policyModelIDs, []string{ids[0]}) {
		t.Errorf("policy updates = %v, want [%s] (batch stopped)", policyModelIDs, ids[0])
	}
	available, ok := GitHubCopilotAvailableModelIDs(credential)
	if !ok || !reflect.DeepEqual(available, []string{ids[0], ids[1]}) {
		t.Errorf("availableModelIds = %v (ok=%v), want [%s %s]", available, ok, ids[0], ids[1])
	}
	if credential.Access != testCopilotAccessToken {
		t.Errorf("access = %s", credential.Access)
	}
}
