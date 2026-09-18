// earendil_announcement.go (pi: components/earendil-announcement.ts) — the
// "/dementedelves" easter-egg announcement.
//
// Declared gap: pi adds a Kitty-terminal Image child (clankolas.png) when the
// bundled asset loads; pig's Image component (terminal-image) lands with
// Phase 8 (photon/wazero), so the image child is omitted here and the
// component always renders the text-only fallback (pi's no-image path).
package components

import (
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

const earendilBlogURL = "https://mariozechner.at/posts/2026-04-08-ive-sold-out/"

// EarendilAnnouncementComponent is the announcement card (pi: class
// EarendilAnnouncementComponent extends Container).
type EarendilAnnouncementComponent struct {
	tui.Container
}

// NewEarendilAnnouncementComponent builds the card (pi: constructor).
func NewEarendilAnnouncementComponent() *EarendilAnnouncementComponent {
	c := &EarendilAnnouncementComponent{}
	c.AddChild(NewDynamicBorder(func(text string) string { return theme.Fg("accent", text) }))
	c.AddChild(tui.NewText(theme.Bold(theme.Fg("accent", "pi has joined Earendil")), 1, 0))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(theme.Fg("muted", "Read the blog post:"), 1, 0))
	c.AddChild(tui.NewText(theme.Fg("mdLink", earendilBlogURL), 1, 0))
	c.AddChild(tui.NewSpacer(1))

	// pi (image branch): Image(clankolas.png base64, "image/png",
	// {fallbackColor: muted}, {maxWidthCells: 56, filename}) + Spacer —
	// omitted until the Phase 8 Image component lands (see file header).
	c.AddChild(NewDynamicBorder(func(text string) string { return theme.Fg("accent", text) }))
	return c
}
