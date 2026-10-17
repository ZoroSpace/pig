// tree_selector.go (pi: components/tree-selector.ts) — the /tree session
// tree selector: flattened tree navigation, filter modes, incremental
// search, branch folding, label editing with timestamps, copy, and
// horizontally clipped rendering with a fixed gutter.
//
// Declared deviations:
//   - pi reads raw message shapes via casts; pig type-switches the typed
//     message structs (types.UserMessage/AssistantMessage/ToolResultMessage,
//     harness.BashExecutionMessage). untypedMessage payloads render via the
//     generic "[role]" branch only (their content shape is unknown here).
//   - pi's `setTimeout(() => onCancel(), 100)` on an empty tree maps to a
//     time.AfterFunc goroutine.
package components

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// GutterInfo is the vertical gutter info: Position (displayIndent level
// where the connector was) and whether to show │ (pi: GutterInfo).
type GutterInfo struct {
	Position int // displayIndent level where the connector was shown
	Show     bool
}

// flatNode is a flattened tree node for navigation (pi: FlatNode).
type flatNode struct {
	node *core.SessionTreeNode
	// Indentation level (each level = 3 chars).
	indent int
	// Whether to show connector (├─ or └─) - true if parent has multiple children.
	showConnector bool
	// If showConnector, isLast = last sibling (└─), else not last (├─).
	isLast bool
	// Gutter info for each ancestor branch point.
	gutters []GutterInfo
	// True if this node is a root under a virtual branching root (multiple roots).
	isVirtualRootChild bool
}

// horizontalViewportRow is one row fed to renderHorizontalViewport (pi:
// HorizontalViewportRow).
type horizontalViewportRow struct {
	gutter     string
	body       string
	anchorCol  int
	bodyWidth  int
	isSelected bool
}

const (
	treeGutterWidth                 = 2
	minVisibleAnchorContentWidth    = 4
	maxVisibleAnchorContentWidth    = 20
	minAnchorContextWidth           = 2
	maxAnchorContextWidth           = 12
	treeIndentChars                 = 3
	treeEntryDisplayTextMaxLen      = 200
	treeToolCallArgsJSONMaxLen      = 40
	treeBashCommandDisplayMaxLen    = 50
	treeAssistantErrorDisplayMaxLen = 80
)

// renderHorizontalViewport renders tree rows into a horizontally clipped
// viewport (pi: renderHorizontalViewport). The tree gutter is always kept
// visible. The row bodies are shifted left only when the selected row's
// anchor (the start of its entry text after tree indentation/markers) would
// otherwise be too far right to see useful content.
func renderHorizontalViewport(rows []horizontalViewportRow, width int) []string {
	viewportWidth := maxInt(0, width-treeGutterWidth)
	maxBodyWidth := 0
	for _, row := range rows {
		if row.bodyWidth > maxBodyWidth {
			maxBodyWidth = row.bodyWidth
		}
	}
	maxHorizontalScroll := maxInt(0, maxBodyWidth-viewportWidth)
	var selectedRow *horizontalViewportRow
	for i := range rows {
		if rows[i].isSelected {
			selectedRow = &rows[i]
			break
		}
	}

	// Only pan horizontally when needed to keep enough selected-row content
	// visible after its anchor.
	horizontalScroll := 0
	if selectedRow != nil && maxHorizontalScroll > 0 {
		minVisibleAnchorContentWidth := minInt(
			maxVisibleAnchorContentWidth,
			maxInt(minVisibleAnchorContentWidth, viewportWidth/3),
		)
		if selectedRow.anchorCol > viewportWidth-minVisibleAnchorContentWidth {
			anchorContextWidth := minInt(
				maxAnchorContextWidth,
				maxInt(minAnchorContextWidth, viewportWidth/4),
			)
			horizontalScroll = minInt(maxHorizontalScroll, selectedRow.anchorCol-anchorContextWidth)
		}
	}

	// Clip only the body; the fixed-width gutter remains visible as navigation context.
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		var line string
		if horizontalScroll > 0 {
			line = fmt.Sprintf("%s%s\x1b[0m", row.gutter, tui.SliceByColumn(row.body, horizontalScroll, viewportWidth, true))
		} else {
			line = row.gutter + row.body
		}
		lines = append(lines, tui.TruncateToWidth(line, width, "", false))
	}
	return lines
}

// FilterMode is the filter mode for the tree display (pi: FilterMode).
type FilterMode string

const (
	FilterModeDefault     FilterMode = "default"
	FilterModeNoTools     FilterMode = "no-tools"
	FilterModeUserOnly    FilterMode = "user-only"
	FilterModeLabeledOnly FilterMode = "labeled-only"
	FilterModeAll         FilterMode = "all"
)

// filterModeCycle lists the modes in cycle order (pi: inline array).
var filterModeCycle = []FilterMode{FilterModeDefault, FilterModeNoTools, FilterModeUserOnly, FilterModeLabeledOnly, FilterModeAll}

// toolCallInfo is tool call info for lookup (pi: ToolCallInfo).
type toolCallInfo struct {
	name      string
	arguments map[string]any
}

// treeList is the tree list component with selection and ASCII art
// visualization (pi: TreeList implements Component).
type treeList struct {
	flatNodes           []*flatNode
	filteredNodes       []*flatNode
	selectedIndex       int
	currentLeafID       string
	maxVisibleLines     int
	filterMode          FilterMode
	searchQuery         string
	toolCallMap         map[string]toolCallInfo
	multipleRoots       bool
	showLabelTimestamps bool
	activePathIDs       map[string]bool
	// visibleParentMap maps a visible entry id to its nearest visible
	// ancestor ("" = root; entry ids are never empty) (pi: Map<string|null,...>).
	visibleParentMap map[string]string
	// visibleChildrenMap maps a parent id ("" = root bucket) to visible
	// child ids in filteredNodes order.
	visibleChildrenMap map[string][]string
	lastSelectedID     string
	foldedNodes        map[string]bool

	// OnSelect fires with the selected entry ID (pi: public onSelect?).
	OnSelect func(entryID string)
	// OnCancel fires on escape with an empty search query (pi: public onCancel?).
	OnCancel func()
	// OnCopy fires with the selected entry's copy text, "" = undefined (pi:
	// public onCopy?: (text: string | undefined) => void).
	OnCopy func(text string)
	// OnLabelEdit fires on the label key (pi: public onLabelEdit?;
	// currentLabel "" = undefined).
	OnLabelEdit func(entryID string, currentLabel string)
}

// newTreeList builds the tree list (pi: TreeList constructor).
// initialSelectedID and initialFilterMode optional (""/"").
func newTreeList(
	tree []*core.SessionTreeNode,
	currentLeafID string,
	maxVisibleLines int,
	initialSelectedID string,
	initialFilterMode FilterMode,
) *treeList {
	l := &treeList{
		selectedIndex:      0,
		currentLeafID:      currentLeafID,
		maxVisibleLines:    maxVisibleLines,
		filterMode:         FilterModeDefault,
		toolCallMap:        map[string]toolCallInfo{},
		activePathIDs:      map[string]bool{},
		visibleParentMap:   map[string]string{},
		visibleChildrenMap: map[string][]string{},
		foldedNodes:        map[string]bool{},
	}
	if initialFilterMode != "" {
		l.filterMode = initialFilterMode
	}
	l.multipleRoots = len(tree) > 1
	l.flatNodes = l.flattenTree(tree)
	l.buildActivePath()
	l.applyFilter()

	// Start with initialSelectedID if provided, otherwise current leaf
	targetID := initialSelectedID
	if targetID == "" {
		targetID = currentLeafID
	}
	l.selectedIndex = l.findNearestVisibleIndex(targetID)
	if l.selectedIndex < len(l.filteredNodes) {
		l.lastSelectedID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
	}
	return l
}

// entryID returns the entry id of a node.
func entryID(node *core.SessionTreeNode) string { return node.Entry.Base().ID }

// entryParentID returns the entry parent id ("" = none).
func entryParentID(node *core.SessionTreeNode) string {
	if p := node.Entry.Base().ParentID; p != nil {
		return *p
	}
	return ""
}

// findNearestVisibleIndex finds the index of the nearest visible entry,
// walking up the parent chain if needed. Returns the index in filteredNodes,
// or the last index as fallback.
func (l *treeList) findNearestVisibleIndex(entryID string) int {
	if len(l.filteredNodes) == 0 {
		return 0
	}

	// Build a map for parent lookup
	entryMap := map[string]*flatNode{}
	for _, flatNode := range l.flatNodes {
		entryMap[flatNode.node.Entry.Base().ID] = flatNode
	}

	// Build a map of visible entry IDs to their indices in filteredNodes
	visibleIDToIndex := map[string]int{}
	for i, node := range l.filteredNodes {
		visibleIDToIndex[node.node.Entry.Base().ID] = i
	}

	// Walk from entryId up to root, looking for a visible entry
	currentID := entryID
	for currentID != "" {
		if index, ok := visibleIDToIndex[currentID]; ok {
			return index
		}
		node, ok := entryMap[currentID]
		if !ok {
			break
		}
		currentID = entryParentID(node.node)
	}

	// Fallback: last visible entry
	return len(l.filteredNodes) - 1
}

// buildActivePath builds the set of entry IDs on the path from root to
// current leaf.
func (l *treeList) buildActivePath() {
	l.activePathIDs = map[string]bool{}
	if l.currentLeafID == "" {
		return
	}

	// Build a map of id -> entry for parent lookup
	entryMap := map[string]*flatNode{}
	for _, flatNode := range l.flatNodes {
		entryMap[flatNode.node.Entry.Base().ID] = flatNode
	}

	// Walk from leaf to root
	currentID := l.currentLeafID
	for currentID != "" {
		l.activePathIDs[currentID] = true
		node, ok := entryMap[currentID]
		if !ok {
			break
		}
		currentID = entryParentID(node.node)
	}
}

// flattenStackItem mirrors pi's stack tuple.
type flattenStackItem struct {
	node               *core.SessionTreeNode
	indent             int
	justBranched       bool
	showConnector      bool
	isLast             bool
	gutters            []GutterInfo
	isVirtualRootChild bool
}

// flattenTree flattens the tree into navigation order, prioritizing the
// branch containing the active leaf (pi: flattenTree).
//
// Indentation rules:
//   - At indent 0: stay at 0 unless parent has >1 children (then +1)
//   - At indent 1: children always go to indent 2 (visual grouping of subtree)
//   - At indent 2+: stay flat for single-child chains, +1 only if parent branches
func (l *treeList) flattenTree(roots []*core.SessionTreeNode) []*flatNode {
	result := []*flatNode{}
	l.toolCallMap = map[string]toolCallInfo{}

	// Determine which subtrees contain the active leaf (to sort current branch first)
	// Use iterative post-order traversal to avoid stack overflow
	containsActive := map[*core.SessionTreeNode]bool{}
	leafID := l.currentLeafID
	{
		// Build list in pre-order, then process in reverse for post-order effect
		var allNodes []*core.SessionTreeNode
		preOrderStack := append([]*core.SessionTreeNode{}, roots...)
		for len(preOrderStack) > 0 {
			node := preOrderStack[len(preOrderStack)-1]
			preOrderStack = preOrderStack[:len(preOrderStack)-1]
			allNodes = append(allNodes, node)
			// Push children in reverse so they're processed left-to-right
			for i := len(node.Children) - 1; i >= 0; i-- {
				preOrderStack = append(preOrderStack, node.Children[i])
			}
		}
		// Process in reverse (post-order): children before parents
		for i := len(allNodes) - 1; i >= 0; i-- {
			node := allNodes[i]
			has := leafID != "" && node.Entry.Base().ID == leafID
			for _, child := range node.Children {
				if containsActive[child] {
					has = true
				}
			}
			containsActive[node] = has
		}
	}

	// Add roots in reverse order, prioritizing the one containing the active leaf
	// If multiple roots, treat them as children of a virtual root that branches
	multipleRoots := len(roots) > 1
	orderedRoots := append([]*core.SessionTreeNode{}, roots...)
	sort.SliceStable(orderedRoots, func(a, b int) bool {
		// pi: Number(containsActive.get(b)) - Number(containsActive.get(a))
		return boolToInt(containsActive[orderedRoots[a]]) > boolToInt(containsActive[orderedRoots[b]])
	})
	var stack []flattenStackItem
	for i := len(orderedRoots) - 1; i >= 0; i-- {
		isLast := i == len(orderedRoots)-1
		stack = append(stack, flattenStackItem{
			node: orderedRoots[i], indent: boolToInt(multipleRoots),
			justBranched: multipleRoots, showConnector: multipleRoots,
			isLast: isLast, gutters: nil, isVirtualRootChild: multipleRoots,
		})
	}

	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node, indent, justBranched, showConnector, isLast, gutters, isVirtualRootChild :=
			item.node, item.indent, item.justBranched, item.showConnector, item.isLast, item.gutters, item.isVirtualRootChild

		// Extract tool calls from assistant messages for later lookup
		if entry := node.Entry; entry.EntryType() == "message" {
			if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
				if assistant, ok := msgEntry.Message.(types.AssistantMessage); ok {
					for _, block := range assistant.Content {
						if tc, ok := block.(types.ToolCall); ok {
							l.toolCallMap[tc.ID] = toolCallInfo{name: tc.Name, arguments: tc.Arguments}
						}
					}
				}
			}
		}

		result = append(result, &flatNode{
			node: node, indent: indent, showConnector: showConnector, isLast: isLast,
			gutters: gutters, isVirtualRootChild: isVirtualRootChild,
		})

		children := node.Children
		multipleChildren := len(children) > 1

		// Order children so the branch containing the active leaf comes first
		var prioritized, rest []*core.SessionTreeNode
		for _, child := range children {
			if containsActive[child] {
				prioritized = append(prioritized, child)
			} else {
				rest = append(rest, child)
			}
		}
		orderedChildren := append(append([]*core.SessionTreeNode{}, prioritized...), rest...)

		// Calculate child indent
		var childIndent int
		if multipleChildren {
			// Parent branches: children get +1
			childIndent = indent + 1
		} else if justBranched && indent > 0 {
			// First generation after a branch: +1 for visual grouping
			childIndent = indent + 1
		} else {
			// Single-child chain: stay flat
			childIndent = indent
		}

		// Build gutters for children
		// If this node showed a connector, add a gutter entry for descendants
		// Only add gutter if connector is actually displayed (not suppressed for virtual root children)
		connectorDisplayed := showConnector && !isVirtualRootChild
		// When connector is displayed, add a gutter entry at the connector's position
		// Connector is at position (displayIndent - 1), so gutter should be there too
		currentDisplayIndent := indent
		if l.multipleRoots {
			currentDisplayIndent = maxInt(0, indent-1)
		}
		connectorPosition := maxInt(0, currentDisplayIndent-1)
		childGutters := gutters
		if connectorDisplayed {
			childGutters = append(append([]GutterInfo{}, gutters...), GutterInfo{Position: connectorPosition, Show: !isLast})
		}

		// Add children in reverse order
		for i := len(orderedChildren) - 1; i >= 0; i-- {
			childIsLast := i == len(orderedChildren)-1
			stack = append(stack, flattenStackItem{
				node: orderedChildren[i], indent: childIndent,
				justBranched: multipleChildren, showConnector: multipleChildren,
				isLast: childIsLast, gutters: childGutters, isVirtualRootChild: false,
			})
		}
	}

	return result
}

// recalcStackItem mirrors pi's recalc stack tuple.
type recalcStackItem struct {
	nodeID             string
	indent             int
	justBranched       bool
	showConnector      bool
	isLast             bool
	gutters            []GutterInfo
	isVirtualRootChild bool
}

// applyFilter recomputes filteredNodes from flatNodes under the current
// filter mode / search query / fold state (pi: applyFilter).
func (l *treeList) applyFilter() {
	// Update lastSelectedId only when we have a valid selection (non-empty list)
	// This preserves the selection when switching through empty filter results
	if len(l.filteredNodes) > 0 && l.selectedIndex < len(l.filteredNodes) {
		l.lastSelectedID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
	}

	searchQuery := strings.ToLower(l.searchQuery)
	searchTokens := nonEmptyFields(searchQuery)

	filtered := []*flatNode{}
	for _, flatNode := range l.flatNodes {
		entry := flatNode.node.Entry
		// pi v0.86.0: usage entries never render in the tree.
		if entry.EntryType() == "usage" {
			continue
		}
		isCurrentLeaf := entry.Base().ID == l.currentLeafID

		// Skip assistant messages with only tool calls (no text) unless error/aborted
		// Always show current leaf so active position is visible
		if entry.EntryType() == "message" && !isCurrentLeaf {
			if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
				if assistant, ok := msgEntry.Message.(types.AssistantMessage); ok {
					hasText := hasTextContentMessage(assistant)
					isErrorOrAborted := assistant.StopReason != "" &&
						assistant.StopReason != "stop" && assistant.StopReason != "toolUse"
					// Only hide if no text AND not an error/aborted message
					if !hasText && !isErrorOrAborted {
						continue
					}
				}
			}
		}

		// Apply filter mode
		passesFilter := true
		// Entry types hidden in default view (settings/bookkeeping)
		isSettingsEntry := entry.EntryType() == "label" ||
			entry.EntryType() == "context_edit" ||
			entry.EntryType() == "custom" ||
			entry.EntryType() == "model_change" ||
			entry.EntryType() == "thinking_level_change" ||
			entry.EntryType() == "session_info"

		switch l.filterMode {
		case FilterModeUserOnly:
			// Just user messages
			passesFilter = false
			if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
				passesFilter = msgEntry.Message.MessageRole() == "user"
			}
		case FilterModeNoTools:
			// Default minus tool results
			passesFilter = !isSettingsEntry
			if msgEntry, ok := entry.(*core.SessionMessageEntry); ok {
				passesFilter = !isSettingsEntry && msgEntry.Message.MessageRole() != "toolResult"
			}
		case FilterModeLabeledOnly:
			// Just labeled entries
			passesFilter = flatNode.node.Label != nil
		case FilterModeAll:
			// Show everything
			passesFilter = true
		default:
			// Default mode: hide settings/bookkeeping entries
			passesFilter = !isSettingsEntry
		}

		if !passesFilter {
			continue
		}

		// Apply search filter
		if len(searchTokens) > 0 {
			nodeText := strings.ToLower(l.getSearchableText(flatNode.node))
			allMatch := true
			for _, token := range searchTokens {
				if !strings.Contains(nodeText, token) {
					allMatch = false
					break
				}
			}
			if !allMatch {
				continue
			}
		}

		filtered = append(filtered, flatNode)
	}
	l.filteredNodes = filtered

	// Filter out descendants of folded nodes.
	if len(l.foldedNodes) > 0 {
		skipSet := map[string]bool{}
		for _, flatNode := range l.flatNodes {
			id := flatNode.node.Entry.Base().ID
			parentID := entryParentID(flatNode.node)
			if parentID != "" && (l.foldedNodes[parentID] || skipSet[parentID]) {
				skipSet[id] = true
			}
		}
		kept := l.filteredNodes[:0]
		for _, flatNode := range l.filteredNodes {
			if !skipSet[flatNode.node.Entry.Base().ID] {
				kept = append(kept, flatNode)
			}
		}
		l.filteredNodes = kept
	}

	// Recalculate visual structure (indent, connectors, gutters) based on visible tree
	l.recalculateVisualStructure()

	// Try to preserve cursor on the same node, or find nearest visible ancestor
	if l.lastSelectedID != "" {
		l.selectedIndex = l.findNearestVisibleIndex(l.lastSelectedID)
	} else if l.selectedIndex >= len(l.filteredNodes) {
		// Clamp index if out of bounds
		l.selectedIndex = maxInt(0, len(l.filteredNodes)-1)
	}

	// Update lastSelectedId to the actual selection (may have changed due to parent walk)
	if len(l.filteredNodes) > 0 && l.selectedIndex < len(l.filteredNodes) {
		l.lastSelectedID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
	}
}

// recalculateVisualStructure recomputes indentation/connectors for the
// filtered view (pi: recalculateVisualStructure).
//
// Filtering can hide intermediate entries; descendants attach to the nearest
// visible ancestor. Keep indentation semantics aligned with flattenTree() so
// single-child chains don't drift right.
func (l *treeList) recalculateVisualStructure() {
	if len(l.filteredNodes) == 0 {
		return
	}

	visibleIDs := map[string]bool{}
	for _, n := range l.filteredNodes {
		visibleIDs[n.node.Entry.Base().ID] = true
	}

	// Build entry map for efficient parent lookup (using full tree)
	entryMap := map[string]*flatNode{}
	for _, flatNode := range l.flatNodes {
		entryMap[flatNode.node.Entry.Base().ID] = flatNode
	}

	// Build visible tree structure:
	// - visibleParent: nodeId → nearest visible ancestor ("" for roots)
	// - visibleChildren: parentId ("" = roots) → list of visible children (in filteredNodes order)
	visibleParent := map[string]string{}
	visibleChildren := map[string][]string{"": {}}

	findVisibleAncestor := func(nodeID string) string {
		currentID := ""
		if node, ok := entryMap[nodeID]; ok {
			currentID = entryParentID(node.node)
		}
		for currentID != "" {
			if visibleIDs[currentID] {
				return currentID
			}
			if node, ok := entryMap[currentID]; ok {
				currentID = entryParentID(node.node)
			} else {
				break
			}
		}
		return ""
	}

	for _, flatNode := range l.filteredNodes {
		nodeID := flatNode.node.Entry.Base().ID
		ancestorID := findVisibleAncestor(nodeID)
		visibleParent[nodeID] = ancestorID
		visibleChildren[ancestorID] = append(visibleChildren[ancestorID], nodeID)
	}

	// Update multipleRoots based on visible roots
	visibleRootIDs := visibleChildren[""]
	l.multipleRoots = len(visibleRootIDs) > 1

	// Build a map for quick lookup: nodeId → FlatNode
	filteredNodeMap := map[string]*flatNode{}
	for _, flatNode := range l.filteredNodes {
		filteredNodeMap[flatNode.node.Entry.Base().ID] = flatNode
	}

	// DFS over the visible tree using flattenTree() indentation semantics
	var stack []recalcStackItem
	// Add visible roots in reverse order (to process in forward order via stack)
	for i := len(visibleRootIDs) - 1; i >= 0; i-- {
		isLast := i == len(visibleRootIDs)-1
		stack = append(stack, recalcStackItem{
			nodeID:             visibleRootIDs[i],
			indent:             boolToInt(l.multipleRoots),
			justBranched:       l.multipleRoots,
			showConnector:      l.multipleRoots,
			isLast:             isLast,
			isVirtualRootChild: l.multipleRoots,
		})
	}

	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodeID, indent, justBranched, showConnector, isLast, gutters, isVirtualRootChild :=
			item.nodeID, item.indent, item.justBranched, item.showConnector, item.isLast, item.gutters, item.isVirtualRootChild

		flatNode, ok := filteredNodeMap[nodeID]
		if !ok {
			continue
		}

		// Update this node's visual properties
		flatNode.indent = indent
		flatNode.showConnector = showConnector
		flatNode.isLast = isLast
		flatNode.gutters = gutters
		flatNode.isVirtualRootChild = isVirtualRootChild

		// Get visible children of this node
		children := visibleChildren[nodeID]
		multipleChildren := len(children) > 1

		// Child indent follows flattenTree(): branch points (and first generation
		// after a branch) shift +1
		var childIndent int
		if multipleChildren {
			childIndent = indent + 1
		} else if justBranched && indent > 0 {
			childIndent = indent + 1
		} else {
			childIndent = indent
		}

		// Child gutters follow flattenTree() connector/gutter rules
		connectorDisplayed := showConnector && !isVirtualRootChild
		currentDisplayIndent := indent
		if l.multipleRoots {
			currentDisplayIndent = maxInt(0, indent-1)
		}
		connectorPosition := maxInt(0, currentDisplayIndent-1)
		childGutters := gutters
		if connectorDisplayed {
			childGutters = append(append([]GutterInfo{}, gutters...), GutterInfo{Position: connectorPosition, Show: !isLast})
		}

		// Add children in reverse order (to process in forward order via stack)
		for i := len(children) - 1; i >= 0; i-- {
			childIsLast := i == len(children)-1
			stack = append(stack, recalcStackItem{
				nodeID: children[i], indent: childIndent,
				justBranched: multipleChildren, showConnector: multipleChildren,
				isLast: childIsLast, gutters: childGutters, isVirtualRootChild: false,
			})
		}
	}

	// Store visible tree maps for ancestor/descendant lookups in navigation
	l.visibleParentMap = visibleParent
	l.visibleChildrenMap = visibleChildren
}

// getSearchableText gets searchable text content from a node (pi:
// getSearchableText).
func (l *treeList) getSearchableText(node *core.SessionTreeNode) string {
	entry := node.Entry
	var parts []string

	if node.Label != nil {
		parts = append(parts, *node.Label)
	}

	switch e := entry.(type) {
	case *core.SessionMessageEntry:
		parts = append(parts, string(e.Message.MessageRole()))
		switch msg := e.Message.(type) {
		case types.UserMessage:
			parts = append(parts, l.extractContent(msg.Content))
		case types.AssistantMessage:
			parts = append(parts, l.extractContent(msg.Content))
		case types.ToolResultMessage:
			parts = append(parts, l.extractContent(msg.Content))
		case harness.BashExecutionMessage:
			if msg.Command != "" {
				parts = append(parts, msg.Command)
			}
		}
	case *core.CustomMessageEntry:
		parts = append(parts, e.CustomType)
		parts = append(parts, l.extractContent(e.Content))
	case *core.CompactionEntry:
		parts = append(parts, "compaction")
	case *core.BranchSummaryEntry:
		parts = append(parts, "branch summary", e.Summary)
	case *core.SessionInfoEntry:
		parts = append(parts, "title")
		if e.Name != nil {
			parts = append(parts, *e.Name)
		}
	case *core.ModelChangeEntry:
		parts = append(parts, "model", e.ModelID)
	case *core.ThinkingLevelChangeEntry:
		parts = append(parts, "thinking", e.ThinkingLevel)
	case *core.CustomEntry:
		parts = append(parts, "custom", e.CustomType)
	case *core.ContextEditEntry:
		// pi: entry.replacement === null ? "omit" : "replace" — a hand-edited
		// entry without the replacement key decodes to nil ("omit") where pi's
		// undefined would read "replace"; pi's own writer always emits the key.
		replacement := "replace"
		if e.Replacement == nil {
			replacement = "omit"
		}
		parts = append(parts, "context edit", replacement, e.TargetID)
	case *core.LabelEntry:
		label := ""
		if e.Label != nil {
			label = *e.Label
		}
		parts = append(parts, "label", label)
	}

	return strings.Join(parts, " ")
}

// Invalidate implements Component.
func (l *treeList) Invalidate() {}

// GetSearchQuery returns the current search query (pi: getSearchQuery).
func (l *treeList) GetSearchQuery() string { return l.searchQuery }

// GetSelectedNode returns the selected node, nil when the list is empty (pi:
// getSelectedNode).
func (l *treeList) GetSelectedNode() *core.SessionTreeNode {
	if l.selectedIndex < len(l.filteredNodes) {
		return l.filteredNodes[l.selectedIndex].node
	}
	return nil
}

// CopySelected copies the selected entry (pi: copySelected).
func (l *treeList) CopySelected() {
	if l.OnCopy == nil {
		return
	}
	node := l.GetSelectedNode()
	text := ""
	if node != nil {
		text = l.getEntryCopyText(node)
	}
	l.OnCopy(text)
}

// UpdateNodeLabel updates a node's label in place (pi: updateNodeLabel).
// label nil = clear; labelTimestamp nil = now (when label set).
func (l *treeList) UpdateNodeLabel(entryID string, label *string, labelTimestamp *string) {
	for _, flatNode := range l.flatNodes {
		if flatNode.node.Entry.Base().ID == entryID {
			flatNode.node.Label = label
			if label != nil {
				if labelTimestamp != nil {
					flatNode.node.LabelTimestamp = labelTimestamp
				} else {
					now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
					flatNode.node.LabelTimestamp = &now
				}
			} else {
				flatNode.node.LabelTimestamp = nil
			}
			break
		}
	}
}

func (l *treeList) getStatusLabels() string {
	labels := ""
	switch l.filterMode {
	case FilterModeNoTools:
		labels += " [no-tools]"
	case FilterModeUserOnly:
		labels += " [user]"
	case FilterModeLabeledOnly:
		labels += " [labeled]"
	case FilterModeAll:
		labels += " [all]"
	}
	if l.showLabelTimestamps {
		labels += " [+label time]"
	}
	return labels
}

// Render implements Component.
func (l *treeList) Render(width int) []string {
	var lines []string

	if len(l.filteredNodes) == 0 {
		lines = append(lines, tui.TruncateToWidth(theme.Fg("muted", "  No entries found"), width, "", false))
		lines = append(lines, tui.TruncateToWidth(
			theme.Fg("muted", fmt.Sprintf("  (0/0)%s", l.getStatusLabels())), width, "", false))
		return lines
	}

	startIndex := maxInt(0, minInt(
		l.selectedIndex-l.maxVisibleLines/2,
		len(l.filteredNodes)-l.maxVisibleLines,
	))
	endIndex := minInt(startIndex+l.maxVisibleLines, len(l.filteredNodes))

	renderedRows := []horizontalViewportRow{}
	for i := startIndex; i < endIndex; i++ {
		flatNode := l.filteredNodes[i]
		entry := flatNode.node.Entry
		isSelected := i == l.selectedIndex

		// Build line: cursor + prefix + path marker + label + content
		cursor := "  "
		if isSelected {
			cursor = theme.Fg("accent", "› ")
		}

		// If multiple roots, shift display (roots at 0, not 1)
		displayIndent := flatNode.indent
		if l.multipleRoots {
			displayIndent = maxInt(0, flatNode.indent-1)
		}

		// Build prefix with gutters at their correct positions
		// Each gutter has a position (displayIndent where its connector was shown)
		connector := ""
		if flatNode.showConnector && !flatNode.isVirtualRootChild {
			if flatNode.isLast {
				connector = "└─ "
			} else {
				connector = "├─ "
			}
		}
		connectorPosition := -1
		if connector != "" {
			connectorPosition = displayIndent - 1
		}

		// Build prefix char by char, placing gutters and connector at their positions
		totalChars := displayIndent * treeIndentChars
		var prefixChars []string
		isFolded := l.foldedNodes[entry.Base().ID]
		for i := 0; i < totalChars; i++ {
			level := i / treeIndentChars
			posInLevel := i % treeIndentChars

			// Check if there's a gutter at this level
			var gutterAtLevel *GutterInfo
			for gi := range flatNode.gutters {
				if flatNode.gutters[gi].Position == level {
					gutterAtLevel = &flatNode.gutters[gi]
					break
				}
			}
			switch {
			case gutterAtLevel != nil:
				if posInLevel == 0 {
					if gutterAtLevel.Show {
						prefixChars = append(prefixChars, "│")
					} else {
						prefixChars = append(prefixChars, " ")
					}
				} else {
					prefixChars = append(prefixChars, " ")
				}
			case connector != "" && level == connectorPosition:
				// Connector at this level, with fold indicator
				if posInLevel == 0 {
					if flatNode.isLast {
						prefixChars = append(prefixChars, "└")
					} else {
						prefixChars = append(prefixChars, "├")
					}
				} else if posInLevel == 1 {
					foldable := l.isFoldable(entry.Base().ID)
					if isFolded {
						prefixChars = append(prefixChars, "⊞")
					} else if foldable {
						prefixChars = append(prefixChars, "⊟")
					} else {
						prefixChars = append(prefixChars, "─")
					}
				} else {
					prefixChars = append(prefixChars, " ")
				}
			default:
				prefixChars = append(prefixChars, " ")
			}
		}
		prefix := strings.Join(prefixChars, "")

		// Fold marker for nodes without connectors (roots)
		showsFoldInConnector := flatNode.showConnector && !flatNode.isVirtualRootChild
		foldMarker := ""
		if isFolded && !showsFoldInConnector {
			foldMarker = theme.Fg("accent", "⊞ ")
		}

		// Active path marker - shown right before the entry text
		pathMarker := ""
		if l.activePathIDs[entry.Base().ID] {
			pathMarker = theme.Fg("accent", "• ")
		}

		label := ""
		if flatNode.node.Label != nil {
			label = theme.Fg("warning", fmt.Sprintf("[%s] ", *flatNode.node.Label))
		}
		labelTimestamp := ""
		if l.showLabelTimestamps && flatNode.node.Label != nil && flatNode.node.LabelTimestamp != nil {
			labelTimestamp = theme.Fg("muted", fmt.Sprintf("%s ", l.formatLabelTimestamp(*flatNode.node.LabelTimestamp)))
		}
		content := l.getEntryDisplayText(flatNode.node, isSelected)
		prefixPart := theme.Fg("dim", prefix) + foldMarker + pathMarker
		anchorCol := tui.VisibleWidth(prefixPart)
		gutter := cursor
		body := prefixPart + label + labelTimestamp + content
		if isSelected {
			gutter = theme.Bg("selectedBg", gutter)
			body = theme.Bg("selectedBg", body)
		}
		renderedRows = append(renderedRows, horizontalViewportRow{
			gutter: gutter, body: body, anchorCol: anchorCol,
			bodyWidth: tui.VisibleWidth(body), isSelected: isSelected,
		})
	}

	lines = append(lines, renderHorizontalViewport(renderedRows, width)...)
	lines = append(lines, tui.TruncateToWidth(
		theme.Fg("muted", fmt.Sprintf("  (%d/%d)%s", l.selectedIndex+1, len(l.filteredNodes), l.getStatusLabels())),
		width, "", false))

	return lines
}

// getEntryDisplayText renders one entry's display text (pi:
// getEntryDisplayText).
func (l *treeList) getEntryDisplayText(node *core.SessionTreeNode, isSelected bool) string {
	entry := node.Entry
	var result string

	normalize := func(s string) string {
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\t' {
				return ' '
			}
			return r
		}, s))
	}

	switch e := entry.(type) {
	case *core.SessionMessageEntry:
		switch msg := e.Message.(type) {
		case types.UserMessage:
			content := normalize(l.extractContent(msg.Content))
			result = theme.Fg("accent", "user: ") + content
		case types.AssistantMessage:
			textContent := normalize(l.extractContent(msg.Content))
			if textContent != "" {
				result = theme.Fg("success", "assistant: ") + textContent
			} else if msg.StopReason == "aborted" {
				result = theme.Fg("success", "assistant: ") + theme.Fg("muted", "(aborted)")
			} else if msg.ErrorMessage != "" {
				errMsg := normalize(msg.ErrorMessage)
				if len(errMsg) > treeAssistantErrorDisplayMaxLen {
					errMsg = errMsg[:treeAssistantErrorDisplayMaxLen]
				}
				result = theme.Fg("success", "assistant: ") + theme.Fg("error", errMsg)
			} else {
				result = theme.Fg("success", "assistant: ") + theme.Fg("muted", "(no content)")
			}
		case types.ToolResultMessage:
			if toolCall, ok := l.toolCallMap[msg.ToolCallID]; ok {
				result = theme.Fg("muted", l.formatToolCall(toolCall.name, toolCall.arguments))
			} else {
				toolName := msg.ToolName
				if toolName == "" {
					toolName = "tool"
				}
				result = theme.Fg("muted", fmt.Sprintf("[%s]", toolName))
			}
		case harness.BashExecutionMessage:
			result = theme.Fg("dim", fmt.Sprintf("[bash]: %s", normalize(msg.Command)))
		default:
			result = theme.Fg("dim", fmt.Sprintf("[%s]", e.Message.MessageRole()))
		}
	case *core.CustomMessageEntry:
		content := ""
		switch c := e.Content.(type) {
		case string:
			content = c
		case []any:
			for _, block := range c {
				if m, ok := block.(map[string]any); ok && m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						content += text
					}
				}
			}
		}
		result = theme.Fg("customMessageLabel", fmt.Sprintf("[%s]: ", e.CustomType)) + normalize(content)
	case *core.CompactionEntry:
		tokens := (e.TokensBefore + 500) / 1000 // Math.round(x/1000) for positives
		result = theme.Fg("borderAccent", fmt.Sprintf("[compaction: %dk tokens]", tokens))
	case *core.BranchSummaryEntry:
		result = theme.Fg("warning", "[branch summary]: ") + normalize(e.Summary)
	case *core.ModelChangeEntry:
		result = theme.Fg("dim", fmt.Sprintf("[model: %s]", e.ModelID))
	case *core.ThinkingLevelChangeEntry:
		result = theme.Fg("dim", fmt.Sprintf("[thinking: %s]", e.ThinkingLevel))
	case *core.CustomEntry:
		result = theme.Fg("dim", fmt.Sprintf("[custom: %s]", e.CustomType))
	case *core.ContextEditEntry:
		replacement := "replace"
		if e.Replacement == nil {
			replacement = "omit"
		}
		result = theme.Fg("dim", fmt.Sprintf("[context %s: %s]", replacement, e.TargetID))
	case *core.LabelEntry:
		label := "(cleared)"
		if e.Label != nil {
			label = *e.Label
		}
		result = theme.Fg("dim", fmt.Sprintf("[label: %s]", label))
	case *core.SessionInfoEntry:
		if e.Name != nil {
			result = strings.Join([]string{theme.Fg("dim", "[title: "), theme.Fg("dim", *e.Name), theme.Fg("dim", "]")}, "")
		} else {
			result = strings.Join([]string{theme.Fg("dim", "[title: "), theme.Italic(theme.Fg("dim", "empty")), theme.Fg("dim", "]")}, "")
		}
	default:
		result = ""
	}

	if isSelected {
		result = theme.Bold(result)
	}
	return result
}

// formatLabelTimestamp renders a label timestamp in the local timezone (pi:
// formatLabelTimestamp).
func (l *treeList) formatLabelTimestamp(timestamp string) string {
	date, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}
	date = date.Local()
	now := time.Now()
	hours := fmt.Sprintf("%02d", date.Hour())
	minutes := fmt.Sprintf("%02d", date.Minute())
	timeStr := fmt.Sprintf("%s:%s", hours, minutes)

	if date.Year() == now.Year() && date.Month() == now.Month() && date.Day() == now.Day() {
		return timeStr
	}

	month := int(date.Month())
	day := date.Day()
	if date.Year() == now.Year() {
		return fmt.Sprintf("%d/%d %s", month, day, timeStr)
	}

	year := date.Year() % 100
	return fmt.Sprintf("%02d/%d/%d %s", year, month, day, timeStr)
}

// extractContent returns the first treeEntryDisplayTextMaxLen chars of the
// full text content (pi: extractContent).
func (l *treeList) extractContent(content any) string {
	full := extractFullContent(content)
	if len(full) > treeEntryDisplayTextMaxLen {
		return full[:treeEntryDisplayTextMaxLen]
	}
	return full
}

// extractFullContent concatenates text blocks from a string | UserContent |
// Content[] | generic JSON value (pi: extractFullContent).
func extractFullContent(content any) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	switch c := content.(type) {
	case types.UserContent:
		if c.Parts != nil {
			return extractFullContent(c.Parts)
		}
		return c.String
	case []types.Content:
		result := ""
		for _, block := range c {
			switch tc := block.(type) {
			case types.TextContent:
				result += tc.Text
			case *types.TextContent:
				result += tc.Text
			}
		}
		return result
	case []any:
		result := ""
		for _, block := range c {
			if m, ok := block.(map[string]any); ok && m["type"] == "text" {
				if text, ok := m["text"].(string); ok {
					result += text
				}
			}
		}
		return result
	}
	return ""
}

// getEntryCopyText returns the copyable text of an entry, "" = undefined
// (pi: getEntryCopyText).
func (l *treeList) getEntryCopyText(node *core.SessionTreeNode) string {
	entry := node.Entry
	var text string

	switch e := entry.(type) {
	case *core.SessionMessageEntry:
		switch msg := e.Message.(type) {
		case harness.BashExecutionMessage:
			text = msg.Command
		case types.UserMessage:
			text = extractFullContent(msg.Content)
		case types.AssistantMessage:
			text = extractFullContent(msg.Content)
			if text == "" {
				text = msg.ErrorMessage
			}
		case types.ToolResultMessage:
			text = extractFullContent(msg.Content)
		}
	case *core.CustomMessageEntry:
		text = extractFullContent(e.Content)
	case *core.CompactionEntry:
		text = e.Summary
	case *core.BranchSummaryEntry:
		text = e.Summary
	}

	if strings.TrimSpace(text) != "" {
		return text
	}
	return ""
}

// hasTextContentMessage reports whether the assistant message has any
// non-blank text content (pi: hasTextContent).
func hasTextContentMessage(msg types.AssistantMessage) bool {
	for _, c := range msg.Content {
		switch tc := c.(type) {
		case types.TextContent:
			if strings.TrimSpace(tc.Text) != "" {
				return true
			}
		case *types.TextContent:
			if strings.TrimSpace(tc.Text) != "" {
				return true
			}
		}
	}
	return false
}

func (l *treeList) formatToolCall(name string, args map[string]any) string {
	shortenPath := func(p string) string {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return p
		}
		if strings.HasPrefix(p, home) {
			return "~" + p[len(home):]
		}
		return p
	}

	argsString := func(key string) string {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	argsStringOr := func(keys ...string) string {
		for _, key := range keys {
			if v := argsString(key); v != "" {
				return v
			}
		}
		return ""
	}

	switch name {
	case "read":
		path := shortenPath(argsStringOr("path", "file_path"))
		return fmt.Sprintf("[read: %s%s]", path, coretools.FormatReadLineRange(args["offset"], args["limit"]))
	case "write":
		return fmt.Sprintf("[write: %s]", shortenPath(argsStringOr("path", "file_path")))
	case "edit":
		return fmt.Sprintf("[edit: %s]", shortenPath(argsStringOr("path", "file_path")))
	case "bash":
		rawCmd := argsString("command")
		cmd := strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '\n' || r == '\t' {
				return ' '
			}
			return r
		}, rawCmd))
		if len(cmd) > treeBashCommandDisplayMaxLen {
			cmd = cmd[:treeBashCommandDisplayMaxLen]
		}
		suffix := ""
		if len(rawCmd) > treeBashCommandDisplayMaxLen {
			suffix = "..."
		}
		return fmt.Sprintf("[bash: %s%s]", cmd, suffix)
	case "grep":
		return fmt.Sprintf("[grep: /%s/ in %s]", argsString("pattern"), shortenPath(argsStringOr("path", "")))
	case "find":
		return fmt.Sprintf("[find: %s in %s]", argsString("pattern"), shortenPath(argsStringOr("path", "")))
	case "ls":
		return fmt.Sprintf("[ls: %s]", shortenPath(argsStringOr("path", "")))
	default:
		// Custom tool - show name and truncated JSON args
		raw, err := json.Marshal(args)
		if err != nil {
			return fmt.Sprintf("[%s: ...]", name)
		}
		argsStr := string(raw)
		if len(argsStr) > treeToolCallArgsJSONMaxLen {
			argsStr = argsStr[:treeToolCallArgsJSONMaxLen]
		}
		suffix := ""
		if len(raw) > treeToolCallArgsJSONMaxLen {
			suffix = "..."
		}
		return fmt.Sprintf("[%s: %s%s]", name, argsStr, suffix)
	}
}

// HandleInput implements InputHandler (pi: handleInput).
func (l *treeList) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, "tui.select.up"):
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.filteredNodes) - 1
		} else {
			l.selectedIndex--
		}
	case kb.Matches(keyData, "tui.select.down"):
		if l.selectedIndex == len(l.filteredNodes)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	case kb.Matches(keyData, core.AppKeybindingTreeFoldOrUp):
		currentID := ""
		if l.selectedIndex < len(l.filteredNodes) {
			currentID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
		}
		if currentID != "" && l.isFoldable(currentID) && !l.foldedNodes[currentID] {
			l.foldedNodes[currentID] = true
			l.applyFilter()
		} else {
			l.selectedIndex = l.findBranchSegmentStart("up")
		}
	case kb.Matches(keyData, core.AppKeybindingTreeUnfoldOrDown):
		currentID := ""
		if l.selectedIndex < len(l.filteredNodes) {
			currentID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
		}
		if currentID != "" && l.foldedNodes[currentID] {
			delete(l.foldedNodes, currentID)
			l.applyFilter()
		} else {
			l.selectedIndex = l.findBranchSegmentStart("down")
		}
	case kb.Matches(keyData, "tui.editor.cursorLeft") || kb.Matches(keyData, "tui.select.pageUp"):
		// Page up
		l.selectedIndex = maxInt(0, l.selectedIndex-l.maxVisibleLines)
	case kb.Matches(keyData, "tui.editor.cursorRight") || kb.Matches(keyData, "tui.select.pageDown"):
		// Page down
		l.selectedIndex = minInt(len(l.filteredNodes)-1, l.selectedIndex+l.maxVisibleLines)
	case kb.Matches(keyData, "tui.select.confirm"):
		if l.selectedIndex < len(l.filteredNodes) && l.OnSelect != nil {
			l.OnSelect(l.filteredNodes[l.selectedIndex].node.Entry.Base().ID)
		}
	case kb.Matches(keyData, "app.message.copy"):
		l.CopySelected()
	case kb.Matches(keyData, "tui.select.cancel"):
		if l.searchQuery != "" {
			l.searchQuery = ""
			l.foldedNodes = map[string]bool{}
			l.applyFilter()
		} else if l.OnCancel != nil {
			l.OnCancel()
		}
	case kb.Matches(keyData, core.AppKeybindingTreeFilterDefault):
		// Direct filter: default
		l.filterMode = FilterModeDefault
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterNoTools):
		// Toggle filter: no-tools ↔ default
		if l.filterMode == FilterModeNoTools {
			l.filterMode = FilterModeDefault
		} else {
			l.filterMode = FilterModeNoTools
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterUserOnly):
		// Toggle filter: user-only ↔ default
		if l.filterMode == FilterModeUserOnly {
			l.filterMode = FilterModeDefault
		} else {
			l.filterMode = FilterModeUserOnly
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterLabeledOnly):
		// Toggle filter: labeled-only ↔ default
		if l.filterMode == FilterModeLabeledOnly {
			l.filterMode = FilterModeDefault
		} else {
			l.filterMode = FilterModeLabeledOnly
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterAll):
		// Toggle filter: all ↔ default
		if l.filterMode == FilterModeAll {
			l.filterMode = FilterModeDefault
		} else {
			l.filterMode = FilterModeAll
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterCycleBackward):
		// Cycle filter backwards
		for i, mode := range filterModeCycle {
			if mode == l.filterMode {
				l.filterMode = filterModeCycle[(i-1+len(filterModeCycle))%len(filterModeCycle)]
				break
			}
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, core.AppKeybindingTreeFilterCycleForward):
		// Cycle filter forwards: default → no-tools → user-only → labeled-only → all → default
		for i, mode := range filterModeCycle {
			if mode == l.filterMode {
				l.filterMode = filterModeCycle[(i+1)%len(filterModeCycle)]
				break
			}
		}
		l.foldedNodes = map[string]bool{}
		l.applyFilter()
	case kb.Matches(keyData, "tui.editor.deleteCharBackward"):
		if l.searchQuery != "" {
			runes := []rune(l.searchQuery)
			l.searchQuery = string(runes[:len(runes)-1])
			l.foldedNodes = map[string]bool{}
			l.applyFilter()
		}
	case kb.Matches(keyData, core.AppKeybindingTreeEditLabel):
		if l.selectedIndex < len(l.filteredNodes) && l.OnLabelEdit != nil {
			selected := l.filteredNodes[l.selectedIndex]
			currentLabel := ""
			if selected.node.Label != nil {
				currentLabel = *selected.node.Label
			}
			l.OnLabelEdit(selected.node.Entry.Base().ID, currentLabel)
		}
	case kb.Matches(keyData, core.AppKeybindingTreeToggleLabelTimestamp):
		l.showLabelTimestamps = !l.showLabelTimestamps
	default:
		hasControlChars := false
		for _, r := range keyData {
			if r < 32 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				hasControlChars = true
				break
			}
		}
		if !hasControlChars && len(keyData) > 0 {
			l.searchQuery += keyData
			l.foldedNodes = map[string]bool{}
			l.applyFilter()
		}
	}
}

// isFoldable reports whether a node can be folded. A node is foldable if it
// has visible children and is either a root (no visible parent) or a segment
// start (visible parent has multiple visible children).
func (l *treeList) isFoldable(entryID string) bool {
	children, ok := l.visibleChildrenMap[entryID]
	if !ok || len(children) == 0 {
		return false
	}
	parentID, ok := l.visibleParentMap[entryID]
	if !ok || parentID == "" {
		return true
	}
	siblings := l.visibleChildrenMap[parentID]
	return len(siblings) > 1
}

// findBranchSegmentStart finds the index of the next branch segment start in
// the given direction. A segment start is the first child of a branch point.
//
// "up" walks the visible parent chain; "down" walks visible children
// (always following the first child).
func (l *treeList) findBranchSegmentStart(direction string) int {
	selectedID := ""
	if l.selectedIndex < len(l.filteredNodes) {
		selectedID = l.filteredNodes[l.selectedIndex].node.Entry.Base().ID
	}
	if selectedID == "" {
		return l.selectedIndex
	}

	indexByEntryID := map[string]int{}
	for i, node := range l.filteredNodes {
		indexByEntryID[node.node.Entry.Base().ID] = i
	}
	currentID := selectedID
	if direction == "down" {
		for {
			children := l.visibleChildrenMap[currentID]
			if len(children) == 0 {
				return indexByEntryID[currentID]
			}
			if len(children) > 1 {
				return indexByEntryID[children[0]]
			}
			currentID = children[0]
		}
	}

	// direction === "up"
	for {
		parentID := l.visibleParentMap[currentID]
		if parentID == "" {
			return indexByEntryID[currentID]
		}
		children := l.visibleChildrenMap[parentID]
		if len(children) > 1 {
			segmentStart := indexByEntryID[currentID]
			if segmentStart < l.selectedIndex {
				return segmentStart
			}
		}
		currentID = parentID
	}
}

// searchLine displays the current search query (pi: SearchLine).
type searchLine struct {
	treeList *treeList
}

func (s *searchLine) Invalidate() {}

func (s *searchLine) Render(width int) []string {
	query := s.treeList.GetSearchQuery()
	if query != "" {
		return []string{tui.TruncateToWidth(
			"  "+theme.Fg("muted", "Type to search:")+" "+theme.Fg("accent", query), width, "", false)}
	}
	return []string{tui.TruncateToWidth("  "+theme.Fg("muted", "Type to search:"), width, "", false)}
}

func (s *searchLine) HandleInput(_ string) {}

// treeHelpItem is one help row entry (pi: TREE_HELP_ITEMS entry).
type treeHelpItem struct {
	keys       []tui.Keybinding
	label      string
	labelFirst bool
}

// treeHelp renders tree help as semantic rows with chunk-aware wrapping
// (pi: TreeHelp).
type treeHelp struct{}

func (h *treeHelp) Invalidate() {}

func (h *treeHelp) Render(width int) []string {
	items := make([]string, 0, len(treeHelpItems))
	for _, item := range treeHelpItems {
		text := formatTreeHelpKeys(item.keys)
		var rendered string
		if text == "" {
			rendered = item.label
		} else if item.labelFirst {
			rendered = item.label + " " + text
		} else {
			rendered = text + " " + item.label
		}
		items = append(items, rendered)
	}

	availableWidth := maxInt(1, width)
	indent := "  "
	separator := " · "
	var lines []string
	currentLine := ""

	for _, item := range items {
		var candidate string
		if currentLine != "" {
			candidate = currentLine + separator + item
		} else if tui.VisibleWidth(indent+item) <= availableWidth {
			candidate = indent + item
		} else {
			candidate = item
		}
		if currentLine == "" || tui.VisibleWidth(candidate) <= availableWidth {
			currentLine = candidate
			continue
		}

		lines = append(lines, tui.WrapTextWithAnsi(jsTrimEndLocal(currentLine), availableWidth)...)
		if tui.VisibleWidth(indent+item) <= availableWidth {
			currentLine = indent + item
		} else {
			currentLine = item
		}
	}

	if currentLine != "" {
		lines = append(lines, tui.WrapTextWithAnsi(jsTrimEndLocal(currentLine), availableWidth)...)
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, theme.Fg("muted", line))
	}
	return out
}

// jsTrimEndLocal trims trailing ECMAScript whitespace (pi: String#trimEnd;
// tui.jsTrimEnd is unexported).
func jsTrimEndLocal(s string) string {
	return strings.TrimRight(s, " \t\n\r\f\v\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}

var treeHelpItems = []treeHelpItem{
	{keys: []tui.Keybinding{"tui.select.up", "tui.select.down"}, label: "move"},
	{keys: []tui.Keybinding{"tui.editor.cursorLeft", "tui.editor.cursorRight"}, label: "page"},
	{keys: []tui.Keybinding{core.AppKeybindingTreeFoldOrUp, core.AppKeybindingTreeUnfoldOrDown}, label: "branch"},
	{keys: []tui.Keybinding{"app.message.copy"}, label: "copy"},
	{keys: []tui.Keybinding{core.AppKeybindingTreeEditLabel}, label: "label"},
	{keys: []tui.Keybinding{core.AppKeybindingTreeToggleLabelTimestamp}, label: "label time"},
	{
		keys: []tui.Keybinding{
			core.AppKeybindingTreeFilterDefault,
			core.AppKeybindingTreeFilterNoTools,
			core.AppKeybindingTreeFilterUserOnly,
			core.AppKeybindingTreeFilterLabeledOnly,
			core.AppKeybindingTreeFilterAll,
		},
		label:      "filters",
		labelFirst: true,
	},
	{
		keys:       []tui.Keybinding{core.AppKeybindingTreeFilterCycleForward, core.AppKeybindingTreeFilterCycleBackward},
		label:      "cycle",
		labelFirst: true,
	},
}

// formatTreeHelpKeys formats the first bound key of each keybinding (pi:
// formatHelpKeys).
func formatTreeHelpKeys(keybindings []tui.Keybinding) string {
	var keys []string
	for _, keybinding := range keybindings {
		bound := tui.GetKeybindings().GetKeys(keybinding)
		if len(bound) > 0 {
			keys = append(keys, bound[0])
		}
	}
	if len(keys) == 0 {
		return ""
	}

	text := coretools.FormatKeyText(compactRawKeys(keys), coretools.KeyTextFormatOptions{})
	text = strings.ReplaceAll(text, "pageUp", "pgup")
	text = strings.ReplaceAll(text, "pageDown", "pgdn")
	text = replaceWord(text, "up", "↑")
	text = replaceWord(text, "down", "↓")
	text = replaceWord(text, "left", "←")
	text = replaceWord(text, "right", "→")
	return text
}

// replaceWord replaces whole-word occurrences (pi: /\bup\b/g semantics).
func replaceWord(s, word, replacement string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		idx := strings.Index(s[i:], word)
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		at := i + idx
		before := byte(0)
		if at > 0 {
			before = s[at-1]
		}
		after := byte(0)
		if at+len(word) < len(s) {
			after = s[at+len(word)]
		}
		if isWordBoundaryByte(before) && isWordBoundaryByte(after) {
			b.WriteString(s[i:at])
			b.WriteString(replacement)
			i = at + len(word)
		} else {
			b.WriteString(s[i : at+len(word)])
			i = at + len(word)
		}
	}
	return b.String()
}

func isWordBoundaryByte(b byte) bool {
	return b == 0 || !isWordByte(b)
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// compactRawKeys compacts a raw key list sharing a modifier prefix (pi:
// compactRawKeys).
func compactRawKeys(keys []string) string {
	if len(keys) == 1 {
		return keys[0]
	}

	type keyParts struct {
		prefix string
		suffix string
	}
	parts := make([]keyParts, 0, len(keys))
	for _, key := range keys {
		separatorIndex := strings.LastIndex(key, "+")
		if separatorIndex == -1 {
			parts = append(parts, keyParts{prefix: "", suffix: key})
		} else {
			parts = append(parts, keyParts{prefix: key[:separatorIndex+1], suffix: key[separatorIndex+1:]})
		}
	}
	prefix := parts[0].prefix
	allSame := true
	for _, part := range parts {
		if part.prefix != prefix {
			allSame = false
			break
		}
	}
	if prefix != "" && allSame {
		suffixes := make([]string, 0, len(parts))
		for _, part := range parts {
			suffixes = append(suffixes, part.suffix)
		}
		return prefix + strings.Join(suffixes, "/")
	}
	return strings.Join(keys, "/")
}

// labelInput is the label input component shown when editing a label (pi:
// LabelInput implements Component, Focusable).
type labelInput struct {
	input   *tui.Input
	entryID string
	// OnSubmit fires with (entryID, label); label nil = remove (pi: public
	// onSubmit?: (entryId, label: string | undefined) => void).
	OnSubmit func(entryID string, label *string)
	// OnCancel fires on escape (pi: public onCancel?).
	OnCancel func()

	focused bool
}

// newLabelInput builds the label input (pi: constructor; currentLabel ""
// = undefined).
func newLabelInput(entryID string, currentLabel string) *labelInput {
	li := &labelInput{
		input:   tui.NewInput(),
		entryID: entryID,
	}
	if currentLabel != "" {
		li.input.SetValue(currentLabel)
	}
	return li
}

func (li *labelInput) Invalidate() {}

// GetFocused/SetFocused implement Focusable - propagate to input for IME
// cursor positioning.
func (li *labelInput) GetFocused() bool { return li.focused }
func (li *labelInput) SetFocused(value bool) {
	li.focused = value
	li.input.SetFocused(value)
}

func (li *labelInput) Render(width int) []string {
	var lines []string
	indent := "  "
	availableWidth := width - len(indent)
	lines = append(lines, tui.TruncateToWidth(indent+theme.Fg("muted", "Label (empty to remove):"), width, "", false))
	for _, line := range li.input.Render(availableWidth) {
		lines = append(lines, tui.TruncateToWidth(indent+line, width, "", false))
	}
	lines = append(lines, tui.TruncateToWidth(
		indent+coretools.KeyHint("tui.select.confirm", "save")+"  "+coretools.KeyHint("tui.select.cancel", "cancel"),
		width, "", false))
	return lines
}

func (li *labelInput) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, "tui.select.confirm") {
		value := strings.TrimSpace(li.input.GetValue())
		if li.OnSubmit != nil {
			if value == "" {
				li.OnSubmit(li.entryID, nil)
			} else {
				li.OnSubmit(li.entryID, &value)
			}
		}
	} else if kb.Matches(keyData, "tui.select.cancel") {
		if li.OnCancel != nil {
			li.OnCancel()
		}
	} else {
		li.input.HandleInput(keyData)
	}
}

// TreeSelectorComponent renders a session tree selector for navigation (pi:
// TreeSelectorComponent extends Container implements Focusable).
type TreeSelectorComponent struct {
	tui.Container

	treeList            *treeList
	labelInput          *labelInput
	labelInputContainer *tui.Container
	treeContainer       *tui.Container
	onLabelChange       func(entryID string, label *string)
	// OnCopy is the copy callback; text "" = undefined (pi: public onCopy?).
	OnCopy func(text string)

	focused bool
}

// NewTreeSelectorComponent builds the selector (pi: constructor).
// onLabelChange and initialSelectedID/initialFilterMode optional (nil/""/"").
func NewTreeSelectorComponent(
	tree []*core.SessionTreeNode,
	currentLeafID string,
	terminalHeight int,
	onSelect func(entryID string),
	onCancel func(),
	onLabelChange func(entryID string, label *string),
	initialSelectedID string,
	initialFilterMode FilterMode,
) *TreeSelectorComponent {
	s := &TreeSelectorComponent{
		Container:     tui.Container{},
		onLabelChange: onLabelChange,
	}
	maxVisibleLines := maxInt(5, terminalHeight/2)

	s.treeList = newTreeList(tree, currentLeafID, maxVisibleLines, initialSelectedID, initialFilterMode)
	s.treeList.OnSelect = onSelect
	s.treeList.OnCancel = onCancel
	s.treeList.OnCopy = func(text string) {
		if s.OnCopy != nil {
			s.OnCopy(text)
		}
	}
	s.treeList.OnLabelEdit = func(entryID string, currentLabel string) {
		s.showLabelInput(entryID, currentLabel)
	}

	s.treeContainer = &tui.Container{}
	s.treeContainer.AddChild(s.treeList)

	s.labelInputContainer = &tui.Container{}

	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewText(theme.Bold("  Session Tree"), 1, 0))
	s.AddChild(&treeHelp{})
	s.AddChild(&searchLine{treeList: s.treeList})
	s.AddChild(NewDynamicBorder(nil))
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(s.treeContainer)
	s.AddChild(s.labelInputContainer)
	s.AddChild(tui.NewSpacer(1))
	s.AddChild(NewDynamicBorder(nil))

	if len(tree) == 0 {
		time.AfterFunc(100*time.Millisecond, func() {
			if onCancel != nil {
				onCancel()
			}
		})
	}
	return s
}

// showLabelInput swaps the tree for the label editor (pi: showLabelInput).
func (s *TreeSelectorComponent) showLabelInput(entryID string, currentLabel string) {
	s.labelInput = newLabelInput(entryID, currentLabel)
	s.labelInput.OnSubmit = func(id string, label *string) {
		s.treeList.UpdateNodeLabel(id, label, nil)
		if s.onLabelChange != nil {
			s.onLabelChange(id, label)
		}
		s.hideLabelInput()
	}
	s.labelInput.OnCancel = func() { s.hideLabelInput() }

	// Propagate current focused state to the new labelInput
	s.labelInput.SetFocused(s.focused)

	s.treeContainer.Clear()
	s.labelInputContainer.Clear()
	s.labelInputContainer.AddChild(s.labelInput)
}

// hideLabelInput restores the tree view (pi: hideLabelInput).
func (s *TreeSelectorComponent) hideLabelInput() {
	s.labelInput = nil
	s.labelInputContainer.Clear()
	s.treeContainer.Clear()
	s.treeContainer.AddChild(s.treeList)
}

// HandleInput implements InputHandler.
func (s *TreeSelectorComponent) HandleInput(keyData string) {
	if s.labelInput != nil {
		s.labelInput.HandleInput(keyData)
	} else {
		s.treeList.HandleInput(keyData)
	}
}

// GetFocused/SetFocused implement Focusable - propagate to labelInput when
// active for IME cursor positioning.
func (s *TreeSelectorComponent) GetFocused() bool { return s.focused }
func (s *TreeSelectorComponent) SetFocused(value bool) {
	s.focused = value
	// Propagate to labelInput when it's active
	if s.labelInput != nil {
		s.labelInput.SetFocused(value)
	}
}

// GetTreeList exposes the inner list (pi: getTreeList).
func (s *TreeSelectorComponent) GetTreeList() *treeList { return s.treeList }

// ---------------------------------------------------------------------------
// Shared helpers (pig-local)
// ---------------------------------------------------------------------------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nonEmptyFields(s string) []string {
	out := []string{}
	for _, f := range strings.Fields(s) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
