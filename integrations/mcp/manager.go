package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager owns the configured MCP servers: lazy connect, tool discovery,
// tool invocation, idle reaping and shutdown. It is safe for concurrent use.
//
// Locking: Manager.mu guards the server map and `closed`; each serverState
// has its own mutex serializing lifecycle (connect/close/reap) and requests
// for that server. Status() reads only fields guarded by Manager.mu-style
// snapshots, so it never blocks on an in-flight tool call.
type Manager struct {
	config *Config
	opts   ManagerOptions

	mu      sync.Mutex
	servers map[string]*serverState
	closed  bool

	reapStop chan struct{}
	reapDone chan struct{}
}

// ManagerOptions configures the Manager. All fields are optional.
type ManagerOptions struct {
	// ResolveHeaders is applied to a server's Headers at connect time, before
	// any network traffic. pig wires core.ResolveHeadersOrThrow here so
	// `!command` / `$env` header values work; the mcp package stays free of
	// core dependencies.
	ResolveHeaders func(server string, headers map[string]string) (map[string]string, error)
	// HTTPClient overrides the HTTP client used for Streamable HTTP servers
	// (tests point it at httptest servers via a custom transport).
	HTTPClient *http.Client
	// Logger receives debug output when set (pig wires a PIG_MCP_DEBUG-gated
	// file writer).
	Logger func(format string, args ...any)
	// ClientVersion is reported to servers during initialize.
	ClientVersion string
	// ReapInterval is how often the idle reaper wakes (default 30s).
	ReapInterval time.Duration
	// DefaultIdleTimeout / DefaultRequestTimeout override the package
	// defaults when config-level values are 0.
	DefaultIdleTimeout    time.Duration
	DefaultRequestTimeout time.Duration
	// ConnectTimeout bounds a single connect (default 10s).
	ConnectTimeout time.Duration
	// TransportFactory overrides transport construction (pig-only test seam:
	// tests hand back in-memory transports; a future unix-socket transport
	// would plug in here too). When nil, stdio/HTTP are built from config.
	TransportFactory func(server string, cfg ServerConfig) (mcp.Transport, error)
}

// ServerStatus is a snapshot of one server's runtime state (for the `status`
// action of the proxy tool and future /mcp UI).
type ServerStatus struct {
	Name       string `json:"name"`
	Transport  string `json:"transport"` // "stdio" | "http"
	Lifecycle  string `json:"lifecycle"`
	Status     string `json:"status"` // "connected" | "disconnected" | "connecting" | "error" | "disabled"
	ToolCount  int    `json:"toolCount,omitempty"`
	PID        int    `json:"pid,omitempty"`
	LastError  string `json:"lastError,omitempty"`
	IdleMs     int64  `json:"idleMs,omitempty"` // ms since last use, when connected
	Disabled   bool   `json:"disabled,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"` // winning config file (for the /mcp panel's write-back)
}

// ToolInfo describes one discovered tool, prefixed for display.
type ToolInfo struct {
	Server      string `json:"server"`
	Name        string `json:"name"`    // display name: ToolPrefix + raw name
	RawName     string `json:"rawName"` // name as the server knows it
	Description string `json:"description"`
	Schema      any    `json:"schema,omitempty"` // JSON Schema (map form)
}

// ToolCallOutput is the raw result of one tool invocation.
type ToolCallOutput struct {
	Content           []mcp.Content
	StructuredContent any
	IsError           bool
}

type serverState struct {
	name string
	cfg  ServerConfig
	// sourcePath is the winning config file for this server (Config.Sources);
	// empty when the manager was built without LoadConfigFiles (tests, direct
	// construction) — SetServerDisabled then only flips the in-memory flag.
	sourcePath string

	mu       sync.Mutex
	session  *mcp.ClientSession
	cmd      *exec.Cmd
	stderr   *ringBuffer
	lastUsed time.Time
	status   string // "connected" | "disconnected" | "connecting" | "error"
	toolErr  string // last discovery/invocation error (for status)

	cachedTools []ToolInfo // valid while session != nil
}

const (
	statusConnected    = "connected"
	statusDisconnected = "disconnected"
	statusConnecting   = "connecting"
	statusError        = "error"
	statusDisabled     = "disabled"
)

// NewManager creates a Manager for the given (already merged) config. It does
// not connect anything: lazy servers connect on first use, eager/keep-alive
// servers connect when StartEager is called.
func NewManager(config *Config, opts ManagerOptions) *Manager {
	if config == nil {
		config = &Config{}
	}
	m := &Manager{
		config:   config,
		opts:     opts,
		servers:  map[string]*serverState{},
		reapStop: make(chan struct{}),
		reapDone: make(chan struct{}),
	}
	for _, name := range config.SortedServerNames() {
		m.servers[name] = &serverState{
			name:       name,
			cfg:        config.MCPServers[name],
			sourcePath: config.Sources[name],
			status:     statusDisconnected,
		}
	}
	// Always run the reaper: Close() waits on reapDone unconditionally, so a
	// missing loop would hang an empty manager's shutdown (the idle ticker
	// with zero servers costs nothing).
	go m.reapLoop()
	return m
}

// StartEager connects every eager/keep-alive server in the background.
// Connection failures only mark server status; they are not fatal.
func (m *Manager) StartEager() {
	m.mu.Lock()
	eager := slices.Collect(maps.Keys(m.servers))
	m.mu.Unlock()
	for _, name := range eager {
		m.mu.Lock()
		st := m.servers[name]
		m.mu.Unlock()
		if st == nil || !isEagerLifecycle(st.cfg.Lifecycle) {
			continue
		}
		go func() {
			st.mu.Lock()
			defer st.mu.Unlock()
			if st.cfg.Disabled {
				return
			}
			// Background eager connect: unbounded context is bounded by the
			// connect timeout inside connectLocked.
			_, _ = m.connectLocked(context.Background(), st)
		}()
	}
}

func isEagerLifecycle(lifecycle string) bool {
	return lifecycle == LifecycleEager || lifecycle == LifecycleKeepAlive
}

func isKeepAlive(lifecycle string) bool {
	return lifecycle == LifecycleKeepAlive || lifecycle == LifecycleLazyKeepAlive
}

// ServerNames returns the configured server names in sorted order.
func (m *Manager) ServerNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(maps.Keys(m.servers))
}

// HasServers reports whether any server is configured.
func (m *Manager) HasServers() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers) > 0
}

// Close shuts every server down and stops the reaper. Idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	servers := slices.Collect(maps.Values(m.servers))
	m.mu.Unlock()

	close(m.reapStop)
	<-m.reapDone

	for _, st := range servers {
		st.mu.Lock()
		m.closeSessionLocked(st)
		st.mu.Unlock()
	}
	return nil
}

// Status returns a snapshot of all servers' runtime state.
//
// m.mu is released before acquiring any st.mu: connectLocked takes m.mu
// while holding st.mu, so the reverse order here would be an ABBA deadlock.
func (m *Manager) Status() []ServerStatus {
	m.mu.Lock()
	names := slices.Sorted(maps.Keys(m.servers))
	states := make([]*serverState, 0, len(names))
	for _, name := range names {
		states = append(states, m.servers[name])
	}
	m.mu.Unlock()

	out := make([]ServerStatus, 0, len(states))
	for _, st := range states {
		st.mu.Lock()
		defer st.mu.Unlock()
		s := ServerStatus{
			Name:       st.name,
			Lifecycle:  st.cfg.Lifecycle,
			Status:     st.status,
			ToolCount:  len(st.cachedTools),
			LastError:  st.toolErr,
			Disabled:   st.cfg.Disabled,
			SourcePath: st.sourcePath,
		}
		if st.cfg.Disabled {
			s.Status = statusDisabled
		}
		if st.cfg.Command != "" {
			s.Transport = "stdio"
			if st.cmd != nil && st.cmd.Process != nil {
				s.PID = st.cmd.Process.Pid
			}
		} else {
			s.Transport = "http"
		}
		if st.status == statusConnected && !st.lastUsed.IsZero() {
			s.IdleMs = time.Since(st.lastUsed).Milliseconds()
		}
		out = append(out, s)
	}
	return out
}

// SearchTools connects servers as needed to list their tools, filters by the
// configured include/exclude globs, scores them against the query and returns
// the top `limit` matches (all matches when limit <= 0). An empty query
// lists every tool.
func (m *Manager) SearchTools(ctx context.Context, query string, limit int) ([]ToolMatch, error) {
	var candidates []ToolInfo
	for _, name := range m.ServerNames() {
		if m.isDisabled(name) {
			continue
		}
		tools, err := m.listServerTools(ctx, name)
		if err != nil {
			// A broken server must not hide the other servers' tools.
			m.logf("search: server %q: %v", name, err)
			continue
		}
		candidates = append(candidates, tools...)
	}
	matches := searchTools(candidates, query)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// DescribeTool returns the full ToolInfo for a display name (ToolPrefix +
// raw name) or raw name. It resolves the name across servers, connecting as
// needed.
func (m *Manager) DescribeTool(ctx context.Context, name string) (ToolInfo, error) {
	info, err := m.resolveToolName(ctx, name)
	if err != nil {
		return ToolInfo{}, err
	}
	return info, nil
}

// CallTool invokes `tool` (display or raw name, or explicit "server__tool")
// with the given arguments on its server, honoring the server's request
// timeout and include/exclude filters.
func (m *Manager) CallTool(ctx context.Context, server, tool string, args map[string]any) (*ToolCallOutput, error) {
	m.mu.Lock()
	st := m.servers[server]
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, errors.New("mcp manager is closed")
	}
	if st == nil {
		return nil, fmt.Errorf("unknown mcp server %q", server)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.session == nil {
		if _, err := m.connectLocked(ctx, st); err != nil {
			return nil, err
		}
	}
	// Accept the full "server__tool" convention as well as display/raw names.
	tool = strings.TrimPrefix(tool, server+"__")
	rawName, ok := st.toolRawName(tool)
	if !ok {
		// Refresh the cache once in case the server added tools since connect.
		if err := m.listToolsLocked(ctx, st); err != nil {
			return nil, err
		}
		rawName, ok = st.toolRawName(tool)
	}
	if !ok {
		return nil, fmt.Errorf("mcp server %q has no tool %q", server, tool)
	}

	reqCtx, cancel := m.requestContext(ctx, st)
	defer cancel()
	res, err := st.session.CallTool(reqCtx, &mcp.CallToolParams{Name: rawName, Arguments: args})
	st.lastUsed = time.Now()
	if err != nil {
		m.markToolError(st, fmt.Sprintf("call %s: %v", rawName, err))
		return nil, fmt.Errorf("mcp server %q tool %q: %w", server, rawName, err)
	}
	m.markToolOK(st)
	return &ToolCallOutput{Content: res.Content, StructuredContent: res.StructuredContent, IsError: res.IsError}, nil
}

// ---- internals ----

type ToolMatch struct {
	Tool  ToolInfo `json:"tool"`
	Score int      `json:"score"`
}

func (m *Manager) server(name string) (*serverState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("mcp manager is closed")
	}
	st := m.servers[name]
	if st == nil {
		return nil, fmt.Errorf("unknown mcp server %q", name)
	}
	return st, nil
}

// resolveToolName maps a display or raw tool name to its ToolInfo, scanning
// all servers (connecting them as needed) when the name has no explicit
// "server__" prefix.
func (m *Manager) resolveToolName(ctx context.Context, name string) (ToolInfo, error) {
	if server, tool, ok := strings.Cut(name, "__"); ok {
		st, err := m.server(server)
		if err != nil {
			return ToolInfo{}, err
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.session == nil {
			if _, err := m.connectLocked(ctx, st); err != nil {
				return ToolInfo{}, err
			}
		}
		if raw, ok := st.toolRawName(tool); ok {
			return st.toolInfo(raw), nil
		}
		if err := m.listToolsLocked(ctx, st); err != nil {
			return ToolInfo{}, err
		}
		if raw, ok := st.toolRawName(tool); ok {
			return st.toolInfo(raw), nil
		}
		return ToolInfo{}, fmt.Errorf("mcp server %q has no tool %q", server, tool)
	}
	var found []ToolInfo
	for _, serverName := range m.ServerNames() {
		tools, err := m.listServerTools(ctx, serverName)
		if err != nil {
			continue
		}
		for _, info := range tools {
			if info.Name == name || info.RawName == name {
				found = append(found, info)
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return ToolInfo{}, fmt.Errorf("no mcp tool named %q (use search to list tools)", name)
	default:
		names := make([]string, 0, len(found))
		for _, info := range found {
			names = append(names, info.Server+"__"+info.RawName)
		}
		return ToolInfo{}, fmt.Errorf("tool name %q is ambiguous across servers; use one of: %s", name, strings.Join(names, ", "))
	}
}

// listServerTools returns the filtered tool list of one server, connecting it
// first if needed. The result is a copy.
func (m *Manager) listServerTools(ctx context.Context, name string) ([]ToolInfo, error) {
	st, err := m.server(name)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.session != nil && len(st.cachedTools) > 0 {
		return slices.Clone(st.cachedTools), nil
	}
	if st.session == nil {
		if _, err := m.connectLocked(ctx, st); err != nil {
			return nil, err
		}
	}
	if err := m.listToolsLocked(ctx, st); err != nil {
		return nil, err
	}
	return slices.Clone(st.cachedTools), nil
}

// ListServerTools returns the filtered tool list of one server, connecting it
// first if needed (lazy semantics unchanged). Disabled servers error. The
// result is a copy. (pig-only: the /mcp panel lists tools on expand.)
func (m *Manager) ListServerTools(ctx context.Context, name string) ([]ToolInfo, error) {
	return m.listServerTools(ctx, name)
}

// isDisabled reports whether the named server is configured disabled.
func (m *Manager) isDisabled(name string) bool {
	m.mu.Lock()
	st := m.servers[name]
	m.mu.Unlock()
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.cfg.Disabled
}

// Reconnect drops the server's current session (if any) and connects afresh.
// Connect errors are returned as-is for the caller to surface (the /mcp
// panel shows them on the server row). Disabled servers are never connected.
// (pig-only: pi-mcp-adapter's ctrl+r.)
func (m *Manager) Reconnect(ctx context.Context, name string) error {
	st, err := m.server(name)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	m.closeSessionLocked(st)
	if _, err := m.connectLocked(ctx, st); err != nil {
		return err
	}
	return nil
}

// SetServerDisabled flips a server's disabled flag in memory and writes it
// back to the server's winning config file (JSON surgery on mcpServers[name]
// only — sibling servers and unknown fields are preserved; enabling deletes
// the key instead of writing false). A disabled server is torn down; a
// re-enabled one goes back to disconnected and reconnects lazily. When the
// manager was built without a source path, only the in-memory flag changes.
// (pig-only: pi-mcp-adapter keeps an overlay state file; pig persists to the
// source mcp.json per user decision.)
func (m *Manager) SetServerDisabled(name string, disabled bool) error {
	st, err := m.server(name)
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.cfg.Disabled = disabled
	if disabled {
		m.closeSessionLocked(st)
		st.status = statusDisabled
	} else {
		st.status = statusDisconnected
	}
	sourcePath := st.sourcePath
	st.mu.Unlock()
	if sourcePath == "" {
		return nil
	}
	return setServerDisabledInFile(sourcePath, name, disabled)
}

// connectLocked connects st. st.mu must be held.
func (m *Manager) connectLocked(ctx context.Context, st *serverState) (*mcp.ClientSession, error) {
	if st.session != nil {
		return st.session, nil
	}
	if st.cfg.Disabled {
		st.status = statusDisabled
		return nil, fmt.Errorf("mcp server %q is disabled", st.name)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("mcp manager is closed")
	}
	m.mu.Unlock()

	st.status = statusConnecting
	st.stderr = newRingBuffer(64 << 10)
	client := mcp.NewClient(&mcp.Implementation{Name: "pig", Version: m.opts.ClientVersion}, nil)

	var transport mcp.Transport
	if m.opts.TransportFactory != nil {
		custom, err := m.opts.TransportFactory(st.name, st.cfg)
		if err != nil {
			st.status = statusError
			st.toolErr = err.Error()
			return nil, fmt.Errorf("build transport for mcp server %q: %w", st.name, err)
		}
		transport = custom
	} else if st.cfg.Command != "" {
		transport = m.newStdioTransport(st)
	} else {
		httpTransport, err := m.newHTTPTransport(ctx, st)
		if err != nil {
			st.status = statusError
			st.toolErr = err.Error()
			m.logf("connect %s: %v", st.name, err)
			return nil, err
		}
		transport = httpTransport
	}

	connectTimeout := m.opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		st.status = statusError
		st.toolErr = stderrSuffix(err, st.stderr)
		m.closeProcessLocked(st)
		m.logf("connect %s: %v%s", st.name, err, stderrTail(st.stderr))
		return nil, fmt.Errorf("connect mcp server %q: %w%s", st.name, err, stderrTail(st.stderr))
	}
	st.session = session
	st.status = statusConnected
	st.lastUsed = time.Now()
	st.toolErr = ""
	m.logf("connected %s", st.name)
	return session, nil
}

func (m *Manager) newStdioTransport(st *serverState) mcp.Transport {
	cmd := exec.Command(st.cfg.Command, st.cfg.Args...)
	setProcessGroup(cmd)
	if st.cfg.Cwd != "" {
		cmd.Dir = st.cfg.Cwd
	}
	if len(st.cfg.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), st.cfg.Env)
	}
	cmd.Stderr = st.stderr
	st.cmd = cmd
	return &mcp.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}
}

func (m *Manager) newHTTPTransport(ctx context.Context, st *serverState) (mcp.Transport, error) {
	base := http.DefaultTransport
	if m.opts.HTTPClient != nil && m.opts.HTTPClient.Transport != nil {
		base = m.opts.HTTPClient.Transport
	}
	headers := maps.Clone(st.cfg.Headers)
	if len(headers) > 0 && m.opts.ResolveHeaders != nil {
		resolved, err := m.opts.ResolveHeaders(st.name, headers)
		if err != nil {
			return nil, fmt.Errorf("resolve headers: %w", err)
		}
		headers = resolved
	}
	client := &http.Client{Transport: &headerTransport{base: base, headers: headers}}
	if m.opts.HTTPClient != nil {
		client.Timeout = m.opts.HTTPClient.Timeout
	}
	return &mcp.StreamableClientTransport{Endpoint: st.cfg.URL, HTTPClient: client}, nil
}

// listToolsLocked lists and caches the server's tools with filters applied.
// st.mu must be held and st.session non-nil.
func (m *Manager) listToolsLocked(ctx context.Context, st *serverState) error {
	reqCtx, cancel := m.requestContext(ctx, st)
	defer cancel()
	tools, err := collectTools(reqCtx, st.session)
	st.lastUsed = time.Now()
	if err != nil {
		m.markToolError(st, fmt.Sprintf("list tools: %v", err))
		return fmt.Errorf("list tools of mcp server %q: %w", st.name, err)
	}
	filtered := make([]ToolInfo, 0, len(tools))
	for _, tool := range tools {
		if !toolAllowed(st.cfg, tool.Name) {
			continue
		}
		filtered = append(filtered, ToolInfo{
			Server:      st.name,
			Name:        st.cfg.ToolPrefix + tool.Name,
			RawName:     tool.Name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
		})
	}
	st.cachedTools = filtered
	m.markToolOK(st)
	return nil
}

func collectTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// toolRawName resolves a display or raw name against the cached tool list.
// st.mu must be held.
func (st *serverState) toolRawName(name string) (string, bool) {
	for _, info := range st.cachedTools {
		if info.Name == name || info.RawName == name {
			return info.RawName, true
		}
	}
	return "", false
}

// toolInfo returns the cached ToolInfo for a raw name. st.mu must be held.
func (st *serverState) toolInfo(rawName string) ToolInfo {
	for _, info := range st.cachedTools {
		if info.RawName == rawName {
			return info
		}
	}
	return ToolInfo{}
}

// toolAllowed applies include/exclude globs (exclude wins) to a raw name.
func toolAllowed(cfg ServerConfig, name string) bool {
	if len(cfg.IncludeTools) > 0 {
		included := false
		for _, pattern := range cfg.IncludeTools {
			if ok, err := path.Match(pattern, name); err == nil && ok {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	for _, pattern := range cfg.ExcludeTools {
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return false
		}
	}
	return true
}

func (m *Manager) requestContext(ctx context.Context, st *serverState) (context.Context, context.CancelFunc) {
	timeout := m.opts.DefaultRequestTimeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultRequestTimeoutMs) * time.Millisecond
	}
	if st.cfg.RequestTimeoutMs > 0 {
		timeout = time.Duration(st.cfg.RequestTimeoutMs) * time.Millisecond
	}
	return context.WithTimeout(ctx, timeout)
}

func (m *Manager) idleTimeout(st *serverState) time.Duration {
	timeout := m.opts.DefaultIdleTimeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultIdleTimeoutMs) * time.Millisecond
	}
	if st.cfg.IdleTimeoutMs > 0 {
		timeout = time.Duration(st.cfg.IdleTimeoutMs) * time.Millisecond
	}
	return timeout
}

func (m *Manager) markToolError(st *serverState, message string) {
	st.status = statusError
	st.toolErr = message
	m.logf("%s: %s", st.name, message)
}

func (m *Manager) markToolOK(st *serverState) {
	st.status = statusConnected
	st.toolErr = ""
}

// reapLoop closes non-keep-alive servers that have been idle past their
// timeout. They reconnect lazily on next use.
func (m *Manager) reapLoop() {
	defer close(m.reapDone)
	interval := m.opts.ReapInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.reapStop:
			return
		case <-ticker.C:
			m.reapOnce()
		}
	}
}

func (m *Manager) reapOnce() {
	m.mu.Lock()
	servers := slices.Collect(maps.Values(m.servers))
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return
	}
	for _, st := range servers {
		st.mu.Lock()
		if st.session == nil || isKeepAlive(st.cfg.Lifecycle) {
			st.mu.Unlock()
			continue
		}
		if time.Since(st.lastUsed) < m.idleTimeout(st) {
			st.mu.Unlock()
			continue
		}
		m.logf("reaping idle server %s", st.name)
		m.closeSessionLocked(st)
		st.mu.Unlock()
	}
}

// closeSessionLocked tears down st's session and process. st.mu must be held.
func (m *Manager) closeSessionLocked(st *serverState) {
	if st.session != nil {
		_ = st.session.Close()
		st.session = nil
	}
	m.closeProcessLocked(st)
	st.cachedTools = nil
	st.status = statusDisconnected
}

// closeProcessLocked force-kills the stdio child after the transport's own
// graceful close, reaping the whole process group (MCP servers spawn
// children of their own).
func (m *Manager) closeProcessLocked(st *serverState) {
	if st.cmd != nil && st.cmd.Process != nil {
		killProcessGroup(st.cmd.Process.Pid)
	}
	st.cmd = nil
}

func (m *Manager) logf(format string, args ...any) {
	if m.opts.Logger != nil {
		m.opts.Logger(format, args...)
	}
}

// mergeEnv returns os environ with the config entries replacing same-named
// variables (later value wins, no duplicates).
func mergeEnv(environ []string, config map[string]string) []string {
	out := make([]string, 0, len(environ)+len(config))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := config[key]; ok {
			continue
		}
		out = append(out, entry)
	}
	for _, key := range slices.Sorted(maps.Keys(config)) {
		out = append(out, key+"="+config[key])
	}
	return out
}

// headerTransport injects config headers into every request (bearer tokens
// and friends cannot go through StreamableClientTransport directly).
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		clone.Header.Set(key, value)
	}
	return t.base.RoundTrip(clone)
}

// ---- stderr ring buffer ----

type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	max  int
	full bool
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, 0, max), max: max}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		if len(r.buf) == r.max {
			copy(r.buf, r.buf[1:])
			r.buf = r.buf[:len(r.buf)-1]
			r.full = true
		}
		r.buf = append(r.buf, b)
	}
	return len(p), nil
}

// String returns the buffered output, with an ellipsis marker if it wrapped.
func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r == nil {
		return ""
	}
	if r.full {
		return "…" + string(r.buf)
	}
	return string(r.buf)
}

func stderrTail(r *ringBuffer) string {
	if r == nil {
		return ""
	}
	out := r.String()
	if out == "" {
		return ""
	}
	return " (stderr: " + strings.TrimSpace(out) + ")"
}

func stderrSuffix(err error, r *ringBuffer) string {
	return stderrTail(r)
}

var _ io.Writer = (*ringBuffer)(nil)
