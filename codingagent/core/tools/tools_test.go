// Port of pi's packages/coding-agent/test/tools.test.ts (the read/write/
// edit/bash/grep/find/ls describes plus the fuzzy-matching and CRLF
// describes) and test/edit-tool-legacy-input.test.ts.
//
// Segment deviations:
//   - The two executeBashWithOperations cases depend on core/
//     bash-executor.ts (agent-session batch) and are placeholders.
//   - "should read BMP files from disk as PNG image attachments" asserts
//     pi's photon-unavailable path: pig's photon runtime arrives with Phase
//     8, so conversion fails exactly as pi without a loadable photon.
//   - applyPatch is a test-only minimal applier (npm diff.applyPatch is not
//     a harness dependency).
//   - The process.platform mock of the stdin-transport test is replaced by
//     a real shell script at the legacy WSL path (same approach as the env
//     test batch).
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

func textOf(result agent.AgentToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if text, ok := c.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func imagesOf(result agent.AgentToolResult) []*types.ImageContent {
	var images []*types.ImageContent
	for _, c := range result.Content {
		if image, ok := c.(*types.ImageContent); ok {
			images = append(images, image)
		}
	}
	return images
}

func execTool(t *testing.T, tool Tool, callID string, params map[string]any) agent.AgentToolResult {
	t.Helper()
	result, err := tool.ExecuteWithToolContext(context.Background(), callID, params, nil, nil)
	if err != nil {
		t.Fatalf("execute %s failed: %v", callID, err)
	}
	return result
}

func mustFail(t *testing.T, tool Tool, callID string, params map[string]any) error {
	t.Helper()
	_, err := tool.ExecuteWithToolContext(context.Background(), callID, params, nil, nil)
	if err == nil {
		t.Fatalf("execute %s should have failed", callID)
	}
	return err
}

func newTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "coding-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func lines(n int, format string) string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(format, i+1)
	}
	return strings.Join(out, "\n")
}

var _ = regexp.MustCompile

// createTinyBmp1x1Red24bpp builds a minimal 1x1 red BMP (pi:
// createTinyBmp1x1Red24bpp).
func createTinyBmp1x1Red24bpp() []byte {
	buffer := make([]byte, 58)
	putUint32LE := func(offset int, value uint32) {
		buffer[offset] = byte(value)
		buffer[offset+1] = byte(value >> 8)
		buffer[offset+2] = byte(value >> 16)
		buffer[offset+3] = byte(value >> 24)
	}
	putInt32LE := func(offset int, value int32) { putUint32LE(offset, uint32(value)) }
	putUint16LE := func(offset int, value uint16) {
		buffer[offset] = byte(value)
		buffer[offset+1] = byte(value >> 8)
	}
	buffer[0] = 'B'
	buffer[1] = 'M'
	putUint32LE(2, uint32(len(buffer)))
	putUint32LE(10, 54)
	putUint32LE(14, 40)
	putInt32LE(18, 1)
	putInt32LE(22, 1)
	putUint16LE(26, 1)
	putUint16LE(28, 24)
	putUint32LE(30, 0)
	putUint32LE(34, 4)
	buffer[56] = 0xff
	return buffer
}

func TestReadTool(t *testing.T) {
	testDir := newTestDir(t)
	readTool := CreateReadTool(cwdForTest(), nil)

	t.Run("should read file contents that fit within limits", func(t *testing.T) {
		testFile := filepath.Join(testDir, "test.txt")
		content := "Hello, world!\nLine 2\nLine 3"
		writeTestFile(t, testFile, content)

		result := execTool(t, readTool, "test-call-1", map[string]any{"path": testFile})
		if textOf(result) != content {
			t.Fatalf("output = %q", textOf(result))
		}
		if strings.Contains(textOf(result), "Use offset=") {
			t.Fatal("unexpected truncation notice")
		}
		if result.Details != nil {
			t.Fatal("unexpected details")
		}
	})

	t.Run("should handle non-existent files", func(t *testing.T) {
		testFile := filepath.Join(testDir, "nonexistent.txt")
		err := mustFail(t, readTool, "test-call-2", map[string]any{"path": testFile})
		re := regexp.MustCompile(`ENOENT|not found`)
		if !re.MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should truncate files exceeding line limit", func(t *testing.T) {
		testFile := filepath.Join(testDir, "large.txt")
		writeTestFile(t, testFile, lines(2500, "Line %d"))

		result := execTool(t, readTool, "test-call-3", map[string]any{"path": testFile})
		output := textOf(result)
		for _, want := range []string{"Line 1", "Line 2000", "[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]"} {
			if !strings.Contains(output, want) {
				t.Fatalf("output missing %q", want)
			}
		}
		if strings.Contains(output, "Line 2001") {
			t.Fatal("output leaks beyond limit")
		}
	})

	t.Run("should truncate when byte limit exceeded", func(t *testing.T) {
		testFile := filepath.Join(testDir, "large-bytes.txt")
		var sb strings.Builder
		for i := 1; i <= 500; i++ {
			sb.WriteString(fmt.Sprintf("Line %d: %s", i, strings.Repeat("x", 200)))
			if i < 500 {
				sb.WriteString("\n")
			}
		}
		writeTestFile(t, testFile, sb.String())

		result := execTool(t, readTool, "test-call-4", map[string]any{"path": testFile})
		output := textOf(result)
		if !strings.Contains(output, "Line 1:") {
			t.Fatalf("output = %q", output)
		}
		re := regexp.MustCompile(`\[Showing lines 1-\d+ of 500 \(.* limit\)\. Use offset=\d+ to continue\.\]`)
		if !re.MatchString(output) {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("should handle offset parameter", func(t *testing.T) {
		testFile := filepath.Join(testDir, "offset-test.txt")
		writeTestFile(t, testFile, lines(100, "Line %d"))

		result := execTool(t, readTool, "test-call-5", map[string]any{"path": testFile, "offset": float64(51)})
		output := textOf(result)
		if strings.Contains(output, "Line 50") || strings.Contains(output, "Use offset=") {
			t.Fatalf("output = %q", output)
		}
		if !strings.Contains(output, "Line 51") || !strings.Contains(output, "Line 100") {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("should handle limit parameter", func(t *testing.T) {
		testFile := filepath.Join(testDir, "limit-test.txt")
		writeTestFile(t, testFile, lines(100, "Line %d"))

		result := execTool(t, readTool, "test-call-6", map[string]any{"path": testFile, "limit": float64(10)})
		output := textOf(result)
		if !strings.Contains(output, "Line 1") || !strings.Contains(output, "Line 10") {
			t.Fatalf("output = %q", output)
		}
		if strings.Contains(output, "Line 11") {
			t.Fatalf("output = %q", output)
		}
		if !strings.Contains(output, "[90 more lines in file. Use offset=11 to continue.]") {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("should handle offset + limit together", func(t *testing.T) {
		testFile := filepath.Join(testDir, "offset-limit-test.txt")
		writeTestFile(t, testFile, lines(100, "Line %d"))

		result := execTool(t, readTool, "test-call-7", map[string]any{
			"path": testFile, "offset": float64(41), "limit": float64(20),
		})
		output := textOf(result)
		if strings.Contains(output, "Line 40") || strings.Contains(output, "Line 61") {
			t.Fatalf("output = %q", output)
		}
		if !strings.Contains(output, "Line 41") || !strings.Contains(output, "Line 60") {
			t.Fatalf("output = %q", output)
		}
		if !strings.Contains(output, "[40 more lines in file. Use offset=61 to continue.]") {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("should show error when offset is beyond file length", func(t *testing.T) {
		testFile := filepath.Join(testDir, "short.txt")
		writeTestFile(t, testFile, "Line 1\nLine 2\nLine 3")

		err := mustFail(t, readTool, "test-call-8", map[string]any{"path": testFile, "offset": float64(100)})
		if !strings.Contains(err.Error(), "Offset 100 is beyond end of file (3 lines total)") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should include truncation details when truncated", func(t *testing.T) {
		testFile := filepath.Join(testDir, "large-file.txt")
		writeTestFile(t, testFile, lines(2500, "Line %d"))

		result := execTool(t, readTool, "test-call-9", map[string]any{"path": testFile})
		details, ok := result.Details.(*ReadToolDetails)
		if !ok || details.Truncation == nil {
			t.Fatalf("details = %+v", result.Details)
		}
		if !details.Truncation.Truncated || details.Truncation.TruncatedBy != "lines" ||
			details.Truncation.TotalLines != 2500 || details.Truncation.OutputLines != 2000 {
			t.Fatalf("truncation = %+v", details.Truncation)
		}
	})

	t.Run("should detect image MIME type from file magic (not extension)", func(t *testing.T) {
		png, err := base64Decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg==")
		if err != nil {
			t.Fatal(err)
		}
		testFile := filepath.Join(testDir, "image.txt")
		writeTestFile(t, testFile, string(png))

		result := execTool(t, readTool, "test-call-img-1", map[string]any{"path": testFile})
		if !strings.Contains(textOf(result), "Read image file [image/png]") {
			t.Fatalf("output = %q", textOf(result))
		}
		images := imagesOf(result)
		if len(images) != 1 || images[0].MimeType != "image/png" || len(images[0].Data) == 0 {
			t.Fatalf("images = %+v", images)
		}
	})

	t.Run("should read BMP files from disk (photon-unavailable path)", func(t *testing.T) {
		testFile := filepath.Join(testDir, "image.bmp")
		writeTestFile(t, testFile, string(createTinyBmp1x1Red24bpp()))

		result := execTool(t, readTool, "test-call-img-bmp", map[string]any{"path": testFile})
		// pi (with photon) converts to PNG; pig's photon placeholder takes
		// the documented omission path.
		if !strings.Contains(textOf(result), "[Image omitted: could not be converted to a supported inline image format.]") {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should treat files with image extension but non-image content as text", func(t *testing.T) {
		testFile := filepath.Join(testDir, "not-an-image.png")
		writeTestFile(t, testFile, "definitely not a png")

		result := execTool(t, readTool, "test-call-img-2", map[string]any{"path": testFile})
		if !strings.Contains(textOf(result), "definitely not a png") {
			t.Fatalf("output = %q", textOf(result))
		}
		if len(imagesOf(result)) != 0 {
			t.Fatal("unexpected image block")
		}
	})
}

func TestWriteTool(t *testing.T) {
	testDir := newTestDir(t)
	writeTool := CreateWriteTool(cwdForTest(), nil)

	t.Run("should write file contents", func(t *testing.T) {
		testFile := filepath.Join(testDir, "write-test.txt")
		result := execTool(t, writeTool, "test-call-3", map[string]any{"path": testFile, "content": "Test content"})
		if !strings.Contains(textOf(result), "Successfully wrote") || !strings.Contains(textOf(result), testFile) {
			t.Fatalf("output = %q", textOf(result))
		}
		if result.Details != nil {
			t.Fatal("unexpected details")
		}
	})

	t.Run("should create parent directories", func(t *testing.T) {
		testFile := filepath.Join(testDir, "nested", "dir", "test.txt")
		result := execTool(t, writeTool, "test-call-4", map[string]any{"path": testFile, "content": "Nested content"})
		if !strings.Contains(textOf(result), "Successfully wrote") {
			t.Fatalf("output = %q", textOf(result))
		}
	})
}

func TestEditTool(t *testing.T) {
	testDir := newTestDir(t)
	editTool := CreateEditTool(cwdForTest(), nil)

	t.Run("should replace text in file", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-test.txt")
		originalContent := "Hello, world!"
		writeTestFile(t, testFile, originalContent)

		result := execTool(t, editTool, "test-call-5", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "world", "newText": "testing"},
			},
		})
		if !strings.Contains(textOf(result), "Successfully replaced") {
			t.Fatalf("output = %q", textOf(result))
		}
		details, ok := result.Details.(*EditToolDetails)
		if !ok {
			t.Fatalf("details = %+v", result.Details)
		}
		if !strings.Contains(details.Diff, "testing") {
			t.Fatalf("diff = %q", details.Diff)
		}
		for _, want := range []string{"--- ", "+++ ", "@@", "-Hello, world!", "+Hello, testing!"} {
			if !strings.Contains(details.Patch, want) {
				t.Fatalf("patch %q missing %q", details.Patch, want)
			}
		}
		patched, err := applyPatch(originalContent, details.Patch)
		if err != nil {
			t.Fatal(err)
		}
		if patched != "Hello, testing!" {
			t.Fatalf("patched = %q", patched)
		}
	})

	t.Run("should fail if text not found", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-test.txt")
		writeTestFile(t, testFile, "Hello, world!")
		err := mustFail(t, editTool, "test-call-6", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "nonexistent", "newText": "testing"},
			},
		})
		if !strings.Contains(err.Error(), "Could not find the exact text") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should include ENOENT when the edit target does not exist", func(t *testing.T) {
		missingFile := filepath.Join(testDir, "missing.txt")
		err := mustFail(t, editTool, "test-call-6b", map[string]any{
			"path": missingFile,
			"edits": []any{
				map[string]any{"oldText": "hello", "newText": "world"},
			},
		})
		if !strings.Contains(err.Error(), fmt.Sprintf("Could not edit file: %s. Error code: ENOENT.", missingFile)) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should fail if text appears multiple times", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-test.txt")
		writeTestFile(t, testFile, "foo foo foo")
		err := mustFail(t, editTool, "test-call-7", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "foo", "newText": "bar"},
			},
		})
		if !strings.Contains(err.Error(), "Found 3 occurrences") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should replace multiple disjoint regions in one call", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-multi.txt")
		writeTestFile(t, testFile, "alpha\nbeta\ngamma\ndelta\n")

		result := execTool(t, editTool, "test-call-8", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "alpha\n", "newText": "ALPHA\n"},
				map[string]any{"oldText": "gamma\n", "newText": "GAMMA\n"},
			},
		})
		if !strings.Contains(textOf(result), "Successfully replaced 2 block(s)") {
			t.Fatalf("output = %q", textOf(result))
		}
		if readTestFile(t, testFile) != "ALPHA\nbeta\nGAMMA\ndelta\n" {
			t.Fatal("file content mismatch")
		}
		details := result.Details.(*EditToolDetails)
		if !strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") {
			t.Fatalf("diff = %q", details.Diff)
		}
	})

	t.Run("should collapse large unchanged gaps in multi-edit diffs", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-multi-large-gap.txt")
		out := make([]string, 600)
		for i := range out {
			out[i] = fmt.Sprintf("line %03d", i+1)
		}
		writeTestFile(t, testFile, strings.Join(out, "\n")+"\n")

		result := execTool(t, editTool, "test-call-8b", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "line 100\n", "newText": "LINE 100\n"},
				map[string]any{"oldText": "line 300\n", "newText": "LINE 300\n"},
				map[string]any{"oldText": "line 500\n", "newText": "LINE 500\n"},
			},
		})
		diff := result.Details.(*EditToolDetails).Diff
		for _, want := range []string{"LINE 100", "LINE 300", "LINE 500", "..."} {
			if !strings.Contains(diff, want) {
				t.Fatalf("diff missing %q", want)
			}
		}
		if strings.Contains(diff, "line 250") {
			t.Fatal("diff leaks collapsed gap")
		}
		if len(strings.Split(diff, "\n")) >= 50 {
			t.Fatalf("diff too long: %d lines", len(strings.Split(diff, "\n")))
		}
	})

	t.Run("should match edits against the original file, not incrementally", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-multi-original.txt")
		writeTestFile(t, testFile, "foo\nbar\nbaz\n")

		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-call-9", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "foo\n", "newText": "foo bar\n"},
				map[string]any{"oldText": "bar\n", "newText": "BAR\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "foo bar\nBAR\nbaz\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should fail when edits is empty", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-empty-edits.txt")
		writeTestFile(t, testFile, "hello\nworld\n")
		err := mustFail(t, editTool, "test-call-11", map[string]any{"path": testFile, "edits": []any{}})
		if !strings.Contains(err.Error(), "edits must contain at least one replacement") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should fail when multi-edit regions overlap", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-overlap.txt")
		writeTestFile(t, testFile, "one\ntwo\nthree\n")
		err := mustFail(t, editTool, "test-call-12", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "one\ntwo\n", "newText": "ONE\nTWO\n"},
				map[string]any{"oldText": "two\nthree\n", "newText": "TWO\nTHREE\n"},
			},
		})
		if !strings.Contains(err.Error(), "overlap") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should not partially apply edits when one edit fails", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-no-partial.txt")
		originalContent := "alpha\nbeta\ngamma\n"
		writeTestFile(t, testFile, originalContent)
		err := mustFail(t, editTool, "test-call-13", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "alpha\n", "newText": "ALPHA\n"},
				map[string]any{"oldText": "missing\n", "newText": "MISSING\n"},
			},
		})
		if !strings.Contains(err.Error(), "Could not find") {
			t.Fatalf("err = %v", err)
		}
		if readTestFile(t, testFile) != originalContent {
			t.Fatal("file must be unchanged")
		}
	})

	t.Run("should include EACCES for read-only files", func(t *testing.T) {
		testFile := filepath.Join(testDir, "edit-readonly.txt")
		writeTestFile(t, testFile, "hello\n")
		if err := os.Chmod(testFile, 0o444); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(testFile, 0o644)
		err := mustFail(t, editTool, "test-call-14", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "hello", "newText": "world"},
			},
		})
		if !strings.Contains(err.Error(), fmt.Sprintf("Could not edit file: %s. Error code: EACCES.", testFile)) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should include the original error message for unknown edit access errors", func(t *testing.T) {
		genericFailureTool := CreateEditTool(testDir, &EditToolOptions{
			Operations: &EditOperations{
				Access: func(ctx context.Context, path string) error { return fmt.Errorf("disk offline") },
				ReadFile: func(ctx context.Context, path string) ([]byte, error) {
					return []byte("hello\n"), nil
				},
				WriteFile: func(ctx context.Context, path string, content string) error { return nil },
			},
		})
		err := mustFail(t, genericFailureTool, "test-call-16", map[string]any{
			"path": "broken.txt",
			"edits": []any{
				map[string]any{"oldText": "hello", "newText": "world"},
			},
		})
		if !strings.Contains(err.Error(), "Could not edit file: broken.txt. Error: disk offline.") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should include ENOENT in diff preview for missing files", func(t *testing.T) {
		missingFile := filepath.Join(testDir, "missing-preview.txt")
		result, previewErr := ComputeEditsDiff(context.Background(), missingFile, []Edit{{OldText: "hello", NewText: "world"}}, testDir)
		if previewErr.Error == "" {
			t.Fatal("expected preview error")
		}
		expected := fmt.Sprintf("Could not edit file: %s. Error code:", missingFile)
		if !strings.Contains(previewErr.Error, expected) {
			t.Fatalf("error = %q", previewErr.Error)
		}
		_ = result
	})

	t.Run("should include EACCES in diff preview for unreadable files", func(t *testing.T) {
		unreadableFile := filepath.Join(testDir, "unreadable-preview.txt")
		writeTestFile(t, unreadableFile, "hello\n")
		if err := os.Chmod(unreadableFile, 0o222); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(unreadableFile, 0o644)
		result, previewErr := ComputeEditsDiff(context.Background(), unreadableFile, []Edit{{OldText: "hello", NewText: "world"}}, testDir)
		if previewErr.Error == "" {
			t.Fatal("expected preview error")
		}
		expected := fmt.Sprintf("Could not edit file: %s. Error code:", unreadableFile)
		if !strings.Contains(previewErr.Error, expected) {
			t.Fatalf("error = %q", previewErr.Error)
		}
		_ = result
	})
}

func TestBashTool(t *testing.T) {
	testDir := newTestDir(t)

	t.Run("should execute simple commands", func(t *testing.T) {
		bashTool := CreateBashTool(testDir, nil)
		result := execTool(t, bashTool, "test-call-8", map[string]any{"command": "echo 'test output'"})
		if !strings.Contains(textOf(result), "test output") {
			t.Fatalf("output = %q", textOf(result))
		}
		if result.Details != nil {
			t.Fatal("unexpected details")
		}
	})

	t.Run("should handle command errors", func(t *testing.T) {
		bashTool := CreateBashTool(testDir, nil)
		err := mustFail(t, bashTool, "test-call-9", map[string]any{"command": "exit 1"})
		re := regexp.MustCompile(`Command failed|code 1`)
		if !re.MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should respect timeout", func(t *testing.T) {
		bashTool := CreateBashTool(testDir, nil)
		err := mustFail(t, bashTool, "test-call-10", map[string]any{
			"command": "sleep 5", "timeout": 0.05,
		})
		if !strings.Contains(strings.ToLower(err.Error()), "timed out") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should include full output path for truncated timeout and abort errors", func(t *testing.T) {
		for _, testCase := range []struct{ error, expected string }{
			{"timeout:5", "Command timed out after 5 seconds"},
			{"aborted", "Command aborted"},
		} {
			t.Run(testCase.error, func(t *testing.T) {
				operations := BashOperations{
					Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
						for i := 1; i <= 3000; i++ {
							options.OnData([]byte(fmt.Sprintf("%d\n", i)))
						}
						return nil, fmt.Errorf("%s", testCase.error)
					},
				}
				bash := CreateBashTool(testDir, &BashToolOptions{Operations: &operations})

				err := mustFail(t, bash, "test-call-"+testCase.error, map[string]any{"command": "chatty-fail"})
				message := err.Error()
				if !strings.Contains(message, testCase.expected) {
					t.Fatalf("message = %q", message)
				}
				re := regexp.MustCompile(`\[Showing lines \d+-\d+ of \d+\. Full output: `)
				if !re.MatchString(message) {
					t.Fatalf("message = %q", message)
				}
				if strings.Contains(message, "Full output: undefined") {
					t.Fatalf("message = %q", message)
				}
				pathRe := regexp.MustCompile(`Full output: ([^\]\n]+)`)
				match := pathRe.FindStringSubmatch(message)
				if match == nil {
					t.Fatalf("message = %q", message)
				}
				if _, err := os.Stat(match[1]); err != nil {
					t.Fatalf("full output file missing: %v", err)
				}
				fullOutput := readTestFile(t, match[1])
				if !strings.Contains(fullOutput, "1\n2\n3") || !strings.Contains(fullOutput, "2998\n2999\n3000") {
					t.Fatalf("full output = %q", fullOutput)
				}
			})
		}
	})

	t.Run("should throw error when cwd does not exist", func(t *testing.T) {
		nonexistentCwd := "/this/directory/definitely/does/not/exist/12345"
		bashTool := CreateBashTool(nonexistentCwd, nil)
		err := mustFail(t, bashTool, "test-call-11", map[string]any{"command": "echo test"})
		if !strings.Contains(err.Error(), "Working directory does not exist") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should handle process spawn errors", func(t *testing.T) {
		// pi mocks getShellConfig to return a nonexistent shell; pig has no
		// module mock, so the equivalent failure surfaces from a shell path
		// that resolves but cannot execute (EACCES on a non-executable
		// file — declared deviation).
		shellPath := filepath.Join(testDir, "not-executable-shell")
		writeTestFile(t, shellPath, "not executable")
		ops := CreateLocalBashOperations(testDir, &BashLocalOptions{ShellPath: shellPath})
		_, err := ops.Exec(context.Background(), "echo test", testDir, &BashExecOptions{OnData: func([]byte) {}})
		if err == nil || !regexp.MustCompile(`ENOENT|EACCES|permission denied`).MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should pass shellPath through to shell resolution", func(t *testing.T) {
		called := false
		operations := BashOperations{
			Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
				code := 0
				return &code, nil
			},
		}
		// Custom operations bypass shell resolution entirely: the resolve
		// callback is only invoked by the local operations.
		bashWithCustomShell := CreateBashTool(testDir, &BashToolOptions{
			ShellPath:  "/custom/bash",
			Operations: &operations,
		})
		if _, err := bashWithCustomShell.ExecuteWithToolContext(context.Background(), "test-call-12b", map[string]any{"command": "echo test"}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if called {
			t.Fatal("shell resolution should not have run")
		}

		localOps := CreateLocalBashOperations(testDir, &BashLocalOptions{ShellPath: "/custom/bash"})
		_, err := localOps.Exec(context.Background(), "echo test", testDir, &BashExecOptions{OnData: func([]byte) {}})
		if err == nil || !strings.Contains(err.Error(), "Custom shell path not found: /custom/bash") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should send commands over stdin when shell resolution requires it", func(t *testing.T) {
		// pi mocks the shell as a node stdin-echo script; pig writes a real
		// `cat` script at the legacy WSL path, which triggers the stdin
		// transport through getShellConfig's raw-path check.
		shellPath := `C:\Windows\System32\bash.exe`
		writeTestFile(t, filepath.Join(testDir, shellPath), "#!/bin/sh\ncat\n")
		if err := os.Chmod(filepath.Join(testDir, shellPath), 0o755); err != nil {
			t.Fatal(err)
		}
		ops := CreateLocalBashOperations(testDir, &BashLocalOptions{ShellPath: shellPath})
		var chunks []byte
		result, err := ops.Exec(context.Background(),
			"name='World'; echo \"Hello, ${name}!\"; count=3; for i in $(seq 1 ${count}); do echo \"Iteration ${i} of ${count}\"; done",
			testDir, &BashExecOptions{OnData: func(data []byte) { chunks = append(chunks, data...) }})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || *result != 0 {
			t.Fatalf("exit code = %v", result)
		}
		expected := "name='World'; echo \"Hello, ${name}!\"; count=3; for i in $(seq 1 ${count}); do echo \"Iteration ${i} of ${count}\"; done"
		if strings.TrimSpace(string(chunks)) != expected {
			t.Fatalf("output = %q", string(chunks))
		}
	})

	t.Run("should resolve legacy WSL bash.exe to stdin command transport", func(t *testing.T) {
		shellPath := `C:\Windows\System32\bash.exe`
		writeTestFile(t, filepath.Join(testDir, shellPath), "")
		config, err := utilsGetShellConfig(testDir, shellPath)
		if err != nil {
			t.Fatal(err)
		}
		// The transport decision is made on the raw path; pig resolves the
		// spawn path against the env cwd (declared in utils/shell.go).
		if config.CommandTransport != "stdin" || !reflect.DeepEqual(config.Args, []string{"-s"}) {
			t.Fatalf("config = %+v", config)
		}
	})

	t.Run("should prepend command prefix when configured", func(t *testing.T) {
		bash := CreateBashTool(testDir, &BashToolOptions{CommandPrefix: "export TEST_VAR=hello"})
		result := execTool(t, bash, "test-prefix-1", map[string]any{"command": "echo $TEST_VAR"})
		if strings.TrimSpace(textOf(result)) != "hello" {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should include output from both prefix and command", func(t *testing.T) {
		bash := CreateBashTool(testDir, &BashToolOptions{CommandPrefix: "echo prefix-output"})
		result := execTool(t, bash, "test-prefix-2", map[string]any{"command": "echo command-output"})
		if strings.TrimSpace(textOf(result)) != "prefix-output\ncommand-output" {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should work without command prefix", func(t *testing.T) {
		bash := CreateBashTool(testDir, &BashToolOptions{})
		result := execTool(t, bash, "test-prefix-3", map[string]any{"command": "echo no-prefix"})
		if strings.TrimSpace(textOf(result)) != "no-prefix" {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should coalesce streaming updates for chatty output", func(t *testing.T) {
		operations := BashOperations{
			Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
				for i := 0; i < 5000; i++ {
					options.OnData([]byte(fmt.Sprintf("line %d\n", i)))
				}
				code := 0
				return &code, nil
			},
		}
		var updates []agent.AgentToolResult
		bash := CreateBashTool(testDir, &BashToolOptions{Operations: &operations})
		result, err := bash.ExecuteWithToolContext(context.Background(), "test-call-chatty-updates", map[string]any{"command": "chatty"},
			func(update agent.AgentToolResult) { updates = append(updates, update) }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(updates) >= 25 {
			t.Fatalf("update count = %d", len(updates))
		}
		if !strings.Contains(textOf(result), "line 4999") {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should not count a trailing newline as an extra truncated bash output line", func(t *testing.T) {
		operations := BashOperations{
			Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
				for i := 1; i <= 4000; i++ {
					options.OnData([]byte(fmt.Sprintf("line-%04d\n", i)))
				}
				code := 0
				return &code, nil
			},
		}
		bash := CreateBashTool(testDir, &BashToolOptions{Operations: &operations})
		result := execTool(t, bash, "test-call-trailing-newline-line-count", map[string]any{"command": "many-lines"})
		output := textOf(result)
		details := result.Details.(*BashToolDetails)
		if details.Truncation.TotalLines != 4000 || details.Truncation.OutputLines != 2000 {
			t.Fatalf("truncation = %+v", details.Truncation)
		}
		for _, want := range []string{"line-2001", "line-4000", "[Showing lines 2001-4000 of 4000. Full output: "} {
			if !strings.Contains(output, want) {
				t.Fatalf("output missing %q", want)
			}
		}
		if strings.Contains(output, "4001") {
			t.Fatal("output leaks beyond limit")
		}
	})

	t.Run("should decode UTF-8 characters split across output chunks", func(t *testing.T) {
		euro := []byte("€\n")
		operations := BashOperations{
			Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
				options.OnData(euro[:1])
				options.OnData(euro[1:])
				code := 0
				return &code, nil
			},
		}
		bash := CreateBashTool(testDir, &BashToolOptions{Operations: &operations})
		result := execTool(t, bash, "test-call-split-utf8", map[string]any{"command": "split-utf8"})
		if strings.TrimSpace(textOf(result)) != "€" {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should expose local bash operations for extension reuse", func(t *testing.T) {
		ops := CreateLocalBashOperations(testDir, nil)
		var chunks []byte
		env := append(os.Environ(), "TEST_LOCAL_BASH_OPS=from-local-ops")
		result, err := ops.Exec(context.Background(), "echo $TEST_LOCAL_BASH_OPS", testDir, &BashExecOptions{
			OnData: func(data []byte) { chunks = append(chunks, data...) },
			Env:    env,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result == nil || *result != 0 {
			t.Fatalf("exit code = %v", result)
		}
		if strings.TrimSpace(string(chunks)) != "from-local-ops" {
			t.Fatalf("output = %q", string(chunks))
		}
	})

	t.Run("should persist full output when truncation happens by line count only", func(t *testing.T) {
		bash := CreateBashTool(testDir, nil)
		result := execTool(t, bash, "test-call-line-truncation", map[string]any{"command": "seq 3000"})
		output := textOf(result)
		details := result.Details.(*BashToolDetails)
		if !details.Truncation.Truncated || details.Truncation.TruncatedBy != "lines" {
			t.Fatalf("truncation = %+v", details.Truncation)
		}
		if details.FullOutputPath == "" {
			t.Fatal("full output path missing")
		}
		re := regexp.MustCompile(`\[Showing lines \d+-\d+ of \d+\. Full output: `)
		if !re.MatchString(output) || strings.Contains(output, "Full output: undefined") {
			t.Fatalf("output = %q", output)
		}
		for i := 0; i < 20; i++ {
			if _, err := os.Stat(details.FullOutputPath); err == nil {
				break
			}
			timeSleep(10 * timeMilli())
		}
		fullOutput := readTestFile(t, details.FullOutputPath)
		if !strings.Contains(fullOutput, "1\n2\n3") || !strings.Contains(fullOutput, "2998\n2999\n3000") {
			t.Fatalf("full output = %q", fullOutput)
		}
	})
}

func TestGrepTool(t *testing.T) {
	testDir := newTestDir(t)
	grepTool := CreateGrepTool(cwdForTest(), nil)

	t.Run("should include filename when searching a single file", func(t *testing.T) {
		testFile := filepath.Join(testDir, "example.txt")
		writeTestFile(t, testFile, "first line\nmatch line\nlast line")

		result := execTool(t, grepTool, "test-call-11", map[string]any{"pattern": "match", "path": testFile})
		if !strings.Contains(textOf(result), "example.txt:2: match line") {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should respect global limit and include context lines", func(t *testing.T) {
		testFile := filepath.Join(testDir, "context.txt")
		writeTestFile(t, testFile, "before\nmatch one\nafter\nmiddle\nmatch two\nafter two")

		result := execTool(t, grepTool, "test-call-12", map[string]any{
			"pattern": "match", "path": testFile, "limit": float64(1), "context": float64(1),
		})
		output := textOf(result)
		for _, want := range []string{"context.txt-1- before", "context.txt:2: match one", "context.txt-3- after",
			"[1 matches limit reached. Use limit=2 for more, or refine pattern]"} {
			if !strings.Contains(output, want) {
				t.Fatalf("output missing %q", want)
			}
		}
		if strings.Contains(output, "match two") {
			t.Fatal("second match should not be present")
		}
	})

	t.Run("should treat flag-like patterns as search text", func(t *testing.T) {
		marker := filepath.Join(testDir, "grep-injection-marker")
		payload := filepath.Join(testDir, "payload.sh")
		testFile := filepath.Join(testDir, "target.txt")
		writeTestFile(t, payload, fmt.Sprintf("#!/bin/sh\necho executed > %s\ncat \"$1\"\n", marker))
		if err := os.Chmod(payload, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, testFile, "target\n")

		result := execTool(t, grepTool, "test-call-grep-injection", map[string]any{
			"pattern": "--pre=" + payload, "path": testDir,
		})
		if !strings.Contains(textOf(result), "No matches found") {
			t.Fatalf("output = %q", textOf(result))
		}
		if fileExists(marker) {
			t.Fatal("payload was executed")
		}
	})
}

func TestFindTool(t *testing.T) {
	testDir := newTestDir(t)
	findTool := CreateFindTool(cwdForTest(), nil)

	t.Run("should include hidden files that are not gitignored", func(t *testing.T) {
		hiddenDir := filepath.Join(testDir, ".secret")
		if err := os.Mkdir(hiddenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(hiddenDir, "hidden.txt"), "hidden")
		writeTestFile(t, filepath.Join(testDir, "visible.txt"), "visible")

		result := execTool(t, findTool, "test-call-13", map[string]any{"pattern": "**/*.txt", "path": testDir})
		var outputLines []string
		for _, line := range strings.Split(textOf(result), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				outputLines = append(outputLines, line)
			}
		}
		found := map[string]bool{}
		for _, line := range outputLines {
			found[line] = true
		}
		if !found["visible.txt"] || !found[".secret/hidden.txt"] {
			t.Fatalf("output lines = %v", outputLines)
		}
	})

	t.Run("should respect .gitignore", func(t *testing.T) {
		gitDir := newTestDir(t)
		writeTestFile(t, filepath.Join(gitDir, ".gitignore"), "ignored.txt\n")
		writeTestFile(t, filepath.Join(gitDir, "ignored.txt"), "ignored")
		writeTestFile(t, filepath.Join(gitDir, "kept.txt"), "kept")

		result := execTool(t, findTool, "test-call-14", map[string]any{"pattern": "**/*.txt", "path": gitDir})
		output := textOf(result)
		if !strings.Contains(output, "kept.txt") {
			t.Fatalf("output = %q", output)
		}
		if strings.Contains(output, "ignored.txt") {
			t.Fatalf("output = %q", output)
		}
	})

	t.Run("should surface fd glob parse errors", func(t *testing.T) {
		err := mustFail(t, findTool, "test-call-15", map[string]any{"pattern": "[", "path": testDir})
		re := regexp.MustCompile(`error parsing glob|fd exited with code|fd error`)
		if !re.MatchString(err.Error()) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should treat flag-like patterns as search text", func(t *testing.T) {
		result := execTool(t, findTool, "test-call-find-flag-pattern", map[string]any{"pattern": "--help", "path": testDir})
		if !strings.Contains(textOf(result), "No files found matching pattern") {
			t.Fatalf("output = %q", textOf(result))
		}
	})
}

func TestLsTool(t *testing.T) {
	testDir := newTestDir(t)
	lsTool := CreateLsTool(cwdForTest(), nil)

	t.Run("should list dotfiles and directories", func(t *testing.T) {
		writeTestFile(t, filepath.Join(testDir, ".hidden-file"), "secret")
		if err := os.Mkdir(filepath.Join(testDir, ".hidden-dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		result := execTool(t, lsTool, "test-call-15", map[string]any{"path": testDir})
		output := textOf(result)
		if !strings.Contains(output, ".hidden-file") || !strings.Contains(output, ".hidden-dir/") {
			t.Fatalf("output = %q", output)
		}
	})
}

func TestEditToolFuzzyMatching(t *testing.T) {
	editTool := CreateEditTool(cwdForTest(), nil)

	run := func(t *testing.T, name string, fileContent string, oldText string, newText string, wantContent string, wantContains ...string) {
		t.Run(name, func(t *testing.T) {
			testDir := newTestDir(t)
			testFile := filepath.Join(testDir, "fuzzy.txt")
			writeTestFile(t, testFile, fileContent)
			result := execTool(t, editTool, "test-fuzzy", map[string]any{
				"path": testFile,
				"edits": []any{
					map[string]any{"oldText": oldText, "newText": newText},
				},
			})
			if !strings.Contains(textOf(result), "Successfully replaced") {
				t.Fatalf("output = %q", textOf(result))
			}
			content := readTestFile(t, testFile)
			if wantContent != "" && content != wantContent {
				t.Fatalf("content = %q, want %q", content, wantContent)
			}
			for _, want := range wantContains {
				if !strings.Contains(content, want) {
					t.Fatalf("content %q missing %q", content, want)
				}
			}
		})
	}

	run(t, "should match text with trailing whitespace stripped",
		"line one   \nline two  \nline three\n", "line one\nline two\n", "replaced\n", "replaced\nline three\n", "")

	run(t, "should match fullwidth punctuation in Chinese text",
		"你好，世界\n你好（世界）\n", "你好,世界\n你好(世界)\n", "你好，pi\n你好(pi)\n", "你好，pi\n你好(pi)\n", "")

	run(t, "should match compatibility-equivalent Unicode forms",
		"ＡＢＣ１２３\ncafe\u0301\n", "ABC123\ncafé\n", "XYZ789\ncoffee\n", "XYZ789\ncoffee\n", "")

	run(t, "should match smart single quotes to ASCII quotes",
		"console.log(‘hello’);\n", "console.log('hello');", "console.log('world');", "", "world")

	run(t, "should match smart double quotes to ASCII quotes",
		"const msg = “Hello World”;\n", `const msg = "Hello World";`, `const msg = "Goodbye";`, "", "Goodbye")

	run(t, "should match Unicode dashes to ASCII hyphen",
		"range: 1–5\nbreak—here\n", "range: 1-5\nbreak-here", "range: 10-50\nbreak--here", "", "10-50")

	run(t, "should match non-breaking space to regular space",
		"hello world\n", "hello world", "hello universe", "", "universe")

	t.Run("should prefer exact match over fuzzy match", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "exact-preferred.txt")
		writeTestFile(t, testFile, "const x = 'exact';\nconst y = 'other';\n")
		execTool(t, editTool, "test-fuzzy-6", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "const x = 'exact';", "newText": "const x = 'changed';"},
			},
		})
		if readTestFile(t, testFile) != "const x = 'changed';\nconst y = 'other';\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should still fail when text is not found even with fuzzy matching", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "no-match.txt")
		writeTestFile(t, testFile, "completely different content\n")
		err := mustFail(t, editTool, "test-fuzzy-7", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "this does not exist", "newText": "replacement"},
			},
		})
		if !strings.Contains(err.Error(), "Could not find the exact text") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should detect duplicates after fuzzy normalization", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "fuzzy-dups.txt")
		writeTestFile(t, testFile, "hello world   \nhello world\n")
		err := mustFail(t, editTool, "test-fuzzy-8", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "hello world", "newText": "replaced"},
			},
		})
		if !strings.Contains(err.Error(), "Found 2 occurrences") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should support fuzzy matching in multi-edit mode", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "fuzzy-multi.txt")
		writeTestFile(t, testFile, "console.log(‘hello’);\nhello world\n")
		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-fuzzy-9", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "console.log('hello');\n", "newText": "console.log('world');\n"},
				map[string]any{"oldText": "hello world\n", "newText": "hello universe\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "console.log('world');\nhello universe\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should preserve the correct occurrence when fuzzy replacement equals a nearby line", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "fuzzy-preserve-duplicate-line.txt")
		originalContent := "replace me   \nafter   \n"
		writeTestFile(t, testFile, originalContent)
		result := execTool(t, editTool, "test-fuzzy-preserve-duplicate-line", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "replace me\n", "newText": "after\n"},
			},
		})
		expectedContent := "after\nafter   \n"
		if readTestFile(t, testFile) != expectedContent {
			t.Fatalf("content = %q", readTestFile(t, testFile))
		}
		patched, err := applyPatch(originalContent, result.Details.(*EditToolDetails).Patch)
		if err != nil {
			t.Fatal(err)
		}
		if patched != expectedContent {
			t.Fatalf("patched = %q", patched)
		}
	})

	t.Run("should preserve untouched lines and produce an applicable patch for fuzzy multi-edits", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "fuzzy-preserve-multi.txt")
		originalContent := strings.Join([]string{
			"keep before  ",
			"first target  ",
			"first after",
			"keep middle   ",
			"second target  ",
			"second after",
			"keep after  ",
			"",
		}, "\n")
		writeTestFile(t, testFile, originalContent)
		result := execTool(t, editTool, "test-fuzzy-preserve-multi", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "first target\nfirst after", "newText": "FIRST\nFIRST2"},
				map[string]any{"oldText": "second target\nsecond after", "newText": "SECOND\nSECOND2"},
			},
		})
		expectedContent := strings.Join([]string{
			"keep before  ",
			"FIRST",
			"FIRST2",
			"keep middle   ",
			"SECOND",
			"SECOND2",
			"keep after  ",
			"",
		}, "\n")
		if readTestFile(t, testFile) != expectedContent {
			t.Fatalf("content = %q", readTestFile(t, testFile))
		}
		patched, err := applyPatch(originalContent, result.Details.(*EditToolDetails).Patch)
		if err != nil {
			t.Fatal(err)
		}
		if patched != expectedContent {
			t.Fatalf("patched = %q", patched)
		}
	})
}

func TestEditToolCRLFHandling(t *testing.T) {
	editTool := CreateEditTool(cwdForTest(), nil)

	t.Run("should match LF oldText against CRLF file content", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "crlf-test.txt")
		writeTestFile(t, testFile, "line one\r\nline two\r\nline three\r\n")
		result := execTool(t, editTool, "test-crlf-1", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "line two\n", "newText": "replaced line\n"},
			},
		})
		if !strings.Contains(textOf(result), "Successfully replaced") {
			t.Fatalf("output = %q", textOf(result))
		}
	})

	t.Run("should preserve CRLF line endings after edit", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "crlf-preserve.txt")
		writeTestFile(t, testFile, "first\r\nsecond\r\nthird\r\n")
		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-crlf-2", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "second\n", "newText": "REPLACED\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "first\r\nREPLACED\r\nthird\r\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should preserve LF line endings for LF files", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "lf-preserve.txt")
		writeTestFile(t, testFile, "first\nsecond\nthird\n")
		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-lf-1", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "second\n", "newText": "REPLACED\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "first\nREPLACED\nthird\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should detect duplicates across CRLF/LF variants", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "mixed-endings.txt")
		writeTestFile(t, testFile, "hello\r\nworld\r\n---\r\nhello\nworld\n")
		err := mustFail(t, editTool, "test-crlf-dup", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "hello\nworld\n", "newText": "replaced\n"},
			},
		})
		if !strings.Contains(err.Error(), "Found 2 occurrences") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("should preserve UTF-8 BOM after edit", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "bom-test.txt")
		writeTestFile(t, testFile, "\uFEFFfirst\r\nsecond\r\nthird\r\n")
		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-bom", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "second\n", "newText": "REPLACED\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "\uFEFFfirst\r\nREPLACED\r\nthird\r\n" {
			t.Fatal("file content mismatch")
		}
	})

	t.Run("should preserve CRLF line endings and BOM in multi-edit mode", func(t *testing.T) {
		testDir := newTestDir(t)
		testFile := filepath.Join(testDir, "bom-crlf-multi.txt")
		writeTestFile(t, testFile, "\uFEFFfirst\r\nsecond\r\nthird\r\nfourth\r\n")
		if _, err := editTool.ExecuteWithToolContext(context.Background(), "test-crlf-multi", map[string]any{
			"path": testFile,
			"edits": []any{
				map[string]any{"oldText": "second\n", "newText": "SECOND\n"},
				map[string]any{"oldText": "fourth\n", "newText": "FOURTH\n"},
			},
		}, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readTestFile(t, testFile) != "\uFEFFfirst\r\nSECOND\r\nthird\r\nFOURTH\r\n" {
			t.Fatal("file content mismatch")
		}
	})
}

func TestEditToolLegacyInput(t *testing.T) {
	// Port of test/edit-tool-legacy-input.test.ts.
	definition := CreateEditToolDefinition(cwdForTest(), nil)

	t.Run("folds top-level oldText/newText into edits", func(t *testing.T) {
		prepared := definition.PrepareArguments(map[string]any{
			"path": "file.txt", "oldText": "before", "newText": "after",
		})
		want := map[string]any{
			"path":  "file.txt",
			"edits": []any{map[string]any{"oldText": "before", "newText": "after"}},
		}
		if !reflect.DeepEqual(prepared, want) {
			t.Fatalf("prepared = %+v", prepared)
		}
	})

	t.Run("appends legacy replacement to existing edits", func(t *testing.T) {
		prepared := definition.PrepareArguments(map[string]any{
			"path":    "file.txt",
			"edits":   []any{map[string]any{"oldText": "a", "newText": "b"}},
			"oldText": "c", "newText": "d",
		})
		want := map[string]any{
			"path": "file.txt",
			"edits": []any{
				map[string]any{"oldText": "a", "newText": "b"},
				map[string]any{"oldText": "c", "newText": "d"},
			},
		}
		if !reflect.DeepEqual(prepared, want) {
			t.Fatalf("prepared = %+v", prepared)
		}
	})

	t.Run("parses edits from a JSON string", func(t *testing.T) {
		prepared := definition.PrepareArguments(map[string]any{
			"path":  "file.txt",
			"edits": `[{"oldText":"a","newText":"b"}]`,
		})
		want := map[string]any{
			"path":  "file.txt",
			"edits": []any{map[string]any{"oldText": "a", "newText": "b"}},
		}
		if !reflect.DeepEqual(prepared, want) {
			t.Fatalf("prepared = %+v", prepared)
		}
	})

	t.Run("leaves edits alone when the string is not valid JSON", func(t *testing.T) {
		prepared := definition.PrepareArguments(map[string]any{
			"path":  "file.txt",
			"edits": "not json",
		})
		want := map[string]any{"path": "file.txt", "edits": "not json"}
		if !reflect.DeepEqual(prepared, want) {
			t.Fatalf("prepared = %+v", prepared)
		}
	})

	t.Run("prepared args execute correctly", func(t *testing.T) {
		dir := newTestDir(t)
		filePath := filepath.Join(dir, "legacy.txt")
		writeTestFile(t, filePath, "before\n")
		dirDefinition := CreateEditToolDefinition(dir, nil)
		prepared := dirDefinition.PrepareArguments(map[string]any{
			"path": "legacy.txt", "oldText": "before", "newText": "after",
		})
		result, err := dirDefinition.Execute(context.Background(), "tool-1", prepared, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if textOf(result) != "Successfully replaced 1 block(s) in legacy.txt." {
			t.Fatalf("output = %q", textOf(result))
		}
		if readTestFile(t, filePath) != "after\n" {
			t.Fatal("file content mismatch")
		}
	})
}

// cwdForTest returns the process working directory (pi: process.cwd()).
func cwdForTest() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

// applyPatch parses the single-file unified patches produced by
// GenerateUnifiedPatch and applies them to the original text (test-only,
// pi: diff.applyPatch). The "\ No newline at end of file" marker decorates
// the immediately preceding - or + line.
func applyPatch(original string, patch string) (string, error) {
	lines := strings.Split(patch, "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "--- ") || strings.HasPrefix(lines[i], "+++ ")) {
		i++
	}
	resultLines := strings.SplitAfter(original, "\n")
	var oldLines, newLines []string
	lastWasAdd := false
	trimLast := func() {
		if lastWasAdd {
			if len(newLines) > 0 {
				newLines[len(newLines)-1] = strings.TrimSuffix(newLines[len(newLines)-1], "\n")
			}
		} else if len(oldLines) > 0 {
			oldLines[len(oldLines)-1] = strings.TrimSuffix(oldLines[len(oldLines)-1], "\n")
		}
	}
	for i < len(lines) {
		line := lines[i]
		if !strings.HasPrefix(line, "@@ ") {
			i++
			continue
		}
		header := regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@`).FindStringSubmatch(line)
		if header == nil {
			return "", fmt.Errorf("unsupported hunk header: %q", line)
		}
		oldStart, _ := strconv.Atoi(header[1])
		oldCount, _ := strconv.Atoi(header[2])
		newCount, _ := strconv.Atoi(header[4])
		i++
		oldLines, newLines = nil, nil
		for len(oldLines) < oldCount || len(newLines) < newCount {
			if i >= len(lines) {
				return "", fmt.Errorf("truncated patch")
			}
			content := lines[i]
			switch {
			case content == `\ No newline at end of file`:
				trimLast()
			case strings.HasPrefix(content, " "):
				oldLines = append(oldLines, content[1:]+"\n")
				newLines = append(newLines, content[1:]+"\n")
				lastWasAdd = false
			case strings.HasPrefix(content, "-"):
				oldLines = append(oldLines, content[1:]+"\n")
				lastWasAdd = false
			case strings.HasPrefix(content, "+"):
				newLines = append(newLines, content[1:]+"\n")
				lastWasAdd = true
			default:
				return "", fmt.Errorf("unsupported patch line: %q", content)
			}
			i++
		}
		// Consume trailing no-newline markers after the hunk body.
		for i < len(lines) && lines[i] == `\ No newline at end of file` {
			trimLast()
			i++
		}
		want := strings.Join(oldLines, "")
		offset := lineOffset(resultLines, oldStart)
		if offset < 0 {
			return "", fmt.Errorf("patch context not found")
		}
		before, rest := splitAtOffset(resultLines, offset)
		if !strings.HasPrefix(rest, want) {
			return "", fmt.Errorf("patch context mismatch")
		}
		var newBlock strings.Builder
		for _, l := range newLines {
			newBlock.WriteString(l)
		}
		resultLines = splitLinesKeepingEndings(before + newBlock.String())
	}
	return strings.Join(resultLines, ""), nil
}

func lineOffset(keptLines []string, startLine int) int {
	offset := 0
	for i, line := range keptLines {
		if i+1 == startLine {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func splitAtOffset(keptLines []string, offset int) (string, string) {
	var before strings.Builder
	consumed := 0
	i := 0
	for ; i < len(keptLines); i++ {
		if consumed+len(keptLines[i]) > offset {
			break
		}
		before.WriteString(keptLines[i])
		consumed += len(keptLines[i])
	}
	var rest strings.Builder
	for ; i < len(keptLines); i++ {
		rest.WriteString(keptLines[i])
	}
	return before.String(), rest.String()
}

func splitLinesKeepingEndings(content string) []string {
	var out []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			out = append(out, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		out = append(out, content[start:])
	}
	return out
}
