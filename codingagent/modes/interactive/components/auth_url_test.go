package components

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// Tests for ced72c2f0: the login dialog's auth URL carries a copy hint and
// app.message.copy (ctrl+x) copies it. pi's third case (the MCP manager view
// sign-in screen) has no pig face — MCP OAuth is not built.

var authURLTestURL = "https://auth.example.invalid/authorize?" + strings.Repeat("x", 300)

// authURLTestHarness serializes dialog mutation and rendering the way the
// production Mutator + TUI lock do (renderedLoginDialog reads the tree).
type authURLTestHarness struct {
	mu     sync.Mutex
	dialog *LoginDialogComponent
}

func newAuthURLTestDialog(t *testing.T) *authURLTestHarness {
	t.Helper()
	initTestTheme(t)
	// app.message.copy lives in the core-merged keybinding table (mcp_panel
	// harness precedent).
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)
	t.Cleanup(func() { tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager) })
	openBrowserFn = func(string) {}
	authURLCopyToClipboard = func(string) error { return nil }
	t.Cleanup(func() {
		openBrowserFn = utils.OpenBrowser
		authURLCopyToClipboard = utils.CopyToClipboard
	})
	h := &authURLTestHarness{}
	h.dialog = NewLoginDialogComponent(nil, "test", func(bool, string) {}, "", "")
	h.dialog.Mutator = func(fn func()) { h.mu.Lock(); defer h.mu.Unlock(); fn() }
	return h
}

func (h *authURLTestHarness) output() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return utils.StripAnsi(strings.Join(h.dialog.Render(80), "\n"))
}

func (h *authURLTestHarness) handleInput(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dialog.HandleInput(key)
}

func TestLoginDialogCopiesAuthURL(t *testing.T) {
	h := newAuthURLTestDialog(t)
	h.dialog.ShowAuth(authURLTestURL, "")
	h.mu.Lock()
	go func() { _, _ = h.dialog.ShowManualInput("Paste the code:") }()
	h.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(h.output(), "to copy") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if output := h.output(); !strings.Contains(output, "to copy") {
		t.Fatalf("auth URL must carry the copy hint, got %q", output)
	}

	var copied string
	authURLCopyToClipboard = func(text string) error { copied = text; return nil }

	h.handleInput("\x18")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(h.output(), "Copied URL to clipboard") && copied == authURLTestURL {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(h.output(), "Copied URL to clipboard") || copied != authURLTestURL {
		t.Fatalf("ctrl+x must copy the auth URL, copied=%q output=%q", copied, h.output())
	}
}

func TestLoginDialogCopyKeyWithoutAuthURLIgnored(t *testing.T) {
	h := newAuthURLTestDialog(t)
	var copies int
	authURLCopyToClipboard = func(string) error { copies++; return nil }

	h.dialog.ShowDeviceCode(ai.OAuthDeviceCodeInfo{UserCode: "ABCD", VerificationURI: "https://example.invalid/device"})
	h.handleInput("\x18")
	if copies != 0 {
		t.Fatal("ctrl+x must be ignored when no auth URL is shown")
	}
}
