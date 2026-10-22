package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestCreateClientIdentityHeaders(t *testing.T) {
	model := &types.Model{
		ID:       "identity-model",
		API:      types.ApiOpenAICompletions,
		Provider: "openrouter",
		BaseURL:  "https://openrouter.ai/api/v1",
		Headers:  map[string]string{"X-OpenRouter-Title": "model-title"},
	}
	identity := &types.ClientIdentity{
		UserAgent:   types.UserAgentShort(),
		Attribution: &types.AttributionSetting{Attribution: &types.PIAttribution},
	}
	nullReferer := (*string)(nil)
	client := createClient(model, utils.NormalizeContext(&types.Context{}), "key",
		types.ProviderHeaders{"HTTP-Referer": nullReferer}, identity, nil, "", nil)

	if got := client.headers["User-Agent"]; got != "pi" {
		t.Fatalf("User-Agent = %q, want pi", got)
	}
	if got := client.headers["X-OpenRouter-Title"]; got != "model-title" {
		t.Fatalf("X-OpenRouter-Title = %q, want model override", got)
	}
	if _, ok := client.headers["HTTP-Referer"]; ok {
		t.Fatal("nil request header should suppress the identity header")
	}
	if got := client.headers["X-OpenRouter-Categories"]; got != "cli-agent" {
		t.Fatalf("X-OpenRouter-Categories = %q", got)
	}
}

func TestCreateClientIdentityKeepsImpersonatingUserAgent(t *testing.T) {
	model := &types.Model{
		ID:       "identity-model",
		API:      types.ApiOpenAICompletions,
		Provider: "github-copilot",
		BaseURL:  "https://api.githubcopilot.com",
		Headers:  map[string]string{"User-Agent": "GitHubCopilotChat/0.35.0"},
	}
	identity := &types.ClientIdentity{UserAgent: types.UserAgentCustom("acme")}
	client := createClient(model, utils.NormalizeContext(&types.Context{}), "key", nil, identity, nil, "", nil)
	if got := client.headers["User-Agent"]; got != "GitHubCopilotChat/0.35.0" {
		t.Fatalf("User-Agent = %q, want model override", got)
	}
}
