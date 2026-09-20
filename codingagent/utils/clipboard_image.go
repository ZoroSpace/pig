// clipboard_image.go (pi: src/utils/clipboard-image.ts) — read an image
// from the system clipboard (Wayland / X11 / WSL PowerShell paths).
//
// pi v0.86.0 reworked the chain around a three-state protocol: a backend
// that answers but has no image returns null (pi) and stops the chain, while
// a backend that cannot answer returns undefined (pi) and falls through to
// the next backend ("An empty Wayland clipboard must not fall through to
// stale X11 clipboard contents"). pig models the two nils with
// clipboardImageResult.
//
// Segment deviations:
//   - pi converts unsupported formats (e.g. Windows DIB data wrapped as BMP)
//     to PNG via photon (WASM). The photon pipeline lands with Phase 8;
//     convertToPng returns not-ok until then (declared).
//   - pi's native path (getNativeClipboard().getImage()) has no Go
//     equivalent: pig's GetNativeClipboard() is nil (S6-2). On darwin pig
//     substitutes external tools (pig-only, docs/feat/MAC-CLIPBOARD-IMAGE.md):
//     osascript ships with every macOS and reads the PNG pasteboard flavor;
//     optional pngpaste (user-installed) covers TIFF-only clipboards. pi's
//     detectSupportedImageMimeType probe is unnecessary on that path — both
//     tools yield image/png verbatim (declared). Windows still reports
//     nothing (declared).
package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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

// clipboardRunCommand spawns a command with a timeout — the read-path
// wrapper over runClipboardCommand (pi: runClipboardCommand with no input;
// ok=false = pi undefined). Kept with the legacy signature for the
// clipboardRunCommandFn test seam.
func clipboardRunCommand(command string, args []string, timeoutMs int64) ([]byte, bool) {
	return runClipboardCommand(command, args, &clipboardCommandOptions{TimeoutMs: timeoutMs})
}

// clipboardRunCommandFn indirections clipboardRunCommand (pig-only test seam:
// tests stub it so the darwin paths never spawn real clipboard processes).
var clipboardRunCommandFn = clipboardRunCommand

// clipboardImageResult is pi's `ClipboardImage | null | undefined` (pig:
// three-state protocol).
type clipboardImageResult struct {
	// image is non-nil when an image was found (pi ClipboardImage).
	image *ClipboardImage
	// none=true means the backend answered and has no image (pi null);
	// none=false with a nil image means the backend is unavailable (pi
	// undefined) and the chain should fall through.
	none bool
}

// clipboardImageUnavailable is pi's undefined.
var clipboardImageUnavailable = clipboardImageResult{}

// ReadClipboardImage's chain state (pi: the undefined | null | image local).
const (
	clipboardStateUndefined = iota
	clipboardStateNull
	clipboardStateImage
)

// clipboardImageNone returns pi's null.
func clipboardImageNone() clipboardImageResult { return clipboardImageResult{none: true} }

// clipboardImageFound wraps a discovered image (pi: the image object).
func clipboardImageFound(image *ClipboardImage) clipboardImageResult {
	return clipboardImageResult{image: image}
}

// readClipboardImageViaWlPaste reads via wl-paste type negotiation (pi:
// readClipboardImageViaWlPaste). Unavailable → undefined (fall through);
// answered-but-no-image → null (stop the chain).
func readClipboardImageViaWlPaste() clipboardImageResult {
	list, ok := clipboardRunCommandFn("wl-paste", []string{"--list-types"}, clipboardDefaultListTimeoutMs)
	if !ok {
		return clipboardImageUnavailable
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
		return clipboardImageNone()
	}

	data, ok := clipboardRunCommandFn("wl-paste", []string{"--type", selectedType, "--no-newline"}, 0)
	if !ok {
		return clipboardImageUnavailable
	}
	if len(data) == 0 {
		return clipboardImageNone()
	}

	return clipboardImageFound(&ClipboardImage{Bytes: data, MimeType: clipboardBaseMimeType(selectedType)})
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
// readClipboardImageViaPowerShell). Any failure is null (pi: the WSL path
// never reports "unavailable" — it terminates the chain).
func readClipboardImageViaPowerShell() clipboardImageResult {
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("pi-wsl-clip-%x-%x-%x-%x.png",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10]))

	winPath, ok := clipboardRunCommandFn("wslpath", []string{"-w", tmpFile}, clipboardDefaultListTimeoutMs)
	if !ok {
		return clipboardImageNone()
	}

	winPathStr := strings.TrimSpace(string(winPath))
	if winPathStr == "" {
		return clipboardImageNone()
	}

	psQuotedWinPath := strings.ReplaceAll(winPathStr, "'", "''")
	psScript := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"Add-Type -AssemblyName System.Drawing",
		fmt.Sprintf("$path = '%s'", psQuotedWinPath),
		"$img = [System.Windows.Forms.Clipboard]::GetImage()",
		"if ($img) { $img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png); Write-Output 'ok' } else { Write-Output 'empty' }",
	}, "; ")

	result, ok := clipboardRunCommandFn("powershell.exe", []string{"-NoProfile", "-Command", psScript},
		clipboardDefaultPowershellTimeoutMs)
	if !ok {
		return clipboardImageNone()
	}

	output := strings.TrimSpace(string(result))
	if output != "ok" {
		return clipboardImageNone()
	}

	bytes, err := os.ReadFile(tmpFile)
	if err != nil || len(bytes) == 0 {
		_ = os.Remove(tmpFile)
		return clipboardImageNone()
	}
	_ = os.Remove(tmpFile)
	return clipboardImageFound(&ClipboardImage{Bytes: bytes, MimeType: "image/png"})
}

// readClipboardImageViaXclip enumerates TARGETS and reads the best type (pi:
// readClipboardImageViaXclip). Unavailable → undefined (fall through);
// answered-but-no-image-type → null (stop the chain).
func readClipboardImageViaXclip() clipboardImageResult {
	targets, ok := clipboardRunCommandFn("xclip",
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

	preferred := selectPreferredImageMimeType(candidateTypes)
	if ok && preferred == "" {
		return clipboardImageNone()
	}
	// pi: tryTypes is a Set — the preferred type dedupes against the
	// supported list.
	var tryTypes []string
	if preferred != "" {
		tryTypes = append(tryTypes, preferred)
		for _, t := range supportedImageMimeTypes {
			if t != preferred {
				tryTypes = append(tryTypes, t)
			}
		}
	} else {
		tryTypes = append(tryTypes, supportedImageMimeTypes...)
	}

	for _, mimeType := range tryTypes {
		data, ok := clipboardRunCommandFn("xclip", []string{"-selection", "clipboard", "-t", mimeType, "-o"}, 0)
		if ok && len(data) > 0 {
			return clipboardImageFound(&ClipboardImage{Bytes: data, MimeType: clipboardBaseMimeType(mimeType)})
		}
	}

	return clipboardImageUnavailable
}

// readClipboardImageViaOsascript reads the PNG pasteboard flavor via osascript
// (pig-only; darwin transport for pi's native-addon getImageBinary, which
// returns image/png as well). ok=false when the clipboard carries no PNG
// flavor (osascript exits non-zero) or the output does not decode.
func readClipboardImageViaOsascript() clipboardImageResult {
	out, ok := clipboardRunCommandFn("osascript",
		[]string{"-e", `the clipboard as «class PNGf»`}, clipboardDefaultOsascriptTimeoutMs)
	if !ok {
		return clipboardImageNone()
	}
	bytes, ok := decodeOsascriptPngData(out)
	if !ok || len(bytes) == 0 {
		return clipboardImageNone()
	}
	return clipboardImageFound(&ClipboardImage{Bytes: bytes, MimeType: "image/png"})
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
func readClipboardImageViaPngpaste() clipboardImageResult {
	data, ok := clipboardRunCommandFn("pngpaste", []string{"-"}, 0)
	if !ok || len(data) == 0 {
		return clipboardImageNone()
	}
	return clipboardImageFound(&ClipboardImage{Bytes: data, MimeType: "image/png"})
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
// readClipboardImage; v0.86.0 three-state chain); nil = nothing available.
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

	// pi's `let image: ClipboardImage | null | undefined` becomes a state
	// variable: undefined = chain may fall through, null = chain stops.
	state := clipboardStateUndefined
	var image *ClipboardImage

	assign := func(result clipboardImageResult) {
		switch {
		case result.image != nil:
			state = clipboardStateImage
			image = result.image
		case result.none:
			state = clipboardStateNull
		default:
			state = clipboardStateUndefined
		}
	}

	// native is pig's stand-in for pi's getNativeClipboard() path: the
	// darwin external-tool chain (declared; see file header). Windows
	// reports nothing.
	native := func() clipboardImageResult {
		if platform != "darwin" {
			return clipboardImageUnavailable
		}
		if result := readClipboardImageViaOsascript(); result.image != nil {
			return result
		}
		return readClipboardImageViaPngpaste()
	}

	if platform == "linux" {
		wsl := clipboardIsWSL(env)
		if isWaylandSessionEnv(env) || wsl {
			assign(readClipboardImageViaWlPaste())
		}
		if state == clipboardStateUndefined {
			assign(readClipboardImageViaXclip())
		}
		// pi: if (!image && wsl) image = (await powershell()) ?? image — a
		// null result preserves the current null/undefined state.
		if state != clipboardStateImage && wsl {
			if result := readClipboardImageViaPowerShell(); result.image != nil {
				assign(result)
			}
		}
		if state == clipboardStateUndefined {
			assign(native())
		}
	} else {
		assign(native())
	}

	if image == nil {
		return nil
	}

	// Convert unsupported formats (e.g., Windows DIB data wrapped as BMP) to
	// PNG. The photon pipeline lands with Phase 8 (declared).
	if !isSupportedImageMimeType(image.MimeType) {
		return nil
	}

	return image
}
