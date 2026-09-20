// session_selector.go (pi: components/session-selector.ts) — the /resume
// session selector: threaded/recent/relevance session listing with search
// (tokens / "phrases" / re: regex), scope toggle (current folder / all),
// rename, path display toggle, delete confirmation (trash CLI with unlink
// fallback), and symlink-canonicalized parent threading.
//
// Declared deviations (pig concurrency):
//   - pi's SessionsLoader is an async function; pig's is a synchronous
//     callback-shaped loader invoked on a goroutine. All post-load component
//     mutations route through the Mutator hook (nil = run inline, tests).
//   - pi's confirmRename is async; pig runs it inline (local-file IO only)
//     before the refresh load goroutine starts.
package components

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
	pigutils "github.com/ZoroSpace/pig/utils"
)

// sessionScope is the session listing scope (pi: SessionScope).
type sessionScope string

const (
	sessionScopeCurrent sessionScope = "current"
	sessionScopeAll     sessionScope = "all"
)

func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func formatSessionDate(date time.Time) string {
	now := time.Now()
	diffMs := now.Sub(date)
	diffMins := int(diffMs / time.Minute)
	diffHours := int(diffMs / time.Hour)
	diffDays := int(diffMs / 24 / time.Hour)

	if diffMins < 1 {
		return "now"
	}
	if diffMins < 60 {
		return fmt.Sprintf("%dm", diffMins)
	}
	if diffHours < 24 {
		return fmt.Sprintf("%dh", diffHours)
	}
	if diffDays < 7 {
		return fmt.Sprintf("%dd", diffDays)
	}
	if diffDays < 30 {
		return fmt.Sprintf("%dw", diffDays/7)
	}
	if diffDays < 365 {
		return fmt.Sprintf("%dmo", diffDays/30)
	}
	return fmt.Sprintf("%dy", diffDays/365)
}

// statusMessageKind discriminates header status messages (pi: inline union
// { type: "info" | "error"; message }).
type statusMessageKind string

const (
	statusKindInfo  statusMessageKind = "info"
	statusKindError statusMessageKind = "error"
)

// sessionStatusMessage is one header status message (pi: inline union).
type sessionStatusMessage struct {
	kind    statusMessageKind
	message string
}

// sessionSelectorHeader is the /resume header line + hint rows (pi:
// SessionSelectorHeader implements Component).
type sessionSelectorHeader struct {
	scope         sessionScope
	sortMode      SortMode
	nameFilter    NameFilter
	requestRender func()
	loading       bool
	loadProgress  *[2]int
	showPath      bool
	// confirmingDeletePath "" = null (paths are never empty).
	confirmingDeletePath string
	statusMessage        *sessionStatusMessage
	statusTimeout        *time.Timer
	showRenameHint       bool
}

func newSessionSelectorHeader(scope sessionScope, sortMode SortMode, nameFilter NameFilter, requestRender func()) *sessionSelectorHeader {
	return &sessionSelectorHeader{
		scope:         scope,
		sortMode:      sortMode,
		nameFilter:    nameFilter,
		requestRender: requestRender,
	}
}

func (h *sessionSelectorHeader) setScope(scope sessionScope) { h.scope = scope }

func (h *sessionSelectorHeader) setSortMode(sortMode SortMode) { h.sortMode = sortMode }

func (h *sessionSelectorHeader) setNameFilter(nameFilter NameFilter) { h.nameFilter = nameFilter }

func (h *sessionSelectorHeader) setLoading(loading bool) {
	h.loading = loading
	// Progress is scoped to the current load; clear whenever the loading state is set
	h.loadProgress = nil
}

func (h *sessionSelectorHeader) setProgress(loaded, total int) {
	h.loadProgress = &[2]int{loaded, total}
}

func (h *sessionSelectorHeader) setShowPath(showPath bool) { h.showPath = showPath }

func (h *sessionSelectorHeader) setShowRenameHint(show bool) { h.showRenameHint = show }

func (h *sessionSelectorHeader) setConfirmingDeletePath(path string) {
	h.confirmingDeletePath = path
}

func (h *sessionSelectorHeader) clearStatusTimeout() {
	if h.statusTimeout != nil {
		h.statusTimeout.Stop()
		h.statusTimeout = nil
	}
}

// setStatusMessage sets the status message; autoHideMs 0 = stick around (pi:
// setStatusMessage(msg, autoHideMs?)). msg nil = clear.
func (h *sessionSelectorHeader) setStatusMessage(msg *sessionStatusMessage, autoHideMs int) {
	h.clearStatusTimeout()
	h.statusMessage = msg
	if msg == nil || autoHideMs == 0 {
		return
	}

	h.statusTimeout = time.AfterFunc(time.Duration(autoHideMs)*time.Millisecond, func() {
		h.statusMessage = nil
		h.statusTimeout = nil
		h.requestRender()
	})
}

func (h *sessionSelectorHeader) Invalidate() {}

func (h *sessionSelectorHeader) Render(width int) []string {
	title := "Resume Session (Current Folder)"
	if h.scope == sessionScopeAll {
		title = "Resume Session (All)"
	}
	leftText := theme.Bold(title)

	sortLabel := "Fuzzy"
	switch h.sortMode {
	case SortModeThreaded:
		sortLabel = "Threaded"
	case SortModeRecent:
		sortLabel = "Recent"
	}
	sortText := theme.Fg("muted", "Sort: ") + theme.Fg("accent", sortLabel)

	nameLabel := "All"
	if h.nameFilter == NameFilterNamed {
		nameLabel = "Named"
	}
	nameText := theme.Fg("muted", "Name: ") + theme.Fg("accent", nameLabel)

	var scopeText string
	if h.loading {
		progressText := "..."
		if h.loadProgress != nil {
			progressText = fmt.Sprintf("%d/%d", h.loadProgress[0], h.loadProgress[1])
		}
		scopeText = theme.Fg("muted", "○ Current Folder | ") + theme.Fg("accent", fmt.Sprintf("Loading %s", progressText))
	} else if h.scope == sessionScopeCurrent {
		scopeText = theme.Fg("accent", "◉ Current Folder") + theme.Fg("muted", " | ○ All")
	} else {
		scopeText = theme.Fg("muted", "○ Current Folder | ") + theme.Fg("accent", "◉ All")
	}

	rightText := tui.TruncateToWidth(scopeText+"  "+nameText+"  "+sortText, width, "", false)
	availableLeft := maxInt(0, width-tui.VisibleWidth(rightText)-1)
	left := tui.TruncateToWidth(leftText, availableLeft, "", false)
	spacing := maxInt(0, width-tui.VisibleWidth(left)-tui.VisibleWidth(rightText))

	// Build hint lines - changes based on state (all branches truncate to width)
	var hintLine1, hintLine2 string
	if h.confirmingDeletePath != "" {
		confirmHint := fmt.Sprintf("Delete session? %s · %s",
			coretools.KeyHint("tui.select.confirm", "confirm"), coretools.KeyHint("tui.select.cancel", "cancel"))
		hintLine1 = theme.Fg("error", tui.TruncateToWidth(confirmHint, width, "…", false))
		hintLine2 = ""
	} else if h.statusMessage != nil {
		color := "accent"
		if h.statusMessage.kind == statusKindError {
			color = "error"
		}
		hintLine1 = theme.Fg(color, tui.TruncateToWidth(h.statusMessage.message, width, "…", false))
		hintLine2 = ""
	} else {
		pathState := "(off)"
		if h.showPath {
			pathState = "(on)"
		}
		sep := theme.Fg("muted", " · ")
		hint1 := coretools.KeyHint("tui.input.tab", "scope") + sep +
			theme.Fg("muted", "re:<pattern> regex · \"phrase\" exact")
		hint2Parts := []string{
			coretools.KeyHint(core.AppKeybindingSessionToggleSort, "sort"),
			coretools.KeyHint(core.AppKeybindingSessionToggleNamedFilter, "named"),
			coretools.KeyHint(core.AppKeybindingSessionDelete, "delete"),
			coretools.KeyHint(core.AppKeybindingSessionTogglePath, fmt.Sprintf("path %s", pathState)),
		}
		if h.showRenameHint {
			hint2Parts = append(hint2Parts, coretools.KeyHint(core.AppKeybindingSessionRename, "rename"))
		}
		hint2 := strings.Join(hint2Parts, sep)
		hintLine1 = tui.TruncateToWidth(hint1, width, "…", false)
		hintLine2 = tui.TruncateToWidth(hint2, width, "…", false)
	}

	return []string{
		left + strings.Repeat(" ", spacing) + rightText,
		hintLine1,
		hintLine2,
	}
}

// sessionTreeNode is a session tree node for hierarchical display (pi:
// SessionTreeNode in session-selector.ts).
type sessionTreeNode struct {
	session        *core.SessionInfo
	children       []*sessionTreeNode
	latestActivity time.Time
}

// flatSessionNode is a flattened node for display with tree structure info
// (pi: FlatSessionNode).
type flatSessionNode struct {
	session           *core.SessionInfo
	depth             int
	isLast            bool
	ancestorContinues []bool
}

// canonicalizeSessionPath resolves symlinks for threading (pi:
// canonicalizePath wrapper).
func canonicalizeSessionPath(path string) string {
	if path == "" {
		return path
	}
	return utils.CanonicalizePath(path)
}

// buildSessionTree builds a tree structure from sessions based on
// parentSessionPath. Returns root nodes sorted by modified date (descending
// via subtree latest activity).
func buildSessionTree(sessions []core.SessionInfo) []*sessionTreeNode {
	byPath := map[string]*sessionTreeNode{}

	for i := range sessions {
		session := &sessions[i]
		sessionPath := canonicalizeSessionPath(session.Path)
		byPath[sessionPath] = &sessionTreeNode{session: session, latestActivity: session.Modified}
	}

	roots := []*sessionTreeNode{}

	for i := range sessions {
		session := &sessions[i]
		sessionPath := canonicalizeSessionPath(session.Path)
		node := byPath[sessionPath]
		parentPath := ""
		if session.ParentSessionPath != nil {
			parentPath = canonicalizeSessionPath(*session.ParentSessionPath)
		}

		if parentPath != "" {
			if parent, ok := byPath[parentPath]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}

	var updateLatestActivity func(node *sessionTreeNode) time.Time
	updateLatestActivity = func(node *sessionTreeNode) time.Time {
		latestActivity := node.session.Modified
		for _, child := range node.children {
			childLatest := updateLatestActivity(child)
			if childLatest.After(latestActivity) {
				latestActivity = childLatest
			}
		}
		node.latestActivity = latestActivity
		return latestActivity
	}

	for _, root := range roots {
		updateLatestActivity(root)
	}

	// Sort children and roots by latest activity in each subtree (descending)
	var sortNodes func(nodes []*sessionTreeNode)
	sortNodes = func(nodes []*sessionTreeNode) {
		sortNodesStableByActivity(nodes)
		for _, node := range nodes {
			sortNodes(node.children)
		}
	}
	sortNodes(roots)

	return roots
}

func sortNodesStableByActivity(nodes []*sessionTreeNode) {
	// Stable insertion sort keeps equal-activity order deterministic (pi:
	// Array#sort stability).
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j].latestActivity.After(nodes[j-1].latestActivity); j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
}

// flattenSessionTree flattens the tree into a display list with tree
// structure metadata.
func flattenSessionTree(roots []*sessionTreeNode) []*flatSessionNode {
	var result []*flatSessionNode

	var walk func(node *sessionTreeNode, depth int, ancestorContinues []bool, isLast bool)
	walk = func(node *sessionTreeNode, depth int, ancestorContinues []bool, isLast bool) {
		result = append(result, &flatSessionNode{
			session: node.session, depth: depth, isLast: isLast, ancestorContinues: ancestorContinues,
		})

		for i := 0; i < len(node.children); i++ {
			childIsLast := i == len(node.children)-1
			// Only show continuation line for non-root ancestors
			continues := false
			if depth > 0 {
				continues = !isLast
			}
			walk(node.children[i], depth+1, append(append([]bool{}, ancestorContinues...), continues), childIsLast)
		}
	}

	for i := 0; i < len(roots); i++ {
		walk(roots[i], 0, nil, i == len(roots)-1)
	}

	return result
}

// sessionList is the custom session list component with multi-line items and
// search (pi: SessionList implements Component, Focusable).
type sessionList struct {
	allSessions                 []core.SessionInfo
	filteredSessions            []*flatSessionNode
	selectedIndex               int
	searchInput                 *tui.Input
	showCwd                     bool
	sortMode                    SortMode
	nameFilter                  NameFilter
	keybindings                 *core.KeybindingsManager
	showPath                    bool
	confirmingDeletePath        string
	currentSessionCanonicalPath string
	maxVisible                  int // Max sessions visible (one line each)

	// OnSelect fires with the selected session path (pi: public onSelect?).
	OnSelect func(sessionPath string)
	// OnCancel fires on escape (pi: public onCancel?).
	OnCancel func()
	// OnExit fires on the editor submit path (pi: public onExit).
	OnExit func()
	// OnToggleScope fires on Tab (pi: public onToggleScope?).
	OnToggleScope func()
	// OnToggleSort fires on the sort key (pi: public onToggleSort?).
	OnToggleSort func()
	// OnToggleNameFilter fires on the named-filter key (pi: public
	// onToggleNameFilter?).
	OnToggleNameFilter func()
	// OnTogglePath fires after the path display toggles (pi: public
	// onTogglePath?).
	OnTogglePath func(showPath bool)
	// OnDeleteConfirmationChange fires when the delete-confirm target changes
	// (pi: public onDeleteConfirmationChange?; "" = null).
	OnDeleteConfirmationChange func(path string)
	// OnDeleteSession performs the deletion (pi: public onDeleteSession?).
	OnDeleteSession func(sessionPath string)
	// OnRenameSession starts the rename flow (pi: public onRenameSession?).
	OnRenameSession func(sessionPath string)
	// OnError surfaces list errors (pi: public onError?).
	OnError func(message string)
}

// newSessionList builds the list (pi: constructor; currentSessionFilePath
// "" = undefined).
func newSessionList(
	sessions []core.SessionInfo,
	showCwd bool,
	sortMode SortMode,
	nameFilter NameFilter,
	keybindings *core.KeybindingsManager,
	currentSessionFilePath string,
) *sessionList {
	l := &sessionList{
		allSessions:   sessions,
		searchInput:   tui.NewInput(),
		showCwd:       showCwd,
		sortMode:      sortMode,
		nameFilter:    nameFilter,
		keybindings:   keybindings,
		maxVisible:    10,
		selectedIndex: 0,
	}
	if currentSessionFilePath != "" {
		l.currentSessionCanonicalPath = canonicalizeSessionPath(currentSessionFilePath)
	}
	l.filterSessions("")

	// Handle Enter in search input - select current item
	l.searchInput.OnSubmit = func(_ string) {
		if l.selectedIndex < len(l.filteredSessions) && l.OnSelect != nil {
			l.OnSelect(l.filteredSessions[l.selectedIndex].session.Path)
		}
	}
	return l
}

func (l *sessionList) Invalidate() {}

// GetFocused/SetFocused implement Focusable - propagate to searchInput for
// IME cursor positioning.
func (l *sessionList) GetFocused() bool      { return l.searchInput.GetFocused() }
func (l *sessionList) SetFocused(value bool) { l.searchInput.SetFocused(value) }

// GetSelectedSessionPath returns the selected session path, "" = undefined
// (pi: getSelectedSessionPath).
func (l *sessionList) GetSelectedSessionPath() string {
	if l.selectedIndex < len(l.filteredSessions) {
		return l.filteredSessions[l.selectedIndex].session.Path
	}
	return ""
}

func (l *sessionList) setSortMode(sortMode SortMode) {
	l.sortMode = sortMode
	l.filterSessions(l.searchInput.GetValue())
}

func (l *sessionList) setNameFilter(nameFilter NameFilter) {
	l.nameFilter = nameFilter
	l.filterSessions(l.searchInput.GetValue())
}

func (l *sessionList) setSessions(sessions []core.SessionInfo, showCwd bool) {
	l.allSessions = sessions
	l.showCwd = showCwd
	l.filterSessions(l.searchInput.GetValue())
}

func (l *sessionList) filterSessions(query string) {
	trimmed := strings.TrimSpace(query)
	nameFiltered := l.allSessions
	if l.nameFilter == NameFilterNamed {
		nameFiltered = nil
		for i := range l.allSessions {
			if HasSessionName(&l.allSessions[i]) {
				nameFiltered = append(nameFiltered, l.allSessions[i])
			}
		}
	}

	if l.sortMode == SortModeThreaded && trimmed == "" {
		// Threaded mode without search: show tree structure
		roots := buildSessionTree(nameFiltered)
		l.filteredSessions = flattenSessionTree(roots)
	} else {
		// Other modes or with search: flat list
		filtered := FilterAndSortSessions(nameFiltered, query, l.sortMode, NameFilterAll)
		l.filteredSessions = make([]*flatSessionNode, 0, len(filtered))
		for i := range filtered {
			l.filteredSessions = append(l.filteredSessions, &flatSessionNode{
				session: &filtered[i],
				depth:   0,
				isLast:  true,
			})
		}
	}
	l.selectedIndex = minInt(l.selectedIndex, maxInt(0, len(l.filteredSessions)-1))
}

func (l *sessionList) setConfirmingDeletePath(path string) {
	l.confirmingDeletePath = path
	if l.OnDeleteConfirmationChange != nil {
		l.OnDeleteConfirmationChange(path)
	}
}

func (l *sessionList) startDeleteConfirmationForSelectedSession() {
	if l.selectedIndex >= len(l.filteredSessions) {
		return
	}
	selected := l.filteredSessions[l.selectedIndex]

	// Prevent deleting current session
	if l.isCurrentSessionPath(selected.session.Path) {
		if l.OnError != nil {
			l.OnError("Cannot delete the currently active session")
		}
		return
	}

	l.setConfirmingDeletePath(selected.session.Path)
}

func (l *sessionList) isCurrentSessionPath(path string) bool {
	if l.currentSessionCanonicalPath == "" {
		return false
	}
	return canonicalizeSessionPath(path) == l.currentSessionCanonicalPath
}

// Render implements Component.
func (l *sessionList) Render(width int) []string {
	lines := []string{}

	// Render search input
	lines = append(lines, l.searchInput.Render(width)...)
	lines = append(lines, "") // Blank line after search

	if len(l.filteredSessions) == 0 {
		var emptyMessage string
		if l.nameFilter == NameFilterNamed {
			toggleKey := coretools.KeyText(core.AppKeybindingSessionToggleNamedFilter)
			if l.showCwd {
				emptyMessage = fmt.Sprintf("  No named sessions found. Press %s to show all.", toggleKey)
			} else {
				emptyMessage = fmt.Sprintf("  No named sessions in current folder. Press %s to show all, or Tab to view all.", toggleKey)
			}
		} else if l.showCwd {
			// "All" scope - no sessions anywhere that match filter
			emptyMessage = "  No sessions found"
		} else {
			// "Current folder" scope - hint to try "all"
			emptyMessage = "  No sessions in current folder. Press Tab to view all."
		}
		lines = append(lines, theme.Fg("muted", tui.TruncateToWidth(emptyMessage, width, "…", false)))
		return lines
	}

	// Calculate visible range with scrolling
	startIndex := maxInt(0, minInt(
		l.selectedIndex-l.maxVisible/2,
		len(l.filteredSessions)-l.maxVisible,
	))
	endIndex := minInt(startIndex+l.maxVisible, len(l.filteredSessions))

	// Render visible sessions (one line each with tree structure)
	for i := startIndex; i < endIndex; i++ {
		node := l.filteredSessions[i]
		session := node.session
		isSelected := i == l.selectedIndex
		isConfirmingDelete := session.Path == l.confirmingDeletePath
		isCurrent := l.isCurrentSessionPath(session.Path)

		// Build tree prefix
		prefix := buildSessionTreePrefix(node)

		// Session display text (name or first message)
		hasName := session.Name != nil
		displayText := session.FirstMessage
		if session.Name != nil {
			displayText = *session.Name
		}
		normalizedMessage := normalizeControlChars(displayText)

		// Right side: message count and age
		age := formatSessionDate(session.Modified)
		msgCount := fmt.Sprintf("%d", session.MessageCount)
		rightPart := fmt.Sprintf("%s %s", msgCount, age)
		if l.showCwd && session.Cwd != "" {
			rightPart = fmt.Sprintf("%s %s", shortenPath(session.Cwd), rightPart)
		}
		if l.showPath {
			rightPart = fmt.Sprintf("%s %s", shortenPath(session.Path), rightPart)
		}

		// Cursor
		cursor := "  "
		if isSelected {
			cursor = theme.Fg("accent", "› ")
		}

		// Calculate available width for message
		prefixWidth := tui.VisibleWidth(prefix)
		rightWidth := tui.VisibleWidth(rightPart) + 2           // +2 for spacing
		availableForMsg := width - 2 - prefixWidth - rightWidth // -2 for cursor

		truncatedMsg := tui.TruncateToWidth(normalizedMessage, maxInt(10, availableForMsg), "…", false)

		// Style message
		var messageColor string
		if isConfirmingDelete {
			messageColor = "error"
		} else if isCurrent {
			messageColor = "accent"
		} else if hasName {
			messageColor = "warning"
		}
		var styledMsg string
		if messageColor != "" {
			styledMsg = theme.Fg(messageColor, truncatedMsg)
		} else {
			styledMsg = truncatedMsg
		}
		if isSelected {
			styledMsg = theme.Bold(styledMsg)
		}

		// Build line
		leftPart := cursor + theme.Fg("dim", prefix) + styledMsg
		leftWidth := tui.VisibleWidth(leftPart)
		spacing := maxInt(1, width-leftWidth-tui.VisibleWidth(rightPart))
		rightColor := "dim"
		if isConfirmingDelete {
			rightColor = "error"
		}
		styledRight := theme.Fg(rightColor, rightPart)

		line := leftPart + strings.Repeat(" ", spacing) + styledRight
		if isSelected {
			line = theme.Bg("selectedBg", line)
		}
		lines = append(lines, tui.TruncateToWidth(line, width, "", false))
	}

	// Add scroll indicator if needed
	if startIndex > 0 || endIndex < len(l.filteredSessions) {
		scrollText := fmt.Sprintf("  (%d/%d)", l.selectedIndex+1, len(l.filteredSessions))
		scrollInfo := theme.Fg("muted", tui.TruncateToWidth(scrollText, width, "", false))
		lines = append(lines, scrollInfo)
	}

	return lines
}

// normalizeControlChars replaces control characters with spaces (pi:
// replace(/[\x00-\x1f\x7f]/g, " ")).
func normalizeControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}

func buildSessionTreePrefix(node *flatSessionNode) string {
	if node.depth == 0 {
		return ""
	}

	var parts []string
	for _, continues := range node.ancestorContinues {
		if continues {
			parts = append(parts, "│  ")
		} else {
			parts = append(parts, "   ")
		}
	}
	branch := "├─ "
	if node.isLast {
		branch = "└─ "
	}
	return strings.Join(parts, "") + branch
}

// HandleInput implements InputHandler.
func (l *sessionList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()

	// Handle delete confirmation state first - intercept all keys
	if l.confirmingDeletePath != "" {
		if kb.Matches(keyData, "tui.select.confirm") {
			pathToDelete := l.confirmingDeletePath
			l.setConfirmingDeletePath("")
			if l.OnDeleteSession != nil {
				l.OnDeleteSession(pathToDelete)
			}
			return
		}
		if kb.Matches(keyData, "tui.select.cancel") {
			l.setConfirmingDeletePath("")
			return
		}
		// Ignore all other keys while confirming
		return
	}

	if kb.Matches(keyData, "tui.input.tab") {
		if l.OnToggleScope != nil {
			l.OnToggleScope()
		}
		return
	}

	if kb.Matches(keyData, core.AppKeybindingSessionToggleSort) {
		if l.OnToggleSort != nil {
			l.OnToggleSort()
		}
		return
	}

	if l.keybindings.Matches(keyData, core.AppKeybindingSessionToggleNamedFilter) {
		if l.OnToggleNameFilter != nil {
			l.OnToggleNameFilter()
		}
		return
	}

	// Ctrl+P: toggle path display
	if kb.Matches(keyData, core.AppKeybindingSessionTogglePath) {
		l.showPath = !l.showPath
		if l.OnTogglePath != nil {
			l.OnTogglePath(l.showPath)
		}
		return
	}

	// Ctrl+D: initiate delete confirmation (useful on terminals that don't
	// distinguish Ctrl+Backspace from Backspace)
	if kb.Matches(keyData, core.AppKeybindingSessionDelete) {
		l.startDeleteConfirmationForSelectedSession()
		return
	}

	// Rename selected session
	if kb.Matches(keyData, core.AppKeybindingSessionRename) {
		if l.selectedIndex < len(l.filteredSessions) && l.OnRenameSession != nil {
			l.OnRenameSession(l.filteredSessions[l.selectedIndex].session.Path)
		}
		return
	}

	// Ctrl+Backspace: non-invasive convenience alias for delete
	// Only triggers deletion when the query is empty; otherwise it is forwarded to the input
	if kb.Matches(keyData, core.AppKeybindingSessionDeleteNoninvasive) {
		if len(l.searchInput.GetValue()) > 0 {
			l.searchInput.HandleInput(keyData)
			l.filterSessions(l.searchInput.GetValue())
			return
		}

		l.startDeleteConfirmationForSelectedSession()
		return
	}

	switch {
	case kb.Matches(keyData, "tui.select.up"):
		// Up arrow
		l.selectedIndex = maxInt(0, l.selectedIndex-1)
	case kb.Matches(keyData, "tui.select.down"):
		// Down arrow
		l.selectedIndex = minInt(len(l.filteredSessions)-1, l.selectedIndex+1)
	case kb.Matches(keyData, "tui.select.pageUp"):
		// Page up - jump up by maxVisible items
		l.selectedIndex = maxInt(0, l.selectedIndex-l.maxVisible)
	case kb.Matches(keyData, "tui.select.pageDown"):
		// Page down - jump down by maxVisible items
		l.selectedIndex = minInt(len(l.filteredSessions)-1, l.selectedIndex+l.maxVisible)
	case kb.Matches(keyData, "tui.select.confirm"):
		// Enter
		if l.selectedIndex < len(l.filteredSessions) && l.OnSelect != nil {
			l.OnSelect(l.filteredSessions[l.selectedIndex].session.Path)
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		// Escape - cancel
		if l.OnCancel != nil {
			l.OnCancel()
		}
	default:
		// Pass everything else to search input
		l.searchInput.HandleInput(keyData)
		l.filterSessions(l.searchInput.GetValue())
	}
}

// sessionsLoader loads one scope's sessions (pi: SessionsLoader — async;
// pig is sync and runs on a goroutine). onProgress may be nil. pi v0.86.0
// adds partialSessions to the progress callback; pig threads it through
// (v0.86.0 sync batch) while the selector keeps publishing only totals
// until the S6-3b interactive batch reworks it.
type sessionsLoader func(onProgress core.SessionListProgress) []core.SessionInfo

// sessionDeleteResult is the deleteSessionFile outcome (pi: inline return
// type { ok, method, error? }).
type sessionDeleteResult struct {
	ok     bool
	method string // "trash" | "unlink"
	err    string
}

// deleteSessionFile deletes a session file, trying the `trash` CLI first,
// then falling back to unlink (pi: deleteSessionFile).
func deleteSessionFile(sessionPath string) sessionDeleteResult {
	// Try `trash` first (if installed)
	trashArgs := []string{sessionPath}
	if strings.HasPrefix(sessionPath, "-") {
		trashArgs = []string{"--", sessionPath}
	}
	trashCmd := exec.Command("trash", trashArgs...)
	var stderr strings.Builder
	trashCmd.Stderr = &stderr
	trashErr := trashCmd.Run()

	getTrashErrorHint := func() string {
		var parts []string
		if trashErr != nil {
			parts = append(parts, trashErr.Error())
		}
		if trimmed := strings.TrimSpace(stderr.String()); trimmed != "" {
			firstLine := trimmed
			if idx := strings.Index(trimmed, "\n"); idx >= 0 {
				firstLine = trimmed[:idx]
			}
			parts = append(parts, firstLine)
		}
		if len(parts) == 0 {
			return ""
		}
		joined := strings.Join(parts, " · ")
		if len(joined) > 200 {
			joined = joined[:200]
		}
		return fmt.Sprintf("trash: %s", joined)
	}

	_, statErr := os.Stat(sessionPath)
	fileGone := errors.Is(statErr, os.ErrNotExist)

	// If trash reports success, or the file is gone afterwards, treat it as successful
	if trashErr == nil || fileGone {
		return sessionDeleteResult{ok: true, method: "trash"}
	}

	// Fallback to permanent deletion
	if err := os.Remove(sessionPath); err != nil {
		unlinkError := err.Error()
		trashErrorHint := getTrashErrorHint()
		if trashErrorHint != "" {
			unlinkError = fmt.Sprintf("%s (%s)", unlinkError, trashErrorHint)
		}
		return sessionDeleteResult{ok: false, method: "unlink", err: unlinkError}
	}
	return sessionDeleteResult{ok: true, method: "unlink"}
}

// SessionSelectorOptions carries the optional constructor options (pi:
// options? parameter).
type SessionSelectorOptions struct {
	// RenameSession persists a session rename (pi: renameSession?; nil =
	// rename disabled).
	RenameSession func(sessionPath string, nextName string) error
	// ShowRenameHint forces the rename hint (pi: showRenameHint?; nil =
	// follow RenameSession presence).
	ShowRenameHint *bool
	// Keybindings is the coding-agent keybindings manager (pi: keybindings?;
	// nil = fresh defaults).
	Keybindings *core.KeybindingsManager
}

// SessionSelectorComponent renders a session selector (pi:
// SessionSelectorComponent extends Container implements Focusable).
type SessionSelectorComponent struct {
	tui.Container

	handleInput func(data string)

	canRename             bool
	sessionList           *sessionList
	header                *sessionSelectorHeader
	keybindings           *core.KeybindingsManager
	scope                 sessionScope
	sortMode              SortMode
	nameFilter            NameFilter
	currentSessions       []core.SessionInfo
	hasCurrentSessions    bool
	allSessions           []core.SessionInfo
	hasAllSessions        bool
	currentSessionsLoader sessionsLoader
	allSessionsLoader     sessionsLoader
	requestRender         func()
	renameSession         func(sessionPath string, nextName string) error
	currentLoading        bool
	allLoading            bool
	allLoadSeq            int

	mode             string // "list" | "rename"
	renameInput      *tui.Input
	renameTargetPath string

	// mutator serializes background-goroutine component mutations (pig
	// concurrency note; nil = run inline, tests). Set at construction — the
	// initial load goroutine starts there.
	mutator func(fn func())

	focused bool
}

// NewSessionSelectorComponent builds the selector (pi: constructor;
// currentSessionFilePath "" = undefined).
func NewSessionSelectorComponent(
	currentSessionsLoader sessionsLoader,
	allSessionsLoader sessionsLoader,
	onSelect func(sessionPath string),
	onCancel func(),
	onExit func(),
	requestRender func(),
	options *SessionSelectorOptions,
	currentSessionFilePath string,
	mutator func(func()),
) *SessionSelectorComponent {
	s := &SessionSelectorComponent{
		Container:             tui.Container{},
		scope:                 sessionScopeCurrent,
		sortMode:              SortModeThreaded,
		nameFilter:            NameFilterAll,
		currentSessionsLoader: currentSessionsLoader,
		allSessionsLoader:     allSessionsLoader,
		requestRender:         requestRender,
		mode:                  "list",
		renameInput:           tui.NewInput(),
		mutator:               mutator,
	}
	if options != nil && options.Keybindings != nil {
		s.keybindings = options.Keybindings
	} else {
		s.keybindings = core.NewKeybindingsManager(nil, "")
	}
	s.header = newSessionSelectorHeader(s.scope, s.sortMode, s.nameFilter, requestRender)
	if options != nil {
		s.renameSession = options.RenameSession
	}
	s.canRename = s.renameSession != nil
	showRenameHint := s.canRename
	if options != nil && options.ShowRenameHint != nil {
		showRenameHint = *options.ShowRenameHint
	}
	s.header.setShowRenameHint(showRenameHint)

	// Create session list (starts empty, will be populated after load)
	s.sessionList = newSessionList(nil, false, s.sortMode, s.nameFilter, s.keybindings, currentSessionFilePath)

	s.buildBaseLayout(s.sessionList, true)

	s.renameInput.OnSubmit = func(_ string) {
		s.confirmRename(s.renameInput.GetValue())
	}

	// Ensure header status timeouts are cleared when leaving the selector
	clearStatusMessage := func() { s.header.setStatusMessage(nil, 0) }
	s.sessionList.OnSelect = func(sessionPath string) {
		clearStatusMessage()
		onSelect(sessionPath)
	}
	s.sessionList.OnCancel = func() {
		clearStatusMessage()
		onCancel()
	}
	s.sessionList.OnExit = func() {
		clearStatusMessage()
		onExit()
	}
	s.sessionList.OnToggleScope = func() { s.toggleScope() }
	s.sessionList.OnToggleSort = func() { s.toggleSortMode() }
	s.sessionList.OnToggleNameFilter = func() { s.toggleNameFilter() }
	s.sessionList.OnRenameSession = func(sessionPath string) {
		if s.renameSession == nil {
			return
		}
		if s.scope == sessionScopeCurrent && s.currentLoading {
			return
		}
		if s.scope == sessionScopeAll && s.allLoading {
			return
		}

		sessions := s.currentSessions
		if s.scope == sessionScopeAll {
			sessions = s.allSessions
		}
		var currentName string
		for i := range sessions {
			if sessions[i].Path == sessionPath {
				if sessions[i].Name != nil {
					currentName = *sessions[i].Name
				}
				break
			}
		}
		s.enterRenameMode(sessionPath, currentName)
	}

	// Sync list events to header
	s.sessionList.OnTogglePath = func(showPath bool) {
		s.header.setShowPath(showPath)
		s.requestRender()
	}
	s.sessionList.OnDeleteConfirmationChange = func(path string) {
		s.header.setConfirmingDeletePath(path)
		s.requestRender()
	}
	s.sessionList.OnError = func(msg string) {
		s.header.setStatusMessage(&sessionStatusMessage{kind: statusKindError, message: msg}, 3000)
		s.requestRender()
	}

	// Handle session deletion
	s.sessionList.OnDeleteSession = func(sessionPath string) {
		result := deleteSessionFile(sessionPath)

		if result.ok {
			s.currentSessions = filterSessionsByPath(s.currentSessions, sessionPath)
			s.allSessions = filterSessionsByPath(s.allSessions, sessionPath)

			sessions := s.currentSessions
			if s.scope == sessionScopeAll {
				sessions = s.allSessions
			}
			showCwd := s.scope == sessionScopeAll
			s.sessionList.setSessions(sessions, showCwd)

			msg := "Session deleted"
			if result.method == "trash" {
				msg = "Session moved to trash"
			}
			s.header.setStatusMessage(&sessionStatusMessage{kind: statusKindInfo, message: msg}, 2000)
			s.refreshSessionsAfterMutation()
		} else {
			errorMessage := result.err
			if errorMessage == "" {
				errorMessage = "Unknown error"
			}
			s.header.setStatusMessage(&sessionStatusMessage{
				kind: statusKindError, message: fmt.Sprintf("Failed to delete: %s", errorMessage),
			}, 3000)
		}

		s.requestRender()
	}

	// Start loading current sessions immediately
	s.loadCurrentSessions()
	return s
}

func filterSessionsByPath(sessions []core.SessionInfo, path string) []core.SessionInfo {
	out := make([]core.SessionInfo, 0, len(sessions))
	for i := range sessions {
		if sessions[i].Path != path {
			out = append(out, sessions[i])
		}
	}
	return out
}

// mutate serializes a component mutation through the Mutator hook.
func (s *SessionSelectorComponent) mutate(fn func()) {
	if s.mutator != nil {
		s.mutator(fn)
		return
	}
	fn()
}

// HandleInput implements InputHandler (pi: handleInput).
func (s *SessionSelectorComponent) HandleInput(data string) {
	if s.mode == "rename" {
		kb := tui.GetKeybindings()
		if kb.Matches(data, "tui.select.cancel") {
			s.exitRenameMode()
			return
		}
		s.renameInput.HandleInput(data)
		return
	}

	s.sessionList.HandleInput(data)
}

// GetFocused/SetFocused implement Focusable - propagate to sessionList /
// renameInput for IME cursor positioning.
func (s *SessionSelectorComponent) GetFocused() bool { return s.focused }
func (s *SessionSelectorComponent) SetFocused(value bool) {
	s.focused = value
	s.sessionList.SetFocused(value)
	s.renameInput.SetFocused(value)
}

// buildBaseLayout rebuilds the container children (pi: buildBaseLayout).
func (s *SessionSelectorComponent) buildBaseLayout(content tui.Component, showHeader bool) {
	s.Clear()
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(func(text string) string { return theme.Fg("accent", text) }))
	s.AddChild(tui.NewSpacer(1))
	if showHeader {
		s.AddChild(s.header)
		s.AddChild(tui.NewSpacer(1))
	}
	s.AddChild(content)
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(func(text string) string { return theme.Fg("accent", text) }))
}

func (s *SessionSelectorComponent) loadCurrentSessions() {
	s.loadScope(sessionScopeCurrent, "initial")
}

func (s *SessionSelectorComponent) enterRenameMode(sessionPath string, currentName string) {
	s.mode = "rename"
	s.renameTargetPath = sessionPath
	s.renameInput.SetValue(currentName)
	s.renameInput.SetFocused(true)

	panel := &tui.Container{}
	panel.AddChild(tui.NewText(theme.Bold("Rename Session"), 1, 0))
	panel.AddChild(tui.NewSpacer(1))
	panel.AddChild(s.renameInput)
	panel.AddChild(tui.NewSpacer(1))
	panel.AddChild(tui.NewText(
		theme.Fg("muted", fmt.Sprintf("%s to save · %s to cancel",
			coretools.KeyText("tui.select.confirm"), coretools.KeyText("tui.select.cancel"))),
		1, 0,
	))

	s.buildBaseLayout(panel, false)
	s.requestRender()
}

func (s *SessionSelectorComponent) exitRenameMode() {
	s.mode = "list"
	s.renameTargetPath = ""

	s.buildBaseLayout(s.sessionList, true)

	s.requestRender()
}

func (s *SessionSelectorComponent) confirmRename(value string) {
	next := strings.TrimSpace(value)
	if next == "" {
		return
	}
	target := s.renameTargetPath
	if target == "" {
		s.exitRenameMode()
		return
	}

	// Find current name for callback
	renameSession := s.renameSession
	if renameSession == nil {
		s.exitRenameMode()
		return
	}

	if err := renameSession(target, next); err != nil {
		s.header.setStatusMessage(&sessionStatusMessage{
			kind: statusKindError, message: fmt.Sprintf("Failed to rename: %s", errorMessageOf(err)),
		}, 4000)
	}
	s.refreshSessionsAfterMutation()
	s.exitRenameMode()
}

// loadScopeReason mirrors pi's reason parameter.
type loadScopeReason string

const (
	loadScopeInitial loadScopeReason = "initial"
	loadScopeRefresh loadScopeReason = "refresh"
	loadScopeToggle  loadScopeReason = "toggle"
)

func (s *SessionSelectorComponent) loadScope(scope sessionScope, reason loadScopeReason) {
	showCwd := scope == sessionScopeAll

	// Mark loading
	if scope == sessionScopeCurrent {
		s.currentLoading = true
	} else {
		s.allLoading = true
	}

	seq := -1
	if scope == sessionScopeAll {
		s.allLoadSeq++
		seq = s.allLoadSeq
	}
	s.header.setScope(scope)
	s.header.setLoading(true)
	s.requestRender()

	onProgress := func(loaded, total int, partialSessions []core.SessionInfo) {
		_ = partialSessions // consumed by the interactive batch (pi v0.86.0)
		s.mutate(func() {
			if scope != s.scope {
				return
			}
			if seq != -1 && seq != s.allLoadSeq {
				return
			}
			s.header.setProgress(loaded, total)
			s.requestRender()
		})
	}

	// pi: await loader — the sync loader runs on a goroutine (pig
	// concurrency note).
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("components.sessionSelector.load")
		var sessions []core.SessionInfo
		var loadErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					loadErr = fmt.Errorf("%v", r)
				}
			}()
			if scope == sessionScopeCurrent {
				sessions = s.currentSessionsLoader(onProgress)
			} else {
				sessions = s.allSessionsLoader(onProgress)
			}
		}()

		s.mutate(func() {
			if loadErr == nil {
				if scope == sessionScopeCurrent {
					s.currentSessions = sessions
					s.hasCurrentSessions = true
					s.currentLoading = false
				} else {
					s.allSessions = sessions
					s.hasAllSessions = true
					s.allLoading = false
				}
			} else {
				if scope == sessionScopeCurrent {
					s.currentLoading = false
				} else {
					s.allLoading = false
				}
			}

			if scope != s.scope {
				return
			}
			if seq != -1 && seq != s.allLoadSeq {
				return
			}

			if loadErr != nil {
				s.header.setLoading(false)
				s.header.setStatusMessage(&sessionStatusMessage{
					kind:    statusKindError,
					message: fmt.Sprintf("Failed to load sessions: %s", errorMessageOf(loadErr)),
				}, 4000)

				if reason == loadScopeInitial {
					s.sessionList.setSessions(nil, showCwd)
				}
				s.requestRender()
				return
			}

			s.header.setLoading(false)
			s.sessionList.setSessions(sessions, showCwd)
			s.requestRender()
		})
	}()
}

func (s *SessionSelectorComponent) toggleSortMode() {
	// Cycle: threaded -> recent -> relevance -> threaded
	if s.sortMode == SortModeThreaded {
		s.sortMode = SortModeRecent
	} else if s.sortMode == SortModeRecent {
		s.sortMode = SortModeRelevance
	} else {
		s.sortMode = SortModeThreaded
	}
	s.header.setSortMode(s.sortMode)
	s.sessionList.setSortMode(s.sortMode)
	s.requestRender()
}

func (s *SessionSelectorComponent) toggleNameFilter() {
	if s.nameFilter == NameFilterAll {
		s.nameFilter = NameFilterNamed
	} else {
		s.nameFilter = NameFilterAll
	}
	s.header.setNameFilter(s.nameFilter)
	s.sessionList.setNameFilter(s.nameFilter)
	s.requestRender()
}

func (s *SessionSelectorComponent) refreshSessionsAfterMutation() {
	s.loadScope(s.scope, loadScopeRefresh)
}

func (s *SessionSelectorComponent) toggleScope() {
	if s.scope == sessionScopeCurrent {
		s.scope = sessionScopeAll
		s.header.setScope(s.scope)

		if s.hasAllSessions {
			s.header.setLoading(false)
			s.sessionList.setSessions(s.allSessions, true)
			s.requestRender()
			return
		}

		if !s.allLoading {
			s.loadScope(sessionScopeAll, loadScopeToggle)
		}
		return
	}

	s.scope = sessionScopeCurrent
	s.header.setScope(s.scope)
	s.header.setLoading(s.currentLoading)
	s.sessionList.setSessions(s.currentSessions, false)
	s.requestRender()
}

// GetSessionList exposes the inner list (pi: getSessionList).
func (s *SessionSelectorComponent) GetSessionList() *sessionList { return s.sessionList }

// errorMessageOf renders an error message (pi: err instanceof Error ?
// err.message : String(err)).
func errorMessageOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
