// tui/alt_screen_search.go (pi: packages/tui/src/alt-screen-search.ts) —
// transcript search corpus/matching plus the bordered search overlay
// component with hoverable navigation buttons (v0.85.1).
//
// Porting notes (declared deviations):
//   - The search corpus and match indexes operate on the rune domain; pi
//     operates on UTF-16 code units. For BMP text (the entire test surface)
//     the two are equivalent; astral graphemes are one match unit here vs.
//     two code units in pi. Consistent with the repo-wide UTF-16 → rune
//     convention (truncate batch precedent).
package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/samber/lo"
)

// altScreenSearchSourceSpan locates a corpus run in the source screen
// (pi: SearchSourceSpan). textStart/textEnd delimit the run within the
// corpus text; linearColumns marks ASCII runs where columns advance 1:1
// with corpus characters.
type altScreenSearchSourceSpan struct {
	textStart     int
	textEnd       int
	row           int
	startCol      int
	endCol        int
	linearColumns bool
}

// AltScreenSearchSegment is one highlighted run within a source row (pi:
// AltScreenSearchSegment).
type AltScreenSearchSegment struct {
	Row      int
	StartCol int
	EndCol   int
}

// AltScreenSearchMatch is a query match spanning one or more segments (pi:
// AltScreenSearchMatch).
type AltScreenSearchMatch struct {
	Segments []AltScreenSearchSegment
}

type altScreenSearchCorpus struct {
	text  []rune
	spans []altScreenSearchSourceSpan
}

// altScreenSearchCorpusChunks accumulates corpus text chunks; text length is
// tracked in runes to keep span offsets aligned.
type altScreenSearchCorpusBuilder struct {
	chunks           []string
	spans            []altScreenSearchSourceSpan
	textLength       int
	pendingSeparator bool
}

func (b *altScreenSearchCorpusBuilder) appendSeparator() {
	if !b.pendingSeparator {
		return
	}
	b.chunks = append(b.chunks, " ")
	b.textLength++
	b.pendingSeparator = false
}

func (b *altScreenSearchCorpusBuilder) appendRun(text string, span altScreenSearchSourceSpan) {
	b.chunks = append(b.chunks, text)
	b.spans = append(b.spans, span)
	b.textLength += len([]rune(text))
}

// isPrintableAsciiRun mirrors pi's /^[\x20-\x7e]*$/ fast-path test.
func isPrintableAsciiRun(line string) bool {
	for i := 0; i < len(line); i++ {
		if line[i] < 0x20 || line[i] > 0x7e {
			return false
		}
	}
	return true
}

func buildSearchCorpus(lines []string) *altScreenSearchCorpus {
	builder := &altScreenSearchCorpusBuilder{}

	for row, line := range lines {
		stripped := StripTerminalSequences(line)
		column := 0

		// Rendered transcripts are overwhelmingly ASCII. Index complete
		// non-space runs at once instead of segmenting and allocating one
		// mapping per cell.
		if isPrintableAsciiRun(stripped) {
			index := 0
			for index < len(stripped) {
				if stripped[index] == 0x20 {
					if builder.textLength > 0 {
						builder.pendingSeparator = true
					}
					column++
					index++
					continue
				}
				end := index + 1
				for end < len(stripped) && stripped[end] != 0x20 {
					end++
				}
				builder.appendSeparator()
				text := stripped[index:end]
				builder.appendRun(text, altScreenSearchSourceSpan{
					textStart:     builder.textLength,
					textEnd:       builder.textLength + len(text),
					row:           row,
					startCol:      column,
					endCol:        column + len(text),
					linearColumns: true,
				})
				column += len(text)
				index = end
			}
		} else {
			for _, text := range GetGraphemeSegmenter().Segments(stripped) {
				width := VisibleWidth(text)
				if isAllJsSpace(text) {
					if builder.textLength > 0 {
						builder.pendingSeparator = true
					}
					column += width
					continue
				}
				builder.appendSeparator()
				runes := len([]rune(text))
				builder.appendRun(text, altScreenSearchSourceSpan{
					textStart:     builder.textLength,
					textEnd:       builder.textLength + runes,
					row:           row,
					startCol:      column,
					endCol:        column + width,
					linearColumns: false,
				})
				column += width
			}
		}
		if builder.textLength > 0 {
			builder.pendingSeparator = true
		}
	}

	return &altScreenSearchCorpus{text: []rune(strings.Join(builder.chunks, "")), spans: builder.spans}
}

// isAllJsSpace mirrors `/^\s+$/u.test(text)` with the ECMAScript \s set.
func isAllJsSpace(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if !isJsSpace(r) {
			return false
		}
	}
	return true
}

func normalizeAltScreenQuery(query string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range query {
		if isJsSpace(r) {
			inSpace = true
			continue
		}
		if inSpace {
			b.WriteRune(' ')
			inSpace = false
		}
		b.WriteRune(r)
	}
	if inSpace {
		b.WriteRune(' ')
	}
	return jsTrim(b.String())
}

var altScreenRegExpEscaper = strings.NewReplacer(
	".", `\.`, "*", `\*`, "+", `\+`, "?", `\?`, "^", `\^`, "$", `\$`,
	"{", `\{`, "}", `\}`, "(", `\(`, ")", `\)`, "|", `\|`, "[", `\[`,
	"]", `\]`, `\`, `\\`,
)

func escapeRegExp(text string) string {
	return altScreenRegExpEscaper.Replace(text)
}

// findSearchCorpusMatches finds all matches of the normalized query in the
// corpus, mapping corpus offsets back to source segments via the span list
// (pi: findSearchCorpusMatches).
func findSearchCorpusMatches(corpus *altScreenSearchCorpus, normalizedQuery string) []AltScreenSearchMatch {
	if normalizedQuery == "" {
		return []AltScreenSearchMatch{}
	}
	expression := regexp.MustCompile("(?i)" + escapeRegExp(normalizedQuery))
	matches := []AltScreenSearchMatch{}
	spanIndex := 0

	for _, loc := range expression.FindAllStringIndex(string(corpus.text), -1) {
		start, end := loc[0], loc[1]
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= start {
			spanIndex++
		}

		var segments []AltScreenSearchSegment
		for index := spanIndex; index < len(corpus.spans); index++ {
			span := corpus.spans[index]
			if span.textStart >= end {
				break
			}
			if span.textEnd <= start {
				continue
			}
			startCol := span.startCol
			if span.linearColumns {
				overlapStart := start
				if span.textStart > overlapStart {
					overlapStart = span.textStart
				}
				startCol = span.startCol + (overlapStart - span.textStart)
			}
			endCol := span.endCol
			if span.linearColumns {
				overlapEnd := end
				if span.textEnd < overlapEnd {
					overlapEnd = span.textEnd
				}
				endCol = span.startCol + (overlapEnd - span.textStart)
			}
			if n := len(segments); n > 0 {
				previous := &segments[n-1]
				if previous.Row == span.row && startCol <= previous.EndCol {
					if endCol > previous.EndCol {
						previous.EndCol = endCol
					}
					continue
				}
			}
			segments = append(segments, AltScreenSearchSegment{Row: span.row, StartCol: startCol, EndCol: endCol})
		}
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= end {
			spanIndex++
		}
		if len(segments) > 0 {
			matches = append(matches, AltScreenSearchMatch{Segments: segments})
		}
	}

	return matches
}

// AltScreenSearchResult carries the matches plus whether they changed since
// the previous search call (pi: AltScreenSearchResult).
type AltScreenSearchResult struct {
	Matches []AltScreenSearchMatch
	Changed bool
}

// AltScreenSearchIndex caches the searchable corpus and matches while
// rendered transcript lines remain unchanged (pi: AltScreenSearchIndex).
type AltScreenSearchIndex struct {
	sourceLines     []string
	corpus          *altScreenSearchCorpus
	normalizedQuery string
	matches         []AltScreenSearchMatch
}

// NewAltScreenSearchIndex creates the search index.
func NewAltScreenSearchIndex() *AltScreenSearchIndex {
	return &AltScreenSearchIndex{}
}

// Search runs the query against lines, reusing the cached corpus when the
// source lines are unchanged (pi: AltScreenSearchIndex.search).
func (idx *AltScreenSearchIndex) Search(lines []string, query string) AltScreenSearchResult {
	sourceChanged := idx.sourceLines != nil && len(idx.sourceLines) != len(lines)
	if !sourceChanged && idx.sourceLines != nil {
		for index := range lines {
			if idx.sourceLines[index] == lines[index] {
				continue
			}
			sourceChanged = true
			break
		}
	}
	if sourceChanged || idx.corpus == nil {
		idx.sourceLines = append([]string(nil), lines...)
		idx.corpus = buildSearchCorpus(lines)
	}

	normalizedQuery := normalizeAltScreenQuery(query)
	changed := sourceChanged || normalizedQuery != idx.normalizedQuery
	if changed {
		idx.normalizedQuery = normalizedQuery
		idx.matches = findSearchCorpusMatches(idx.corpus, normalizedQuery)
	}
	return AltScreenSearchResult{Matches: idx.matches, Changed: changed}
}

// FindAltScreenSearchMatches finds all matches of query in the normalized
// rendered transcript text without caching (pi: findAltScreenSearchMatches).
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	normalizedQuery := normalizeAltScreenQuery(query)
	if normalizedQuery == "" {
		return []AltScreenSearchMatch{}
	}
	return findSearchCorpusMatches(buildSearchCorpus(lines), normalizedQuery)
}

// GetAltScreenSearchMatchKey returns the stable identity key of a match (pi:
// getAltScreenSearchMatchKey).
func GetAltScreenSearchMatchKey(match AltScreenSearchMatch) string {
	if len(match.Segments) == 0 {
		return ""
	}
	first := match.Segments[0]
	last := match.Segments[len(match.Segments)-1]
	return strconv.Itoa(first.Row) + ":" + strconv.Itoa(first.StartCol) + ":" +
		strconv.Itoa(last.Row) + ":" + strconv.Itoa(last.EndCol)
}

// formatAltScreenSearchKeyLabel formats a keybinding id for display: Title
// Case parts, with Alt mapped to Option on macOS (pi: formatKey).
func formatAltScreenSearchKeyLabel(key string) string {
	if key == "" {
		return "Unbound"
	}
	parts := strings.Split(key, "+")
	for i, part := range parts {
		if altScreenPlatform == "darwin" && strings.EqualFold(part, "alt") {
			parts[i] = "Option"
			continue
		}
		if part != "" {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "+")
}

// AltScreenSearchComponent is the bordered search overlay input with
// navigation buttons (pi: AltScreenSearchComponent implements Component,
// Focusable).
type AltScreenSearchComponent struct {
	input                      *Input
	onQueryChange              func(query string)
	navigationButtonStyle      func(text string, hovered bool) string
	resultCount                int
	resultIndex                int
	previousButtonStart        int
	previousButtonEnd          int
	nextButtonStart            int
	nextButtonEnd              int
	hoveredNavigationDirection int // -1 | 1 | 0 (0 = none)
	focused                    bool
}

// NewAltScreenSearchComponent creates the search component (pi constructor;
// navigationButtonStyle may be nil for identity styling).
func NewAltScreenSearchComponent(onQueryChange func(query string), navigationButtonStyle func(text string, hovered bool) string) *AltScreenSearchComponent {
	if navigationButtonStyle == nil {
		navigationButtonStyle = func(text string, hovered bool) string { return text }
	}
	input := NewInput(&InputOptions{
		Prompt:           lo.ToPtr(" "),
		Placeholder:      "Find in transcript",
		PlaceholderStyle: func(text string) string { return "\x1b[2m" + text + "\x1b[22m" },
	})
	return &AltScreenSearchComponent{
		input:                 input,
		onQueryChange:         onQueryChange,
		navigationButtonStyle: navigationButtonStyle,
		resultIndex:           -1,
		previousButtonStart:   -1,
		previousButtonEnd:     -1,
		nextButtonStart:       -1,
		nextButtonEnd:         -1,
	}
}

// GetFocused implements Focusable.
func (c *AltScreenSearchComponent) GetFocused() bool { return c.focused }

// SetFocused implements Focusable.
func (c *AltScreenSearchComponent) SetFocused(value bool) {
	c.focused = value
	c.input.SetFocused(value)
}

// SetResult records the match status shown in the search box (pi: setResult).
func (c *AltScreenSearchComponent) SetResult(index int, count int) {
	c.resultIndex = index
	c.resultCount = count
}

// GetNavigationDirectionAt reports which navigation button covers the given
// component-local cell (pi: getNavigationDirectionAt). Row 2 is the bottom
// border row where the buttons render. Returns -1 (previous), 1 (next), or
// 0 (none).
func (c *AltScreenSearchComponent) GetNavigationDirectionAt(row int, column int) int {
	if row != 2 {
		return 0
	}
	if column >= c.previousButtonStart && column < c.previousButtonEnd {
		return -1
	}
	if column >= c.nextButtonStart && column < c.nextButtonEnd {
		return 1
	}
	return 0
}

// SetHoveredNavigationDirection updates the hovered button and reports
// whether the hover state changed (pi: setHoveredNavigationDirection).
// direction: -1 | 1 | 0 (none).
func (c *AltScreenSearchComponent) SetHoveredNavigationDirection(direction int) bool {
	if direction == c.hoveredNavigationDirection {
		return false
	}
	c.hoveredNavigationDirection = direction
	return true
}

// HandleInput implements InputHandler.
func (c *AltScreenSearchComponent) HandleInput(data string) {
	previous := c.input.GetValue()
	c.input.HandleInput(data)
	query := c.input.GetValue()
	if query != previous && c.onQueryChange != nil {
		c.onQueryChange(query)
	}
}

// Invalidate implements Component.
func (c *AltScreenSearchComponent) Invalidate() {
	c.input.Invalidate()
}

// Render implements Component (pi: render — bordered box with the input on
// row 1 and the navigation buttons embedded in the bottom border).
func (c *AltScreenSearchComponent) Render(width int) []string {
	safeWidth := maxInt(1, width)
	innerWidth := maxInt(0, safeWidth-2)
	keybindings := GetKeybindings()
	previousKey := ""
	if keys := keybindings.GetKeys("tui.altScreen.searchPrevious"); len(keys) > 0 {
		previousKey = formatAltScreenSearchKeyLabel(string(keys[0]))
	}
	nextKey := ""
	if keys := keybindings.GetKeys("tui.altScreen.searchNext"); len(keys) > 0 {
		nextKey = formatAltScreenSearchKeyLabel(string(keys[0]))
	}
	query := c.input.GetValue()
	result := ""
	if query != "" {
		if c.resultCount == 0 {
			result = "No matches"
		} else {
			result = strconv.Itoa(c.resultIndex+1) + "/" + strconv.Itoa(c.resultCount)
		}
	}
	resultSpace := maxInt(0, innerWidth-3)
	visibleResult := TruncateToWidth(result, resultSpace, "", false)
	resultText := ""
	if visibleResult != "" {
		resultText = "\x1b[2m " + visibleResult + " \x1b[22m"
	}
	inputWidth := maxInt(0, innerWidth-VisibleWidth(resultText))
	var inputLine string
	if rendered := c.input.Render(maxInt(1, inputWidth)); len(rendered) > 0 {
		inputLine = TruncateToWidth(rendered[0], inputWidth, "", false)
	}
	inputPadding := strings.Repeat(" ", maxInt(0, inputWidth-VisibleWidth(inputLine)))
	content := inputLine + inputPadding + resultText

	previousButton := "↑ " + previousKey
	nextButton := "↓ " + nextKey
	separator := " · "
	outerGapWidth := 1
	availableControlsWidth := maxInt(0, innerWidth-outerGapWidth*2-1)
	controlsWidth := VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	if controlsWidth > availableControlsWidth {
		previousButton = "↑"
		nextButton = "↓"
		separator = " "
		controlsWidth = VisibleWidth(previousButton) + VisibleWidth(separator) + VisibleWidth(nextButton)
	}
	showButtons := controlsWidth <= availableControlsWidth
	renderedButtons := ""
	if showButtons {
		renderedButtons = c.navigationButtonStyle(previousButton, c.hoveredNavigationDirection == -1) +
			separator +
			c.navigationButtonStyle(nextButton, c.hoveredNavigationDirection == 1)
	}
	outerGapsWidth := 0
	if showButtons {
		outerGapsWidth = outerGapWidth * 2
	}
	rightRuleWidth := 0
	if renderedButtons != "" && innerWidth > controlsWidth+outerGapsWidth {
		rightRuleWidth = 1
	}
	leftRuleWidth := maxInt(0, innerWidth-(map[bool]int{true: controlsWidth, false: 0}[showButtons])-outerGapsWidth-rightRuleWidth)
	previousStart := 1 + leftRuleWidth + outerGapWidth
	if showButtons {
		c.previousButtonStart = previousStart
		c.previousButtonEnd = previousStart + VisibleWidth(previousButton)
		c.nextButtonStart = c.previousButtonEnd + VisibleWidth(separator)
		c.nextButtonEnd = c.nextButtonStart + VisibleWidth(nextButton)
	} else {
		c.previousButtonStart = -1
		c.previousButtonEnd = -1
		c.nextButtonStart = -1
		c.nextButtonEnd = -1
	}

	if safeWidth == 1 {
		return []string{"┌", "│", "└"}
	}
	buttonGap := ""
	if renderedButtons != "" {
		buttonGap = " "
	}
	bottomBorder := "└" + strings.Repeat("─", leftRuleWidth) + buttonGap + renderedButtons + buttonGap + strings.Repeat("─", rightRuleWidth) + "┘"
	return []string{
		"┌" + strings.Repeat("─", innerWidth) + "┐",
		"│" + content + "│",
		bottomBorder,
	}
}
