// pi: test/openrouter-oauth.test.ts — the models integration pair. External
// test package: it crosses into package ai (which imports auth).
package auth_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/providers"
)

func TestOpenRouterOAuthResolvesStoredKeyForChatAndImageModels(t *testing.T) {
	credentials := &auth.InMemoryCredentialStore{}
	if _, err := credentials.Modify(context.Background(), "openrouter", func(auth.Credential) (auth.Credential, error) {
		return &auth.OAuthCredential{OAuthCredentials: auth.OAuthCredentials{
			Access:  "sk-or-stored",
			Refresh: "",
			Expires: 1<<63 - 1,
		}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	models := ai.NewModels(&ai.CreateModelsOptions{Credentials: credentials})
	models.SetProvider(providers.OpenRouterProvider())

	chatModels := models.GetModels()
	if len(chatModels) == 0 {
		t.Fatal("no chat models on the openrouter builtin")
	}
	var imageModel *ai.Model
	for i := range models.GetModelsOfType(ai.ModelTypeImage) {
		candidate := &models.GetModelsOfType(ai.ModelTypeImage)[i]
		if candidate.Provider == ai.ProviderOpenRouter {
			imageModel = candidate
			break
		}
	}
	if imageModel == nil {
		t.Fatal("no image model on the openrouter builtin")
	}

	chatAuth, err := models.GetAuthForModel(context.Background(), &chatModels[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	imageAuth, err := models.GetAuthForModel(context.Background(), imageModel, nil)
	if err != nil {
		t.Fatal(err)
	}
	if chatAuth.Auth.APIKey != "sk-or-stored" {
		t.Errorf("chat auth key = %q", chatAuth.Auth.APIKey)
	}
	if imageAuth.Auth.APIKey != "sk-or-stored" {
		t.Errorf("image auth key = %q", imageAuth.Auth.APIKey)
	}
}

func TestOpenRouterProviderExposesOAuthAlongsideAPIKey(t *testing.T) {
	provider := providers.OpenRouterProvider()
	authSurface := provider.Auth()
	if authSurface.APIKey == nil {
		t.Error("openrouter has no api_key auth")
	}
	if authSurface.OAuth == nil {
		t.Fatal("openrouter has no oauth auth")
	}
	if !strings.Contains(authSurface.OAuth.LoginLabel(), "OpenRouter") {
		t.Errorf("loginLabel = %q", authSurface.OAuth.LoginLabel())
	}
}
