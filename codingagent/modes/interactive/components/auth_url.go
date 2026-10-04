// auth_url.go (pi: components/auth-url.ts, ced72c2f0) — a sign-in URL with a
// click hint and a copy hint. Hosts call Copy() when `app.message.copy` is
// pressed, since a long URL wraps and often cannot be selected or clicked as
// a whole (SSH, tmux).
package components

import (
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// authURLCopyToClipboard is the clipboard write seam (pig test convention;
// production: the interactive clipboard pipeline).
var authURLCopyToClipboard = utils.CopyToClipboard

// AuthURLComponent shows the auth URL (OSC 8 hyperlink) plus the platform
// click hint and the copy key hint (pi: AuthUrlComponent).
type AuthURLComponent struct {
	tui.Container

	url  string
	hint *tui.Text
}

// NewAuthURLComponent builds the component and requests a render through the
// host TUI (pi: setHint calls tui.requestRender).
func NewAuthURLComponent(tuiInstance tui.TUI, url string) *AuthURLComponent {
	c := &AuthURLComponent{url: url}
	currentTheme := theme.Current()
	c.AddChild(tui.NewText(currentTheme.Fg("accent", tui.Hyperlink(url, url)), 1, 0))
	c.hint = tui.NewText("", 1, 0)
	c.AddChild(c.hint)
	c.setHint(tuiInstance, coretools.KeyHint("app.message.copy", "to copy"))
	return c
}

func (c *AuthURLComponent) setHint(tuiInstance tui.TUI, suffix string) {
	currentTheme := theme.Current()
	clickHint := clickToOpenHint()
	c.hint.SetText(currentTheme.Fg("dim", tui.Hyperlink(clickHint, c.url)) + " " + currentTheme.Fg("dim", "•") + " " + suffix)
	if tuiInstance != nil {
		tuiInstance.RequestRender(false)
	}
}

// Copy writes the URL to the clipboard and reflects the outcome in the hint
// (pi: copy).
func (c *AuthURLComponent) Copy(tuiInstance tui.TUI) {
	currentTheme := theme.Current()
	if err := authURLCopyToClipboard(c.url); err != nil {
		c.setHint(tuiInstance, currentTheme.Fg("error", err.Error()))
		return
	}
	c.setHint(tuiInstance, currentTheme.Fg("success", "Copied URL to clipboard"))
}
