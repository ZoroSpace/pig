// pi: test/github-copilot-oauth.test.ts — the Models-integration filter cases
// (getAvailable restricted to the credential's availableModelIds). The
// login-flow cases live in ai/auth (transport seam + fake clock are package
// private there); the login→store persistence pi pins is pig's generic
// Models.Login → store.Modify plumbing, covered by the model-runtime
// credential-sync tests.
package providers

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

// copilotCredential builds an OAuth credential carrying the given
// availableModelIds (pi: {...credentials, type: "oauth"}).
func copilotCredential(modelIDs []string) *auth.OAuthCredential {
	anyIDs := make([]any, len(modelIDs))
	for i, id := range modelIDs {
		anyIDs[i] = id
	}
	return &auth.OAuthCredential{
		OAuthCredentials: auth.OAuthCredentials{
			Refresh: "ghu_refresh_token",
			Access:  "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;",
			Expires: 9999999999000,
			Extra:   map[string]any{"availableModelIds": anyIDs},
		},
	}
}

func TestGitHubCopilotModelsFilteredToAccountCatalog(t *testing.T) {
	provider := GitHubCopilotProvider()
	catalog := provider.GetModels()
	if len(catalog) < 3 {
		t.Skipf("github-copilot catalog not populated (%d models)", len(catalog))
	}
	pickerModelID := catalog[0].ID
	disabledModelID := catalog[1].ID
	hiddenModelID := catalog[2].ID

	store := &auth.InMemoryCredentialStore{}
	if _, err := store.Modify(context.Background(), "github-copilot", func(auth.Credential) (auth.Credential, error) {
		return copilotCredential([]string{pickerModelID}), nil
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}
	models := BuiltinModels(&ai.CreateModelsOptions{Credentials: store})
	available, err := models.GetAvailable(context.Background(), "github-copilot")
	if err != nil {
		t.Fatalf("getAvailable: %v", err)
	}
	var ids []string
	for _, model := range available {
		ids = append(ids, model.ID)
	}
	if len(ids) != 1 || ids[0] != pickerModelID {
		t.Errorf("available = %v, want [%s] (filtered out %s, %s)", ids, pickerModelID, disabledModelID, hiddenModelID)
	}
}

func TestGitHubCopilotModelsEmptyAccountCatalogFiltersAll(t *testing.T) {
	provider := GitHubCopilotProvider()
	catalog := provider.GetModels()
	if len(catalog) < 1 {
		t.Skip("github-copilot catalog not populated")
	}
	// A well-formed but empty list means the account can use nothing.
	store := &auth.InMemoryCredentialStore{}
	if _, err := store.Modify(context.Background(), "github-copilot", func(auth.Credential) (auth.Credential, error) {
		return copilotCredential([]string{}), nil
	}); err != nil {
		t.Fatalf("modify: %v", err)
	}
	models := BuiltinModels(&ai.CreateModelsOptions{Credentials: store})
	available, err := models.GetAvailable(context.Background(), "github-copilot")
	if err != nil {
		t.Fatalf("getAvailable: %v", err)
	}
	if len(available) != 0 {
		t.Errorf("available = %d models, want 0 for an empty account catalog", len(available))
	}
}
