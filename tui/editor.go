// tui/editor.go — ported from pi: packages/tui/src/components/editor.ts
//
// Declared deviations:
//   - UTF-16 code units → UTF-8 byte offsets throughout (repo convention).
//     Indices derived from grapheme operations stay grapheme-aligned; raw
//     length checks (e.g. large-paste char count) count bytes.
//   - Intl.Segmenter → uniseg (see word_navigation.go); word isWordLike is
//     approximated (see segmentIsWordLike).
//   - structuredClone → JSON round-trip clone of EditorSnapshot/EditorState.
//   - TS `${suffixGroup}` with an absent group renders the string "undefined";
//     the marker-renumber path replicates that quirk faithfully.
//   - **Concurrency adaptation** (pi relies on the JS event loop): the
//     editor state is guarded by a single mutex with reentrant dispatch
//     (lock/lockSync, TuiBase pattern) because the autocomplete request
//     machinery runs its continuation on a goroutine (pi: promise task
//     chain). Public entry points take e.lock (reentrant); the autocomplete
//     continuation and debounce-timer goroutines take e.lockSync (always
//     locks — they must never run inline). Render is intentionally
//     lock-free: it runs under the TUI mutex (pi: event-loop serialization;
//     T2 batch-1 status quo) and locking it would invert the lock order
//     against tui.RequestRender calls made under the editor lock. The
//     autocomplete continuation fires OnChange and tui.RequestRender after
//     releasing the lock for the same reason (pi fires onChange
//     synchronously).
//   - GetSuggestions runs off the lock; the editor passes it a stable copy
//     of the lines plus the cursor (pi reads live state on the event loop).
//     Staleness checks (token/requestId/text/cursor snapshot) discard any
//     result computed from outdated state, matching pi.

package tui

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ZoroSpace/pig/utils"
	"github.com/rivo/uniseg"
)

// pasteDecodeCsiURegex matches CSI-u Ctrl+<letter> sequences inside bracketed
// paste (pi: /\x1b\[(\d+);5u/g).
var pasteDecodeCsiURegex = regexp.MustCompile(`\x1b\[(\d+);5u`)

// pastePathPrefixRegex matches pi's /^[/~.]/.
var pastePathPrefixRegex = regexp.MustCompile(`^[/~.]`)

// pasteWordCharRegex matches pi's /\w/.
var pasteWordCharRegex = regexp.MustCompile(`[0-9A-Za-z_]`)

// sortInts sorts ascending (pi: .sort((a, b) => a - b)).
func sortInts(v []int) { sort.Ints(v) }

// Regex matching paste markers like `[paste #1 +123 lines]` or `[paste #2 1234 chars]`.
var pasteMarkerRegex = regexp.MustCompile(`\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]`)

// Non-global version for single-segment testing (pi: PASTE_MARKER_SINGLE, anchored).
var pasteMarkerSingle = regexp.MustCompile(`^\[paste #(\d+)( (\+\d+ lines|\d+ chars))?\]$`)

// Check if a segment is a paste marker (i.e. was merged by segmentWithMarkers).
func isPasteMarker(segment string) bool {
	return len(segment) >= 10 && pasteMarkerSingle.MatchString(segment)
}

// SegmenterFunc produces segments for text (the Go stand-in for passing an
// Intl.Segmenter around).
type SegmenterFunc func(text string) []SegmentData

// graphemeSegmentData segments text into grapheme clusters with byte indices
// (pi: Intl.Segmenter grapheme granularity; IsWordLike stays false, matching
// TS undefined).
func graphemeSegmentData(text string) []SegmentData {
	var out []SegmentData
	index := 0
	state := -1
	for len(text) > 0 {
		cluster, rest, _, newState := uniseg.FirstGraphemeClusterInString(text, state)
		out = append(out, SegmentData{Segment: cluster, Index: index})
		index += len(cluster)
		text = rest
		state = newState
	}
	return out
}

// segmentWithMarkers wraps a segmenter and merges graphemes that fall within
// paste markers into single atomic segments. This makes cursor movement,
// deletion, word-wrap, etc. treat paste markers as single units.
//
// Only markers whose numeric ID exists in validIds are merged.
func segmentWithMarkers(text string, baseSegmenter SegmenterFunc, validIds map[int]bool) []SegmentData {
	// Fast path: no paste markers in the text or no valid IDs.
	if len(validIds) == 0 || !strings.Contains(text, "[paste #") {
		return baseSegmenter(text)
	}

	// Find all marker spans with valid IDs.
	type markerSpan struct{ start, end int }
	var markers []markerSpan
	for _, m := range pasteMarkerRegex.FindAllStringSubmatchIndex(text, -1) {
		id, err := strconv.Atoi(text[m[2]:m[3]])
		if err != nil || !validIds[id] {
			continue
		}
		markers = append(markers, markerSpan{start: m[0], end: m[1]})
	}
	if len(markers) == 0 {
		return baseSegmenter(text)
	}

	// Build merged segment list.
	baseSegments := baseSegmenter(text)
	result := make([]SegmentData, 0, len(baseSegments))
	markerIdx := 0

	for _, seg := range baseSegments {
		// Skip past markers that are entirely before this segment.
		for markerIdx < len(markers) && markers[markerIdx].end <= seg.Index {
			markerIdx++
		}

		var marker *markerSpan
		if markerIdx < len(markers) {
			marker = &markers[markerIdx]
		}

		if marker != nil && seg.Index >= marker.start && seg.Index < marker.end {
			// This segment falls inside a marker.
			// If this is the first segment of the marker, emit a merged segment.
			if seg.Index == marker.start {
				markerText := text[marker.start:marker.end]
				result = append(result, SegmentData{Segment: markerText, Index: marker.start})
			}
			// Otherwise skip (already merged into the first segment).
		} else {
			result = append(result, seg)
		}
	}

	return result
}

// TextChunk represents a chunk of text for word-wrap layout.
// Tracks both the text content and its position in the original line.
type TextChunk struct {
	Text       string
	StartIndex int
	EndIndex   int
}

// WordWrapLine splits a line into word-wrapped chunks.
// Wraps at word boundaries when possible, falling back to character-level
// wrapping for words longer than the available width.
//
//	line - The text line to wrap
//	maxWidth - Maximum visible width per chunk
//	preSegmented - Optional pre-segmented graphemes (e.g. with paste-marker
//	  awareness). When nil the default grapheme segmenter is used.
//
// Returns chunks with text and position information (byte offsets).
func WordWrapLine(line string, maxWidth int, preSegmented []SegmentData) []TextChunk {
	if line == "" || maxWidth <= 0 {
		return []TextChunk{{Text: "", StartIndex: 0, EndIndex: 0}}
	}

	lineWidth := VisibleWidth(line)
	if lineWidth <= maxWidth {
		return []TextChunk{{Text: line, StartIndex: 0, EndIndex: len(line)}}
	}

	var chunks []TextChunk
	segments := preSegmented
	if segments == nil {
		segments = graphemeSegmentData(line)
	}

	currentWidth := 0
	chunkStart := 0

	// Wrap opportunity: the position after the last whitespace before a non-whitespace
	// grapheme, i.e. where a line break is allowed.
	wrapOppIndex := -1
	wrapOppWidth := 0

	for i := 0; i < len(segments); i++ {
		seg := segments[i]
		grapheme := seg.Segment
		gWidth := VisibleWidth(grapheme)
		charIndex := seg.Index
		isWs := !isPasteMarker(grapheme) && grapheme != "" && IsWhitespaceChar(grapheme)

		// Overflow check before advancing.
		if currentWidth+gWidth > maxWidth {
			if wrapOppIndex >= 0 && currentWidth-wrapOppWidth+gWidth <= maxWidth {
				// Backtrack to last wrap opportunity (the remaining content
				// plus the current grapheme still fits within maxWidth).
				chunks = append(chunks, TextChunk{Text: line[chunkStart:wrapOppIndex], StartIndex: chunkStart, EndIndex: wrapOppIndex})
				chunkStart = wrapOppIndex
				currentWidth -= wrapOppWidth
			} else if chunkStart < charIndex {
				// No viable wrap opportunity: force-break at current position.
				// This also handles the case where backtracking to a word
				// boundary wouldn't help because the remaining content plus
				// the current grapheme (e.g. a wide character) still exceeds
				// maxWidth.
				chunks = append(chunks, TextChunk{Text: line[chunkStart:charIndex], StartIndex: chunkStart, EndIndex: charIndex})
				chunkStart = charIndex
				currentWidth = 0
			}
			wrapOppIndex = -1
		}

		if gWidth > maxWidth {
			// Single atomic segment wider than maxWidth (e.g. paste marker
			// in a narrow terminal). Re-wrap it at grapheme granularity.

			// The segment remains logically atomic for cursor
			// movement / editing — the split is purely visual for word-wrap layout.
			subChunks := WordWrapLine(grapheme, maxWidth, nil)
			for j := 0; j < len(subChunks)-1; j++ {
				sc := subChunks[j]
				chunks = append(chunks, TextChunk{Text: sc.Text, StartIndex: charIndex + sc.StartIndex, EndIndex: charIndex + sc.EndIndex})
			}
			last := subChunks[len(subChunks)-1]
			chunkStart = charIndex + last.StartIndex
			currentWidth = VisibleWidth(last.Text)
			wrapOppIndex = -1
			continue
		}

		// Advance.
		currentWidth += gWidth

		// Record wrap opportunity: whitespace followed by non-whitespace
		// (multiple spaces join; the break point is after the last space),
		// or at a boundary where either side is CJK (CJK allows breaking
		// between any adjacent characters).
		var next *SegmentData
		if i+1 < len(segments) {
			next = &segments[i+1]
		}
		if isWs && next != nil && (isPasteMarker(next.Segment) || !IsWhitespaceChar(next.Segment)) {
			wrapOppIndex = next.Index
			wrapOppWidth = currentWidth
		} else if !isWs && next != nil && !IsWhitespaceChar(next.Segment) {
			isCjk := !isPasteMarker(grapheme) && isCJKBreak(grapheme)
			nextIsCjk := !isPasteMarker(next.Segment) && isCJKBreak(next.Segment)
			if isCjk || nextIsCjk {
				wrapOppIndex = next.Index
				wrapOppWidth = currentWidth
			}
		}
	}

	// Push final chunk.
	chunks = append(chunks, TextChunk{Text: line[chunkStart:], StartIndex: chunkStart, EndIndex: len(line)})

	return chunks
}

// editorState is the editor text state (pi: EditorState).
type editorState struct {
	Lines      []string
	CursorLine int
	CursorCol  int
}

// editorSnapshot is the undo snapshot: editor text state plus the paste registry.
type editorSnapshot struct {
	State        editorState
	Pastes       map[int]string
	PasteCounter int
}

// layoutLine is one rendered row of the editor (pi: LayoutLine).
type layoutLine struct {
	Text      string
	HasCursor bool
	// CursorPos is the byte offset of the cursor within Text. Always set when
	// HasCursor is true (pi: cursorPos?, but every HasCursor push sets it).
	CursorPos int
}

// EditorTheme holds editor appearance hooks.
type EditorTheme struct {
	BorderColor func(text string) string
	// SelectList colors the autocomplete dropdown (pi: selectList).
	SelectList SelectListTheme
}

// EditorOptions configures the editor.
type EditorOptions struct {
	PaddingX int
	// AutocompleteMaxVisible clamps the autocomplete dropdown size (pi:
	// autocompleteMaxVisible?, default 5; explicit 0 clamps to 3, so the
	// pointer distinguishes unset from zero).
	AutocompleteMaxVisible *int
}

// SLASH_COMMAND_SELECT_LIST_LAYOUT is the select-list layout used by the
// slash-command autocomplete menu (pi: SLASH_COMMAND_SELECT_LIST_LAYOUT).
var slashCommandSelectListLayout = &SelectListLayoutOptions{
	MaxPrimaryColumnWidth: 32,
}

// attachmentAutocompleteDebounceMs is the debounce delay for attachment (@)
// autocomplete requests (pi: ATTACHMENT_AUTOCOMPLETE_DEBOUNCE_MS).
const attachmentAutocompleteDebounceMs = 20

// defaultAutocompleteTriggerCharacters is the default trigger character set
// (pi: DEFAULT_AUTOCOMPLETE_TRIGGER_CHARACTERS).
var defaultAutocompleteTriggerCharacters = []string{"@", "#"}

// escapeCharacterClass escapes a character for use inside a regex character
// class (pi: escapeCharacterClass).
func escapeCharacterClass(value string) string {
	return regexp.MustCompile(`[\\^$.*+?()\[\]{}|\-]`).ReplaceAllStringFunc(value, func(match string) string {
		return "\\" + match
	})
}

// buildTriggerPattern builds the natural-trigger pattern (pi:
// buildTriggerPattern): (?:^|[\s])[chars([^\s]*$.
func buildTriggerPattern(triggerCharacters []string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[\s])[` + escapeCharacterClass(strings.Join(triggerCharacters, "")) + `][^\s]*$`)
}

// buildDebouncePattern builds the debounce pattern (pi: buildDebouncePattern):
// attachment (@) tokens debounce, other triggers do not.
func buildDebouncePattern(triggerCharacters []string) *regexp.Regexp {
	var escapedWithoutAt []string
	for _, character := range triggerCharacters {
		if character != "@" {
			escapedWithoutAt = append(escapedWithoutAt, escapeCharacterClass(character))
		}
	}
	return regexp.MustCompile(`(?:^|[ \t])(?:@(?:"[^"]*|[^\s]*)|[` + strings.Join(escapedWithoutAt, "") + `][^\s]*)$`)
}

// createScrollBorder renders a scroll indicator line (v0.85.1: centered
// label when the width admits it, legacy left-aligned indicator otherwise).
func createScrollBorder(direction string, hiddenLineCount int, width int) string {
	availableWidth := width
	if availableWidth < 0 {
		availableWidth = 0
	}
	label := fmt.Sprintf(" %s %d more ", direction, hiddenLineCount)
	labelWidth := VisibleWidth(label)
	if labelWidth+2 <= availableWidth {
		leftWidth := (availableWidth - labelWidth) / 2
		return strings.Repeat("─", leftWidth) + label + strings.Repeat("─", availableWidth-leftWidth-labelWidth)
	}

	indicator := fmt.Sprintf("─── %s %d more ", direction, hiddenLineCount)
	remaining := availableWidth - VisibleWidth(indicator)
	if remaining >= 0 {
		return indicator + strings.Repeat("─", remaining)
	}

	ellipsis := "..."
	if len(ellipsis) > availableWidth {
		ellipsis = ellipsis[:availableWidth]
	}
	indicatorWidth := availableWidth - VisibleWidth(ellipsis)
	return SliceByColumn(indicator, 0, indicatorWidth, true) + ellipsis
}

// visualLine maps a wrapped visual row back to logical positions
// (pi: the { logicalLine, startCol, length } entries of buildVisualLineMap).
type visualLine struct {
	LogicalLine int
	StartCol    int
	Length      int
}

// Editor is a multi-line text editor component (pi: Editor class).
// It implements Component, InputHandler and Focusable.
type Editor struct {
	// State is the editor document state (pi: private state; exported name
	// avoided — use accessors).
	state editorState

	// Focused is the Focusable flag - set by TUI when focus changes.
	Focused bool

	tui       TUI
	theme     EditorTheme
	paddingX  int
	lastWidth int // last render width for cursor navigation

	// Render geometry for cursor navigation and mouse hit-testing (pi:
	// renderedVisibleLineCount / renderedAutocompleteHeight).
	renderedVisibleLineCount   int
	renderedAutocompleteHeight int

	// mu guards editor state against the autocomplete request machinery
	// (see file header "Concurrency adaptation").
	mu sync.Mutex
	// lockOwner tracks the goroutine currently holding mu, for reentrant
	// lock() calls (see goid.go).
	lockOwner reentrantLockOwner

	// scrollOffset is the vertical scrolling support.
	scrollOffset int

	// BorderColor can be changed dynamically (pi: public borderColor).
	BorderColor func(text string) string

	// TopBorderOverride optionally replaces the default top border (pig
	// addition: pi overrides protected renderTopBorder in CustomEditor; Go
	// embedding has no virtual dispatch, so the override is injected as a
	// callback. Return ok=false to fall through to the default border —
	// that is the equivalent of calling super.renderTopBorder).
	TopBorderOverride func(width int, hiddenLineCount int) (string, bool)

	// Autocomplete support (pi: autocompleteProvider family).
	autocompleteProvider          AutocompleteProvider
	autocompleteTriggerCharacters []string
	autocompleteTriggerPattern    *regexp.Regexp
	autocompleteDebouncePattern   *regexp.Regexp
	autocompleteList              *SelectList
	autocompleteState             string // "" | "regular" | "force"
	autocompletePrefix            string
	autocompleteMaxVisible        int
	autocompleteAbort             *editorAbortController
	autocompleteDebounceTimer     *time.Timer
	autocompleteRequestTask       chan struct{} // nil = settled (pi: resolved promise)
	autocompleteStartToken        int
	autocompleteRequestId         int

	// pastes tracks large pastes for marker expansion.
	pastes       map[int]string
	pasteCounter int

	// pasteBuffer/isInPaste implement bracketed paste mode buffering.
	pasteBuffer string
	isInPaste   bool

	// history holds prompt history for up/down navigation.
	history      []string
	historyIndex int // -1 = not browsing, 0 = most recent, 1 = older, etc.
	historyDraft *editorState

	// killRing for Emacs-style kill/yank operations.
	killRing   KillRing
	lastAction string // "kill" | "yank" | "type-word" | ""

	// jumpMode is the character jump mode ("forward" | "backward" | "").
	jumpMode string

	// preferredVisualCol is the sticky column for vertical cursor movement.
	preferredVisualCol *int

	// snappedFromCursorCol: when the cursor is snapped to the start of an
	// atomic segment, e.g. a paste marker, cursorCol no longer reflects where
	// the cursor would have landed. This field stores the pre-snap cursorCol
	// so that the next vertical move can resolve it to a visual column on
	// whatever VL it belongs to.
	snappedFromCursorCol *int

	// undoStack supports undo/redo.
	undoStack *UndoStack[editorSnapshot]

	// OnSubmit is called when user submits (e.g. Enter).
	OnSubmit func(text string)
	// OnChange is called when text changes.
	OnChange func(text string)
	// DisableSubmit suppresses submission.
	DisableSubmit bool
}

// GetFocused reports the current focus state (pi: Focusable.focused property).
func (e *Editor) GetFocused() bool { return e.Focused }

// SetFocused is called by the TUI when focus changes.
func (e *Editor) SetFocused(focused bool) { e.Focused = focused }

// NewEditor creates an editor bound to tui (pi: constructor; options is
// optional — pass nil for defaults).
func NewEditor(tui TUI, theme EditorTheme, options *EditorOptions) *Editor {
	e := &Editor{
		state: editorState{Lines: []string{""}},
		tui:   tui,
		theme: theme,
	}
	e.BorderColor = theme.BorderColor
	paddingX := 0
	if options != nil {
		paddingX = options.PaddingX
	}
	// pi: Number.isFinite(paddingX) ? Math.max(0, Math.floor(paddingX)) : 0 —
	// Go ints are always finite, so only the clamp remains.
	if paddingX < 0 {
		paddingX = 0
	}
	e.paddingX = paddingX
	e.historyIndex = -1 // pi: -1 = not browsing
	e.lastWidth = 80    // pi: initial lastWidth
	e.pastes = make(map[int]string)
	e.killRing = KillRing{}
	e.undoStack = NewUndoStack(cloneEditorSnapshot)
	e.autocompleteTriggerCharacters = append([]string{}, defaultAutocompleteTriggerCharacters...)
	e.autocompleteTriggerPattern = buildTriggerPattern(e.autocompleteTriggerCharacters)
	e.autocompleteDebouncePattern = buildDebouncePattern(e.autocompleteTriggerCharacters)
	e.autocompleteMaxVisible = 5 // pi: options.autocompleteMaxVisible ?? 5
	if options != nil && options.AutocompleteMaxVisible != nil {
		// pi: Number.isFinite(maxVisible) ? Math.max(3, Math.min(20, Math.floor(maxVisible))) : 5
		e.autocompleteMaxVisible = clampAutocompleteMaxVisible(*options.AutocompleteMaxVisible)
	}
	return e
}

// clampAutocompleteMaxVisible clamps the dropdown size (pi: Number.isFinite
// check + Math.max(3, Math.min(20, Math.floor(maxVisible)))).
func clampAutocompleteMaxVisible(maxVisible int) int {
	if maxVisible < 3 {
		return 3
	}
	if maxVisible > 20 {
		return 20
	}
	return maxVisible
}

// lock runs fn under the editor mutex. When the calling goroutine is already
// inside a locked editor entry point, fn runs inline — reproducing JS
// event-loop reentrancy (TuiBase pattern).
func (e *Editor) lock(fn func()) {
	if e.lockOwner.isCurrent() {
		fn()
		return
	}
	e.mu.Lock()
	e.lockOwner.acquire()
	fn()
	e.lockOwner.release()
	e.mu.Unlock()
}

// lockSync always acquires the editor mutex. Used by the autocomplete
// continuation and debounce-timer goroutines: they must never take the
// inline path (the lock owner is another goroutine; see file
// header "Concurrency adaptation").
func (e *Editor) lockSync(fn func()) {
	e.mu.Lock()
	fn()
	e.mu.Unlock()
}

func cloneEditorState(s editorState) editorState {
	lines := make([]string, len(s.Lines))
	copy(lines, s.Lines)
	return editorState{Lines: lines, CursorLine: s.CursorLine, CursorCol: s.CursorCol}
}

func cloneEditorSnapshot(s editorSnapshot) editorSnapshot {
	// pi: structuredClone — JSON round-trip is exact for this shape.
	clone := cloneEditorState(s.State)
	pastes := make(map[int]string, len(s.Pastes))
	for id, content := range s.Pastes {
		pastes[id] = content
	}
	return editorSnapshot{State: clone, Pastes: pastes, PasteCounter: s.PasteCounter}
}

// validPasteIds returns the set of currently valid paste IDs, for
// marker-aware segmentation.
func (e *Editor) validPasteIds() map[int]bool {
	ids := make(map[int]bool, len(e.pastes))
	for id := range e.pastes {
		ids[id] = true
	}
	return ids
}

// segment segments text with paste-marker awareness, only merging markers
// with valid IDs. mode is "word" or "grapheme".
func (e *Editor) segment(text string, mode string) []SegmentData {
	base := graphemeSegmentData
	if mode == "word" {
		base = wordSegmentData
	}
	return segmentWithMarkers(text, base, e.validPasteIds())
}

// GetPaddingX returns the horizontal padding.
func (e *Editor) GetPaddingX() int { return e.paddingX }

// SetPaddingX sets the horizontal padding.
func (e *Editor) SetPaddingX(padding int) {
	if padding < 0 {
		padding = 0
	}
	if e.paddingX != padding {
		e.paddingX = padding
		e.tui.RequestRender(false)
	}
}

// GetAutocompleteProvider returns the installed provider, or nil (pig
// addition for the interactive-mode tests; pi reads the private field from
// the same package).
func (e *Editor) GetAutocompleteProvider() AutocompleteProvider { return e.autocompleteProvider }

// GetAutocompleteMaxVisible returns the dropdown size (pi:
// getAutocompleteMaxVisible).
func (e *Editor) GetAutocompleteMaxVisible() int {
	var maxVisible int
	e.lock(func() { maxVisible = e.autocompleteMaxVisible })
	return maxVisible
}

// SetAutocompleteMaxVisible sets the dropdown size (pi:
// setAutocompleteMaxVisible).
func (e *Editor) SetAutocompleteMaxVisible(maxVisible int) {
	newMaxVisible := clampAutocompleteMaxVisible(maxVisible) // Go ints are always finite
	e.lock(func() {
		if e.autocompleteMaxVisible != newMaxVisible {
			e.autocompleteMaxVisible = newMaxVisible
			e.tui.RequestRender(false)
		}
	})
}

// SetAutocompleteProvider installs the autocomplete provider (pi:
// setAutocompleteProvider).
func (e *Editor) SetAutocompleteProvider(provider AutocompleteProvider) {
	e.lock(func() {
		e.cancelAutocomplete()
		e.autocompleteProvider = provider
		triggerCharacters := []string{}
		if tc, ok := provider.(TriggerCharactersProvider); ok {
			triggerCharacters = tc.TriggerCharacters()
		}
		e.setAutocompleteTriggerCharacters(triggerCharacters)
	})
}

// AddToHistory adds a prompt to history for up/down arrow navigation.
// Called after successful submission.
func (e *Editor) AddToHistory(text string) {
	trimmed := jsTrim(text)
	if trimmed == "" {
		return
	}
	// Don't add consecutive duplicates
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	// Limit history size
	if len(e.history) > 100 {
		e.history = e.history[:len(e.history)-1]
	}
}

func (e *Editor) isEditorEmpty() bool {
	return len(e.state.Lines) == 1 && e.state.Lines[0] == ""
}

func (e *Editor) isOnFirstVisualLine() bool {
	visualLines := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(visualLines) == 0
}

func (e *Editor) isOnLastVisualLine() bool {
	visualLines := e.buildVisualLineMap(e.lastWidth)
	return e.findCurrentVisualLine(visualLines) == len(visualLines)-1
}

func (e *Editor) navigateHistory(direction int) {
	e.lastAction = ""
	if len(e.history) == 0 {
		return
	}

	newIndex := e.historyIndex - direction // Up(-1) increases index, Down(1) decreases
	if newIndex < -1 || newIndex >= len(e.history) {
		return
	}

	// Capture state when first entering history browsing mode
	if e.historyIndex == -1 && newIndex >= 0 {
		e.pushUndoSnapshot()
		draft := cloneEditorState(e.state)
		e.historyDraft = &draft
	}

	e.historyIndex = newIndex

	if e.historyIndex == -1 {
		draft := e.historyDraft
		e.historyDraft = nil
		if draft != nil {
			e.state = *draft
			e.preferredVisualCol = nil
			e.snappedFromCursorCol = nil
			e.scrollOffset = 0
			if e.OnChange != nil {
				e.OnChange(e.GetText())
			}
		} else {
			e.setTextInternal("", "end")
		}
	} else {
		text := ""
		if e.historyIndex < len(e.history) {
			text = e.history[e.historyIndex]
		}
		placement := "end"
		if direction == -1 {
			placement = "start"
		}
		e.setTextInternal(text, placement)
	}
}

func (e *Editor) exitHistoryBrowsing() {
	e.historyIndex = -1
	e.historyDraft = nil
}

// setTextInternal is the internal setText that doesn't reset history state -
// used by navigateHistory. cursorPlacement is "start" or "end".
func (e *Editor) setTextInternal(text string, cursorPlacement string) {
	if cursorPlacement != "start" {
		cursorPlacement = "end"
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	e.state.Lines = lines
	if cursorPlacement == "start" {
		e.state.CursorLine = 0
	} else {
		e.state.CursorLine = len(e.state.Lines) - 1
	}
	col := 0
	if cursorPlacement != "start" {
		col = len(e.state.Lines[e.state.CursorLine])
	}
	e.setCursorCol(col)
	// Reset scroll - render() will adjust to show cursor
	e.scrollOffset = 0

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// Invalidate implements Component (no cached state to invalidate currently).
func (e *Editor) Invalidate() {}

// renderTopBorder renders the top border with an optional scroll indicator
// (pi: protected renderTopBorder).
func (e *Editor) renderTopBorder(width int, hiddenLineCount int) string {
	if e.TopBorderOverride != nil {
		if line, ok := e.TopBorderOverride(width, hiddenLineCount); ok {
			return line
		}
	}
	border := strings.Repeat("─", width)
	if hiddenLineCount > 0 {
		border = createScrollBorder("↑", hiddenLineCount, width)
	}
	return e.BorderColor(border)
}

// renderBottomBorder renders the bottom border with an optional scroll
// indicator (pi: protected renderBottomBorder).
func (e *Editor) renderBottomBorder(width int, hiddenLineCount int) string {
	border := strings.Repeat("─", width)
	if hiddenLineCount > 0 {
		border = createScrollBorder("↓", hiddenLineCount, width)
	}
	return e.BorderColor(border)
}

// Render implements Component.
func (e *Editor) Render(width int) []string {
	maxPadding := (width - 1) / 2
	if maxPadding < 0 {
		maxPadding = 0
	}
	paddingX := e.paddingX
	if paddingX > maxPadding {
		paddingX = maxPadding
	}
	contentWidth := width - paddingX*2
	if contentWidth < 1 {
		contentWidth = 1
	}

	// Layout width: with padding the cursor can overflow into it,
	// without padding we reserve 1 column for the cursor.
	layoutWidth := contentWidth
	if paddingX == 0 {
		layoutWidth = contentWidth - 1
	}
	if layoutWidth < 1 {
		layoutWidth = 1
	}

	// Store last render width for cursor navigation (must match wrapping
	// width) plus render geometry for mouse hit-testing (v0.85.1).
	e.lastWidth = layoutWidth

	// Layout the text
	layoutLines := e.layoutText(layoutWidth)

	// Calculate max visible lines: 30% of terminal height, minimum 5 lines
	terminalRows := e.tui.Terminal().Rows()
	maxVisibleLines := terminalRows * 3 / 10
	if maxVisibleLines < 5 {
		maxVisibleLines = 5
	}

	// Find the cursor line index in layoutLines
	cursorLineIndex := -1
	for i, line := range layoutLines {
		if line.HasCursor {
			cursorLineIndex = i
			break
		}
	}
	if cursorLineIndex == -1 {
		cursorLineIndex = 0
	}

	// Adjust scroll offset to keep cursor visible
	if cursorLineIndex < e.scrollOffset {
		e.scrollOffset = cursorLineIndex
	} else if cursorLineIndex >= e.scrollOffset+maxVisibleLines {
		e.scrollOffset = cursorLineIndex - maxVisibleLines + 1
	}

	// Clamp scroll offset to valid range
	maxScrollOffset := len(layoutLines) - maxVisibleLines
	if maxScrollOffset < 0 {
		maxScrollOffset = 0
	}
	if e.scrollOffset > maxScrollOffset {
		e.scrollOffset = maxScrollOffset
	}
	if e.scrollOffset < 0 {
		e.scrollOffset = 0
	}

	// Get visible lines slice
	visibleLines := layoutLines[e.scrollOffset:]
	if len(visibleLines) > maxVisibleLines {
		visibleLines = visibleLines[:maxVisibleLines]
	}
	e.renderedVisibleLineCount = len(visibleLines)

	result := make([]string, 0, len(visibleLines)+2)
	leftPadding := strings.Repeat(" ", paddingX)
	rightPadding := leftPadding

	// Render top border (with scroll indicator if scrolled down)
	result = append(result, e.renderTopBorder(width, e.scrollOffset))

	// Render each visible layout line
	// Emit hardware cursor marker when focused so TUI can position the
	// hardware cursor for IME candidate-window placement even while
	// autocomplete (e.g. slash-command menu) is visible.
	emitCursorMarker := e.Focused

	for _, ll := range visibleLines {
		displayText := ll.Text
		lineVisibleWidth := VisibleWidth(ll.Text)
		cursorInPadding := false

		// Add cursor if this line has it
		if ll.HasCursor {
			cursorPos := ll.CursorPos
			before := displayText[:cursorPos]
			after := displayText[cursorPos:]

			// Hardware cursor marker (zero-width, emitted before fake cursor for IME positioning)
			marker := ""
			if emitCursorMarker {
				marker = CursorMarker
			}

			if len(after) > 0 {
				// Cursor is on a character (grapheme) - replace it with highlighted version
				// Get the first grapheme from 'after'
				afterGraphemes := e.segment(after, "grapheme")
				firstGrapheme := ""
				if len(afterGraphemes) > 0 {
					firstGrapheme = afterGraphemes[0].Segment
				}
				restAfter := after[len(firstGrapheme):]
				cursor := "\x1b[7m" + firstGrapheme + "\x1b[0m"
				displayText = before + marker + cursor + restAfter
				// lineVisibleWidth stays the same - we're replacing, not adding
			} else {
				// Cursor is at the end - add highlighted space
				cursor := "\x1b[7m \x1b[0m"
				displayText = before + marker + cursor
				lineVisibleWidth = lineVisibleWidth + 1
				// If cursor overflows content width into the padding, flag it
				if lineVisibleWidth > contentWidth && paddingX > 0 {
					cursorInPadding = true
				}
			}
		}

		// Calculate padding based on actual visible width
		padding := strings.Repeat(" ", max(0, contentWidth-lineVisibleWidth))
		lineRightPadding := rightPadding
		if cursorInPadding {
			lineRightPadding = rightPadding[1:]
		}

		// Render the line (no side borders, just horizontal lines above and below)
		result = append(result, leftPadding+displayText+padding+lineRightPadding)
	}

	// Render bottom border (with scroll indicator if more content below)
	linesBelow := len(layoutLines) - (e.scrollOffset + len(visibleLines))
	result = append(result, e.renderBottomBorder(width, linesBelow))

	// pi: add autocomplete list if active
	e.renderedAutocompleteHeight = 0
	if e.autocompleteState != "" && e.autocompleteList != nil {
		autocompleteResult := e.autocompleteList.Render(contentWidth)
		e.renderedAutocompleteHeight = len(autocompleteResult)
		for _, line := range autocompleteResult {
			lineWidth := VisibleWidth(line)
			linePadding := strings.Repeat(" ", max(0, contentWidth-lineWidth))
			result = append(result, leftPadding+line+linePadding+rightPadding)
		}
	}

	return result
}

// HandleMouse implements MouseHandler (pi: Editor.handleMouse): clicks on
// the autocomplete dropdown are forwarded to the list; a click gesture on
// an editor row positions the cursor. Press/drag/release stay unhandled so
// the renderer's screen-level text selection can run over the editor rows.
// Must be called under the lock (pig: state mutation serialized like
// handleInputLocked).
func (e *Editor) handleMouseLocked(event *TuiMouseEvent) *TuiMouseEventResult {
	autocompleteStartRow := e.renderedVisibleLineCount + 2
	if e.autocompleteState != "" && e.autocompleteList != nil &&
		event.Y >= autocompleteStartRow && event.Y < autocompleteStartRow+e.renderedAutocompleteHeight {
		maxPadding := (event.Width - 1) / 2
		if maxPadding < 0 {
			maxPadding = 0
		}
		paddingX := e.paddingX
		if paddingX > maxPadding {
			paddingX = maxPadding
		}
		contentWidth := max(1, event.Width-paddingX*2)
		listEvent := *event
		listEvent.X = event.X - paddingX
		listEvent.Y = event.Y - autocompleteStartRow
		listEvent.Width = contentWidth
		listEvent.Height = e.renderedAutocompleteHeight
		var result *TuiMouseEventResult
		if mh, ok := Component(e.autocompleteList).(MouseHandler); ok {
			result = mh.HandleMouse(&listEvent)
		}
		if result != nil {
			out := *result
			out.Focus = true
			return &out
		}
		return nil
	}

	// Leave press/drag/release unhandled so the renderer's screen-level text
	// selection can run over the editor rows (drag to select, release to
	// copy). The renderer synthesizes a click when press and release land on
	// the same cell without movement, which is the gesture that positions
	// the cursor.
	if event.Type != MouseEventTypeClick || event.Button != MouseButtonLeft {
		return nil
	}
	if event.Y <= 0 || event.Y > e.renderedVisibleLineCount {
		return &TuiMouseEventResult{Handled: true, Focus: true}
	}

	visualLines := e.buildVisualLineMap(e.lastWidth)
	visualLineIndex := e.scrollOffset + event.Y - 1
	if visualLineIndex < 0 || visualLineIndex >= len(visualLines) {
		return &TuiMouseEventResult{Handled: true, Focus: true}
	}
	visualLine := visualLines[visualLineIndex]
	logicalLine := ""
	if visualLine.LogicalLine < len(e.state.Lines) {
		logicalLine = e.state.Lines[visualLine.LogicalLine]
	}
	chunkEnd := visualLine.StartCol + visualLine.Length
	chunk := logicalLine[visualLine.StartCol:chunkEnd]
	maxPadding := (event.Width - 1) / 2
	if maxPadding < 0 {
		maxPadding = 0
	}
	paddingX := e.paddingX
	if paddingX > maxPadding {
		paddingX = maxPadding
	}
	targetColumn := max(0, event.X-paddingX)
	visibleColumn := 0
	targetIndex := len(chunk)
	lastGraphemeIndex := 0
	for _, grapheme := range e.segment(chunk, "grapheme") {
		nextColumn := visibleColumn + VisibleWidth(grapheme.Segment)
		lastGraphemeIndex = grapheme.Index
		if targetColumn < nextColumn {
			targetIndex = grapheme.Index
			break
		}
		visibleColumn = nextColumn
	}
	isLastSegment := visualLineIndex == len(visualLines)-1 ||
		visualLines[visualLineIndex+1].LogicalLine != visualLine.LogicalLine
	if !isLastSegment && targetIndex == len(chunk) && len(chunk) > 0 {
		targetIndex = lastGraphemeIndex
	}

	e.state.CursorLine = visualLine.LogicalLine
	e.setCursorCol(visualLine.StartCol + targetIndex)
	e.lastAction = ""
	e.exitHistoryBrowsing()
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	}
	return &TuiMouseEventResult{Handled: true, Focus: true}
}

// HandleMouse implements MouseHandler.
func (e *Editor) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	var result *TuiMouseEventResult
	e.lock(func() { result = e.handleMouseLocked(event) })
	return result
}

// HandleInput implements InputHandler.
func (e *Editor) HandleInput(data string) {
	e.lock(func() { e.handleInputLocked(data) })

}

func (e *Editor) handleInputLocked(data string) {
	kb := GetKeybindings()

	// Handle character jump mode (awaiting next character to jump to)
	if e.jumpMode != "" {
		// Cancel if the hotkey is pressed again
		if kb.Matches(data, "tui.editor.jumpForward") || kb.Matches(data, "tui.editor.jumpBackward") {
			e.jumpMode = ""
			return
		}

		printable, ok := DecodePrintableKey(data)
		if !ok {
			if r, _ := utf8.DecodeRuneInString(data); r >= 32 {
				printable, ok = data, true
			}
		}
		if ok {
			// Printable character - perform the jump
			direction := e.jumpMode
			e.jumpMode = ""
			e.jumpToChar(printable, direction)
			return
		}

		// Control character - cancel and fall through to normal handling
		e.jumpMode = ""
	}

	// Handle bracketed paste mode
	if strings.Contains(data, "\x1b[200~") {
		e.isInPaste = true
		e.pasteBuffer = ""
		data = strings.Replace(data, "\x1b[200~", "", 1)
	}

	if e.isInPaste {
		e.pasteBuffer += data
		endIndex := strings.Index(e.pasteBuffer, "\x1b[201~")
		if endIndex != -1 {
			pasteContent := e.pasteBuffer[:endIndex]
			if len(pasteContent) > 0 {
				e.handlePaste(pasteContent)
			}
			e.isInPaste = false
			remaining := e.pasteBuffer[endIndex+6:]
			e.pasteBuffer = ""
			if len(remaining) > 0 {
				e.HandleInput(remaining)
			}
			return
		}
		return
	}

	// Ctrl+C - let parent handle (exit/clear)
	if kb.Matches(data, "tui.input.copy") {
		return
	}

	// Undo
	if kb.Matches(data, "tui.editor.undo") {
		e.undo()
		return
	}

	// Handle autocomplete mode
	if e.autocompleteState != "" && e.autocompleteList != nil {
		if kb.Matches(data, "tui.select.cancel") {
			e.cancelAutocomplete()
			return
		}

		if kb.Matches(data, "tui.select.up") || kb.Matches(data, "tui.select.down") {
			e.autocompleteList.HandleInput(data)
			return
		}

		if kb.Matches(data, "tui.input.tab") {
			selected := e.autocompleteList.GetSelectedItem()
			if selected != nil && e.autocompleteProvider != nil {
				e.pushUndoSnapshot()
				e.lastAction = ""
				result := e.autocompleteProvider.ApplyCompletion(
					e.state.Lines,
					e.state.CursorLine,
					e.state.CursorCol,
					autocompleteItemFromSelect(*selected),
					e.autocompletePrefix,
				)
				e.state.Lines = result.Lines
				e.state.CursorLine = result.CursorLine
				e.setCursorCol(result.CursorCol)
				e.cancelAutocomplete()
				if e.OnChange != nil {
					e.OnChange(e.GetText())
				}
			}
			return
		}

		if kb.Matches(data, "tui.select.confirm") {
			selected := e.autocompleteList.GetSelectedItem()
			if selected != nil && e.autocompleteProvider != nil {
				e.pushUndoSnapshot()
				e.lastAction = ""
				result := e.autocompleteProvider.ApplyCompletion(
					e.state.Lines,
					e.state.CursorLine,
					e.state.CursorCol,
					autocompleteItemFromSelect(*selected),
					e.autocompletePrefix,
				)
				e.state.Lines = result.Lines
				e.state.CursorLine = result.CursorLine
				e.setCursorCol(result.CursorCol)

				if strings.HasPrefix(e.autocompletePrefix, "/") {
					e.cancelAutocomplete()
					// Fall through to submit
				} else {
					e.cancelAutocomplete()
					if e.OnChange != nil {
						e.OnChange(e.GetText())
					}
					return
				}
			}
		}
	}

	// Tab - trigger completion
	if kb.Matches(data, "tui.input.tab") && e.autocompleteState == "" {
		e.handleTabCompletion()
		return
	}

	// Deletion actions
	if kb.Matches(data, "tui.editor.deleteToLineEnd") {
		e.deleteToEndOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteToLineStart") {
		e.deleteToStartOfLine()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordBackward") {
		e.deleteWordBackwards()
		return
	}
	if kb.Matches(data, "tui.editor.deleteWordForward") {
		e.deleteWordForward()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharBackward") || MatchesKey(data, "shift+backspace") {
		e.handleBackspace()
		return
	}
	if kb.Matches(data, "tui.editor.deleteCharForward") || MatchesKey(data, "shift+delete") {
		e.handleForwardDelete()
		return
	}

	// Kill ring actions
	if kb.Matches(data, "tui.editor.yank") {
		e.yank()
		return
	}
	if kb.Matches(data, "tui.editor.yankPop") {
		e.yankPop()
		return
	}

	// Dedicated history actions always browse entries instead of moving the cursor.
	if kb.Matches(data, "tui.editor.historyPrevious") {
		e.cancelAutocomplete()
		e.navigateHistory(-1)
		return
	}
	if kb.Matches(data, "tui.editor.historyNext") {
		e.cancelAutocomplete()
		e.navigateHistory(1)
		return
	}

	// Cursor movement actions
	if kb.Matches(data, "tui.editor.cursorLineStart") {
		e.moveToLineStart()
		return
	}
	if kb.Matches(data, "tui.editor.cursorLineEnd") {
		e.moveToLineEnd()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordLeft") {
		e.moveWordBackwards()
		return
	}
	if kb.Matches(data, "tui.editor.cursorWordRight") {
		e.moveWordForwards()
		return
	}

	// New line
	if kb.Matches(data, "tui.input.newLine") ||
		(len(data) > 1 && data[0] == 10) ||
		data == "\x1b\r" ||
		data == "\x1b[13;2~" ||
		(len(data) > 1 && strings.Contains(data, "\x1b") && strings.Contains(data, "\r")) ||
		data == "\n" {
		if e.shouldSubmitOnBackslashEnter(data, kb) {
			e.handleBackspace()
			e.submitValue()
			return
		}
		e.addNewLine()
		return
	}

	// Submit (Enter)
	if kb.Matches(data, "tui.input.submit") {
		if e.DisableSubmit {
			return
		}

		// Workaround for terminals without Shift+Enter support:
		// If char before cursor is \, delete it and insert newline instead of submitting.
		currentLine := ""
		if e.state.CursorLine < len(e.state.Lines) {
			currentLine = e.state.Lines[e.state.CursorLine]
		}
		if e.state.CursorCol > 0 && e.state.CursorCol-1 < len(currentLine) && currentLine[e.state.CursorCol-1] == '\\' {
			e.handleBackspace()
			e.addNewLine()
			return
		}

		e.submitValue()
		return
	}

	// Arrow key navigation (with history support)
	if kb.Matches(data, "tui.editor.cursorUp") {
		if e.isOnFirstVisualLine() &&
			(e.isEditorEmpty() || e.historyIndex > -1 || e.state.CursorCol == 0) {
			e.navigateHistory(-1)
		} else if e.isOnFirstVisualLine() {
			// Already at top - jump to start of line
			e.moveToLineStart()
		} else {
			e.moveCursor(-1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorDown") {
		if e.historyIndex > -1 && e.isOnLastVisualLine() {
			e.navigateHistory(1)
		} else if e.isOnLastVisualLine() {
			// Already at bottom - jump to end of line
			e.moveToLineEnd()
		} else {
			e.moveCursor(1, 0)
		}
		return
	}
	if kb.Matches(data, "tui.editor.cursorRight") {
		e.moveCursor(0, 1)
		return
	}
	if kb.Matches(data, "tui.editor.cursorLeft") {
		e.moveCursor(0, -1)
		return
	}

	// Page up/down - scroll by page and move cursor
	if kb.Matches(data, "tui.editor.pageUp") {
		e.pageScroll(-1)
		return
	}
	if kb.Matches(data, "tui.editor.pageDown") {
		e.pageScroll(1)
		return
	}

	// Character jump mode triggers
	if kb.Matches(data, "tui.editor.jumpForward") {
		e.jumpMode = "forward"
		return
	}
	if kb.Matches(data, "tui.editor.jumpBackward") {
		e.jumpMode = "backward"
		return
	}

	// Shift+Space - insert regular space
	if MatchesKey(data, "shift+space") {
		e.insertCharacter(" ", false)
		return
	}

	if printable, ok := DecodePrintableKey(data); ok {
		e.insertCharacter(printable, false)
		return
	}

	// Regular characters
	if r, _ := utf8.DecodeRuneInString(data); r >= 32 {
		e.insertCharacter(data, false)
	}
}

func (e *Editor) layoutText(contentWidth int) []layoutLine {
	layoutLines := []layoutLine{}

	if len(e.state.Lines) == 0 || (len(e.state.Lines) == 1 && e.state.Lines[0] == "") {
		// Empty editor
		layoutLines = append(layoutLines, layoutLine{Text: "", HasCursor: true, CursorPos: 0})
		return layoutLines
	}

	// Process each logical line
	for i := 0; i < len(e.state.Lines); i++ {
		line := e.state.Lines[i]
		isCurrentLine := i == e.state.CursorLine
		lineVisibleWidth := VisibleWidth(line)

		if lineVisibleWidth <= contentWidth {
			// Line fits in one layout line
			if isCurrentLine {
				layoutLines = append(layoutLines, layoutLine{Text: line, HasCursor: true, CursorPos: e.state.CursorCol})
			} else {
				layoutLines = append(layoutLines, layoutLine{Text: line, HasCursor: false})
			}
		} else {
			// Line needs wrapping - use word-aware wrapping
			chunks := WordWrapLine(line, contentWidth, e.segment(line, "grapheme"))

			for chunkIndex := 0; chunkIndex < len(chunks); chunkIndex++ {
				chunk := chunks[chunkIndex]

				cursorPos := e.state.CursorCol
				isLastChunk := chunkIndex == len(chunks)-1

				// Determine if cursor is in this chunk
				// For word-wrapped chunks, we need to handle the case where
				// cursor might be in trimmed whitespace at end of chunk
				hasCursorInChunk := false
				adjustedCursorPos := 0

				if isCurrentLine {
					if isLastChunk {
						// Last chunk: cursor belongs here if >= startIndex
						hasCursorInChunk = cursorPos >= chunk.StartIndex
						adjustedCursorPos = cursorPos - chunk.StartIndex
					} else {
						// Non-last chunk: cursor belongs here if in range [startIndex, endIndex)
						// But we need to handle the visual position in the trimmed text
						hasCursorInChunk = cursorPos >= chunk.StartIndex && cursorPos < chunk.EndIndex
						if hasCursorInChunk {
							adjustedCursorPos = cursorPos - chunk.StartIndex
							// Clamp to text length (in case cursor was in trimmed whitespace)
							if adjustedCursorPos > len(chunk.Text) {
								adjustedCursorPos = len(chunk.Text)
							}
						}
					}
				}

				if hasCursorInChunk {
					layoutLines = append(layoutLines, layoutLine{Text: chunk.Text, HasCursor: true, CursorPos: adjustedCursorPos})
				} else {
					layoutLines = append(layoutLines, layoutLine{Text: chunk.Text, HasCursor: false})
				}
			}
		}
	}

	return layoutLines
}

// GetText returns the current text content.
func (e *Editor) GetText() string {
	var text string
	e.lock(func() { text = e.getTextLocked() })
	return text
}

func (e *Editor) getTextLocked() string {
	return strings.Join(e.state.Lines, "\n")
}

func (e *Editor) expandPasteMarkers(text string) string {
	result := text
	for pasteID, pasteContent := range e.pastes {
		markerRegex := regexp.MustCompile(`\[paste #` + strconv.Itoa(pasteID) + `( (\+\d+ lines|\d+ chars))?\]`)
		// pi: result.replace(markerRegex, () => pasteContent) — function
		// replacement is literal; Go's ReplaceAllString would interpret $1.
		result = markerRegex.ReplaceAllLiteralString(result, pasteContent)
	}
	return result
}

// GetExpandedText gets text with paste markers expanded to their actual content.
// Use this when you need the full content (e.g., for external editor).
func (e *Editor) GetExpandedText() string {
	var text string
	e.lock(func() { text = e.expandPasteMarkers(e.getTextLocked()) })
	return text
}

// GetLines returns the lines as a defensive copy.
func (e *Editor) GetLines() []string {
	var out []string
	e.lock(func() {
		out = make([]string, len(e.state.Lines))
		copy(out, e.state.Lines)
	})
	return out
}

// EditorCursor is the return shape of GetCursor (pi: { line, col }).
type EditorCursor struct {
	Line int
	Col  int
}

// GetCursor returns the cursor position.
func (e *Editor) GetCursor() EditorCursor {
	var cursor EditorCursor
	e.lock(func() { cursor = EditorCursor{Line: e.state.CursorLine, Col: e.state.CursorCol} })
	return cursor
}

// SetText sets the text content.
func (e *Editor) SetText(text string) {
	e.lock(func() { e.setTextLocked(text) })
}

func (e *Editor) setTextLocked(text string) {
	e.cancelAutocomplete()
	e.lastAction = ""
	e.exitHistoryBrowsing()
	normalized := e.normalizeText(text)
	// Push undo snapshot if content differs (makes programmatic changes undoable)
	if e.GetText() != normalized {
		e.pushUndoSnapshot()
	}
	e.pastes = make(map[int]string)
	e.pasteCounter = 0
	e.setTextInternal(normalized, "end")
}

// InsertTextAtCursor inserts text at the current cursor position.
// Used for programmatic insertion (e.g., clipboard image markers).
// This is atomic for undo - single undo restores entire pre-insert state.
func (e *Editor) InsertTextAtCursor(text string) {
	e.lock(func() {
		if text == "" {
			return
		}
		e.cancelAutocomplete()
		e.pushUndoSnapshot()
		e.lastAction = ""
		e.exitHistoryBrowsing()
		e.insertTextAtCursorInternal(text)
	})
}

// normalizeText normalizes text for editor storage:
//   - Normalize line endings (\r\n and \r -> \n)
//   - Expand tabs to 4 spaces
func (e *Editor) normalizeText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(text, "\t", "    ")
}

// insertTextAtCursorInternal inserts text at cursor. Handles single and
// multi-line text. Does not push undo snapshots or trigger autocomplete -
// caller is responsible. Normalizes line endings and calls onChange once at
// the end.
func (e *Editor) insertTextAtCursorInternal(text string) {
	if text == "" {
		return
	}

	// Normalize line endings and tabs
	normalized := e.normalizeText(text)
	insertedLines := strings.Split(normalized, "\n")

	currentLine := e.state.Lines[e.state.CursorLine]
	beforeCursor := currentLine[:e.state.CursorCol]
	afterCursor := currentLine[e.state.CursorCol:]

	if len(insertedLines) == 1 {
		// Single line - insert at cursor position
		e.state.Lines[e.state.CursorLine] = beforeCursor + normalized + afterCursor
		e.setCursorCol(e.state.CursorCol + len(normalized))
	} else {
		// Multi-line insertion
		newLines := make([]string, 0, len(e.state.Lines)+len(insertedLines)-1)
		// All lines before current line
		newLines = append(newLines, e.state.Lines[:e.state.CursorLine]...)
		// The first inserted line merged with text before cursor
		newLines = append(newLines, beforeCursor+insertedLines[0])
		// All middle inserted lines
		newLines = append(newLines, insertedLines[1:len(insertedLines)-1]...)
		// The last inserted line with text after cursor
		newLines = append(newLines, insertedLines[len(insertedLines)-1]+afterCursor)
		// All lines after current line
		newLines = append(newLines, e.state.Lines[e.state.CursorLine+1:]...)
		e.state.Lines = newLines

		e.state.CursorLine += len(insertedLines) - 1
		e.setCursorCol(len(insertedLines[len(insertedLines)-1]))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) insertCharacter(char string, skipUndoCoalescing bool) {
	e.exitHistoryBrowsing()

	// Undo coalescing (fish-style):
	// - Consecutive word chars coalesce into one undo unit
	// - Space captures state before itself (so undo removes space+following word together)
	// - Each space is separately undoable
	// Skip coalescing when called from atomic operations (e.g., handlePaste)
	if !skipUndoCoalescing {
		if (char != "" && IsWhitespaceChar(char)) || e.lastAction != "type-word" {
			e.pushUndoSnapshot()
		}
		e.lastAction = "type-word"
	}

	line := e.state.Lines[e.state.CursorLine]

	before := line[:e.state.CursorCol]
	after := line[e.state.CursorCol:]

	e.state.Lines[e.state.CursorLine] = before + char + after
	e.setCursorCol(e.state.CursorCol + len(char))

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	// Check if we should trigger or update autocomplete
	if e.autocompleteState == "" {
		// Auto-trigger for "/" at the start of a line (slash commands)
		if char == "/" && e.isAtStartOfMessage() {
			e.tryTriggerAutocomplete(false)
		} else if e.autocompleteTriggerCharactersContains(char) {
			// Auto-trigger for symbol-based completion like @, #, or provider
			// triggers at token boundaries
			currentLine := e.state.Lines[e.state.CursorLine]
			textBeforeCursor := currentLine[:e.state.CursorCol]
			charBeforeSymbol := ""
			if len(textBeforeCursor) >= 2 {
				charBeforeSymbol = textBeforeCursor[len(textBeforeCursor)-2 : len(textBeforeCursor)-1]
			}
			if len(textBeforeCursor) == 1 || charBeforeSymbol == " " || charBeforeSymbol == "\t" {
				e.tryTriggerAutocomplete(false)
			}
		} else if editorAlphaNumDotDashUnderScoreRegex.MatchString(char) {
			// Also auto-trigger when typing letters in a slash command or symbol
			// completion context
			currentLine := e.state.Lines[e.state.CursorLine]
			textBeforeCursor := currentLine[:e.state.CursorCol]
			// Check if we're in a slash command (with or without space for arguments)
			if e.isInSlashCommandContext(textBeforeCursor) {
				e.tryTriggerAutocomplete(false)
			} else if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
				// Check if we're in a symbol-based completion context like @, #, or
				// provider triggers
				e.tryTriggerAutocomplete(false)
			}
		}
	} else {
		e.updateAutocomplete()
	}
}

// autocompleteTriggerCharactersContains reports whether char is in the
// trigger set (pi: this.autocompleteTriggerCharacters.includes(char)).
func (e *Editor) autocompleteTriggerCharactersContains(char string) bool {
	for _, c := range e.autocompleteTriggerCharacters {
		if c == char {
			return true
		}
	}
	return false
}

// editorAlphaNumDotDashUnderScoreRegex matches pi's /[a-zA-Z0-9.\-_]/.
var editorAlphaNumDotDashUnderScoreRegex = regexp.MustCompile(`[a-zA-Z0-9.\-_]`)

func (e *Editor) handlePaste(pastedText string) {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = ""

	e.pushUndoSnapshot()

	// Some terminals (e.g. tmux popups with extended-keys-format=csi-u) re-encode
	// control bytes inside bracketed paste as CSI-u Ctrl+<letter> sequences
	// (ESC [ <codepoint> ; 5 u). Decode those back to their literal byte so the
	// per-char filter below preserves newlines instead of stripping ESC and
	// leaking the printable tail (e.g. "[106;5u") into the editor.
	decodedText := pasteDecodeCsiURegex.ReplaceAllStringFunc(pastedText, func(match string) string {
		sub := pasteDecodeCsiURegex.FindStringSubmatch(match)
		cp, err := strconv.Atoi(sub[1])
		if err != nil {
			return match
		}
		if cp >= 97 && cp <= 122 {
			return string(rune(cp - 96))
		}
		if cp >= 65 && cp <= 90 {
			return string(rune(cp - 64))
		}
		return match
	})

	// Clean the pasted text: normalize line endings, expand tabs
	cleanText := e.normalizeText(decodedText)

	// Filter out non-printable characters except newlines
	var sb strings.Builder
	for _, r := range cleanText {
		if r == '\n' || r >= 32 {
			sb.WriteRune(r)
		}
	}
	filteredText := sb.String()

	// If pasting a file path (starts with /, ~, or .) and the character before
	// the cursor is a word character, prepend a space for better readability
	if pastePathPrefixRegex.MatchString(filteredText) {
		currentLine := e.state.Lines[e.state.CursorLine]
		charBeforeCursor := ""
		if e.state.CursorCol > 0 && e.state.CursorCol <= len(currentLine) {
			r, _ := utf8.DecodeLastRuneInString(currentLine[:e.state.CursorCol])
			charBeforeCursor = string(r)
		}
		if charBeforeCursor != "" && pasteWordCharRegex.MatchString(charBeforeCursor) {
			filteredText = " " + filteredText
		}
	}

	// Split into lines to check for large paste
	pastedLines := strings.Split(filteredText, "\n")

	// Check if this is a large paste (> 10 lines or > 1000 characters)
	totalChars := len(filteredText)
	if len(pastedLines) > 10 || totalChars > 1000 {
		// Store the paste and insert a marker
		e.pasteCounter++
		pasteID := e.pasteCounter
		e.pastes[pasteID] = filteredText

		// Insert marker like "[paste #1 +123 lines]" or "[paste #1 1234 chars]"
		marker := fmt.Sprintf("[paste #%d %d chars]", pasteID, totalChars)
		if len(pastedLines) > 10 {
			marker = fmt.Sprintf("[paste #%d +%d lines]", pasteID, len(pastedLines))
		}
		e.insertTextAtCursorInternal(marker)
		return
	}

	if len(pastedLines) == 1 {
		// Single line - insert atomically (do not trigger autocomplete during paste)
		e.insertTextAtCursorInternal(filteredText)
		return
	}

	// Multi-line paste - use direct state manipulation
	e.insertTextAtCursorInternal(filteredText)
}

func (e *Editor) addNewLine() {
	e.cancelAutocomplete()
	e.exitHistoryBrowsing()
	e.lastAction = ""

	e.pushUndoSnapshot()

	currentLine := e.state.Lines[e.state.CursorLine]

	before := currentLine[:e.state.CursorCol]
	after := currentLine[e.state.CursorCol:]

	// Split current line
	e.state.Lines[e.state.CursorLine] = before
	rest := append([]string{after}, e.state.Lines[e.state.CursorLine+1:]...)
	e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], rest...)

	// Move cursor to start of new line
	e.state.CursorLine++
	e.setCursorCol(0)

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) shouldSubmitOnBackslashEnter(data string, kb *KeybindingsManager) bool {
	if e.DisableSubmit {
		return false
	}
	if !MatchesKey(data, "enter") {
		return false
	}
	submitKeys := kb.GetKeys("tui.input.submit")
	hasShiftEnter := false
	for _, key := range submitKeys {
		if key == "shift+enter" || key == "shift+return" {
			hasShiftEnter = true
			break
		}
	}
	if !hasShiftEnter {
		return false
	}

	currentLine := e.state.Lines[e.state.CursorLine]
	return e.state.CursorCol > 0 && e.state.CursorCol-1 < len(currentLine) && currentLine[e.state.CursorCol-1] == '\\'
}

func (e *Editor) submitValue() {
	e.cancelAutocomplete()
	result := jsTrim(e.expandPasteMarkers(strings.Join(e.state.Lines, "\n")))

	e.state = editorState{Lines: []string{""}}
	e.pastes = make(map[int]string)
	e.pasteCounter = 0
	e.exitHistoryBrowsing()
	e.scrollOffset = 0
	e.undoStack.Clear()
	e.lastAction = ""

	if e.OnChange != nil {
		e.OnChange("")
	}
	if e.OnSubmit != nil {
		e.OnSubmit(result)
	}
}

func (e *Editor) handleBackspace() {
	e.exitHistoryBrowsing()
	e.lastAction = ""

	if e.state.CursorCol > 0 {
		e.pushUndoSnapshot()

		// Delete grapheme before cursor (handles emojis, combining characters, etc.)
		line := e.state.Lines[e.state.CursorLine]
		beforeCursor := line[:e.state.CursorCol]

		// Find the last grapheme in the text before cursor
		graphemes := e.segment(beforeCursor, "grapheme")
		lastGrapheme := SegmentData{Segment: ""}
		if len(graphemes) > 0 {
			lastGrapheme = graphemes[len(graphemes)-1]
		}
		graphemeLength := len(lastGrapheme.Segment)
		if len(graphemes) == 0 {
			graphemeLength = 1
		}
		isPastedSegmented := pasteMarkerSingle.FindStringSubmatch(lastGrapheme.Segment)

		if isPastedSegmented != nil {
			// This contains the id part e.g 4 from [paste #4 +123 lines]
			targetID, _ := strconv.Atoi(isPastedSegmented[1])
			delete(e.pastes, targetID)
			e.pasteCounter--

			// Shift registry entries down in ascending id order, independent
			// of marker order in the text ([paste #3] becomes [paste #2] when
			// [paste #1] is removed).
			higherIDs := make([]int, 0, len(e.pastes))
			for id := range e.pastes {
				if id > targetID {
					higherIDs = append(higherIDs, id)
				}
			}
			sortInts(higherIDs)
			for _, id := range higherIDs {
				e.pastes[id-1] = e.pastes[id]
				delete(e.pastes, id)
			}

			// Renumber markers with ids greater than the removed one.
			for li, l := range e.state.Lines {
				e.state.Lines[li] = pasteMarkerRegex.ReplaceAllStringFunc(l, func(fullMatch string) string {
					groups := pasteMarkerRegex.FindStringSubmatch(fullMatch)
					x, _ := strconv.Atoi(groups[1])
					if x <= targetID {
						return fullMatch
					}
					suffixGroup := groups[2]
					if suffixGroup == "" {
						// pi quirk: the absent regex group is undefined in TS,
						// and `${undefined}` renders the string "undefined".
						suffixGroup = "undefined"
					}
					return fmt.Sprintf("[paste #%d%s]", x-1, suffixGroup)
				})
			}
		}

		line = e.state.Lines[e.state.CursorLine]

		before := line[:e.state.CursorCol-graphemeLength]
		after := line[e.state.CursorCol:]

		e.state.Lines[e.state.CursorLine] = before + after
		e.setCursorCol(e.state.CursorCol - graphemeLength)
	} else if e.state.CursorLine > 0 {
		e.pushUndoSnapshot()

		// Merge with previous line
		currentLine := e.state.Lines[e.state.CursorLine]
		previousLine := e.state.Lines[e.state.CursorLine-1]

		e.state.Lines[e.state.CursorLine-1] = previousLine + currentLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)

		e.state.CursorLine--
		e.setCursorCol(len(previousLine))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	// Update or re-trigger autocomplete after backspace
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	} else {
		// If autocomplete was cancelled (no matches), re-trigger if we're in a
		// completable context
		currentLine := e.state.Lines[e.state.CursorLine]
		textBeforeCursor := currentLine[:e.state.CursorCol]
		// Slash command context
		if e.isInSlashCommandContext(textBeforeCursor) {
			e.tryTriggerAutocomplete(false)
		} else if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
			// Symbol-based completion context like @, #, or provider triggers
			e.tryTriggerAutocomplete(false)
		}
	}
}

// setCursorCol sets the cursor column and clears preferredVisualCol.
// Use this for all non-vertical cursor movements to reset sticky column behavior.
func (e *Editor) setCursorCol(col int) {
	e.state.CursorCol = col
	e.preferredVisualCol = nil
	e.snappedFromCursorCol = nil
}

// moveToVisualLine moves the cursor to a target visual line, applying sticky
// column logic. Shared by moveCursor() and pageScroll().
func (e *Editor) moveToVisualLine(visualLines []visualLine, currentVisualLine, targetVisualLine int) {
	currentVL := visualLines[currentVisualLine]
	targetVL := visualLines[targetVisualLine]

	// When the cursor was snapped to a segment start, resolve the pre-snap
	// position against the VL it belongs to. This gives the correct visual
	// column even after a resize reshuffles VLs.
	var currentVisualCol int
	if e.snappedFromCursorCol != nil {
		vlIndex := e.findVisualLineAt(visualLines, currentVL.LogicalLine, *e.snappedFromCursorCol)
		currentVisualCol = *e.snappedFromCursorCol - visualLines[vlIndex].StartCol
	} else {
		currentVisualCol = e.state.CursorCol - currentVL.StartCol
	}

	// For non-last segments, clamp to length-1 to stay within the segment
	isLastSourceSegment := currentVisualLine == len(visualLines)-1 ||
		visualLines[currentVisualLine+1].LogicalLine != currentVL.LogicalLine
	sourceMaxVisualCol := currentVL.Length
	if !isLastSourceSegment {
		sourceMaxVisualCol = max(0, currentVL.Length-1)
	}

	isLastTargetSegment := targetVisualLine == len(visualLines)-1 ||
		visualLines[targetVisualLine+1].LogicalLine != targetVL.LogicalLine
	targetMaxVisualCol := targetVL.Length
	if !isLastTargetSegment {
		targetMaxVisualCol = max(0, targetVL.Length-1)
	}

	moveToVisualCol := e.computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol)

	// Set cursor position
	e.state.CursorLine = targetVL.LogicalLine
	targetCol := targetVL.StartCol + moveToVisualCol
	logicalLine := e.state.Lines[targetVL.LogicalLine]
	e.state.CursorCol = min(targetCol, len(logicalLine))

	// Snap cursor to atomic segment boundary (e.g. paste markers)
	// so the cursor never lands in the middle of a multi-grapheme unit.
	// Single-grapheme segments don't need snapping.
	segments := e.segment(logicalLine, "grapheme")
	for _, seg := range segments {
		if seg.Index > e.state.CursorCol {
			break
		}
		if len(seg.Segment) <= 1 {
			continue
		}
		if e.state.CursorCol < seg.Index+len(seg.Segment) {
			isContinuation := seg.Index < targetVL.StartCol
			isMovingDown := targetVisualLine > currentVisualLine

			if isContinuation && isMovingDown {
				// The segment started on a previous visual line, and we
				// already visited it on the way down. Skip all remaining
				// continuation VLs and land on the first VL past it.
				segEnd := seg.Index + len(seg.Segment)
				next := targetVisualLine + 1
				for next < len(visualLines) &&
					visualLines[next].LogicalLine == targetVL.LogicalLine &&
					visualLines[next].StartCol < segEnd {
					next++
				}
				if next < len(visualLines) {
					e.moveToVisualLine(visualLines, currentVisualLine, next)
					return
				}
			}

			// Snap to the start of the segment so it gets highlighted.
			// Store the pre-snap position so the next vertical move can
			// resolve it to the correct visual column.
			snapped := e.state.CursorCol
			e.snappedFromCursorCol = &snapped
			e.state.CursorCol = seg.Index
			return
		}
	}

	// No snap occurred – we moved out of the atomic segment.
	e.snappedFromCursorCol = nil
}

// computeVerticalMoveColumn computes the target visual column for vertical
// cursor movement. Implements the sticky column decision table:
//
//	| P | S | T | U | Scenario                                             | Set Preferred | Move To     |
//	|---|---|---|---| ---------------------------------------------------- |---------------|-------------|
//	| 0 | * | 0 | - | Start nav, target fits                               | null          | current     |
//	| 0 | * | 1 | - | Start nav, target shorter                            | current       | target end  |
//	| 1 | 0 | 0 | 0 | Clamped, target fits preferred                       | null          | preferred   |
//	| 1 | 0 | 0 | 1 | Clamped, target longer but still can't fit preferred | keep          | target end  |
//	| 1 | 0 | 1 | - | Clamped, target even shorter                         | keep          | target end  |
//	| 1 | 1 | 0 | - | Rewrapped, target fits current                       | null          | current     |
//	| 1 | 1 | 1 | - | Rewrapped, target shorter than current               | current       | target end  |
//
// Where:
//   - P = preferred col is set
//   - S = cursor in middle of source line (not clamped to end)
//   - T = target line shorter than current visual col
//   - U = target line shorter than preferred col
func (e *Editor) computeVerticalMoveColumn(currentVisualCol, sourceMaxVisualCol, targetMaxVisualCol int) int {
	hasPreferred := e.preferredVisualCol != nil             // P
	cursorInMiddle := currentVisualCol < sourceMaxVisualCol // S
	targetTooShort := targetMaxVisualCol < currentVisualCol // T

	if !hasPreferred || cursorInMiddle {
		if targetTooShort {
			// Cases 2 and 7
			preferred := currentVisualCol
			e.preferredVisualCol = &preferred
			return targetMaxVisualCol
		}

		// Cases 1 and 6
		e.preferredVisualCol = nil
		return currentVisualCol
	}

	targetCantFitPreferred := targetMaxVisualCol < *e.preferredVisualCol // U
	if targetTooShort || targetCantFitPreferred {
		// Cases 4 and 5
		return targetMaxVisualCol
	}

	// Case 3
	result := *e.preferredVisualCol
	e.preferredVisualCol = nil
	return result
}

func (e *Editor) moveToLineStart() {
	e.lastAction = ""
	e.setCursorCol(0)
}

func (e *Editor) moveToLineEnd() {
	e.lastAction = ""
	currentLine := e.state.Lines[e.state.CursorLine]
	e.setCursorCol(len(currentLine))
}

func (e *Editor) deleteToStartOfLine() {
	e.exitHistoryBrowsing()

	currentLine := e.state.Lines[e.state.CursorLine]

	if e.state.CursorCol > 0 {
		e.pushUndoSnapshot()

		// Calculate text to be deleted and save to kill ring (backward deletion = prepend)
		deletedText := currentLine[:e.state.CursorCol]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		// Delete from start of line up to cursor
		e.state.Lines[e.state.CursorLine] = currentLine[e.state.CursorCol:]
		e.setCursorCol(0)
	} else if e.state.CursorLine > 0 {
		e.pushUndoSnapshot()

		// At start of line - merge with previous line, treating newline as deleted text
		e.killRing.Push("\n", KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		previousLine := e.state.Lines[e.state.CursorLine-1]
		e.state.Lines[e.state.CursorLine-1] = previousLine + currentLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)
		e.state.CursorLine--
		e.setCursorCol(len(previousLine))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteToEndOfLine() {
	e.exitHistoryBrowsing()

	currentLine := e.state.Lines[e.state.CursorLine]

	if e.state.CursorCol < len(currentLine) {
		e.pushUndoSnapshot()

		// Calculate text to be deleted and save to kill ring (forward deletion = append)
		deletedText := currentLine[e.state.CursorCol:]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		// Delete from cursor to end of line
		e.state.Lines[e.state.CursorLine] = currentLine[:e.state.CursorCol]
	} else if e.state.CursorLine < len(e.state.Lines)-1 {
		e.pushUndoSnapshot()

		// At end of line - merge with next line, treating newline as deleted text
		e.killRing.Push("\n", KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
		e.lastAction = "kill"

		nextLine := e.state.Lines[e.state.CursorLine+1]
		e.state.Lines[e.state.CursorLine] = currentLine + nextLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordBackwards() {
	e.exitHistoryBrowsing()

	currentLine := e.state.Lines[e.state.CursorLine]

	// If at start of line, behave like backspace at column 0 (merge with previous line)
	if e.state.CursorCol == 0 {
		if e.state.CursorLine > 0 {
			e.pushUndoSnapshot()

			// Treat newline as deleted text (backward deletion = prepend)
			e.killRing.Push("\n", KillRingPushOptions{Prepend: true, Accumulate: e.lastAction == "kill"})
			e.lastAction = "kill"

			previousLine := e.state.Lines[e.state.CursorLine-1]
			e.state.Lines[e.state.CursorLine-1] = previousLine + currentLine
			e.state.Lines = append(e.state.Lines[:e.state.CursorLine], e.state.Lines[e.state.CursorLine+1:]...)
			e.state.CursorLine--
			e.setCursorCol(len(previousLine))
		}
	} else {
		e.pushUndoSnapshot()

		// Save lastAction before cursor movement (moveWordBackwards resets it)
		wasKill := e.lastAction == "kill"

		oldCursorCol := e.state.CursorCol
		e.moveWordBackwards()
		deleteFrom := e.state.CursorCol
		e.setCursorCol(oldCursorCol)

		deletedText := currentLine[deleteFrom:e.state.CursorCol]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: true, Accumulate: wasKill})
		e.lastAction = "kill"

		e.state.Lines[e.state.CursorLine] = currentLine[:deleteFrom] + currentLine[e.state.CursorCol:]
		e.setCursorCol(deleteFrom)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) deleteWordForward() {
	e.exitHistoryBrowsing()

	currentLine := e.state.Lines[e.state.CursorLine]

	// If at end of line, merge with next line (delete the newline)
	if e.state.CursorCol >= len(currentLine) {
		if e.state.CursorLine < len(e.state.Lines)-1 {
			e.pushUndoSnapshot()

			// Treat newline as deleted text (forward deletion = append)
			e.killRing.Push("\n", KillRingPushOptions{Prepend: false, Accumulate: e.lastAction == "kill"})
			e.lastAction = "kill"

			nextLine := e.state.Lines[e.state.CursorLine+1]
			e.state.Lines[e.state.CursorLine] = currentLine + nextLine
			e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
		}
	} else {
		e.pushUndoSnapshot()

		// Save lastAction before cursor movement (moveWordForwards resets it)
		wasKill := e.lastAction == "kill"

		oldCursorCol := e.state.CursorCol
		e.moveWordForwards()
		deleteTo := e.state.CursorCol
		e.setCursorCol(oldCursorCol)

		deletedText := currentLine[e.state.CursorCol:deleteTo]
		e.killRing.Push(deletedText, KillRingPushOptions{Prepend: false, Accumulate: wasKill})
		e.lastAction = "kill"

		e.state.Lines[e.state.CursorLine] = currentLine[:e.state.CursorCol] + currentLine[deleteTo:]
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) handleForwardDelete() {
	e.exitHistoryBrowsing()
	e.lastAction = ""

	currentLine := e.state.Lines[e.state.CursorLine]

	if e.state.CursorCol < len(currentLine) {
		e.pushUndoSnapshot()

		// Delete grapheme at cursor position (handles emojis, combining characters, etc.)
		afterCursor := currentLine[e.state.CursorCol:]

		// Find the first grapheme at cursor
		graphemes := e.segment(afterCursor, "grapheme")
		firstGrapheme := SegmentData{Segment: ""}
		if len(graphemes) > 0 {
			firstGrapheme = graphemes[0]
		}
		graphemeLength := len(firstGrapheme.Segment)
		if len(graphemes) == 0 {
			graphemeLength = 1
		}

		before := currentLine[:e.state.CursorCol]
		after := currentLine[e.state.CursorCol+graphemeLength:]
		e.state.Lines[e.state.CursorLine] = before + after
	} else if e.state.CursorLine < len(e.state.Lines)-1 {
		e.pushUndoSnapshot()

		// At end of line - merge with next line
		nextLine := e.state.Lines[e.state.CursorLine+1]
		e.state.Lines[e.state.CursorLine] = currentLine + nextLine
		e.state.Lines = append(e.state.Lines[:e.state.CursorLine+1], e.state.Lines[e.state.CursorLine+2:]...)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}

	// Update or re-trigger autocomplete after forward delete
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	} else {
		currentLine := e.state.Lines[e.state.CursorLine]
		textBeforeCursor := currentLine[:e.state.CursorCol]
		// Slash command context
		if e.isInSlashCommandContext(textBeforeCursor) {
			e.tryTriggerAutocomplete(false)
		} else if e.autocompleteTriggerPattern.MatchString(textBeforeCursor) {
			// Symbol-based completion context like @, #, or provider triggers
			e.tryTriggerAutocomplete(false)
		}
	}
}

// buildVisualLineMap builds a mapping from visual lines to logical positions.
// Returns a slice where each element represents a visual line with:
//   - LogicalLine: index into e.state.Lines
//   - StartCol: starting column in the logical line
//   - Length: length of this visual line segment
func (e *Editor) buildVisualLineMap(width int) []visualLine {
	visualLines := []visualLine{}

	for i := 0; i < len(e.state.Lines); i++ {
		line := e.state.Lines[i]
		lineVisWidth := VisibleWidth(line)
		if len(line) == 0 {
			// Empty line still takes one visual line
			visualLines = append(visualLines, visualLine{LogicalLine: i, StartCol: 0, Length: 0})
		} else if lineVisWidth <= width {
			visualLines = append(visualLines, visualLine{LogicalLine: i, StartCol: 0, Length: len(line)})
		} else {
			// Line needs wrapping - use word-aware wrapping
			chunks := WordWrapLine(line, width, e.segment(line, "grapheme"))
			for _, chunk := range chunks {
				visualLines = append(visualLines, visualLine{
					LogicalLine: i,
					StartCol:    chunk.StartIndex,
					Length:      chunk.EndIndex - chunk.StartIndex,
				})
			}
		}
	}

	return visualLines
}

// findVisualLineAt finds the visual line index that contains the given
// logical position.
func (e *Editor) findVisualLineAt(visualLines []visualLine, line, col int) int {
	for i := 0; i < len(visualLines); i++ {
		vl := visualLines[i]
		if vl.LogicalLine != line {
			continue
		}
		offset := col - vl.StartCol
		// Cursor is in this segment if it's within range. For the last
		// segment of a logical line, cursor can be at length (end position)
		isLastSegmentOfLine := i == len(visualLines)-1 || visualLines[i+1].LogicalLine != vl.LogicalLine
		if offset >= 0 && (offset < vl.Length || (isLastSegmentOfLine && offset == vl.Length)) {
			return i
		}
	}
	return len(visualLines) - 1
}

// findCurrentVisualLine finds the visual line index for the current cursor position.
func (e *Editor) findCurrentVisualLine(visualLines []visualLine) int {
	return e.findVisualLineAt(visualLines, e.state.CursorLine, e.state.CursorCol)
}

func (e *Editor) moveCursor(deltaLine, deltaCol int) {
	e.lastAction = ""
	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLine(visualLines)

	if deltaLine != 0 {
		targetVisualLine := currentVisualLine + deltaLine

		if targetVisualLine >= 0 && targetVisualLine < len(visualLines) {
			e.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine)
		}
	}

	if deltaCol != 0 {
		currentLine := e.state.Lines[e.state.CursorLine]

		if deltaCol > 0 {
			// Moving right - move by one grapheme (handles emojis, combining characters, etc.)
			if e.state.CursorCol < len(currentLine) {
				afterCursor := currentLine[e.state.CursorCol:]
				graphemes := e.segment(afterCursor, "grapheme")
				firstGrapheme := SegmentData{Segment: ""}
				if len(graphemes) > 0 {
					firstGrapheme = graphemes[0]
				}
				advance := len(firstGrapheme.Segment)
				if len(graphemes) == 0 {
					advance = 1
				}
				e.setCursorCol(e.state.CursorCol + advance)
			} else if e.state.CursorLine < len(e.state.Lines)-1 {
				// Wrap to start of next logical line
				e.state.CursorLine++
				e.setCursorCol(0)
			} else {
				// At end of last line - can't move, but set preferredVisualCol for up/down navigation
				if currentVisualLine < len(visualLines) {
					currentVL := visualLines[currentVisualLine]
					preferred := e.state.CursorCol - currentVL.StartCol
					e.preferredVisualCol = &preferred
				}
			}
		} else {
			// Moving left - move by one grapheme (handles emojis, combining characters, etc.)
			if e.state.CursorCol > 0 {
				beforeCursor := currentLine[:e.state.CursorCol]
				graphemes := e.segment(beforeCursor, "grapheme")
				lastGrapheme := SegmentData{Segment: ""}
				if len(graphemes) > 0 {
					lastGrapheme = graphemes[len(graphemes)-1]
				}
				retreat := len(lastGrapheme.Segment)
				if len(graphemes) == 0 {
					retreat = 1
				}
				e.setCursorCol(e.state.CursorCol - retreat)
			} else if e.state.CursorLine > 0 {
				// Wrap to end of previous logical line
				e.state.CursorLine--
				prevLine := e.state.Lines[e.state.CursorLine]
				e.setCursorCol(len(prevLine))
			}
		}
	}

	// Keep an open autocomplete picker in sync with the new cursor
	// position: cursor movement changes the text before the cursor, so a
	// picker computed for the old position is stale. Re-query so it
	// refreshes — or closes when the new position yields no suggestions —
	// mirroring insertCharacter()/handleBackspace(). Without this, arrowing
	// left from `/cmd ` back into the command name leaves the argument
	// picker showing against a `/cmd` prefix (and a Tab there would
	// concatenate the stale suggestion onto the partial command name).
	if e.autocompleteState != "" {
		e.updateAutocomplete()
	}
}

// pageScroll scrolls by a page (direction: -1 for up, 1 for down).
// Moves cursor by the page size while keeping it in bounds.
func (e *Editor) pageScroll(direction int) {
	e.lastAction = ""
	terminalRows := e.tui.Terminal().Rows()
	pageSize := terminalRows * 3 / 10
	if pageSize < 5 {
		pageSize = 5
	}

	visualLines := e.buildVisualLineMap(e.lastWidth)
	currentVisualLine := e.findCurrentVisualLine(visualLines)
	targetVisualLine := currentVisualLine + direction*pageSize
	if targetVisualLine > len(visualLines)-1 {
		targetVisualLine = len(visualLines) - 1
	}
	if targetVisualLine < 0 {
		targetVisualLine = 0
	}

	e.moveToVisualLine(visualLines, currentVisualLine, targetVisualLine)
}

func (e *Editor) moveWordBackwards() {
	e.lastAction = ""
	currentLine := e.state.Lines[e.state.CursorLine]

	// If at start of line, move to end of previous line
	if e.state.CursorCol == 0 {
		if e.state.CursorLine > 0 {
			e.state.CursorLine--
			prevLine := e.state.Lines[e.state.CursorLine]
			e.setCursorCol(len(prevLine))
		}
		return
	}

	e.setCursorCol(FindWordBackward(currentLine, e.state.CursorCol, &WordNavigationOptions{
		Segment:         func(text string) []SegmentData { return e.segment(text, "word") },
		IsAtomicSegment: isPasteMarker,
	}))
}

// yank pastes (yanks) the most recent kill ring entry at cursor position.
func (e *Editor) yank() {
	if e.killRing.Len() == 0 {
		return
	}

	e.pushUndoSnapshot()

	text, _ := e.killRing.Peek()
	e.insertYankedText(text)

	e.lastAction = "yank"
}

// yankPop cycles through kill ring (only works immediately after yank or
// yank-pop). Replaces the last yanked text with the previous entry in the ring.
func (e *Editor) yankPop() {
	// Only works if we just yanked and have more than one entry
	if e.lastAction != "yank" || e.killRing.Len() <= 1 {
		return
	}

	e.pushUndoSnapshot()

	// Delete the previously yanked text (still at end of ring before rotation)
	e.deleteYankedText()

	// Rotate the ring: move end to front
	e.killRing.Rotate()

	// Insert the new most recent entry (now at end after rotation)
	text, _ := e.killRing.Peek()
	e.insertYankedText(text)

	e.lastAction = "yank"
}

// insertYankedText inserts text at cursor position (used by yank operations).
func (e *Editor) insertYankedText(text string) {
	e.exitHistoryBrowsing()
	lines := strings.Split(text, "\n")

	if len(lines) == 1 {
		// Single line - insert at cursor
		currentLine := e.state.Lines[e.state.CursorLine]
		before := currentLine[:e.state.CursorCol]
		after := currentLine[e.state.CursorCol:]
		e.state.Lines[e.state.CursorLine] = before + text + after
		e.setCursorCol(e.state.CursorCol + len(text))
	} else {
		// Multi-line insert
		currentLine := e.state.Lines[e.state.CursorLine]
		before := currentLine[:e.state.CursorCol]
		after := currentLine[e.state.CursorCol:]

		// First line merges with text before cursor
		e.state.Lines[e.state.CursorLine] = before + lines[0]

		// Insert middle lines
		for i := 1; i < len(lines)-1; i++ {
			e.state.Lines = append(e.state.Lines, "")
			copy(e.state.Lines[e.state.CursorLine+i+1:], e.state.Lines[e.state.CursorLine+i:])
			e.state.Lines[e.state.CursorLine+i] = lines[i]
		}

		// Last line merges with text after cursor
		lastLineIndex := e.state.CursorLine + len(lines) - 1
		e.state.Lines = append(e.state.Lines, "")
		copy(e.state.Lines[lastLineIndex+1:], e.state.Lines[lastLineIndex:])
		e.state.Lines[lastLineIndex] = lines[len(lines)-1] + after

		// Update cursor position
		e.state.CursorLine = lastLineIndex
		e.setCursorCol(len(lines[len(lines)-1]))
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// deleteYankedText deletes the previously yanked text (used by yank-pop).
// The yanked text is derived from killRing[end] since it hasn't been rotated yet.
func (e *Editor) deleteYankedText() {
	yankedText, ok := e.killRing.Peek()
	if !ok {
		return
	}

	yankLines := strings.Split(yankedText, "\n")

	if len(yankLines) == 1 {
		// Single line - delete backward from cursor
		currentLine := e.state.Lines[e.state.CursorLine]
		deleteLen := len(yankedText)
		before := currentLine[:e.state.CursorCol-deleteLen]
		after := currentLine[e.state.CursorCol:]
		e.state.Lines[e.state.CursorLine] = before + after
		e.setCursorCol(e.state.CursorCol - deleteLen)
	} else {
		// Multi-line delete - cursor is at end of last yanked line
		startLine := e.state.CursorLine - (len(yankLines) - 1)
		startCol := len(e.state.Lines[startLine]) - len(yankLines[0])

		// Get text after cursor on current line
		afterCursor := e.state.Lines[e.state.CursorLine][e.state.CursorCol:]

		// Get text before yank start position
		beforeYank := e.state.Lines[startLine][:startCol]

		// Remove all lines from startLine to cursorLine and replace with merged line
		remaining := append([]string{beforeYank + afterCursor}, e.state.Lines[startLine+len(yankLines):]...)
		e.state.Lines = append(e.state.Lines[:startLine], remaining...)

		// Update cursor
		e.state.CursorLine = startLine
		e.setCursorCol(startCol)
	}

	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

func (e *Editor) pushUndoSnapshot() {
	e.undoStack.Push(editorSnapshot{State: e.state, Pastes: e.pastes, PasteCounter: e.pasteCounter})
}

func (e *Editor) undo() {
	e.exitHistoryBrowsing()
	snapshot, ok := e.undoStack.Pop()
	if !ok {
		return
	}
	e.state = snapshot.State
	e.pastes = snapshot.Pastes
	e.pasteCounter = snapshot.PasteCounter
	e.lastAction = ""
	e.preferredVisualCol = nil
	if e.OnChange != nil {
		e.OnChange(e.GetText())
	}
}

// jumpToChar jumps to the first occurrence of a character in the specified
// direction. Multi-line search. Case-sensitive. Skips the current cursor position.
func (e *Editor) jumpToChar(char string, direction string) {
	e.lastAction = ""
	isForward := direction == "forward"
	lines := e.state.Lines

	end := -1
	step := -1
	if isForward {
		end = len(lines)
		step = 1
	}

	for lineIdx := e.state.CursorLine; lineIdx != end; lineIdx += step {
		line := ""
		if lineIdx >= 0 && lineIdx < len(lines) {
			line = lines[lineIdx]
		}
		isCurrentLine := lineIdx == e.state.CursorLine

		if isForward {
			// pi: line.indexOf(char, cursorCol + 1) — skip the character under
			// the cursor. Search from the cursor; if the match starts exactly
			// at the cursor (the current grapheme), continue after it.
			searchFrom := 0
			if isCurrentLine {
				searchFrom = e.state.CursorCol
			}
			if searchFrom > len(line) {
				continue
			}
			idx := strings.Index(line[searchFrom:], char)
			if idx == 0 && isCurrentLine {
				if searchFrom+len(char) <= len(line) {
					idx = strings.Index(line[searchFrom+len(char):], char)
					if idx >= 0 {
						idx += searchFrom + len(char)
					}
				} else {
					idx = -1
				}
			} else if idx >= 0 {
				idx += searchFrom
			}
			if idx != -1 {
				e.state.CursorLine = lineIdx
				e.setCursorCol(idx)
				return
			}
		} else {
			// pi: line.lastIndexOf(char, cursorCol - 1) — occurrences starting
			// at or before cursorCol-1.
			if isCurrentLine {
				from := e.state.CursorCol - 1
				if from < 0 {
					from = 0
				}
				end := min(from+len(char), len(line))
				idx := strings.LastIndex(line[:end], char)
				if idx != -1 && idx <= from {
					e.state.CursorLine = lineIdx
					e.setCursorCol(idx)
					return
				}
			} else {
				idx := strings.LastIndex(line, char)
				if idx != -1 {
					e.state.CursorLine = lineIdx
					e.setCursorCol(idx)
					return
				}
			}
		}
	}
	// No match found - cursor stays in place
}

func (e *Editor) moveWordForwards() {
	e.lastAction = ""
	currentLine := e.state.Lines[e.state.CursorLine]

	// If at end of line, move to start of next line
	if e.state.CursorCol >= len(currentLine) {
		if e.state.CursorLine < len(e.state.Lines)-1 {
			e.state.CursorLine++
			e.setCursorCol(0)
		}
		return
	}

	e.setCursorCol(FindWordForward(currentLine, e.state.CursorCol, &WordNavigationOptions{
		Segment:         func(text string) []SegmentData { return e.segment(text, "word") },
		IsAtomicSegment: isPasteMarker,
	}))
}

// isSlashMenuAllowed reports whether the slash menu is allowed (only on the
// first line of the editor). Consumed by the autocomplete subsection (T2b).
func (e *Editor) isSlashMenuAllowed() bool {
	return e.state.CursorLine == 0
}

// isAtStartOfMessage checks if cursor is at start of message (for slash
// command detection). Consumed by the autocomplete subsection (T2b).
func (e *Editor) isAtStartOfMessage() bool {
	if !e.isSlashMenuAllowed() {
		return false
	}
	currentLine := e.state.Lines[e.state.CursorLine]
	beforeCursor := currentLine[:e.state.CursorCol]
	t := jsTrim(beforeCursor)
	return t == "" || t == "/"
}

// isInSlashCommandContext checks whether the text before the cursor opens a
// slash command. Consumed by the autocomplete subsection (T2b).
func (e *Editor) isInSlashCommandContext(textBeforeCursor string) bool {
	if !e.isSlashMenuAllowed() {
		return false
	}
	return strings.HasPrefix(jsTrimStart(textBeforeCursor), "/")
}

// =============================================================================
// Autocomplete subsection (pi: "// Autocomplete methods" block at the tail of
// editor.ts; dependencies autocomplete.ts and select-list.ts ported in the
// same T2b batch).
// =============================================================================

// editorAbortController is the autocomplete request's cancellation handle
// (pi: AbortController → ctx+cancel struct, repo refreshController pattern).
type editorAbortController struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newEditorAbortController() *editorAbortController {
	ctx, cancel := context.WithCancel(context.Background())
	return &editorAbortController{ctx: ctx, cancel: cancel}
}

// autocompleteItemFromSelect converts a SelectItem into an AutocompleteItem
// (pi: structural typing — SelectList items and autocomplete items are the
// same {value, label, description?} shape; Go needs an explicit conversion).
func autocompleteItemFromSelect(item SelectItem) AutocompleteItem {
	return AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}
}

// autocompleteRequestOptions mirrors pi's inline { force, explicitTab }
// request options.
type autocompleteRequestOptions struct {
	force       bool
	explicitTab bool
}

// getBestAutocompleteMatchIndex finds the best autocomplete item index for
// the given prefix. Returns -1 if no match is found.
//
// Match priority:
//  1. Exact match (prefix === item.value) -> always selected
//  2. Prefix match -> first item whose value starts with prefix
//  3. No match -> -1 (keep default highlight)
//
// Matching is case-sensitive and checks item.value only.
func (e *Editor) getBestAutocompleteMatchIndex(items []AutocompleteItem, prefix string) int {
	if prefix == "" {
		return -1
	}

	firstPrefixIndex := -1

	for i := range items {
		value := items[i].Value
		if value == prefix {
			return i // Exact match always wins
		}
		if firstPrefixIndex == -1 && strings.HasPrefix(value, prefix) {
			firstPrefixIndex = i
		}
	}

	return firstPrefixIndex
}

// createAutocompleteList builds the dropdown for a suggestion set (pi:
// createAutocompleteList). Must be called under the lock. pi passes
// autocomplete items straight into SelectList (structural typing); Go
// converts to SelectItem.
func (e *Editor) createAutocompleteList(prefix string, items []AutocompleteItem) *SelectList {
	var layout *SelectListLayoutOptions
	if strings.HasPrefix(prefix, "/") {
		layout = slashCommandSelectListLayout
	}
	selectItems := make([]SelectItem, len(items))
	for i, item := range items {
		selectItems[i] = SelectItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	list := NewSelectList(selectItems, e.autocompleteMaxVisible, e.theme.SelectList, layout)
	// v0.85.1: mouse clicks confirm the completion directly (pi: list.onSelect).
	list.OnSelect = func(selected SelectItem) {
		if e.autocompleteProvider == nil {
			return
		}
		e.pushUndoSnapshot()
		e.lastAction = ""
		result := e.autocompleteProvider.ApplyCompletion(
			e.state.Lines,
			e.state.CursorLine,
			e.state.CursorCol,
			autocompleteItemFromSelect(selected),
			e.autocompletePrefix,
		)
		e.state.Lines = result.Lines
		e.state.CursorLine = result.CursorLine
		e.setCursorCol(result.CursorCol)
		e.cancelAutocomplete()
		if e.OnChange != nil {
			e.OnChange(e.GetText())
		}
	}
	return list
}

// tryTriggerAutocomplete issues a regular autocomplete request (pi:
// tryTriggerAutocomplete). Must be called under the lock.
func (e *Editor) tryTriggerAutocomplete(explicitTab bool) {
	e.requestAutocomplete(autocompleteRequestOptions{force: false, explicitTab: explicitTab})
}

// handleTabCompletion routes Tab to slash-command or forced file completion
// (pi: handleTabCompletion). Must be called under the lock.
func (e *Editor) handleTabCompletion() {
	if e.autocompleteProvider == nil {
		return
	}

	currentLine := e.state.Lines[e.state.CursorLine]
	beforeCursor := currentLine[:e.state.CursorCol]

	if e.isInSlashCommandContext(beforeCursor) && !strings.Contains(jsTrimStart(beforeCursor), " ") {
		e.handleSlashCommandCompletion()
	} else {
		e.forceFileAutocomplete(true)
	}
}

// handleSlashCommandCompletion requests slash-command completion via Tab (pi:
// handleSlashCommandCompletion). Must be called under the lock.
func (e *Editor) handleSlashCommandCompletion() {
	e.requestAutocomplete(autocompleteRequestOptions{force: false, explicitTab: true})
}

// forceFileAutocomplete requests forced file completion (pi:
// forceFileAutocomplete). Must be called under the lock.
func (e *Editor) forceFileAutocomplete(explicitTab bool) {
	e.requestAutocomplete(autocompleteRequestOptions{force: true, explicitTab: explicitTab})
}

// requestAutocomplete cancels any pending request and schedules a new one,
// honoring the debounce delay (pi: requestAutocomplete). Must be called under
// the lock (main goroutine input paths).
func (e *Editor) requestAutocomplete(options autocompleteRequestOptions) {
	if e.autocompleteProvider == nil {
		return
	}

	if options.force {
		shouldTrigger := true
		if fc, ok := e.autocompleteProvider.(FileCompletionTriggerer); ok {
			shouldTrigger = fc.ShouldTriggerFileCompletion(e.state.Lines, e.state.CursorLine, e.state.CursorCol)
		}
		if !shouldTrigger {
			return
		}
	}

	e.cancelAutocompleteRequest()
	e.autocompleteStartToken++
	startToken := e.autocompleteStartToken

	debounceMs := e.getAutocompleteDebounceMs(options)
	if debounceMs > 0 {
		e.autocompleteDebounceTimer = time.AfterFunc(time.Duration(debounceMs)*time.Millisecond, func() {
			e.lockSync(func() {
				e.autocompleteDebounceTimer = nil
				e.startAutocompleteRequestLocked(startToken, options)
			})
		})
		return
	}

	e.startAutocompleteRequestLocked(startToken, options)
}

// startAutocompleteRequestLocked captures the task chain and spawns the
// request goroutine (pi: startAutocompleteRequest — the async body runs off
// the event loop; here off the editor lock). Must be called under the lock.
func (e *Editor) startAutocompleteRequestLocked(startToken int, options autocompleteRequestOptions) {
	previous := e.autocompleteRequestTask
	done := make(chan struct{})
	e.autocompleteRequestTask = done
	go func() {
		// pig panic containment: the fd/glob search continuation must not
		// kill the process. close(done) still runs first (waiters unblock).
		// See docs/feat/PANIC-RESILIENCE.md.
		defer utils.RecoverPanic("tui.editor.autocomplete")
		defer close(done)
		// pi: await previousTask — serialize behind the previous request.
		if previous != nil {
			<-previous
		}

		e.mu.Lock()
		if startToken != e.autocompleteStartToken || e.autocompleteProvider == nil {
			e.mu.Unlock()
			return
		}
		controller := newEditorAbortController()
		e.autocompleteAbort = controller
		e.autocompleteRequestId++
		requestId := e.autocompleteRequestId
		snapshotText := e.getTextLocked()
		snapshotLine := e.state.CursorLine
		snapshotCol := e.state.CursorCol
		provider := e.autocompleteProvider
		e.mu.Unlock()

		e.runAutocompleteRequest(provider, requestId, controller, snapshotText, snapshotLine, snapshotCol, options)
	}()
}

// setAutocompleteTriggerCharacters rebuilds the trigger set and patterns (pi:
// setAutocompleteTriggerCharacters). Must be called under the lock.
func (e *Editor) setAutocompleteTriggerCharacters(triggerCharacters []string) {
	next := append([]string{}, defaultAutocompleteTriggerCharacters...)
	for _, character := range triggerCharacters {
		duplicate := false
		for _, existing := range next {
			if existing == character {
				duplicate = true
				break
			}
		}
		// pi: character.length !== 1 (UTF-16) → rune count (repo convention;
		// astral characters differ, declared in autocomplete.go header).
		if utf8.RuneCountInString(character) != 1 || character == "/" || IsWhitespaceChar(character) || duplicate {
			continue
		}
		next = append(next, character)
	}
	e.autocompleteTriggerCharacters = next
	e.autocompleteTriggerPattern = buildTriggerPattern(next)
	e.autocompleteDebouncePattern = buildDebouncePattern(next)
}

// getAutocompleteDebounceMs returns the debounce delay for a request (pi:
// getAutocompleteDebounceMs). Must be called under the lock.
func (e *Editor) getAutocompleteDebounceMs(options autocompleteRequestOptions) int {
	if options.explicitTab || options.force {
		return 0
	}

	currentLine := e.state.Lines[e.state.CursorLine]
	textBeforeCursor := currentLine[:e.state.CursorCol]
	if e.autocompleteDebouncePattern.MatchString(textBeforeCursor) {
		return attachmentAutocompleteDebounceMs
	}
	return 0
}

// runAutocompleteRequest calls the provider and applies or cancels the
// result (pi: runAutocompleteRequest — async body). The provider call runs
// off the editor lock; every state access is under the lock (see file
// header "Concurrency adaptation").
func (e *Editor) runAutocompleteRequest(provider AutocompleteProvider, requestId int, controller *editorAbortController, snapshotText string, snapshotLine int, snapshotCol int, options autocompleteRequestOptions) {
	// pi reads live state at the await point; the editor passes a stable
	// snapshot copy so the unlocked provider call stays race-free.
	e.mu.Lock()
	lines := make([]string, len(e.state.Lines))
	copy(lines, e.state.Lines)
	cursorLine := e.state.CursorLine
	cursorCol := e.state.CursorCol
	e.mu.Unlock()

	suggestions := provider.GetSuggestions(controller.ctx, lines, cursorLine, cursorCol, AutocompleteGetSuggestionsOptions{Force: options.force})

	e.mu.Lock()
	if !e.isAutocompleteRequestCurrent(requestId, controller, snapshotText, snapshotLine, snapshotCol) {
		e.mu.Unlock()
		return
	}

	e.autocompleteAbort = nil

	if suggestions == nil || len(suggestions.Items) == 0 {
		e.cancelAutocomplete()
		e.mu.Unlock()
		e.tui.RequestRender(false)
		return
	}

	if options.force && options.explicitTab && len(suggestions.Items) == 1 {
		item := suggestions.Items[0]
		e.pushUndoSnapshot()
		e.lastAction = ""
		result := provider.ApplyCompletion(
			e.state.Lines,
			e.state.CursorLine,
			e.state.CursorCol,
			item,
			suggestions.Prefix,
		)
		e.state.Lines = result.Lines
		e.state.CursorLine = result.CursorLine
		e.setCursorCol(result.CursorCol)
		changed := e.OnChange != nil
		changedText := ""
		if changed {
			changedText = e.getTextLocked()
		}
		e.mu.Unlock()
		// pi fires onChange synchronously; here after unlocking so user
		// callbacks never run under the editor lock (file header).
		if changed {
			e.OnChange(changedText)
		}
		e.tui.RequestRender(false)
		return
	}

	state := "regular"
	if options.force {
		state = "force"
	}
	e.applyAutocompleteSuggestions(suggestions, state)
	e.mu.Unlock()
	e.tui.RequestRender(false)
}

// isAutocompleteRequestCurrent reports whether a finished request is still
// current (pi: isAutocompleteRequestCurrent). Must be called under the lock.
func (e *Editor) isAutocompleteRequestCurrent(requestId int, controller *editorAbortController, snapshotText string, snapshotLine int, snapshotCol int) bool {
	return controller.ctx.Err() == nil &&
		requestId == e.autocompleteRequestId &&
		e.getTextLocked() == snapshotText &&
		e.state.CursorLine == snapshotLine &&
		e.state.CursorCol == snapshotCol
}

// applyAutocompleteSuggestions installs a suggestion dropdown (pi:
// applyAutocompleteSuggestions). Must be called under the lock.
func (e *Editor) applyAutocompleteSuggestions(suggestions *AutocompleteSuggestions, state string) {
	e.autocompletePrefix = suggestions.Prefix
	e.autocompleteList = e.createAutocompleteList(suggestions.Prefix, suggestions.Items)

	bestMatchIndex := e.getBestAutocompleteMatchIndex(suggestions.Items, suggestions.Prefix)
	if bestMatchIndex >= 0 {
		e.autocompleteList.SetSelectedIndex(bestMatchIndex)
	}

	e.autocompleteState = state
}

// cancelAutocompleteRequest cancels the pending request and aborts the
// in-flight provider call (pi: cancelAutocompleteRequest). Must be called
// under the lock.
func (e *Editor) cancelAutocompleteRequest() {
	e.autocompleteStartToken++
	if e.autocompleteDebounceTimer != nil {
		e.autocompleteDebounceTimer.Stop()
		e.autocompleteDebounceTimer = nil
	}
	if e.autocompleteAbort != nil {
		e.autocompleteAbort.cancel()
		e.autocompleteAbort = nil
	}
}

// clearAutocompleteUi hides the dropdown (pi: clearAutocompleteUi). Must be
// called under the lock.
func (e *Editor) clearAutocompleteUi() {
	e.autocompleteState = ""
	e.autocompleteList = nil
	e.autocompletePrefix = ""
}

// cancelAutocomplete cancels the request and hides the dropdown (pi:
// cancelAutocomplete). Must be called under the lock.
func (e *Editor) cancelAutocomplete() {
	e.cancelAutocompleteRequest()
	e.clearAutocompleteUi()
}

// IsShowingAutocomplete reports whether the dropdown is open (pi:
// isShowingAutocomplete).
func (e *Editor) IsShowingAutocomplete() bool {
	var showing bool
	e.lock(func() { showing = e.autocompleteState != "" })
	return showing
}

// updateAutocomplete re-queries with the current state's force flag (pi:
// updateAutocomplete). Must be called under the lock.
func (e *Editor) updateAutocomplete() {
	if e.autocompleteState == "" || e.autocompleteProvider == nil {
		return
	}
	e.requestAutocomplete(autocompleteRequestOptions{force: e.autocompleteState == "force", explicitTab: false})
}
