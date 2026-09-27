package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDisabledServerSkipsAllPaths(t *testing.T) {
	fake := &fakeServer{name: "off"}
	config := &Config{
		Sources: map[string]string{"off": "/tmp/fake-mcp.json"},
		MCPServers: map[string]ServerConfig{
			"off": {Command: "unused", Disabled: true},
		},
	}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	if status := manager.Status()[0]; status.Status != statusDisabled || !status.Disabled || status.SourcePath != "/tmp/fake-mcp.json" {
		t.Fatalf("disabled server must report disabled status with source, got %+v", status)
	}
	// Lazy connect path: search and list must not connect it.
	if _, err := manager.SearchTools(t.Context(), "echo", 0); err != nil {
		t.Fatal(err)
	}
	if fake.connects.Load() != 0 {
		t.Fatal("disabled server must never connect on search")
	}
	if _, err := manager.ListServerTools(t.Context(), "off"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("ListServerTools on a disabled server must error, got %v", err)
	}
	if _, err := manager.CallTool(t.Context(), "off", "echo", nil); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("CallTool on a disabled server must error, got %v", err)
	}
	if fake.connects.Load() != 0 {
		t.Fatal("disabled server must never connect on call")
	}
	// Reconnect refuses too.
	if err := manager.Reconnect(t.Context(), "off"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Reconnect on a disabled server must error, got %v", err)
	}
}

func TestDisabledEagerServerNeverConnects(t *testing.T) {
	fake := &fakeServer{name: "off"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"off": {Command: "unused", Lifecycle: LifecycleEager, Disabled: true},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})
	manager.StartEager()
	time.Sleep(100 * time.Millisecond)
	if status := manager.Status()[0]; status.Status != statusDisabled {
		t.Fatalf("disabled eager server must stay disabled, got %q", status.Status)
	}
	if fake.connects.Load() != 0 {
		t.Fatal("disabled eager server must never connect")
	}
}

func TestReconnect(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{
		"fs": {Command: "unused", Lifecycle: LifecycleKeepAlive},
	}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	if _, err := manager.CallTool(t.Context(), "fs", "echo", map[string]any{"message": "first"}); err != nil {
		t.Fatal(err)
	}
	if fake.connects.Load() != 1 {
		t.Fatalf("want 1 connect, got %d", fake.connects.Load())
	}
	// keep-alive sessions are not reaped, so this must be an explicit
	// teardown + fresh connect.
	if err := manager.Reconnect(t.Context(), "fs"); err != nil {
		t.Fatal(err)
	}
	if fake.connects.Load() != 2 {
		t.Fatalf("reconnect must connect again, got %d connects", fake.connects.Load())
	}
	if _, err := manager.CallTool(t.Context(), "fs", "echo", map[string]any{"message": "second"}); err != nil {
		t.Fatalf("server must work after reconnect: %v", err)
	}

	if err := manager.Reconnect(t.Context(), "nope"); err == nil || !strings.Contains(err.Error(), "unknown mcp server") {
		t.Fatalf("unknown server must error clearly, got %v", err)
	}
}

func TestReconnectFailureSurfaces(t *testing.T) {
	working := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{"fs": {Command: "unused"}}}
	factory := working.spawn(t.Context())
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: factory})
	if _, err := manager.CallTool(t.Context(), "fs", "echo", nil); err != nil {
		t.Fatal(err)
	}
	// Break the factory after the first connect: the explicit reconnect must
	// surface the fresh connect error (not the stale session).
	manager.opts.TransportFactory = func(string, ServerConfig) (mcp.Transport, error) {
		return nil, errors.New("dial refused")
	}
	if err := manager.Reconnect(t.Context(), "fs"); err == nil || !strings.Contains(err.Error(), "dial refused") {
		t.Fatalf("reconnect after factory failure must surface the error, got %v", err)
	}
	// The failure is visible in status.
	if status := manager.Status()[0]; status.Status != statusError || status.LastError == "" {
		t.Fatalf("failed reconnect must land in error status, got %+v", status)
	}
}

func TestSetServerDisabledFileSurgery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	original := `{
  "mcpServers": {
    "a": {
      "command": "cmd-a",
      "args": ["-v"],
      "customFutureField": {"nested": [1, 2]},
      "disabled": true
    },
    "b": {
      "url": "https://example.com/mcp?x=1&y=2"
    }
  },
  "topLevelUnknown": 12345678901234567890
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	config, diags, err := LoadConfigFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if config.Sources["a"] != path || config.Sources["b"] != path {
		t.Fatalf("sources must record the winner file, got %+v", config.Sources)
	}
	fake := &fakeServer{name: "a"}
	fakeB := &fakeServer{name: "b"}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: func(server string, cfg ServerConfig) (mcp.Transport, error) {
		if server == "a" {
			return fake.spawn(t.Context())("a", cfg)
		}
		return fakeB.spawn(t.Context())("b", cfg)
	}})

	// Enable a: the "disabled": true key must be deleted, siblings and
	// unknown fields preserved.
	if err := manager.SetServerDisabled("a", false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "disabled") {
		t.Fatalf("enabling must delete the disabled key, got:\n%s", text)
	}
	for _, want := range []string{"\"customFutureField\"", "\"nested\"", "cmd-a", "https://example.com/mcp?x=1&y=2", "topLevelUnknown", "12345678901234567890"} {
		if !strings.Contains(text, want) {
			t.Fatalf("surgery must preserve %q, got:\n%s", want, text)
		}
	}
	if !strings.HasSuffix(text, "}\n") {
		t.Fatalf("file must end with a trailing newline, got %q", text[len(text)-10:])
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode must be preserved, got %v %v", info.Mode().Perm(), err)
	}

	// Disable b: key written back; the manager tears the session down.
	if err := manager.SetServerDisabled("b", true); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "\"disabled\": true") {
		t.Fatalf("disabling must write the key, got:\n%s", content)
	}
	if status := manager.Status()[1]; status.Name != "b" || status.Status != statusDisabled {
		t.Fatalf("b must be disabled in memory, got %+v", status)
	}

	// Re-enabling b reconnects lazily and works.
	if err := manager.SetServerDisabled("b", false); err != nil {
		t.Fatal(err)
	}
	if status := manager.Status()[1]; status.Status != statusDisconnected {
		t.Fatalf("re-enabled server must go back to disconnected, got %q", status.Status)
	}
	if _, err := manager.ListServerTools(t.Context(), "b"); err != nil {
		t.Fatalf("re-enabled server must connect lazily: %v", err)
	}
}

func TestSetServerDisabledWithoutSourceIsMemoryOnly(t *testing.T) {
	fake := &fakeServer{name: "fs"}
	config := &Config{MCPServers: map[string]ServerConfig{"fs": {Command: "unused"}}}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})
	if _, err := manager.CallTool(t.Context(), "fs", "echo", nil); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetServerDisabled("fs", true); err != nil {
		t.Fatal(err)
	}
	if status := manager.Status()[0]; status.Status != statusDisabled {
		t.Fatalf("in-memory disable must report disabled, got %q", status.Status)
	}
	if fake.connects.Load() != 1 {
		t.Fatal("disable must tear the session down (no new connect)")
	}
	if _, err := manager.CallTool(t.Context(), "fs", "echo", nil); err == nil {
		t.Fatal("disabled server must refuse calls")
	}
	if err := manager.SetServerDisabled("fs", false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CallTool(t.Context(), "fs", "echo", map[string]any{"message": "back"}); err != nil {
		t.Fatalf("re-enabled server must work: %v", err)
	}
	if err := manager.SetServerDisabled("nope", true); err == nil || !strings.Contains(err.Error(), "unknown mcp server") {
		t.Fatalf("unknown server must error clearly, got %v", err)
	}
}

func TestLoadConfigFilesRecordsSourcesLaterWins(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	if err := os.WriteFile(first, []byte(`{"mcpServers":{"a":{"command":"cmd-1"},"b":{"command":"cmd-b"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`{"mcpServers":{"a":{"command":"cmd-2"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := LoadConfigFiles([]string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if config.MCPServers["a"].Command != "cmd-2" {
		t.Fatalf("later file must win, got %+v", config.MCPServers["a"])
	}
	if config.Sources["a"] != second {
		t.Fatalf("source must point at the winner file, got %q", config.Sources["a"])
	}
	if config.Sources["b"] != first {
		t.Fatalf("uncontested server keeps its own source, got %q", config.Sources["b"])
	}
}
