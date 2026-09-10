// trust_selector_test.go (pi: test/trust-selector.test.ts) — the /trust
// option list, saved-decision marker, and selection flow. Ported in S5b
// (existed at the pi baseline; pig deferred it to this batch, ported with the
// v0.85.1 marker-position changes).
package components

import (
	"strings"
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

func TestTrustSelectorKeepsSavedDecisionMarkedWhileBrowsing(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	selector := NewTrustSelectorComponent(TrustSelectorOptions{
		Cwd:            "/project",
		SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/project", Decision: true},
		ProjectTrusted: true,
		OnSelect:       func(TrustSelection) {},
		OnCancel:       func() {},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "Saved decision: trusted (/project)") {
		t.Fatalf("missing saved decision line:\n%s", output)
	}
	if !strings.Contains(output, "Current session: trusted") {
		t.Fatalf("missing current session line:\n%s", output)
	}
	if !strings.Contains(output, "→ ✓ Trust") {
		t.Fatalf("expected marked Trust row:\n%s", output)
	}

	selector.HandleInput("\x1b[B")
	output = utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "✓ Trust") {
		t.Fatalf("expected Trust to stay marked while browsing:\n%s", output)
	}
	if !strings.Contains(output, "→   Trust parent folder (/)") {
		t.Fatalf("expected cursor on parent-folder option:\n%s", output)
	}
	if strings.Contains(output, "✓ Do not trust") {
		t.Fatal("Do not trust must not be marked")
	}
}

func TestTrustSelectorSelectsDecision(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	var selections []TrustSelection
	selector := NewTrustSelectorComponent(TrustSelectorOptions{
		Cwd:            "/project",
		SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/project", Decision: true},
		ProjectTrusted: true,
		OnSelect: func(s TrustSelection) {
			selections = append(selections, s)
		},
		OnCancel: func() {},
	})
	_ = selector

	// Move to "Do not trust" and confirm.
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\r")
	if len(selections) != 1 || selections[0].Trusted {
		t.Fatalf("expected untrusted selection, got %+v", selections)
	}
}

func TestTrustSelectorShowsInheritedDecision(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	var selections []TrustSelection
	selector := NewTrustSelectorComponent(TrustSelectorOptions{
		Cwd:            "/parent/project",
		SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/parent", Decision: true},
		ProjectTrusted: true,
		OnSelect: func(s TrustSelection) {
			selections = append(selections, s)
		},
		OnCancel: func() {},
	})

	output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
	if !strings.Contains(output, "Saved decision: trusted (inherited from /parent)") {
		t.Fatalf("missing inherited decision line:\n%s", output)
	}
	if !strings.Contains(output, "✓ Trust parent folder (/parent)") {
		t.Fatalf("expected marked parent option:\n%s", output)
	}

	selector.HandleInput("\r")
	if len(selections) != 1 || !selections[0].Trusted {
		t.Fatalf("expected trusted selection, got %+v", selections)
	}
}
