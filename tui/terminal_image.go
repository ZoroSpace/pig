// tui/terminal_image.go (pi: packages/tui/src/terminal-image.ts) — minimal
// subset consumed by the T1 core (tui.ts / tui-main-screen.ts) plus the
// Kitty metadata/registration/placement/crop half consumed by the alt-screen
// layout machinery (T4): Kitty image line detection, image deletion escapes,
// Kitty image encoding, cell dimensions, capability cache, and the
// registered-metadata placement/crop helpers.
//
// Porting scope (declared, README §4 Phase 8):
//   - The image half of the terminal detection machinery (OSC queries, tmux
//     probing, iTerm2 protocol, Image component, sixel/iterm rendering) is
//     deferred to Phase 8 with the image pipeline. GetCapabilities returns a
//     non-image result until then; SetCapabilities keeps the test seam. The
//     metadata/registry functions ported here are pure string/escape helpers
//     with no detection dependency.
//   - The `trueColor` branch of pi's detectCapabilitiesFromEnvironment *is*
//     ported (2026-10-01 fix): the theme picked its color mode from it, so
//     leaving it at false silently quantized every theme color to the
//     256-color palette. The `hyperlinks` branch is ported for the terminals
//     S16-3 brought (Herdr, pi b2363841a); its remaining branches (the tmux
//     client_termfeatures probe and the kitty/ghostty/... families) stay
//     deferred together with the images half and the Phase 8 image pipeline.
package tui

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/samber/lo"
)

const kittyImagePrefix = "\x1b_G"
const iterm2ImagePrefix = "\x1b]1337;"

// TerminalCapabilities describes detected terminal features.
type TerminalCapabilities struct {
	Images     any // pi: boolean | "kitty" | "iterm2"; nil until detected (Herdr → "kitty"; other families deferred to Phase 8)
	TrueColor  bool
	Hyperlinks bool
}

var (
	capabilitiesMu        sync.Mutex
	cachedCapabilities    *TerminalCapabilities
	capabilityOverrides   TerminalCapabilityOverrides
	cellDimensionsMu      sync.Mutex
	currentCellDimensions *CellDimensions
)

// TerminalCapabilityOverrides carries user-forced capability overrides (pi:
// Partial<TerminalCapabilities> in setCapabilityOverrides; nil fields leave
// auto-detection in force). Images mirrors pi's boolean | "kitty" |
// "iterm2" | null union flattened for Go: nil = no override, false = force
// off (pi images:null), true/"kitty"/"iterm2" = force on.
type TerminalCapabilityOverrides struct {
	Images     any
	TrueColor  *bool
	Hyperlinks *bool
}

// CellDimensions is the terminal cell size in pixels.
type CellDimensions struct {
	WidthPx  int
	HeightPx int
}

// GetCellDimensions returns the last reported cell size (nil = unknown).
func GetCellDimensions() *CellDimensions {
	cellDimensionsMu.Lock()
	defer cellDimensionsMu.Unlock()
	return currentCellDimensions
}

// SetCellDimensions records the terminal cell size in pixels.
func SetCellDimensions(dims CellDimensions) {
	cellDimensionsMu.Lock()
	defer cellDimensionsMu.Unlock()
	d := dims
	currentCellDimensions = &d
}

// GetCapabilities returns the cached terminal capabilities. TrueColor is
// auto-detected from the environment (detectTrueColorCapability) with the
// PI_TRUE_COLOR override on top, and Hyperlinks from
// detectHyperlinksCapability with PI_HYPERLINKS; what a caller forces via
// SetCapabilityOverrides (user settings' `terminal.*`) wins over both
// (pi: getCapabilities spreads capabilityOverrides over
// detectCapabilities). Images detection is currently limited to the Herdr
// branch (detectImagesCapability); the other image families remain deferred
// to Phase 8, so Images stays nil there unless overridden.
func GetCapabilities() TerminalCapabilities {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	if cachedCapabilities == nil {
		caps := TerminalCapabilities{
			Images:     detectImagesCapability(),
			TrueColor:  detectTrueColorCapability(),
			Hyperlinks: detectHyperlinksCapability(),
		}
		if override := parseBooleanCapabilityOverride(os.Getenv("PI_TRUE_COLOR")); override != nil {
			caps.TrueColor = *override
		}
		if override := parseBooleanCapabilityOverride(os.Getenv("PI_HYPERLINKS")); override != nil {
			caps.Hyperlinks = *override
		}
		applyCapabilityOverrides(&caps, capabilityOverrides)
		cachedCapabilities = &caps
	}
	return *cachedCapabilities
}

// parseBooleanCapabilityOverride parses a boolean capability environment
// override (pi: parseBooleanCapabilityOverride). Only "1"/"0" force a value;
// anything else — including "auto" and unset — keeps auto-detection.
func parseBooleanCapabilityOverride(value string) *bool {
	switch value {
	case "1":
		return lo.ToPtr(true)
	case "0":
		return lo.ToPtr(false)
	default:
		return nil
	}
}

// detectImagesCapability reports the auto-detected image protocol. Herdr
// supports the Kitty graphics protocol and is checked before the
// outer-terminal detection because variables like KITTY_WINDOW_ID can leak
// into its panes (pi 78a3c10ba #10774). The remaining image families stay
// deferred with the Phase 8 image pipeline, so this reports nil (pi
// images:null) otherwise.
func detectImagesCapability() any {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	if termProgram == "herdr" {
		return "kitty"
	}
	return nil
}

// detectTrueColorCapability reports whether the terminal renders 24-bit
// color. This is the trueColor branch of pi's
// detectCapabilitiesFromEnvironment; the images/hyperlinks branches of that
// function are still deferred (see GetCapabilities).
func detectTrueColorCapability() bool {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(os.Getenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(os.Getenv("TERM"))
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit" || strings.HasSuffix(term, "-direct")
	isWindowsConsole := runtime.GOOS == "windows"

	// Multiplexers do not report the outer terminal, so an explicit
	// $COLORTERM/$TERM hint decides (pi: tmux branch; images stay off under
	// tmux, which is part of the deferred image half).
	if os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux") {
		return hasTrueColorHint
	}

	// screen does not forward OSC 8 hyperlinks, so keep them off there.
	if strings.HasPrefix(term, "screen") {
		return hasTrueColorHint
	}

	// Herdr forwards OSC 8 hyperlinks, but its trueColor follows the explicit
	// hint only: it runs inside another terminal whose variables, such as
	// KITTY_WINDOW_ID, may leak into its panes, so check it before the
	// outer-terminal detection (pi b2363841a #10573).
	if termProgram == "herdr" {
		return hasTrueColorHint
	}

	// Rex supports OSC 8 hyperlinks but only partially implements the Kitty
	// graphics protocol (no Unicode placeholders), so leave images off (pi
	// 0ed1b0431 #11131). Check it before outer-terminal detection.
	if termProgram == "rex" {
		return true
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty" {
		return true
	}

	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return true
	}

	if os.Getenv("WEZTERM_PANE") != "" || termProgram == "wezterm" {
		return true
	}

	// Warp supports the Kitty graphics protocol and OSC 8 hyperlinks.
	if termProgram == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return true
	}

	if os.Getenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app" {
		return true
	}

	if os.Getenv("WT_SESSION") != "" {
		return true
	}

	if termProgram == "alacritty" || termProgram == "vscode" || termProgram == "zed" {
		return true
	}

	if terminalEmulator == "jetbrains-jediterm" {
		return true
	}

	// Windows Terminal does not always set WT_SESSION, for example when it
	// hosts a cmd.exe launched directly from Win+R. Modern Windows consoles
	// support truecolor; keep hyperlinks off unless we positively detected
	// support above.
	if isWindowsConsole {
		return true
	}

	// Unknown terminal: be conservative and trust only an explicit hint (pi:
	// hasTrueColorHint).
	return hasTrueColorHint
}

// detectHyperlinksCapability reports whether the terminal renders OSC 8
// hyperlinks. This is the hyperlinks branch of pi's
// detectCapabilitiesFromEnvironment, landed incrementally: S16-3 brought the
// Herdr branch (pi b2363841a #10573); the remaining branches stay deferred
// with the Phase 8 image pipeline (the tmux branch needs the
// client_termfeatures probe, and flipping hyperlinks on for the
// kitty/ghostty/... families is a user-visible switch pig has so far kept
// off).
func detectHyperlinksCapability() bool {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))

	// pi: tmux branch (term prefix + TMUX env) — hyperlinks only when `tmux
	// display-message -p '#{client_termfeatures}'` lists `hyperlinks`.
	// Deferred with Phase 8. pi: screen branch — screen does not forward
	// OSC 8 hyperlinks; false. Deferred with Phase 8.

	// Herdr forwards OSC 8 hyperlinks. It runs inside another terminal whose
	// variables, such as KITTY_WINDOW_ID, may leak into its panes, so check
	// it first and leave image protocols off (pi b2363841a #10573).
	if termProgram == "herdr" {
		return true
	}

	// Rex supports OSC 8 hyperlinks; check it before outer-terminal detection
	// so leaked variables like KITTY_WINDOW_ID do not change the outcome (pi
	// 0ed1b0431 #11131).
	if termProgram == "rex" {
		return true
	}

	// pi: kitty / ghostty / wezterm / warp / iterm.app / WT_SESSION /
	// alacritty / vscode / zed branches → hyperlinks true;
	// jetbrains-jediterm / windows console / unknown → false. Deferred with
	// Phase 8.
	return false
}

// applyCapabilityOverrides spreads non-empty overrides over caps (pig helper
// for pi's `{...detected, ...capabilityOverrides}` spread; typed fields need
// explicit per-field logic).
func applyCapabilityOverrides(caps *TerminalCapabilities, overrides TerminalCapabilityOverrides) {
	if overrides.Images != nil {
		caps.Images = overrides.Images
	}
	if overrides.TrueColor != nil {
		caps.TrueColor = *overrides.TrueColor
	}
	if overrides.Hyperlinks != nil {
		caps.Hyperlinks = *overrides.Hyperlinks
	}
}

// ResetCapabilitiesCache drops the cached capabilities.
func ResetCapabilitiesCache() {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	cachedCapabilities = nil
}

// SetCapabilities overrides the cached capabilities (test seam, pi: same).
func SetCapabilities(caps TerminalCapabilities) {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	c := caps
	cachedCapabilities = &c
}

// SetCapabilityOverrides overrides selected auto-detected capabilities (pi:
// setCapabilityOverrides). Takes effect on the next capability cache fill.
func SetCapabilityOverrides(overrides TerminalCapabilityOverrides) {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	if capabilityOverridesEqual(capabilityOverrides, overrides) {
		return
	}
	capabilityOverrides = overrides
	cachedCapabilities = nil
}

// capabilityOverridesEqual reports shallow equality (pi: field comparison
// early-return in setCapabilityOverrides).
func capabilityOverridesEqual(a, b TerminalCapabilityOverrides) bool {
	if a.Images != b.Images {
		return false
	}
	if (a.TrueColor == nil) != (b.TrueColor == nil) {
		return false
	}
	if a.TrueColor != nil && *a.TrueColor != *b.TrueColor {
		return false
	}
	if (a.Hyperlinks == nil) != (b.Hyperlinks == nil) {
		return false
	}
	if a.Hyperlinks != nil && *a.Hyperlinks != *b.Hyperlinks {
		return false
	}
	return true
}

// Hyperlink wraps text in an OSC 8 hyperlink sequence (pi:
// terminal-image.ts hyperlink — ported early because the Markdown component
// consumes it).
func Hyperlink(text string, url string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// IsImageLine reports whether the line contains a Kitty or iTerm2 image
// placement sequence.
func IsImageLine(line string) bool {
	// Fast path: sequence at line start (single-row images)
	if strings.HasPrefix(line, kittyImagePrefix) || strings.HasPrefix(line, iterm2ImagePrefix) {
		return true
	}
	// Slow path: sequence elsewhere (multi-row images have cursor-up prefix)
	return strings.Contains(line, kittyImagePrefix) || strings.Contains(line, iterm2ImagePrefix)
}

// AllocateImageId generates a random image ID in range [1, 0xffffffff] to
// avoid collisions between different module instances.
func AllocateImageId() int {
	return rand.Intn(0xfffffffe) + 1
}

// EncodeKittyParams are the optional parameters for EncodeKitty.
type EncodeKittyParams struct {
	Columns int
	Rows    int
	ImageId int
	// MoveCursorOff mirrors pi's moveCursor: false (Kitty applies its default
	// cursor movement after placement unless C=1 is sent).
	MoveCursorOff bool
}

// EncodeKitty encodes base64 image data as a Kitty graphics placement escape.
func EncodeKitty(base64Data string, options EncodeKittyParams) string {
	const chunkSize = 4096

	params := []string{"a=T", "f=100", "q=2"}
	if options.MoveCursorOff {
		params = append(params, "C=1")
	}
	if options.Columns != 0 {
		params = append(params, "c="+itoa(options.Columns))
	}
	if options.Rows != 0 {
		params = append(params, "r="+itoa(options.Rows))
	}
	if options.ImageId != 0 {
		params = append(params, "i="+itoa(options.ImageId))
	}

	if len(base64Data) <= chunkSize {
		return "\x1b_G" + strings.Join(params, ",") + ";" + base64Data + "\x1b\\"
	}

	var chunks []string
	offset := 0
	isFirst := true
	for offset < len(base64Data) {
		end := offset + chunkSize
		if end > len(base64Data) {
			end = len(base64Data)
		}
		chunk := base64Data[offset:end]
		isLast := offset+chunkSize >= len(base64Data)

		if isFirst {
			chunks = append(chunks, "\x1b_G"+strings.Join(params, ",")+",m=1;"+chunk+"\x1b\\")
			isFirst = false
		} else if isLast {
			chunks = append(chunks, "\x1b_Gm=0;"+chunk+"\x1b\\")
		} else {
			chunks = append(chunks, "\x1b_Gm=1;"+chunk+"\x1b\\")
		}

		offset += chunkSize
	}

	return strings.Join(chunks, "")
}

// DeleteKittyImage generates an escape deleting a Kitty graphics image by ID.
// Uses uppercase 'I' to also free the image data.
func DeleteKittyImage(imageId int) string {
	return "\x1b_Ga=d,d=I,i=" + itoa(imageId) + ",q=2\x1b\\"
}

// DeleteAllKittyImages deletes all visible Kitty graphics images (uppercase
// 'A' also frees the image data).
func DeleteAllKittyImages() string {
	return "\x1b_Ga=d,d=A,q=2\x1b\\"
}

// DeleteAllKittyPlacements deletes all visible Kitty placements while
// retaining their uploaded image data.
func DeleteAllKittyPlacements() string {
	return "\x1b_Ga=d,d=a,q=2\x1b\\"
}

// =============================================================================
// Kitty image metadata registry (pi: kittyImageMetadata map + helpers)
// =============================================================================

// KittyImageMetadata describes a registered Kitty image's cell footprint
// (pi: KittyImageMetadata).
type KittyImageMetadata struct {
	Columns  int
	Rows     int
	ImageId  int
	WidthPx  int
	HeightPx int
}

type registeredKittyImageMetadata struct {
	KittyImageMetadata
	transmissionGeneration int
}

// KittyImagePlacement describes a placement-only retransmission for an image
// line emitted by the image renderer (pi: KittyImagePlacement).
type KittyImagePlacement struct {
	ImageId                int
	TransmissionGeneration int
	TransmissionBytes      int
	EstimatedDecodedBytes  int
	// Rows is the number of terminal rows the placement covers (explicit
	// `r=` control when present, else the registered metadata rows).
	Rows            int
	Sequence        string
	ReplacementLine string
}

var (
	kittyImageMetadataMu        sync.Mutex
	kittyImageMetadataRegistry  = map[int]*registeredKittyImageMetadata{}
	kittyImageMetadataOrder     []int // insertion order for FIFO eviction (JS Map iteration)
	kittyTransmissionGeneration int
)

// RegisterKittyImageMetadata records the metadata of a transmitted image
// (pi: registerKittyImageMetadata). Bumps the transmission generation and
// keeps at most 1000 entries (oldest inserted evicted first).
func RegisterKittyImageMetadata(metadata KittyImageMetadata) {
	kittyImageMetadataMu.Lock()
	defer kittyImageMetadataMu.Unlock()
	kittyTransmissionGeneration++
	if _, ok := kittyImageMetadataRegistry[metadata.ImageId]; !ok {
		kittyImageMetadataOrder = append(kittyImageMetadataOrder, metadata.ImageId)
	}
	kittyImageMetadataRegistry[metadata.ImageId] = &registeredKittyImageMetadata{
		KittyImageMetadata:     metadata,
		transmissionGeneration: kittyTransmissionGeneration,
	}
	if len(kittyImageMetadataRegistry) > 1000 {
		oldest := kittyImageMetadataOrder[0]
		kittyImageMetadataOrder = kittyImageMetadataOrder[1:]
		delete(kittyImageMetadataRegistry, oldest)
	}
}

var (
	kittyControlsRegex = regexp.MustCompile(`\x1b_G([^;]*);`)
	kittyImageIdRegex  = regexp.MustCompile(`(?:^|,)i=(\d+)(?:,|$)`)
	kittyRowsRegex     = regexp.MustCompile(`(?:^|,)r=(\d+)(?:,|$)`)
	kittyChunkedRegex  = regexp.MustCompile(`(?:^|,)m=1(?:,|$)`)
	kittyCropControlRe = regexp.MustCompile(`^[yhr]=`)
)

func getRegisteredKittyImageMetadataFromControls(controls string) *registeredKittyImageMetadata {
	idMatch := kittyImageIdRegex.FindStringSubmatch(controls)
	if idMatch == nil {
		return nil
	}
	imageId, err := strconv.Atoi(idMatch[1])
	if err != nil {
		return nil
	}
	kittyImageMetadataMu.Lock()
	defer kittyImageMetadataMu.Unlock()
	return kittyImageMetadataRegistry[imageId]
}

func getRegisteredKittyImageMetadata(line string) *registeredKittyImageMetadata {
	m := kittyControlsRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return getRegisteredKittyImageMetadataFromControls(m[1])
}

// getExplicitKittyImageRows reads the placement's `r=` control (pi:
// getExplicitKittyImageRows); 0 = not set or not positive.
func getExplicitKittyImageRows(controls string) int {
	m := kittyRowsRegex.FindStringSubmatch(controls)
	if m == nil {
		return 0
	}
	rows, err := strconv.Atoi(m[1])
	if err != nil || rows <= 0 {
		return 0
	}
	return rows
}

// getKittyImageRowsFromControls prefers the explicit `r=` control over the
// registered metadata rows (pi: getKittyImageRowsFromControls).
func getKittyImageRowsFromControls(controls string, fallbackRows int) int {
	if rows := getExplicitKittyImageRows(controls); rows != 0 {
		return rows
	}
	return fallbackRows
}

// GetKittyImageMetadata returns the registered metadata referenced by a
// Kitty image line (pi: getKittyImageMetadata).
func GetKittyImageMetadata(line string) *KittyImageMetadata {
	metadata := getRegisteredKittyImageMetadata(line)
	if metadata == nil {
		return nil
	}
	m := metadata.KittyImageMetadata
	return &m
}

var kittyPlacementControlKeys = map[string]bool{
	"i": true, "p": true, "x": true, "y": true, "w": true, "h": true,
	"X": true, "Y": true, "c": true, "r": true, "C": true, "U": true,
	"z": true, "P": true, "Q": true, "H": true, "V": true,
}

// GetKittyImagePlacementRows reads the number of rows covered by an image
// placement without scanning its payload (pi: getKittyImagePlacementRows,
// #10319): the explicit `r=` control when present, else the registered
// metadata rows; 0 = the line places no known image.
func GetKittyImagePlacementRows(line string) int {
	m := kittyControlsRegex.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	if rows := getExplicitKittyImageRows(m[1]); rows != 0 {
		return rows
	}
	metadata := getRegisteredKittyImageMetadataFromControls(m[1])
	if metadata == nil {
		return 0
	}
	return metadata.Rows
}

// GetKittyImagePlacement builds a placement-only command for an image line
// emitted by the image renderer (pi: getKittyImagePlacement).
func GetKittyImagePlacement(line string) *KittyImagePlacement {
	match := kittyControlsRegex.FindStringSubmatchIndex(line)
	if match == nil {
		return nil
	}
	metadata := getRegisteredKittyImageMetadata(line)
	if metadata == nil {
		return nil
	}

	commandStart := match[0]
	commandControls := line[match[2]:match[3]]
	var transmissionEnd int
	for {
		idx := strings.Index(line[commandStart+len(kittyImagePrefix):], "\x1b\\")
		if idx == -1 {
			return nil
		}
		transmissionEnd = commandStart + len(kittyImagePrefix) + idx + 2
		if !kittyChunkedRegex.MatchString(commandControls) {
			break
		}
		commandStart = transmissionEnd
		if !strings.HasPrefix(line[commandStart:], kittyImagePrefix) {
			return nil
		}
		controlsEnd := strings.Index(line[commandStart+len(kittyImagePrefix):], ";")
		if controlsEnd == -1 {
			return nil
		}
		commandControls = line[commandStart+len(kittyImagePrefix) : commandStart+len(kittyImagePrefix)+controlsEnd]
	}

	controls := line[match[2]:match[3]]
	var kept []string
	for _, control := range strings.Split(controls, ",") {
		// JS: control.split("=", 1)[0] — the pre-"=" part, or the whole string
		// when there is no "=".
		key := strings.SplitN(control, "=", 2)[0]
		if kittyPlacementControlKeys[key] {
			kept = append(kept, control)
		}
	}
	sequence := "\x1b_Ga=p,q=2," + strings.Join(kept, ",") + "\x1b\\"
	return &KittyImagePlacement{
		ImageId:                metadata.ImageId,
		TransmissionGeneration: metadata.transmissionGeneration,
		TransmissionBytes:      transmissionEnd - match[0],
		EstimatedDecodedBytes:  metadata.WidthPx * metadata.HeightPx * 4,
		Rows:                   getKittyImageRowsFromControls(controls, metadata.Rows),
		Sequence:               sequence,
		ReplacementLine:        line[:match[0]] + sequence + line[transmissionEnd:],
	}
}

// CropKittyImageLine rewrites a Kitty image line to show only a window of
// rows (pi: cropKittyImageLine).
func CropKittyImageLine(line string, hiddenRows int, visibleRows int) string {
	metadata := GetKittyImageMetadata(line)
	match := kittyControlsRegex.FindStringSubmatchIndex(line)
	if metadata == nil || match == nil || hiddenRows < 0 || hiddenRows >= metadata.Rows || visibleRows <= 0 {
		return line
	}
	croppedRows := minInt(visibleRows, metadata.Rows-hiddenRows)
	if hiddenRows == 0 && croppedRows == metadata.Rows {
		return line
	}
	sourceY := int(math.Floor(float64(metadata.HeightPx*hiddenRows) / float64(metadata.Rows)))
	sourceEnd := int(math.Ceil(float64(metadata.HeightPx*(hiddenRows+croppedRows)) / float64(metadata.Rows)))
	sourceHeight := maxInt(1, minInt(metadata.HeightPx, sourceEnd)-sourceY)
	controls := strings.Split(line[match[2]:match[3]], ",")
	var kept []string
	for _, control := range controls {
		if !kittyCropControlRe.MatchString(control) {
			kept = append(kept, control)
		}
	}
	kept = append(kept, "y="+itoa(sourceY), "h="+itoa(sourceHeight), "r="+itoa(croppedRows))
	return line[:match[0]] + "\x1b_G" + strings.Join(kept, ",") + ";" + line[match[1]:]
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ImageDimensions describes an image's pixel size (pi: ImageDimensions).
type ImageDimensions struct {
	WidthPx  int
	HeightPx int
}

// GetPngDimensions parses the IHDR size from a base64 PNG (pi:
// getPngDimensions).
func GetPngDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil
	}
	if len(buf) < 24 {
		return nil
	}
	if buf[0] != 0x89 || buf[1] != 0x50 || buf[2] != 0x4e || buf[3] != 0x47 {
		return nil
	}
	width := int(binary.BigEndian.Uint32(buf[16:20]))
	height := int(binary.BigEndian.Uint32(buf[20:24]))
	return &ImageDimensions{WidthPx: width, HeightPx: height}
}

// GetJpegDimensions scans SOF0-SOF2 markers for the size (pi:
// getJpegDimensions).
func GetJpegDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil
	}
	if len(buf) < 2 {
		return nil
	}
	if buf[0] != 0xff || buf[1] != 0xd8 {
		return nil
	}
	offset := 2
	for offset < len(buf)-9 {
		if buf[offset] != 0xff {
			offset++
			continue
		}
		marker := buf[offset+1]
		if marker >= 0xc0 && marker <= 0xc2 {
			height := int(binary.BigEndian.Uint16(buf[offset+5 : offset+7]))
			width := int(binary.BigEndian.Uint16(buf[offset+7 : offset+9]))
			return &ImageDimensions{WidthPx: width, HeightPx: height}
		}
		if offset+3 >= len(buf) {
			return nil
		}
		length := int(binary.BigEndian.Uint16(buf[offset+2 : offset+4]))
		if length < 2 {
			return nil
		}
		offset += 2 + length
	}
	return nil
}

// GetGifDimensions reads the LE size from the GIF header (pi:
// getGifDimensions).
func GetGifDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil
	}
	if len(buf) < 10 {
		return nil
	}
	sig := string(buf[0:6])
	if sig != "GIF87a" && sig != "GIF89a" {
		return nil
	}
	width := int(binary.LittleEndian.Uint16(buf[6:8]))
	height := int(binary.LittleEndian.Uint16(buf[8:10]))
	return &ImageDimensions{WidthPx: width, HeightPx: height}
}

// GetWebpDimensions reads VP8/VP8L/VP8X chunk sizes (pi:
// getWebpDimensions).
func GetWebpDimensions(base64Data string) *ImageDimensions {
	buf, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil
	}
	if len(buf) < 30 {
		return nil
	}
	riff := string(buf[0:4])
	webp := string(buf[8:12])
	if riff != "RIFF" || webp != "WEBP" {
		return nil
	}
	chunk := string(buf[12:16])
	switch chunk {
	case "VP8 ":
		if len(buf) < 30 {
			return nil
		}
		width := int(binary.LittleEndian.Uint16(buf[26:28])) & 0x3fff
		height := int(binary.LittleEndian.Uint16(buf[28:30])) & 0x3fff
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	case "VP8L":
		if len(buf) < 25 {
			return nil
		}
		bits := binary.LittleEndian.Uint32(buf[21:25])
		width := int(bits&0x3fff) + 1
		height := int((bits>>14)&0x3fff) + 1
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	case "VP8X":
		if len(buf) < 30 {
			return nil
		}
		width := int(buf[24]) | int(buf[25])<<8 | int(buf[26])<<16
		height := int(buf[27]) | int(buf[28])<<8 | int(buf[29])<<16
		return &ImageDimensions{WidthPx: width + 1, HeightPx: height + 1}
	}
	return nil
}

// GetImageDimensions dispatches on the mime type (pi: getImageDimensions).
func GetImageDimensions(base64Data string, mimeType string) *ImageDimensions {
	switch mimeType {
	case "image/png":
		return GetPngDimensions(base64Data)
	case "image/jpeg":
		return GetJpegDimensions(base64Data)
	case "image/gif":
		return GetGifDimensions(base64Data)
	case "image/webp":
		return GetWebpDimensions(base64Data)
	}
	return nil
}

// shortenImagePath shortens home-prefixed absolute paths to ~/... (pi:
// shortenImagePath).
func shortenImagePath(filename string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filename
	}
	if filename == home || strings.HasPrefix(filename, home+"/") || strings.HasPrefix(filename, home+"\\") {
		return "~" + filename[len(home):]
	}
	return filename
}

// ImageFallback is the text fallback when the terminal cannot render
// inline images (pi: imageFallback). dimensions nil = unknown; filename ""
// = omitted.
func ImageFallback(mimeType string, dimensions *ImageDimensions, filename string) string {
	var parts []string
	if filename != "" {
		display := shortenImagePath(filename)
		if GetCapabilities().Hyperlinks && filepath.IsAbs(filename) {
			parts = append(parts, hyperlinkPath(display, filename))
		} else {
			parts = append(parts, display)
		}
	}
	parts = append(parts, "["+mimeType+"]")
	if dimensions != nil {
		parts = append(parts, fmt.Sprintf("%dx%d", dimensions.WidthPx, dimensions.HeightPx))
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}

// hyperlinkPath wraps a display path in an OSC 8 file:// link (pi:
// hyperlink(display, pathToFileURL(filename).href)).
func hyperlinkPath(display string, filename string) string {
	u := url.URL{Scheme: "file", Path: filename}
	return Hyperlink(display, u.String())
}
