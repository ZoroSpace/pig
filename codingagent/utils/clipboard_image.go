// clipboard_image.go (pi: src/utils/clipboard-image.ts) — read an image from
// the system clipboard (Wayland / X11 / WSL PowerShell paths).
//
// Segment deviations:
//   - pi converts unsupported formats (e.g. BMP from WSLg) to PNG via photon
//     (WASM). The photon pipeline lands with Phase 8; convertToPng returns
//     not-ok until then (declared).
//   - pi's native-addon half (clipboard.hasImage/getImageBinary) has no Go
//     equivalent. On darwin pig substitutes external tools (pig-only,
//     docs/feat/MAC-CLIPBOARD-IMAGE.md): osascript ships with every macOS and
//     reads the PNG pasteboard flavor; optional pngpaste (user-installed)
//     covers TIFF-only clipboards. Windows still reports nothing (declared).
package utils

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ClipboardImage is an image read from the clipboard (pi: ClipboardImage).
type ClipboardImage struct {
	Bytes    []byte
	MimeType string
}

var supportedImageMimeTypes = []string{"image/png", "image/jpeg", "image/webp", "image/gif"}

const (
	clipboardDefaultListTimeoutMs       = 1000
	clipboardDefaultReadTimeoutMs       = 3000
	clipboardDefaultPowershellTimeoutMs = 5000
	// clipboardDefaultOsascriptTimeoutMs is generous on purpose (pig-only):
	// osascript dumps the PNG flavor as hex (2x the bytes) and multi-MB
	// screenshots can take seconds; the 3s read default is too tight.
	clipboardDefaultOsascriptTimeoutMs = 10000
)

// IsWaylandSession reports a Wayland session (pi: isWaylandSession).
func IsWaylandSession(env func(string) string) bool {
	return isWaylandSessionEnv(env)
}

// clipboardBaseMimeType strips parameters and lowercases (pi: baseMimeType).
func clipboardBaseMimeType(mimeType string) string {
	base := strings.SplitN(mimeType, ";", 2)[0]
	base = strings.TrimSpace(base)
	if base == "" {
		return strings.ToLower(mimeType)
	}
	return strings.ToLower(base)
}

// ExtensionForImageMimeType maps an image MIME type to a file extension (pi:
// extensionForImageMimeType); "" = unsupported.
func ExtensionForImageMimeType(mimeType string) string {
	switch clipboardBaseMimeType(mimeType) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return ""
	}
}

// selectPreferredImageMimeType picks the first supported type, then any
// image/* (pi: selectPreferredImageMimeType).
func selectPreferredImageMimeType(mimeTypes []string) string {
	type normalizedType struct{ raw, base string }
	var normalized []normalizedType
	for _, t := range mimeTypes {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		normalized = append(normalized, normalizedType{raw: t, base: clipboardBaseMimeType(t)})
	}

	for _, preferred := range supportedImageMimeTypes {
		for _, t := range normalized {
			if t.base == preferred {
				return t.raw
			}
		}
	}

	for _, t := range normalized {
		if strings.HasPrefix(t.base, "image/") {
			return t.raw
		}
	}
	return ""
}

func isSupportedImageMimeType(mimeType string) bool {
	base := clipboardBaseMimeType(mimeType)
	for _, t := range supportedImageMimeTypes {
		if t == base {
			return true
		}
	}
	return false
}

// clipboardRunCommand spawns a command with a timeout (pi: runCommand);
// ok=false on any failure (pi returns empty stdout + ok=false).
func clipboardRunCommand(command string, args []string, timeoutMs int64) ([]byte, bool) {
	if timeoutMs == 0 {
		timeoutMs = clipboardDefaultReadTimeoutMs
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, args...).Output()
	if err != nil {
		return nil, false
	}
	return out, true
}

// clipboardRunCommandFn indirections clipboardRunCommand (pig-only test seam:
// tests stub it so the darwin paths never spawn real clipboard processes).
var clipboardRunCommandFn = clipboardRunCommand

// readClipboardImageViaWlPaste reads via wl-paste type negotiation (pi:
// readClipboardImageViaWlPaste).
func readClipboardImageViaWlPaste() *ClipboardImage {
	list, ok := clipboardRunCommand("wl-paste", []string{"--list-types"}, clipboardDefaultListTimeoutMs)
	if !ok {
		return nil
	}

	var types []string
	for _, t := range strings.FieldsFunc(strings.ReplaceAll(string(list), "\r\n", "\n"), func(r rune) bool { return r == '\n' }) {
		t = strings.TrimSpace(t)
		if t != "" {
			types = append(types, t)
		}
	}

	selectedType := selectPreferredImageMimeType(types)
	if selectedType == "" {
		return nil
	}

	data, ok := clipboardRunCommand("wl-paste", []string{"--type", selectedType, "--no-newline"}, 0)
	if !ok || len(data) == 0 {
		return nil
	}

	return &ClipboardImage{Bytes: data, MimeType: clipboardBaseMimeType(selectedType)}
}

// clipboardIsWSL detects WSL (pi: isWSL).
func clipboardIsWSL(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	if env("WSL_DISTRO_NAME") != "" || env("WSLENV") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?i)microsoft|wsl`).Match(release)
}

// readClipboardImageViaPowerShell reads the Windows clipboard from WSL (pi:
// readClipboardImageViaPowerShell).
func readClipboardImageViaPowerShell() *ClipboardImage {
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("pi-wsl-clip-%x-%x-%x-%x.png",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10]))

	winPath, ok := clipboardRunCommand("wslpath", []string{"-w", tmpFile}, clipboardDefaultListTimeoutMs)
	if !ok {
		return nil
	}

	winPathStr := strings.TrimSpace(string(winPath))
	if winPathStr == "" {
		return nil
	}

	psQuotedWinPath := strings.ReplaceAll(winPathStr, "'", "''")
	psScript := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		fmt.Sprintf("$path = '%s'", psQuotedWinPath),
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }",
	}, "; ")

	result, ok := clipboardRunCommand("powershell.exe", []string{"-NoProfile", "-Command", psScript},
		clipboardDefaultPowershellTimeoutMs)
	if !ok {
		return nil
	}

	output := strings.TrimSpace(string(result))
	if output != "ok" {
		return nil
	}

	bytes, err := os.ReadFile(tmpFile)
	if err != nil || len(bytes) == 0 {
		_ = os.Remove(tmpFile)
		return nil
	}
	_ = os.Remove(tmpFile)
	return &ClipboardImage{Bytes: bytes, MimeType: "image/png"}
}

// readClipboardImageViaXclip enumerates TARGETS and reads the best type (pi:
// readClipboardImageViaXclip).
func readClipboardImageViaXclip() *ClipboardImage {
	targets, ok := clipboardRunCommand("xclip",
		[]string{"-selection", "clipboard", "-t", "TARGETS", "-o"}, clipboardDefaultListTimeoutMs)

	var candidateTypes []string
	if ok {
		for _, t := range strings.FieldsFunc(strings.ReplaceAll(string(targets), "\r\n", "\n"), func(r rune) bool { return r == '\n' }) {
			t = strings.TrimSpace(t)
			if t != "" {
				candidateTypes = append(candidateTypes, t)
			}
		}
	}

	var tryTypes []string
	if preferred := selectPreferredImageMimeType(candidateTypes); preferred != "" {
		tryTypes = append([]string{preferred}, supportedImageMimeTypes...)
	} else {
		tryTypes = append([]string{}, supportedImageMimeTypes...)
	}

	for _, mimeType := range tryTypes {
		data, ok := clipboardRunCommand("xclip", []string{"-selection", "clipboard", "-t", mimeType, "-o"}, 0)
		if ok && len(data) > 0 {
			return &ClipboardImage{Bytes: data, MimeType: clipboardBaseMimeType(mimeType)}
		}
	}

	return nil
}

// readClipboardImageViaOsascript reads the PNG pasteboard flavor via osascript
// (pig-only; darwin transport for pi's native-addon getImageBinary, which
// returns image/png as well). ok=false when the clipboard carries no PNG
// flavor (osascript exits non-zero) or the output does not decode.
func readClipboardImageViaOsascript() *ClipboardImage {
	out, ok := clipboardRunCommandFn("osascript",
		[]string{"-e", `the clipboard as «class PNGf»`}, clipboardDefaultOsascriptTimeoutMs)
	if !ok {
		return nil
	}
	bytes, ok := decodeOsascriptPngData(out)
	if !ok || len(bytes) == 0 {
		return nil
	}
	return &ClipboardImage{Bytes: bytes, MimeType: "image/png"}
}

// decodeOsascriptPngData decodes osascript's binary-literal output
// `«data PNGf<hex>»` (pig-only). AppleScript renders the PNGf flavor as one
// data token; the hex payload is the raw PNG bytes. Trailing newlines are
// tolerated; anything else (missing prefix/suffix, odd length, bad hex) is a
// decode failure.
func decodeOsascriptPngData(output []byte) ([]byte, bool) {
	const prefix = "«data PNGf"
	const suffix = "»"
	s := strings.TrimSpace(string(output))
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) {
		return nil, false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(s, prefix), suffix)
	if len(hexPart) == 0 || len(hexPart)%2 != 0 {
		return nil, false
	}
	bytes, err := hex.DecodeString(hexPart)
	if err != nil || len(bytes) == 0 {
		return nil, false
	}
	return bytes, true
}

// readClipboardImageViaPngpaste reads via the optional pngpaste tool
// (pig-only fallback for readClipboardImageViaOsascript; it also converts
// TIFF-only clipboards, which the PNGf flavor path cannot see). Absent or
// failing tool → nil.
func readClipboardImageViaPngpaste() *ClipboardImage {
	data, ok := clipboardRunCommandFn("pngpaste", []string{"-"}, 0)
	if !ok || len(data) == 0 {
		return nil
	}
	return &ClipboardImage{Bytes: data, MimeType: "image/png"}
}

// ReadClipboardImageOptions parameterize ReadClipboardImage (pi: the options
// param; nil = process values).
type ReadClipboardImageOptions struct {
	// Env overrides the environment lookup (pig: test seam).
	Env func(string) string
	// Platform overrides the GOOS lookup (pig: test seam).
	Platform string
}

// ReadClipboardImage reads an image from the system clipboard (pi:
// readClipboardImage); nil = nothing available.
func ReadClipboardImage(options *ReadClipboardImageOptions) *ClipboardImage {
	env := os.Getenv
	platform := runtime.GOOS
	if options != nil {
		if options.Env != nil {
			env = options.Env
		}
		if options.Platform != "" {
			platform = options.Platform
		}
	}

	if env("TERMUX_VERSION") != "" {
		return nil
	}

	var image *ClipboardImage

	if platform == "linux" {
		wsl := clipboardIsWSL(env)
		wayland := isWaylandSessionEnv(env)

		if wayland || wsl {
			image = readClipboardImageViaWlPaste()
			if image == nil {
				image = readClipboardImageViaXclip()
			}
		}

		if image == nil && wsl {
			image = readClipboardImageViaPowerShell()
		}

		// pi's native-addon half has no equivalent (file header); the
		// non-wayland X11 fallback still runs.
		if image == nil && !wayland {
			image = readClipboardImageViaXclip()
		}
	} else if platform == "darwin" {
		// pig-only: osascript first (always present), then pngpaste when
		// installed (TIFF-only clipboards; see file header).
		image = readClipboardImageViaOsascript()
		if image == nil {
			image = readClipboardImageViaPngpaste()
		}
	}
	// Windows: pi's native-addon half only — no Go equivalent (declared).

	if image == nil {
		return nil
	}

	// Convert unsupported formats (e.g., BMP from WSLg) to PNG. The photon
	// pipeline lands with Phase 8 (declared).
	if !isSupportedImageMimeType(image.MimeType) {
		return nil
	}

	return image
}
