// pi: src/harness/tools/edit-diff.ts — shared diff computation utilities for
// the edit and similar tools.
//
// Segment deviations:
//   - jsdiff's createTwoFilesPatch/diffLines are vendored in jsdiff.go (see
//     its header for the pin); the pi functions below call that port.
//   - normalizeForFuzzyMatch's per-line trimEnd uses Go unicode.IsSpace,
//     which covers the ASCII whitespace JS trimEnd strips plus Unicode
//     spaces; pi's file contents are UTF-8 text where the difference only
//     shows for exotic separators (declared here).
//   - The union return `"\r\n" | "\n"` collapses to string; "" is never
//     returned (pi always returns one of the two).
package tools

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// DetectLineEnding reports whether the content uses CRLF (pi:
// detectLineEnding).
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF converts CRLF and lone CR line endings to LF (pi:
// normalizeToLF).
func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// RestoreLineEndings converts LF back to the original ending (pi:
// restoreLineEndings).
func RestoreLineEndings(text string, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// fuzzy quote/dash/space normalizations (pi: the four replace chains of
// normalizeForFuzzyMatch).
var (
	smartSingleQuotes = strings.NewReplacer("\u2018", "'", "\u2019", "'", "\u201A", "'", "\u201B", "'")
	smartDoubleQuotes = strings.NewReplacer("\u201C", "\"", "\u201D", "\"", "\u201E", "\"", "\u201F", "\"")
	unicodeDashes     = strings.NewReplacer(
		"\u2010", "-", "\u2011", "-", "\u2012", "-", "\u2013", "-",
		"\u2014", "-", "\u2015", "-", "\u2212", "-")
	unicodeSpaces = strings.NewReplacer(
		"\u00A0", " ", "\u2002", " ", "\u2003", " ", "\u2004", " ",
		"\u2005", " ", "\u2006", " ", "\u2007", " ", "\u2008", " ",
		"\u2009", " ", "\u200A", " ", "\u202F", " ", "\u205F", " ",
		"\u3000", " ")
)

// trimEndLine strips trailing whitespace from one line (JS trimEnd).
func trimEndLine(line string) string {
	return strings.TrimRightFunc(line, unicode.IsSpace)
}

// NormalizeForFuzzyMatch normalizes text for fuzzy matching (pi:
// normalizeForFuzzyMatch): NFKC, per-line trailing-whitespace strip, smart
// quotes/dashes/spaces to ASCII.
func NormalizeForFuzzyMatch(text string) string {
	normalized := norm.NFKC.String(text)
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		lines[i] = trimEndLine(line)
	}
	normalized = strings.Join(lines, "\n")
	normalized = smartSingleQuotes.Replace(normalized)
	normalized = smartDoubleQuotes.Replace(normalized)
	normalized = unicodeDashes.Replace(normalized)
	normalized = unicodeSpaces.Replace(normalized)
	return normalized
}

// splitLinesWithEndings splits content into line chunks keeping their
// trailing newline; the final line may lack one (pi:
// splitLinesWithEndings, via the /[^\n]*\n|[^\n]+/g match).
func splitLinesWithEndings(content string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

// LineSpan is the [start, end) byte range of one line (pi: LineSpan; TS
// offsets are UTF-16 code units, Go uses byte offsets — all consumers
// operate on the same Go strings, so the unit is internally consistent).
type LineSpan struct {
	Start int
	End   int
}

// MatchedEdit is one located edit (pi: MatchedEdit).
type MatchedEdit struct {
	EditIndex   int
	MatchIndex  int
	MatchLength int
	NewText     string
}

// TextReplacement is the subset of MatchedEdit used for application (pi:
// TextReplacement).
type TextReplacement struct {
	MatchIndex  int
	MatchLength int
	NewText     string
}

// getLineSpans computes the spans of each line (pi: getLineSpans).
func getLineSpans(content string) []LineSpan {
	offset := 0
	lines := splitLinesWithEndings(content)
	spans := make([]LineSpan, len(lines))
	for i, line := range lines {
		spans[i] = LineSpan{Start: offset, End: offset + len(line)}
		offset = spans[i].End
	}
	return spans
}

// getReplacementLineRange maps a replacement onto the line range it touches
// (pi: getReplacementLineRange).
func getReplacementLineRange(lines []LineSpan, replacement TextReplacement) (startLine int, endLine int, err error) {
	replacementStart := replacement.MatchIndex
	replacementEnd := replacement.MatchIndex + replacement.MatchLength

	startLine = -1
	for i, line := range lines {
		if replacementStart >= line.Start && replacementStart < line.End {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}

	endLine = startLine
	for endLine < len(lines) && lines[endLine].End < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}
	return startLine, endLine + 1, nil
}

// applyReplacements applies replacements matched against baseContent (pi:
// applyReplacements; offset shifts the match indices).
func applyReplacements(content string, replacements []TextReplacement, offset int) string {
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		matchIndex := replacement.MatchIndex - offset
		result = result[:matchIndex] + replacement.NewText + result[matchIndex+replacement.MatchLength:]
	}
	return result
}

// ApplyReplacementsPreservingUnchangedLines applies replacements matched
// against baseContent to originalContent while preserving unchanged line
// blocks from the original (pi: applyReplacementsPreservingUnchangedLines).
func ApplyReplacementsPreservingUnchangedLines(originalContent string, baseContent string, replacements []TextReplacement) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", fmt.Errorf("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	type replacementGroup struct {
		startLine    int
		endLine      int
		replacements []TextReplacement
	}
	sortedReplacements := make([]TextReplacement, len(replacements))
	copy(sortedReplacements, replacements)
	for i := 1; i < len(sortedReplacements); i++ {
		for j := i; j > 0 && sortedReplacements[j].MatchIndex < sortedReplacements[j-1].MatchIndex; j-- {
			sortedReplacements[j], sortedReplacements[j-1] = sortedReplacements[j-1], sortedReplacements[j]
		}
	}
	var groups []replacementGroup
	for _, replacement := range sortedReplacements {
		startLine, endLine, err := getReplacementLineRange(baseLines, replacement)
		if err != nil {
			return "", err
		}
		if len(groups) > 0 {
			current := &groups[len(groups)-1]
			if startLine < current.endLine {
				if endLine > current.endLine {
					current.endLine = endLine
				}
				current.replacements = append(current.replacements, replacement)
				continue
			}
		}
		groups = append(groups, replacementGroup{startLine: startLine, endLine: endLine, replacements: []TextReplacement{replacement}})
	}

	originalLineIndex := 0
	var result strings.Builder
	for _, group := range groups {
		for _, line := range originalLines[originalLineIndex:group.startLine] {
			result.WriteString(line)
		}
		groupStartOffset := baseLines[group.startLine].Start
		groupEndOffset := baseLines[group.endLine-1].End
		replaced := applyReplacements(baseContent[groupStartOffset:groupEndOffset], group.replacements, groupStartOffset)
		result.WriteString(replaced)
		originalLineIndex = group.endLine
	}
	for _, line := range originalLines[originalLineIndex:] {
		result.WriteString(line)
	}
	return result.String(), nil
}

// FuzzyMatchResult is the outcome of one fuzzy search (pi:
// FuzzyMatchResult).
type FuzzyMatchResult struct {
	// Found reports whether a match was found.
	Found bool
	// Index is where the match starts (in the content that should be used
	// for replacement).
	Index int
	// MatchLength is the length of the matched text.
	MatchLength int
	// UsedFuzzyMatch is false for exact matches.
	UsedFuzzyMatch bool
	// ContentForReplacement is the content to use for replacement
	// operations: original content for exact matches, normalized content
	// for fuzzy matches.
	ContentForReplacement string
}

// Edit is one exact-text replacement (pi: Edit).
type Edit struct {
	OldText string
	NewText string
}

// AppliedEditsResult carries the normalized base and the new content (pi:
// AppliedEditsResult).
type AppliedEditsResult struct {
	BaseContent string
	NewContent  string
}

// FuzzyFindText finds oldText in content, trying exact match first, then
// fuzzy match (pi: fuzzyFindText).
func FuzzyFindText(content string, oldText string) FuzzyMatchResult {
	exactIndex := strings.Index(content, oldText)
	if exactIndex != -1 {
		return FuzzyMatchResult{
			Found:                 true,
			Index:                 exactIndex,
			MatchLength:           len(oldText),
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}

	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return FuzzyMatchResult{
			Found:                 false,
			Index:                 -1,
			MatchLength:           0,
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}

	return FuzzyMatchResult{
		Found:                 true,
		Index:                 fuzzyIndex,
		MatchLength:           len(fuzzyOldText),
		UsedFuzzyMatch:        true,
		ContentForReplacement: fuzzyContent,
	}
}

// StripBom strips a UTF-8 BOM if present (pi: stripBom).
func StripBom(content string) (bom string, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[len("\uFEFF"):]
	}
	return "", content
}

// countOccurrences counts fuzzy-space occurrences (pi: countOccurrences).
func countOccurrences(content string, oldText string) int {
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	return strings.Count(fuzzyContent, fuzzyOldText)
}

// getNotFoundError builds the not-found error message (pi:
// getNotFoundError).
func getNotFoundError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf(
			"Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Errorf(
		"Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", editIndex, path)
}

// getDuplicateError builds the duplicate-occurrence error message (pi:
// getDuplicateError).
func getDuplicateError(path string, editIndex int, totalEdits int, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf(
			"Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, path)
	}
	return fmt.Errorf(
		"Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, editIndex, path)
}

// getEmptyOldTextError builds the empty-oldText error message (pi:
// getEmptyOldTextError).
func getEmptyOldTextError(path string, editIndex int, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", editIndex, path)
}

// getNoChangeError builds the no-change error message (pi:
// getNoChangeError).
func getNoChangeError(path string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf(
			"No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}

// ApplyEditsToNormalizedContent applies one or more exact-text replacements
// to LF-normalized content (pi: applyEditsToNormalizedContent). All edits
// are matched against the same original content; replacements are applied in
// reverse order so offsets remain stable. If any edit needs fuzzy matching,
// the operation runs in fuzzy-normalized content space and then overlays
// those line-level changes onto the original content so unchanged line
// blocks keep their original bytes.
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (AppliedEditsResult, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{OldText: NormalizeToLF(edit.OldText), NewText: NormalizeToLF(edit.NewText)}
	}

	for i := range normalizedEdits {
		if len(normalizedEdits[i].OldText) == 0 {
			return AppliedEditsResult{}, getEmptyOldTextError(path, i, len(normalizedEdits))
		}
	}

	initialMatches := make([]FuzzyMatchResult, len(normalizedEdits))
	for i, edit := range normalizedEdits {
		initialMatches[i] = FuzzyFindText(normalizedContent, edit.OldText)
	}
	usedFuzzyMatch := false
	for _, match := range initialMatches {
		if match.UsedFuzzyMatch {
			usedFuzzyMatch = true
			break
		}
	}
	replacementBaseContent := normalizedContent
	if usedFuzzyMatch {
		replacementBaseContent = NormalizeForFuzzyMatch(normalizedContent)
	}

	matchedEdits := make([]MatchedEdit, 0, len(normalizedEdits))
	for i, edit := range normalizedEdits {
		matchResult := FuzzyFindText(replacementBaseContent, edit.OldText)
		if !matchResult.Found {
			return AppliedEditsResult{}, getNotFoundError(path, i, len(normalizedEdits))
		}
		occurrences := countOccurrences(replacementBaseContent, edit.OldText)
		if occurrences > 1 {
			return AppliedEditsResult{}, getDuplicateError(path, i, len(normalizedEdits), occurrences)
		}
		matchedEdits = append(matchedEdits, MatchedEdit{
			EditIndex:   i,
			MatchIndex:  matchResult.Index,
			MatchLength: matchResult.MatchLength,
			NewText:     edit.NewText,
		})
	}

	for i := 1; i < len(matchedEdits); i++ {
		for j := i; j > 0 && matchedEdits[j].MatchIndex < matchedEdits[j-1].MatchIndex; j-- {
			matchedEdits[j], matchedEdits[j-1] = matchedEdits[j-1], matchedEdits[j]
		}
	}
	for i := 1; i < len(matchedEdits); i++ {
		previous := matchedEdits[i-1]
		current := matchedEdits[i]
		if previous.MatchIndex+previous.MatchLength > current.MatchIndex {
			return AppliedEditsResult{}, fmt.Errorf(
				"edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				previous.EditIndex, current.EditIndex, path)
		}
	}

	baseContent := normalizedContent
	var newContent string
	if usedFuzzyMatch {
		replacements := make([]TextReplacement, len(matchedEdits))
		for i, m := range matchedEdits {
			replacements[i] = TextReplacement{MatchIndex: m.MatchIndex, MatchLength: m.MatchLength, NewText: m.NewText}
		}
		var err error
		newContent, err = ApplyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, replacements)
		if err != nil {
			return AppliedEditsResult{}, err
		}
	} else {
		replacements := make([]TextReplacement, len(matchedEdits))
		for i, m := range matchedEdits {
			replacements[i] = TextReplacement{MatchIndex: m.MatchIndex, MatchLength: m.MatchLength, NewText: m.NewText}
		}
		newContent = applyReplacements(replacementBaseContent, replacements, 0)
	}

	if baseContent == newContent {
		return AppliedEditsResult{}, getNoChangeError(path, len(normalizedEdits))
	}

	return AppliedEditsResult{BaseContent: baseContent, NewContent: newContent}, nil
}

// GenerateUnifiedPatch generates a standard unified patch (pi:
// generateUnifiedPatch; contextLines defaults to 4).
func GenerateUnifiedPatch(path string, oldContent string, newContent string, contextLines ...int) string {
	context := 4
	if len(contextLines) > 0 {
		context = contextLines[0]
	}
	return CreateTwoFilesPatch(path, path, oldContent, newContent, context)
}

// DiffStringResult carries the display diff and the first changed line (pi:
// the { diff, firstChangedLine } return of generateDiffString).
type DiffStringResult struct {
	Diff             string
	FirstChangedLine int
	// HasFirstChangedLine is false when pi's firstChangedLine is undefined.
	HasFirstChangedLine bool
}

// GenerateDiffString generates a display-oriented diff string with line
// numbers and context (pi: generateDiffString; contextLines defaults to 4).
func GenerateDiffString(oldContent string, newContent string, contextLines ...int) DiffStringResult {
	context := 4
	if len(contextLines) > 0 {
		context = contextLines[0]
	}
	parts := diffLines(oldContent, newContent)
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := len(oldLines)
	if len(newLines) > maxLineNum {
		maxLineNum = len(newLines)
	}
	lineNumWidth := len(fmt.Sprintf("%d", maxLineNum))

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	result := DiffStringResult{}

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		raw := strings.Split(part.Value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.Added || part.Removed {
			// Capture the first changed line (in the new file).
			if !result.HasFirstChangedLine {
				result.FirstChangedLine = newLineNum
				result.HasFirstChangedLine = true
			}

			for _, line := range raw {
				if part.Added {
					lineNum := padStart(fmt.Sprintf("%d", newLineNum), lineNumWidth)
					output = append(output, "+"+lineNum+" "+line)
					newLineNum++
				} else {
					lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
					output = append(output, "-"+lineNum+" "+line)
					oldLineNum++
				}
			}
			lastWasChange = true
		} else {
			nextPartIsChange := i < len(parts)-1 && (parts[i+1].Added || parts[i+1].Removed)
			hasLeadingChange := lastWasChange
			hasTrailingChange := nextPartIsChange

			if hasLeadingChange && hasTrailingChange {
				if len(raw) <= context*2 {
					for _, line := range raw {
						lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}
				} else {
					leadingLines := raw[:context]
					trailingLines := raw[len(raw)-context:]
					skippedLines := len(raw) - len(leadingLines) - len(trailingLines)

					for _, line := range leadingLines {
						lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}

					output = append(output, " "+padStart("", lineNumWidth)+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines

					for _, line := range trailingLines {
						lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
						output = append(output, " "+lineNum+" "+line)
						oldLineNum++
						newLineNum++
					}
				}
			} else if hasLeadingChange {
				shownLines := raw
				if len(raw) > context {
					shownLines = raw[:context]
				}
				skippedLines := len(raw) - len(shownLines)

				for _, line := range shownLines {
					lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
					output = append(output, " "+lineNum+" "+line)
					oldLineNum++
					newLineNum++
				}

				if skippedLines > 0 {
					output = append(output, " "+padStart("", lineNumWidth)+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}
			} else if hasTrailingChange {
				skippedLines := len(raw) - context
				if skippedLines < 0 {
					skippedLines = 0
				}
				if skippedLines > 0 {
					output = append(output, " "+padStart("", lineNumWidth)+" ...")
					oldLineNum += skippedLines
					newLineNum += skippedLines
				}

				for _, line := range raw[skippedLines:] {
					lineNum := padStart(fmt.Sprintf("%d", oldLineNum), lineNumWidth)
					output = append(output, " "+lineNum+" "+line)
					oldLineNum++
					newLineNum++
				}
			} else {
				// Skip these context lines entirely.
				oldLineNum += len(raw)
				newLineNum += len(raw)
			}

			lastWasChange = false
		}
	}

	result.Diff = strings.Join(output, "\n")
	return result
}

// padStart left-pads s with spaces to width (JS String.padStart).
func padStart(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}
