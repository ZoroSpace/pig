// editor_stale_accept_test.go — pig-only hardening regression for stale
// autocomplete accepts (2026-09-18 smoke observation: rapid input + enter
// accepted a suggestion applied for an older text snapshot and submitted
// mangled text, '/hotkeys' → '/hhotkeys'; pi editor.ts carries the same
// latent race, pig hardens it — see
// docs/fix/2026-09-18-autocomplete-stale-accept-mangles-text.md).
//
// The race is injected deterministically: an apply whose Prefix no longer
// matches the text before the cursor (exactly the state a mid-burst apply
// leaves behind), then Enter/Tab. The guard must reject the accept and keep
// the user's text verbatim.
package tui

import (
	"context"
	"testing"
)

func newStaleAcceptTestEditor(t *testing.T) (*Editor, *string) {
	t.Helper()
	editor := newTestEditor(80, 24, nil)
	submitted := ""
	editor.OnSubmit = func(text string) { submitted = text }
	editor.SetAutocompleteProvider(&mockAutocompleteProvider{
		getSuggestions: func(ctx context.Context, lines []string, cursorLine, cursorCol int, options AutocompleteGetSuggestionsOptions) *AutocompleteSuggestions {
			return &AutocompleteSuggestions{
				Items:  []AutocompleteItem{{Value: "hotkeys", Label: "hotkeys"}},
				Prefix: "/hotkeys",
			}
		},
	})
	// Type '/hotkeys' one keystroke at a time and settle the final request.
	for _, key := range []string{"/", "h", "o", "t", "k", "e", "y", "s"} {
		editor.HandleInput(key)
	}
	flushAutocomplete(t, editor)
	return editor, &submitted
}

// injectStaleApply installs an applied suggestion whose Prefix was computed
// for '/hotke' while the line now holds '/hotkeys' (the post-race state).
func injectStaleApply(editor *Editor) {
	editor.mu.Lock()
	editor.applyAutocompleteSuggestions(&AutocompleteSuggestions{
		Items:  []AutocompleteItem{{Value: "hotkeys", Label: "hotkeys"}},
		Prefix: "/hotke",
	}, "regular")
	editor.mu.Unlock()
}

func TestEditorEnterWithStaleAutocompleteSubmitsRawText(t *testing.T) {
	editor, submitted := newStaleAcceptTestEditor(t)
	wantText(t, editor, "/hotkeys")
	injectStaleApply(editor)
	if !editor.IsShowingAutocomplete() {
		t.Fatal("expected injected stale autocomplete to be visible")
	}

	editor.HandleInput("\r")

	// The guard must reject the stale accept and submit the raw typed text.
	// Without it, ApplyCompletion mis-slices against the stale prefix and
	// submits '/hhotkeys'.
	if *submitted != "/hotkeys" {
		t.Fatalf("submitted %q, want the raw typed %q", *submitted, "/hotkeys")
	}
	wantText(t, editor, "")
}

func TestEditorTabWithStaleAutocompleteLeavesTextIntact(t *testing.T) {
	editor, _ := newStaleAcceptTestEditor(t)
	wantText(t, editor, "/hotkeys")
	injectStaleApply(editor)

	editor.HandleInput("\t")

	// Tab accepts the CURRENT suggestion when current; when stale it must be
	// a no-op on the text (cancel only).
	wantText(t, editor, "/hotkeys")
	if editor.IsShowingAutocomplete() {
		t.Error("expected stale autocomplete to be cancelled after tab")
	}
}
