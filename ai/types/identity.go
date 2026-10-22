package types

// Client identity types live in ai/types rather than ai/utils because
// ProviderRequestOptions references them and ai/types is the dependency leaf
// (pi keeps them in utils/identity.ts; only the definitions move).

// UserAgentMode is the `User-Agent` mode (pi: UserAgentMode):
//   - Detailed (the zero value): `pi/<version> (<platform>; <runtime>; <arch>)`.
//   - Short: `pi`.
//   - Custom: the given value.
//
// pi's string-or-{custom} union maps to this struct; Custom wins over Short.
type UserAgentMode struct {
	Short  bool
	Custom string
}

// UserAgentShort is the "short" mode.
func UserAgentShort() *UserAgentMode { return &UserAgentMode{Short: true} }

// UserAgentDetailed is the default mode.
func UserAgentDetailed() *UserAgentMode { return &UserAgentMode{} }

// UserAgentCustom is the `{ custom }` mode.
func UserAgentCustom(value string) *UserAgentMode { return &UserAgentMode{Custom: value} }

// ClientAttribution says who requests are attributed to (pi:
// ClientAttribution). Attribution headers map the fields onto each provider's
// headers.
type ClientAttribution struct {
	// Name is the app name: OpenRouter app title, NVIDIA invoke origin (first
	// letter upper-cased), OpenCode client.
	Name string
	// URL is the app URL: OpenRouter HTTP-Referer, the app's identity in
	// OpenRouter's rankings, grouped by origin.
	URL string
	// Categories are app categories, e.g. ["cli-agent"]: OpenRouter
	// X-OpenRouter-Categories, comma-separated.
	Categories []string
}

// PIAttribution is pi's attribution (pi: PI_ATTRIBUTION), used by
// pi-agent-core, pi-durable, and the coding agent.
var PIAttribution = ClientAttribution{
	Name:       "pi",
	URL:        "https://pi.dev",
	Categories: []string{"cli-agent"},
}

// AttributionSetting distinguishes an absent attribution field from an
// explicit null (pi: `attribution?: ClientAttribution | null`):
//   - nil AttributionSetting: absent; keep a merged value.
//   - non-nil with nil Attribution: explicit null; turn attribution off.
//   - non-nil with non-nil Attribution: use this attribution.
type AttributionSetting struct {
	Attribution *ClientAttribution
}

// ClientIdentity is how pi-ai identifies the calling app to providers (pi:
// ClientIdentity). Every field is optional; absent fields use pi-ai's
// defaults: a detailed User-Agent with the pi-ai version and no attribution
// headers. An empty Version also means absent.
type ClientIdentity struct {
	// UserAgent defaults to a detailed user agent.
	UserAgent *UserAgentMode
	// Attribution controls provider attribution headers. Nil / absent means
	// none; a non-nil setting with nil Attribution turns attribution set by a
	// merged identity off.
	Attribution *AttributionSetting
	// Version is the version in the detailed User-Agent. Empty means the
	// pi-ai version.
	Version string
}

// ClientIdentityTarget is the provider and endpoint a request goes to (pi:
// ClientIdentityTarget).
type ClientIdentityTarget struct {
	Provider string
	BaseURL  string
}
