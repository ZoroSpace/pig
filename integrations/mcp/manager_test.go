package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---- fake servers over in-memory transports ----

type fakeServer struct {
	name       string
	connects   atomic.Int32
	toolNames  []string
	slowTool   time.Duration
	closeDelay time.Duration
}

// spawn returns a TransportFactory that connects a fresh fake MCP server
// (with echo/add/screenshot/slow/fail tools) via an in-memory transport.
func (f *fakeServer) spawn(ctx context.Context) func(string, ServerConfig) (mcp.Transport, error) {
	return func(server string, cfg ServerConfig) (mcp.Transport, error) {
		if server != f.name {
			return nil, fmt.Errorf("unexpected server %q", server)
		}
		f.connects.Add(1)
		clientTransport, serverTransport := mcp.NewInMemoryTransports()
		srv := mcp.NewServer(&mcp.Implementation{Name: "fake-" + f.name, Version: "1.0.0"}, nil)
		mcp.AddTool(srv, &mcp.Tool{Name: "echo", Description: "echo the message back"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + in.Message}}}, nil, nil
		})
		mcp.AddTool(srv, &mcp.Tool{Name: "write_file", Description: "write a file to disk"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, nil
		})
		mcp.AddTool(srv, &mcp.Tool{Name: "take_screenshot", Description: "take a png screenshot of the page"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte("fakepng"), MIMEType: "image/png"}}}, nil, nil
		})
		mcp.AddTool(srv, &mcp.Tool{Name: "fail", Description: "always fails"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New("boom")
		})
		if f.slowTool > 0 {
			mcp.AddTool(srv, &mcp.Tool{Name: "slow", Description: "slow tool"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
				select {
				case <-time.After(f.slowTool):
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil, nil
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
			})
		}
		go func() {
			_ = srv.Run(ctx, serverTransport)
		}()
		return clientTransport, nil
	}
}

type echoInput struct {
	Message string `json:"message" jsonschema:"the message to echo"`
}

func newTestManager(t *testing.T, config *Config, opts ManagerOptions) *Manager {
	t.Helper()
	if opts.TransportFactory == nil {
		for _, cfg := range config.MCPServers {
			if cfg.URL == "" {
				// stdio fake config without a factory would shell out for
				// real; fail loudly instead (error, not nil transport — nil
				// segfaults inside the SDK).
				opts.TransportFactory = func(server string, _ ServerConfig) (mcp.Transport, error) {
					return nil, fmt.Errorf("test bug: no transport factory installed for server %q", server)
				}
				break
			}
		}
	}
	manager := NewManager(config, opts)
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestSearchAndCallInMemory(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused"},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	matches, err := manager.SearchTools(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, match := range matches {
		names = append(names, match.Tool.Name)
	}
	// SDK serves tool lists sorted by name.
	if got := strings.Join(names, ","); got != "echo,fail,take_screenshot,write_file" {
		t.Fatalf("unexpected tool list: %q", got)
	}

	ranked, err := manager.SearchTools(t.Context(), "screenshot", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 || ranked[0].Tool.RawName != "take_screenshot" {
		t.Fatalf("screenshot query must rank take_screenshot first, got %+v", ranked)
	}

	out, err := manager.CallTool(t.Context(), "fs", "fs__echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError {
		t.Fatalf("echo must not be an error result: %+v", out)
	}
	text, ok := out.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "echo: hi" {
		t.Fatalf("unexpected echo result: %#v", out.Content)
	}
	if fake.connects.Load() != 1 {
		t.Fatalf("lazy connect must happen exactly once, got %d", fake.connects.Load())
	}

	// Display name and raw name both resolve.
	if _, err := manager.CallTool(t.Context(), "fs", "echo", nil); err != nil {
		t.Fatalf("raw name must resolve: %v", err)
	}
}

func TestCallImageAndErrorResults(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{"fs": {Command: "unused"}}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	out, err := manager.CallTool(t.Context(), "fs", "take_screenshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	image, ok := out.Content[0].(*mcp.ImageContent)
	if !ok || image.MIMEType != "image/png" || string(image.Data) != "fakepng" {
		t.Fatalf("unexpected image content: %#v", out.Content[0])
	}

	// SDK reports tool-handler errors as IsError results (MCP spec: the error
	// must be visible to the LLM, not a protocol error).
	out, err = manager.CallTool(t.Context(), "fs", "fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsError {
		t.Fatal("failing tool must come back with IsError=true")
	}
	if text, ok := out.Content[0].(*mcp.TextContent); !ok || !strings.Contains(text.Text, "boom") {
		t.Fatalf("error text must be preserved: %#v", out.Content)
	}
}

func TestToolPrefixAndGlobFilters(t *testing.T) {
	fake := &fakeServer{name: "web"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"web": {Command: "unused", ToolPrefix: "web_", ExcludeTools: []string{"write_*", "fail"}},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	matches, err := manager.SearchTools(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, match := range matches {
		names = append(names, match.Tool.Name)
	}
	if got := strings.Join(names, ","); got != "web_echo,web_take_screenshot" {
		t.Fatalf("exclude globs + prefix not applied: %q", got)
	}

	// Display (prefixed) name resolves to the raw name on call.
	out, err := manager.CallTool(t.Context(), "web", "web_echo", map[string]any{"message": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if text := out.Content[0].(*mcp.TextContent); text.Text != "echo: x" {
		t.Fatalf("unexpected echo result: %#v", out.Content)
	}
	if _, err := manager.CallTool(t.Context(), "web", "write_file", nil); err == nil {
		t.Fatal("excluded tool must not be callable")
	}
}

func TestLazyConnectAndIdleReap(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused", IdleTimeoutMs: 80},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory: fake.spawn(t.Context()),
		ReapInterval:     20 * time.Millisecond,
	})

	if status := manager.Status()[0]; status.Status != statusDisconnected {
		t.Fatalf("lazy server must start disconnected, got %q", status.Status)
	}
	if _, err := manager.SearchTools(t.Context(), "echo", 0); err != nil {
		t.Fatal(err)
	}
	if status := manager.Status()[0]; status.Status != statusConnected {
		t.Fatalf("search must connect lazily, got %q", status.Status)
	}
	if fake.connects.Load() != 1 {
		t.Fatalf("want 1 connect, got %d", fake.connects.Load())
	}

	time.Sleep(250 * time.Millisecond)
	status := manager.Status()[0]
	if status.Status != statusDisconnected {
		t.Fatalf("idle server must be reaped, got %q", status.Status)
	}
	// Next use reconnects.
	if _, err := manager.CallTool(t.Context(), "fs", "echo", map[string]any{"message": "again"}); err != nil {
		t.Fatal(err)
	}
	if fake.connects.Load() != 2 {
		t.Fatalf("want 2 connects after reap, got %d", fake.connects.Load())
	}
}

func TestKeepAliveNotReaped(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused", Lifecycle: LifecycleLazyKeepAlive, IdleTimeoutMs: 50},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory: fake.spawn(t.Context()),
		ReapInterval:     20 * time.Millisecond,
	})
	if _, err := manager.SearchTools(t.Context(), "echo", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if status := manager.Status()[0]; status.Status != statusConnected {
		t.Fatalf("keep-alive server must not be reaped, got %q", status.Status)
	}
}

func TestStartEagerConnects(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused", Lifecycle: LifecycleEager},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})
	manager.StartEager()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if manager.Status()[0].Status == statusConnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status := manager.Status()[0]; status.Status != statusConnected {
		t.Fatalf("eager server must connect after StartEager, got %q", status.Status)
	}
	if fake.connects.Load() != 1 {
		t.Fatalf("want exactly 1 eager connect, got %d", fake.connects.Load())
	}
}

func TestRequestTimeout(t *testing.T) {
	fake := &fakeServer{name: "fs", slowTool: 2 * time.Second}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused", RequestTimeoutMs: 50},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	start := time.Now()
	_, err := manager.CallTool(t.Context(), "fs", "slow", nil)
	if err == nil {
		t.Fatal("slow tool must hit the request timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout must fire at ~50ms, took %v", elapsed)
	}
}

func TestBrokenServerDoesNotHideOthers(t *testing.T) {
	working := &fakeServer{name: "working"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"broken":  {Command: "/nonexistent/mcp-server-binary"},
		"working": {Command: "unused"},
	}}
	var logged []string
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory: func(server string, cfg ServerConfig) (mcp.Transport, error) {
			if server == "broken" {
				// Simulate a connect failure; the real stdio path surfaces
				// the same shape via build/connect errors.
				return nil, errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
			}
			return working.spawn(t.Context())("working", cfg)
		},
		Logger: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	})

	matches, err := manager.SearchTools(t.Context(), "echo", 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, match := range matches {
		if match.Tool.Server == "working" {
			found = true
		}
		if match.Tool.Server == "broken" {
			t.Fatal("broken server must contribute no tools")
		}
	}
	if !found {
		t.Fatal("working server's tools must still be searchable")
	}
	if _, err := manager.CallTool(t.Context(), "broken", "echo", nil); err == nil {
		t.Fatal("calling a broken server must fail")
	}
	status := manager.Status()[0]
	if status.Name != "broken" || status.Status != statusError || status.LastError == "" {
		t.Fatalf("broken server status must surface the error, got %+v", status)
	}
	if len(logged) == 0 {
		t.Fatal("debug logger must receive the failure")
	}
}

func TestUnknownServerAndManagerClose(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{"fs": {Command: "unused"}}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	if _, err := manager.CallTool(t.Context(), "nope", "echo", nil); err == nil || !strings.Contains(err.Error(), "unknown mcp server") {
		t.Fatalf("unknown server must error clearly, got %v", err)
	}
	// Ambiguous bare name across servers.
	other := &fakeServer{name: "fs2"}
	config.MCPServers["fs2"] = ServerConfig{Command: "unused"}
	otherManager := newTestManager(t, config, ManagerOptions{TransportFactory: func(server string, cfg ServerConfig) (mcp.Transport, error) {
		if server == "fs" {
			return fake.spawn(t.Context())("fs", cfg)
		}
		return other.spawn(t.Context())("fs2", cfg)
	}})
	if _, err := otherManager.CallTool(t.Context(), "", "echo", nil); err == nil || !strings.Contains(err.Error(), "unknown mcp server") {
		t.Fatalf("empty server must error, got %v", err)
	}

	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal("Close must be idempotent")
	}
	if _, err := manager.CallTool(t.Context(), "fs", "echo", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("calls after close must fail, got %v", err)
	}
}

// ---- stdio e2e: helper process ----

func TestMain(m *testing.M) {
	if os.Getenv("PIG_MCP_HELPER_PROCESS") == "1" {
		runHelperStdioServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHelperStdioServer runs a minimal MCP stdio server as a child process of
// the test binary (helper-process pattern; GO test helper precedent in repo).
func runHelperStdioServer() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "pig-mcp-helper", Version: "1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "helper_echo", Description: "echo helper"}, func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "helper: " + in.Message}}}, nil, nil
	})
	ctx := context.Background()
	_ = srv.Run(ctx, &mcp.StdioTransport{})
}

func TestStdioTransportE2E(t *testing.T) {
	if os.Getenv("PIG_MCP_HELPER_PROCESS") == "1" {
		t.Skip("helper process")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := &Config{MCPServers: map[string]ServerConfig{
		"helper": {Command: exe, Env: map[string]string{"PIG_MCP_HELPER_PROCESS": "1"}},
	}}
	manager := NewManager(config, ManagerOptions{ReapInterval: time.Hour})
	t.Cleanup(func() { _ = manager.Close() })

	matches, err := manager.SearchTools(t.Context(), "helper_echo", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 || matches[0].Tool.RawName != "helper_echo" {
		t.Fatalf("helper server tool not found: %+v", matches)
	}
	out, err := manager.CallTool(t.Context(), "helper", "helper_echo", map[string]any{"message": "stdio"})
	if err != nil {
		t.Fatal(err)
	}
	if text := out.Content[0].(*mcp.TextContent); text.Text != "helper: stdio" {
		t.Fatalf("unexpected result: %#v", out.Content)
	}

	// Dispose must tear down the child process.
	status := manager.Status()[0]
	if status.PID == 0 {
		t.Fatal("stdio server must report a PID")
	}
	pid := status.PID
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if processAlive(pid) {
		t.Fatalf("stdio child (pid %d) must be dead after Close", pid)
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Wait briefly for the exit to settle before probing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAliveOnce(pid) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return processAliveOnce(pid)
}

// ---- streamable HTTP e2e ----

func TestStreamableHTTPE2EWithHeaders(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "http-fake", Version: "1.0.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "http_tool", Description: "tool over http"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "over http"}}}, nil, nil
	})

	var gotAuth atomic.Value
	// One handler instance for the whole test: each instance keeps its own
	// session store, so per-request construction loses the session.
	httpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	handler := http.NewServeMux()
	handler.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		httpHandler.ServeHTTP(w, r)
	}))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	config := &Config{MCPServers: map[string]ServerConfig{
		"remote": {URL: ts.URL, Headers: map[string]string{"Authorization": "$SECRET_TOKEN"}},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		ResolveHeaders: func(server string, headers map[string]string) (map[string]string, error) {
			resolved := make(map[string]string, len(headers))
			for key, value := range headers {
				resolved[key] = strings.ReplaceAll(value, "$SECRET_TOKEN", "Bearer resolved-secret")
			}
			return resolved, nil
		},
	})

	out, err := manager.CallTool(t.Context(), "remote", "http_tool", nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := out.Content[0].(*mcp.TextContent); text.Text != "over http" {
		t.Fatalf("unexpected result: %#v", out.Content)
	}
	if got := gotAuth.Load(); got != "Bearer resolved-secret" {
		t.Fatalf("resolved header must reach the server, got %v", got)
	}
}

func TestHeaderTransportRejectFailure(t *testing.T) {
	config := &Config{MCPServers: map[string]ServerConfig{
		"remote": {URL: "http://unused", Headers: map[string]string{"Authorization": "!exit-1"}},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		// No TransportFactory: header resolution happens while building the
		// HTTP transport, before any dialing, so `http://unused` is never hit.
		ResolveHeaders: func(string, map[string]string) (map[string]string, error) {
			return nil, errors.New("command failed: exit 1")
		},
	})
	_, err := manager.CallTool(t.Context(), "remote", "http_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "resolve headers") {
		t.Fatalf("header resolution failure must surface, got %v", err)
	}
	if status := manager.Status()[0]; status.Status != statusError {
		t.Fatalf("server must be in error status, got %q", status.Status)
	}
}

// blockingTransport hangs in Connect until its context is canceled, simulating
// a server whose initialize never answers.
type blockingTransport struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestCloseAbortsInFlightConnect ports pi v1.0.4's #10249 fix semantics:
// close() aborts a connect in progress instead of waiting for it to settle,
// so shutdown does not block until the connect timeout.
func TestCloseAbortsInFlightConnect(t *testing.T) {
	blocker := &blockingTransport{started: make(chan struct{})}
	config := &Config{MCPServers: map[string]ServerConfig{
		"stuck": {Command: "irrelevant", Args: []string{}},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory: func(server string, _ ServerConfig) (mcp.Transport, error) {
			return blocker, nil
		},
		// Generous timeout: only the close-abort path can unblock the
		// connect in time.
		ConnectTimeout: 60 * time.Second,
	})

	connectErr := make(chan error, 1)
	go func() {
		connectErr <- manager.Reconnect(t.Context(), "stuck")
	}()
	select {
	case <-blocker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never reached the transport")
	}

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on the in-flight connect (v1.0.4 #10249 regression)")
	}
	if err := <-connectErr; err == nil {
		t.Fatal("aborted connect must report an error")
	}
	// pig semantics: per-server status goes disconnected; the manager-level
	// closed flag rejects further work.
	if st := manager.Status()[0]; st.Status != "disconnected" {
		t.Fatalf("status = %q, want disconnected", st.Status)
	}
	if _, err := manager.CallTool(t.Context(), "stuck", "echo", map[string]any{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("CallTool after Close = %v, want manager-closed error", err)
	}
}

// pi #10562: disabling a server whose connect is still in flight must not
// block until the connect timeout — the disable aborts the attempt first
// (pig shape of pi's attempt-invalidation + closing wait).
func TestSetServerDisabledAbortsInFlightConnect(t *testing.T) {
	blocker := &blockingTransport{started: make(chan struct{})}
	config := &Config{MCPServers: map[string]ServerConfig{
		"stuck": {Command: "irrelevant", Args: []string{}},
	}}
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory: func(server string, _ ServerConfig) (mcp.Transport, error) {
			return blocker, nil
		},
		ConnectTimeout: 60 * time.Second,
	})

	connectErr := make(chan error, 1)
	go func() {
		connectErr <- manager.Reconnect(t.Context(), "stuck")
	}()
	select {
	case <-blocker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("connect never reached the transport")
	}

	disabled := make(chan error, 1)
	go func() { disabled <- manager.SetServerDisabled("stuck", true) }()
	select {
	case err := <-disabled:
		if err != nil {
			t.Fatalf("SetServerDisabled: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("disable blocked on the in-flight connect (#10562 regression)")
	}
	if err := <-connectErr; err == nil {
		t.Fatal("aborted connect must report an error")
	}
	if st := manager.Status()[0]; st.Status != "disabled" {
		t.Fatalf("status = %q, want disabled", st.Status)
	}
	if _, err := manager.CallTool(t.Context(), "stuck", "echo", map[string]any{}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("CallTool on a disabled server = %v, want disabled error", err)
	}
}
