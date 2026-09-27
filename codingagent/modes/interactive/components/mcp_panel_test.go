// mcp_panel_test.go — pig-only tests for the /mcp panel (pi has no native
// MCP; the fake provider stands in for *mcp.Manager, whose interface
// satisfaction is pinned in mcp_integration_test.go). Tests pass
// RefreshInterval -1 so no ticker goroutine runs; the Mutate hook is a real
// mutex, and the harness routes every interaction through it exactly the way
// the TUI lock serializes them in production (HandleInput acquires it via
// p.mutate, Render is wrapped manually).
package components

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	pigmcp "github.com/ZoroSpace/pig/integrations/mcp"
	"github.com/ZoroSpace/pig/tui"
)

const (
	mcpKeyEnter  = "\r"
	mcpKeyUp     = "\x1b[A"
	mcpKeyDown   = "\x1b[B"
	mcpKeyEscape = "\x1b"
	mcpKeyCtrlR  = "\x12"
	mcpKeyCtrlD  = "\x04"
	mcpKeyCtrlY  = "\x19"

	// mcpRefreshDisabled is the RefreshInterval sentinel: no ticker.
	mcpRefreshDisabled = -1 * time.Second
)

// *mcp.Manager must satisfy the panel's provider interface.
var _ MCPPanelProvider = (*pigmcp.Manager)(nil)

type fakeMCPPanelProvider struct {
	mu        sync.Mutex
	statuses  []pigmcp.ServerStatus
	tools     map[string][]pigmcp.ToolInfo
	listErr   map[string]error
	reconnErr map[string]error
	disabled  map[string]bool
	calls     []string
}

func newFakeMCPProvider() *fakeMCPPanelProvider {
	return &fakeMCPPanelProvider{
		statuses: []pigmcp.ServerStatus{
			{Name: "fs", Transport: "stdio", Lifecycle: "lazy", Status: "connected", ToolCount: 2, IdleMs: 5_000},
			{Name: "broken", Transport: "http", Lifecycle: "eager", Status: "error", LastError: "dial refused (stderr: boom)"},
		},
		tools: map[string][]pigmcp.ToolInfo{
			"fs": {
				{Server: "fs", Name: "fs_echo", RawName: "echo", Description: "echo it back"},
				{Server: "fs", Name: "fs_add", RawName: "add", Description: "add numbers"},
			},
		},
		disabled: map[string]bool{},
	}
}

func (f *fakeMCPPanelProvider) Status() []pigmcp.ServerStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pigmcp.ServerStatus(nil), f.statuses...)
}

func (f *fakeMCPPanelProvider) setStatuses(statuses []pigmcp.ServerStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = statuses
}

func (f *fakeMCPPanelProvider) ListServerTools(_ context.Context, server string) ([]pigmcp.ToolInfo, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "list:"+server)
	tools := f.tools[server]
	listErr := f.listErr[server]
	disabled := f.disabled[server]
	f.mu.Unlock()
	if disabled {
		return nil, fmt.Errorf("mcp server %q is disabled", server)
	}
	if listErr != nil {
		return nil, listErr
	}
	return tools, nil
}

func (f *fakeMCPPanelProvider) Reconnect(_ context.Context, server string) error {
	f.mu.Lock()
	f.calls = append(f.calls, "reconnect:"+server)
	err := f.reconnErr[server]
	f.mu.Unlock()
	return err
}

func (f *fakeMCPPanelProvider) SetServerDisabled(server string, disabled bool) error {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("disable:%s=%v", server, disabled))
	f.disabled[server] = disabled
	f.mu.Unlock()
	return nil
}

// mcpPanelHarness serializes panel access the way the TUI lock does.
type mcpPanelHarness struct {
	t        *testing.T
	mu       *sync.Mutex
	panel    *MCPPanelComponent
	provider *fakeMCPPanelProvider
}

func newMCPPanelHarness(t *testing.T, provider *fakeMCPPanelProvider, extra func(*MCPPanelOptions)) *mcpPanelHarness {
	t.Helper()
	initTestTheme(t)
	// The app.mcp.* panel keys live in the core-merged keybinding table;
	// install it (scoped_models_selector_test precedent).
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)
	t.Cleanup(func() { tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager) })

	var mu sync.Mutex
	opts := MCPPanelOptions{
		Provider:        provider,
		Mutate:          func(fn func()) { mu.Lock(); defer mu.Unlock(); fn() },
		RequestRender:   func() {},
		CopyToClipboard: func(string) error { return nil },
		RefreshInterval: mcpRefreshDisabled,
	}
	if extra != nil {
		extra(&opts)
	}
	panel := NewMCPPanelComponent(opts)
	t.Cleanup(panel.Dispose)
	return &mcpPanelHarness{t: t, mu: &mu, panel: panel, provider: provider}
}

func (h *mcpPanelHarness) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return utils.StripAnsi(strings.Join(h.panel.Render(120), "\n"))
}

func (h *mcpPanelHarness) input(key string) {
	h.panel.HandleInput(key)
}

func (h *mcpPanelHarness) refresh() {
	h.panel.refreshNow()
}

// waitFor polls until cond passes (async fetch/reconnect callbacks run on
// goroutines).
func (h *mcpPanelHarness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("condition %q did not become true within 3s (output=%q)", what, h.output())
}

func TestMCPPanelRendersStatusSuffixes(t *testing.T) {
	provider := newFakeMCPProvider()
	provider.statuses[0].Status = "connecting"
	provider.statuses[0].IdleMs = 0
	provider.statuses = append(provider.statuses, pigmcp.ServerStatus{Name: "off", Transport: "stdio", Status: "disconnected", Disabled: true})
	h := newMCPPanelHarness(t, provider, nil)
	h.refresh()

	output := h.output()
	for _, want := range []string{"MCP Servers", "fs", "connecting", "failed", "dial refused (stderr: boom)", "disabled"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output = %q, want it to contain %q", output, want)
		}
	}
}

func TestMCPPanelRendersConnectedIdleSuffix(t *testing.T) {
	h := newMCPPanelHarness(t, newFakeMCPProvider(), nil)
	output := h.output()
	if !strings.Contains(output, "connected") || !strings.Contains(output, "idle 5s") {
		t.Fatalf("connected + idle suffix must render, got %q", output)
	}
}

func TestMCPPanelExpandListsToolsLazily(t *testing.T) {
	h := newMCPPanelHarness(t, newFakeMCPProvider(), nil)

	// Navigate to fs (rows sort alphabetically: broken, its error row, fs),
	// then enter expands it (fetch runs on a goroutine; the tools land when
	// it completes).
	h.input(mcpKeyDown)
	h.input(mcpKeyDown)
	h.input(mcpKeyEnter)

	h.waitFor("expanded tools", func() bool {
		return strings.Contains(h.output(), "fs_echo")
	})
	output := h.output()
	for _, want := range []string{"echo it back", "fs_add"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expanded tools must render, output = %q", output)
		}
	}
	h.provider.mu.Lock()
	listed := strings.Join(h.provider.calls, ",")
	h.provider.mu.Unlock()
	if !strings.Contains(listed, "list:fs") {
		t.Fatalf("expand must list tools, calls = %s", listed)
	}

	// Enter again collapses.
	h.input(mcpKeyEnter)
	h.waitFor("collapsed", func() bool {
		return !strings.Contains(h.output(), "fs_echo")
	})
}

func TestMCPPanelExpandFailedListingShowsErrorRow(t *testing.T) {
	provider := newFakeMCPProvider()
	provider.listErr = map[string]error{"fs": errors.New("connect mcp server \"fs\": refused")}
	h := newMCPPanelHarness(t, provider, nil)

	h.input(mcpKeyDown)
	h.input(mcpKeyDown)
	h.input(mcpKeyEnter)
	h.waitFor("listing failure", func() bool {
		return strings.Contains(h.output(), "listing tools failed")
	})
	if !strings.Contains(h.output(), "refused") {
		t.Fatal("listing failure detail must render")
	}
}

func TestMCPPanelFiltersServers(t *testing.T) {
	h := newMCPPanelHarness(t, newFakeMCPProvider(), nil)

	for _, ch := range "bro" {
		h.input(string(ch))
	}
	output := h.output()
	if strings.Contains(output, "fs_echo") || !strings.Contains(output, "broken") {
		t.Fatalf("filter must narrow to 'broken', got %q", output)
	}
}

func TestMCPPanelReconnectAction(t *testing.T) {
	provider := newFakeMCPProvider()
	h := newMCPPanelHarness(t, provider, nil)
	h.input(mcpKeyDown)
	h.input(mcpKeyDown)

	h.input(mcpKeyCtrlR)
	h.waitFor("reconnect notice", func() bool {
		return strings.Contains(h.output(), "reconnected fs")
	})
	provider.mu.Lock()
	calls := strings.Join(provider.calls, ",")
	provider.mu.Unlock()
	if !strings.Contains(calls, "reconnect:fs") {
		t.Fatalf("ctrl+r must call Reconnect, calls = %s", calls)
	}

	// Failure surfaces in the notice line.
	provider.reconnErr = map[string]error{"fs": errors.New("still down")}
	h.input(mcpKeyCtrlR)
	h.waitFor("reconnect failure", func() bool {
		return strings.Contains(h.output(), "still down")
	})
}

func TestMCPPanelToggleDisabledAction(t *testing.T) {
	provider := newFakeMCPProvider()
	h := newMCPPanelHarness(t, provider, nil)
	h.input(mcpKeyDown)
	h.input(mcpKeyDown)

	h.input(mcpKeyCtrlD)
	h.waitFor("disable notice", func() bool {
		output := h.output()
		return strings.Contains(output, "disabled fs") && strings.Contains(output, "saved to mcp.json")
	})
	if provider.disabled["fs"] != true {
		t.Fatal("ctrl+d must disable the selected server")
	}
	// The refreshed status shows the disabled suffix once the provider
	// reflects it (the real manager flips it in the same call).
	provider.mu.Lock()
	for i := range provider.statuses {
		if provider.statuses[i].Name == "fs" {
			provider.statuses[i].Disabled = true
			provider.statuses[i].Status = "disabled"
		}
	}
	provider.mu.Unlock()
	h.refresh()
	if output := h.output(); !strings.Contains(output, "disabled") {
		t.Fatalf("disabled suffix must render, got %q", output)
	}

	h.input(mcpKeyCtrlD)
	h.waitFor("enable notice", func() bool {
		return strings.Contains(h.output(), "enabled fs")
	})
	if provider.disabled["fs"] != false {
		t.Fatal("second ctrl+d must re-enable")
	}
}

func TestMCPPanelCopyErrorAction(t *testing.T) {
	provider := newFakeMCPProvider()
	var copied string
	h := newMCPPanelHarness(t, provider, func(o *MCPPanelOptions) {
		o.CopyToClipboard = func(text string) error { copied = text; return nil }
	})

	// The broken server row is selected by default (alphabetical order);
	// copy its error.
	h.input(mcpKeyCtrlY)
	if copied != "dial refused (stderr: boom)" {
		t.Fatalf("ctrl+y must copy LastError, got %q", copied)
	}
	h.waitFor("copy notice", func() bool {
		return strings.Contains(h.output(), "error copied to clipboard")
	})

	// A server without an error shows the "nothing to copy" notice: navigate
	// to fs (past the broken server's error row).
	h.input(mcpKeyDown)
	h.input(mcpKeyDown)
	h.input(mcpKeyCtrlY)
	h.waitFor("no-copy notice", func() bool {
		return strings.Contains(h.output(), "no error to copy for fs")
	})
}

func TestMCPPanelEscapeCancels(t *testing.T) {
	cancelled := false
	h := newMCPPanelHarness(t, newFakeMCPProvider(), func(o *MCPPanelOptions) {
		o.OnCancel = func() { cancelled = true }
	})
	h.input(mcpKeyEscape)
	if !cancelled {
		t.Fatal("esc must invoke OnCancel")
	}
}

func TestMCPPanelRefreshSnapshotsProvider(t *testing.T) {
	provider := newFakeMCPProvider()
	h := newMCPPanelHarness(t, provider, nil)
	provider.setStatuses([]pigmcp.ServerStatus{{Name: "late", Transport: "http", Status: "connected"}})
	h.refresh()
	if output := h.output(); !strings.Contains(output, "late") {
		t.Fatalf("refresh must pick up new provider snapshot, got %q", output)
	}
}

func TestMCPPanelSelectionSurvivesRefresh(t *testing.T) {
	h := newMCPPanelHarness(t, newFakeMCPProvider(), nil)

	// broken is selected by default (alphabetical order)
	h.refresh()
	h.input(mcpKeyCtrlY)
	if output := h.output(); strings.Contains(output, "no error to copy") {
		t.Fatalf("selection must survive refresh (broken still selected), got %q", output)
	}
}
