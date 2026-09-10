// tui/keys_test.go — ported from pi: packages/tui/test/keys.test.ts
package tui

import (
	"testing"
)

func TestMatchesKeyKittyAlternateKeys(t *testing.T) {
	// Kitty protocol flag 4 (Report alternate keys) sends:
	// CSI codepoint:shifted:base ; modifier:event u
	// Where base is the key in standard PC-101 layout
	t.Run("matches Ctrl+c when pressing Ctrl+С (Cyrillic) with base layout key", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		// Cyrillic 'с' = codepoint 1089, Latin 'c' = codepoint 99
		cyrillicCtrlC := "\x1b[1089::99;5u"
		if !MatchesKey(cyrillicCtrlC, "ctrl+c") {
			t.Fatal("expected match")
		}
	})
	t.Run("matches Ctrl+d when pressing Ctrl+В (Cyrillic) with base layout key", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlD := "\x1b[1074::100;5u"
		if !MatchesKey(cyrillicCtrlD, "ctrl+d") {
			t.Fatal("expected match")
		}
	})
	t.Run("matches Ctrl+z when pressing Ctrl+Я (Cyrillic) with base layout key", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlZ := "\x1b[1103::122;5u"
		if !MatchesKey(cyrillicCtrlZ, "ctrl+z") {
			t.Fatal("expected match")
		}
	})
	t.Run("matches Ctrl+Shift+p with base layout key", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlShiftP := "\x1b[1079::112;6u"
		if !MatchesKey(cyrillicCtrlShiftP, "ctrl+shift+p") {
			t.Fatal("expected match")
		}
	})
	t.Run("still matches direct codepoint when no base layout key", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		latinCtrlC := "\x1b[99;5u"
		if !MatchesKey(latinCtrlC, "ctrl+c") {
			t.Fatal("expected match")
		}
	})
	t.Run("matches super-modified Kitty bindings, including combined modifiers", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if !MatchesKey("\x1b[107;9u", "super+k") {
			t.Fatal("super+k")
		}
		if !MatchesKey("\x1b[13;9u", "super+enter") {
			t.Fatal("super+enter")
		}
		if !MatchesKey("\x1b[107;13u", KeyCtrlSuper("k")) {
			t.Fatal("ctrl+super+k helper")
		}
		if !MatchesKey("\x1b[107;13u", "ctrl+super+k") {
			t.Fatal("ctrl+super+k")
		}
		if !MatchesKey("\x1b[107;14u", "ctrl+shift+super+k") {
			t.Fatal("ctrl+shift+super+k")
		}
		if MatchesKey("\x1b[107;13u", "super+k") {
			t.Fatal("super+k must not match ctrl+super")
		}
		if key, ok := ParseKey("\x1b[107;9u"); !ok || key != "super+k" {
			t.Fatalf("parseKey super+k = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[13;9u"); !ok || key != "super+enter" {
			t.Fatalf("parseKey super+enter = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[107;13u"); !ok || key != "ctrl+super+k" {
			t.Fatalf("parseKey ctrl+super+k = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[107;14u"); !ok || key != "shift+ctrl+super+k" {
			t.Fatalf("parseKey shift+ctrl+super+k = %q %v", key, ok)
		}
	})
	t.Run("matches digit bindings via Kitty CSI-u", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if !MatchesKey("\x1b[49u", "1") {
			t.Fatal("1")
		}
		if !MatchesKey("\x1b[49;5u", "ctrl+1") {
			t.Fatal("ctrl+1")
		}
		if MatchesKey("\x1b[49;5u", "ctrl+2") {
			t.Fatal("ctrl+2 must not match")
		}
		if key, ok := ParseKey("\x1b[49u"); !ok || key != "1" {
			t.Fatalf("parseKey = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[49;5u"); !ok || key != "ctrl+1" {
			t.Fatalf("parseKey = %q %v", key, ok)
		}
	})
	t.Run("normalizes Kitty keypad functional keys to logical digits, symbols, and navigation", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if !MatchesKey("\x1b[57400u", "1") {
			t.Fatal("57400 -> 1")
		}
		if !MatchesKey("\x1b[57410u", "/") {
			t.Fatal("57410 -> /")
		}
		if !MatchesKey("\x1b[57417u", "left") {
			t.Fatal("57417 -> left")
		}
		if !MatchesKey("\x1b[57426u", "delete") {
			t.Fatal("57426 -> delete")
		}
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1b[57399u", "0")
		expectParse("\x1b[57409u", ".")
		expectParse("\x1b[57413u", "+")
		expectParse("\x1b[57416u", ",")
		expectParse("\x1b[57417u", "left")
		expectParse("\x1b[57418u", "right")
		expectParse("\x1b[57419u", "up")
		expectParse("\x1b[57420u", "down")
		expectParse("\x1b[57421u", "pageUp")
		expectParse("\x1b[57422u", "pageDown")
		expectParse("\x1b[57423u", "home")
		expectParse("\x1b[57424u", "end")
		expectParse("\x1b[57425u", "insert")
		expectParse("\x1b[57426u", "delete")
	})
	t.Run("handles shifted key in format", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		// Latin 'c' with shifted 'C' (67) and base 'c' (99)
		shiftedKey := "\x1b[99:67:99;2u"
		if !MatchesKey(shiftedKey, "shift+c") {
			t.Fatal("expected match")
		}
	})
	t.Run("handles event type in format", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		releaseEvent := "\x1b[1089::99;5:3u"
		if !MatchesKey(releaseEvent, "ctrl+c") {
			t.Fatal("expected match")
		}
	})
	t.Run("handles full format with shifted key, base key, and event type", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		fullFormat := "\x1b[1089:1057:99;6:2u"
		if !MatchesKey(fullFormat, "ctrl+shift+c") {
			t.Fatal("expected match")
		}
	})
	t.Run("prefers codepoint for Latin letters even when base layout differs", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		dvorakCtrlK := "\x1b[107::118;5u"
		if !MatchesKey(dvorakCtrlK, "ctrl+k") {
			t.Fatal("ctrl+k")
		}
		if MatchesKey(dvorakCtrlK, "ctrl+v") {
			t.Fatal("ctrl+v must not match")
		}
	})
	t.Run("prefers codepoint for symbol keys even when base layout differs", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		dvorakCtrlSlash := "\x1b[47::91;5u"
		if !MatchesKey(dvorakCtrlSlash, "ctrl+/") {
			t.Fatal("ctrl+/")
		}
		if MatchesKey(dvorakCtrlSlash, "ctrl+[") {
			t.Fatal("ctrl+[ must not match")
		}
	})
	t.Run("does not match wrong key even with base layout", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlC := "\x1b[1089::99;5u"
		if MatchesKey(cyrillicCtrlC, "ctrl+d") {
			t.Fatal("must not match ctrl+d")
		}
	})
	t.Run("does not match wrong modifiers even with base layout", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlC := "\x1b[1089::99;5u"
		if MatchesKey(cyrillicCtrlC, "ctrl+shift+c") {
			t.Fatal("must not match ctrl+shift+c")
		}
	})
}

func TestMatchesKeyModifyOtherKeys(t *testing.T) {
	SetKittyProtocolActive(false)
	t.Run("matches xterm modifyOtherKeys Ctrl+c", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;99~", "ctrl+c") {
			t.Fatal("match")
		}
		if key, ok := ParseKey("\x1b[27;5;99~"); !ok || key != "ctrl+c" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Ctrl+d", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;100~", "ctrl+d") {
			t.Fatal("match")
		}
		if key, ok := ParseKey("\x1b[27;5;100~"); !ok || key != "ctrl+d" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Ctrl+z", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;122~", "ctrl+z") {
			t.Fatal("match")
		}
		if key, ok := ParseKey("\x1b[27;5;122~"); !ok || key != "ctrl+z" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Enter variants", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;13~", "ctrl+enter") {
			t.Fatal("ctrl+enter")
		}
		if !MatchesKey("\x1b[27;2;13~", "shift+enter") {
			t.Fatal("shift+enter")
		}
		if !MatchesKey("\x1b[27;3;13~", "alt+enter") {
			t.Fatal("alt+enter")
		}
		if key, ok := ParseKey("\x1b[27;5;13~"); !ok || key != "ctrl+enter" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;2;13~"); !ok || key != "shift+enter" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;3;13~"); !ok || key != "alt+enter" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Tab variants", func(t *testing.T) {
		if !MatchesKey("\x1b[27;2;9~", "shift+tab") {
			t.Fatal("shift+tab")
		}
		if !MatchesKey("\x1b[27;5;9~", "ctrl+tab") {
			t.Fatal("ctrl+tab")
		}
		if !MatchesKey("\x1b[27;3;9~", "alt+tab") {
			t.Fatal("alt+tab")
		}
		if key, ok := ParseKey("\x1b[27;2;9~"); !ok || key != "shift+tab" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;5;9~"); !ok || key != "ctrl+tab" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;3;9~"); !ok || key != "alt+tab" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Backspace variants", func(t *testing.T) {
		if !MatchesKey("\x1b[27;1;127~", "backspace") {
			t.Fatal("backspace")
		}
		if !MatchesKey("\x1b[27;5;127~", "ctrl+backspace") {
			t.Fatal("ctrl+backspace")
		}
		if !MatchesKey("\x1b[27;3;127~", "alt+backspace") {
			t.Fatal("alt+backspace")
		}
		if key, ok := ParseKey("\x1b[27;1;127~"); !ok || key != "backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;5;127~"); !ok || key != "ctrl+backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;3;127~"); !ok || key != "alt+backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Escape", func(t *testing.T) {
		if !MatchesKey("\x1b[27;1;27~", "escape") {
			t.Fatal("escape")
		}
		if key, ok := ParseKey("\x1b[27;1;27~"); !ok || key != "escape" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys Space variants", func(t *testing.T) {
		if !MatchesKey("\x1b[27;1;32~", "space") {
			t.Fatal("space")
		}
		if !MatchesKey("\x1b[27;5;32~", "ctrl+space") {
			t.Fatal("ctrl+space")
		}
		if key, ok := ParseKey("\x1b[27;1;32~"); !ok || key != "space" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;5;32~"); !ok || key != "ctrl+space" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys symbol combos", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;47~", "ctrl+/") {
			t.Fatal("ctrl+/")
		}
		if key, ok := ParseKey("\x1b[27;5;47~"); !ok || key != "ctrl+/" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys digit combos", func(t *testing.T) {
		if !MatchesKey("\x1b[27;5;49~", "ctrl+1") {
			t.Fatal("ctrl+1")
		}
		if !MatchesKey("\x1b[27;2;49~", "shift+1") {
			t.Fatal("shift+1")
		}
		if key, ok := ParseKey("\x1b[27;5;49~"); !ok || key != "ctrl+1" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;2;49~"); !ok || key != "shift+1" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches xterm modifyOtherKeys shifted uppercase letters", func(t *testing.T) {
		if !MatchesKey("\x1b[27;2;69~", "shift+e") {
			t.Fatal("shift+e")
		}
		if !MatchesKey("\x1b[27;6;69~", "ctrl+shift+e") {
			t.Fatal("ctrl+shift+e")
		}
		if key, ok := ParseKey("\x1b[27;2;69~"); !ok || key != "shift+e" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x1b[27;6;69~"); !ok || key != "shift+ctrl+e" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches Ctrl+Alt+letter via CSI-u when kitty inactive", func(t *testing.T) {
		if !MatchesKey("\x1b[104;7u", "ctrl+alt+h") {
			t.Fatal("ctrl+alt+h")
		}
		if key, ok := ParseKey("\x1b[104;7u"); !ok || key != "ctrl+alt+h" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches Ctrl+Alt+letter via xterm modifyOtherKeys", func(t *testing.T) {
		if !MatchesKey("\x1b[27;7;104~", "ctrl+alt+h") {
			t.Fatal("ctrl+alt+h")
		}
		if key, ok := ParseKey("\x1b[27;7;104~"); !ok || key != "ctrl+alt+h" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
}

func TestMatchesKeyLegacy(t *testing.T) {
	t.Run("matches legacy Ctrl+c", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\x03", "ctrl+c") {
			t.Fatal("ctrl+c")
		}
	})
	t.Run("matches legacy Ctrl+d", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\x04", "ctrl+d") {
			t.Fatal("ctrl+d")
		}
	})
	t.Run("matches escape key", func(t *testing.T) {
		if !MatchesKey("\x1b", "escape") {
			t.Fatal("escape")
		}
	})
	t.Run("matches legacy linefeed as enter", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\n", "enter") {
			t.Fatal("enter")
		}
		if key, ok := ParseKey("\n"); !ok || key != "enter" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("treats linefeed as shift+enter when kitty active", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if !MatchesKey("\n", "shift+enter") {
			t.Fatal("shift+enter")
		}
		if MatchesKey("\n", "enter") {
			t.Fatal("enter must not match")
		}
		if key, ok := ParseKey("\n"); !ok || key != "shift+enter" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("parses ctrl+space", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\x00", "ctrl+space") {
			t.Fatal("ctrl+space")
		}
		if key, ok := ParseKey("\x00"); !ok || key != "ctrl+space" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches legacy Ctrl+symbol", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\x1c", "ctrl+\\") {
			t.Fatal("ctrl+\\")
		}
		if key, ok := ParseKey("\x1c"); !ok || key != "ctrl+\\" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x1d", "ctrl+]") {
			t.Fatal("ctrl+]")
		}
		if key, ok := ParseKey("\x1d"); !ok || key != "ctrl+]" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x1f", "ctrl+_") {
			t.Fatal("ctrl+_")
		}
		if !MatchesKey("\x1f", "ctrl+-") {
			t.Fatal("ctrl+-")
		}
		if key, ok := ParseKey("\x1f"); !ok || key != "ctrl+-" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("matches legacy Ctrl+Alt+symbol", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if !MatchesKey("\x1b\x1b", "ctrl+alt+[") {
			t.Fatal("ctrl+alt+[")
		}
		if key, ok := ParseKey("\x1b\x1b"); !ok || key != "ctrl+alt+[" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x1b\x1c", "ctrl+alt+\\") {
			t.Fatal("ctrl+alt+\\")
		}
		if key, ok := ParseKey("\x1b\x1c"); !ok || key != "ctrl+alt+\\" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x1b\x1d", "ctrl+alt+]") {
			t.Fatal("ctrl+alt+]")
		}
		if key, ok := ParseKey("\x1b\x1d"); !ok || key != "ctrl+alt+]" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x1b\x1f", "ctrl+alt+_") {
			t.Fatal("ctrl+alt+_")
		}
		if !MatchesKey("\x1b\x1f", "ctrl+alt+-") {
			t.Fatal("ctrl+alt+-")
		}
		if key, ok := ParseKey("\x1b\x1f"); !ok || key != "ctrl+alt+-" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("treats raw 0x08 as plain backspace outside Windows Terminal", func(t *testing.T) {
		SetKittyProtocolActive(false)
		t.Setenv("WT_SESSION", "")
		if !MatchesKey("\x7f", "backspace") {
			t.Fatal("backspace")
		}
		if MatchesKey("\x7f", "ctrl+backspace") {
			t.Fatal("ctrl+backspace must not match")
		}
		if key, ok := ParseKey("\x7f"); !ok || key != "backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x08", "backspace") {
			t.Fatal("backspace from 0x08")
		}
		if MatchesKey("\x08", "ctrl+backspace") {
			t.Fatal("ctrl+backspace must not match")
		}
		if key, ok := ParseKey("\x08"); !ok || key != "backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x08", "ctrl+h") {
			t.Fatal("ctrl+h")
		}
	})
	t.Run("treats raw 0x08 as ctrl+backspace in local Windows Terminal", func(t *testing.T) {
		SetKittyProtocolActive(false)
		t.Setenv("WT_SESSION", "test-session")
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_CLIENT", "")
		t.Setenv("SSH_TTY", "")
		if !MatchesKey("\x08", "ctrl+backspace") {
			t.Fatal("ctrl+backspace")
		}
		if MatchesKey("\x08", "backspace") {
			t.Fatal("backspace must not match")
		}
		if key, ok := ParseKey("\x08"); !ok || key != "ctrl+backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x08", "ctrl+h") {
			t.Fatal("ctrl+h")
		}
	})
	t.Run("treats raw 0x08 as plain backspace in Windows Terminal over SSH", func(t *testing.T) {
		SetKittyProtocolActive(false)
		t.Setenv("WT_SESSION", "test-session")
		t.Setenv("SSH_CONNECTION", "1 2 3 4")
		t.Setenv("SSH_CLIENT", "1 2 3")
		t.Setenv("SSH_TTY", "/dev/pts/1")
		if MatchesKey("\x08", "ctrl+backspace") {
			t.Fatal("ctrl+backspace must not match")
		}
		if !MatchesKey("\x08", "backspace") {
			t.Fatal("backspace")
		}
		if key, ok := ParseKey("\x08"); !ok || key != "backspace" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if !MatchesKey("\x08", "ctrl+h") {
			t.Fatal("ctrl+h")
		}
	})
	t.Run("parses legacy alt-prefixed sequences when kitty inactive", func(t *testing.T) {
		SetKittyProtocolActive(false)
		expect := func(data, keyId string) {
			t.Helper()
			if !MatchesKey(data, keyId) {
				t.Fatalf("MatchesKey(%q, %q) = false", data, keyId)
			}
			key, ok := ParseKey(data)
			if !ok || key != keyId {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, keyId)
			}
		}
		expect("\x1b ", "alt+space")
		expect("\x1b\b", "alt+backspace")
		expect("\x1b\x03", "ctrl+alt+c")
		expect("\x1bB", "alt+left")
		expect("\x1bF", "alt+right")
		expect("\x1ba", "alt+a")
		expect("\x1b1", "alt+1")
		expect("\x1b,", "alt+,")
		expect("\x1b.", "alt+.")
		expect("\x1by", "alt+y")
		expect("\x1bz", "alt+z")

		SetKittyProtocolActive(true)
		expectNoMatch := func(data, keyId string) {
			t.Helper()
			if MatchesKey(data, keyId) {
				t.Fatalf("MatchesKey(%q, %q) = true with kitty active", data, keyId)
			}
			if key, ok := ParseKey(data); ok {
				t.Fatalf("ParseKey(%q) = %q with kitty active; want none", data, key)
			}
		}
		expectNoMatch("\x1b ", "alt+space")
		if key, ok := ParseKey("\x1b "); ok {
			t.Fatalf("ParseKey alt+space with kitty = %q", key)
		}
		expect("\x1b\b", "alt+backspace") // still matches with kitty active
		expectNoMatch("\x1b\x03", "ctrl+alt+c")
		expectNoMatch("\x1bB", "alt+left")
		expectNoMatch("\x1bF", "alt+right")
		expectNoMatch("\x1ba", "alt+a")
		expectNoMatch("\x1b1", "alt+1")
		expectNoMatch("\x1b,", "alt+,")
		expectNoMatch("\x1b.", "alt+.")
		expectNoMatch("\x1by", "alt+y")
		expectNoMatch("\x1bz", "alt+z")
		SetKittyProtocolActive(false)
	})
	t.Run("matches arrow keys", func(t *testing.T) {
		if !MatchesKey("\x1b[A", "up") {
			t.Fatal("up")
		}
		if !MatchesKey("\x1b[B", "down") {
			t.Fatal("down")
		}
		if !MatchesKey("\x1b[C", "right") {
			t.Fatal("right")
		}
		if !MatchesKey("\x1b[D", "left") {
			t.Fatal("left")
		}
	})
	t.Run("matches SS3 arrows and home/end", func(t *testing.T) {
		if !MatchesKey("\x1bOA", "up") {
			t.Fatal("up")
		}
		if !MatchesKey("\x1bOB", "down") {
			t.Fatal("down")
		}
		if !MatchesKey("\x1bOC", "right") {
			t.Fatal("right")
		}
		if !MatchesKey("\x1bOD", "left") {
			t.Fatal("left")
		}
		if !MatchesKey("\x1bOH", "home") {
			t.Fatal("home")
		}
		if !MatchesKey("\x1bOF", "end") {
			t.Fatal("end")
		}
	})
	t.Run("matches xterm Ctrl-modified viewport navigation", func(t *testing.T) {
		if !MatchesKey("\x1b[1;5H", "ctrl+home") {
			t.Fatal("ctrl+home")
		}
		if !MatchesKey("\x1b[1;5F", "ctrl+end") {
			t.Fatal("ctrl+end")
		}
		if !MatchesKey("\x1b[5;5~", "ctrl+pageUp") {
			t.Fatal("ctrl+pageUp")
		}
		if !MatchesKey("\x1b[6;5~", "ctrl+pageDown") {
			t.Fatal("ctrl+pageDown")
		}
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1b[1;5H", "ctrl+home")
		expectParse("\x1b[1;5F", "ctrl+end")
		expectParse("\x1b[5;5~", "ctrl+pageUp")
		expectParse("\x1b[6;5~", "ctrl+pageDown")
	})
	t.Run("matches legacy function keys and clear", func(t *testing.T) {
		if !MatchesKey("\x1bOP", "f1") {
			t.Fatal("f1")
		}
		if !MatchesKey("\x1b[24~", "f12") {
			t.Fatal("f12")
		}
		if !MatchesKey("\x1b[E", "clear") {
			t.Fatal("clear")
		}
	})
	t.Run("matches alt+arrows", func(t *testing.T) {
		if !MatchesKey("\x1bp", "alt+up") {
			t.Fatal("alt+up")
		}
		if MatchesKey("\x1bp", "up") {
			t.Fatal("up must not match")
		}
	})
	t.Run("matches rxvt modifier sequences", func(t *testing.T) {
		if !MatchesKey("\x1b[a", "shift+up") {
			t.Fatal("shift+up")
		}
		if !MatchesKey("\x1bOa", "ctrl+up") {
			t.Fatal("ctrl+up")
		}
		if !MatchesKey("\x1b[2$", "shift+insert") {
			t.Fatal("shift+insert")
		}
		if !MatchesKey("\x1b[2^", "ctrl+insert") {
			t.Fatal("ctrl+insert")
		}
		if !MatchesKey("\x1b[7$", "shift+home") {
			t.Fatal("shift+home")
		}
	})
}

func TestDecodeKittyPrintable(t *testing.T) {
	t.Run("decodes Kitty keypad functional keys to printable characters", func(t *testing.T) {
		expect := func(data, want string) {
			t.Helper()
			got, ok := DecodeKittyPrintable(data)
			if !ok || got != want {
				t.Fatalf("DecodeKittyPrintable(%q) = %q, %v; want %q", data, got, ok, want)
			}
		}
		expectNo := func(data string) {
			t.Helper()
			if got, ok := DecodeKittyPrintable(data); ok {
				t.Fatalf("DecodeKittyPrintable(%q) = %q; want none", data, got)
			}
		}
		expect("\x1b[57399u", "0")
		expect("\x1b[57400u", "1")
		expect("\x1b[57409u", ".")
		expect("\x1b[57410u", "/")
		expect("\x1b[57411u", "*")
		expect("\x1b[57412u", "-")
		expect("\x1b[57413u", "+")
		expect("\x1b[57415u", "=")
		expect("\x1b[57416u", ",")
		expectNo("\x1b[57417u")
	})
}

func TestDecodePrintableKey(t *testing.T) {
	t.Run("decodes printable xterm modifyOtherKeys sequences", func(t *testing.T) {
		expect := func(data, want string) {
			t.Helper()
			got, ok := DecodePrintableKey(data)
			if !ok || got != want {
				t.Fatalf("DecodePrintableKey(%q) = %q, %v; want %q", data, got, ok, want)
			}
		}
		expectNo := func(data string) {
			t.Helper()
			if got, ok := DecodePrintableKey(data); ok {
				t.Fatalf("DecodePrintableKey(%q) = %q; want none", data, got)
			}
		}
		expect("\x1b[27;2;69~", "E")
		expect("\x1b[27;2;196~", "Ä")
		expect("\x1b[27;2;32~", " ")
		expectNo("\x1b[27;2;13~")
		expectNo("\x1b[27;6;69~")
	})
}

func TestParseKey(t *testing.T) {
	t.Run("returns Latin key name when base layout key is present", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		cyrillicCtrlC := "\x1b[1089::99;5u"
		if key, ok := ParseKey(cyrillicCtrlC); !ok || key != "ctrl+c" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("prefers codepoint for Latin letters when base layout differs", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		dvorakCtrlK := "\x1b[107::118;5u"
		if key, ok := ParseKey(dvorakCtrlK); !ok || key != "ctrl+k" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("prefers codepoint for symbol keys when base layout differs", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		dvorakCtrlSlash := "\x1b[47::91;5u"
		if key, ok := ParseKey(dvorakCtrlSlash); !ok || key != "ctrl+/" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("returns key name from codepoint when no base layout", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		latinCtrlC := "\x1b[99;5u"
		if key, ok := ParseKey(latinCtrlC); !ok || key != "ctrl+c" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("parses shifted uppercase CSI-u letters as shift+letter", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if !MatchesKey("\x1b[69;2u", "shift+e") {
			t.Fatal("shift+e")
		}
		if key, ok := ParseKey("\x1b[69;2u"); !ok || key != "shift+e" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("ignores Kitty CSI-u with unsupported modifiers", func(t *testing.T) {
		SetKittyProtocolActive(true)
		defer SetKittyProtocolActive(false)
		if key, ok := ParseKey("\x1b[99;17u"); ok {
			t.Fatalf("parse = %q; want none", key)
		}
	})

	// Legacy key parsing
	t.Run("parses legacy Ctrl+letter", func(t *testing.T) {
		SetKittyProtocolActive(false)
		if key, ok := ParseKey("\x03"); !ok || key != "ctrl+c" {
			t.Fatalf("parse = %q %v", key, ok)
		}
		if key, ok := ParseKey("\x04"); !ok || key != "ctrl+d" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
	t.Run("parses special keys", func(t *testing.T) {
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1b", "escape")
		expectParse("\t", "tab")
		expectParse("\r", "enter")
		expectParse("\n", "enter")
		expectParse("\x00", "ctrl+space")
		expectParse(" ", "space")
		expectParse("1", "1")
		if !MatchesKey("1", "1") {
			t.Fatal("MatchesKey 1")
		}
	})
	t.Run("parses arrow keys", func(t *testing.T) {
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1b[A", "up")
		expectParse("\x1b[B", "down")
		expectParse("\x1b[C", "right")
		expectParse("\x1b[D", "left")
	})
	t.Run("parses SS3 arrows and home/end", func(t *testing.T) {
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1bOA", "up")
		expectParse("\x1bOB", "down")
		expectParse("\x1bOC", "right")
		expectParse("\x1bOD", "left")
		expectParse("\x1bOH", "home")
		expectParse("\x1bOF", "end")
	})
	t.Run("parses legacy function and modifier sequences", func(t *testing.T) {
		expectParse := func(data, want string) {
			t.Helper()
			key, ok := ParseKey(data)
			if !ok || key != want {
				t.Fatalf("ParseKey(%q) = %q, %v; want %q", data, key, ok, want)
			}
		}
		expectParse("\x1bOP", "f1")
		expectParse("\x1b[24~", "f12")
		expectParse("\x1b[E", "clear")
		expectParse("\x1b[2^", "ctrl+insert")
		expectParse("\x1bp", "alt+up")
	})
	t.Run("parses double bracket pageUp", func(t *testing.T) {
		if key, ok := ParseKey("\x1b[[5~"); !ok || key != "pageUp" {
			t.Fatalf("parse = %q %v", key, ok)
		}
	})
}
