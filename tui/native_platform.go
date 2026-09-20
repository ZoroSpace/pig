// tui/native_platform.go (pi v0.86.0: packages/tui/src/native-platform.ts) —
// unified native platform helper surface (clipboard + Windows VT input +
// modifier probing).
//
// Declared deviation (extends the v0.85.1 native_modifiers.go note): pi loads
// platform .node modules (darwin-platform / win32-platform /
// linux-platform-x11) through native-module-path.ts candidate resolution. Go
// has no counterpart for that machinery — every accessor here returns nil /
// false. pig reaches the same functional outcomes differently:
//   - clipboard text/image: external platform tools (codingagent/utils/
//     clipboard.go, clipboard_image.go — xclip/xsel/wl-*/pbcopy/clip,
//     osascript/pngpaste) plus OSC 52 fallbacks;
//   - Shift+Enter detection degrades per pi's "native helper not available"
//     path (IsNativeModifierPressed always false);
//   - Windows VT input enabling (pi: enableVirtualTerminalInput) is a no-op
//     here (see terminal.go header; WINDOWS-SUPPORT covers the size-poll
//     half in terminal_windows.go).
//
// The NativeClipboard interface and GetNativeClipboard are declared for
// structural parity with pi v0.86.0's index.ts export; the coding-agent
// clipboard consumers (pi: utils/clipboard.ts, utils/clipboard-image.ts)
// keep their external-tool paths in pig (S6-3 re-examines the seam).
package tui

// ModifierKey is a physically probeable modifier key
// (pi: type ModifierKey = "shift" | "command" | "control" | "option").
type ModifierKey string

const (
	ModifierKeyShift   ModifierKey = "shift"
	ModifierKeyCommand ModifierKey = "command"
	ModifierKeyControl ModifierKey = "control"
	ModifierKeyOption  ModifierKey = "option"
)

// NativeClipboard mirrors pi v0.86.0's NativeClipboard interface.
// Three-state semantics (pi: string | null | undefined):
//   - ok == false           → helper/display unavailable (pi undefined);
//   - ok == true, nil value → empty clipboard (pi null);
//   - ok == true, value     → payload. Transfer failures surface as err
//     (pi: promise rejection).
type NativeClipboard interface {
	GetText() (text *string, ok bool, err error)
	GetImage() (image []byte, ok bool, err error)
	// SetText writes text to the system clipboard. Only Linux lacks it:
	// command-line tools are used there to retain clipboard ownership
	// (pi: setText? optional member).
	SetText(text string) error
}

// GetNativeClipboard returns the platform clipboard helper without opening
// the display until a read is requested (pi: getNativeClipboard — Linux
// requires DISPLAY for the X11 helper). Always nil in pig: there is no
// native helper to load (see file header).
func GetNativeClipboard() NativeClipboard {
	return nil
}

// nativePlatformHelper is the composite helper type pi loads from
// darwin/win32 .node modules (pi: NativePlatformHelper). Unloadable in Go —
// declared deviation, see file header.
type nativePlatformHelper struct{}

// getNativePlatformHelper would load the native helper for darwin/win32
// (pi: getNativePlatformHelper). Returns nil in pig.
func getNativePlatformHelper() *nativePlatformHelper {
	return nil
}
