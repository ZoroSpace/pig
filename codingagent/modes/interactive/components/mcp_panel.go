// mcp_panel.go — the /mcp servers panel (pig-only; pi has no native MCP).
// Panel semantics follow the pi-mcp-adapter panel
// (github.com/nicobailon/pi-mcp-adapter): server rows with status suffixes,
// expandable tool rows, a live filter line, reconnect / enable-disable /
// copy-error actions. The differences are deliberate: no directTools mode,
// no OAuth actions, no metadata cache (expanding lists tools live, which
// connects lazy servers), and enable/disable persists straight into the
// server's winning mcp.json file instead of an overlay state file.
package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	pigmcp "github.com/ZoroSpace/pig/integrations/mcp"
	"github.com/ZoroSpace/pig/tui"
	pigutils "github.com/ZoroSpace/pig/utils"
)

// MCPPanelProvider is the data/action surface the panel needs. *pigmcp.Manager
// satisfies it; the interface keeps the component fake-able in tests
// (ReadonlyFooterDataProvider precedent).
type MCPPanelProvider interface {
	Status() []pigmcp.ServerStatus
	ListServerTools(ctx context.Context, server string) ([]pigmcp.ToolInfo, error)
	Reconnect(ctx context.Context, server string) error
	SetServerDisabled(server string, disabled bool) error
	// SetServerDisabledInProject writes the toggle as a project override of a
	// global server (#10277; pi's "Enable/Disable in this project" item).
	SetServerDisabledInProject(server string, disabled bool) error
	// SignIn runs the interactive OAuth sign-in (S12-4; pi: signInWithUi).
	// The prompt shows the authorization URL and collects a pasted redirect
	// URL; openURL (optional) opens the browser.
	SignIn(ctx context.Context, server string, prompt pigmcp.SignInPrompt, openURL func(string)) error
	// SignOut removes stored OAuth credentials and disconnects (pi: signOut).
	SignOut(server string) (bool, error)
}

// MCPPanelOptions wires the panel into interactive mode. Mutate serializes
// UI mutation (interactive passes m.mutateUI: HandleInput reenters the TUI
// lock, ticker/async callbacks take it fresh); RequestRender wakes the
// render loop after background updates; CopyToClipboard is the interactive
// clipboard seam; OpenURL (optional, S12-4) opens the OAuth authorization
// URL in the browser. RefreshInterval overrides the 2s status refresh tick
// (tests pass -1 to run without the ticker goroutine; 0 = default).
type MCPPanelOptions struct {
	Provider        MCPPanelProvider
	Mutate          func(fn func())
	RequestRender   func()
	CopyToClipboard func(text string) error
	OnCancel        func()
	OpenURL         func(url string)
	RefreshInterval time.Duration
}

const mcpPanelRefreshInterval = 2 * time.Second

type mcpPanelRowKind int

const (
	mcpRowServer mcpPanelRowKind = iota
	mcpRowTool
	mcpRowError  // failed server's LastError line
	mcpRowNotice // loading / tool-listing error / needs-sign-in placeholder
)

type mcpPanelRow struct {
	kind   mcpPanelRowKind
	server string // owning server name
	tool   string // raw tool name (mcpRowTool)
	value  string // SelectItem identity
}

// MCPPanelComponent renders the /mcp panel (tui.Container + Focusable, like
// the other selectors).
type MCPPanelComponent struct {
	tui.Container
	opts MCPPanelOptions

	statuses []pigmcp.ServerStatus
	expanded map[string]bool
	tools    map[string][]pigmcp.ToolInfo // fetched per server (nil = not fetched)
	toolErrs map[string]string
	loading  map[string]bool
	busy     map[string]bool // reconnect/disable in flight for a server
	rows     []mcpPanelRow

	searchInput          *tui.Input
	selectList           *tui.SelectList
	selectListChildIndex int
	noticeText           *tui.Text

	// signIn is the active OAuth sign-in view (S12-4); non-nil swaps the
	// server list for the sign-in screen until the flow settles.
	signIn *mcpSignInView

	focused  bool
	stopTick chan struct{}
}

// mcpSignInView is the panel's sign-in screen (pi: signInWithUi + the
// manager view's redirect-URL screen): status line, the authorization URL
// (with copy hint, S12-2's AuthURLComponent), and a paste input racing the
// browser callback for machines the browser cannot reach (SSH).
type mcpSignInView struct {
	server string
	status *tui.Text
	url    *AuthURLComponent // set once the flow produces the URL
	input  *tui.Input
	paste  chan string // submitted redirect URLs (buffered 1)
	cancel context.CancelFunc
}

// NewMCPPanelComponent builds the panel. Provider status is snapshotted
// immediately; a 2s ticker keeps rows fresh while the panel is open (mcp
// statuses change under the user's eyes: connects, reaps, reconnects).
func NewMCPPanelComponent(opts MCPPanelOptions) *MCPPanelComponent {
	p := &MCPPanelComponent{
		Container: tui.Container{},
		opts:      opts,
		expanded:  map[string]bool{},
		tools:     map[string][]pigmcp.ToolInfo{},
		toolErrs:  map[string]string{},
		loading:   map[string]bool{},
		busy:      map[string]bool{},
		stopTick:  make(chan struct{}),
	}

	p.AddChild(NewDynamicBorder(nil))
	p.AddChild(tui.NewSpacer(1))
	p.AddChild(tui.NewText("MCP Servers", 0, 0))
	p.AddChild(tui.NewSpacer(1))

	p.searchInput = tui.NewInput()
	p.searchInput.OnSubmit = func(_ string) { p.selectList.HandleInput("\r") }
	p.AddChild(p.searchInput)
	p.AddChild(tui.NewSpacer(1))

	p.statuses = opts.Provider.Status()
	p.rebuildLocked()
	p.AddChild(tui.NewSpacer(1))

	p.noticeText = tui.NewText("", 0, 0)
	p.AddChild(p.noticeText)
	p.AddChild(tui.NewText(theme.Fg("dim",
		"  "+coretools.KeyDisplayText("tui.select.confirm")+" expand/collapse · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPReconnect)+" reconnect · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPToggleDisabled)+" enable/disable · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPToggleDisabledProject)+" in project · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPCopyError)+" copy error · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPSignIn)+" sign in · "+
			coretools.KeyDisplayText(core.AppKeybindingMCPSignOut)+" sign out · "+
			coretools.KeyDisplayText("tui.select.cancel")+" close"), 0, 0))
	p.AddChild(NewDynamicBorder(nil))

	if opts.RefreshInterval >= 0 {
		go p.ticker()
	}
	return p
}

// Dispose stops the refresh ticker (selectorComponent.dispose hook; the
// panel is rebuilt on every /mcp open, so leaks would accumulate).
func (p *MCPPanelComponent) Dispose() {
	select {
	case <-p.stopTick:
	default:
		close(p.stopTick)
	}
}

func (p *MCPPanelComponent) ticker() {
	interval := p.opts.RefreshInterval
	if interval == 0 {
		interval = mcpPanelRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopTick:
			return
		case <-ticker.C:
			p.refreshNow()
		}
	}
}

// refreshNow re-snapshots provider status and rebuilds rows (ticker path;
// tests call it directly instead of waiting on the 2s interval).
func (p *MCPPanelComponent) refreshNow() {
	p.mutate(func() {
		p.statuses = p.opts.Provider.Status()
		p.rebuildLocked()
	})
	p.requestRender()
}

func (p *MCPPanelComponent) mutate(fn func()) {
	if p.opts.Mutate != nil {
		p.opts.Mutate(fn)
		return
	}
	fn()
}

func (p *MCPPanelComponent) requestRender() {
	if p.opts.RequestRender != nil {
		p.opts.RequestRender()
	}
}

// rebuildLocked recomputes the row list from the current state and swaps the
// select list child, preserving the selection by row identity. Runs inside
// p.mutate (TUI lock) — never call it from a bare goroutine.
func (p *MCPPanelComponent) rebuildLocked() {
	selected := ""
	if p.selectList != nil {
		if item := p.selectList.GetSelectedItem(); item != nil {
			selected = item.Value
		}
	}

	var rows []mcpPanelRow
	if p.signIn != nil {
		// Sign-in screen: swap the select list for the sign-in view (status,
		// authorization URL with copy hint, paste input).
		screen := &tui.Container{}
		screen.AddChild(tui.NewSpacer(1))
		screen.AddChild(tui.NewText(theme.Fg("accent", "Sign in to "+p.signIn.server), 1, 0))
		screen.AddChild(tui.NewSpacer(1))
		screen.AddChild(p.signIn.status)
		if p.signIn.url != nil {
			screen.AddChild(tui.NewSpacer(1))
			screen.AddChild(p.signIn.url)
		}
		screen.AddChild(tui.NewSpacer(1))
		screen.AddChild(p.signIn.input)
		screen.AddChild(tui.NewSpacer(1))
		screen.AddChild(tui.NewText(theme.Fg("dim",
			"  paste the redirect URL and press "+coretools.KeyDisplayText("tui.select.confirm")+" · "+
				coretools.KeyDisplayText("app.message.copy")+" copy URL · "+
				coretools.KeyDisplayText("tui.select.cancel")+" cancel"), 0, 0))
		if len(p.Children) > p.selectListChildIndex {
			p.Children[p.selectListChildIndex] = screen
		} else {
			p.selectListChildIndex = len(p.Children)
			p.AddChild(screen)
		}
		p.selectList = nil
		p.rows = rows
		return
	}
	for _, st := range p.filterStatuses() {
		rows = append(rows, mcpPanelRow{kind: mcpRowServer, server: st.Name, value: "server:" + st.Name})
		if st.Status == "error" && st.LastError != "" {
			rows = append(rows, mcpPanelRow{kind: mcpRowError, server: st.Name, value: "error:" + st.Name})
		}
		if st.Status == "needs-auth" {
			rows = append(rows, mcpPanelRow{kind: mcpRowNotice, server: st.Name, value: "needsauth:" + st.Name})
		}
		if !p.expanded[st.Name] {
			continue
		}
		if p.loading[st.Name] {
			rows = append(rows, mcpPanelRow{kind: mcpRowNotice, server: st.Name, value: "notice:" + st.Name})
			continue
		}
		if err := p.toolErrs[st.Name]; err != "" && len(p.tools[st.Name]) == 0 {
			rows = append(rows, mcpPanelRow{kind: mcpRowError, server: st.Name, value: "toolerr:" + st.Name})
			continue
		}
		for _, tool := range p.tools[st.Name] {
			rows = append(rows, mcpPanelRow{kind: mcpRowTool, server: st.Name, tool: tool.RawName, value: "tool:" + st.Name + "/" + tool.RawName})
		}
	}
	p.rows = rows

	items := make([]tui.SelectItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, p.renderRow(row))
	}
	maxVisible := len(items)
	if maxVisible == 0 {
		maxVisible = 1
	}
	list := tui.NewSelectList(items, maxVisible, theme.GetSelectListTheme(), nil)
	list.OnSelect = func(item tui.SelectItem) { p.handleConfirm(item) }
	list.OnCancel = func() { p.handleCancel() }
	for i, item := range items {
		if item.Value == selected {
			list.SetSelectedIndex(i)
			break
		}
	}
	if len(p.Children) > p.selectListChildIndex {
		p.Children[p.selectListChildIndex] = list
	} else {
		p.selectListChildIndex = len(p.Children)
		p.AddChild(list)
	}
	p.selectList = list
}

// filterStatuses applies the live filter (fuzzy over server names; tool rows
// follow their expanded server).
func (p *MCPPanelComponent) filterStatuses() []pigmcp.ServerStatus {
	query := strings.TrimSpace(p.searchInput.GetValue())
	statuses := slices.Clone(p.statuses)
	slices.SortFunc(statuses, func(a, b pigmcp.ServerStatus) int { return strings.Compare(a.Name, b.Name) })
	if query == "" {
		return statuses
	}
	filtered := tui.FuzzyFilter(statuses, query, func(st pigmcp.ServerStatus) string { return st.Name })
	return filtered
}

// renderRow builds one SelectItem (pi-mcp-adapter row shapes: expand marker +
// status symbol + name + transport/lifecycle + tool count + status suffix;
// errors hang below the row in the error color).
func (p *MCPPanelComponent) renderRow(row mcpPanelRow) tui.SelectItem {
	switch row.kind {
	case mcpRowServer:
		st, ok := p.statusByName(row.server)
		if !ok {
			return tui.SelectItem{Value: row.value, Label: row.server}
		}
		return tui.SelectItem{Value: row.value, Label: p.serverLabel(st), Description: p.serverSuffix(st)}
	case mcpRowTool:
		for _, tool := range p.tools[row.server] {
			if tool.RawName == row.tool {
				return tui.SelectItem{
					Value: row.value,
					Label: "    " + tool.Name + " — " + strings.ReplaceAll(tool.Description, "\n", " "),
				}
			}
		}
		return tui.SelectItem{Value: row.value, Label: "    " + row.tool}
	case mcpRowError:
		if strings.HasPrefix(row.value, "toolerr:") {
			return tui.SelectItem{Value: row.value, Label: "    " + theme.Fg("error", "listing tools failed: "+p.toolErrs[row.server])}
		}
		st, ok := p.statusByName(row.server)
		if !ok {
			return tui.SelectItem{Value: row.value, Label: "    " + theme.Fg("error", "failed")}
		}
		return tui.SelectItem{Value: row.value, Label: "    " + theme.Fg("error", st.LastError)}
	case mcpRowNotice:
		if strings.HasPrefix(row.value, "needsauth:") {
			return tui.SelectItem{Value: row.value, Label: "    " + theme.Fg("warning", "needs sign-in — press "+coretools.KeyDisplayText(core.AppKeybindingMCPSignIn)+" to sign in")}
		}
		return tui.SelectItem{Value: row.value, Label: "    " + theme.Fg("dim", "connecting… listing tools")}
	}
	return tui.SelectItem{Value: row.value}
}

func (p *MCPPanelComponent) statusByName(name string) (pigmcp.ServerStatus, bool) {
	for _, st := range p.statuses {
		if st.Name == name {
			return st, true
		}
	}
	return pigmcp.ServerStatus{}, false
}

// serverLabel renders the server row up to the status suffix.
func (p *MCPPanelComponent) serverLabel(st pigmcp.ServerStatus) string {
	marker := "▸"
	if p.expanded[st.Name] {
		marker = "▾"
	}
	symbol := "○"
	switch {
	case st.Disabled:
		symbol = "·"
	case st.Status == "connected":
		symbol = "●"
	case st.Status == "connecting":
		symbol = "◌"
	case st.Status == "needs-auth":
		symbol = "∅"
	case st.Status == "error":
		symbol = "✗"
	}
	var b strings.Builder
	b.WriteString(marker + " " + symbol + " " + st.Name)
	meta := st.Transport
	if st.Lifecycle != "" {
		meta += "·" + st.Lifecycle
	}
	b.WriteString(" (" + meta + ")")
	if st.ToolCount > 0 {
		b.WriteString(fmt.Sprintf(" · %d tools", st.ToolCount))
	}
	return b.String()
}

// serverSuffix renders the colored status suffix (pi-mcp-adapter order:
// disabled beats connecting beats failed beats connected). An overridden
// server carries the "project override" marker (#10277).
func (p *MCPPanelComponent) serverSuffix(st pigmcp.ServerStatus) string {
	suffix := ""
	switch {
	case st.Disabled:
		suffix = theme.Fg("dim", "disabled")
	case st.Status == "connecting":
		suffix = theme.Fg("muted", "connecting")
	case st.Status == "needs-auth":
		suffix = theme.Fg("warning", "needs sign-in")
	case st.Status == "error":
		suffix = theme.Fg("error", "failed")
	case st.Status == "connected":
		suffix = theme.Fg("success", "connected")
		if st.IdleMs > 0 {
			suffix += theme.Fg("dim", " · idle "+mcpPanelFormatIdle(st.IdleMs))
		}
	default:
		suffix = theme.Fg("dim", "disconnected")
	}
	if st.OverridePath != "" {
		suffix += theme.Fg("dim", " · project override")
	}
	return suffix
}

func mcpPanelFormatIdle(ms int64) string {
	switch {
	case ms < 60_000:
		return strconv.FormatInt(ms/1000, 10) + "s"
	case ms < 3_600_000:
		return strconv.FormatInt(ms/60_000, 10) + "m"
	default:
		return strconv.FormatInt(ms/3_600_000, 10) + "h"
	}
}

// ---- actions ----

// handleConfirm toggles expansion on server rows (connecting + listing tools
// lazily); tool rows do nothing on enter. Runs inside p.mutate (via
// HandleInput or SelectList mouse dispatch, both under the TUI lock).
func (p *MCPPanelComponent) handleConfirm(item tui.SelectItem) {
	if !strings.HasPrefix(item.Value, "server:") {
		return
	}
	server := strings.TrimPrefix(item.Value, "server:")
	if p.expanded[server] {
		delete(p.expanded, server)
		p.rebuildLocked()
		return
	}
	p.expanded[server] = true
	if _, fetched := p.tools[server]; fetched || p.loading[server] {
		p.rebuildLocked()
		return
	}
	p.loading[server] = true
	p.rebuildLocked()
	p.requestRender()
	p.fetchTools(server)
}

// fetchTools lists one server's tools in the background (decision B: the
// list connects lazy servers, so it must not block input handling). The
// result lands via p.mutate.
func (p *MCPPanelComponent) fetchTools(server string) {
	go pigutils.Guard("mcp.panel.tools", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		tools, err := p.opts.Provider.ListServerTools(ctx, server)
		p.mutate(func() {
			p.loading[server] = false
			if err != nil {
				p.toolErrs[server] = err.Error()
				p.tools[server] = nil
			} else {
				delete(p.toolErrs, server)
				p.tools[server] = tools
			}
			p.rebuildLocked()
		})
		p.requestRender()
	})
}

func (p *MCPPanelComponent) handleCancel() {
	p.Dispose()
	if p.opts.OnCancel != nil {
		p.opts.OnCancel()
	}
}

// selectedServer resolves the selected row's owning server (tool and error
// rows act on their server).
func (p *MCPPanelComponent) selectedServer() string {
	item := p.selectList.GetSelectedItem()
	if item == nil {
		return ""
	}
	for _, row := range p.rows {
		if row.value == item.Value {
			return row.server
		}
	}
	return ""
}

// reconnectSelected drops and reconnects the selected server (ctrl+r). The
// connect runs in the background; failures surface in the notice line and in
// the server's error row on the next refresh. Runs inside p.mutate.
func (p *MCPPanelComponent) reconnectSelected() {
	server := p.selectedServer()
	if server == "" || p.busy[server] {
		return
	}
	p.busy[server] = true
	p.setNoticeLocked(theme.Fg("muted", "reconnecting "+server+"…"))
	p.requestRender()
	go pigutils.Guard("mcp.panel.reconnect", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := p.opts.Provider.Reconnect(ctx, server)
		p.mutate(func() {
			p.busy[server] = false
			if err != nil {
				p.setNoticeLocked(theme.Fg("error", "reconnect failed: "+err.Error()))
			} else {
				p.setNoticeLocked(theme.Fg("success", "reconnected "+server))
			}
			p.statuses = p.opts.Provider.Status()
			p.rebuildLocked()
		})
		p.requestRender()
	})
}

// toggleDisabledSelected flips the selected server's disabled flag (ctrl+d).
// The manager persists it to the file that owns the server — the project
// override file for overridden servers, else the winning source mcp.json;
// failures surface in the notice line. Runs inside p.mutate; the flag flip
// itself is fast (memory + one small file write), so it happens inline and
// the row flips this frame. Enabling does not connect: the server goes to
// disconnected and reconnects lazily; the tools cache is dropped so a later
// expand re-lists.
func (p *MCPPanelComponent) toggleDisabledSelected() {
	p.toggleDisabledWithTarget(false)
}

// toggleDisabledInProject flips the selected global server's disabled flag as
// a project override (ctrl+p, #10277): the global mcp.json is untouched,
// the trusted project's mcp.json records the override.
func (p *MCPPanelComponent) toggleDisabledInProject() {
	p.toggleDisabledWithTarget(true)
}

func (p *MCPPanelComponent) toggleDisabledWithTarget(inProject bool) {
	server := p.selectedServer()
	if server == "" || p.busy[server] {
		return
	}
	st, ok := p.statusByName(server)
	if !ok {
		return
	}
	if inProject {
		if !st.ProjectOverridable {
			p.setNoticeLocked(theme.Fg("dim", "no project override available for "+server+" (already overridden, defined in the project, or project not trusted)"))
			p.requestRender()
			return
		}
	}
	target := !st.Disabled
	where := "mcp.json"
	if inProject || st.OverridePath != "" {
		where = "the project mcp.json"
	}
	// pi #10562: run connection actions in the background so the subscribed
	// menu stays usable while the disable/replace settles.
	p.busy[server] = true
	p.setNoticeLocked(theme.Fg("muted", "updating "+server+"…"))
	p.requestRender()
	go pigutils.Guard("mcp.panel.toggle", func() {
		var err error
		if inProject {
			err = p.opts.Provider.SetServerDisabledInProject(server, target)
		} else {
			err = p.opts.Provider.SetServerDisabled(server, target)
		}
		p.mutate(func() {
			p.busy[server] = false
			if err != nil {
				p.setNoticeLocked(theme.Fg("error", "failed to update "+server+": "+err.Error()))
			} else {
				verb := "enabled"
				if target {
					verb = "disabled"
				}
				p.setNoticeLocked(theme.Fg("success", verb+" "+server+" (saved to "+where+")"))
			}
			p.statuses = p.opts.Provider.Status()
			delete(p.tools, server)
			delete(p.toolErrs, server)
			p.rebuildLocked()
		})
		p.requestRender()
	})
}

// copyErrorSelected copies the selected server's last error (ctrl+y). Runs
// inside p.mutate.
func (p *MCPPanelComponent) copyErrorSelected() {
	server := p.selectedServer()
	if server == "" {
		return
	}
	st, ok := p.statusByName(server)
	if !ok || st.LastError == "" {
		p.setNoticeLocked(theme.Fg("dim", "no error to copy for "+server))
		p.requestRender()
		return
	}
	if p.opts.CopyToClipboard == nil {
		return
	}
	if err := p.opts.CopyToClipboard(st.LastError); err != nil {
		p.setNoticeLocked(theme.Fg("error", "copy failed: "+err.Error()))
	} else {
		p.setNoticeLocked(theme.Fg("success", "error copied to clipboard"))
	}
	p.requestRender()
}

func (p *MCPPanelComponent) setNoticeLocked(text string) {
	p.noticeText.SetText(text)
}

// ---- OAuth sign-in (S12-4; pi: signInWithUi) ----

// signInSelected starts the interactive OAuth sign-in for the selected
// server: the panel swaps to the sign-in screen and the flow runs in the
// background (the callback server + prompt race run inside the manager).
func (p *MCPPanelComponent) signInSelected() {
	server := p.selectedServer()
	if server == "" || p.signIn != nil {
		return
	}
	st, ok := p.statusByName(server)
	if !ok {
		return
	}
	if st.OAuthURL == "" {
		p.setNoticeLocked(theme.Fg("dim", "MCP server "+server+" does not use OAuth (only HTTP servers without an Authorization header do)"))
		p.requestRender()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	view := &mcpSignInView{
		server: server,
		status: tui.NewText(theme.Fg("dim", "Contacting the authorization server…"), 1, 0),
		input:  tui.NewInput(),
		paste:  make(chan string, 1),
		cancel: cancel,
	}
	view.input.OnSubmit = func(value string) {
		select {
		case view.paste <- value:
		default:
		}
	}
	p.signIn = view
	p.rebuildLocked()
	p.requestRender()
	go pigutils.Guard("mcp.panel.signin", func() {
		err := p.opts.Provider.SignIn(ctx, server, mcpSignInPrompt{p: p, view: view}, p.opts.OpenURL)
		p.mutate(func() {
			if p.signIn == view {
				p.signIn = nil
				p.rebuildLocked()
			}
			switch {
			case err == nil:
				p.setNoticeLocked(theme.Fg("success", "signed in to "+server))
			case errors.Is(err, pigmcp.ErrSignInCancelled):
				p.setNoticeLocked(theme.Fg("dim", "sign-in cancelled"))
			default:
				p.setNoticeLocked(theme.Fg("error", err.Error()))
			}
			p.statuses = p.opts.Provider.Status()
		})
		p.requestRender()
	})
}

// signOutSelected removes the selected server's stored OAuth credentials and
// disconnects (ctrl+o).
func (p *MCPPanelComponent) signOutSelected() {
	server := p.selectedServer()
	if server == "" || p.signIn != nil {
		return
	}
	st, ok := p.statusByName(server)
	if !ok || st.OAuthURL == "" {
		p.setNoticeLocked(theme.Fg("dim", "MCP server "+server+" does not use OAuth"))
		p.requestRender()
		return
	}
	go pigutils.Guard("mcp.panel.signout", func() {
		removed, err := p.opts.Provider.SignOut(server)
		p.mutate(func() {
			switch {
			case err != nil:
				p.setNoticeLocked(theme.Fg("error", "sign out failed: "+err.Error()))
			case removed:
				p.setNoticeLocked(theme.Fg("success", "signed out of "+server))
			default:
				p.setNoticeLocked(theme.Fg("dim", "no stored credentials for "+server))
			}
			p.statuses = p.opts.Provider.Status()
			delete(p.tools, server)
			delete(p.toolErrs, server)
			p.rebuildLocked()
		})
		p.requestRender()
	})
}

// cancelSignInLocked aborts the active sign-in (esc): the context
// cancellation unblocks the flow, which reports sign-in cancelled.
func (p *MCPPanelComponent) cancelSignInLocked() {
	if p.signIn == nil {
		return
	}
	p.signIn.cancel()
}

// mcpSignInPrompt adapts the sign-in screen to pigmcp.SignInPrompt. Methods
// run on the sign-in goroutine; UI touches marshal through p.mutate.
type mcpSignInPrompt struct {
	p    *MCPPanelComponent
	view *mcpSignInView
}

// ShowAuthorizationURL displays the URL with its copy hint (pi:
// showAuthorizationUrl + the manager view's URL screen).
func (m mcpSignInPrompt) ShowAuthorizationURL(url string) {
	m.p.mutate(func() {
		if m.p.signIn != m.view {
			return
		}
		m.view.url = NewAuthURLComponent(nil, url)
		m.view.status.SetText(theme.Fg("dim", "Complete the sign-in in your browser. If it cannot reach this machine, paste the URL it redirected to."))
		m.p.rebuildLocked()
	})
	m.p.requestRender()
}

// PromptForRedirectURL resolves with the pasted redirect URL, or an error
// once the context is cancelled (callback arrival or esc).
func (m mcpSignInPrompt) PromptForRedirectURL(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", pigmcp.ErrSignInCancelled
	case value := <-m.view.paste:
		if strings.TrimSpace(value) == "" {
			return "", pigmcp.ErrSignInCancelled
		}
		return value, nil
	}
}

// SetFocused implements Focusable — propagate to the search input for IME
// cursor positioning (thinking_selector precedent).
func (p *MCPPanelComponent) GetFocused() bool { return p.focused }

func (p *MCPPanelComponent) SetFocused(value bool) {
	p.focused = value
	p.searchInput.SetFocused(value)
}

// HandleInput implements InputHandler: panel actions first (they must win
// over the select list), then navigation keys, then the filter input. The
// whole body runs inside p.mutate — in production the TUI lock is reentrant
// (HandleInput already holds it), and background goroutines serialize through
// the same hook.
func (p *MCPPanelComponent) HandleInput(keyData string) {
	p.mutate(func() { p.handleInputLocked(keyData) })
}

func (p *MCPPanelComponent) handleInputLocked(keyData string) {
	kb := tui.GetKeybindings()
	if p.signIn != nil {
		// Sign-in screen: esc cancels, copy copies the URL, everything else
		// goes to the paste input.
		if kb.Matches(keyData, "tui.select.cancel") {
			p.cancelSignInLocked()
			return
		}
		if kb.Matches(keyData, "app.message.copy") && p.signIn.url != nil {
			p.signIn.url.Copy(nil)
			p.requestRender()
			return
		}
		p.signIn.input.HandleInput(keyData)
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPReconnect) {
		p.reconnectSelected()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPSignIn) {
		p.signInSelected()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPSignOut) {
		p.signOutSelected()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPToggleDisabled) {
		p.toggleDisabledSelected()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPToggleDisabledProject) {
		p.toggleDisabledInProject()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPCopyError) {
		p.copyErrorSelected()
		return
	}
	if kb.Matches(keyData, "tui.select.cancel") {
		p.handleCancel()
		return
	}
	if kb.Matches(keyData, "tui.select.up") ||
		kb.Matches(keyData, "tui.select.down") ||
		kb.Matches(keyData, "tui.select.pageUp") ||
		kb.Matches(keyData, "tui.select.pageDown") ||
		kb.Matches(keyData, "tui.select.confirm") {
		p.selectList.HandleInput(keyData)
		return
	}
	p.searchInput.HandleInput(keyData)
	p.rebuildLocked()
}
