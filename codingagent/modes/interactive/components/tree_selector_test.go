// tree_selector_test.go (pi: test/tree-selector.test.ts) — tree selector
// selection/filter/fold/copy/label-timestamp behavior tests.
package components

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func stripVT(t *testing.T, s string) string {
	t.Helper()
	return stripAnsiForTest(s)
}

// stripAnsiForTest strips ANSI escapes (pi tests: stripVTControlCharacters).
func stripAnsiForTest(s string) string {
	// Reuse the same regex family as pi's stripVTControlCharacters.
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			// Skip CSI/OSC/APC sequences.
			j := i + 1
			if j < len(s) && s[j] == ']' {
				// OSC ... BEL or ST
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j < len(s) && s[j] == 0x07 {
					j++
				} else if j+1 < len(s) {
					j += 2
				}
				i = j
				continue
			}
			if j < len(s) && (s[j] == 'P' || s[j] == '_' || s[j] == '^') {
				for j < len(s) && s[j] != 0x07 && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				if j < len(s) && s[j] == 0x07 {
					j++
				} else if j+1 < len(s) {
					j += 2
				}
				i = j
				continue
			}
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] >= 0x20 && s[j] <= 0x3f) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			// Single-char escape
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// treeTestUserMessage creates a user message entry (pi: userMessage helper).
func treeTestUserMessage(id string, parentID *string, content string) *core.SessionMessageEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: id, ParentID: parentID, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		Message: types.UserMessage{
			Role:    types.RoleUser,
			Content: types.UserContent{String: content},
		},
	}
}

// treeTestAssistantMessage creates an assistant message entry (pi:
// assistantMessage helper).
func treeTestAssistantMessage(id string, parentID *string, text string) *core.SessionMessageEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: id, ParentID: parentID, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		Message: types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent(text)},
			API:        "anthropic-messages",
			Provider:   "anthropic",
			Model:      "claude-sonnet-4",
			Usage:      types.Usage{Cost: types.Cost{}},
			StopReason: types.StopReasonStop,
		},
	}
}

// treeTestToolCallOnlyAssistant creates a tool-call-only assistant message
// (filtered out in default mode; pi: toolCallOnlyAssistant helper).
func treeTestToolCallOnlyAssistant(id string, parentID *string) *core.SessionMessageEntry {
	return &core.SessionMessageEntry{
		EntryBase: core.EntryBase{Type: "message", ID: id, ParentID: parentID, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		Message: types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.Content{
				types.NewToolCall("tc-"+id, "read", map[string]any{"path": "test.ts"}),
			},
			API:        "anthropic-messages",
			Provider:   "anthropic",
			Model:      "claude-sonnet-4",
			Usage:      types.Usage{Cost: types.Cost{}},
			StopReason: types.StopReasonToolUse,
		},
	}
}

// treeTestModelChange creates a model_change entry (pi: modelChange helper).
func treeTestModelChange(id string, parentID *string) *core.ModelChangeEntry {
	return &core.ModelChangeEntry{
		EntryBase: core.EntryBase{Type: "model_change", ID: id, ParentID: parentID, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		Provider:  "anthropic",
		ModelID:   "claude-sonnet-4",
	}
}

// treeTestThinkingChange creates a thinking_level_change entry (pi: inline
// object in tests).
func treeTestThinkingChange(id string, parentID *string) *core.ThinkingLevelChangeEntry {
	return &core.ThinkingLevelChangeEntry{
		EntryBase:     core.EntryBase{Type: "thinking_level_change", ID: id, ParentID: parentID, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		ThinkingLevel: "high",
	}
}

// treeTestBuildTree builds a tree from entries using parentId relationships
// (pi: buildTree helper).
func treeTestBuildTree(entries []core.SessionEntry) []*core.SessionTreeNode {
	if len(entries) == 0 {
		return nil
	}

	nodes := make([]*core.SessionTreeNode, 0, len(entries))
	for _, entry := range entries {
		nodes = append(nodes, &core.SessionTreeNode{Entry: entry})
	}

	byID := map[string]*core.SessionTreeNode{}
	for _, node := range nodes {
		byID[node.Entry.Base().ID] = node
	}

	var roots []*core.SessionTreeNode
	for _, node := range nodes {
		if node.Entry.Base().ParentID == nil {
			roots = append(roots, node)
		} else if parent, ok := byID[*node.Entry.Base().ParentID]; ok {
			parent.Children = append(parent.Children, node)
		}
	}
	return roots
}

func strPtr(s string) *string { return &s }

// treeTestSelectedID returns the selected node id, "" = undefined.
func treeTestSelectedID(selector *TreeSelectorComponent) string {
	node := selector.GetTreeList().GetSelectedNode()
	if node == nil {
		return ""
	}
	return node.Entry.Base().ID
}

func TestTreeSelectorInitialSelectionWithMetadataEntries(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("focuses nearest visible ancestor when currentLeafId is a model_change with sibling branch", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			treeTestUserMessage("user-2", strPtr("asst-1"), "active branch"),  // Active branch
			treeTestModelChange("model-1", strPtr("user-2")),                  // Current leaf (metadata)
			treeTestUserMessage("user-3", strPtr("asst-1"), "sibling branch"), // Sibling branch
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "model-1", 24, func(string) {}, func() {}, nil, "", "")

		// Should focus on user-2 (parent of model-1), not user-3 (last item)
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}
	})

	t.Run("hides context edits by default and labels them in all mode", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			&core.ContextEditEntry{
				EntryBase: core.EntryBase{Type: "context_edit", ID: "edit-1", ParentID: strPtr("asst-1"), Timestamp: time.Now().UTC().Format(time.RFC3339)},
				TargetID:  "asst-1",
			},
		}
		tree := treeTestBuildTree(entries)

		defaultSelector := NewTreeSelectorComponent(tree, "edit-1", 24, func(string) {}, func() {}, nil, "", "")
		if got := treeTestSelectedID(defaultSelector); got != "asst-1" {
			t.Fatalf("selected = %q, want asst-1", got)
		}

		allSelector := NewTreeSelectorComponent(tree, "edit-1", 24, func(string) {}, func() {}, nil, "", FilterModeAll)
		rendered := strings.Join(allSelector.GetTreeList().Render(200), "\n")
		if !strings.Contains(stripVT(t, rendered), "[context omit: asst-1]") {
			t.Fatalf("rendered missing [context omit: asst-1], got %q", stripVT(t, rendered))
		}
	})

	t.Run("focuses nearest visible ancestor when currentLeafId is a thinking_level_change entry", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			treeTestUserMessage("user-2", strPtr("asst-1"), "active branch"),
			treeTestThinkingChange("thinking-1", strPtr("user-2")),
			treeTestUserMessage("user-3", strPtr("asst-1"), "sibling branch"),
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "thinking-1", 24, func(string) {}, func() {}, nil, "", "")

		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}
	})
}

func TestTreeSelectorFilterSwitchingWithParentTraversal(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("switches to nearest visible user message when changing to user-only filter", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			treeTestUserMessage("user-2", strPtr("asst-1"), "active branch"),
			treeTestAssistantMessage("asst-2", strPtr("user-2"), "response"),
			treeTestUserMessage("user-3", strPtr("asst-1"), "sibling branch"),
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "asst-2", 24, func(string) {}, func() {}, nil, "", "")
		if got := treeTestSelectedID(selector); got != "asst-2" {
			t.Fatalf("selected = %q, want asst-2", got)
		}

		// Simulate Ctrl+U (user-only filter)
		selector.HandleInput("\x15")

		// Should now be on user-2 (the parent user message), not user-3
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}
	})

	t.Run("returns to nearest visible ancestor when switching back to default filter", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			treeTestUserMessage("user-2", strPtr("asst-1"), "active branch"),
			treeTestAssistantMessage("asst-2", strPtr("user-2"), "response"),
			treeTestUserMessage("user-3", strPtr("asst-1"), "sibling branch"),
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "asst-2", 24, func(string) {}, func() {}, nil, "", "")
		if got := treeTestSelectedID(selector); got != "asst-2" {
			t.Fatalf("selected = %q, want asst-2", got)
		}

		// Switch to user-only
		selector.HandleInput("\x15") // Ctrl+U
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}

		// Switch back to default - should stay on user-2
		selector.HandleInput("\x04") // Ctrl+D
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}
	})
}

func TestTreeSelectorHelp(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("renders semantic help rows without truncating narrow terminal controls", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
		}
		tree := treeTestBuildTree(entries)
		selector := NewTreeSelectorComponent(tree, "asst-1", 24, func(string) {}, func() {}, nil, "", "")

		lines := selector.Render(30)
		plainLines := make([]string, 0, len(lines))
		for _, line := range lines {
			plainLines = append(plainLines, stripVT(t, line))
		}
		plain := strings.Join(plainLines, "\n")
		for _, want := range []string{"branch", "copy", "filters", "cycle", "label time"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("rendered help missing %q:\n%s", want, plain)
			}
		}
		if strings.Contains(plain, "...") {
			t.Fatalf("rendered help contains truncation ellipsis:\n%s", plain)
		}
		for _, line := range plainLines {
			if tui.VisibleWidth(line) > 30 {
				t.Fatalf("line width %d exceeds 30: %q", tui.VisibleWidth(line), line)
			}
		}
	})
}

func TestTreeSelectorCopy(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("copies the full selected message with ctrl+x", func(t *testing.T) {
		message := strings.Repeat("long message ", 30) + "\nsecond line"
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), message),
		}
		tree := treeTestBuildTree(entries)
		selector := NewTreeSelectorComponent(tree, "asst-1", 24, func(string) {}, func() {}, nil, "", "")
		copied := ""
		called := false
		selector.OnCopy = func(text string) {
			copied = text
			called = true
		}

		selector.HandleInput("\x18")

		if !called {
			t.Fatal("onCopy not called")
		}
		if copied != message {
			t.Fatalf("copied = %q, want %q", copied, message)
		}
	})
}

func TestTreeSelectorLabelTimestamps(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("toggles label timestamps for labeled nodes", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
		}
		tree := treeTestBuildTree(entries)
		labelDate := time.Date(2026, 3, 28, 14, 32, 0, 0, time.Local)
		tree[0].Label = strPtr("checkpoint")
		tree[0].LabelTimestamp = strPtr(labelDate.Format(time.RFC3339))

		selector := NewTreeSelectorComponent(tree, "asst-1", 24, func(string) {}, func() {}, nil, "", "")

		render := strings.Join(selector.Render(200), "\n")
		if !strings.Contains(render, "[checkpoint]") {
			t.Fatalf("render missing [checkpoint]:\n%s", render)
		}
		if strings.Contains(stripVT(t, render), "3/28 14:32") {
			t.Fatalf("render should not show label timestamps yet:\n%s", stripVT(t, render))
		}
		if strings.Contains(render, "[+label time]") {
			t.Fatalf("render should not show [+label time] yet:\n%s", render)
		}

		selector.HandleInput("T")

		render = strings.Join(selector.Render(200), "\n")
		if !strings.Contains(stripVT(t, render), "3/28 14:32") {
			t.Fatalf("render missing label timestamp:\n%s", stripVT(t, render))
		}
		if !strings.Contains(render, "[+label time]") {
			t.Fatalf("render missing [+label time]:\n%s", render)
		}
	})
}

func TestTreeSelectorEmptyFilterPreservation(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	t.Run("preserves selection when switching to empty labeled filter and back", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
			treeTestUserMessage("user-2", strPtr("asst-1"), "bye"),
			treeTestAssistantMessage("asst-2", strPtr("user-2"), "goodbye"),
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "asst-2", 24, func(string) {}, func() {}, nil, "", "")
		if got := treeTestSelectedID(selector); got != "asst-2" {
			t.Fatalf("selected = %q, want asst-2", got)
		}

		// Switch to labeled-only filter (no labels exist, so empty result)
		selector.HandleInput("\x0c") // Ctrl+L

		// The list should be empty, getSelectedNode returns undefined
		if selector.GetTreeList().GetSelectedNode() != nil {
			t.Fatal("expected empty selection after labeled-only filter")
		}

		// Switch back to default filter
		selector.HandleInput("\x04") // Ctrl+D

		// Should restore to asst-2 (the selection before we switched to empty filter)
		if got := treeTestSelectedID(selector); got != "asst-2" {
			t.Fatalf("selected = %q, want asst-2", got)
		}
	})

	t.Run("preserves selection through multiple empty filter switches", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "hi"),
		}
		tree := treeTestBuildTree(entries)

		selector := NewTreeSelectorComponent(tree, "asst-1", 24, func(string) {}, func() {}, nil, "", "")
		if got := treeTestSelectedID(selector); got != "asst-1" {
			t.Fatalf("selected = %q, want asst-1", got)
		}

		// Switch to labeled-only (empty) - Ctrl+L toggles labeled ↔ default
		selector.HandleInput("\x0c") // Ctrl+L -> labeled-only
		if selector.GetTreeList().GetSelectedNode() != nil {
			t.Fatal("expected empty selection after labeled-only filter")
		}

		// Switch to default, then back to labeled-only
		selector.HandleInput("\x0c") // Ctrl+L -> default (toggle back)
		if got := treeTestSelectedID(selector); got != "asst-1" {
			t.Fatalf("selected = %q, want asst-1", got)
		}

		selector.HandleInput("\x0c") // Ctrl+L -> labeled-only again
		if selector.GetTreeList().GetSelectedNode() != nil {
			t.Fatal("expected empty selection after labeled-only filter")
		}

		// Switch back to default with Ctrl+D
		selector.HandleInput("\x04") // Ctrl+D
		if got := treeTestSelectedID(selector); got != "asst-1" {
			t.Fatalf("selected = %q, want asst-1", got)
		}
	})
}

// treeTestBranchingTree builds the branch-navigation fixture (pi:
// buildBranchingTree).
//
// Tree structure:
//
//	user-1
//	asst-1
//	user-2
//	asst-2          ← branch point (has 2 children)
//	├─ user-3a      ← branch A (active: leaf is asst-4a)
//	│  asst-3a
//	│  user-4a
//	│  asst-4a
//	└─ user-3b      ← branch B
//	   asst-3b
//	   user-4b
//
// Foldable nodes: user-1 (root), user-3a (segment start), user-3b (segment start)
func treeTestBranchingTree() []*core.SessionTreeNode {
	entries := []core.SessionEntry{
		treeTestUserMessage("user-1", nil, "first message"),
		treeTestAssistantMessage("asst-1", strPtr("user-1"), "response 1"),
		treeTestUserMessage("user-2", strPtr("asst-1"), "second message"),
		treeTestAssistantMessage("asst-2", strPtr("user-2"), "response 2"),
		// Branch A (active)
		treeTestUserMessage("user-3a", strPtr("asst-2"), "branch A start"),
		treeTestAssistantMessage("asst-3a", strPtr("user-3a"), "branch A response"),
		treeTestUserMessage("user-4a", strPtr("asst-3a"), "branch A deep"),
		treeTestAssistantMessage("asst-4a", strPtr("user-4a"), "branch A leaf"),
		// Branch B
		treeTestUserMessage("user-3b", strPtr("asst-2"), "branch B start"),
		treeTestAssistantMessage("asst-3b", strPtr("user-3b"), "branch B response"),
		treeTestUserMessage("user-4b", strPtr("asst-3b"), "branch B deep"),
	}
	return treeTestBuildTree(entries)
}

func TestTreeSelectorBranchNavigationAndFolding(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)

	const (
		up        = "\x1b[A"
		down      = "\x1b[B"
		ctrlLeft  = "\x1b[1;5D"
		ctrlRight = "\x1b[1;5C"
		altLeft   = "\x1b[1;3D"
		altRight  = "\x1b[1;3C"
		ctrlRKey  = "\x12" // unused here; keeps close to pi naming block
		_         = ctrlRKey
	)

	t.Run("ctrl+right unfolds a folded node, then does segment jump when unfolded", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(ctrlLeft) // asst-4a → user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(down) // user-3a → user-3b (children hidden)
		if got := treeTestSelectedID(selector); got != "user-3b" {
			t.Fatalf("selected = %q, want user-3b", got)
		}

		selector.HandleInput(up) // user-3b → user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(ctrlRight) // unfold user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(down) // user-3a → asst-3a (children restored)
		if got := treeTestSelectedID(selector); got != "asst-3a" {
			t.Fatalf("selected = %q, want asst-3a", got)
		}

		selector.HandleInput(ctrlLeft) // asst-3a → user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(ctrlRight) // user-3a → asst-4a (segment jump to leaf)
		if got := treeTestSelectedID(selector); got != "asst-4a" {
			t.Fatalf("selected = %q, want asst-4a", got)
		}
	})

	t.Run("alt+left/right are aliases for fold and unfold navigation", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(altLeft) // asst-4a → user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(altLeft) // fold user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(altRight) // unfold user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(altRight) // user-3a → asst-4a
		if got := treeTestSelectedID(selector); got != "asst-4a" {
			t.Fatalf("selected = %q, want asst-4a", got)
		}
	})

	t.Run("folding root hides entire subtree, nested fold preserved on unfold", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(ctrlLeft) // asst-4a → user-3a
		selector.HandleInput(ctrlLeft) // fold user-3a
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(ctrlLeft) // user-3a (folded) → user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(down) // wrap (only visible node)
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(ctrlRight) // unfold user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(ctrlRight) // user-1 → user-3a (segment jump, user-3a still folded)
		if got := treeTestSelectedID(selector); got != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", got)
		}

		selector.HandleInput(down) // user-3a → user-3b (user-3a still folded)
		if got := treeTestSelectedID(selector); got != "user-3b" {
			t.Fatalf("selected = %q, want user-3b", got)
		}
	})

	t.Run("fold and navigate on non-active branch", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		// Navigate down to user-3b (branch B)
		found := false
		for i := 0; i < 20; i++ {
			selector.HandleInput(down)
			if treeTestSelectedID(selector) == "user-3b" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("did not navigate to user-3b")
		}

		selector.HandleInput(ctrlRight) // user-3b → user-4b (segment jump to leaf)
		if got := treeTestSelectedID(selector); got != "user-4b" {
			t.Fatalf("selected = %q, want user-4b", got)
		}

		selector.HandleInput(ctrlLeft) // user-4b → user-3b
		if got := treeTestSelectedID(selector); got != "user-3b" {
			t.Fatalf("selected = %q, want user-3b", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-3b
		if got := treeTestSelectedID(selector); got != "user-3b" {
			t.Fatalf("selected = %q, want user-3b", got)
		}

		selector.HandleInput(ctrlLeft) // user-3b (folded) → user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}
	})

	t.Run("fold and navigate with multiple roots", func(t *testing.T) {
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "first root"),
			treeTestAssistantMessage("asst-1", strPtr("user-1"), "response 1"),
			treeTestUserMessage("user-2", nil, "second root"),
			treeTestAssistantMessage("asst-2", strPtr("user-2"), "response 2"),
		}
		tree := treeTestBuildTree(entries)
		selector := NewTreeSelectorComponent(tree, "asst-1", 24, func(string) {}, func() {}, nil, "", "")

		if got := treeTestSelectedID(selector); got != "asst-1" {
			t.Fatalf("selected = %q, want asst-1", got)
		}

		selector.HandleInput(ctrlLeft) // asst-1 → user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(down) // user-1 → user-2 (children hidden)
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}

		selector.HandleInput(ctrlRight) // user-2 → asst-2 (segment jump to leaf)
		if got := treeTestSelectedID(selector); got != "asst-2" {
			t.Fatalf("selected = %q, want asst-2", got)
		}

		selector.HandleInput(ctrlLeft) // asst-2 → user-2
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-2
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}

		selector.HandleInput(ctrlLeft) // user-2 (folded, root) → stays on user-2
		if got := treeTestSelectedID(selector); got != "user-2" {
			t.Fatalf("selected = %q, want user-2", got)
		}
	})

	t.Run("folding root hides descendants even when intermediate nodes are filtered out", func(t *testing.T) {
		// user-1 → toolCallOnly-1 (filtered out) → user-2 → asst-2
		entries := []core.SessionEntry{
			treeTestUserMessage("user-1", nil, "hello"),
			treeTestToolCallOnlyAssistant("tool-asst-1", strPtr("user-1")),
			treeTestUserMessage("user-2", strPtr("tool-asst-1"), "follow up"),
			treeTestAssistantMessage("asst-2", strPtr("user-2"), "response"),
		}
		tree := treeTestBuildTree(entries)
		selector := NewTreeSelectorComponent(tree, "asst-2", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(ctrlLeft) // asst-2 → user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(ctrlLeft) // fold user-1
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}

		selector.HandleInput(down) // wrap (only visible node)
		if got := treeTestSelectedID(selector); got != "user-1" {
			t.Fatalf("selected = %q, want user-1", got)
		}
	})

	t.Run("search resets fold state", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(ctrlLeft) // asst-4a → user-3a
		selector.HandleInput(ctrlLeft) // fold user-3a

		selector.HandleInput(down) // user-3a → user-3b (children hidden)
		if got := treeTestSelectedID(selector); got != "user-3b" {
			t.Fatalf("selected = %q, want user-3b", got)
		}

		selector.HandleInput("b")    // search resets folds
		selector.HandleInput("\x1b") // clear search

		// Navigate to user-3a to verify fold was reset
		currentID := ""
		for i := 0; i < 20; i++ {
			selector.HandleInput(down)
			currentID = treeTestSelectedID(selector)
			if currentID == "user-3a" {
				break
			}
		}
		if currentID != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", currentID)
		}

		selector.HandleInput(down) // user-3a → asst-3a (not user-3b)
		if got := treeTestSelectedID(selector); got != "asst-3a" {
			t.Fatalf("selected = %q, want asst-3a", got)
		}
	})

	t.Run("filter mode change resets fold state", func(t *testing.T) {
		tree := treeTestBranchingTree()
		selector := NewTreeSelectorComponent(tree, "asst-4a", 24, func(string) {}, func() {}, nil, "", "")

		selector.HandleInput(ctrlLeft) // asst-4a → user-3a
		selector.HandleInput(ctrlLeft) // fold user-3a

		selector.HandleInput("\x15") // ctrl+u: user-only filter resets folds
		selector.HandleInput("\x04") // ctrl+d: back to default

		// Navigate to user-3a to verify fold was reset
		currentID := ""
		for i := 0; i < 20; i++ {
			selector.HandleInput(down)
			currentID = treeTestSelectedID(selector)
			if currentID == "user-3a" {
				break
			}
		}
		if currentID != "user-3a" {
			t.Fatalf("selected = %q, want user-3a", currentID)
		}

		selector.HandleInput(down) // user-3a → asst-3a (not user-3b)
		if got := treeTestSelectedID(selector); got != "asst-3a" {
			t.Fatalf("selected = %q, want asst-3a", got)
		}
	})
}

// keep json imported for future assertion helpers (render snapshotting).
var _ = json.Marshal
