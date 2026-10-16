// tui/keys.go (pi: packages/tui/src/keys.ts) — keyboard input handling for
// terminal applications: legacy sequences + Kitty keyboard protocol + xterm
// modifyOtherKeys.
//
// Porting notes:
//   - TS `KeyId` is a template-literal union for autocomplete; Go uses a plain
//     string alias (open union, README §3.4). The `Key` helper object becomes
//     Key* constants + KeyCtrl/KeyCtrlShift/... helper functions.
//   - `_kittyProtocolActive` global → atomic.Bool (pi relies on the single
//     event loop; tests are sequential, atomics keep -race clean).
//   - `_lastEventType` in pi is written by parseKittySequence but never read
//     (isKeyRelease/isKeyRepeat inspect the raw data string instead) — not
//     ported.
package tui

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// =============================================================================
// Global Kitty Protocol State
// =============================================================================

var kittyProtocolActive atomic.Bool

// SetKittyProtocolActive sets the global Kitty keyboard protocol state.
// Called by ProcessTerminal after detecting protocol support.
func SetKittyProtocolActive(active bool) { kittyProtocolActive.Store(active) }

// IsKittyProtocolActive queries whether Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool { return kittyProtocolActive.Load() }

// =============================================================================
// Type-Safe Key Identifiers
// =============================================================================

// KeyId is a key identifier string such as "escape", "ctrl+c", "shift+enter".
// (pi: union type KeyId — open string alias in Go.)
type KeyId = string

// Special key constants (pi: Key object).
const (
	KeyEscape    KeyId = "escape"
	KeyEsc       KeyId = "esc"
	KeyEnter     KeyId = "enter"
	KeyReturn    KeyId = "return"
	KeyTab       KeyId = "tab"
	KeySpace     KeyId = "space"
	KeyBackspace KeyId = "backspace"
	KeyDelete    KeyId = "delete"
	KeyInsert    KeyId = "insert"
	KeyClear     KeyId = "clear"
	KeyHome      KeyId = "home"
	KeyEnd       KeyId = "end"
	KeyPageUp    KeyId = "pageUp"
	KeyPageDown  KeyId = "pageDown"
	KeyUp        KeyId = "up"
	KeyDown      KeyId = "down"
	KeyLeft      KeyId = "left"
	KeyRight     KeyId = "right"
	KeyF1        KeyId = "f1"
	KeyF2        KeyId = "f2"
	KeyF3        KeyId = "f3"
	KeyF4        KeyId = "f4"
	KeyF5        KeyId = "f5"
	KeyF6        KeyId = "f6"
	KeyF7        KeyId = "f7"
	KeyF8        KeyId = "f8"
	KeyF9        KeyId = "f9"
	KeyF10       KeyId = "f10"
	KeyF11       KeyId = "f11"
	KeyF12       KeyId = "f12"
)

// Symbol key constants (pi: Key object symbol members).
const (
	KeyBacktick     KeyId = "`"
	KeyHyphen       KeyId = "-"
	KeyEquals       KeyId = "="
	KeyLeftBracket  KeyId = "["
	KeyRightBracket KeyId = "]"
	KeyBackslash    KeyId = "\\"
	KeySemicolon    KeyId = ";"
	KeyQuote        KeyId = "'"
	KeyComma        KeyId = ","
	KeyPeriod       KeyId = "."
	KeySlash        KeyId = "/"
	KeyExclamation  KeyId = "!"
	KeyAt           KeyId = "@"
	KeyHash         KeyId = "#"
	KeyDollar       KeyId = "$"
	KeyPercent      KeyId = "%"
	KeyCaret        KeyId = "^"
	KeyAmpersand    KeyId = "&"
	KeyAsterisk     KeyId = "*"
	KeyLeftParen    KeyId = "("
	KeyRightParen   KeyId = ")"
	KeyUnderscore   KeyId = "_"
	KeyPlus         KeyId = "+"
	KeyPipe         KeyId = "|"
	KeyTilde        KeyId = "~"
	KeyLeftBrace    KeyId = "{"
	KeyRightBrace   KeyId = "}"
	KeyColon        KeyId = ":"
	KeyLessThan     KeyId = "<"
	KeyGreaterThan  KeyId = ">"
	KeyQuestion     KeyId = "?"
)

// Single-modifier helper functions (pi: Key.ctrl("c") etc.).
func KeyCtrl(key string) string           { return "ctrl+" + key }
func KeyShift(key string) string          { return "shift+" + key }
func KeyAlt(key string) string            { return "alt+" + key }
func KeySuper(key string) string          { return "super+" + key }
func KeyCtrlShift(key string) string      { return "ctrl+shift+" + key }
func KeyShiftCtrl(key string) string      { return "shift+ctrl+" + key }
func KeyCtrlAlt(key string) string        { return "ctrl+alt+" + key }
func KeyAltCtrl(key string) string        { return "alt+ctrl+" + key }
func KeyShiftAlt(key string) string       { return "shift+alt+" + key }
func KeyAltShift(key string) string       { return "alt+shift+" + key }
func KeyCtrlSuper(key string) string      { return "ctrl+super+" + key }
func KeySuperCtrl(key string) string      { return "super+ctrl+" + key }
func KeyShiftSuper(key string) string     { return "shift+super+" + key }
func KeySuperShift(key string) string     { return "super+shift+" + key }
func KeyAltSuper(key string) string       { return "alt+super+" + key }
func KeySuperAlt(key string) string       { return "super+alt+" + key }
func KeyCtrlShiftAlt(key string) string   { return "ctrl+shift+alt+" + key }
func KeyCtrlShiftSuper(key string) string { return "ctrl+shift+super+" + key }

// =============================================================================
// Constants
// =============================================================================

var symbolKeys = map[string]bool{
	"`": true, "-": true, "=": true, "[": true, "]": true, "\\": true,
	";": true, "'": true, ",": true, ".": true, "/": true,
	"!": true, "@": true, "#": true, "$": true, "%": true,
	"^": true, "&": true, "*": true, "(": true, ")": true,
	"_": true, "+": true, "|": true, "~": true, "{": true, "}": true,
	":": true, "<": true, ">": true, "?": true,
}

// shiftedSymbolKeys is the set of symbols Kitty/modifyOtherKeys report as
// Shift plus the physical key (pi: SHIFTED_SYMBOL_KEYS).
var shiftedSymbolKeys = map[string]bool{
	"~": true, "!": true, "@": true, "#": true, "$": true, "%": true,
	"^": true, "&": true, "*": true, "(": true, ")": true,
	"_": true, "+": true, "|": true, "{": true, "}": true,
	":": true, "<": true, ">": true, "?": true,
}

// modifierNames is the modifier prefix order parseKeyId strips (pi:
// MODIFIER_NAMES).
var modifierNames = []string{"shift", "alt", "ctrl", "super"}

const (
	modifierShift = 1
	modifierAlt   = 2
	modifierCtrl  = 4
	modifierSuper = 8
)

const lockMask = 64 + 128 // Caps Lock + Num Lock

const (
	codepointEscape    = 27
	codepointTab       = 9
	codepointEnter     = 13
	codepointSpace     = 32
	codepointBackspace = 127
	codepointKpEnter   = 57414 // Numpad Enter (Kitty protocol)
)

// Arrow codepoints are negative sentinels (not real codepoints).
const (
	arrowUp    = -1
	arrowDown  = -2
	arrowRight = -3
	arrowLeft  = -4
)

const (
	funcDelete = -10
	funcInsert = -11
	funcPageUp = -12
	funcPageDn = -13
	funcHome   = -14
	funcEnd    = -15
)

var kittyFunctionalKeyEquivalents = map[int]int{
	57399: 48, // KP_0 -> 0
	57400: 49, // KP_1 -> 1
	57401: 50, // KP_2 -> 2
	57402: 51, // KP_3 -> 3
	57403: 52, // KP_4 -> 4
	57404: 53, // KP_5 -> 5
	57405: 54, // KP_6 -> 6
	57406: 55, // KP_7 -> 7
	57407: 56, // KP_8 -> 8
	57408: 57, // KP_9 -> 9
	57409: 46, // KP_DECIMAL -> .
	57410: 47, // KP_DIVIDE -> /
	57411: 42, // KP_MULTIPLY -> *
	57412: 45, // KP_SUBTRACT -> -
	57413: 43, // KP_ADD -> +
	57415: 61, // KP_EQUAL -> =
	57416: 44, // KP_SEPARATOR -> ,
	57417: arrowLeft,
	57418: arrowRight,
	57419: arrowUp,
	57420: arrowDown,
	57421: funcPageUp,
	57422: funcPageDn,
	57423: funcHome,
	57424: funcEnd,
	57425: funcInsert,
	57426: funcDelete,
}

func normalizeKittyFunctionalCodepoint(codepoint int) int {
	if eq, ok := kittyFunctionalKeyEquivalents[codepoint]; ok {
		return eq
	}
	return codepoint
}

func normalizeShiftedLetterIdentityCodepoint(codepoint int, modifier int) int {
	effectiveModifier := modifier &^ lockMask
	if effectiveModifier&modifierShift != 0 && codepoint >= 65 && codepoint <= 90 {
		return codepoint + 32
	}
	return codepoint
}

// getLogicalShiftedSymbolCandidate resolves a shifted symbol reported by
// Kitty/modifyOtherKeys (e.g. "+" as Shift plus the physical "=" key) into the
// codepoint the binding names, consuming the implicit Shift while preserving
// Ctrl/Alt/Super. Returns ok=false when the codepoint is absent or not a
// recognized shifted symbol. (pi: getLogicalShiftedSymbolCandidate)
func getLogicalShiftedSymbolCandidate(codepoint int, modifier int, allowedSymbols map[string]bool) (int, int, bool) {
	if codepoint < 0 || modifier&modifierShift == 0 {
		return 0, 0, false
	}
	normalizedCodepoint := normalizeKittyFunctionalCodepoint(codepoint)
	if !allowedSymbols[string(rune(normalizedCodepoint))] {
		return 0, 0, false
	}
	return normalizedCodepoint, modifier &^ modifierShift, true
}

var legacyKeySequences = map[string][]string{
	"up":       {"\x1b[A", "\x1bOA"},
	"down":     {"\x1b[B", "\x1bOB"},
	"right":    {"\x1b[C", "\x1bOC"},
	"left":     {"\x1b[D", "\x1bOD"},
	"home":     {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
	"end":      {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
	"insert":   {"\x1b[2~"},
	"delete":   {"\x1b[3~"},
	"pageUp":   {"\x1b[5~", "\x1b[[5~"},
	"pageDown": {"\x1b[6~", "\x1b[[6~"},
	"clear":    {"\x1b[E", "\x1bOE"},
	"f1":       {"\x1bOP", "\x1b[11~", "\x1b[[A"},
	"f2":       {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
	"f3":       {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4":       {"\x1bOS", "\x1b[14~", "\x1b[[D"},
	"f5":       {"\x1b[15~", "\x1b[[E"},
	"f6":       {"\x1b[17~"},
	"f7":       {"\x1b[18~"},
	"f8":       {"\x1b[19~"},
	"f9":       {"\x1b[20~"},
	"f10":      {"\x1b[21~"},
	"f11":      {"\x1b[23~"},
	"f12":      {"\x1b[24~"},
}

var legacyShiftSequences = map[string][]string{
	"up":       {"\x1b[a"},
	"down":     {"\x1b[b"},
	"right":    {"\x1b[c"},
	"left":     {"\x1b[d"},
	"clear":    {"\x1b[e"},
	"insert":   {"\x1b[2$"},
	"delete":   {"\x1b[3$"},
	"pageUp":   {"\x1b[5$"},
	"pageDown": {"\x1b[6$"},
	"home":     {"\x1b[7$"},
	"end":      {"\x1b[8$"},
}

var legacyCtrlSequences = map[string][]string{
	"up":       {"\x1bOa"},
	"down":     {"\x1bOb"},
	"right":    {"\x1bOc"},
	"left":     {"\x1bOd"},
	"clear":    {"\x1bOe"},
	"insert":   {"\x1b[2^"},
	"delete":   {"\x1b[3^"},
	"pageUp":   {"\x1b[5^"},
	"pageDown": {"\x1b[6^"},
	"home":     {"\x1b[7^"},
	"end":      {"\x1b[8^"},
}

var legacySequenceKeyIds = map[string]string{
	"\x1bOA":   "up",
	"\x1bOB":   "down",
	"\x1bOC":   "right",
	"\x1bOD":   "left",
	"\x1bOH":   "home",
	"\x1bOF":   "end",
	"\x1b[E":   "clear",
	"\x1bOE":   "clear",
	"\x1bOe":   "ctrl+clear",
	"\x1b[e":   "shift+clear",
	"\x1b[2~":  "insert",
	"\x1b[2$":  "shift+insert",
	"\x1b[2^":  "ctrl+insert",
	"\x1b[3$":  "shift+delete",
	"\x1b[3^":  "ctrl+delete",
	"\x1b[[5~": "pageUp",
	"\x1b[[6~": "pageDown",
	"\x1b[a":   "shift+up",
	"\x1b[b":   "shift+down",
	"\x1b[c":   "shift+right",
	"\x1b[d":   "shift+left",
	"\x1bOa":   "ctrl+up",
	"\x1bOb":   "ctrl+down",
	"\x1bOc":   "ctrl+right",
	"\x1bOd":   "ctrl+left",
	"\x1b[5$":  "shift+pageUp",
	"\x1b[6$":  "shift+pageDown",
	"\x1b[7$":  "shift+home",
	"\x1b[8$":  "shift+end",
	"\x1b[5^":  "ctrl+pageUp",
	"\x1b[6^":  "ctrl+pageDown",
	"\x1b[7^":  "ctrl+home",
	"\x1b[8^":  "ctrl+end",
	"\x1bOP":   "f1",
	"\x1bOQ":   "f2",
	"\x1bOR":   "f3",
	"\x1bOS":   "f4",
	"\x1b[11~": "f1",
	"\x1b[12~": "f2",
	"\x1b[13~": "f3",
	"\x1b[14~": "f4",
	"\x1b[[A":  "f1",
	"\x1b[[B":  "f2",
	"\x1b[[C":  "f3",
	"\x1b[[D":  "f4",
	"\x1b[[E":  "f5",
	"\x1b[15~": "f5",
	"\x1b[17~": "f6",
	"\x1b[18~": "f7",
	"\x1b[19~": "f8",
	"\x1b[20~": "f9",
	"\x1b[21~": "f10",
	"\x1b[23~": "f11",
	"\x1b[24~": "f12",
	"\x1bb":    "alt+left",
	"\x1bf":    "alt+right",
	"\x1bp":    "alt+up",
	"\x1bn":    "alt+down",
}

func matchesLegacySequence(data string, sequences []string) bool {
	for _, seq := range sequences {
		if data == seq {
			return true
		}
	}
	return false
}

func matchesLegacyModifierSequence(data string, key string, modifier int) bool {
	if modifier == modifierShift {
		return matchesLegacySequence(data, legacyShiftSequences[key])
	}
	if modifier == modifierCtrl {
		return matchesLegacySequence(data, legacyCtrlSequences[key])
	}
	return false
}

// =============================================================================
// Kitty Protocol Parsing
// =============================================================================

// KeyEventType are event types from Kitty keyboard protocol (flag 2):
// 1 = key press, 2 = key repeat, 3 = key release.
type KeyEventType = string

const (
	KeyEventPress   KeyEventType = "press"
	KeyEventRepeat  KeyEventType = "repeat"
	KeyEventRelease KeyEventType = "release"
)

type parsedKittySequence struct {
	codepoint     int
	shiftedKey    int // Shifted version of the key; -1 when absent
	baseLayoutKey int // Key in standard PC-101 layout; -1 when absent
	modifier      int
	eventType     KeyEventType
}

type parsedModifyOtherKeysSequence struct {
	codepoint int
	modifier  int
}

// IsKeyRelease reports whether the data looks like a Kitty key release event.
// Only meaningful when Kitty keyboard protocol with flag 2 is active.
func IsKeyRelease(data string) bool {
	// Don't treat bracketed paste content as key release, even if it contains
	// patterns like ":3F" (e.g., bluetooth MAC addresses like "90:62:3F:A5").
	// Terminal.ts re-wraps paste content with bracketed paste markers before
	// passing to TUI, so pasted data will always contain \x1b[200~.
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, marker := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, marker) {
			return true
		}
	}
	return false
}

// IsKeyRepeat reports whether the data looks like a Kitty key repeat event.
// Only meaningful when Kitty keyboard protocol with flag 2 is active.
func IsKeyRepeat(data string) bool {
	// Don't treat bracketed paste content as key repeat, even if it contains
	// patterns like ":2F". See IsKeyRelease for details.
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, marker := range []string{":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"} {
		if strings.Contains(data, marker) {
			return true
		}
	}
	return false
}

func parseEventType(eventTypeStr string, present bool) KeyEventType {
	if !present {
		return KeyEventPress
	}
	eventType, err := strconv.Atoi(eventTypeStr)
	if err != nil {
		return KeyEventPress
	}
	if eventType == 2 {
		return KeyEventRepeat
	}
	if eventType == 3 {
		return KeyEventRelease
	}
	return KeyEventPress
}

var kittyCsiURegex = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
var kittyArrowRegex = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
var kittyFuncRegex = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$`)
var kittyHomeEndRegex = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([HF])$`)

func parseKittySequence(data string) *parsedKittySequence {
	// CSI u format with alternate keys (flag 4):
	// \x1b[<codepoint>u
	// \x1b[<codepoint>;<mod>u
	// \x1b[<codepoint>;<mod>:<event>u
	// \x1b[<codepoint>:<shifted>;<mod>u
	// \x1b[<codepoint>:<shifted>:<base>;<mod>u
	// \x1b[<codepoint>::<base>;<mod>u (no shifted key, only base)
	//
	// With flag 2, event type is appended after modifier colon: 1=press, 2=repeat, 3=release
	// With flag 4, alternate keys are appended after codepoint with colons
	if m := kittyCsiURegex.FindStringSubmatch(data); m != nil {
		codepoint, _ := strconv.Atoi(m[1])
		shiftedKey := -1
		if m[2] != "" {
			shiftedKey, _ = strconv.Atoi(m[2])
		}
		baseLayoutKey := -1
		if m[3] != "" {
			baseLayoutKey, _ = strconv.Atoi(m[3])
		}
		modValue := 1
		if m[4] != "" {
			modValue, _ = strconv.Atoi(m[4])
		}
		eventType := parseEventType(m[5], m[5] != "")
		return &parsedKittySequence{codepoint: codepoint, shiftedKey: shiftedKey, baseLayoutKey: baseLayoutKey, modifier: modValue - 1, eventType: eventType}
	}

	// Arrow keys with modifier: \x1b[1;<mod>A/B/C/D or \x1b[1;<mod>:<event>A/B/C/D
	if m := kittyArrowRegex.FindStringSubmatch(data); m != nil {
		modValue, _ := strconv.Atoi(m[1])
		eventType := parseEventType(m[2], m[2] != "")
		arrowCodes := map[string]int{"A": arrowUp, "B": arrowDown, "C": arrowRight, "D": arrowLeft}
		return &parsedKittySequence{codepoint: arrowCodes[m[3]], baseLayoutKey: -1, modifier: modValue - 1, eventType: eventType}
	}

	// Functional keys: \x1b[<num>~ or \x1b[<num>;<mod>~ or \x1b[<num>;<mod>:<event>~
	if m := kittyFuncRegex.FindStringSubmatch(data); m != nil {
		keyNum, _ := strconv.Atoi(m[1])
		modValue := 1
		if m[2] != "" {
			modValue, _ = strconv.Atoi(m[2])
		}
		eventType := parseEventType(m[3], m[3] != "")
		funcCodes := map[int]int{
			2: funcInsert,
			3: funcDelete,
			5: funcPageUp,
			6: funcPageDn,
			7: funcHome,
			8: funcEnd,
		}
		if codepoint, ok := funcCodes[keyNum]; ok {
			return &parsedKittySequence{codepoint: codepoint, baseLayoutKey: -1, modifier: modValue - 1, eventType: eventType}
		}
	}

	// Home/End with modifier: \x1b[1;<mod>H/F or \x1b[1;<mod>:<event>H/F
	if m := kittyHomeEndRegex.FindStringSubmatch(data); m != nil {
		modValue, _ := strconv.Atoi(m[1])
		eventType := parseEventType(m[2], m[2] != "")
		codepoint := funcEnd
		if m[3] == "H" {
			codepoint = funcHome
		}
		return &parsedKittySequence{codepoint: codepoint, baseLayoutKey: -1, modifier: modValue - 1, eventType: eventType}
	}

	return nil
}

func matchesKittySequence(data string, expectedCodepoint int, expectedModifier int) bool {
	parsed := parseKittySequence(data)
	if parsed == nil {
		return false
	}
	actualMod := parsed.modifier &^ lockMask
	expectedMod := expectedModifier &^ lockMask

	normalizedCodepoint := normalizeShiftedLetterIdentityCodepoint(
		normalizeKittyFunctionalCodepoint(parsed.codepoint), parsed.modifier)
	normalizedExpectedCodepoint := normalizeShiftedLetterIdentityCodepoint(
		normalizeKittyFunctionalCodepoint(expectedCodepoint), expectedModifier)

	// Physical match: primary codepoint and every reported modifier match.
	if actualMod == expectedMod && normalizedCodepoint == normalizedExpectedCodepoint {
		return true
	}

	// Logical match: Kitty reports symbols such as "+" as Shift plus the
	// physical "=" key. Bindings name the produced symbol, so consume that
	// implicit Shift while preserving Ctrl, Alt, and Super. The physical
	// "shift+=" spelling remains valid through the match above.
	if logicalCodepoint, logicalModifier, ok := getLogicalShiftedSymbolCandidate(parsed.shiftedKey, actualMod, symbolKeys); ok &&
		(logicalModifier&^lockMask) == expectedMod &&
		logicalCodepoint == normalizeKittyFunctionalCodepoint(expectedCodepoint) {
		return true
	}

	if actualMod != expectedMod {
		return false
	}

	// Alternate match: use base layout key for non-Latin keyboard layouts.
	// This allows Ctrl+С (Cyrillic) to match Ctrl+c (Latin) when terminal reports
	// the base layout key (the key in standard PC-101 layout).
	//
	// Only fall back to base layout key when the codepoint is NOT already a
	// recognized Latin letter (a-z) or symbol (e.g., /, -, [, ;, etc.).
	// When the codepoint is a recognized key, it is authoritative regardless
	// of physical key position. This prevents remapped layouts (Dvorak, Colemak,
	// xremap, etc.) from causing false matches: both letters and symbols move
	// to different physical positions, so Ctrl+K could falsely match Ctrl+V
	// (letter remapping) and Ctrl+/ could falsely match Ctrl+[ (symbol remapping)
	// if the base layout key were always considered.
	if parsed.baseLayoutKey != -1 && parsed.baseLayoutKey == expectedCodepoint {
		cp := normalizedCodepoint
		isLatinLetter := cp >= 97 && cp <= 122 // a-z
		isKnownSymbol := symbolKeys[string(rune(cp))]
		if !isLatinLetter && !isKnownSymbol {
			return true
		}
	}

	return false
}

var modifyOtherKeysRegex = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)

func parseModifyOtherKeysSequence(data string) *parsedModifyOtherKeysSequence {
	m := modifyOtherKeysRegex.FindStringSubmatch(data)
	if m == nil {
		return nil
	}
	modValue, _ := strconv.Atoi(m[1])
	codepoint, _ := strconv.Atoi(m[2])
	return &parsedModifyOtherKeysSequence{codepoint: codepoint, modifier: modValue - 1}
}

// matchesModifyOtherKeys matches xterm modifyOtherKeys format:
// CSI 27 ; modifiers ; keycode ~. Used by terminals when Kitty protocol is not
// enabled. Modifier values are 1-indexed: 2=shift, 3=alt, 5=ctrl, etc.
func matchesModifyOtherKeys(data string, expectedKeycode int, expectedModifier int) bool {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	return parsed.codepoint == expectedKeycode && parsed.modifier == expectedModifier
}

func isWindowsTerminalSession() bool {
	return os.Getenv("WT_SESSION") != "" &&
		os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_CLIENT") == "" && os.Getenv("SSH_TTY") == ""
}

// matchesRawBackspace: raw 0x08 (BS) is ambiguous in legacy terminals.
//
//   - Windows Terminal uses it for Ctrl+Backspace.
//   - Some legacy terminals and tmux setups send it for plain Backspace.
//
// Prefer explicit Kitty / CSI-u / modifyOtherKeys sequences whenever they are
// available. Fall back to a Windows Terminal heuristic only for raw BS bytes.
func matchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if isWindowsTerminalSession() {
		return expectedModifier == modifierCtrl
	}
	return expectedModifier == 0
}

// =============================================================================
// Generic Key Matching
// =============================================================================

// rawCtrlChar returns the control character for a key.
// Uses the universal formula: code & 0x1f (mask to lower 5 bits)
//
// Works for:
//   - Letters a-z → 1-26
//   - Symbols [\]_ → 27, 28, 29, 31
//   - Also maps - to same as _ (same physical key on US keyboards)
func rawCtrlChar(key string) string {
	char := strings.ToLower(key)
	code := rune(char[0])
	if (code >= 97 && code <= 122) || char == "[" || char == "\\" || char == "]" || char == "_" {
		return string(code & 0x1f)
	}
	// Handle - as _ (same physical key on US keyboards)
	if char == "-" {
		return string(rune(31)) // Same as Ctrl+_
	}
	return ""
}

func isDigitKey(key string) bool {
	return key >= "0" && key <= "9"
}

func matchesPrintableModifyOtherKeys(data string, expectedKeycode int, expectedModifier int) bool {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return false
	}
	physicalMatch := parsed.modifier == expectedModifier &&
		normalizeShiftedLetterIdentityCodepoint(parsed.codepoint, parsed.modifier) ==
			normalizeShiftedLetterIdentityCodepoint(expectedKeycode, expectedModifier)
	if physicalMatch {
		return true
	}
	logicalCodepoint, logicalModifier, ok := getLogicalShiftedSymbolCandidate(
		parsed.codepoint, parsed.modifier, shiftedSymbolKeys)
	return ok && logicalModifier == expectedModifier && logicalCodepoint == expectedKeycode
}

func formatKeyNameWithModifiers(keyName string, modifier int) (string, bool) {
	var mods []string
	effectiveMod := modifier &^ lockMask
	supportedModifierMask := modifierShift | modifierCtrl | modifierAlt | modifierSuper
	if effectiveMod&^supportedModifierMask != 0 {
		return "", false
	}
	if effectiveMod&modifierShift != 0 {
		mods = append(mods, "shift")
	}
	if effectiveMod&modifierCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if effectiveMod&modifierAlt != 0 {
		mods = append(mods, "alt")
	}
	if effectiveMod&modifierSuper != 0 {
		mods = append(mods, "super")
	}
	if len(mods) > 0 {
		return strings.Join(mods, "+") + "+" + keyName, true
	}
	return keyName, true
}

type parsedKeyId struct {
	key   string
	ctrl  bool
	shift bool
	alt   bool
	super bool
}

func parseKeyId(keyId string) *parsedKeyId {
	key := strings.ToLower(keyId)
	modifiers := map[string]bool{}
	for {
		modifier := ""
		for _, name := range modifierNames {
			if strings.HasPrefix(key, name+"+") {
				modifier = name
				break
			}
		}
		if modifier == "" {
			break
		}
		if modifiers[modifier] {
			return nil
		}
		modifiers[modifier] = true
		key = key[len(modifier)+1:]
	}
	if key == "" || (strings.Contains(key, "+") && key != "+") {
		return nil
	}
	return &parsedKeyId{
		key:   key,
		ctrl:  modifiers["ctrl"],
		shift: modifiers["shift"],
		alt:   modifiers["alt"],
		super: modifiers["super"],
	}
}

// MatchesKey matches input data against a key identifier string.
//
// Supported key identifiers:
//   - Single keys: "escape", "tab", "enter", "backspace", "delete", "home", "end", "space"
//   - Arrow keys: "up", "down", "left", "right"
//   - Ctrl combinations: "ctrl+c", "ctrl+z", etc.
//   - Shift combinations: "shift+tab", "shift+enter"
//   - Alt combinations: "alt+enter", "alt+backspace"
//   - Super combinations: "super+k", "super+enter"
//   - Combined modifiers: "shift+ctrl+p", "ctrl+alt+x", "ctrl+super+k"
//
// Use the Key* constants/helpers for readability: KeyCtrl("c"), KeyEscape,
// KeyCtrlShift("p"), KeySuper("k").
func MatchesKey(data string, keyId KeyId) bool {
	parsed := parseKeyId(keyId)
	if parsed == nil {
		return false
	}

	key := parsed.key
	ctrl, shift, alt, superModifier := parsed.ctrl, parsed.shift, parsed.alt, parsed.super
	modifier := 0
	if shift {
		modifier |= modifierShift
	}
	if alt {
		modifier |= modifierAlt
	}
	if ctrl {
		modifier |= modifierCtrl
	}
	if superModifier {
		modifier |= modifierSuper
	}

	switch key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == "\x1b" ||
			matchesKittySequence(data, codepointEscape, 0) ||
			matchesModifyOtherKeys(data, codepointEscape, 0)

	case "space":
		if !IsKittyProtocolActive() {
			if modifier == modifierCtrl && data == "\x00" {
				return true
			}
			if modifier == modifierAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				matchesKittySequence(data, codepointSpace, 0) ||
				matchesModifyOtherKeys(data, codepointSpace, 0)
		}
		return matchesKittySequence(data, codepointSpace, modifier) ||
			matchesModifyOtherKeys(data, codepointSpace, modifier)

	case "tab":
		if modifier == modifierShift {
			return data == "\x1b[Z" ||
				matchesKittySequence(data, codepointTab, modifierShift) ||
				matchesModifyOtherKeys(data, codepointTab, modifierShift)
		}
		if modifier == 0 {
			return data == "\t" || matchesKittySequence(data, codepointTab, 0)
		}
		return matchesKittySequence(data, codepointTab, modifier) ||
			matchesModifyOtherKeys(data, codepointTab, modifier)

	case "enter", "return":
		if modifier == modifierShift {
			// CSI u sequences (standard Kitty protocol)
			if matchesKittySequence(data, codepointEnter, modifierShift) ||
				matchesKittySequence(data, codepointKpEnter, modifierShift) {
				return true
			}
			// xterm modifyOtherKeys format (fallback when Kitty protocol not enabled)
			if matchesModifyOtherKeys(data, codepointEnter, modifierShift) {
				return true
			}
			// When Kitty protocol is active, legacy sequences are custom terminal mappings
			// \x1b\r = Kitty's "map shift+enter send_text all \e\r"
			// \n = Ghostty's "keybind = shift+enter=text:\n"
			if IsKittyProtocolActive() {
				return data == "\x1b\r" || data == "\n"
			}
			return false
		}
		if modifier == modifierAlt {
			// CSI u sequences (standard Kitty protocol)
			if matchesKittySequence(data, codepointEnter, modifierAlt) ||
				matchesKittySequence(data, codepointKpEnter, modifierAlt) {
				return true
			}
			// xterm modifyOtherKeys format (fallback when Kitty protocol not enabled)
			if matchesModifyOtherKeys(data, codepointEnter, modifierAlt) {
				return true
			}
			// \x1b\r is alt+enter only in legacy mode (no Kitty protocol)
			// When Kitty protocol is active, alt+enter comes as CSI u sequence
			if !IsKittyProtocolActive() {
				return data == "\x1b\r"
			}
			return false
		}
		if modifier == 0 {
			return data == "\r" ||
				(!IsKittyProtocolActive() && data == "\n") ||
				data == "\x1bOM" || // SS3 M (numpad enter in some terminals)
				matchesKittySequence(data, codepointEnter, 0) ||
				matchesKittySequence(data, codepointKpEnter, 0)
		}
		return matchesKittySequence(data, codepointEnter, modifier) ||
			matchesKittySequence(data, codepointKpEnter, modifier) ||
			matchesModifyOtherKeys(data, codepointEnter, modifier)

	case "backspace":
		if modifier == modifierAlt {
			if data == "\x1b\x7f" || data == "\x1b\b" {
				return true
			}
			return matchesKittySequence(data, codepointBackspace, modifierAlt) ||
				matchesModifyOtherKeys(data, codepointBackspace, modifierAlt)
		}
		if modifier == modifierCtrl {
			// Legacy raw 0x08 is ambiguous: it can be Ctrl+Backspace on Windows
			// Terminal or plain Backspace on other terminals, while also
			// overlapping with Ctrl+H.
			if matchesRawBackspace(data, modifierCtrl) {
				return true
			}
			return matchesKittySequence(data, codepointBackspace, modifierCtrl) ||
				matchesModifyOtherKeys(data, codepointBackspace, modifierCtrl)
		}
		if modifier == 0 {
			return matchesRawBackspace(data, 0) ||
				matchesKittySequence(data, codepointBackspace, 0) ||
				matchesModifyOtherKeys(data, codepointBackspace, 0)
		}
		return matchesKittySequence(data, codepointBackspace, modifier) ||
			matchesModifyOtherKeys(data, codepointBackspace, modifier)

	case "insert":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["insert"]) ||
				matchesKittySequence(data, funcInsert, 0)
		}
		if matchesLegacyModifierSequence(data, "insert", modifier) {
			return true
		}
		return matchesKittySequence(data, funcInsert, modifier)

	case "delete":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["delete"]) ||
				matchesKittySequence(data, funcDelete, 0)
		}
		if matchesLegacyModifierSequence(data, "delete", modifier) {
			return true
		}
		return matchesKittySequence(data, funcDelete, modifier)

	case "clear":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["clear"])
		}
		return matchesLegacyModifierSequence(data, "clear", modifier)

	case "home":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["home"]) ||
				matchesKittySequence(data, funcHome, 0)
		}
		if matchesLegacyModifierSequence(data, "home", modifier) {
			return true
		}
		return matchesKittySequence(data, funcHome, modifier)

	case "end":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["end"]) ||
				matchesKittySequence(data, funcEnd, 0)
		}
		if matchesLegacyModifierSequence(data, "end", modifier) {
			return true
		}
		return matchesKittySequence(data, funcEnd, modifier)

	case "pageup":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageUp"]) ||
				matchesKittySequence(data, funcPageUp, 0)
		}
		if matchesLegacyModifierSequence(data, "pageUp", modifier) {
			return true
		}
		return matchesKittySequence(data, funcPageUp, modifier)

	case "pagedown":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageDown"]) ||
				matchesKittySequence(data, funcPageDn, 0)
		}
		if matchesLegacyModifierSequence(data, "pageDown", modifier) {
			return true
		}
		return matchesKittySequence(data, funcPageDn, modifier)

	case "up":
		if modifier == modifierAlt {
			return data == "\x1bp" || matchesKittySequence(data, arrowUp, modifierAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["up"]) ||
				matchesKittySequence(data, arrowUp, 0)
		}
		if matchesLegacyModifierSequence(data, "up", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowUp, modifier)

	case "down":
		if modifier == modifierAlt {
			return data == "\x1bn" || matchesKittySequence(data, arrowDown, modifierAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["down"]) ||
				matchesKittySequence(data, arrowDown, 0)
		}
		if matchesLegacyModifierSequence(data, "down", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowDown, modifier)

	case "left":
		if modifier == modifierAlt {
			return data == "\x1b[1;3D" ||
				(!IsKittyProtocolActive() && data == "\x1bB") ||
				data == "\x1bb" ||
				matchesKittySequence(data, arrowLeft, modifierAlt)
		}
		if modifier == modifierCtrl {
			return data == "\x1b[1;5D" ||
				matchesLegacyModifierSequence(data, "left", modifierCtrl) ||
				matchesKittySequence(data, arrowLeft, modifierCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["left"]) ||
				matchesKittySequence(data, arrowLeft, 0)
		}
		if matchesLegacyModifierSequence(data, "left", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowLeft, modifier)

	case "right":
		if modifier == modifierAlt {
			return data == "\x1b[1;3C" ||
				(!IsKittyProtocolActive() && data == "\x1bF") ||
				data == "\x1bf" ||
				matchesKittySequence(data, arrowRight, modifierAlt)
		}
		if modifier == modifierCtrl {
			return data == "\x1b[1;5C" ||
				matchesLegacyModifierSequence(data, "right", modifierCtrl) ||
				matchesKittySequence(data, arrowRight, modifierCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["right"]) ||
				matchesKittySequence(data, arrowRight, 0)
		}
		if matchesLegacyModifierSequence(data, "right", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowRight, modifier)

	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		if modifier != 0 {
			return false
		}
		return matchesLegacySequence(data, legacyKeySequences[key])
	}

	// Handle single letter/digit keys and symbols
	if len(key) == 1 && ((key >= "a" && key <= "z") || isDigitKey(key) || symbolKeys[key]) {
		codepoint := int(key[0])
		rawCtrl := rawCtrlChar(key)
		isLetter := key >= "a" && key <= "z"
		isDigit := isDigitKey(key)

		if modifier == modifierCtrl+modifierAlt && !IsKittyProtocolActive() && rawCtrl != "" {
			// Legacy: ctrl+alt+key is ESC followed by the control character.
			// If that legacy form does not match, continue so CSI-u and
			// modifyOtherKeys sequences from tmux can still be recognized.
			if data == "\x1b"+rawCtrl {
				return true
			}
		}

		if modifier == modifierAlt && !IsKittyProtocolActive() && (isLetter || isDigit || symbolKeys[key]) {
			// Legacy: alt+printable key is ESC followed by the key
			if data == "\x1b"+key {
				return true
			}
		}

		if modifier == modifierCtrl {
			// Legacy: ctrl+key sends the control character
			if rawCtrl != "" && data == rawCtrl {
				return true
			}
			return matchesKittySequence(data, codepoint, modifierCtrl) ||
				matchesPrintableModifyOtherKeys(data, codepoint, modifierCtrl)
		}

		if modifier == modifierShift+modifierCtrl {
			return matchesKittySequence(data, codepoint, modifierShift+modifierCtrl) ||
				matchesPrintableModifyOtherKeys(data, codepoint, modifierShift+modifierCtrl)
		}

		if modifier == modifierShift {
			// Legacy: shift+letter produces uppercase
			if isLetter && data == strings.ToUpper(key) {
				return true
			}
			return matchesKittySequence(data, codepoint, modifierShift) ||
				matchesPrintableModifyOtherKeys(data, codepoint, modifierShift)
		}

		if modifier != 0 {
			return matchesKittySequence(data, codepoint, modifier) ||
				matchesPrintableModifyOtherKeys(data, codepoint, modifier)
		}

		// Check both raw char and Kitty sequence (needed for release events)
		return data == key || matchesKittySequence(data, codepoint, 0) || matchesPrintableModifyOtherKeys(data, codepoint, 0)
	}

	return false
}

// =============================================================================
// Key Parsing
// =============================================================================

func formatParsedKey(codepoint int, modifier int, baseLayoutKey int) (string, bool) {
	normalizedCodepoint := normalizeKittyFunctionalCodepoint(codepoint)
	identityCodepoint := normalizeShiftedLetterIdentityCodepoint(normalizedCodepoint, modifier)

	// Use base layout key only when codepoint is not a recognized Latin
	// letter (a-z), digit (0-9), or symbol (/, -, [, ;, etc.). For those,
	// the codepoint is authoritative regardless of physical key position.
	// This prevents remapped layouts (Dvorak, Colemak, xremap, etc.) from
	// reporting the wrong key name based on the QWERTY physical position.
	isLatinLetter := identityCodepoint >= 97 && identityCodepoint <= 122 // a-z
	isDigit := identityCodepoint >= 48 && identityCodepoint <= 57        // 0-9
	isKnownSymbol := symbolKeys[string(rune(identityCodepoint))]
	effectiveCodepoint := identityCodepoint
	if !isLatinLetter && !isDigit && !isKnownSymbol && baseLayoutKey != -1 {
		effectiveCodepoint = baseLayoutKey
	}

	var keyName string
	switch effectiveCodepoint {
	case codepointEscape:
		keyName = "escape"
	case codepointTab:
		keyName = "tab"
	case codepointEnter, codepointKpEnter:
		keyName = "enter"
	case codepointSpace:
		keyName = "space"
	case codepointBackspace:
		keyName = "backspace"
	case funcDelete:
		keyName = "delete"
	case funcInsert:
		keyName = "insert"
	case funcHome:
		keyName = "home"
	case funcEnd:
		keyName = "end"
	case funcPageUp:
		keyName = "pageUp"
	case funcPageDn:
		keyName = "pageDown"
	case arrowUp:
		keyName = "up"
	case arrowDown:
		keyName = "down"
	case arrowLeft:
		keyName = "left"
	case arrowRight:
		keyName = "right"
	default:
		if effectiveCodepoint >= 48 && effectiveCodepoint <= 57 {
			keyName = string(rune(effectiveCodepoint))
		} else if effectiveCodepoint >= 97 && effectiveCodepoint <= 122 {
			keyName = string(rune(effectiveCodepoint))
		} else if symbolKeys[string(rune(effectiveCodepoint))] {
			keyName = string(rune(effectiveCodepoint))
		} else {
			return "", false
		}
	}

	return formatKeyNameWithModifiers(keyName, modifier)
}

// ParseKey parses input data and returns the key identifier if recognized.
// ok is false when the data does not correspond to a recognized key
// (pi: returns undefined).
func ParseKey(data string) (string, bool) {
	if kitty := parseKittySequence(data); kitty != nil {
		codepoint, modifier := kitty.codepoint, kitty.modifier
		if logicalCodepoint, logicalModifier, ok := getLogicalShiftedSymbolCandidate(kitty.shiftedKey, kitty.modifier, symbolKeys); ok {
			codepoint, modifier = logicalCodepoint, logicalModifier
		}
		return formatParsedKey(codepoint, modifier, kitty.baseLayoutKey)
	}

	if modifyOtherKeys := parseModifyOtherKeysSequence(data); modifyOtherKeys != nil {
		codepoint, modifier := modifyOtherKeys.codepoint, modifyOtherKeys.modifier
		if logicalCodepoint, logicalModifier, ok := getLogicalShiftedSymbolCandidate(modifyOtherKeys.codepoint, modifyOtherKeys.modifier, shiftedSymbolKeys); ok {
			codepoint, modifier = logicalCodepoint, logicalModifier
		}
		return formatParsedKey(codepoint, modifier, -1)
	}

	// Mode-aware legacy sequences
	// When Kitty protocol is active, ambiguous sequences are interpreted as custom terminal mappings:
	// - \x1b\r = shift+enter (Kitty mapping), not alt+enter
	// - \n = shift+enter (Ghostty mapping)
	if IsKittyProtocolActive() {
		if data == "\x1b\r" || data == "\n" {
			return "shift+enter", true
		}
	}

	if legacySequenceKeyId, ok := legacySequenceKeyIds[data]; ok {
		return legacySequenceKeyId, true
	}

	// Legacy sequences (used when Kitty protocol is not active, or for unambiguous sequences)
	switch {
	case data == "\x1b":
		return "escape", true
	case data == "\x1c":
		return "ctrl+\\", true
	case data == "\x1d":
		return "ctrl+]", true
	case data == "\x1f":
		return "ctrl+-", true
	case data == "\x1b\x1b":
		return "ctrl+alt+[", true
	case data == "\x1b\x1c":
		return "ctrl+alt+\\", true
	case data == "\x1b\x1d":
		return "ctrl+alt+]", true
	case data == "\x1b\x1f":
		return "ctrl+alt+-", true
	case data == "\t":
		return "tab", true
	case data == "\r" || (!IsKittyProtocolActive() && data == "\n") || data == "\x1bOM":
		return "enter", true
	case data == "\x00":
		return "ctrl+space", true
	case data == " ":
		return "space", true
	case data == "\x7f":
		return "backspace", true
	case data == "\x08":
		if isWindowsTerminalSession() {
			return "ctrl+backspace", true
		}
		return "backspace", true
	case data == "\x1b[Z":
		return "shift+tab", true
	case !IsKittyProtocolActive() && data == "\x1b\r":
		return "alt+enter", true
	case !IsKittyProtocolActive() && data == "\x1b ":
		return "alt+space", true
	case data == "\x1b\x7f" || data == "\x1b\b":
		return "alt+backspace", true
	case !IsKittyProtocolActive() && data == "\x1bB":
		return "alt+left", true
	case !IsKittyProtocolActive() && data == "\x1bF":
		return "alt+right", true
	}

	if !IsKittyProtocolActive() && len(data) == 2 && data[0] == '\x1b' {
		code := int(data[1])
		if code >= 1 && code <= 26 {
			return "ctrl+alt+" + string(rune(code+96)), true
		}
		// Legacy alt+letter/digit/symbol (ESC followed by the key)
		key := string(data[1])
		if (code >= 97 && code <= 122) || (code >= 48 && code <= 57) || symbolKeys[key] {
			return "alt+" + key, true
		}
	}

	switch data {
	case "\x1b[A":
		return "up", true
	case "\x1b[B":
		return "down", true
	case "\x1b[C":
		return "right", true
	case "\x1b[D":
		return "left", true
	case "\x1b[H", "\x1bOH":
		return "home", true
	case "\x1b[F", "\x1bOF":
		return "end", true
	case "\x1b[3~":
		return "delete", true
	case "\x1b[5~":
		return "pageUp", true
	case "\x1b[6~":
		return "pageDown", true
	}

	// Raw Ctrl+letter
	if len(data) == 1 {
		code := int(data[0])
		if code >= 1 && code <= 26 {
			return "ctrl+" + string(rune(code+96)), true
		}
		if code >= 32 && code <= 126 {
			return data, true
		}
	}

	return "", false
}

// =============================================================================
// Kitty CSI-u Printable Decoding
// =============================================================================

var kittyPrintableAllowedModifiers = modifierShift | lockMask

// DecodeKittyPrintable decodes a Kitty CSI-u sequence into a printable
// character, if applicable.
//
// When Kitty keyboard protocol flag 1 (disambiguate) is active, terminals send
// CSI-u sequences for all keys, including plain printable characters. This
// function extracts the printable character from such sequences.
//
// Only accepts plain or Shift-modified keys. Rejects Ctrl, Alt, and unsupported
// modifier combinations (those are handled by keybinding matching instead).
// Prefers the shifted keycode when Shift is held and a shifted key is reported.
//
// ok is false when data is not a printable CSI-u sequence (pi: undefined).
func DecodeKittyPrintable(data string) (string, bool) {
	m := kittyCsiURegex.FindStringSubmatch(data)
	if m == nil {
		return "", false
	}

	// CSI-u groups: <codepoint>[:<shifted>[:<base>]];<mod>[:<event>]u
	codepoint, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}

	var shiftedKey int
	hasShifted := m[2] != ""
	if hasShifted {
		shiftedKey, _ = strconv.Atoi(m[2])
	}
	modValue := 1
	if m[4] != "" {
		if v, err := strconv.Atoi(m[4]); err == nil {
			modValue = v
		}
	}
	// Modifiers are 1-indexed in CSI-u; normalize to our bitmask.
	modifier := 0
	if modValue >= 1 {
		modifier = modValue - 1
	}

	// Only accept printable CSI-u input for plain or Shift-modified text keys.
	// Reject unsupported modifier bits (e.g. Super/Meta) to avoid inserting
	// characters from modifier-only terminal events.
	if modifier&^kittyPrintableAllowedModifiers != 0 {
		return "", false
	}
	if modifier&(modifierAlt|modifierCtrl) != 0 {
		return "", false
	}

	// Prefer the shifted keycode when Shift is held.
	effectiveCodepoint := codepoint
	if modifier&modifierShift != 0 && hasShifted {
		effectiveCodepoint = shiftedKey
	}
	effectiveCodepoint = normalizeKittyFunctionalCodepoint(effectiveCodepoint)
	// Drop control characters or invalid codepoints.
	if effectiveCodepoint < 32 || effectiveCodepoint > 0x10FFFF {
		return "", false
	}

	return string(rune(effectiveCodepoint)), true
}

func decodeModifyOtherKeysPrintable(data string) (string, bool) {
	parsed := parseModifyOtherKeysSequence(data)
	if parsed == nil {
		return "", false
	}
	modifier := parsed.modifier &^ lockMask
	if modifier&^modifierShift != 0 {
		return "", false
	}
	if parsed.codepoint < 32 || parsed.codepoint > 0x10FFFF {
		return "", false
	}
	return string(rune(parsed.codepoint)), true
}

// DecodePrintableKey decodes a printable key from Kitty CSI-u or xterm
// modifyOtherKeys sequences. ok is false when not decodable (pi: undefined).
func DecodePrintableKey(data string) (string, bool) {
	if s, ok := DecodeKittyPrintable(data); ok {
		return s, true
	}
	return decodeModifyOtherKeysPrintable(data)
}
