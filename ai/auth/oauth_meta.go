// pi: auth/oauth/meta.ts.
//
// # Meta Model API OAuth flow
//
// RFC 8628 device authorization grant against https://auth.meta.com (JSON
// responses). Meta splits identity from API access: the resulting identity
// token is not accepted for inference, so it is exchanged for a Model API
// key via the Muse Code key-mint endpoint (minted keys live about a day).
// The identity token is stored as `refresh` and the minted key as `access`,
// so the standard OAuth scheduler re-mints the key when it expires with no
// bespoke renewal machinery. The identity token itself is not renewable
// (auth.meta.com answers grant_type=refresh_token with 404 and issues no
// refresh_token), so a 401/403 from mint means the session is dead and the
// user must sign in again.
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

	"github.com/samber/lo"
)

const (
	metaClientID               = "1031625952748946"
	metaAuthHost               = "https://auth.meta.com"
	metaDeviceAuthorizationURL = metaAuthHost + "/oidc/device/authorization/"
	metaDeviceTokenURL         = metaAuthHost + "/oidc/device/token/"
	metaAPIKeyMintURL          = "https://api.meta.ai/muse-code/key"
	metaAPIKeyLifetimeMs       = int64(24 * 60 * 60 * 1000)
	metaRequestTimeout         = 30 * time.Second
)

// metaOAuthDo is the HTTP transport seam for every Meta OAuth endpoint (pi:
// the global fetch stubbed by tests). The request carries its context and
// headers; pi's AbortSignal.any([AbortSignal.timeout(30s), signal]) maps
// onto http.Client.Timeout + the request context.
var metaOAuthDo = func(request *http.Request) (*http.Response, error) {
	client := &http.Client{Timeout: metaRequestTimeout}
	return client.Do(request)
}

// metaOAuthFetch runs one Meta OAuth request, mapping transport failures
// during cancellation to the login-cancel message (pig convention shared
// with xai/codex; pi surfaces the DOMException and loginMeta re-maps it).
func metaOAuthFetch(ctx context.Context, request *http.Request) (*http.Response, error) {
	response, err := metaOAuthDo(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s", deviceCodeCancelMessage)
		}
		return nil, err
	}
	return response, nil
}

// metaNewRequest builds one request with the JSON Accept header all Meta
// endpoints expect (pi: the per-fetch headers object).
func metaNewRequest(ctx context.Context, method, requestURL, contentType, body string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

// metaReadJSON parses one JSON response body; a non-object or parse failure
// yields an empty map (pi: readJson returning null — every consumer reads
// optional fields off it).
func metaReadJSON(response *http.Response) map[string]any {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return map[string]any{}
	}
	var parsed any
	if json.Unmarshal(body, &parsed) != nil {
		return map[string]any{}
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// metaErrorDetail appends the first meaningful error field (pi: errorDetail).
func metaErrorDetail(body map[string]any) string {
	for _, key := range []string{"error_description", "detail", "message", "error"} {
		if value, ok := body[key].(string); ok && strings.TrimSpace(value) != "" {
			return ": " + strings.TrimSpace(value)
		}
	}
	return ""
}

// metaTrustedHTTPURL validates the URL is http(s) so a malicious response
// cannot make `open` launch something else (pi: trustedHttpUrl). "" when
// untrusted (pi null/undefined).
func metaTrustedHTTPURL(value any) string {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	return parsed.String()
}

// metaPositiveNumber extracts a finite positive number (pi: positiveNumber);
// ok=false = pi undefined.
func metaPositiveNumber(body map[string]any, field string) (int64, bool) {
	number, ok := body[field].(float64)
	if !ok || number != number || number <= 0 {
		return 0, false
	}
	return int64(number), true
}

// metaDeviceAuthorization is the device authorization response (pi:
// DeviceAuthorization). IntervalSeconds/ExpiresInSeconds are absent (0)
// when the server omits them.
type metaDeviceAuthorization struct {
	deviceCode       string
	userCode         string
	verificationURI  string
	intervalSeconds  int64
	expiresInSeconds int64
}

// metaStartDeviceAuthorization begins the device grant (pi:
// startDeviceAuthorization).
func metaStartDeviceAuthorization(ctx context.Context) (*metaDeviceAuthorization, error) {
	request, err := metaNewRequest(ctx, http.MethodPost, metaDeviceAuthorizationURL,
		"application/x-www-form-urlencoded", url.Values{"client_id": {metaClientID}}.Encode())
	if err != nil {
		return nil, err
	}
	response, err := metaOAuthFetch(ctx, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body := metaReadJSON(response)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Meta device authorization failed with status %d%s",
			response.StatusCode, metaErrorDetail(body))
	}
	deviceCode, _ := body["device_code"].(string)
	userCode, _ := body["user_code"].(string)
	verificationURI := metaTrustedHTTPURL(body["verification_uri_complete"])
	if verificationURI == "" {
		verificationURI = metaTrustedHTTPURL(body["verification_uri"])
	}
	if deviceCode == "" || userCode == "" || verificationURI == "" {
		encoded, _ := json.Marshal(body)
		return nil, fmt.Errorf("Invalid Meta device authorization response: %s", encoded)
	}
	intervalSeconds := int64(0)
	if interval, ok := metaPositiveNumber(body, "interval"); ok {
		intervalSeconds = interval
	}
	expiresInSeconds := int64(0)
	if expires, ok := metaPositiveNumber(body, "expires_in"); ok {
		expiresInSeconds = expires
	}
	return &metaDeviceAuthorization{
		deviceCode:       deviceCode,
		userCode:         userCode,
		verificationURI:  verificationURI,
		intervalSeconds:  intervalSeconds,
		expiresInSeconds: expiresInSeconds,
	}, nil
}

// metaPollForIdentityToken polls the token endpoint until the grant
// completes, yielding the identity token (pi: pollForIdentityToken).
func metaPollForIdentityToken(ctx context.Context, device *metaDeviceAuthorization) (string, error) {
	return PollOAuthDeviceCodeFlow(DeviceCodePollOptions[string]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Ctx:                 ctx,
		Poll: func() DeviceCodePollResult[string] {
			request, err := metaNewRequest(ctx, http.MethodPost, metaDeviceTokenURL,
				"application/x-www-form-urlencoded", url.Values{
					"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
					"device_code": {device.deviceCode},
					"client_id":   {metaClientID},
				}.Encode())
			if err != nil {
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: err.Error()}
			}
			response, err := metaOAuthFetch(ctx, request)
			if err != nil {
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: err.Error()}
			}
			defer response.Body.Close()
			body := metaReadJSON(response)
			if accessToken, ok := body["access_token"].(string); ok &&
				response.StatusCode >= 200 && response.StatusCode < 300 && accessToken != "" {
				return DeviceCodePollResult[string]{Status: DeviceCodeComplete, Value: accessToken}
			}
			switch body["error"] {
			case "authorization_pending":
				return DeviceCodePollResult[string]{Status: DeviceCodePending}
			case "slow_down":
				intervalSeconds, _ := metaPositiveNumber(body, "interval")
				return DeviceCodePollResult[string]{Status: DeviceCodeSlowDown, IntervalSeconds: intervalSeconds}
			case "access_denied":
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: "Meta login was denied."}
			case "expired_token":
				return DeviceCodePollResult[string]{Status: DeviceCodeFailed, Message: "Meta device authorization expired. Please restart login."}
			default:
				return DeviceCodePollResult[string]{
					Status:  DeviceCodeFailed,
					Message: fmt.Sprintf("Meta device token request failed with status %d%s", response.StatusCode, metaErrorDetail(body)),
				}
			}
		},
	})
}

// metaMintAPIKey exchanges an identity token for a Model API key; keys are
// valid for about a day (pi: mintApiKey).
func metaMintAPIKey(ctx context.Context, identityToken string) (*OAuthCredential, error) {
	request, err := metaNewRequest(ctx, http.MethodPost, metaAPIKeyMintURL, "application/json", "{}")
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+identityToken)
	request.Header.Set("x-api-version", "1.0.0")
	response, err := metaOAuthFetch(ctx, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body := metaReadJSON(response)
	if response.StatusCode == 401 || response.StatusCode == 403 {
		// Identity token is not renewable (see file header); only a fresh device flow helps.
		return nil, fmt.Errorf("Meta session expired (status %d). Run `/login meta` to sign in again.%s",
			response.StatusCode, metaErrorDetail(body))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Meta API key mint failed with status %d%s", response.StatusCode, metaErrorDetail(body))
	}
	apiKey, _ := body["api_key"].(string)
	if apiKey == "" {
		actionURL := metaTrustedHTTPURL(body["action_url"])
		suffix := ""
		if actionURL != "" {
			suffix = " Complete setup at " + actionURL
		}
		return nil, fmt.Errorf("Meta did not issue an API key.%s", suffix)
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: identityToken,
			Access:  apiKey,
			// Expires is a Unix timestamp in milliseconds (pi: Date.now() under
			// the fake-timer clock — pig: the shared deviceCodeNow seam).
			Expires: deviceCodeNow().UnixMilli() + metaAPIKeyLifetimeMs,
		},
	}, nil
}

// loginMeta runs the full Meta login flow (pi: loginMeta).
func loginMeta(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	device, err := metaStartDeviceAuthorization(ctx)
	if err != nil {
		return nil, err
	}
	event := AuthEvent{
		Type:            "device_code",
		UserCode:        device.userCode,
		VerificationURI: device.verificationURI,
	}
	if device.intervalSeconds > 0 {
		event.IntervalSeconds = lo.ToPtr(device.intervalSeconds)
	}
	if device.expiresInSeconds > 0 {
		event.ExpiresInSeconds = lo.ToPtr(device.expiresInSeconds)
	}
	interaction.Notify(event)
	identityToken, err := metaPollForIdentityToken(ctx, device)
	if err != nil {
		return nil, err
	}
	interaction.Notify(AuthEvent{Type: "progress", Message: "Enabling Meta Model API access..."})
	return metaMintAPIKey(ctx, identityToken)
}

// MetaOAuth is the Meta (Muse subscription) OAuth auth method (pi: metaOAuth).
type MetaOAuth struct{}

func (MetaOAuth) Name() string         { return "Meta (Muse subscription)" }
func (MetaOAuth) IsSubscription() bool { return true }
func (MetaOAuth) LoginLabel() string   { return "Sign in with Meta" }

// Login runs the provider-owned login flow.
func (MetaOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	return loginMeta(ctx, interaction)
}

// Refresh re-mints the API key from the stored identity token.
func (MetaOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	return metaMintAPIKey(ctx, credential.Refresh)
}

// ToAuth derives request auth from the stored credential (pi: toAuth).
func (MetaOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
