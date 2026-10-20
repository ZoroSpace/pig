// tui/terminal_image_test.go (pi: packages/tui/test/terminal-image.test.ts)
//
// Ported here: the `detectCapabilities` trueColor cases plus the
// PI_TRUE_COLOR/programmatic override cases (pi's "applies environment
// overrides" and "applies and clears programmatic overrides"). The
// images/hyperlinks halves of that suite stay with the Phase 8 image
// pipeline, matching the porting scope declared in terminal_image.go.
//
// Regression under test (2026-10-01, docs/fix/2026-10-01-truecolor-detection-missing.md):
// GetCapabilities never detected truecolor, so the theme quantized every
// color to the 256-color palette.
package tui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/samber/lo"
)

// capabilityEnvKeys are every environment key terminal detection reads
// (pi: ENV_KEYS).
var capabilityEnvKeys = []string{
	"TERM",
	"TERM_PROGRAM",
	"TERMINAL_EMULATOR",
	"COLORTERM",
	"TMUX",
	"KITTY_WINDOW_ID",
	"GHOSTTY_RESOURCES_DIR",
	"WEZTERM_PANE",
	"ITERM_SESSION_ID",
	"WT_SESSION",
	"WARP_SESSION_ID",
	"WARP_TERMINAL_SESSION_UUID",
	"PI_TRUE_COLOR",
	"PI_HYPERLINKS",
}

// withCapabilityEnv clears the detection environment, applies overrides,
// resets the capability cache and restores everything afterwards (pi: withEnv
// + resetCapabilitiesCache/setCapabilityOverrides in terminal-image.test.ts).
func withCapabilityEnv(t *testing.T, env map[string]string, fn func()) {
	t.Helper()
	for _, key := range capabilityEnvKeys {
		t.Setenv(key, "")
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	SetCapabilityOverrides(TerminalCapabilityOverrides{})
	ResetCapabilitiesCache()
	t.Cleanup(func() {
		SetCapabilityOverrides(TerminalCapabilityOverrides{})
		ResetCapabilitiesCache()
	})
	fn()
}

func TestGetCapabilitiesDetectsTrueColor(t *testing.T) {
	if runtime.GOOS == "windows" {
		// pi's isWindowsConsole branch forces truecolor on Windows, which
		// makes the negative cases below platform specific.
		t.Skip("windows console detection forces truecolor")
	}
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"iTerm2 via TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "iTerm.app"}, true},
		{"iTerm2 via ITERM_SESSION_ID", map[string]string{"ITERM_SESSION_ID": "w0t0p0:1"}, true},
		{"Kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, true},
		{"Ghostty via TERM", map[string]string{"TERM": "xterm-ghostty"}, true},
		{"Ghostty via GHOSTTY_RESOURCES_DIR", map[string]string{"GHOSTTY_RESOURCES_DIR": "/ghostty"}, true},
		{"WezTerm", map[string]string{"WEZTERM_PANE": "0"}, true},
		{"Warp via TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "WarpTerminal"}, true},
		{"Warp via WARP_SESSION_ID", map[string]string{"WARP_SESSION_ID": "some-session-id"}, true},
		{"Warp via WARP_TERMINAL_SESSION_UUID", map[string]string{"WARP_TERMINAL_SESSION_UUID": "d0e1a2e5"}, true},
		{"Zed", map[string]string{"TERM_PROGRAM": "zed"}, true},
		{"Alacritty", map[string]string{"TERM_PROGRAM": "alacritty"}, true},
		{"Windows Terminal", map[string]string{"WT_SESSION": "session", "TERM": "xterm-256color"}, true},
		{"JetBrains JediTerm", map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "TERM": "xterm-256color"}, true},
		{"direct-color TERM", map[string]string{"TERM": "xterm-direct"}, true},
		{"COLORTERM hint", map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, true},
		{"COLORTERM 24bit hint", map[string]string{"TERM": "xterm-256color", "COLORTERM": "24bit"}, true},
		{"unknown terminal without hints", map[string]string{}, false},
		{"unknown 256-color terminal", map[string]string{"TERM": "xterm-256color"}, false},
		{
			"tmux does not inherit Windows Terminal truecolor",
			map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color", "WT_SESSION": "session"},
			false,
		},
		{
			"tmux trusts an explicit COLORTERM hint",
			map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color", "COLORTERM": "truecolor"},
			true,
		},
		{
			"tmux TERM prefix forces the same rule",
			map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "iTerm.app"},
			false,
		},
		{
			"screen does not inherit the outer terminal",
			map[string]string{"TERM": "screen-256color", "TERM_PROGRAM": "iTerm.app"},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withCapabilityEnv(t, tc.env, func() {
				if got := GetCapabilities().TrueColor; got != tc.want {
					t.Fatalf("TrueColor = %v, want %v", got, tc.want)
				}
			})
		})
	}
}

func TestGetCapabilitiesDetectsHerdrHyperlinks(t *testing.T) {
	// #10573 ports (pi b2363841a): Herdr forwards OSC 8 hyperlinks, and it is
	// checked before outer-terminal detection so leaked variables like
	// KITTY_WINDOW_ID do not enable images inside Herdr panes. #10774 (pi
	// 78a3c10ba) then flipped Herdr to the Kitty graphics protocol.
	t.Run("enables hyperlinks and Kitty images for Herdr", func(t *testing.T) {
		withCapabilityEnv(t, map[string]string{
			"TERM_PROGRAM": "herdr", "TERM": "xterm-256color",
			"COLORTERM": "truecolor", "KITTY_WINDOW_ID": "1",
		}, func() {
			caps := GetCapabilities()
			if !caps.Hyperlinks {
				t.Fatal("Hyperlinks = false, want true for Herdr")
			}
			if caps.Images != "kitty" {
				t.Fatalf("Images = %v, want \"kitty\" for Herdr", caps.Images)
			}
			if !caps.TrueColor {
				t.Fatal("TrueColor = false, want the COLORTERM hint")
			}
		})
	})

	t.Run("PI_HYPERLINKS forces both directions", func(t *testing.T) {
		withCapabilityEnv(t, map[string]string{"TERM_PROGRAM": "herdr", "PI_HYPERLINKS": "0"}, func() {
			if GetCapabilities().Hyperlinks {
				t.Fatal("PI_HYPERLINKS=0 must force hyperlinks off")
			}
		})
		withCapabilityEnv(t, map[string]string{"TERM": "xterm-256color", "PI_HYPERLINKS": "1"}, func() {
			if !GetCapabilities().Hyperlinks {
				t.Fatal("PI_HYPERLINKS=1 must force hyperlinks on")
			}
		})
	})

	t.Run("leaked kitty variables do not enable truecolor inside Herdr", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("windows console detection forces truecolor")
		}
		withCapabilityEnv(t, map[string]string{
			"TERM_PROGRAM": "herdr", "TERM": "xterm-256color", "KITTY_WINDOW_ID": "1",
		}, func() {
			if GetCapabilities().TrueColor {
				t.Fatal("Herdr must not inherit kitty truecolor from leaked variables")
			}
		})
	})
}

func TestGetCapabilitiesDetectsRexHyperlinks(t *testing.T) {
	// #11131 (pi 0ed1b0431): Rex supports OSC 8 but only partially implements
	// the Kitty graphics protocol, so hyperlinks/truecolor are on and images
	// stay off even when KITTY_WINDOW_ID leaks in.
	withCapabilityEnv(t, map[string]string{
		"TERM_PROGRAM": "rex", "TERM": "xterm-256color", "KITTY_WINDOW_ID": "1",
	}, func() {
		caps := GetCapabilities()
		if !caps.Hyperlinks {
			t.Fatal("Hyperlinks = false, want true for Rex")
		}
		if caps.Images != nil {
			t.Fatalf("Images = %v, want nil for Rex", caps.Images)
		}
		if !caps.TrueColor {
			t.Fatal("TrueColor = false, want true for Rex")
		}
	})
	withCapabilityEnv(t, map[string]string{"TERM_PROGRAM": "rex", "PI_HYPERLINKS": "0"}, func() {
		if GetCapabilities().Hyperlinks {
			t.Fatal("PI_HYPERLINKS=0 must force hyperlinks off for Rex")
		}
	})
}

func TestGetCapabilitiesTrueColorOverrides(t *testing.T) {
	t.Run("PI_TRUE_COLOR forces both directions", func(t *testing.T) {
		withCapabilityEnv(t, map[string]string{"TERM": "xterm-256color", "PI_TRUE_COLOR": "1"}, func() {
			if !GetCapabilities().TrueColor {
				t.Fatal("PI_TRUE_COLOR=1 must force truecolor")
			}
		})
		withCapabilityEnv(t, map[string]string{"TERM_PROGRAM": "iTerm.app", "PI_TRUE_COLOR": "0"}, func() {
			if GetCapabilities().TrueColor {
				t.Fatal("PI_TRUE_COLOR=0 must force 256color")
			}
		})
	})

	t.Run("auto and unknown values keep automatic detection", func(t *testing.T) {
		for _, value := range []string{"auto", "true", ""} {
			withCapabilityEnv(t, map[string]string{"TERM_PROGRAM": "iTerm.app", "PI_TRUE_COLOR": value}, func() {
				if !GetCapabilities().TrueColor {
					t.Fatalf("PI_TRUE_COLOR=%q must keep detection", value)
				}
			})
		}
	})

	t.Run("programmatic overrides win and clearing them restores auto detection", func(t *testing.T) {
		withCapabilityEnv(t, map[string]string{"TERM_PROGRAM": "iTerm.app", "PI_TRUE_COLOR": "1"}, func() {
			SetCapabilityOverrides(TerminalCapabilityOverrides{TrueColor: lo.ToPtr(false)})
			if GetCapabilities().TrueColor {
				t.Fatal("settings override must win over detection and PI_TRUE_COLOR")
			}
			SetCapabilityOverrides(TerminalCapabilityOverrides{})
			if !GetCapabilities().TrueColor {
				t.Fatal("clearing the override must return to automatic detection")
			}
		})
	})
}

// #10319 ports (pi 672000c80): placement rows are readable without a payload
// scan, from the explicit r= control or the registered metadata.

func TestGetKittyImagePlacementRowsReadsExplicitRowsWithoutRegisteredMetadata(t *testing.T) {
	sequence := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 3})
	if got := GetKittyImagePlacementRows(sequence); got != 3 {
		t.Fatalf("GetKittyImagePlacementRows = %d, want 3", got)
	}
}

func TestKittyImagePlacementRowsPreferExplicitControlsOverMetadata(t *testing.T) {
	RegisterKittyImageMetadata(KittyImageMetadata{ImageId: 42, Columns: 3, Rows: 3, WidthPx: 100, HeightPx: 100})
	transmission := EncodeKitty(strings.Repeat("A", 8192), EncodeKittyParams{Columns: 3, Rows: 3, ImageId: 42, MoveCursorOff: true})
	line := "left " + CropKittyImageLine(transmission, 2, 1) + " right"
	placement := GetKittyImagePlacement(line)
	if placement == nil {
		t.Fatal("expected a placement")
	}
	if got := GetKittyImagePlacementRows(line); got != 1 {
		t.Fatalf("GetKittyImagePlacementRows = %d, want 1", got)
	}
	if placement.TransmissionBytes != len(line)-len("left ")-len(" right") {
		t.Fatalf("TransmissionBytes = %d, want %d", placement.TransmissionBytes, len(line)-len("left ")-len(" right"))
	}
	if placement.EstimatedDecodedBytes != 100*100*4 {
		t.Fatalf("EstimatedDecodedBytes = %d, want %d", placement.EstimatedDecodedBytes, 100*100*4)
	}
	if placement.Rows != 1 {
		t.Fatalf("Rows = %d, want 1", placement.Rows)
	}
	if placement.Sequence != "\x1b_Ga=p,q=2,C=1,c=3,i=42,y=66,h=34,r=1\x1b\\" {
		t.Fatalf("Sequence = %q", placement.Sequence)
	}
}
