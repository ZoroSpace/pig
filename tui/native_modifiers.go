// tui/native_modifiers.go (pi: packages/tui/src/native-modifiers.ts) —
// native modifier-key probing helper.
//
// pi v0.86.0 moved the platform helper loading to native-platform.ts and
// native-modifiers.ts became a thin delegate over getNativePlatformHelper().
// pig's stub story lives in native_platform.go (no native modules in Go —
// see the file header there): IsNativeModifierPressed always returns false,
// which matches pi's "helper not available" degradation path (Shift+Enter
// detection falls back to the non-native input normalization).
package tui

// IsNativeModifierPressed reports whether a native modifier key is
// physically held (pi: isNativeModifierPressed → helper.isModifierPressed).
// Always false in pig (see native_platform.go header).
func IsNativeModifierPressed(key ModifierKey) bool {
	_ = getNativePlatformHelper()
	_ = key
	return false
}
