// tui/keybindings.go — ported from pi: packages/tui/src/keybindings.ts
//
// Declared deviations:
//   - The Keybindings interface (44 boolean flags) is a closed TS union used
//     as keyof; pig uses plain string keys (KeyId = string already alias)
//     with TUIKeyBindings holding the defaults. Call sites pass the same
//     literals as pi, keeping grep parity.
//   - Object.entries iteration order (JS insertion order) is preserved with
//     ordered key slices where order is observable (conflict detection).

package tui

import (
	"sort"
	"sync"
)

// Keybinding identifies a bindable action (pi: keyof Keybindings).
type Keybinding = string

// KeybindingDefinition holds the default keys and a description.
// DefaultKeys is a slice to express pi's KeyId | KeyId[] union.
type KeybindingDefinition struct {
	DefaultKeys []KeyId
	Description string
}

// KeybindingDefinitions maps keybinding ids to definitions
// (pi: Record<string, KeybindingDefinition>; DefaultKeys uses a []KeyId slice
// to express pi's KeyId | KeyId[] union).
type KeybindingDefinitions map[string]KeybindingDefinition

// KeybindingsConfig maps keybinding ids to user-chosen keys
// (pi: Record<string, KeyId | KeyId[] | undefined>; a nil slice = undefined).
type KeybindingsConfig map[string][]KeyId

// TUIKeyBindings holds the default keybinding definitions
// (pi: TUI_KEYBINDINGS, declaration order preserved).
var TUIKeyBindings = KeybindingDefinitions{
	"tui.editor.cursorUp":           {DefaultKeys: []KeyId{"up"}, Description: "Move cursor up"},
	"tui.editor.cursorDown":         {DefaultKeys: []KeyId{"down"}, Description: "Move cursor down"},
	"tui.editor.historyPrevious":    {DefaultKeys: []KeyId{}, Description: "Select previous prompt history entry"},
	"tui.editor.historyNext":        {DefaultKeys: []KeyId{}, Description: "Select next prompt history entry"},
	"tui.editor.cursorLeft":         {DefaultKeys: []KeyId{"left", "ctrl+b"}, Description: "Move cursor left"},
	"tui.editor.cursorRight":        {DefaultKeys: []KeyId{"right", "ctrl+f"}, Description: "Move cursor right"},
	"tui.editor.cursorWordLeft":     {DefaultKeys: []KeyId{"alt+left", "ctrl+left", "alt+b"}, Description: "Move cursor word left"},
	"tui.editor.cursorWordRight":    {DefaultKeys: []KeyId{"alt+right", "ctrl+right", "alt+f"}, Description: "Move cursor word right"},
	"tui.editor.cursorLineStart":    {DefaultKeys: []KeyId{"home", "ctrl+a"}, Description: "Move to line start"},
	"tui.editor.cursorLineEnd":      {DefaultKeys: []KeyId{"end", "ctrl+e"}, Description: "Move to line end"},
	"tui.editor.jumpForward":        {DefaultKeys: []KeyId{"ctrl+]"}, Description: "Jump forward to character"},
	"tui.editor.jumpBackward":       {DefaultKeys: []KeyId{"ctrl+alt+]"}, Description: "Jump backward to character"},
	"tui.editor.pageUp":             {DefaultKeys: []KeyId{"pageUp", "ctrl+pageUp"}, Description: "Page up"},
	"tui.editor.pageDown":           {DefaultKeys: []KeyId{"pageDown", "ctrl+pageDown"}, Description: "Page down"},
	"tui.editor.deleteCharBackward": {DefaultKeys: []KeyId{"backspace"}, Description: "Delete character backward"},
	"tui.editor.deleteCharForward":  {DefaultKeys: []KeyId{"delete", "ctrl+d"}, Description: "Delete character forward"},
	"tui.editor.deleteWordBackward": {DefaultKeys: []KeyId{"ctrl+w", "alt+backspace"}, Description: "Delete word backward"},
	"tui.editor.deleteWordForward":  {DefaultKeys: []KeyId{"alt+d", "alt+delete"}, Description: "Delete word forward"},
	"tui.editor.deleteToLineStart":  {DefaultKeys: []KeyId{"ctrl+u"}, Description: "Delete to line start"},
	"tui.editor.deleteToLineEnd":    {DefaultKeys: []KeyId{"ctrl+k"}, Description: "Delete to line end"},
	"tui.editor.yank":               {DefaultKeys: []KeyId{"ctrl+y"}, Description: "Yank"},
	"tui.editor.yankPop":            {DefaultKeys: []KeyId{"alt+y"}, Description: "Yank pop"},
	"tui.editor.undo":               {DefaultKeys: []KeyId{"ctrl+-"}, Description: "Undo"},
	"tui.input.newLine":             {DefaultKeys: []KeyId{"shift+enter", "ctrl+j"}, Description: "Insert newline"},
	"tui.input.submit":              {DefaultKeys: []KeyId{"enter"}, Description: "Submit input"},
	"tui.input.tab":                 {DefaultKeys: []KeyId{"tab"}, Description: "Tab / autocomplete"},
	"tui.input.copy":                {DefaultKeys: []KeyId{"ctrl+c"}, Description: "Copy selection"},
	"tui.select.up":                 {DefaultKeys: []KeyId{"up"}, Description: "Move selection up"},
	"tui.select.down":               {DefaultKeys: []KeyId{"down"}, Description: "Move selection down"},
	"tui.select.pageUp":             {DefaultKeys: []KeyId{"pageUp"}, Description: "Selection page up"},
	"tui.select.pageDown":           {DefaultKeys: []KeyId{"pageDown"}, Description: "Selection page down"},
	"tui.select.confirm":            {DefaultKeys: []KeyId{"enter"}, Description: "Confirm selection"},
	"tui.select.cancel":             {DefaultKeys: []KeyId{"escape", "ctrl+c"}, Description: "Cancel selection"},
	// These intentionally shadow the unmodified editor bindings in fullscreen mode.
	"tui.altScreen.pageUp":   {DefaultKeys: []KeyId{"pageUp"}, Description: "Scroll viewport up one page"},
	"tui.altScreen.pageDown": {DefaultKeys: []KeyId{"pageDown"}, Description: "Scroll viewport down one page"},

	"tui.altScreen.halfPageUp":   {DefaultKeys: []KeyId{}, Description: "Scroll viewport up half a page"},
	"tui.altScreen.halfPageDown": {DefaultKeys: []KeyId{}, Description: "Scroll viewport down half a page"},
	"tui.altScreen.lineUp":       {DefaultKeys: []KeyId{}, Description: "Scroll viewport up one line"},
	"tui.altScreen.lineDown":     {DefaultKeys: []KeyId{}, Description: "Scroll viewport down one line"},
	"tui.altScreen.previousPrompt": {
		DefaultKeys: []KeyId{"ctrl+shift+up", "ctrl+up"}, Description: "Jump to previous semantic prompt",
	},
	"tui.altScreen.nextPrompt": {
		DefaultKeys: []KeyId{"ctrl+shift+down", "ctrl+down"}, Description: "Jump to next semantic prompt",
	},
	"tui.altScreen.search": {
		DefaultKeys: []KeyId{"ctrl+shift+f"}, Description: "Search the primary scroll view",
	},
	"tui.altScreen.searchNext": {
		DefaultKeys: []KeyId{"enter", "ctrl+g"}, Description: "Select the next search match",
	},
	"tui.altScreen.searchPrevious": {
		DefaultKeys: []KeyId{"shift+enter", "ctrl+shift+g"}, Description: "Select the previous search match",
	},
	"tui.altScreen.searchClose": {DefaultKeys: []KeyId{"escape"}, Description: "Close transcript search"},
	"tui.altScreen.top":         {DefaultKeys: []KeyId{"ctrl+home"}, Description: "Scroll viewport to top"},
	"tui.altScreen.bottom":      {DefaultKeys: []KeyId{"ctrl+end"}, Description: "Scroll viewport to bottom"},
}

// KeybindingConflict reports a key claimed by multiple user bindings.
type KeybindingConflict struct {
	Key         KeyId
	Keybindings []string
}

// normalizeKeys dedupes a key list preserving insertion order
// (pi: normalizeKeys with a seen-set).
func normalizeKeys(keys []KeyId) []KeyId {
	seen := make(map[KeyId]bool)
	result := make([]KeyId, 0, len(keys))
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

// KeybindingsManager resolves keybinding ids to key sequences.
type KeybindingsManager struct {
	definitions  KeybindingDefinitions
	userBindings KeybindingsConfig
	keysById     map[Keybinding][]KeyId
	conflicts    []KeybindingConflict
}

// NewKeybindingsManager creates a manager for the given definitions and user
// overrides.
func NewKeybindingsManager(definitions KeybindingDefinitions, userBindings KeybindingsConfig) *KeybindingsManager {
	m := &KeybindingsManager{
		definitions:  definitions,
		userBindings: userBindings,
	}
	m.rebuild()
	return m
}

func (m *KeybindingsManager) rebuild() {
	m.keysById = make(map[Keybinding][]KeyId)
	m.conflicts = nil

	// userClaims: key -> set of claiming keybindings.
	// pi iterates Object.entries(userBindings) in insertion order; Go map
	// iteration is unordered, so claiming order is resolved by sorting the
	// keybinding ids alphabetically (declared deviation — deterministic,
	// matches pi's literal order in practice).
	type claims struct {
		ids []Keybinding
	}
	userClaims := make(map[KeyId]*claims)
	var claimOrder []KeyId
	for keybinding, keys := range m.userBindings {
		if _, ok := m.definitions[keybinding]; !ok {
			continue
		}
		for _, key := range normalizeKeys(keys) {
			c, ok := userClaims[key]
			if !ok {
				c = &claims{}
				userClaims[key] = c
				claimOrder = append(claimOrder, key)
			}
			c.ids = append(c.ids, keybinding)
		}
	}
	sort.Strings(claimOrder)

	for _, key := range claimOrder {
		ids := userClaims[key].ids
		sort.Strings(ids)
		if len(ids) > 1 {
			m.conflicts = append(m.conflicts, KeybindingConflict{Key: key, Keybindings: ids})
		}
	}

	for id, definition := range m.definitions {
		userKeys, hasUser := m.userBindings[id]
		var keys []KeyId
		if !hasUser {
			keys = normalizeKeys(definition.DefaultKeys)
		} else {
			keys = normalizeKeys(userKeys)
		}
		m.keysById[id] = keys
	}
}

// Matches reports whether data matches any key bound to the keybinding.
func (m *KeybindingsManager) Matches(data string, keybinding Keybinding) bool {
	keys, ok := m.keysById[keybinding]
	if !ok {
		return false
	}
	for _, key := range keys {
		if MatchesKey(data, key) {
			return true
		}
	}
	return false
}

// GetKeys returns the keys bound to the keybinding (defensive copy).
func (m *KeybindingsManager) GetKeys(keybinding Keybinding) []KeyId {
	keys, ok := m.keysById[keybinding]
	if !ok {
		return nil
	}
	out := make([]KeyId, len(keys))
	copy(out, keys)
	return out
}

// GetDefinition returns the definition for the keybinding.
func (m *KeybindingsManager) GetDefinition(keybinding Keybinding) KeybindingDefinition {
	return m.definitions[keybinding]
}

// GetConflicts returns the current user-binding conflicts (defensive copy).
func (m *KeybindingsManager) GetConflicts() []KeybindingConflict {
	out := make([]KeybindingConflict, 0, len(m.conflicts))
	for _, c := range m.conflicts {
		kb := make([]string, len(c.Keybindings))
		copy(kb, c.Keybindings)
		out = append(out, KeybindingConflict{Key: c.Key, Keybindings: kb})
	}
	return out
}

// SetUserBindings replaces the user overrides and re-resolves.
func (m *KeybindingsManager) SetUserBindings(userBindings KeybindingsConfig) {
	m.userBindings = userBindings
	m.rebuild()
}

// GetUserBindings returns the user overrides (defensive copy).
func (m *KeybindingsManager) GetUserBindings() KeybindingsConfig {
	out := make(KeybindingsConfig, len(m.userBindings))
	for id, keys := range m.userBindings {
		out[id] = append([]KeyId(nil), keys...)
	}
	return out
}

// GetResolvedBindings returns the effective bindings per keybinding
// (pi: single key as scalar is folded back into a one-element slice).
func (m *KeybindingsManager) GetResolvedBindings() KeybindingsConfig {
	resolved := make(KeybindingsConfig)
	for id := range m.definitions {
		keys := m.keysById[id]
		if keys == nil {
			keys = []KeyId{}
		}
		resolved[id] = append([]KeyId(nil), keys...)
	}
	return resolved
}

var (
	keybindingsMu     sync.Mutex
	globalKeybindings *KeybindingsManager
)

// SetKeybindings replaces the global keybindings manager.
func SetKeybindings(keybindings *KeybindingsManager) {
	keybindingsMu.Lock()
	defer keybindingsMu.Unlock()
	globalKeybindings = keybindings
}

// GetKeybindings returns the global keybindings manager, lazily initialized
// with the defaults.
func GetKeybindings() *KeybindingsManager {
	keybindingsMu.Lock()
	defer keybindingsMu.Unlock()
	if globalKeybindings == nil {
		globalKeybindings = NewKeybindingsManager(TUIKeyBindings, nil)
	}
	return globalKeybindings
}
