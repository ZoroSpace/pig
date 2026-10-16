// clipboard.go (pi: src/utils/clipboard.ts) — system clipboard text
// read/write with OSC 52 fallback for remote sessions (rewritten in pi
// v0.86.0 around runClipboardCommand).
//
// Segment deviations:
//   - pi's native path (getNativeClipboard()) has no Go equivalent:
//     pig's GetNativeClipboard() is nil (S6-2). Every platform goes through
//     the external tools (and OSC 52); reads gain platform-tool fallbacks
//     that pi's native addon covered (pbpaste / powershell Get-Clipboard /
//     xclip / xsel), and writes skip the non-Linux native setText step.
//     Declared; see docs/feat/MAC-CLIPBOARD-IMAGE.md for the image-path
//     equivalent.
//   - pi returns `string | null`; pig returns "" for null (existing
//     signature).
package utils

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maxOsc52EncodedLength = 100_000

// clipboardCommandTimeoutMs is the shared 5s budget (pi: timeoutMs: 5000).
const clipboardCommandTimeoutMs = 5000

// clipboardPlatformFn indirections the platform lookup (pig-only test seam;
// pi: node:os platform() mock).
var clipboardPlatformFn = func() string { return runtime.GOOS }

// clipboardRunCommandOptionsFn indirections runClipboardCommand for the text
// path (pig-only test seam; pi: vi.mock of ./clipboard-command.ts). The
// image path keeps the legacy clipboardRunCommandFn seam.
var clipboardRunCommandOptionsFn = runClipboardCommand

// osc52Writer is the stdout seam for emitOsc52 (pig: tests).
var osc52Writer = func(b []byte) (int, error) { return os.Stdout.Write(b) }

// isRemoteSession reports whether the session runs over SSH/Mosh (pi:
// isRemoteSession). Remote sessions also emit OSC 52 so the client terminal
// receives the copy. PI_CLIPBOARD_OSC52=1 covers remote clients that leave no
// SSH markers, such as multiplexer attaches (#10114, #10835) or IDE daemons
// (#10826).
func isRemoteSession(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return env("SSH_CONNECTION") != "" || env("SSH_CLIENT") != "" || env("MOSH_CONNECTION") != "" || env("PI_CLIPBOARD_OSC52") == "1"
}

// emitOsc52 writes the text to the system clipboard via OSC 52 (pi:
// emitOsc52); false = payload too large.
func emitOsc52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > maxOsc52EncodedLength {
		return false
	}
	_, _ = osc52Writer([]byte(fmt.Sprintf("\x1b]52;c;%s\x07", encoded)))
	return true
}

// isWaylandSessionEnv reports a Wayland session (pi: isWaylandSession,
// clipboard-image.ts). isWaylandSessionEnv lives here; see
// clipboard_image.go for the exported alias.
func isWaylandSessionEnv(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return env("WAYLAND_DISPLAY") != "" || env("XDG_SESSION_TYPE") == "wayland"
}

// IsWaylandSession reports a Wayland session (pi: isWaylandSession,
// clipboard-image.ts; kept for callers).
func IsWaylandSession(env func(string) string) bool {
	return isWaylandSessionEnv(env)
}

// ReadClipboardText reads plain text from the system clipboard (pi:
// readClipboardText; v0.86.0 chains the platform tools via
// runClipboardCommand). "" = nothing available (pi null).
func ReadClipboardText() string {
	if clipboardPlatformFn() == "linux" {
		type clipboardCommand struct {
			command string
			args    []string
		}
		var commands []clipboardCommand
		if os.Getenv("TERMUX_VERSION") != "" {
			commands = append(commands, clipboardCommand{command: "termux-clipboard-get"})
		}
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, clipboardCommand{command: "wl-paste", args: []string{"--no-newline", "--type", "text"}})
		}
		if os.Getenv("DISPLAY") != "" {
			commands = append(commands,
				clipboardCommand{command: "xclip", args: []string{"-selection", "clipboard", "-out"}},
				clipboardCommand{command: "xsel", args: []string{"--clipboard", "--output"}},
			)
		}
		for _, command := range commands {
			if bytes, ok := clipboardRunCommandOptionsFn(command.command, command.args,
				&clipboardCommandOptions{TimeoutMs: clipboardCommandTimeoutMs}); ok {
				text := string(bytes)
				if text == "" {
					return "" // pi: bytes.toString("utf8") || null
				}
				return text
			}
		}
	}

	// pi's native getText() half; pig has no native clipboard (file header):
	// nil → null → "". A native failure is swallowed like pi's try/catch.
	return ""
}

// clipboardFilePathsRunCommandFn indirections the osascript invocation for
// ReadClipboardFilePaths (pig-only test seam; the legacy clipboardRunCommandFn
// seam belongs to the image path).
var clipboardFilePathsRunCommandFn = clipboardRunCommandOptionsFn

// ReadClipboardFilePaths reads file paths, such as Finder file copies, from
// the clipboard (pi: readClipboardFilePaths — the native addon's
// getFilePaths, which reads NSPasteboard file URLs; a rejected promise maps
// to a non-nil error). pig has no native clipboard (file header): macOS
// substitutes osascript reading the «class furl» pasteboard flavor, the same
// external-tool route as image reading; other platforms have no equivalent
// reader, matching pi's addon coverage. nil = nothing available (pi null).
func ReadClipboardFilePaths() ([]string, error) {
	if clipboardPlatformFn() != "darwin" {
		return nil, nil
	}
	// Finder copies write the file URLs pasteboard flavor; a single copied
	// item coerces to «class furl», several to «class furl» list. Try the
	// list form first, then the single form. POSIX path per item, one per
	// line.
	script := `set output to ""
try
	repeat with f in (the clipboard as «class furl» list)
		set output to output & (POSIX path of f) & linefeed
	end repeat
on error number -1700
	try
		set output to (POSIX path of (the clipboard as «class furl»)) & linefeed
	end try
end try
return output`
	out, ok := clipboardFilePathsRunCommandFn("osascript", []string{"-e", script},
		&clipboardCommandOptions{TimeoutMs: clipboardCommandTimeoutMs})
	if !ok {
		return nil, fmt.Errorf("clipboard file path read failed")
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// copyViaWindowsClipboard writes the Windows clipboard through WSL interop
// (pi: copyViaWindowsClipboard). WSL without WSLg has no Linux display, so
// PowerShell reads the text from a file because `clip.exe` and PowerShell
// stdin decode piped bytes with the console code page, which mangles
// non-ASCII UTF-8. ok=false = pi returning undefined (fall through).
func copyViaWindowsClipboard(text string) bool {
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return false
	}
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("pi-wsl-clip-%x-%x-%x-%x.txt",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10]))
	defer func() { _ = os.Remove(tmpFile) }()
	if err := os.WriteFile(tmpFile, []byte(text), 0o600); err != nil {
		return false
	}
	winPath, ok := clipboardRunCommandOptionsFn("wslpath", []string{"-w", tmpFile},
		&clipboardCommandOptions{TimeoutMs: 1000})
	if !ok {
		return false
	}
	winPathStr := strings.TrimSpace(string(winPath))
	if winPathStr == "" {
		return false
	}
	script := fmt.Sprintf(`Set-Clipboard -Value ([System.IO.File]::ReadAllText('%s', [System.Text.Encoding]::UTF8))`,
		strings.ReplaceAll(winPathStr, "'", "''"))
	_, ok = clipboardRunCommandOptionsFn("powershell.exe", []string{"-NoProfile", "-Command", script},
		&clipboardCommandOptions{TimeoutMs: clipboardCommandTimeoutMs})
	return ok
}

// CopyToClipboard writes text to the system clipboard, preferring direct
// clipboard writes and emitting OSC 52 for remote sessions (pi:
// copyToClipboard; v0.86.0 — "Direct writes precede OSC 52 so the terminal
// cannot race the native writer"; v0.86.1 — WSL routes + OSC 52 for headless
// sessions, #9688). See the file header for the native-addon deviation.
func CopyToClipboard(text string) error {
	p := clipboardPlatformFn()
	copied := false

	// pi first tries the native clipboard on non-Linux platforms; pig has no
	// native clipboard, so the tool chain runs on every platform.

	if !copied {
		type clipboardWriteCommand struct {
			command string
			args    []string
		}
		var commands []clipboardWriteCommand
		switch p {
		case "darwin":
			commands = append(commands, clipboardWriteCommand{command: "pbcopy"})
		case "windows":
			commands = append(commands, clipboardWriteCommand{command: "clip"})
		default:
			if os.Getenv("TERMUX_VERSION") != "" {
				commands = append(commands, clipboardWriteCommand{command: "termux-clipboard-set"})
			}
			if os.Getenv("WAYLAND_DISPLAY") != "" {
				commands = append(commands, clipboardWriteCommand{command: "wl-copy"})
			}
			if os.Getenv("DISPLAY") != "" {
				commands = append(commands,
					clipboardWriteCommand{command: "xclip", args: []string{"-selection", "clipboard"}},
					clipboardWriteCommand{command: "xsel", args: []string{"--clipboard", "--input"}},
				)
			}
		}
		for _, command := range commands {
			if _, ok := clipboardRunCommandOptionsFn(command.command, command.args,
				&clipboardCommandOptions{Input: text, HasInput: true, TimeoutMs: clipboardCommandTimeoutMs}); ok {
				copied = true
				break
			}
		}
	}

	osc52Emitted := false
	if !copied && p == "linux" && isWSL(nil) {
		// Windows Terminal supports OSC 52; prefer it over the slower PowerShell round trip.
		if os.Getenv("WT_SESSION") != "" {
			osc52Emitted = emitOsc52(text)
		}
		copied = osc52Emitted || copyViaWindowsClipboard(text)
	}
	// OSC 52 cannot be verified, so a desktop session with a display reports the failure
	// instead (#9618). Without a display the terminal is the only clipboard route (containers,
	// WSL without WSLg), and remote sessions always emit it to reach the client clipboard.
	headless := p == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("TERMUX_VERSION") == ""
	oversized := false
	if !osc52Emitted && (isRemoteSession(nil) || (!copied && headless)) {
		if emitOsc52(text) {
			copied = true
		} else {
			oversized = true
		}
	}
	if copied {
		return nil
	}
	if oversized {
		return fmt.Errorf("Clipboard unavailable: text exceeds the OSC 52 size limit")
	}
	if p == "linux" {
		if os.Getenv("TERMUX_VERSION") != "" {
			return fmt.Errorf("Clipboard unavailable: install the Termux:API app and `termux-api` package")
		}
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return fmt.Errorf("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
		}
		if os.Getenv("DISPLAY") != "" {
			return fmt.Errorf("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
		}
	}
	return fmt.Errorf("Clipboard unavailable")
}
