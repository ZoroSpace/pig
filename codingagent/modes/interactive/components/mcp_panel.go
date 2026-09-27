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
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	pigmcp "github.com/ZoroSpace/pig/mcp"
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
}

// MCPPanelOptions wires the panel into interactive mode. Mutate serializes
// UI mutation (interactive passes m.mutateUI: HandleInput reenters the TUI
// lock, ticker/async callbacks take it fresh); RequestRender wakes the
// render loop after background updates; CopyToClipboard is the interactive
// clipboard seam. RefreshInterval overrides the 2s status refresh tick
// (tests pass -1 to run without the ticker goroutine; 0 = default).
type MCPPanelOptions struct {
	Provider        MCPPanelProvider
	Mutate          func(fn func())
	RequestRender   func()
	CopyToClipboard func(text string) error
	OnCancel        func()
	RefreshInterval time.Duration
}

const mcpPanelRefreshInterval = 2 * time.Second

type mcpPanelRowKind int

const (
	mcpRowServer mcpPanelRowKind = iota
	mcpRowTool
	mcpRowError  // failed server's LastError line
	mcpRowNotice // loading / tool-listing error placeholder
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

	focused  bool
	stopTick chan struct{}
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
			coretools.KeyDisplayText(core.AppKeybindingMCPCopyError)+" copy error · "+
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
	for _, st := range p.filterStatuses() {
		rows = append(rows, mcpPanelRow{kind: mcpRowServer, server: st.Name, value: "server:" + st.Name})
		if st.Status == "error" && st.LastError != "" {
			rows = append(rows, mcpPanelRow{kind: mcpRowError, server: st.Name, value: "error:" + st.Name})
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
// disabled beats connecting beats failed beats connected).
func (p *MCPPanelComponent) serverSuffix(st pigmcp.ServerStatus) string {
	switch {
	case st.Disabled:
		return theme.Fg("dim", "disabled")
	case st.Status == "connecting":
		return theme.Fg("muted", "connecting")
	case st.Status == "error":
		return theme.Fg("error", "failed")
	case st.Status == "connected":
		suffix := theme.Fg("success", "connected")
		if st.IdleMs > 0 {
			suffix += theme.Fg("dim", " · idle "+mcpPanelFormatIdle(st.IdleMs))
		}
		return suffix
	default:
		return theme.Fg("dim", "disconnected")
	}
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
// The manager persists it to the winning mcp.json; failures surface in the
// notice line. Runs inside p.mutate; the flag flip itself is fast (memory +
// one small file write), so it happens inline and the row flips this frame.
// Enabling does not connect: the server goes to disconnected and reconnects
// lazily; the tools cache is dropped so a later expand re-lists.
func (p *MCPPanelComponent) toggleDisabledSelected() {
	server := p.selectedServer()
	if server == "" || p.busy[server] {
		return
	}
	st, ok := p.statusByName(server)
	if !ok {
		return
	}
	target := !st.Disabled
	err := p.opts.Provider.SetServerDisabled(server, target)
	if err != nil {
		p.setNoticeLocked(theme.Fg("error", "failed to update "+server+": "+err.Error()))
	} else {
		verb := "enabled"
		if target {
			verb = "disabled"
		}
		p.setNoticeLocked(theme.Fg("success", verb+" "+server+" (saved to mcp.json)"))
	}
	p.statuses = p.opts.Provider.Status()
	delete(p.tools, server)
	delete(p.toolErrs, server)
	p.rebuildLocked()
	p.requestRender()
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
	if kb.Matches(keyData, core.AppKeybindingMCPReconnect) {
		p.reconnectSelected()
		return
	}
	if kb.Matches(keyData, core.AppKeybindingMCPToggleDisabled) {
		p.toggleDisabledSelected()
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
