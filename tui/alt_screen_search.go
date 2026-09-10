// tui/alt_screen_search.go (pi: packages/tui/src/alt-screen-search.ts)
//
// Porting note (declared deviation): the search corpus and match indexes
// operate on the rune domain; pi operates on UTF-16 code units. For BMP text
// (the entire test surface) the two are equivalent; astral graphemes are one
// match unit here vs. two code units in pi. Consistent with the repo-wide
// UTF-16 → rune convention (truncate batch precedent).
package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// altScreenSearchSourceSpan locates a corpus character in the source screen.
type altScreenSearchSourceSpan struct {
	row      int
	startCol int
	endCol   int
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
	text   []rune
	source []*altScreenSearchSourceSpan
}

func appendMappedText(text string, span *altScreenSearchSourceSpan, corpus *altScreenSearchCorpus) {
	for _, r := range text {
		corpus.text = append(corpus.text, r)
		corpus.source = append(corpus.source, span)
	}
}

func buildSearchCorpus(lines []string) *altScreenSearchCorpus {
	corpus := &altScreenSearchCorpus{}
	pendingSeparator := false

	segmenter := GetGraphemeSegmenter()
	for row, line := range lines {
		stripped := StripTerminalSequences(line)
		column := 0
		for _, text := range segmenter.Segments(stripped) {
			width := VisibleWidth(text)
			if isAllJsSpace(text) {
				if len(corpus.text) > 0 {
					pendingSeparator = true
				}
				column += width
				continue
			}
			if pendingSeparator {
				appendMappedText(" ", nil, corpus)
				pendingSeparator = false
			}
			appendMappedText(text, &altScreenSearchSourceSpan{row: row, startCol: column, endCol: column + width}, corpus)
			column += width
		}
		if len(corpus.text) > 0 {
			pendingSeparator = true
		}
	}

	return corpus
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

// FindAltScreenSearchMatches finds all matches of query in the normalized
// rendered transcript text (pi: findAltScreenSearchMatches).
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	normalizedQuery := normalizeAltScreenQuery(query)
	if normalizedQuery == "" {
		return nil
	}

	corpus := buildSearchCorpus(lines)
	expression := regexp.MustCompile("(?i)" + escapeRegExp(normalizedQuery))
	matches := []AltScreenSearchMatch{}

	for _, loc := range expression.FindAllStringIndex(string(corpus.text), -1) {
		start, end := loc[0], loc[1]
		var segments []AltScreenSearchSegment
		for index := start; index < end; index++ {
			span := corpus.source[index]
			if span == nil {
				continue
			}
			if n := len(segments); n > 0 {
				previous := &segments[n-1]
				if previous.Row == span.row && span.startCol <= previous.EndCol {
					previous.EndCol = maxInt(previous.EndCol, span.endCol)
					continue
				}
			}
			segments = append(segments, AltScreenSearchSegment{Row: span.row, StartCol: span.startCol, EndCol: span.endCol})
		}
		if len(segments) > 0 {
			matches = append(matches, AltScreenSearchMatch{Segments: segments})
		}
	}

	return matches
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

// AltScreenSearchComponent is the search query overlay input (pi:
// AltScreenSearchComponent implements Component, Focusable).
type AltScreenSearchComponent struct {
	input         *Input
	onQueryChange func(query string)
	resultCount   int
	resultIndex   int
	focused       bool
}

// NewAltScreenSearchComponent creates the search component.
func NewAltScreenSearchComponent(onQueryChange func(query string)) *AltScreenSearchComponent {
	return &AltScreenSearchComponent{input: NewInput(), onQueryChange: onQueryChange, resultIndex: -1}
}

// GetFocused implements Focusable.
func (c *AltScreenSearchComponent) GetFocused() bool { return c.focused }

// SetFocused implements Focusable.
func (c *AltScreenSearchComponent) SetFocused(value bool) {
	c.focused = value
	c.input.SetFocused(value)
}

// SetResult records the match status shown in the title bar (pi: setResult).
func (c *AltScreenSearchComponent) SetResult(index int, count int) {
	c.resultIndex = index
	c.resultCount = count
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

// Render implements Component.
func (c *AltScreenSearchComponent) Render(width int) []string {
	safeWidth := maxInt(1, width)
	label := " Find transcript"
	query := c.input.GetValue()
	status := ""
	if query != "" {
		if c.resultCount == 0 {
			status = "No matches "
		} else {
			status = strconv.Itoa(c.resultIndex+1) + "/" + strconv.Itoa(c.resultCount) + " "
		}
	}
	labelWidth := VisibleWidth(label)
	statusWidth := VisibleWidth(status)
	gap := strings.Repeat(" ", maxInt(1, safeWidth-labelWidth-statusWidth))
	title := TruncateToWidth(label+gap+status, safeWidth, "", false)
	padding := strings.Repeat(" ", maxInt(0, safeWidth-VisibleWidth(title)))
	return append([]string{"\x1b[7m" + title + padding + "\x1b[27m"}, c.input.Render(safeWidth)...)
}
