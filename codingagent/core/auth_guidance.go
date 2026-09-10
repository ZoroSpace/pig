// pi: src/core/auth-guidance.ts — auth/setup guidance messages.
package core

import (
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/codingagent"
)

const unknownProvider = "unknown"

// GetProviderLoginHelp returns the provider login guidance (pi:
// getProviderLoginHelp).
func GetProviderLoginHelp() string {
	return strings.Join([]string{
		"Use /login to log into a provider via OAuth or API key. See:",
		"  " + filepath.Join(codingagent.GetDocsPath(), "providers.md"),
		"  " + filepath.Join(codingagent.GetDocsPath(), "models.md"),
	}, "\n")
}

// FormatNoModelsAvailableMessage formats the no-models guidance (pi:
// formatNoModelsAvailableMessage).
func FormatNoModelsAvailableMessage() string {
	return "No models available. " + GetProviderLoginHelp()
}

// FormatNoModelSelectedMessage formats the no-model-selected guidance (pi:
// formatNoModelSelectedMessage).
func FormatNoModelSelectedMessage() string {
	return "No model selected.\n\n" + GetProviderLoginHelp() + "\n\nThen use /model to select a model."
}

// FormatNoApiKeyFoundMessage formats the missing-API-key guidance (pi:
// formatNoApiKeyFoundMessage).
func FormatNoApiKeyFoundMessage(provider string) string {
	providerDisplay := provider
	if provider == unknownProvider {
		providerDisplay = "the selected model"
	}
	return "No API key found for " + providerDisplay + ".\n\n" + GetProviderLoginHelp()
}
