// pi: auth/oauth/kimi-coding.ts.
//
// Kimi Code (subscription) OAuth flow — RFC 8628 device authorization grant
// against https://auth.kimi.com with JSON responses. The access token
// authenticates requests to https://api.kimi.com/coding as an
// Authorization: Bearer header.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

const (
	kimiOAuthClientID              = "17e5f671-d194-4dfb-9706-5516cb48c098"
	kimiOAuthDefaultHost           = "https://auth.kimi.com"
	kimiDeviceCodeTimeoutSeconds   = int64(15 * 60)
	kimiDefaultPollIntervalSeconds = int64(5)
	kimiRequestTimeout             = 30 * time.Second
	kimiRefreshMaxRetries          = 3
)

// getKimiOAuthHost resolves the host override (pi: getOauthHost; env
// overrides KIMI_CODE_OAUTH_HOST / KIMI_OAUTH_HOST).
func getKimiOAuthHost(env types.ProviderEnv) string {
	override := utils.GetProviderEnvValue("KIMI_CODE_OAUTH_HOST", env)
	if override == "" {
		override = utils.GetProviderEnvValue("KIMI_OAUTH_HOST", env)
	}
	if override == "" {
		override = kimiOAuthDefaultHost
	}
	return strings.TrimRight(override, "/")
}

// kimiFormURLEncode encodes one form post body (pi: formUrlEncode via
// URLSearchParams).
func kimiFormURLEncode(fields map[string]string) string {
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	return values.Encode()
}

// kimiReadJSON parses one JSON response body; a null JSON or parse failure
// yields nil (pi: readJson).
func kimiReadJSON(response *http.Response) (map[string]any, error) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil
	}
	if m, ok := parsed.(map[string]any); ok {
		return m, nil
	}
	return nil, nil
}

// trustedHTTPURL only trusts http(s) URLs for the browser (pi:
// trustedHttpUrl).
func trustedHTTPURL(value any) string {
	s, ok := value.(string)
	if !ok || s == "" {
		return ""
	}
	parsed, err := url.Parse(s)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.String()
}

// kimiDeviceAuthorization is the device authorization response (pi:
// DeviceAuthorization).
type kimiDeviceAuthorization struct {
	deviceCode              string
	userCode                string
	verificationUri         string
	verificationUriComplete string
	intervalSeconds         int64
	expiresInSeconds        int64
}

// kimiHTTPPostForm posts one form body with the request timeout (pi: fetch
// with AbortSignal.any([timeout, signal]); ctx carries the signal).
func kimiHTTPPostForm(ctx context.Context, url string, body string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: kimiRequestTimeout}
	return client.Do(request)
}

// startKimiDeviceAuthorization begins the device grant (pi:
// startDeviceAuthorization).
func startKimiDeviceAuthorization(ctx context.Context, oauthHost string) (*kimiDeviceAuthorization, error) {
	response, err := kimiHTTPPostForm(ctx, oauthHost+"/api/oauth/device_authorization", kimiFormURLEncode(map[string]string{"client_id": kimiOAuthClientID}))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		text, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("Kimi Code device authorization failed with status %d%s", response.StatusCode, textSuffix(string(text)))
	}
	jsonBody, err := kimiReadJSON(response)
	if err != nil {
		return nil, err
	}
	nonNullJSON := jsonBody
	if nonNullJSON == nil {
		nonNullJSON = map[string]any{}
	}
	deviceCode, _ := nonNullJSON["device_code"].(string)
	userCode, _ := nonNullJSON["user_code"].(string)
	verificationUri, _ := nonNullJSON["verification_uri"].(string)
	verificationUriComplete, _ := nonNullJSON["verification_uri_complete"].(string)
	if deviceCode == "" || userCode == "" || verificationUri == "" ||
		trustedHTTPURL(verificationUriComplete) == "" || trustedHTTPURL(verificationUri) == "" {
		encoded, _ := json.Marshal(nonNullJSON)
		return nil, fmt.Errorf("Invalid Kimi Code device authorization response: %s", encoded)
	}
	intervalSeconds := kimiDefaultPollIntervalSeconds
	if interval, ok := nonNullJSON["interval"].(float64); ok && !math.IsNaN(interval) && !math.IsInf(interval, 0) && interval > 0 {
		intervalSeconds = int64(interval)
	}
	expiresInSeconds := kimiDeviceCodeTimeoutSeconds
	if expiresIn, ok := nonNullJSON["expires_in"].(float64); ok && !math.IsNaN(expiresIn) && !math.IsInf(expiresIn, 0) && expiresIn > 0 {
		expiresInSeconds = int64(expiresIn)
	}
	return &kimiDeviceAuthorization{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationUri:         verificationUri,
		verificationUriComplete: verificationUriComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

// kimiParseTokenResponse validates one token response (pi:
// parseTokenResponse).
func kimiParseTokenResponse(jsonBody map[string]any, operation string) (access string, refresh string, expires int64, err error) {
	accessToken, _ := jsonBody["access_token"].(string)
	refreshToken, _ := jsonBody["refresh_token"].(string)
	expiresIn, _ := jsonBody["expires_in"].(float64)
	if accessToken == "" || refreshToken == "" || math.IsNaN(expiresIn) || math.IsInf(expiresIn, 0) || expiresIn <= 0 {
		encoded, _ := json.Marshal(jsonBody)
		return "", "", 0, fmt.Errorf("Kimi Code token %s response missing fields: %s", operation, encoded)
	}
	return accessToken, refreshToken, time.Now().UnixMilli() + int64(expiresIn)*1000, nil
}

// pollForKimiToken polls the token endpoint until completion (pi:
// pollForToken via pollOAuthDeviceCodeFlow).
func pollForKimiToken(ctx context.Context, oauthHost string, device *kimiDeviceAuthorization) (kimiTokenResponse, error) {
	return PollOAuthDeviceCodeFlow(DeviceCodePollOptions[kimiTokenResponse]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Ctx:                 ctx,
		Poll: func() DeviceCodePollResult[kimiTokenResponse] {
			response, pollErr := kimiHTTPPostForm(ctx, oauthHost+"/api/oauth/token", kimiFormURLEncode(map[string]string{
				"client_id":   kimiOAuthClientID,
				"device_code": device.deviceCode,
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			}))
			if pollErr != nil {
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: pollErr.Error()}
			}
			defer response.Body.Close()
			if response.StatusCode >= 500 {
				text, _ := io.ReadAll(response.Body)
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: fmt.Sprintf("Kimi Code device token request failed with status %d%s", response.StatusCode, textSuffix(string(text)))}
			}
			jsonBody, readErr := kimiReadJSON(response)
			if readErr != nil {
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: readErr.Error()}
			}
			nonNullJSON := jsonBody
			if nonNullJSON == nil {
				nonNullJSON = map[string]any{}
			}
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				if _, hasToken := nonNullJSON["access_token"].(string); hasToken {
					access, refresh, expires, parseErr := kimiParseTokenResponse(nonNullJSON, "poll")
					if parseErr != nil {
						return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: parseErr.Error()}
					}
					return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeComplete, Value: kimiTokenResponse{access: access, refresh: refresh, expires: expires}}
				}
			}
			errorCode, _ := nonNullJSON["error"].(string)
			description := ""
			if rawDescription, ok := nonNullJSON["error_description"].(string); ok {
				description = ": " + rawDescription
			}
			switch errorCode {
			case "authorization_pending":
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodePending}
			case "slow_down":
				result := DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeSlowDown}
				if interval, ok := nonNullJSON["interval"].(float64); ok && interval > 0 {
					result.IntervalSeconds = int64(interval)
				}
				return result
			case "expired_token":
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: "Kimi Code device authorization expired. Please restart login."}
			case "access_denied":
				return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: "Kimi Code login was denied."}
			}
			return DeviceCodePollResult[kimiTokenResponse]{Status: DeviceCodeFailed, Message: fmt.Sprintf("Kimi Code device token request failed (status %d)%s", response.StatusCode, errorCodeSuffix(errorCode, description))}
		},
	})
}

// kimiTokenResponse carries the poll result fields (pi: TokenResponse).
type kimiTokenResponse struct {
	access  string
	refresh string
	expires int64
}

func textSuffix(text string) string {
	if text != "" {
		return ": " + text
	}
	return ""
}

func errorCodeSuffix(errorCode string, description string) string {
	if errorCode != "" {
		return ": " + errorCode + description
	}
	return ""
}

// kimiIsRetryableRefreshFailure reports whether a refresh failure may be
// retried (pi: isRetryableRefreshFailure).
func kimiIsRetryableRefreshFailure(status int) bool {
	return status == 429 || status >= 500
}

// refreshKimiToken exchanges the refresh token with bounded retries (pi:
// refreshToken).
func refreshKimiToken(ctx context.Context, oauthHost string, refreshToken string) (kimiTokenResponse, error) {
	var lastError error
	for attempt := 0; attempt <= kimiRefreshMaxRetries; attempt++ {
		if attempt > 0 {
			delayMs := int64(1000) * (1 << (attempt - 1))
			timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return kimiTokenResponse{}, fmt.Errorf("Kimi Code token refresh aborted")
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			return kimiTokenResponse{}, fmt.Errorf("Kimi Code token refresh aborted")
		}

		response, requestErr := kimiHTTPPostForm(ctx, oauthHost+"/api/oauth/token", kimiFormURLEncode(map[string]string{
			"client_id":     kimiOAuthClientID,
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
		}))
		if requestErr != nil {
			lastError = requestErr
			continue
		}
		jsonBody, readErr := kimiReadJSON(response)
		if readErr != nil {
			response.Body.Close()
			lastError = readErr
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			response.Body.Close()
			access, refresh, expires, parseErr := kimiParseTokenResponse(orEmptyMap(jsonBody), "refresh")
			if parseErr != nil {
				return kimiTokenResponse{}, parseErr
			}
			return kimiTokenResponse{access: access, refresh: refresh, expires: expires}, nil
		}
		nonNullJSON := orEmptyMap(jsonBody)
		// Unauthorized: the stored credential is dead; Models clears it and
		// prompts re-login.
		if response.StatusCode == 401 || response.StatusCode == 403 || nonNullJSON["error"] == "invalid_grant" {
			response.Body.Close()
			description := ""
			if rawDescription, ok := nonNullJSON["error_description"].(string); ok {
				description = ": " + rawDescription
			}
			return kimiTokenResponse{}, fmt.Errorf("Kimi Code token refresh unauthorized (status %d)%s", response.StatusCode, description)
		}
		if kimiIsRetryableRefreshFailure(response.StatusCode) && attempt < kimiRefreshMaxRetries {
			response.Body.Close()
			lastError = fmt.Errorf("Kimi Code token refresh failed with status %d", response.StatusCode)
			continue
		}
		encoded, _ := json.Marshal(nonNullJSON)
		response.Body.Close()
		return kimiTokenResponse{}, fmt.Errorf("Kimi Code token refresh failed with status %d%s", response.StatusCode, textSuffix(string(encoded)))
	}
	if lastError == nil {
		lastError = fmt.Errorf("Kimi Code token refresh failed")
	}
	return kimiTokenResponse{}, lastError
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// loginKimiCoding runs the device-code login (pi: loginKimiCoding).
func loginKimiCoding(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	oauthHost := getKimiOAuthHost(nil)
	device, err := startKimiDeviceAuthorization(ctx, oauthHost)
	if err != nil {
		return nil, err
	}
	interval := device.intervalSeconds
	expiresIn := device.expiresInSeconds
	interaction.Notify(AuthEvent{
		Type:             "device_code",
		UserCode:         device.userCode,
		VerificationURI:  device.verificationUriComplete,
		IntervalSeconds:  &interval,
		ExpiresInSeconds: &expiresIn,
	})
	token, err := pollForKimiToken(ctx, oauthHost, device)
	if err != nil {
		return nil, err
	}
	return &OAuthCredential{OAuthCredentials: OAuthCredentials{Refresh: token.refresh, Access: token.access, Expires: token.expires}}, nil
}

// KimiCodingOAuth is the Kimi Code subscription OAuth auth (pi:
// kimiCodingOAuth).
type KimiCodingOAuth struct{}

func (KimiCodingOAuth) Name() string         { return "Kimi Code (subscription)" }
func (KimiCodingOAuth) IsSubscription() bool { return true }
func (KimiCodingOAuth) LoginLabel() string   { return "Sign in with Kimi Code" }

// Login runs the provider-owned login flow.
func (KimiCodingOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return loginKimiCoding(ctx, interaction)
}

// Refresh exchanges the refresh token.
func (KimiCodingOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	token, err := refreshKimiToken(ctx, getKimiOAuthHost(nil), credential.Refresh)
	if err != nil {
		return nil, err
	}
	return &OAuthCredential{OAuthCredentials: OAuthCredentials{Refresh: token.refresh, Access: token.access, Expires: token.expires}}, nil
}

// ToAuth derives request auth from a valid credential.
func (KimiCodingOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	bearer := "Bearer " + credential.Access
	return &ModelAuth{Headers: types.ProviderHeaders{"Authorization": &bearer}}, nil
}
