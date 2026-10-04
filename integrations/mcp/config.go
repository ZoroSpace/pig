// Package mcp implements a native MCP (Model Context Protocol) client layer
// for pig. It is a pig-only surface (pi has no native MCP): it reads standard
// `mcpServers` config files, lazily spawns/connects servers over stdio or
// Streamable HTTP, and exposes tool discovery + invocation to the proxy tool
// built in codingagent/core (see core/mcp_integration.go).
//
// The package is deliberately leaf-ish: it depends only on the official SDK
// (github.com/modelcontextprotocol/go-sdk) and stdlib. All pig-side knowledge
// (config paths, trust gating, `!command`/$env header resolution, tool
// definition plumbing) lives in codingagent/core.
package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Config is the root shape of a pig MCP config file. It matches the
// ecosystem-standard `mcpServers` key so existing Claude Code / Cursor style
// .mcp.json files work as-is.
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`

	// Sources maps each merged server name to the file path of the winning
	// definition (later-wins chain; pig-only, not serialized). Set only by
	// LoadConfigFiles — the /mcp panel needs it to write `disabled` back to
	// the file that actually owns the server.
	Sources map[string]string `json:"-"`

	// Overrides maps server names to the project file that overrides them
	// (pi 1387af7b4's McpServerEntry.override; pig-only, not serialized). A
	// project entry without `command`/`url` overrides only `disabled` of the
	// server with the same name defined in a later (global/user-level) file.
	Overrides map[string]string `json:"-"`
}

// ConfigFile is one file in the config chain with its scope. Project-scoped
// files accept override entries (no `command`/`url`) that only set
// `disabled` on a server defined elsewhere in the chain (#10277).
type ConfigFile struct {
	Path    string
	Project bool
}

// Lifecycle values for ServerConfig.
const (
	// LifecycleLazy (default) connects on first use and reaps the server after
	// IdleTimeoutMs of inactivity.
	LifecycleLazy = "lazy"
	// LifecycleEager connects when the manager starts and reaps after idle.
	LifecycleEager = "eager"
	// LifecycleKeepAlive connects eagerly and is never reaped.
	LifecycleKeepAlive = "keep-alive"
	// LifecycleLazyKeepAlive connects on first use and is never reaped.
	LifecycleLazyKeepAlive = "lazy-keep-alive"
)

// Defaults overridable via ManagerOptions (config-level zero values fall back
// to these).
const (
	DefaultIdleTimeoutMs    = 5 * 60 * 1000
	DefaultRequestTimeoutMs = 60 * 1000
)

// ServerConfig describes one MCP server. A server is stdio (Command set) or
// HTTP (URL set), never both.
type ServerConfig struct {
	// stdio transport
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`

	// Streamable HTTP transport
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Lifecycle: "lazy" (default) | "eager" | "keep-alive" | "lazy-keep-alive".
	// keep-alive variants are never reaped for idleness.
	Lifecycle string `json:"lifecycle,omitempty"`

	// IdleTimeoutMs overrides the idle reap delay (DefaultIdleTimeoutMs when 0).
	IdleTimeoutMs int `json:"idleTimeoutMs,omitempty"`
	// RequestTimeoutMs overrides the per-request timeout
	// (DefaultRequestTimeoutMs when 0).
	RequestTimeoutMs int `json:"requestTimeoutMs,omitempty"`

	// IncludeTools/ExcludeTools are glob patterns matched against the raw tool
	// name (path.Match semantics). Exclude wins.
	IncludeTools []string `json:"includeTools,omitempty"`
	ExcludeTools []string `json:"excludeTools,omitempty"`

	// ToolPrefix is prepended to tool names for display (and accepted back on
	// call). Empty = no prefix.
	ToolPrefix string `json:"toolPrefix,omitempty"`

	// Disabled turns the server off without deleting its entry (pig-only
	// field; the /mcp panel toggles it and writes it back to the source
	// file). Disabled servers never connect and contribute no tools.
	Disabled bool `json:"disabled,omitempty"`

	// OAuth configures the authorization-code flow for servers that do not
	// support dynamic client registration or need specific scopes (pi:
	// McpOAuthConfig). Only meaningful for HTTP servers.
	OAuth *OAuthConfig `json:"oauth,omitempty"`

	// Auth sends a /login provider's token instead of running the OAuth flow
	// (pi 91f9f3b5d). Only allowed in global files, never project files.
	Auth *MCPAuthConfig `json:"auth,omitempty"`
}

// OAuthConfig is the per-server OAuth client configuration.
type OAuthConfig struct {
	// ClientID is a pre-registered client id. Without it, pig registers a
	// client with the authorization server (dynamic client registration).
	ClientID string `json:"clientId,omitempty"`
	// ClientSecret may reference environment variables (`${NAME}`) or
	// commands (`!cmd`); resolved at use time.
	ClientSecret string `json:"clientSecret,omitempty"`
	// CallbackPort pins the loopback callback server port, for clients
	// registered with a fixed redirect URI.
	CallbackPort int `json:"callbackPort,omitempty"`
	// CallbackURL is the registered loopback redirect URI, sent exactly as
	// written (http on localhost/127.0.0.1/[::1], no query or fragment).
	CallbackURL string `json:"callbackUrl,omitempty"`
	// Scope lists the scopes to request, separated by spaces. Default: the
	// scopes the server advertises.
	Scope string `json:"scope,omitempty"`
	// ClientName is the `client_name` sent with dynamic client registration.
	// Default: "pig".
	ClientName string `json:"clientName,omitempty"`
	// ClientRegistration selects how pig identifies itself without a
	// clientId. Only "dcr" (the default) is supported: pi's "cimd" relies on
	// pi.dev-hosted Client ID Metadata Documents, which do not exist for pig.
	ClientRegistration string `json:"clientRegistration,omitempty"`
	// AuthServerMetadataURL overrides discovery with a configured
	// authorization server metadata document (RFC 8414 / OIDC), for servers
	// that advertise a wrong authorization server or none. Trusted as
	// configured; must use https, except on loopback hosts.
	AuthServerMetadataURL string `json:"authServerMetadataUrl,omitempty"`
}

// MCPAuthConfig sends a provider's login token instead of OAuth.
type MCPAuthConfig struct {
	Provider string `json:"provider"`
}

// Diagnostic is a config-level problem. Severity is "warning" or "error";
// errors mean the config (or the offending server) was not loaded.
type Diagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Server   string `json:"server,omitempty"`
}

// ParseConfigBytes parses the raw JSON content of one MCP config file. A
// missing `mcpServers` key is not an error (empty config); malformed JSON and
// invalid server entries produce diagnostics.
func ParseConfigBytes(path string, content []byte) (*Config, []Diagnostic) {
	config, diags, _ := parseConfigBytesScoped(path, content, false)
	return config, diags
}

// pendingOverride is a project override entry collected during the chain
// walk. pig's chain reads project files FIRST (later-wins: user/global files
// follow), so the overridden server is not loaded yet when the override is
// seen — resolution happens after the whole chain (pi resolves inline because
// it reads global first; the deferred pass is the order-flipped equivalent).
type pendingOverride struct {
	name     string
	path     string
	disabled bool
}

func parseConfigBytesScoped(path string, content []byte, project bool) (*Config, []Diagnostic, []pendingOverride) {
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, []Diagnostic{{Severity: "error", Message: fmt.Sprintf("invalid JSON: %v", err), Path: path}}, nil
	}
	var diags []Diagnostic
	var pending []pendingOverride
	if project {
		// Raw pass for override detection (pi: isOverride — the `command` and
		// `url` KEYS must be absent; `"command": ""` still counts as defined).
		var raw struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(content, &raw); err != nil {
			// Already reported by the struct decode above.
			return nil, diags, nil
		}
		for _, name := range sortedRawServerNames(raw.MCPServers) {
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw.MCPServers[name], &keys); err != nil {
				continue // struct decode already reported the malformed entry
			}
			_, hasCommand := keys["command"]
			_, hasURL := keys["url"]
			if hasCommand || hasURL {
				// A real definition in a project file: auth may not pick
				// where a credential goes (pi: "auth is only allowed in the
				// global mcp.json").
				if _, hasAuth := keys["auth"]; hasAuth {
					diags = append(diags, Diagnostic{Severity: "error", Path: path, Server: name,
						Message: fmt.Sprintf("server %q: auth is only allowed in the global mcp.json", name)})
					delete(config.MCPServers, name)
				}
				continue
			}
			// Override entry: only `disabled` may be set (pig's whole
			// OVERRIDE_KEYS surface — pi's enabled/exposure/toolExposure map
			// to it, exposure/toolExposure have no pig face).
			extra := false
			for key := range keys {
				if key != "disabled" {
					extra = true
				}
			}
			delete(config.MCPServers, name)
			if extra {
				diags = append(diags, Diagnostic{Severity: "error", Path: path, Server: name,
					Message: fmt.Sprintf("server %q: an override can only set disabled", name)})
				continue
			}
			override := pendingOverride{name: name, path: path}
			if rawDisabled, ok := keys["disabled"]; ok {
				if err := json.Unmarshal(rawDisabled, &override.disabled); err != nil {
					diags = append(diags, Diagnostic{Severity: "error", Path: path, Server: name,
						Message: fmt.Sprintf("server %q: disabled must be a boolean", name)})
					continue
				}
			}
			pending = append(pending, override)
		}
	}
	for _, name := range sortedServerNames(config.MCPServers) {
		server := config.MCPServers[name]
		if diag := validateServerConfig(name, server); diag != nil {
			diag.Path = path
			diags = append(diags, *diag)
			delete(config.MCPServers, name)
		}
	}
	return &config, diags, pending
}

// sortedRawServerNames returns raw entry names in deterministic order
// (pig: map iteration is random; pi's Object.entries is insertion order).
func sortedRawServerNames(raw map[string]json.RawMessage) []string {
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedServerNames(servers map[string]ServerConfig) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseConfigFile reads and parses one MCP config file. A missing file is not
// an error and returns a nil config.
func ParseConfigFile(path string) (*Config, []Diagnostic, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	config, diags := ParseConfigBytes(path, content)
	return config, diags, nil
}

// LoadConfigFiles parses the given files in order and merges them, with later
// files winning on per-server conflicts (whole-entry replacement, not field
// merge). Invalid servers are dropped from the merged result but reported in
// the returned diagnostics. All files are treated as non-project (no
// override entries); use LoadConfigFileChain for scoped chains.
func LoadConfigFiles(paths []string) (*Config, []Diagnostic, error) {
	files := make([]ConfigFile, len(paths))
	for i, path := range paths {
		files[i] = ConfigFile{Path: path}
	}
	return LoadConfigFileChain(files)
}

// LoadConfigFileChain parses the given files in order and merges them with
// later files winning on per-server conflicts. Project-scoped files accept
// override entries (no `command`/`url`, only `disabled`): they are resolved
// after the whole chain against the server with the same name defined in a
// later file, and recorded in Config.Overrides (#10277).
func LoadConfigFileChain(files []ConfigFile) (*Config, []Diagnostic, error) {
	merged := &Config{MCPServers: map[string]ServerConfig{}, Sources: map[string]string{}, Overrides: map[string]string{}}
	var diags []Diagnostic
	var pending []pendingOverride
	for _, file := range files {
		if file.Path == "" {
			continue
		}
		content, err := os.ReadFile(file.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			diags = append(diags, Diagnostic{Severity: "warning", Message: err.Error(), Path: file.Path})
			continue
		}
		config, fileDiags, filePending := parseConfigBytesScoped(file.Path, content, file.Project)
		diags = append(diags, fileDiags...)
		pending = append(pending, filePending...)
		if config == nil {
			continue
		}
		for name, server := range config.MCPServers {
			merged.MCPServers[name] = server
			merged.Sources[name] = file.Path
		}
	}
	// Resolve collected overrides in file order (later files win).
	for _, override := range pending {
		base, ok := merged.MCPServers[override.name]
		if !ok {
			diags = append(diags, Diagnostic{Severity: "error", Path: override.path, Server: override.name,
				Message: fmt.Sprintf("server %q needs \"command\" or \"url\", or a global server to override", override.name)})
			continue
		}
		base.Disabled = override.disabled
		merged.MCPServers[override.name] = base
		merged.Overrides[override.name] = override.path
	}
	return merged, diags, nil
}

// loopbackRedirectHosts are the hosts a loopback redirect URI may use (pi:
// LOOPBACK_HOSTS).
var loopbackRedirectHosts = []string{"localhost", "127.0.0.1", "[::1]"}

// isLoopbackRedirectUri reports whether value is an http URI on localhost,
// 127.0.0.1, or [::1] without query or fragment (pi: isLoopbackRedirectUri) —
// the shape pi's loopback callback server can serve.
func isLoopbackRedirectUri(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return u.Scheme == "http" && slices.Contains(loopbackRedirectHosts, u.Hostname()) &&
		u.RawQuery == "" && u.Fragment == ""
}

// validateOAuthConfig mirrors pi's validateOAuth (core/mcp-servers.ts).
func validateOAuthConfig(o *OAuthConfig) string {
	const prefix = "oauth."
	if o.CallbackPort != 0 && (o.CallbackPort < 1 || o.CallbackPort > 65535) {
		return prefix + "callbackPort must be a port number"
	}
	if o.CallbackURL != "" {
		if !isLoopbackRedirectUri(o.CallbackURL) {
			return prefix + "callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment"
		}
		if u, err := url.Parse(o.CallbackURL); err == nil && u.Port() != "" {
			if port, err := strconv.Atoi(u.Port()); err == nil && o.CallbackPort != 0 && port != o.CallbackPort {
				return prefix + "callbackUrl and oauth.callbackPort name different ports"
			}
		}
	}
	switch o.ClientRegistration {
	case "":
	case "dcr":
	case "cimd":
		return `oauth.clientRegistration "cimd" is not supported by pig: pi.dev-hosted Client ID Metadata Documents do not exist for pig (use dynamic client registration or oauth.clientId)`
	default:
		return `oauth.clientRegistration must be "dcr" or "cimd"`
	}
	if o.AuthServerMetadataURL != "" {
		u, err := url.Parse(o.AuthServerMetadataURL)
		if err != nil || !(u.Scheme == "https" || (u.Scheme == "http" && slices.Contains(loopbackRedirectHosts, u.Hostname()))) {
			return prefix + "authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]"
		}
	}
	return ""
}

func validateServerConfig(name string, server ServerConfig) *Diagnostic {
	if strings.TrimSpace(name) == "" {
		return &Diagnostic{Severity: "error", Message: "server entry with empty name"}
	}
	hasCommand := strings.TrimSpace(server.Command) != ""
	hasURL := strings.TrimSpace(server.URL) != ""
	if hasCommand == hasURL {
		kind := "neither command nor url"
		if hasCommand {
			kind = "both command and url"
		}
		return &Diagnostic{Severity: "error", Message: fmt.Sprintf("invalid server config: %s", kind)}
	}
	if server.OAuth != nil {
		if !hasURL {
			return &Diagnostic{Severity: "error", Message: fmt.Sprintf("server %q: oauth only applies to HTTP servers (with \"url\")", name)}
		}
		if message := validateOAuthConfig(server.OAuth); message != "" {
			return &Diagnostic{Severity: "error", Message: fmt.Sprintf("server %q: %s", name, message)}
		}
	}
	if server.Auth != nil {
		if !hasURL {
			return &Diagnostic{Severity: "error", Message: fmt.Sprintf("server %q: auth only applies to HTTP servers (with \"url\")", name)}
		}
		if strings.TrimSpace(server.Auth.Provider) == "" {
			return &Diagnostic{Severity: "error", Message: fmt.Sprintf("server %q: auth.provider must be a non-empty string", name)}
		}
		// Sending the provider's credential to `url` demands https (pi:
		// "requires https except on loopback hosts").
		if u, err := url.Parse(server.URL); err != nil || !(u.Scheme == "https" || (u.Scheme == "http" && slices.Contains(loopbackRedirectHosts, u.Hostname()))) {
			return &Diagnostic{Severity: "error", Message: fmt.Sprintf("server %q: auth requires an https url, or http on localhost, 127.0.0.1, or [::1]", name)}
		}
	}
	switch server.Lifecycle {
	case "", LifecycleLazy, LifecycleEager, LifecycleKeepAlive, LifecycleLazyKeepAlive:
	default:
		return &Diagnostic{Severity: "error", Message: fmt.Sprintf("invalid lifecycle %q (want lazy, eager, keep-alive or lazy-keep-alive)", server.Lifecycle)}
	}
	if server.IdleTimeoutMs < 0 || server.RequestTimeoutMs < 0 {
		return &Diagnostic{Severity: "error", Message: "idleTimeoutMs and requestTimeoutMs must be >= 0"}
	}
	return nil
}

// SortedServerNames returns the merged server names in deterministic order.
func (c *Config) SortedServerNames() []string {
	names := make([]string, 0, len(c.MCPServers))
	for name := range c.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsEmpty reports whether no servers are configured.
func (c *Config) IsEmpty() bool {
	return c == nil || len(c.MCPServers) == 0
}
