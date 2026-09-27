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
	"os"
	"sort"
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
	var config Config
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, []Diagnostic{{Severity: "error", Message: fmt.Sprintf("invalid JSON: %v", err), Path: path}}
	}
	var diags []Diagnostic
	for name, server := range config.MCPServers {
		if diag := validateServerConfig(name, server); diag != nil {
			diag.Path = path
			diags = append(diags, *diag)
			delete(config.MCPServers, name)
		}
	}
	return &config, diags
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
// the returned diagnostics.
func LoadConfigFiles(paths []string) (*Config, []Diagnostic, error) {
	merged := &Config{MCPServers: map[string]ServerConfig{}, Sources: map[string]string{}}
	var diags []Diagnostic
	for _, path := range paths {
		if path == "" {
			continue
		}
		config, fileDiags, err := ParseConfigFile(path)
		if err != nil {
			diags = append(diags, Diagnostic{Severity: "warning", Message: err.Error(), Path: path})
			continue
		}
		diags = append(diags, fileDiags...)
		if config == nil {
			continue
		}
		for name, server := range config.MCPServers {
			merged.MCPServers[name] = server
			merged.Sources[name] = path
		}
	}
	return merged, diags, nil
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
