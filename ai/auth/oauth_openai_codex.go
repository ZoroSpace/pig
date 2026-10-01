// pi: auth/oauth/openai-codex.ts.
//
// OpenAI Codex (ChatGPT OAuth) flow. pi uses Node's crypto/http for the
// callback server (CLI-only); pig uses net/http directly. All OAuth HTTP
// traffic goes through the codexOAuthDo seam so tests can stub it (pi:
// vi.stubGlobal("fetch")).
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

const (
	codexOAuthClientID             = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexAuthBaseURL               = "https://auth.openai.com"
	codexAuthorizeURL              = codexAuthBaseURL + "/oauth/authorize"
	codexTokenURL                  = codexAuthBaseURL + "/oauth/token"
	codexRedirectURI               = "http://localhost:1455/auth/callback"
	codexDeviceUserCodeURL         = codexAuthBaseURL + "/api/accounts/deviceauth/usercode"
	codexDeviceTokenURL            = codexAuthBaseURL + "/api/accounts/deviceauth/token"
	codexDeviceVerificationURI     = codexAuthBaseURL + "/codex/device"
	codexDeviceRedirectURI         = codexAuthBaseURL + "/deviceauth/callback"
	codexDeviceCodeTimeoutSeconds  = int64(15 * 60)
	codexBrowserLoginMethod        = "browser"
	codexDeviceCodeLoginMethod     = "device_code"
	codexScope                     = "openid profile email offline_access"
	codexJWTClaimPath              = "https://api.openai.com/auth"
	codexOAuthCallbackPort         = 1455
	codexOAuthCallbackPath         = "/auth/callback"
	codexOAuthClientRequestTimeout = 0 // pi's fetch has no timeout; ctx governs
)

// codexTimeNow is the token-expiry clock seam; it aliases deviceCodeNow so
// one fake clock controls poll deadlines and expires math (pi: Date.now()).
var codexTimeNow = func() time.Time { return deviceCodeNow() }

// codexOAuthDo is the HTTP transport seam for every codex OAuth endpoint
// (pi: the global fetch stubbed by tests). contentType may be empty.
var codexOAuthDo = func(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, requestURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Timeout: codexOAuthClientRequestTimeout}
	return client.Do(request)
}

// codexOAuthToken is one token endpoint payload (pi: OAuthToken).
type codexOAuthToken struct {
	access  string
	refresh string
	// expires is a Unix timestamp in milliseconds.
	expires int64
}

// codexDeviceAuthInfo is the device-authorization response (pi:
// DeviceAuthInfo).
type codexDeviceAuthInfo struct {
	deviceAuthID string
	userCode     string
	// intervalSeconds keeps the server-reported poll interval as a number
	// (pi parses strings via Number(trim)); the poll loop floors to whole
	// seconds when handing it to PollOAuthDeviceCodeFlow.
	intervalSeconds float64
}

// codexDeviceTokenSuccess is what a completed device poll yields (pi:
// DeviceTokenSuccess).
type codexDeviceTokenSuccess struct {
	authorizationCode string
	codeVerifier      string
}

// getCodexCallbackHost resolves the callback bind host (pi:
// getCallbackHost from PI_OAUTH_CALLBACK_HOST; pig reads the PIG_ spelling
// and falls back to the pi spelling for shared shells).
func getCodexCallbackHost() string {
	if host := utils.GetProviderEnvValue("PIG_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	if host := utils.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

// codexCreateState makes the OAuth state token (pi: createState — 16 random
// bytes hex).
func codexCreateState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// codexParseAuthorizationInput parses a pasted redirect URL / code (pi:
// parseAuthorizationInput). Shared logic lives in parseAuthorizationInput
// (the anthropic port); the codex variant has the same URL/#/code=/bare
// branches.
func codexParseAuthorizationInput(input string) (code string, state string) {
	return parseAuthorizationInput(input)
}

// codexDecodeJWT parses one JWT's payload segment (pi: decodeJwt; pi's atob
// is forgiving base64, so both padded standard and unpadded URL-safe
// spellings decode).
func codexDecodeJWT(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	var (
		data []byte
		err  error
	)
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.RawURLEncoding, base64.URLEncoding,
	} {
		if data, err = encoding.DecodeString(payload); err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) != nil {
		return nil
	}
	return parsed
}

// codexStatusText extracts the reason phrase from resp.Status (pi:
// response.statusText).
func codexStatusText(response *http.Response) string {
	prefix := strconv.Itoa(response.StatusCode) + " "
	if text, ok := strings.CutPrefix(response.Status, prefix); ok {
		return text
	}
	return response.Status
}

// codexOAuthFetch runs one OAuth request, mapping transport failures during
// cancellation to the login-cancel message (pi: fetchWithLoginCancellation).
func codexOAuthFetch(ctx context.Context, method, requestURL, contentType, body string) (*http.Response, error) {
	response, err := codexOAuthDo(ctx, method, requestURL, contentType, body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	return response, nil
}

// codexReadResponseText drains the body (pi: response.text().catch(() => "")).
func codexReadResponseText(response *http.Response) string {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return ""
	}
	return string(body)
}

// codexReadTokenResponse validates one token endpoint response (pi:
// readTokenResponse).
func codexReadTokenResponse(response *http.Response, operation string) (codexOAuthToken, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return codexOAuthToken{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		text := string(body)
		if text == "" {
			text = codexStatusText(response)
		}
		return codexOAuthToken{}, fmt.Errorf("OpenAI Codex token %s failed (%d): %s", operation, response.StatusCode, text)
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return codexOAuthToken{}, err
	}
	obj, _ := parsed.(map[string]any)
	access, _ := obj["access_token"].(string)
	refresh, _ := obj["refresh_token"].(string)
	expiresIn, isNumber := obj["expires_in"].(float64)
	if access == "" || refresh == "" || !isNumber {
		encoded, _ := json.Marshal(parsed)
		return codexOAuthToken{}, fmt.Errorf("OpenAI Codex token %s response missing fields: %s", operation, string(encoded))
	}
	return codexOAuthToken{
		access:  access,
		refresh: refresh,
		expires: codexTimeNow().UnixMilli() + int64(expiresIn)*1000,
	}, nil
}

// codexEncodeForm renders one form post body (pi: new URLSearchParams);
// url.Values sorts keys where pi preserves insertion order — declared
// deviation, servers do not depend on form ordering (xai port precedent).
func codexEncodeForm(fields map[string]string) string {
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	return values.Encode()
}

// codexExchangeAuthorizationCode trades an authorization code for tokens
// (pi: exchangeAuthorizationCode).
func codexExchangeAuthorizationCode(ctx context.Context, code string, verifier string, redirectURI string) (codexOAuthToken, error) {
	response, err := codexOAuthFetch(ctx, http.MethodPost, codexTokenURL, "application/x-www-form-urlencoded", codexEncodeForm(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     codexOAuthClientID,
		"code":          code,
		"code_verifier": verifier,
		"redirect_uri":  redirectURI,
	}))
	if err != nil {
		return codexOAuthToken{}, err
	}
	return codexReadTokenResponse(response, "exchange")
}

// codexRefreshAccessToken exchanges the refresh token (pi:
// refreshAccessToken).
func codexRefreshAccessToken(ctx context.Context, refreshToken string) (codexOAuthToken, error) {
	response, err := codexOAuthDo(ctx, http.MethodPost, codexTokenURL, "application/x-www-form-urlencoded", codexEncodeForm(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     codexOAuthClientID,
	}))
	if err != nil {
		return codexOAuthToken{}, fmt.Errorf("OpenAI Codex token refresh error: %s", err.Error())
	}
	return codexReadTokenResponse(response, "refresh")
}

// codexStartDeviceAuth begins the device grant (pi: startOpenAICodexDeviceAuth).
func codexStartDeviceAuth(ctx context.Context) (*codexDeviceAuthInfo, error) {
	requestBody, err := json.Marshal(map[string]string{"client_id": codexOAuthClientID})
	if err != nil {
		return nil, err
	}
	response, err := codexOAuthFetch(ctx, http.MethodPost, codexDeviceUserCodeURL, "application/json", string(requestBody))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusNotFound {
			return nil, errors.New("OpenAI Codex device code login is not enabled for this server. Use browser login or verify the server URL.")
		}
		responseBody := codexReadResponseText(response)
		message := fmt.Sprintf("OpenAI Codex device code request failed with status %d", response.StatusCode)
		if responseBody != "" {
			message += ": " + responseBody
		}
		return nil, errors.New(message)
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(responseBody, &raw); err != nil {
		return nil, err
	}
	obj, _ := raw.(map[string]any)
	deviceAuthID, _ := obj["device_auth_id"].(string)
	userCode, _ := obj["user_code"].(string)
	// pi: typeof json?.interval === "string" ? Number(json.interval.trim()) :
	// json?.interval, valid when finite and >= 0 (JS Number("") is 0, so an
	// empty string stays valid).
	intervalSeconds, hasInterval := intervalNumber(obj["interval"])
	if deviceAuthID == "" || userCode == "" || !hasInterval || intervalSeconds < 0 {
		encoded, _ := json.Marshal(raw)
		return nil, fmt.Errorf("Invalid OpenAI Codex device code response: %s", string(encoded))
	}
	return &codexDeviceAuthInfo{
		deviceAuthID:    deviceAuthID,
		userCode:        userCode,
		intervalSeconds: intervalSeconds,
	}, nil
}

// intervalNumber mirrors pi's interval coercion: strings go through
// Number(trim) (empty string is 0), numbers pass through as float64; the
// bool reports a finite number (pi's typeof number check — NaN/Inf fail).
// Callers apply the >= 0 validation separately.
func intervalNumber(value any) (float64, bool) {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, true
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return parsed, !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	default:
		return 0, false
	}
}

// codexPollDeviceAuth polls the device token endpoint until the grant
// completes (pi: pollOpenAICodexDeviceAuth).
func codexPollDeviceAuth(ctx context.Context, device *codexDeviceAuthInfo) (*codexDeviceTokenSuccess, error) {
	requestBody, err := json.Marshal(struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
	}{device.deviceAuthID, device.userCode})
	if err != nil {
		return nil, err
	}
	return PollOAuthDeviceCodeFlow(DeviceCodePollOptions[*codexDeviceTokenSuccess]{
		// pi floors at seconds→ms; pig floors one step earlier (whole
		// seconds). Server intervals are whole seconds in practice.
		IntervalSeconds:  int64(device.intervalSeconds),
		ExpiresInSeconds: codexDeviceCodeTimeoutSeconds,
		Ctx:              ctx,
		Poll: func() DeviceCodePollResult[*codexDeviceTokenSuccess] {
			response, err := codexOAuthFetch(ctx, http.MethodPost, codexDeviceTokenURL, "application/json", string(requestBody))
			if err != nil {
				return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodeFailed, Message: err.Error()}
			}
			defer response.Body.Close()

			if response.StatusCode >= 200 && response.StatusCode < 300 {
				body, err := io.ReadAll(response.Body)
				if err != nil {
					return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodeFailed, Message: err.Error()}
				}
				var raw any
				if json.Unmarshal(body, &raw) != nil {
					raw = nil
				}
				obj, _ := raw.(map[string]any)
				authorizationCode, _ := obj["authorization_code"].(string)
				codeVerifier, _ := obj["code_verifier"].(string)
				if authorizationCode == "" || codeVerifier == "" {
					encoded, _ := json.Marshal(raw)
					return DeviceCodePollResult[*codexDeviceTokenSuccess]{
						Status:  DeviceCodeFailed,
						Message: fmt.Sprintf("Invalid OpenAI Codex device auth token response: %s", string(encoded)),
					}
				}
				return DeviceCodePollResult[*codexDeviceTokenSuccess]{
					Status: DeviceCodeComplete,
					Value:  &codexDeviceTokenSuccess{authorizationCode: authorizationCode, codeVerifier: codeVerifier},
				}
			}

			if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
				return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodePending}
			}

			responseBody := codexReadResponseText(response)
			errorCode := codexPollErrorCode(responseBody)
			switch errorCode {
			case "deviceauth_authorization_pending":
				return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodePending}
			case "slow_down":
				return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodeSlowDown}
			}
			message := fmt.Sprintf("OpenAI Codex device auth failed with status %d", response.StatusCode)
			if responseBody != "" {
				message += ": " + responseBody
			}
			return DeviceCodePollResult[*codexDeviceTokenSuccess]{Status: DeviceCodeFailed, Message: message}
		},
	})
}

// codexPollErrorCode extracts the poll error code (pi: the inline
// JSON.parse + error?.code walk; error may be a string or an object).
func codexPollErrorCode(responseBody string) string {
	if responseBody == "" {
		return ""
	}
	var parsed struct {
		Error any `json:"error"`
	}
	if json.Unmarshal([]byte(responseBody), &parsed) != nil {
		return ""
	}
	switch err := parsed.Error.(type) {
	case string:
		return err
	case map[string]any:
		code, _ := err["code"].(string)
		return code
	default:
		return ""
	}
}

// codexCreateAuthorizationFlow builds the browser authorization URL (pi:
// createAuthorizationFlow). pi's URLSearchParams preserves insertion order;
// url.Values sorts — declared deviation.
func codexCreateAuthorizationFlow(originator string) (verifier string, state string, authURL string, err error) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return "", "", "", err
	}
	if originator == "" {
		originator = "pi"
	}
	if state, err = codexCreateState(); err != nil {
		return "", "", "", err
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", codexOAuthClientID)
	params.Set("redirect_uri", codexRedirectURI)
	params.Set("scope", codexScope)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	params.Set("originator", originator)
	return verifier, state, codexAuthorizeURL + "?" + params.Encode(), nil
}

// codexGetAccountID pulls the ChatGPT account id from the token's JWT
// payload (pi: getAccountId).
func codexGetAccountID(accessToken string) string {
	payload := codexDecodeJWT(accessToken)
	if payload == nil {
		return ""
	}
	auth, _ := payload[codexJWTClaimPath].(map[string]any)
	if auth == nil {
		return ""
	}
	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID
}

// codexCredentialsFromToken builds the stored credential (pi:
// credentialsFromToken). The accountId rides OAuthCredential.Extra so it
// round-trips through auth.json (pi: a named OAuthCredential field).
func codexCredentialsFromToken(token codexOAuthToken) (*OAuthCredential, error) {
	accountID := codexGetAccountID(token.access)
	if accountID == "" {
		return nil, errors.New("Failed to extract accountId from token")
	}
	return &OAuthCredential{
		OAuthCredentials: OAuthCredentials{
			Refresh: token.refresh,
			Access:  token.access,
			Expires: token.expires,
			Extra:   map[string]any{"accountId": accountID},
		},
	}, nil
}

// codexExchangeCredentials exchanges a code and derives the credential (pi:
// exchangeAuthorizationCodeForCredentials).
func codexExchangeCredentials(ctx context.Context, code string, verifier string, redirectURI string) (*OAuthCredential, error) {
	token, err := codexExchangeAuthorizationCode(ctx, code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	return codexCredentialsFromToken(token)
}

// codexLoginDeviceCode runs the headless device flow (pi:
// loginOpenAICodexDeviceCode).
func codexLoginDeviceCode(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error) {
	device, err := codexStartDeviceAuth(ctx)
	if err != nil {
		return nil, err
	}
	interaction.Notify(AuthEvent{
		Type:             "device_code",
		UserCode:         device.userCode,
		VerificationURI:  codexDeviceVerificationURI,
		IntervalSeconds:  lo.ToPtr(int64(device.intervalSeconds)),
		ExpiresInSeconds: lo.ToPtr(codexDeviceCodeTimeoutSeconds),
	})
	code, err := codexPollDeviceAuth(ctx, device)
	if err != nil {
		return nil, err
	}
	return codexExchangeCredentials(ctx, code.authorizationCode, code.codeVerifier, codexDeviceRedirectURI)
}

// codexLoginBrowser runs the browser flow: a shared loopback callback server
// racing a manual-code prompt (pi: loginOpenAICodex). Port 1455 is shared
// with the Codex CLI; when it is taken, fall back to the pasted redirect URL
// (pi: .catch(() => undefined)).
func codexLoginBrowser(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	verifier, state, authURL, err := codexCreateAuthorizationFlow("pi")
	if err != nil {
		return nil, err
	}
	var callback *OAuthCallbackServer[string]
	if server, serverErr := StartOAuthCallbackServer[string](OAuthCallbackServerOptions[string]{
		ProviderName: "OpenAI",
		Host:         getCodexCallbackHost(),
		Port:         codexOAuthCallbackPort,
		Path:         codexOAuthCallbackPath,
		State:        state,
		Complete:     func(_ context.Context, callbackCode string) (string, error) { return callbackCode, nil },
		Ctx:          ctx,
	}); serverErr == nil {
		callback = server
		defer callback.Close()
	}

	interaction.Notify(AuthEvent{
		Type:         "auth_url",
		URL:          authURL,
		Instructions: "A browser window should open. Complete login to finish.",
	})

	result, err := WaitForCallbackOrManualInput[string](interaction, callback,
		"Complete login in your browser, or paste the authorization code / redirect URL here:", codexRedirectURI)
	if err != nil {
		return nil, err
	}
	var code string
	if result.Type == "callback" {
		code = result.Value
	} else {
		parsedCode, parsedState := codexParseAuthorizationInput(result.Input)
		if parsedState != "" && parsedState != state {
			return nil, errors.New("State mismatch")
		}
		code = parsedCode
	}

	if code == "" {
		return nil, errors.New("Missing authorization code")
	}
	return codexExchangeCredentials(ctx, code, verifier, codexRedirectURI)
}

// OpenAICodexOAuth is the OpenAI Codex (ChatGPT Plus/Pro) OAuth auth (pi:
// openaiCodexOAuth). pi has no loginLabel; LoginLabel stays empty.
type OpenAICodexOAuth struct{}

func (OpenAICodexOAuth) Name() string         { return "OpenAI (ChatGPT Plus/Pro)" }
func (OpenAICodexOAuth) IsSubscription() bool { return true }
func (OpenAICodexOAuth) LoginLabel() string   { return "" }

// Login runs the provider-owned login flow, dispatching on the method
// select (pi: login).
func (OpenAICodexOAuth) Login(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error) {
	method, err := interaction.Prompt(AuthPrompt{
		Type:    "select",
		Message: "Select OpenAI Codex login method:",
		Options: []AuthPromptOption{
			{ID: codexBrowserLoginMethod, Label: "Browser login (default)"},
			{ID: codexDeviceCodeLoginMethod, Label: "Device code login (headless)"},
		},
	})
	if err != nil {
		return nil, err
	}
	switch method {
	case codexDeviceCodeLoginMethod:
		return codexLoginDeviceCode(ctx, interaction)
	case codexBrowserLoginMethod:
		return codexLoginBrowser(ctx, interaction, options)
	default:
		return nil, fmt.Errorf("Unknown OpenAI Codex login method: %s", method)
	}
}

// Refresh exchanges the refresh token (pi: refreshOpenAICodexToken).
func (OpenAICodexOAuth) Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error) {
	token, err := codexRefreshAccessToken(ctx, credential.Refresh)
	if err != nil {
		return nil, err
	}
	return codexCredentialsFromToken(token)
}

// ToAuth derives request auth from a valid credential (pi: toAuth — the
// access token authenticates as the API key).
func (OpenAICodexOAuth) ToAuth(credential *OAuthCredential) (*ModelAuth, error) {
	return &ModelAuth{APIKey: credential.Access}, nil
}
