// pi: src/core/tools/edit-diff.ts — the diff algorithm functions are
// byte-identical to the agent package's copy (verified by diff); pig imports
// agent/harness/tools and adds only the coding-agent preview helpers.
//
// Not yet ported: nothing — computeEditsDiff/computeEditDiff are the
// TUI-preview helpers and are ported here (they have no theme dependency).
package tools

import (
	"context"
	"fmt"
	"os"

	harnessTools "github.com/ZoroSpace/pig/agent/harness/tools"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// splitBom splits a leading UTF-8 BOM (pi: utils/text.ts splitBom via the
// edit-diff import; aliased from the utils package).
func splitBom(content string) (bom string, text string) {
	return codingagentutils.SplitBom(content)
}

// Edit is one exact-text replacement (pi: Edit; alias to the harness port).
type Edit = harnessTools.Edit

// DetectLineEnding reports the content's line ending (pi: detectLineEnding).
func DetectLineEnding(content string) string { return harnessTools.DetectLineEnding(content) }

// NormalizeToLF converts CRLF/CR to LF (pi: normalizeToLF).
func NormalizeToLF(text string) string { return harnessTools.NormalizeToLF(text) }

// RestoreLineEndings converts LF back to the original ending (pi:
// restoreLineEndings).
func RestoreLineEndings(text string, ending string) string {
	return harnessTools.RestoreLineEndings(text, ending)
}

// ApplyEditsToNormalizedContent applies exact-text replacements to
// LF-normalized content (pi: applyEditsToNormalizedContent).
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (harnessTools.AppliedEditsResult, error) {
	return harnessTools.ApplyEditsToNormalizedContent(normalizedContent, edits, path)
}

// GenerateUnifiedPatch renders a unified patch (pi: generateUnifiedPatch).
func GenerateUnifiedPatch(path string, oldContent string, newContent string) string {
	return harnessTools.GenerateUnifiedPatch(path, oldContent, newContent)
}

// EditDiffResult carries the display diff (pi: EditDiffResult).
type EditDiffResult struct {
	Diff             string
	FirstChangedLine int
	// HasFirstChangedLine is false when pi's firstChangedLine is undefined.
	HasFirstChangedLine bool
}

// EditDiffError carries a preview failure (pi: EditDiffError).
type EditDiffError struct {
	Error string
}

// generateDiffStringResult converts the harness result type.
func generateDiffStringResult(oldContent string, newContent string) EditDiffResult {
	result := harnessTools.GenerateDiffString(oldContent, newContent)
	return EditDiffResult{Diff: result.Diff, FirstChangedLine: result.FirstChangedLine, HasFirstChangedLine: result.HasFirstChangedLine}
}

// ComputeEditsDiff computes the diff for one or more edit operations without
// applying them (pi: computeEditsDiff; used for preview rendering in the TUI
// before the tool executes). The OK member discriminates the union result.
func ComputeEditsDiff(ctx context.Context, path string, edits []Edit, cwd string) (EditDiffResult, EditDiffError) {
	absolutePath := ResolveToCwd(path, cwd)

	// Check if file exists and is readable.
	if _, err := os.Stat(absolutePath); err != nil {
		if code := codingagentutils.NodeErrCodeOf(err); code != "" {
			return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error code: %s.", path, code)}
		}
		return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error: %v.", path, err)}
	}

	rawContent, err := os.ReadFile(absolutePath)
	if err != nil {
		if code := codingagentutils.NodeErrCodeOf(err); code != "" {
			return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error code: %s.", path, code)}
		}
		return EditDiffResult{}, EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error: %v.", path, err)}
	}

	// Strip BOM before matching (LLM won't include invisible BOM in
	// oldText).
	_, text := splitBom(string(rawContent))
	normalizedContent := NormalizeToLF(text)
	applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
	if err != nil {
		return EditDiffResult{}, EditDiffError{Error: err.Error()}
	}

	result := generateDiffStringResult(applied.BaseContent, applied.NewContent)
	return result, EditDiffError{}
}

// ComputeEditDiff computes the diff for a single edit operation without
// applying it (pi: computeEditDiff; convenience wrapper for single-edit
// callers).
func ComputeEditDiff(ctx context.Context, path string, oldText string, newText string, cwd string) (EditDiffResult, EditDiffError) {
	return ComputeEditsDiff(ctx, path, []Edit{{OldText: oldText, NewText: newText}}, cwd)
}
