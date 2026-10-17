// pi: src/core/auth-guidance.ts — auth/setup guidance messages.
//
// Segment deviation: pi's getProviderLoginHelp lists docs/providers.md and
// docs/models.md under the package dir; those files do not exist in the pig
// repo (the pi docs were not ported), so the path lines are dropped.
package core

const unknownProvider = "unknown"

// GetProviderLoginHelp returns the provider login guidance (pi:
// getProviderLoginHelp).
func GetProviderLoginHelp() string {
	return "Use /login to log into a provider via OAuth or API key."
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
