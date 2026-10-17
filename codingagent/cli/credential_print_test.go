// Ported from pi's test/credential-print.test.ts. The runtime fixture mirrors
// print_mode_test.go: built-in provider catalogs over an in-memory credential
// store, with models.json disabled so no network access happens.
package cli

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/core"
)

func credentialPrintStrPtr(v string) *string { return &v }

func credentialPrintRuntime(t *testing.T, credentials map[string]string) *core.ModelRuntime {
	t.Helper()
	storage := core.NewInMemoryAuthStorageWithCredentials(credentials)
	for provider, key := range credentials {
		if _, err := storage.Modify(context.Background(), provider, func(auth.Credential) (auth.Credential, error) {
			return &auth.ApiKeyCredential{Key: key}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials: storage,
		ModelsPath:  credentialPrintStrPtr(""),
	})
}

func TestResolveCredentialForPrint(t *testing.T) {
	t.Run("prints a resolved API key", func(t *testing.T) {
		runtime := credentialPrintRuntime(t, map[string]string{"openai": "test-api-key"})
		value, err := ResolveCredentialForPrint(context.Background(), &Args{
			Provider: credentialPrintStrPtr("openai"),
		}, runtime, AuthCommandAPIKey, nil)
		if err != nil {
			t.Fatal(err)
		}
		if value != "test-api-key" {
			t.Fatalf("value = %q", value)
		}
	})

	t.Run("resolves --model without --provider only among catalog models of configured providers", func(t *testing.T) {
		runtime := credentialPrintRuntime(t, map[string]string{
			"openai":    "openai-test-key",
			"anthropic": "anthropic-test-key",
		})
		// anthropic has no gpt-5.5; an uncatalogued id on a configured provider
		// must not count as a match.
		value, err := ResolveCredentialForPrint(context.Background(), &Args{
			Model: credentialPrintStrPtr("gpt-5.5"),
		}, runtime, AuthCommandAPIKey, nil)
		if err != nil {
			t.Fatal(err)
		}
		if value != "openai-test-key" {
			t.Fatalf("value = %q", value)
		}
	})
}
