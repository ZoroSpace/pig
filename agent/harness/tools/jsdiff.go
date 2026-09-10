// Minimal port of the npm `diff` package (v8.0.4, pin from pi's
// packages/agent/package.json) covering exactly the two entry points the
// harness consumes: diffLines and createTwoFilesPatch with
// FILE_HEADERS_ONLY. pi imports these from npm; pig vendors the algorithm
// verbatim (Myers edit-graph walk with jsdiff's tie-breaking and hunk
// building) so patch output is byte-identical. Source:
// node_modules/diff/dist/diff.js (Diff.diffWithOptionsObj/addToPath/
// extractCommon/buildValues, tokenize, diffLinesResultToPatch, formatPatch,
// splitLines). Exports not consumed by the harness are omitted.
package tools

import (
	"strconv"
	"strings"
)

// LineDiffChange is one change object of the line diff (jsdiff:
// { count, added, removed, value }).
type LineDiffChange struct {
	Count   int
	Added   bool
	Removed bool
	Value   string
}

// diffComponent is the linked-list node built during the walk (jsdiff's
// component objects; value is assigned by buildValues).
type diffComponent struct {
	count   int
	added   bool
	removed bool
	value   string
	prev    *diffComponent
}

// pathState is one bestPath entry (jsdiff: { oldPos, lastComponent }).
type pathState struct {
	oldPos        int
	lastComponent *diffComponent
}

// diffLinesTokenize splits text into line tokens including their trailing
// newline, matching jsdiff's LineDiff tokenize
// (split(/(\n|\r\n)/) + separator merge + final-empty drop).
func diffLinesTokenize(value string) []string {
	var tokens []string
	start := 0
	i := 0
	for i < len(value) {
		if value[i] == '\n' {
			tokens = append(tokens, value[start:i+1])
			i++
			start = i
		} else if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
			tokens = append(tokens, value[start:i+2])
			i += 2
			start = i
		} else {
			i++
		}
	}
	if start < len(value) {
		tokens = append(tokens, value[start:])
	}
	return tokens
}

// diffLines produces the line-level change objects (jsdiff: diffLines). The
// Myers walk (diffWithOptionsObj) is ported including the
// min/maxDiagonalToConsider pruning optimization and the add-before-remove
// tie-break, so ambiguous edits resolve identically.
func diffLines(oldStr string, newStr string) []LineDiffChange {
	return diffTokens(diffLinesTokenize(oldStr), diffLinesTokenize(newStr), tokenDiffHooks{
		equals: func(a, b string) bool { return a == b },
		join:   func(tokens []string) string { return strings.Join(tokens, "") },
	})
}

// tokenDiffHooks carries the subclass-specific operations of jsdiff's Diff
// base class (equals/join/postProcess; tokenize is applied by the caller).
type tokenDiffHooks struct {
	// equals is Diff.equals (default: strict token equality).
	equals func(a, b string) bool
	// join is Diff.join (default: concatenation).
	join func(tokens []string) string
	// postProcess is Diff.postProcess (nil = identity).
	postProcess func(changes []LineDiffChange) []LineDiffChange
}

// diffTokens is jsdiff's Diff.diffWithOptionsObj over pre-tokenized input:
// the Myers edit-graph walk with the min/maxDiagonalToConsider pruning
// optimization and the add-before-remove tie-break, shared by diffLines and
// DiffWords so both resolve ambiguous edits identically.
func diffTokens(oldTokens []string, newTokens []string, hooks tokenDiffHooks) []LineDiffChange {
	// base.js removeEmpty(tokenize(...)).
	oldTokens = removeEmptyTokens(oldTokens)
	newTokens = removeEmptyTokens(newTokens)
	oldLen := len(oldTokens)
	newLen := len(newTokens)
	maxEditLength := newLen + oldLen

	bestPath := map[int]*pathState{0: {oldPos: -1}}
	extractCommon := func(base *pathState, diagonalPath int) int {
		oldPos := base.oldPos
		newPos := oldPos - diagonalPath
		commonCount := 0
		for newPos+1 < newLen && oldPos+1 < oldLen && hooks.equals(oldTokens[oldPos+1], newTokens[newPos+1]) {
			newPos++
			oldPos++
			commonCount++
		}
		if commonCount > 0 {
			base.lastComponent = &diffComponent{count: commonCount, prev: base.lastComponent}
		}
		base.oldPos = oldPos
		return newPos
	}

	finish := func(changes []LineDiffChange) []LineDiffChange {
		if hooks.postProcess != nil {
			return hooks.postProcess(changes)
		}
		return changes
	}

	newPos := extractCommon(bestPath[0], 0)
	if bestPath[0].oldPos+1 >= oldLen && newPos+1 >= newLen {
		return finish(buildDiffValues(bestPath[0].lastComponent, newTokens, oldTokens, hooks.join))
	}

	editLength := 1
	minDiagonalToConsider := -1 << 60
	maxDiagonalToConsider := 1 << 60

	addToPath := func(path *pathState, added bool, removed bool, oldPosInc int) *pathState {
		last := path.lastComponent
		if last != nil && last.added == added && last.removed == removed {
			return &pathState{
				oldPos:        path.oldPos + oldPosInc,
				lastComponent: &diffComponent{count: last.count + 1, added: added, removed: removed, prev: last.prev},
			}
		}
		return &pathState{
			oldPos:        path.oldPos + oldPosInc,
			lastComponent: &diffComponent{count: 1, added: added, removed: removed, prev: last},
		}
	}

	for editLength <= maxEditLength {
		for diagonalPath := max(minDiagonalToConsider, -editLength); diagonalPath <= min(maxDiagonalToConsider, editLength); diagonalPath += 2 {
			removePath := bestPath[diagonalPath-1]
			delete(bestPath, diagonalPath-1)
			addPath := bestPath[diagonalPath+1]
			canAdd := false
			if addPath != nil {
				addPathNewPos := addPath.oldPos - diagonalPath
				canAdd = 0 <= addPathNewPos && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				delete(bestPath, diagonalPath)
				continue
			}
			var basePath *pathState
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				basePath = addToPath(addPath, true, false, 0)
			} else {
				basePath = addToPath(removePath, false, true, 1)
			}
			newPos = extractCommon(basePath, diagonalPath)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return finish(buildDiffValues(basePath.lastComponent, newTokens, oldTokens, hooks.join))
			}
			bestPath[diagonalPath] = basePath
			if basePath.oldPos+1 >= oldLen {
				maxDiagonalToConsider = min(maxDiagonalToConsider, diagonalPath-1)
			}
			if newPos+1 >= newLen {
				minDiagonalToConsider = max(minDiagonalToConsider, diagonalPath+1)
			}
		}
		editLength++
	}
	// jsdiff returns undefined only under maxEditLength/timeout options,
	// which the harness never sets.
	return nil
}

// removeEmptyTokens drops empty tokens (base.js removeEmpty; no-op for the
// line tokenizer, meaningful for subclasses).
func removeEmptyTokens(tokens []string) []string {
	out := tokens[:0:0]
	for _, t := range tokens {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// buildDiffValues converts the linked component list into ordered change
// objects with values filled from the token arrays (jsdiff: buildValues).
func buildDiffValues(lastComponent *diffComponent, newTokens []string, oldTokens []string, join func([]string) string) []LineDiffChange {
	var components []*diffComponent
	for c := lastComponent; c != nil; c = c.prev {
		components = append(components, c)
	}
	for i, j := 0, len(components)-1; i < j; i, j = i+1, j-1 {
		components[i], components[j] = components[j], components[i]
	}
	changes := make([]LineDiffChange, len(components))
	newPos, oldPos := 0, 0
	for i, component := range components {
		changes[i] = LineDiffChange{Count: component.count, Added: component.added, Removed: component.removed}
		if !component.removed {
			changes[i].Value = join(newTokens[newPos : newPos+component.count])
			newPos += component.count
			if !component.added {
				oldPos += component.count
			}
		} else {
			changes[i].Value = join(oldTokens[oldPos : oldPos+component.count])
			oldPos += component.count
		}
	}
	return changes
}

// UnifiedPatchHunk is one hunk of a structured patch (jsdiff:
// { oldStart, oldLines, newStart, newLines, lines }).
type UnifiedPatchHunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []string
}

// StructuredPatch is the structured patch object (jsdiff:
// { oldFileName, newFileName, oldHeader, newHeader, hunks }).
type StructuredPatch struct {
	OldFileName string
	NewFileName string
	OldHeader   string
	NewHeader   string
	Hunks       []UnifiedPatchHunk
}

// diffLinesSplitLines splits text into lines each carrying its trailing
// newline (jsdiff: splitLines).
func diffLinesSplitLines(text string) []string {
	hasTrailingNl := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	result := make([]string, len(parts))
	for i, line := range parts {
		result[i] = line + "\n"
	}
	if hasTrailingNl {
		result = result[:len(result)-1]
	} else {
		last := len(result) - 1
		result[last] = strings.TrimSuffix(result[last], "\n")
	}
	return result
}

// structuredPatchLines builds the structured patch from two texts (jsdiff:
// structuredPatch + diffLinesResultToPatch; context defaults to 4 as in pi's
// generateUnifiedPatch call sites).
func structuredPatchLines(oldFileName string, newFileName string, oldStr string, newStr string, context int) *StructuredPatch {
	if context < 0 {
		context = 4
	}
	diff := diffLines(oldStr, newStr)
	// Append an empty entry to make cleanup easier (jsdiff:
	// diff.push({ value: '', lines: [] })). jsdiff reads `current.lines`
	// first — the preset empty array is truthy — so the sentinel contributes
	// zero lines; replicate with the sentinel flag.
	diff = append(diff, LineDiffChange{Value: ""})
	sentinelIndex := len(diff) - 1

	var hunks []UnifiedPatchHunk
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1

	contextLines := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, entry := range lines {
			out[i] = " " + entry
		}
		return out
	}

	for i := 0; i < len(diff); i++ {
		current := diff[i]
		var lines []string
		if i == sentinelIndex {
			lines = nil
		} else {
			lines = diffLinesSplitLines(current.Value)
		}
		if current.Added || current.Removed {
			if oldRangeStart == 0 {
				oldRangeStart = oldLine
				newRangeStart = newLine
				if i > 0 {
					prev := diff[i-1]
					if context > 0 {
						prevLines := diffLinesSplitLines(prev.Value)
						if len(prevLines) > context {
							prevLines = prevLines[len(prevLines)-context:]
						}
						curRange = contextLines(prevLines)
					} else {
						curRange = nil
					}
					oldRangeStart -= len(curRange)
					newRangeStart -= len(curRange)
				}
			}
			for _, line := range lines {
				if current.Added {
					curRange = append(curRange, "+"+line)
				} else {
					curRange = append(curRange, "-"+line)
				}
			}
			if current.Added {
				newLine += len(lines)
			} else {
				oldLine += len(lines)
			}
		} else {
			if oldRangeStart != 0 {
				if len(lines) <= context*2 && i < len(diff)-2 {
					curRange = append(curRange, contextLines(lines)...)
				} else {
					contextSize := len(lines)
					if contextSize > context {
						contextSize = context
					}
					curRange = append(curRange, contextLines(lines[:contextSize])...)
					hunks = append(hunks, UnifiedPatchHunk{
						OldStart: oldRangeStart,
						OldLines: oldLine - oldRangeStart + contextSize,
						NewStart: newRangeStart,
						NewLines: newLine - newRangeStart + contextSize,
						Lines:    curRange,
					})
					oldRangeStart = 0
					newRangeStart = 0
					curRange = nil
				}
			}
			oldLine += len(lines)
			newLine += len(lines)
		}
	}

	// Step 2: strip the trailing \n from each hunk line, inserting
	// "\ No newline at end of file" where needed (jsdiff: step 2 loop).
	for h := range hunks {
		hunk := &hunks[h]
		for i := 0; i < len(hunk.Lines); i++ {
			if strings.HasSuffix(hunk.Lines[i], "\n") {
				hunk.Lines[i] = strings.TrimSuffix(hunk.Lines[i], "\n")
			} else {
				hunk.Lines = append(hunk.Lines[:i+1], append([]string{"\\ No newline at end of file"}, hunk.Lines[i+1:]...)...)
				i++
			}
		}
	}

	return &StructuredPatch{
		OldFileName: oldFileName,
		NewFileName: newFileName,
		Hunks:       hunks,
	}
}

// formatPatchHeadersOnly renders a structured patch with the
// FILE_HEADERS_ONLY header options (---/+++ lines, no index/underline), the
// only style pi's generateUnifiedPatch consumes.
func formatPatchHeadersOnly(patch *StructuredPatch) string {
	var ret []string
	ret = append(ret, "--- "+patch.OldFileName)
	ret = append(ret, "+++ "+patch.NewFileName)
	for i := range patch.Hunks {
		hunk := &patch.Hunks[i]
		// Unified Diff Format quirk: if the chunk size is 0, the first
		// number is one lower than one would expect.
		oldStart := hunk.OldStart
		newStart := hunk.NewStart
		if hunk.OldLines == 0 {
			oldStart--
		}
		if hunk.NewLines == 0 {
			newStart--
		}
		ret = append(ret, "@@ -"+strconv.Itoa(oldStart)+","+strconv.Itoa(hunk.OldLines)+
			" +"+strconv.Itoa(newStart)+","+strconv.Itoa(hunk.NewLines)+" @@")
		ret = append(ret, hunk.Lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}

// CreateTwoFilesPatch renders a unified patch between two texts (jsdiff:
// createTwoFilesPatch with headerOptions FILE_HEADERS_ONLY).
func CreateTwoFilesPatch(oldFileName string, newFileName string, oldStr string, newStr string, context int) string {
	return formatPatchHeadersOnly(structuredPatchLines(oldFileName, newFileName, oldStr, newStr, context))
}
