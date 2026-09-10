// tui/native_modifiers.go (pi: packages/tui/src/native-modifiers.ts) —
// native modifier-key probing helper.
//
// Declared deviation: pi loads a platform .node native module
// (darwin-modifiers / win32-console-mode) to detect physically held modifiers.
// Go has no equivalent native module in this port — IsNativeModifierPressed
// always returns false until a CGO/platform-specific implementation is
// justified (see README §4 Phase 6 "可砍/延后"; native-module-path.ts has no
// Go counterpart either).
package tui

// IsNativeModifierPressed reports whether a native modifier key
// ("shift" | "command" | "control" | "option") is physically held.
// Currently a stub returning false (see file header).
func IsNativeModifierPressed(key string) bool {
	_ = key
	return false
}
