// pi: src/compat/extension-oauth-types.ts — legacy extension OAuth prompt and
// callback surface, retained only for coding-agent extension compatibility.
package ai

import "github.com/ZoroSpace/pig/ai/auth"

// OAuthPrompt is the legacy extension OAuth prompt (pi: OAuthPrompt).
type OAuthPrompt struct {
	Message     string
	Placeholder string
	AllowEmpty  bool
}

// OAuthAuthInfo is the legacy authorization-link notification (pi:
// OAuthAuthInfo).
type OAuthAuthInfo struct {
	URL          string
	Instructions string
}

// OAuthDeviceCodeInfo is the legacy device-code notification (pi:
// OAuthDeviceCodeInfo).
type OAuthDeviceCodeInfo struct {
	UserCode         string
	VerificationURI  string
	IntervalSeconds  *int64
	ExpiresInSeconds *int64
}

// OAuthSelectOption is one select option (pi: OAuthSelectOption).
type OAuthSelectOption struct {
	ID    string
	Label string
}

// OAuthSelectPrompt is the legacy select prompt (pi: OAuthSelectPrompt).
type OAuthSelectPrompt struct {
	Message string
	Options []OAuthSelectOption
}

// OAuthLoginCallbacks is the callback surface handed to extension OAuth
// login flows (pi: OAuthLoginCallbacks). pi's optional signal member has no
// counterpart: pig's AuthInteraction carries cancellation at the interaction
// level. Promise-returning members become (value, error).
type OAuthLoginCallbacks struct {
	OnAuth       func(info OAuthAuthInfo)
	OnDeviceCode func(info OAuthDeviceCodeInfo)
	// OnPrompt shows a text prompt and returns the entered value.
	OnPrompt func(prompt OAuthPrompt) (string, error)
	// OnProgress delivers a non-blocking progress message.
	OnProgress func(message string)
	// OnManualCodeInput asks the user to paste an authorization code.
	OnManualCodeInput func() (string, error)
	// OnSelect shows a select prompt; an empty string means the user
	// cancelled (pi: Promise<string | undefined>).
	OnSelect func(prompt OAuthSelectPrompt) (string, error)
}

// ExtensionOAuthConfig consumer contract: the coding-agent composer adapts
// these callbacks onto auth.AuthInteraction (pi: adaptOAuth in
// provider-composer.ts). Declared here so the ai-level OAuth credential type
// stays the single import for extension authors.
type ExtensionOAuthLogin = func(callbacks OAuthLoginCallbacks) (auth.OAuthCredentials, error)
