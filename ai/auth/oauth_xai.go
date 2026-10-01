// pi: auth/oauth/xai.ts.
//
// xAI OAuth device-code flow (RFC 8628) against https://auth.x.ai with JSON
// responses. The access token authenticates requests to
// https://api.x.ai/v1 as an Authorization: Bearer header.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	xaiClientID       = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope          = "openid profile email offline_access grok-cli:access api:access"
	xaiDeviceCodeURL  = "https://auth.x.ai/oauth2/device/code"
	xaiTokenURL       = "https://auth.x.ai/oauth2/token"
	xaiRequestTimeout = 30 * time.Second
	// Refresh slightly before the reported expiry to avoid using a token that dies mid-request.
	xaiRefreshSkewMs            = int64(5 * 60 * 1000)
	xaiDefaultTokenLifetimeSecs = int64(3600)
)

// xaiDeviceCode is the device authorization response (pi: XaiDeviceCode).
type xaiDeviceCode struct {
	deviceCode              string
	userCode                string
	verificationURI         string
	verificationURIComplete string
	intervalSeconds         int64
	expiresInSeconds        int64
}

// xaiPostForm posts one form body with the request timeout (pi: postForm;
// pi's signal → ctx; cancel during the call maps to the login-cancel
// message).
func xaiPostForm(ctx context.Context, postURL string, fields map[string]string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(xaiFormURLEncode(fields)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: xaiRequestTimeout}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s", deviceCodeCancelMessage)
		}
		return nil, err
	}
	return response, nil
}

// xaiFormURLEncode encodes one form post body (pi: new URLSearchParams).
func xaiFormURLEncode(fields map[string]string) string {
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	return values.Encode()
}

// xaiReadJSON parses one JSON response body; a non-object or parse failure
// yields an empty map (pi: the postForm json parse catch).
func xaiReadJSON(response *http.Response) (map[string]any, error) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		if response.Request != nil && response.Request.Context().Err() != nil {
			return nil, fmt.Errorf("%s", deviceCodeCancelMessage)
		}
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		if response.Request != nil && response.Request.Context().Err() != nil {
			return nil, fmt.Errorf("%s", deviceCodeCancelMessage)
		}
		return nil, fmt.Errorf("xAI OAuth returned invalid JSON (HTTP %d)", response.StatusCode)
	}
	if m, ok := parsed.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

// xaiRequestFailure builds the failure error for a non-2xx OAuth response
// (pi: requestFailure).
func xaiRequestFailure(action string, status int, body map[string]any) error {
	parts := []string{}
	if errStr, ok := body["error"].(string); ok && errStr != "" {
		parts = append(parts, errStr)
	}
	if desc, ok := body["error_description"].(string); ok && desc != "" {
		parts = append(parts, desc)
	}
	detail := strings.Join(parts, ": ")
	message := fmt.Sprintf("xAI OAuth %s failed (HTTP %d)", action, status)
	if detail != "" {
		message += ": " + detail
	}
	return fmt.Errorf("%s", message)
}

// xaiRequiredString extracts a required non-empty string field (pi:
// requiredString).
func xaiRequiredString(body map[string]any, field string) (string, error) {
	value, _ := body[field].(string)
	if value == "" {
		return "", fmt.Errorf("Invalid xAI OAuth response field: %s", field)
	}
	return value, nil
}

// xaiPositiveNumber extracts a required finite positive number field (pi:
// positiveNumber).
func xaiPositiveNumber(body map[string]any, field string) (int64, error) {
	number, ok := body[field].(float64)
	if !ok || number != number || number <= 0 {
		return 0, fmt.Errorf("Invalid xAI OAuth response field: %s", field)
	}
	return int64(number), nil
}

// xaiValidateVerificationURI validates the verification URI is https so a
// malicious response cannot make `open` launch something else (pi:
// validateVerificationUri).
func xaiValidateVerificationURI(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return "", fmt.Errorf("Untrusted verification URI in xAI OAuth response")
	}
	return parsed.String(), nil
}

// xaiParseDeviceCode parses the device authorization response (pi:
// parseDeviceCode). RFC 8628 allows interval 0 (no minimum wait); fall back
// to the poller's default instead of failing on non-positive values.
func xaiParseDeviceCode(body map[string]any) (*xaiDeviceCode, error) {
	deviceCode, err := xaiRequiredString(body, "device_code")
	if err != nil {
		return nil, err
	}
	userCode, err := xaiRequiredString(body, "user_code")
	if err != nil {
		return nil, err
	}
	verificationURI, err := xaiValidateVerificationURI(mustXaiString(body, "verification_uri"))
	if err != nil {
		return nil, err
	}
	intervalSeconds := int64(0)
	if interval, ok := body["interval"].(float64); ok && interval > 0 {
		intervalSeconds = int64(interval)
	}
	verificationURIComplete := ""
	if raw := mustXaiString(body, "verification_uri_complete"); raw != "" {
		validated, err := xaiValidateVerificationURI(raw)
		if err != nil {
			return nil, err
		}
		verificationURIComplete = validated
	}
	expiresInSeconds, err := xaiPositiveNumber(body, "expires_in")
	if err != nil {
		return nil, err
	}
	return &xaiDeviceCode{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationURI:         verificationURI,
		verificationURIComplete: verificationURIComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

// mustXaiString reads an optional string field (empty when absent/wrong
// type).
func mustXaiString(body map[string]any, field string) string {
	value, _ := body[field].(string)
	return value
}

// xaiCredentialsFromTokenResponse builds the credential from a token
// response (pi: credentialsFromTokenResponse). xAI may omit refresh_token on
// refresh when the token is not rotated.
func xaiCredentialsFromTokenResponse(body map[string]any, previousRefreshToken string) (*OAuthCredential, error) {
	access, err := xaiRequiredString(body, "access_token")
	if err != nil {
		return nil, err
	}
	refresh := ""
	if _, present := body["refresh_token"]; !present && previousRefreshToken != "" {
		refresh = previousRefreshToken
	} else {
		refresh, err = xaiRequiredString(body, "refresh_token")
		if err != nil {
			return nil, err
		}
	}
	expiresInSeconds := xaiDefaultTokenLifetimeSecs
	if _, present := body["expires_in"]; present {
		expiresInSeconds, err = xaiPositiveNumber(body, "expires_in")
		if err != nil {
			return nil, err
		}
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: refresh,
			Access:  access,
			// Expires is a Unix timestamp in milliseconds.
			Expires: time.Now().UnixMilli() + expiresInSeconds*1000 - xaiRefreshSkewMs,
		},
	}, nil
}

// xaiRequestDeviceCode begins the device grant (pi: requestDeviceCode).
func xaiRequestDeviceCode(ctx context.Context) (*xaiDeviceCode, error) {
	response, err := xaiPostForm(ctx, xaiDeviceCodeURL, map[string]string{
		"client_id": xaiClientID,
		"scope":     xaiScope,
		"referrer":  "pi",
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := xaiReadJSON(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, xaiRequestFailure("device authorization", response.StatusCode, body)
	}
	return xaiParseDeviceCode(body)
}

// xaiPollForTokens polls the token endpoint until the grant completes (pi:
// pollForTokens).
func xaiPollForTokens(ctx context.Context, device *xaiDeviceCode) (*OAuthCredential, error) {
	return PollOAuthDeviceCodeFlow(DeviceCodePollOptions[*OAuthCredential]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Ctx:                 ctx,
		Poll: func() DeviceCodePollResult[*OAuthCredential] {
			response, err := xaiPostForm(ctx, xaiTokenURL, map[string]string{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"client_id":   xaiClientID,
				"device_code": device.deviceCode,
			})
			if err != nil {
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeFailed, Message: err.Error()}
			}
			defer response.Body.Close()
			body, err := xaiReadJSON(response)
			if err != nil {
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeFailed, Message: err.Error()}
			}

			if response.StatusCode >= 200 && response.StatusCode < 300 {
				credential, err := xaiCredentialsFromTokenResponse(body, "")
				if err != nil {
					return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeFailed, Message: err.Error()}
				}
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeComplete, Value: credential}
			}

			switch body["error"] {
			case "authorization_pending":
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodePending}
			case "slow_down":
				intervalSeconds := int64(0)
				if interval, ok := body["interval"].(float64); ok {
					intervalSeconds = int64(interval)
				}
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeSlowDown, IntervalSeconds: intervalSeconds}
			case "access_denied", "authorization_denied":
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeFailed, Message: "xAI device authorization was denied"}
			case "expired_token":
				return DeviceCodePollResult[*OAuthCredential]{Status: DeviceCodeFailed, Message: "xAI device code expired"}
			default:
				return DeviceCodePollResult[*OAuthCredential]{
					Status:  DeviceCodeFailed,
					Message: xaiRequestFailure("device token polling", response.StatusCode, body).Error(),
				}
			}
		},
	})
}

// loginXai runs the full device flow (pi: loginXai).
func loginXai(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	device, err := xaiRequestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}
	verificationURI := device.verificationURI
	if device.verificationURIComplete != "" {
		verificationURI = device.verificationURIComplete
	}
	interaction.Notify(AuthEvent{
		Type:            "device_code",
		UserCode:        device.userCode,
		VerificationURI: verificationURI,
	})
	return xaiPollForTokens(ctx, device)
}

// refreshXaiToken exchanges the refresh token (pi: refreshXaiToken).
func refreshXaiToken(ctx context.Context, refreshToken string) (*OAuthCredential, error) {
	response, err := xaiPostForm(ctx, xaiTokenURL, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     xaiClientID,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := xaiReadJSON(response)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, xaiRequestFailure("token refresh", response.StatusCode, body)
	}
	return xaiCredentialsFromTokenResponse(body, refreshToken)
}

// XaiOAuth is the xAI (Grok/X subscription) OAuth auth method (pi: xaiOAuth).
type XaiOAuth struct{}

func (XaiOAuth) Name() string         { return "xAI (Grok/X subscription)" }
func (XaiOAuth) IsSubscription() bool { return true }
func (XaiOAuth) LoginLabel() string   { return "Sign in with SuperGrok or X Premium" }

// Login runs the provider-owned login flow.
func (XaiOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return loginXai(ctx, interaction)
}

// Refresh exchanges the refresh token.
func (XaiOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return refreshXaiToken(ctx, credential.Refresh)
}

// ToAuth derives request auth from the stored credential (pi: toAuth).
func (XaiOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
