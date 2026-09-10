// keybinding_hints.go (pi: components/keybinding-hints.ts) — utilities for
// formatting keybinding hints in the UI.
//
// Declared deviation: pi keeps this file in modes/interactive/components,
// but pig's core/tools renderers (write/read) need KeyHint/KeyText and
// core/tools cannot import components (components → core/tools for
// tool-execution's builtin-definition fallback would cycle). The components
// message components consume these helpers via the tools import.
package tools

import (
	"runtime"
	"strings"
	"unicode"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// KeyTextFormatOptions mirrors pi's KeyTextFormatOptions.
type KeyTextFormatOptions struct {
	Capitalize bool
}

func formatKeyPart(part string, options KeyTextFormatOptions) string {
	displayPart := part
	if runtime.GOOS == "darwin" && strings.ToLower(part) == "alt" {
		displayPart = "option"
	}
	if options.Capitalize {
		runes := []rune(displayPart)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			displayPart = string(runes)
		}
	}
	return displayPart
}

// FormatKeyText formats a slash-joined key list with plus-joined modifiers
// (pi: formatKeyText).
func FormatKeyText(key string, options KeyTextFormatOptions) string {
	parts := strings.Split(key, "/")
	for i, k := range parts {
		modifiers := strings.Split(k, "+")
		for j, part := range modifiers {
			modifiers[j] = formatKeyPart(part, options)
		}
		parts[i] = strings.Join(modifiers, "+")
	}
	return strings.Join(parts, "/")
}

func formatKeys(keys []tui.KeyId, options KeyTextFormatOptions) string {
	if len(keys) == 0 {
		return ""
	}
	return FormatKeyText(strings.Join(keys, "/"), options)
}

// KeyText renders the keys bound to a keybinding (pi: keyText).
func KeyText(keybinding tui.Keybinding) string {
	return formatKeys(tui.GetKeybindings().GetKeys(keybinding), KeyTextFormatOptions{})
}

// KeyDisplayText renders capitalized key text (pi: keyDisplayText).
func KeyDisplayText(keybinding tui.Keybinding) string {
	return formatKeys(tui.GetKeybindings().GetKeys(keybinding), KeyTextFormatOptions{Capitalize: true})
}

// KeyHint renders "keys description" with dim keys and muted description
// (pi: keyHint).
func KeyHint(keybinding tui.Keybinding, description string) string {
	return theme.Fg("dim", KeyText(keybinding)) + theme.Fg("muted", " "+description)
}

// RawKeyHint renders a hint for a raw key string (pi: rawKeyHint).
func RawKeyHint(key string, description string) string {
	return theme.Fg("dim", FormatKeyText(key, KeyTextFormatOptions{})) + theme.Fg("muted", " "+description)
}
