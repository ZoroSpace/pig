// diff.go (pi: src/modes/interactive/components/diff.ts) — renderDiff:
// colored diff text with word-level intra-line change highlighting.
//
// Declared deviation: pi keeps this file in modes/interactive/components,
// but its only consumer is core/tools' edit tool renderer and core/tools
// cannot import components (components → core/tools for tool-execution's
// builtin-definition fallback would cycle). Consumed from core/tools by
// edit.go; pi's components/index.ts re-export has no Go counterpart.
package tools

import (
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/agent/harness/tools"
)

// diffLeadingWsRe mirrors JS /^\s*/ on the first diff part (diff.ts's
// inline leading-whitespace strip).
var diffLeadingWsRe = regexp.MustCompile(`^[\s]*`)

// diffLineRe is diff.ts's parseDiffLine regex: prefix, optional line
// number, and content ("+123 content" / "-123 content" / " 123 content" /
// "     ...").
var diffLineRe = regexp.MustCompile(`^([+-]|[[:space:]])([[:space:]]*[0-9]*)[[:space:]](.*)$`)

type parsedDiffLine struct {
	prefix  string
	lineNum string
	content string
}

// parseDiffLine splits one diff line; nil when the line doesn't match.
func parseDiffLine(line string) *parsedDiffLine {
	m := diffLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	return &parsedDiffLine{prefix: m[1], lineNum: m[2], content: m[3]}
}

// replaceTabsDiff replaces tabs with spaces for consistent rendering (pi:
// diff.ts's local replaceTabs duplicate of render-utils').
func replaceTabsDiff(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

// renderIntraLineDiff computes the word-level diff and renders changed
// parts plain (pi: renderIntraLineDiff, which uses inverse video).
// pig-only deviation (2026-10-12, user request, two-step convergence):
// inverse renders as a colored BACKGROUND on the terminal (the exact look
// the BLOCK-BORDERS rework removed), and bold on the changed tokens was
// also declined — the whole line keeps its single fg hue with no intra-line
// emphasis. The word diff is kept for structural parity and because the
// reconstruction concatenates back to the original content. diffWords
// groups whitespace with adjacent words; leading whitespace is stripped
// from the first changed part so indentation handling stays pi-shaped.
func renderIntraLineDiff(theme UITheme, oldContent string, newContent string) (string, string) {
	wordDiff := tools.DiffWords(oldContent, newContent)

	var removedLine, addedLine strings.Builder
	isFirstRemoved := true
	isFirstAdded := true

	for _, part := range wordDiff {
		if part.Removed {
			value := part.Value
			if isFirstRemoved {
				leadingWs := diffLeadingWsRe.FindString(value)
				value = value[len(leadingWs):]
				removedLine.WriteString(leadingWs)
				isFirstRemoved = false
			}
			if value != "" {
				removedLine.WriteString(value)
			}
		} else if part.Added {
			value := part.Value
			if isFirstAdded {
				leadingWs := diffLeadingWsRe.FindString(value)
				value = value[len(leadingWs):]
				addedLine.WriteString(leadingWs)
				isFirstAdded = false
			}
			if value != "" {
				addedLine.WriteString(value)
			}
		} else {
			removedLine.WriteString(part.Value)
			addedLine.WriteString(part.Value)
		}
	}

	return removedLine.String(), addedLine.String()
}

// RenderDiffOptions carries the renderDiff options (pi: RenderDiffOptions;
// filePath is unused, kept for API compatibility).
type RenderDiffOptions struct {
	FilePath string
}

// RenderDiff renders a diff string with colored lines (pi: renderDiff,
// with the pig-only no-intra-line-emphasis deviation noted on
// renderIntraLineDiff):
//   - context lines: toolDiffContext
//   - removed lines: toolDiffRemoved, plain fg (no intra-line emphasis)
//   - added lines: toolDiffAdded, plain fg (no intra-line emphasis)
func RenderDiff(theme UITheme, diffText string, options RenderDiffOptions) string {
	_ = options
	lines := strings.Split(diffText, "\n")
	result := make([]string, 0, len(lines))

	i := 0
	for i < len(lines) {
		line := lines[i]
		parsed := parseDiffLine(line)

		if parsed == nil {
			result = append(result, theme.Fg("toolDiffContext", line))
			i++
			continue
		}

		if parsed.prefix == "-" {
			// Collect consecutive removed lines
			type numberedLine struct {
				lineNum string
				content string
			}
			var removedLines []numberedLine
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removedLines = append(removedLines, numberedLine{lineNum: p.lineNum, content: p.content})
				i++
			}

			// Collect consecutive added lines
			var addedLines []numberedLine
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				addedLines = append(addedLines, numberedLine{lineNum: p.lineNum, content: p.content})
				i++
			}

			// Only do intra-line diffing when there's exactly one removed
			// and one added line (indicating a single line modification).
			// Otherwise, show lines as-is.
			if len(removedLines) == 1 && len(addedLines) == 1 {
				removed := removedLines[0]
				added := addedLines[0]

				removedLine, addedLine := renderIntraLineDiff(
					theme,
					replaceTabsDiff(removed.content),
					replaceTabsDiff(added.content),
				)

				result = append(result, theme.Fg("toolDiffRemoved", "-"+removed.lineNum+" "+removedLine))
				result = append(result, theme.Fg("toolDiffAdded", "+"+added.lineNum+" "+addedLine))
			} else {
				// Show all removed lines first, then all added lines
				for _, removed := range removedLines {
					result = append(result, theme.Fg("toolDiffRemoved", "-"+removed.lineNum+" "+replaceTabsDiff(removed.content)))
				}
				for _, added := range addedLines {
					result = append(result, theme.Fg("toolDiffAdded", "+"+added.lineNum+" "+replaceTabsDiff(added.content)))
				}
			}
		} else if parsed.prefix == "+" {
			// Standalone added line
			result = append(result, theme.Fg("toolDiffAdded", "+"+parsed.lineNum+" "+replaceTabsDiff(parsed.content)))
			i++
		} else {
			// Context line
			result = append(result, theme.Fg("toolDiffContext", " "+parsed.lineNum+" "+replaceTabsDiff(parsed.content)))
			i++
		}
	}

	return strings.Join(result, "\n")
}
