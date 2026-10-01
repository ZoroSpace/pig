// pi: auth/oauth/github-copilot.ts — GitHub Copilot OAuth device flow.
//
// Segment deviations (declared):
//   - pi's static import of the generated GITHUB_COPILOT_MODELS catalog
//     becomes the githubCopilotKnownModelIDs hook: the catalog lives in
//     package ai, which package auth cannot import (layering);
//     ai/providers/github_copilot.go injects the lookup at init.
//   - Extra token fields (availableModelIds, enterpriseUrl) ride OAuth
//     credential Extra (pi: open index signature on OAuthCredentials).
//   - pi's fetch calls go through the githubCopilotOAuthDo transport seam
//     (pi: vi.stubGlobal("fetch") in tests); Date.now() shares the
//     deviceCodeNow fake-clock seam.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// githubCopilotClientID is pi's CLIENT_ID (base64-obfuscated there too:
	// decode("SXYxLmI1MDdhMDhjODdlY2ZlOTg=")).
	githubCopilotClientID = "SXYxLmI1MDdhMDhjODdlY2ZlOTg="

	githubCopilotAPIVersion = "2026-06-01"

	githubCopilotDefaultDomain = "github.com"
	// githubCopilotIndividualAPIBaseURL is the Individual-account endpoint;
	// the policy fallback is limited to it (pi: allowPolicyFallback).
	githubCopilotIndividualAPIBaseURL = "https://api.individual.githubcopilot.com"

	githubCopilotRequestTimeout = 5 * time.Second
)

// githubCopilotHeaders are the static Copilot request headers (pi:
// COPILOT_HEADERS).
func githubCopilotHeaders() map[string]string {
	return map[string]string{
		"User-Agent":             "GitHubCopilotChat/0.35.0",
		"Editor-Version":         "vscode/1.107.0",
		"Editor-Plugin-Version":  "copilot-chat/0.35.0",
		"Copilot-Integration-Id": "vscode-chat",
	}
}

// githubCopilotDeviceCodeResponse is the device authorization response (pi:
// DeviceCodeResponse).
type githubCopilotDeviceCodeResponse struct {
	deviceCode       string
	userCode         string
	verificationURI  string
	intervalSeconds  *int64
	expiresInSeconds int64
}

// githubCopilotModelCatalog is the parsed /models response (pi: the return of
// parseGitHubCopilotModelCatalog).
type githubCopilotModelCatalog struct {
	availableModelIDs []string
	policyModelIDs    []string
}

// githubCopilotKnownModelIDs is the catalog-membership lookup behind the
// policy-model filter (pi: Object.hasOwn(GITHUB_COPILOT_MODELS, model.id)).
// ai/providers injects the generated catalog at init.
var githubCopilotKnownModelIDs = func() map[string]bool { return nil }

// SetGitHubCopilotKnownModelIDs installs the catalog-membership lookup used
// by the policy-model filter during login (see the file header for why this
// is a hook rather than a static reference).
func SetGitHubCopilotKnownModelIDs(fn func() map[string]bool) {
	githubCopilotKnownModelIDs = fn
}

// GitHubCopilotAvailableModelIDs reads the account's available model ids from
// an OAuth credential (pi: credential.availableModelIds). ok is false when
// the field is absent or malformed — both mean "do not filter".
func GitHubCopilotAvailableModelIDs(credential *OAuthCredential) (ids []string, ok bool) {
	if credential == nil {
		return nil, false
	}
	raw, present := credential.Extra["availableModelIds"]
	if !present {
		return nil, false
	}
	list, isList := raw.([]any)
	if !isList {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, isString := item.(string)
		if !isString {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func gitHubCopilotSetAvailableModelIDs(credential *OAuthCredential, ids []string) {
	if credential.Extra == nil {
		credential.Extra = map[string]any{}
	}
	anyIDs := make([]any, len(ids))
	for i, id := range ids {
		anyIDs[i] = id
	}
	credential.Extra["availableModelIds"] = anyIDs
}

func gitHubCopilotEnterpriseURL(credential *OAuthCredential) string {
	if credential == nil {
		return ""
	}
	s, _ := credential.Extra["enterpriseUrl"].(string)
	return s
}

func gitHubCopilotSetEnterpriseURL(credential *OAuthCredential, enterpriseURL string) {
	if credential.Extra == nil {
		credential.Extra = map[string]any{}
	}
	credential.Extra["enterpriseUrl"] = enterpriseURL
}

// githubCopilotNormalizeDomain parses a GitHub Enterprise URL/domain into a
// hostname (pi: normalizeDomain); ok is false for blank or invalid input.
func githubCopilotNormalizeDomain(input string) (domain string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	return parsed.Hostname(), true
}

// githubCopilotGetUrls is pi's getUrls.
func githubCopilotGetUrls(domain string) (deviceCodeURL, accessTokenURL, copilotTokenURL string) {
	return "https://" + domain + "/login/device/code",
		"https://" + domain + "/login/oauth/access_token",
		"https://api." + domain + "/copilot_internal/v2/token"
}

var githubCopilotProxyEPRe = regexp.MustCompile(`proxy-ep=([^;]+)`)

// githubCopilotBaseURLFromToken parses the proxy-ep from a Copilot token and
// converts it to the API base URL (pi: getBaseUrlFromToken). Token format:
// tid=...;exp=...;proxy-ep=proxy.individual.githubcopilot.com;...
func githubCopilotBaseURLFromToken(token string) string {
	match := githubCopilotProxyEPRe.FindStringSubmatch(token)
	if match == nil {
		return ""
	}
	apiHost := githubCopilotProxyHostRe.ReplaceAllString(match[1], "api.")
	return "https://" + apiHost
}

var githubCopilotProxyHostRe = regexp.MustCompile(`^proxy\.`)

// githubCopilotBaseURL resolves the credential-specific proxy endpoint (pi:
// getGitHubCopilotBaseUrl).
func githubCopilotBaseURL(accessToken, enterpriseDomain string) string {
	if accessToken != "" {
		if urlFromToken := githubCopilotBaseURLFromToken(accessToken); urlFromToken != "" {
			return urlFromToken
		}
	}
	if enterpriseDomain != "" {
		return "https://copilot-api." + enterpriseDomain
	}
	return githubCopilotIndividualAPIBaseURL
}

// githubCopilotParseModelCatalog parses the account /models response into the
// available and policy model id sets (pi: parseGitHubCopilotModelCatalog).
func githubCopilotParseModelCatalog(raw any, allowPolicyFallback bool) (githubCopilotModelCatalog, error) {
	record, _ := raw.(map[string]any)
	data, _ := record["data"].([]any)
	if data == nil {
		return githubCopilotModelCatalog{}, fmt.Errorf("Invalid Copilot models response")
	}

	type accountModel struct {
		id          string
		picker      bool
		policyState string
	}
	var accountModels []accountModel
	for _, rawItem := range data {
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		picker := item["model_picker_enabled"] == true
		policyState := ""
		if policy, isMap := item["policy"].(map[string]any); isMap {
			policyState, _ = policy["state"].(string)
		}
		capabilities, _ := item["capabilities"].(map[string]any)
		supports, _ := capabilities["supports"].(map[string]any)
		if supports != nil && supports["tool_calls"] == false {
			continue
		}
		accountModels = append(accountModels, accountModel{id: id, picker: picker, policyState: policyState})
	}

	var pickerModelIDs []string
	for _, model := range accountModels {
		if model.picker && model.policyState != "disabled" {
			pickerModelIDs = append(pickerModelIDs, model.id)
		}
	}
	usePolicyFallback := allowPolicyFallback && len(pickerModelIDs) == 0
	var availableModelIDs []string
	if len(pickerModelIDs) > 0 || !allowPolicyFallback {
		availableModelIDs = pickerModelIDs
	} else {
		for _, model := range accountModels {
			if model.policyState == "enabled" {
				availableModelIDs = append(availableModelIDs, model.id)
			}
		}
	}
	known := githubCopilotKnownModelIDs()
	var policyModelIDs []string
	for _, model := range accountModels {
		if model.policyState != "unconfigured" || !known[model.id] {
			continue
		}
		if model.picker || usePolicyFallback {
			policyModelIDs = append(policyModelIDs, model.id)
		}
	}
	return githubCopilotModelCatalog{availableModelIDs: availableModelIDs, policyModelIDs: policyModelIDs}, nil
}

// githubCopilotRetryPolicy bounds one rate-limit retry wrapper (pi: the
// { maxRetries, maxElapsedMs } literal).
type githubCopilotRetryPolicy struct {
	maxRetries   int
	maxElapsedMs int64
}

// githubCopilotFetchWithRateLimitRetry performs one request with 429 retry
// handling (pi: fetchWithRateLimitRetry): exponential backoff, Retry-After
// in seconds or HTTP-date, and a wall-clock retry budget.
func githubCopilotFetchWithRateLimitRetry(ctx context.Context, requestURL string, headers map[string]string, body string, retryPolicy githubCopilotRetryPolicy) (*http.Response, error) {
	retryDeadline := time.Time{}
	if retryPolicy.maxRetries > 0 && retryPolicy.maxElapsedMs > 0 {
		retryDeadline = deviceCodeNow().Add(time.Duration(retryPolicy.maxElapsedMs) * time.Millisecond)
	}
	for retry := 0; ; retry++ {
		// pi caps every attempt at 5s (AbortSignal.timeout(5000) under
		// AbortSignal.any) and additionally bounds the whole loop by the
		// retry budget.
		attemptDeadline := githubCopilotRequestTimeout
		if !retryDeadline.IsZero() {
			// deviceCodeNow (not time.Now) keeps the math on the fake-clock
			// seam under tests; both agree in production.
			if remaining := retryDeadline.Sub(deviceCodeNow()); remaining < attemptDeadline {
				attemptDeadline = remaining
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptDeadline)
		response, err := githubCopilotOAuthDo(attemptCtx, http.MethodGet, requestURL, headers, body)
		cancel()
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusTooManyRequests || retry == retryPolicy.maxRetries {
			return response, nil
		}

		var delayMs int64 = 500 * (1 << retry)
		if retryAfter := response.Header.Get("retry-after"); retryAfter != "" {
			if seconds, parseErr := strconv.ParseFloat(retryAfter, 64); parseErr == nil {
				if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
					return response, nil
				}
				delayMs = int64(seconds * 1000)
			} else if parsed, dateErr := http.ParseTime(retryAfter); dateErr == nil {
				delayMs = time.Until(parsed).Milliseconds()
			} else {
				return response, nil
			}
		}
		if delayMs < 0 {
			delayMs = 0
		}
		if !retryDeadline.IsZero() && delayMs >= retryDeadline.Sub(deviceCodeNow()).Milliseconds() {
			return response, nil
		}
		_ = response.Body.Close()
		if err := deviceCodeSleep(ctx, delayMs); err != nil {
			return nil, err
		}
	}
}

// githubCopilotFetchModels fetches the account model catalog (pi:
// fetchGitHubCopilotModels). The policy fallback is limited to the Individual
// endpoint so other account types keep strict picker semantics.
func githubCopilotFetchModels(ctx context.Context, copilotToken, enterpriseDomain string, retryPolicy githubCopilotRetryPolicy) (githubCopilotModelCatalog, error) {
	baseURL := githubCopilotBaseURL(copilotToken, enterpriseDomain)
	allowPolicyFallback := baseURL == githubCopilotIndividualAPIBaseURL
	headers := map[string]string{
		"Accept":               "application/json",
		"Authorization":        "Bearer " + copilotToken,
		"X-GitHub-Api-Version": githubCopilotAPIVersion,
	}
	for k, v := range githubCopilotHeaders() {
		headers[k] = v
	}
	response, err := githubCopilotFetchWithRateLimitRetry(ctx, baseURL+"/models", headers, "", retryPolicy)
	if err != nil {
		return githubCopilotModelCatalog{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		// pi: `${response.status} ${response.statusText}: ${text}`; Go's
		// Status field already spells "429 Too Many Requests".
		return githubCopilotModelCatalog{}, fmt.Errorf("%s: %s", response.Status, string(body))
	}
	var parsed any
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return githubCopilotModelCatalog{}, err
	}
	return githubCopilotParseModelCatalog(parsed, allowPolicyFallback)
}

// githubCopilotFetchJSON performs one JSON request (pi: fetchJson); non-2xx
// becomes an error carrying the status and body text.
func githubCopilotFetchJSON(ctx context.Context, method, requestURL string, headers map[string]string, body string) (any, error) {
	response, err := githubCopilotOAuthDo(ctx, method, requestURL, headers, body)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", response.Status, string(raw))
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// githubCopilotStartDeviceFlow starts the GitHub device flow (pi:
// startDeviceFlow); the verification URI is scheme-checked so a malicious
// enterprise server cannot reach the OS opener.
func githubCopilotStartDeviceFlow(ctx context.Context, domain string) (*githubCopilotDeviceCodeResponse, error) {
	deviceCodeURL, _, _ := githubCopilotGetUrls(domain)
	headers := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/x-www-form-urlencoded",
		"User-Agent":   "GitHubCopilotChat/0.35.0",
	}
	body := url.Values{"client_id": {mustDecodeBase64(githubCopilotClientID)}, "scope": {"read:user"}}.Encode()
	data, err := githubCopilotFetchJSON(ctx, http.MethodPost, deviceCodeURL, headers, body)
	if err != nil {
		return nil, err
	}
	record, _ := data.(map[string]any)
	if record == nil {
		return nil, fmt.Errorf("Invalid device code response")
	}
	deviceCode, _ := record["device_code"].(string)
	userCode, _ := record["user_code"].(string)
	verificationURI, _ := record["verification_uri"].(string)
	expiresIn, expiresOK := numberFromAny(record["expires_in"])
	var intervalSeconds *int64
	if rawInterval, present := record["interval"]; present {
		// pi: an interval present but not a number rejects the response.
		interval, isNumber := numberFromAny(rawInterval)
		if !isNumber {
			return nil, fmt.Errorf("Invalid device code response fields")
		}
		seconds := int64(interval)
		intervalSeconds = &seconds
	}
	if deviceCode == "" || userCode == "" || verificationURI == "" || !expiresOK {
		return nil, fmt.Errorf("Invalid device code response fields")
	}

	// The verification URI is opened in the user's browser and to prevent
	// `open` from opening an executable or similar, we force it to be a URL.
	// pi's URL parser percent-encodes control characters and normalizes the
	// URI; Go's url.Parse rejects them outright, so escape them first to keep
	// the normalization semantics (github-copilot-oauth
	// "normalizes verification_uri" case).
	parsedURI, err := url.Parse(escapeControlBytes(verificationURI))
	if err != nil {
		return nil, fmt.Errorf("Untrusted verification_uri in device code response")
	}
	if parsedURI.Scheme != "https" && parsedURI.Scheme != "http" {
		return nil, fmt.Errorf("Untrusted verification_uri in device code response")
	}

	return &githubCopilotDeviceCodeResponse{
		deviceCode:       deviceCode,
		userCode:         userCode,
		verificationURI:  parsedURI.String(),
		intervalSeconds:  intervalSeconds,
		expiresInSeconds: int64(expiresIn),
	}, nil
}

// githubCopilotPollForAccessToken polls the OAuth access-token endpoint until
// the user completes authorization (pi: pollForGitHubAccessToken).
func githubCopilotPollForAccessToken(ctx context.Context, domain string, device *githubCopilotDeviceCodeResponse) (string, error) {
	_, accessTokenURL, _ := githubCopilotGetUrls(domain)
	var intervalSeconds int64
	if device.intervalSeconds != nil {
		intervalSeconds = *device.intervalSeconds
	}
	headers := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/x-www-form-urlencoded",
		"User-Agent":   "GitHubCopilotChat/0.35.0",
	}
	return PollOAuthDeviceCodeFlow(DeviceCodePollOptions[string]{
		IntervalSeconds:     intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Ctx:                 ctx,
		Poll: func() DeviceCodePollResult[string] {
			body := url.Values{
				"client_id":   {mustDecodeBase64(githubCopilotClientID)},
				"device_code": {device.deviceCode},
				"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			}.Encode()
			raw, err := githubCopilotFetchJSON(ctx, http.MethodPost, accessTokenURL, headers, body)
			if err != nil {
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: err.Error()}
			}
			record, _ := raw.(map[string]any)
			if record == nil {
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: "Invalid device token response"}
			}
			if accessToken, isString := record["access_token"].(string); isString {
				return DeviceCodePollResult[string]{Status: DeviceCodeComplete, Value: accessToken}
			}
			if errString, isString := record["error"].(string); isString {
				description, _ := record["error_description"].(string)
				if errString == "authorization_pending" {
					return DeviceCodePollResult[string]{Status: DeviceCodePending}
				}
				if errString == "slow_down" {
					result := DeviceCodePollResult[string]{Status: DeviceCodeSlowDown}
					if interval, isNumber := numberFromAny(record["interval"]); isNumber {
						result.IntervalSeconds = int64(interval)
					}
					return result
				}
				message := "Device flow failed: " + errString
				if description != "" {
					message += ": " + description
				}
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: message}
			}
			return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: "Invalid device token response"}
		},
	})
}

// githubCopilotRefreshAccessToken exchanges the GitHub refresh token for a
// Copilot API token (pi: refreshGitHubCopilotAccessToken).
func githubCopilotRefreshAccessToken(ctx context.Context, refreshToken, enterpriseDomain string) (*OAuthCredential, error) {
	domain := enterpriseDomain
	if domain == "" {
		domain = githubCopilotDefaultDomain
	}
	_, _, copilotTokenURL := githubCopilotGetUrls(domain)
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + refreshToken,
	}
	for k, v := range githubCopilotHeaders() {
		headers[k] = v
	}
	raw, err := githubCopilotFetchJSON(ctx, http.MethodGet, copilotTokenURL, headers, "")
	if err != nil {
		return nil, err
	}
	record, _ := raw.(map[string]any)
	if record == nil {
		return nil, fmt.Errorf("Invalid Copilot token response")
	}
	token, _ := record["token"].(string)
	expiresAt, expiresOK := numberFromAny(record["expires_at"])
	if token == "" || !expiresOK {
		return nil, fmt.Errorf("Invalid Copilot token response fields")
	}
	credential := &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: refreshToken,
			Access:  token,
			// pi: expires_at (seconds) * 1000 - 5 * 60 * 1000.
			Expires: int64(expiresAt)*1000 - 5*60*1000,
		},
	}
	if enterpriseDomain != "" {
		gitHubCopilotSetEnterpriseURL(credential, enterpriseDomain)
	}
	return credential, nil
}

// githubCopilotRefreshToken refreshes the API token and re-reads the account
// model catalog without rate-limit retries (pi: refreshGitHubCopilotToken).
func githubCopilotRefreshToken(ctx context.Context, refreshToken, enterpriseDomain string) (*OAuthCredential, error) {
	credentials, err := githubCopilotRefreshAccessToken(ctx, refreshToken, enterpriseDomain)
	if err != nil {
		return nil, err
	}
	models, err := githubCopilotFetchModels(ctx, credentials.Access, enterpriseDomain, githubCopilotRetryPolicy{})
	if err != nil {
		return nil, err
	}
	gitHubCopilotSetAvailableModelIDs(credentials, models.availableModelIDs)
	return credentials, nil
}

// githubCopilotEnableModel enables one model for the account (pi:
// enableGitHubCopilotModel). Required for some models (like Claude, Grok)
// before they can be used. Returns false when the policy update fails.
func githubCopilotEnableModel(ctx context.Context, token, modelID, enterpriseDomain string) (bool, error) {
	baseURL := githubCopilotBaseURL(token, enterpriseDomain)
	policyURL := baseURL + "/models/" + modelID + "/policy"
	headers := map[string]string{
		"Content-Type":       "application/json",
		"Authorization":      "Bearer " + token,
		"openai-intent":      "chat-policy",
		"x-interaction-type": "chat-policy",
	}
	for k, v := range githubCopilotHeaders() {
		headers[k] = v
	}
	body := `{"state":"enabled"}`
	response, err := githubCopilotFetchWithRateLimitRetry(ctx, policyURL, headers, body, githubCopilotRetryPolicy{maxRetries: 2, maxElapsedMs: 5000})
	if err != nil {
		if ctx.Err() != nil {
			return false, err
		}
		return false, nil
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		body, _ := io.ReadAll(response.Body)
		return false, fmt.Errorf("%s: %s", response.Status, string(body))
	}
	return response.StatusCode >= 200 && response.StatusCode < 300, nil
}

// githubCopilotEnableModels enables the requested models and returns the
// successful ids (pi: enableGitHubCopilotModels). Policy updates are best
// effort; exhausted rate limiting stops the batch.
func githubCopilotEnableModels(ctx context.Context, token string, modelIDs []string, enterpriseDomain string) ([]string, error) {
	var enabledModelIDs []string
	for _, modelID := range modelIDs {
		enabled, err := githubCopilotEnableModel(ctx, token, modelID, enterpriseDomain)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			break
		}
		if enabled {
			enabledModelIDs = append(enabledModelIDs, modelID)
		}
	}
	return enabledModelIDs, nil
}

// githubCopilotLogin runs the full login flow (pi: loginGitHubCopilot).
func githubCopilotLogin(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	input, err := interaction.Prompt(AuthPrompt{
		Type:        "text",
		Message:     "GitHub Enterprise URL/domain (blank for github.com)",
		Placeholder: "company.ghe.com",
	})
	if err != nil {
		return nil, err
	}
	enterpriseDomain, valid := githubCopilotNormalizeDomain(input)
	if strings.TrimSpace(input) != "" && !valid {
		return nil, fmt.Errorf("Invalid GitHub Enterprise URL/domain")
	}
	domain := githubCopilotDefaultDomain
	if valid {
		domain = enterpriseDomain
	}

	device, err := githubCopilotStartDeviceFlow(ctx, domain)
	if err != nil {
		return nil, err
	}
	notifyDevice := AuthEvent{
		Type:             "device_code",
		UserCode:         device.userCode,
		VerificationURI:  device.verificationURI,
		ExpiresInSeconds: &device.expiresInSeconds,
	}
	if device.intervalSeconds != nil {
		notifyDevice.IntervalSeconds = device.intervalSeconds
	}
	interaction.Notify(notifyDevice)

	githubAccessToken, err := githubCopilotPollForAccessToken(ctx, domain, device)
	if err != nil {
		return nil, err
	}
	credentials, err := githubCopilotRefreshAccessToken(ctx, githubAccessToken, enterpriseDomain)
	if err != nil {
		return nil, err
	}
	models, err := githubCopilotFetchModels(ctx, credentials.Access, enterpriseDomain, githubCopilotRetryPolicy{maxRetries: 2, maxElapsedMs: 5000})
	if err != nil {
		return nil, err
	}
	var enabledModelIDs []string
	if len(models.policyModelIDs) > 0 {
		interaction.Notify(AuthEvent{Type: "progress", Message: "Enabling models..."})
		enabledModelIDs, err = githubCopilotEnableModels(ctx, credentials.Access, models.policyModelIDs, enterpriseDomain)
		if err != nil {
			return nil, err
		}
	}
	// pi: availableModelIds: [...new Set([...availableModelIds, ...enabledModelIds])]
	// — insertion order, first occurrence wins.
	available := append([]string{}, models.availableModelIDs...)
	available = append(available, enabledModelIDs...)
	seen := make(map[string]bool, len(available))
	deduped := available[:0]
	for _, id := range available {
		if seen[id] {
			continue
		}
		seen[id] = true
		deduped = append(deduped, id)
	}
	gitHubCopilotSetAvailableModelIDs(credentials, deduped)
	return credentials, nil
}

// GitHubCopilotOAuth is the GitHub Copilot subscription OAuth auth (pi:
// githubCopilotOAuth).
type GitHubCopilotOAuth struct{}

func (GitHubCopilotOAuth) Name() string         { return "GitHub Copilot" }
func (GitHubCopilotOAuth) IsSubscription() bool { return true }
func (GitHubCopilotOAuth) LoginLabel() string   { return "" }

func (GitHubCopilotOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return githubCopilotLogin(ctx, interaction)
}

func (GitHubCopilotOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return githubCopilotRefreshToken(ctx, credential.Refresh, gitHubCopilotEnterpriseURL(credential))
}

// ToAuth derives the credential-specific proxy endpoint for each request (pi:
// toAuth).
func (GitHubCopilotOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{
		APIKey:  credential.Access,
		BaseURL: githubCopilotBaseURL(credential.Access, gitHubCopilotEnterpriseURL(credential)),
	}, nil
}

// githubCopilotOAuthDo is the HTTP transport seam for every Copilot OAuth
// endpoint call (pi: global fetch, stubbed by tests).
var githubCopilotOAuthDo = func(ctx context.Context, method, requestURL string, headers map[string]string, body string) (*http.Response, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{}
	return client.Do(request)
}

// numberFromAny reads a JSON number as float64 (pi: typeof x === "number").
func numberFromAny(value any) (float64, bool) {
	number, isNumber := value.(float64)
	return number, isNumber
}

// escapeControlBytes percent-encodes ASCII control characters (pi's URL
// normalization keeps them out of the raw URI before the scheme check).
func escapeControlBytes(input string) string {
	if !strings.ContainsFunc(input, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return input
	}
	var builder strings.Builder
	for i := 0; i < len(input); i++ {
		b := input[i]
		if b < 0x20 || b == 0x7f {
			builder.WriteString(fmt.Sprintf("%%%02X", b))
		} else {
			builder.WriteByte(b)
		}
	}
	return builder.String()
}

// mustDecodeBase64 decodes pi's obfuscated constants; the literals are
// compile-time constants so errors are impossible.
func mustDecodeBase64(encoded string) string {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		panic(fmt.Sprintf("invalid base64 constant %q: %v", encoded, err))
	}
	return string(decoded)
}
