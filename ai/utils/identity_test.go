package utils

import (
	"runtime"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func testAttribution(name, url string, categories []string) *types.ClientAttribution {
	return &types.ClientAttribution{Name: name, URL: url, Categories: categories}
}

func testIdentity(attribution *types.ClientAttribution) *types.ClientIdentity {
	identity := &types.ClientIdentity{}
	if attribution != nil {
		identity.Attribution = &types.AttributionSetting{Attribution: attribution}
	}
	return identity
}

func headerValue(t *testing.T, headers types.ProviderHeaders, key string) string {
	t.Helper()
	value := headers[key]
	if value == nil {
		t.Fatalf("header %q is absent", key)
	}
	return *value
}

func TestFormatUserAgent(t *testing.T) {
	defaultUA := FormatUserAgent(nil)
	if !strings.HasPrefix(defaultUA, "pi/"+PiAIVersion+" (") {
		t.Fatalf("default user agent = %q", defaultUA)
	}
	if !strings.Contains(defaultUA, runtime.GOOS) || !strings.Contains(defaultUA, runtime.GOARCH) {
		t.Fatalf("default user agent = %q, want platform and arch", defaultUA)
	}
	if got := FormatUserAgent(&types.ClientIdentity{Version: "9.9.9"}); !strings.HasPrefix(got, "pi/9.9.9 (") {
		t.Fatalf("versioned user agent = %q", got)
	}
	if got := FormatUserAgent(&types.ClientIdentity{UserAgent: types.UserAgentShort()}); got != "pi" {
		t.Fatalf("short user agent = %q", got)
	}
	if got := FormatUserAgent(&types.ClientIdentity{UserAgent: types.UserAgentCustom("acme/1.0")}); got != "acme/1.0" {
		t.Fatalf("custom user agent = %q", got)
	}
}

func TestMergeClientIdentity(t *testing.T) {
	if got := MergeClientIdentity(nil, nil); got != nil {
		t.Fatalf("merge(nil, nil) = %+v, want nil", got)
	}
	merged := MergeClientIdentity(
		&types.ClientIdentity{Attribution: &types.AttributionSetting{Attribution: &types.PIAttribution}, UserAgent: types.UserAgentShort()},
		&types.ClientIdentity{Version: "1.0.0"},
	)
	if merged == nil || merged.UserAgent == nil || !merged.UserAgent.Short || merged.Version != "1.0.0" {
		t.Fatalf("merged = %+v", merged)
	}
	if merged.Attribution == nil || merged.Attribution.Attribution == nil || merged.Attribution.Attribution.Name != "pi" {
		t.Fatalf("attribution = %+v", merged.Attribution)
	}
	off := MergeClientIdentity(
		&types.ClientIdentity{Attribution: &types.AttributionSetting{Attribution: &types.PIAttribution}},
		&types.ClientIdentity{Attribution: &types.AttributionSetting{}},
	)
	if off.Attribution == nil || off.Attribution.Attribution != nil {
		t.Fatalf("null override = %+v", off.Attribution)
	}
}

func TestGetAttributionHeaders(t *testing.T) {
	openRouter := types.ClientIdentityTarget{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}
	if headers := GetAttributionHeaders(openRouter, nil); len(headers) != 0 {
		t.Fatalf("no identity headers = %v", headers)
	}
	if headers := GetAttributionHeaders(openRouter, testIdentity(nil)); len(headers) != 0 {
		t.Fatalf("no attribution headers = %v", headers)
	}
	headers := GetAttributionHeaders(openRouter, testIdentity(&types.PIAttribution))
	if got := headerValue(t, headers, "HTTP-Referer"); got != "https://pi.dev" {
		t.Fatalf("HTTP-Referer = %q", got)
	}
	if got := headerValue(t, headers, "X-OpenRouter-Title"); got != "pi" {
		t.Fatalf("X-OpenRouter-Title = %q", got)
	}
	if got := headerValue(t, headers, "X-OpenRouter-Categories"); got != "cli-agent" {
		t.Fatalf("X-OpenRouter-Categories = %q", got)
	}

	custom := GetAttributionHeaders(openRouter, testIdentity(testAttribution("acme", "https://acme.example", []string{"cli-agent", "cloud-agent"})))
	if got := headerValue(t, custom, "X-OpenRouter-Categories"); got != "cli-agent,cloud-agent" {
		t.Fatalf("categories = %q", got)
	}
	noCategories := GetAttributionHeaders(openRouter, testIdentity(testAttribution("acme", "https://acme.example", nil)))
	if _, ok := noCategories["X-OpenRouter-Categories"]; ok {
		t.Fatal("empty categories should not add the header")
	}
	legacy := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "custom", BaseURL: "not-a-url-openrouter.ai"}, testIdentity(&types.PIAttribution))
	if got := headerValue(t, legacy, "X-OpenRouter-Title"); got != "pi" {
		t.Fatalf("legacy title = %q", got)
	}

	nvidia := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "nvidia", BaseURL: "https://example.test/v1"}, testIdentity(&types.PIAttribution))
	if got := headerValue(t, nvidia, "X-BILLING-INVOKE-ORIGIN"); got != "Pi" {
		t.Fatalf("nvidia origin = %q", got)
	}
	customNim := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "custom-nim", BaseURL: "https://integrate.api.nvidia.com/v1"}, testIdentity(testAttribution("acme", "https://acme.example", nil)))
	if got := headerValue(t, customNim, "X-BILLING-INVOKE-ORIGIN"); got != "Acme" {
		t.Fatalf("nim origin = %q", got)
	}
	gateway := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}, testIdentity(&types.PIAttribution))
	if _, ok := gateway["X-BILLING-INVOKE-ORIGIN"]; ok {
		t.Fatal("OpenRouter must not receive NVIDIA attribution")
	}

	openCode := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "opencode", BaseURL: "https://opencode.ai/zen/v1"}, testIdentity(&types.PIAttribution))
	if got := headerValue(t, openCode, "x-opencode-client"); got != "pi" {
		t.Fatalf("opencode client = %q", got)
	}
	customOpenCode := GetAttributionHeaders(types.ClientIdentityTarget{Provider: "custom", BaseURL: "https://opencode.ai/zen/v1"}, testIdentity(testAttribution("acme", "https://acme.example", nil)))
	if got := headerValue(t, customOpenCode, "x-opencode-client"); got != "acme" {
		t.Fatalf("opencode custom client = %q", got)
	}
}

func TestGetIdentityHeadersAndOpenCodeSession(t *testing.T) {
	target := types.ClientIdentityTarget{Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}
	headers := GetIdentityHeaders(target, testIdentity(&types.PIAttribution))
	if got := headerValue(t, headers, "User-Agent"); got != FormatUserAgent(testIdentity(&types.PIAttribution)) {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := headerValue(t, headers, "X-OpenRouter-Title"); got != "pi" {
		t.Fatalf("X-OpenRouter-Title = %q", got)
	}

	session := GetOpenCodeSessionHeaders(types.ClientIdentityTarget{Provider: "custom-opencode", BaseURL: "https://opencode.ai/zen/v1"}, "session-1")
	if got := headerValue(t, session, "x-opencode-session"); got != "session-1" {
		t.Fatalf("x-opencode-session = %q", got)
	}
	if session := GetOpenCodeSessionHeaders(target, "session-1"); session != nil {
		t.Fatalf("non-OpenCode session headers = %v", session)
	}
	if session := GetOpenCodeSessionHeaders(types.ClientIdentityTarget{Provider: "opencode"}, ""); session != nil {
		t.Fatalf("empty session headers = %v", session)
	}
}
