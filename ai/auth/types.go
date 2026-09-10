// pi: auth/types.ts.
package auth

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ZoroSpace/pig/ai/types"
)

// ModelAuth is the request auth for a single model request. If a value cannot
// be expressed as APIKey, Headers, or BaseURL, it is provider config, not
// auth.
type ModelAuth struct {
	APIKey  string                `json:"apiKey,omitempty"`
	Headers types.ProviderHeaders `json:"headers,omitempty"`
	BaseURL string                `json:"baseUrl,omitempty"`
}

// CredentialType discriminates stored credentials.
type CredentialType string

const (
	CredentialTypeAPIKey CredentialType = "api_key"
	CredentialTypeOAuth  CredentialType = "oauth"
)

// Credential is one type-tagged credential per provider — the shape of
// today's auth.json. Concrete types: ApiKeyCredential, OAuthCredential.
type Credential interface {
	CredentialType() CredentialType
}

// ApiKeyCredential is a stored api-key credential. Env holds provider-scoped
// environment/config values such as Cloudflare account/gateway ids.
type ApiKeyCredential struct {
	Key string            `json:"key,omitempty"`
	Env types.ProviderEnv `json:"env,omitempty"`
}

// OAuthCredentials is the OAuth token data returned by extension
// compatibility flows; Extra preserves further token fields on roundtrip.
type OAuthCredentials struct {
	Refresh string `json:"refresh"`
	Access  string `json:"access"`
	// Expires is a Unix timestamp in milliseconds.
	Expires int64          `json:"expires"`
	Extra   map[string]any `json:"-"`
}

// OAuthCredential is a stored canonical OAuth credential.
type OAuthCredential struct {
	OAuthCredentials
}

func (*ApiKeyCredential) CredentialType() CredentialType { return CredentialTypeAPIKey }
func (*OAuthCredential) CredentialType() CredentialType  { return CredentialTypeOAuth }

func (c *ApiKeyCredential) MarshalJSON() ([]byte, error) {
	type alias ApiKeyCredential
	return json.Marshal(struct {
		Type CredentialType `json:"type"`
		*alias
	}{CredentialTypeAPIKey, (*alias)(c)})
}

func (c *ApiKeyCredential) UnmarshalJSON(data []byte) error {
	type alias ApiKeyCredential
	aux := struct {
		Type CredentialType `json:"type"`
		*alias
	}{alias: (*alias)(c)}
	return json.Unmarshal(data, &aux)
}

func (c *OAuthCredential) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(c.OAuthCredentials)
	if err != nil {
		return nil, err
	}
	merged := map[string]json.RawMessage{"type": json.RawMessage(`"oauth"`)}
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range c.Extra {
		enc, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		merged[k] = enc
	}
	return json.Marshal(merged)
}

func (c *OAuthCredential) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	known := map[string]struct{}{"type": {}, "refresh": {}, "access": {}, "expires": {}}
	filtered := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if _, ok := known[k]; ok {
			filtered[k] = v
			continue
		}
		var anyValue any
		if err := json.Unmarshal(v, &anyValue); err != nil {
			return err
		}
		if c.Extra == nil {
			c.Extra = map[string]any{}
		}
		c.Extra[k] = anyValue
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, &c.OAuthCredentials)
}

// UnmarshalCredential decodes one credential, dispatching on its "type"
// field.
func UnmarshalCredential(data []byte) (Credential, error) {
	var probe struct {
		Type CredentialType `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case CredentialTypeAPIKey:
		c := new(ApiKeyCredential)
		return c, json.Unmarshal(data, c)
	case CredentialTypeOAuth:
		c := new(OAuthCredential)
		return c, json.Unmarshal(data, c)
	default:
		return nil, fmt.Errorf("ai: unknown credential type %q", probe.Type)
	}
}

// CredentialInfo is non-secret credential metadata for account/status
// enumeration.
type CredentialInfo struct {
	ProviderID string         `json:"providerId"`
	Type       CredentialType `json:"type"`
}

// AuthOperationOptions carried cancellation in pi; pig passes a Go context at
// call sites instead.
type AuthOperationOptions struct{}

// CredentialStore is app-owned credential storage, keyed by provider id, one
// credential per provider. Modify is the only write path, so every mutation
// is a serialized read-modify-write; Models runs OAuth refresh inside Modify
// so concurrent requests cannot double-refresh a rotated token. The app
// persists a credential after login via
// Modify(providerID, func(Credential) (Credential, error)).
//
// Error semantics: Read returns (nil, nil) for missing entries. Methods fail
// only on storage failure; Models wraps such errors in a ModelsError with
// code "auth". Best-effort stores that serve an in-memory view and record
// persistence errors internally (like coding-agent's AuthStorage) are valid
// implementations.
type CredentialStore interface {
	// Read the stored credential, possibly expired. Display/status use;
	// resolved request auth comes from Models.GetAuth.
	Read(ctx context.Context, providerID string) (Credential, error)
	// List stored credential metadata without resolving or exposing secrets.
	// Implementations must not execute configured API-key commands while
	// listing.
	List(ctx context.Context) ([]CredentialInfo, error)
	// Modify is the serialized write path. fn sees the current credential
	// because correct writes (refresh, login-during-refresh) depend on it;
	// return the new credential, or nil to leave the entry unchanged.
	// Mutual exclusion is per provider id, cross-process too where the
	// backing store supports it (e.g. a file lock). Returns the post-write
	// credential; fn errors propagate.
	Modify(ctx context.Context, providerID string, fn func(current Credential) (Credential, error)) (Credential, error)
	// Delete removes a credential (logout). Implementations serialize this
	// against Modify.
	Delete(ctx context.Context, providerID string) error
}

// AuthContext is environment access for auth resolution. Injectable for
// tests.
type AuthContext interface {
	// Env resolves one environment variable; ok is false when unset or
	// blank.
	Env(name string) (value string, ok bool)
	// FileExists reports whether a file exists. Supports a leading ~.
	FileExists(path string) bool
}

// AuthResult is the result of resolving auth for a model.
type AuthResult struct {
	Auth ModelAuth
	// Env holds provider-scoped environment/config values resolved from
	// credentials and ambient context.
	Env types.ProviderEnv `json:"env,omitempty"`
	// Source is a human-readable label for status UI: "ANTHROPIC_API_KEY",
	// "OAuth", "~/.aws/credentials".
	Source string `json:"source,omitempty"`
}

// AuthCheck reports whether a provider has complete auth configuration.
type AuthCheck struct {
	Source string   `json:"source,omitempty"`
	Type   AuthType `json:"type"`
}

// AuthType discriminates auth methods.
type AuthType string

const (
	AuthTypeAPIKey AuthType = "api_key"
	AuthTypeOAuth  AuthType = "oauth"
)

// AuthPromptOption is one selectable option of a select prompt.
type AuthPromptOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// AuthPrompt is a prompt shown to the user during login. pi attaches a
// per-prompt AbortSignal so a flow can cancel a pending prompt when an
// out-of-band event resolves the step; pig hands the prompt's context to the
// interaction call instead (see AuthInteraction).
type AuthPrompt struct {
	Type        string             `json:"type"` // "text" | "secret" | "select" | "manual_code"
	Message     string             `json:"message"`
	Placeholder string             `json:"placeholder,omitempty"`
	Options     []AuthPromptOption `json:"options,omitempty"`
}

// AuthInfoLink is a link rendered alongside an info event.
type AuthInfoLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

// AuthEvent is one progress notification from a login flow. Type is "info",
// "auth_url", "device_code", or "progress"; exactly the matching fields are
// set.
type AuthEvent struct {
	Type string `json:"type"`
	// info
	Message string         `json:"message,omitempty"`
	Links   []AuthInfoLink `json:"links,omitempty"`
	// auth_url
	URL          string `json:"url,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	// device_code
	UserCode         string `json:"userCode,omitempty"`
	VerificationURI  string `json:"verificationUri,omitempty"`
	IntervalSeconds  *int64 `json:"intervalSeconds,omitempty"`
	ExpiresInSeconds *int64 `json:"expiresInSeconds,omitempty"`
}

// AuthInteraction serves both api-key and OAuth login flows.
//
// Prompt shows one prompt and returns the entered/selected string (select
// returns the option id); an error means cancel/abort. Per-prompt
// cancellation uses the prompt context. Notify delivers a non-blocking
// progress event.
type AuthInteraction interface {
	Prompt(prompt AuthPrompt) (string, error)
	Notify(event AuthEvent)
}

// ApiKeyAuthInput is the input of ApiKeyAuth Resolve and Check (pi:
// { ctx, credential?, signal }).
type ApiKeyAuthInput struct {
	// Ctx is pi's ctx: the AuthContext for env/file access.
	Ctx AuthContext
	// Credential is the stored api-key credential, nil when none stored.
	Credential *ApiKeyCredential
	// Context is pi's signal.
	Context context.Context
}

// ApiKeyAuth is api-key auth: stored key/provider env plus ambient sources
// (env vars, AWS profiles, ADC files). Ambient-only providers omit the login
// capability (ApiKeyAuthLogin).
type ApiKeyAuth interface {
	// Name is a display name, e.g. "Anthropic API key".
	Name() string
	// Resolve derives auth from the stored credential and/or ambient
	// sources, merging per field (credential key ?? env(...), credential env
	// ?? env(...)). A nil result means not configured. Resolution is
	// provider-scoped; model-specific endpoint preparation happens after
	// auth has been resolved.
	Resolve(input ApiKeyAuthInput) (*AuthResult, error)
}

// ApiKeyAuthLogin is the optional interactive-setup capability of an
// ApiKeyAuth (pi: optional login member; absent = ambient-only).
type ApiKeyAuthLogin interface {
	Login(ctx context.Context, interaction AuthInteraction) (*ApiKeyCredential, error)
}

// ApiKeyAuthChecker is the optional side-effect-free availability check
// capability of an ApiKeyAuth (pi: optional check member). Use it when
// Resolve may execute commands or other request-time work; when absent,
// Models checks availability by resolving auth.
type ApiKeyAuthChecker interface {
	Check(input ApiKeyAuthInput) (*AuthCheck, error)
}

// OAuthAuth is OAuth auth. The Refresh/ToAuth split lets Models own the
// locked refresh pattern: Refresh produces a credential, ToAuth derives
// request auth from whatever credential ends up stored.
type OAuthAuth interface {
	// Name is a display name, e.g. "Anthropic (Claude Pro/Max)".
	Name() string
	// IsSubscription reports whether access through this auth method is
	// backed by a provider subscription (pi: optional isSubscription).
	IsSubscription() bool
	// LoginLabel is the selector label for the OAuth login option, e.g.
	// "Sign in with SuperGrok or X Premium"; empty when none (pi: optional
	// loginLabel).
	LoginLabel() string
	// Login runs the provider-owned login flow.
	Login(ctx context.Context, interaction AuthInteraction) (*OAuthCredential, error)
	// Refresh exchanges the refresh token. Network call; errors on failure
	// (invalid_grant etc.). Models runs it under the store lock.
	Refresh(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error)
	// ToAuth derives request auth from a valid credential,
	// side-effect-free. Covers per-credential baseUrl (GitHub Copilot).
	ToAuth(credential *OAuthCredential) (*ModelAuth, error)
}

// ProviderAuth is provider auth. At least one of APIKey/OAuth must be
// present: even ambient-credential providers and keyless local servers
// provide APIKey auth whose Resolve reports whether the provider is
// configured.
type ProviderAuth struct {
	APIKey ApiKeyAuth
	OAuth  OAuthAuth
}
