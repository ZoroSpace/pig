// external_editor.go (pi: src/modes/interactive/external-editor.ts) — edit
// editor text in the user's external editor ($EDITOR-style command).
//
// Segment deviations:
//   - pi avoids spawnSync on Windows because libuv's console read races the
//     child; Go's exec has no such shared console reader, so a plain
//     synchronous run is equivalent on every platform.
package utils

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExternalEditorResult is the editor outcome (pi: ExternalEditorResult).
type ExternalEditorResult struct {
	// Status is "complete" or "failed".
	Status string
	// Content is the edited text when Status == "complete".
	Content string
}

// EditInExternalEditor runs the editor command over the content in a temp
// file (pi: editInExternalEditor). command is split on spaces (pi's split).
func EditInExternalEditor(ctx context.Context, command string, content string) (ExternalEditorResult, error) {
	directory, err := os.MkdirTemp(os.TempDir(), "pi-editor-")
	if err != nil {
		return ExternalEditorResult{Status: "failed"}, err
	}
	defer func() {
		// Cleanup is best effort.
		_ = os.RemoveAll(directory)
	}()
	filePath := filepath.Join(directory, "prompt.md")
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return ExternalEditorResult{Status: "failed"}, err
	}
	parts := strings.Split(command, " ")
	args := append(parts[1:], filePath)
	// The TUI stdin is already released by the caller (ui.stop), so the
	// child can own the terminal (pi: stdio: "inherit").
	cmd := exec.CommandContext(ctx, parts[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return ExternalEditorResult{Status: "failed"}, nil
	}

	edited, err := os.ReadFile(filePath)
	if err != nil {
		return ExternalEditorResult{Status: "failed"}, nil
	}
	text := StripBom(string(edited))
	text = strings.TrimSuffix(text, "\n")
	return ExternalEditorResult{Status: "complete", Content: text}, nil
}
