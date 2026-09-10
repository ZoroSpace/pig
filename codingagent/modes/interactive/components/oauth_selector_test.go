// oauth_selector_test.go (pi: test/oauth-selector.test.ts — the render /
// status-indicator cases; the getLoginProviderOptions projection case lives
// in the interactive package where the method is defined).
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func newTestOAuthSelector(t *testing.T, providers []AuthSelectorProvider) *OAuthSelectorComponent {
	t.Helper()
	return NewOAuthSelectorComponent("login", providers, func(string, auth.AuthType) {}, func() {}, "")
}

func TestOAuthSelectorRendersUnconfiguredWithoutCheckmark(t *testing.T) {
	initTestTheme(t)

	selector := newTestOAuthSelector(t, []AuthSelectorProvider{
		{ID: "google", Name: "Google", AuthType: auth.AuthTypeAPIKey, Status: nil},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "unconfigured") {
		t.Fatalf("output = %q, want it to contain %q", output, "unconfigured")
	}
	if strings.Contains(output, "✓ configured") {
		t.Fatalf("output = %q, want no %q", output, "✓ configured")
	}
}

func TestOAuthSelectorShowsOAuthAuthDistinctlyInAPIKeySelector(t *testing.T) {
	initTestTheme(t)

	selector := newTestOAuthSelector(t, []AuthSelectorProvider{
		{ID: "anthropic", Name: "Anthropic", AuthType: auth.AuthTypeAPIKey, Status: &auth.AuthCheck{Type: auth.AuthTypeOAuth, Source: "OAuth"}},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "subscription configured") {
		t.Fatalf("output = %q, want it to contain %q", output, "subscription configured")
	}
}

func TestOAuthSelectorShowsEnvironmentAPIKeyAuthAsConfigured(t *testing.T) {
	initTestTheme(t)

	selector := newTestOAuthSelector(t, []AuthSelectorProvider{
		{ID: "openai", Name: "OpenAI", AuthType: auth.AuthTypeAPIKey, Status: &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "OPENAI_API_KEY"}},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "✓ env: OPENAI_API_KEY") {
		t.Fatalf("output = %q, want it to contain %q", output, "✓ env: OPENAI_API_KEY")
	}
	if strings.Contains(output, "unconfigured") {
		t.Fatalf("output = %q, want no %q", output, "unconfigured")
	}
}

func TestOAuthSelectorShowsModelsJSONAPIKeyAuthAsConfigured(t *testing.T) {
	initTestTheme(t)

	selector := newTestOAuthSelector(t, []AuthSelectorProvider{
		{ID: "local-proxy", Name: "local-proxy", AuthType: auth.AuthTypeAPIKey, Status: &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "key in models.json"}},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "✓ key in models.json") {
		t.Fatalf("output = %q, want it to contain %q", output, "✓ key in models.json")
	}
}

func TestOAuthSelectorShowsModelsJSONCommandAuthAsConfigured(t *testing.T) {
	initTestTheme(t)

	selector := newTestOAuthSelector(t, []AuthSelectorProvider{
		{ID: "op-proxy", Name: "op-proxy", AuthType: auth.AuthTypeAPIKey, Status: &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "command in models.json"}},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "✓ command in models.json") {
		t.Fatalf("output = %q, want it to contain %q", output, "✓ command in models.json")
	}
}

// formatAuthTypeLabelFormat keeps FormatAuthSelectorProviderType locked (pi:
// formatAuthSelectorProviderType).
func TestFormatAuthSelectorProviderType(t *testing.T) {
	if got := FormatAuthSelectorProviderType(auth.AuthTypeOAuth); got != "subscription" {
		t.Fatalf("oauth label = %q, want subscription", got)
	}
	if got := FormatAuthSelectorProviderType(auth.AuthTypeAPIKey); got != "API key" {
		t.Fatalf("api_key label = %q, want API key", got)
	}
}
