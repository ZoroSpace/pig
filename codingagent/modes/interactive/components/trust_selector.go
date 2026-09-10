// trust_selector.go (pi: components/trust-selector.ts) — the /trust project
// trust prompt: a navigable list of trust options with the saved decision
// checkmark.
package components

import (
	core "github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// TrustSelection is the user's trust decision (pi: TrustSelection =
// Pick<ProjectTrustOption, "trusted" | "updates">).
type TrustSelection struct {
	Trusted bool
	Updates []core.ProjectTrustUpdate
}

// TrustSelectorOptions configure the trust selector (pi:
// TrustSelectorOptions).
type TrustSelectorOptions struct {
	Cwd            string
	SavedDecision  *core.ProjectTrustStoreEntry
	ProjectTrusted bool
	OnSelect       func(selection TrustSelection)
	OnCancel       func()
}

// formatTrustDecision renders the saved decision line (pi: formatDecision).
// hasTrustPath is false when there are no options (pi: undefined savedPath).
func formatTrustDecision(trustPath string, hasTrustPath bool, decision *core.ProjectTrustStoreEntry) string {
	if decision == nil {
		return "none"
	}
	label := "untrusted"
	if decision.Decision {
		label = "trusted"
	}
	if hasTrustPath && decision.Path != trustPath {
		return label + " (inherited from " + decision.Path + ")"
	}
	return label + " (" + decision.Path + ")"
}

// TrustSelectorComponent renders the project trust selector (pi:
// TrustSelectorComponent extends Container).
type TrustSelectorComponent struct {
	tui.Container

	selectedIndex    int
	listContainer    *tui.Container
	trustOptions     []core.ProjectTrustOption
	savedDecision    *core.ProjectTrustStoreEntry
	onSelectCallback func(selection TrustSelection)
	onCancelCallback func()
}

// NewTrustSelectorComponent builds the selector (pi: constructor).
func NewTrustSelectorComponent(options TrustSelectorOptions) *TrustSelectorComponent {
	t := &TrustSelectorComponent{
		savedDecision:    options.SavedDecision,
		trustOptions:     core.GetProjectTrustOptions(options.Cwd, nil),
		onSelectCallback: options.OnSelect,
		onCancelCallback: options.OnCancel,
	}

	t.selectedIndex = 0
	for i, option := range t.trustOptions {
		if t.isSavedOption(option) {
			t.selectedIndex = i
			break
		}
	}

	currentTheme := theme.Current()
	t.AddChild(NewDynamicBorder(nil))
	t.AddChild(tui.NewSpacer(1))
	t.AddChild(tui.NewText(currentTheme.Fg("accent", currentTheme.Bold("Project trust")), 1, 0))
	t.AddChild(tui.NewText(currentTheme.Fg("muted", options.Cwd), 1, 0))
	t.AddChild(tui.NewSpacer(1))
	trustPath := ""
	hasTrustPath := false
	if len(t.trustOptions) > 0 {
		trustPath = t.trustOptions[0].SavedPath
		hasTrustPath = trustPath != ""
	}
	t.AddChild(tui.NewText(currentTheme.Fg("muted", "Saved decision: "+formatTrustDecision(trustPath, hasTrustPath, options.SavedDecision)), 1, 0))
	currentSession := "untrusted"
	if options.ProjectTrusted {
		currentSession = "trusted"
	}
	t.AddChild(tui.NewText(currentTheme.Fg("muted", "Current session: "+currentSession), 1, 0))
	t.AddChild(tui.NewSpacer(1))

	t.listContainer = &tui.Container{}
	t.AddChild(t.listContainer)
	t.AddChild(tui.NewSpacer(1))
	t.AddChild(tui.NewText(
		coretools.RawKeyHint("↑↓", "navigate")+
			"  "+
			coretools.KeyHint("tui.select.confirm", "save")+
			"  "+
			coretools.KeyHint("tui.select.cancel", "cancel"),
		1, 0,
	))
	t.AddChild(tui.NewSpacer(1))
	t.AddChild(NewDynamicBorder(nil))

	t.updateList()
	return t
}

func (t *TrustSelectorComponent) isSavedOption(option core.ProjectTrustOption) bool {
	return option.SavedPath != "" &&
		t.savedDecision != nil &&
		t.savedDecision.Decision == option.Trusted &&
		t.savedDecision.Path == option.SavedPath
}

func (t *TrustSelectorComponent) updateList() {
	t.listContainer.Clear()
	currentTheme := theme.Current()
	for i := range t.trustOptions {
		option := t.trustOptions[i]

		isSelected := i == t.selectedIndex
		isCurrent := t.isSavedOption(option)
		// v0.85.1: the current-decision marker moves before the label and is
		// accent-colored, so it stays visible while browsing.
		currentMarker := "  "
		if isCurrent {
			currentMarker = currentTheme.Fg("accent", "✓ ")
		}
		prefix := "  "
		if isSelected {
			prefix = currentTheme.Fg("accent", "→ ")
		}
		label := currentTheme.Fg("text", option.Label)
		if isSelected {
			label = currentTheme.Fg("accent", option.Label)
		}
		t.listContainer.AddChild(tui.NewText(prefix+currentMarker+label, 1, 0))
	}
}

// HandleInput implements InputHandler (pi: handleInput).
func (t *TrustSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, "tui.select.up") || keyData == "k":
		t.selectedIndex = maxInt(0, t.selectedIndex-1)
		t.updateList()
	case kb.Matches(keyData, "tui.select.down") || keyData == "j":
		t.selectedIndex = minInt(t.selectedIndex+1, len(t.trustOptions)-1)
		t.updateList()
	case kb.Matches(keyData, "tui.select.confirm") || keyData == "\n":
		if t.selectedIndex < len(t.trustOptions) {
			selected := t.trustOptions[t.selectedIndex]
			if t.onSelectCallback != nil {
				t.onSelectCallback(TrustSelection{Trusted: selected.Trusted, Updates: selected.Updates})
			}
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		if t.onCancelCallback != nil {
			t.onCancelCallback()
		}
	}
}
