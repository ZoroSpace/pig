// Ports of pi's test/path-utils.test.ts, test/paths.test.ts (the
// paths.go-covered subset), and test/file-mutation-queue.test.ts.
//
// Segment deviations:
//   - The deferred/promise gating of the mutation-queue tests becomes
//     channels; resolvesWithin becomes a select with timeout.
//   - paths.test.ts's "throws for invalid file URLs" case is unportable:
//     pig's fileURLToPath returns the raw path on parse failure (the
//     non-throwing contract declared in utils/paths.go).
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func TestExpandPath(t *testing.T) {
	t.Run("should expand ~ to home directory", func(t *testing.T) {
		result := ExpandPath("~")
		if strings.Contains(result, "~") {
			t.Fatalf("result = %q", result)
		}
	})
	t.Run("should expand ~/path to home directory", func(t *testing.T) {
		result := ExpandPath("~/Documents/file.txt")
		if strings.Contains(result, "~/") {
			t.Fatalf("result = %q", result)
		}
	})
	t.Run("should keep tilde-prefixed filenames literal", func(t *testing.T) {
		if got := ExpandPath("~draft.md"); got != "~draft.md" {
			t.Fatalf("got %q", got)
		}
		if got := ExpandPath("@~draft.md"); got != "~draft.md" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("should normalize Unicode spaces", func(t *testing.T) {
		if got := ExpandPath("file\u00A0name.txt"); got != "file name.txt" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestResolveToCwdPathUtils(t *testing.T) {
	t.Run("should resolve absolute paths as-is", func(t *testing.T) {
		absolutePath := filepath.Join(os.TempDir(), "absolute", "path", "file.txt")
		result := ResolveToCwd(absolutePath, filepath.Join(os.TempDir(), "some", "cwd"))
		if result != absolutePath {
			t.Fatalf("got %q", result)
		}
	})
	t.Run("should resolve relative paths against cwd", func(t *testing.T) {
		result := ResolveToCwd("relative/file.txt", "/some/cwd")
		if want := filepath.Clean("/some/cwd/relative/file.txt"); result != want {
			t.Fatalf("got %q, want %q", result, want)
		}
	})
	t.Run("should resolve tilde-prefixed filenames against cwd", func(t *testing.T) {
		cwd := filepath.Join(os.TempDir(), "pi-path-utils-cwd")
		if got := ResolveToCwd("~draft.md", cwd); got != filepath.Join(cwd, "~draft.md") {
			t.Fatalf("got %q", got)
		}
		if got := ResolveToCwd("@~draft.md", cwd); got != filepath.Join(cwd, "~draft.md") {
			t.Fatalf("got %q", got)
		}
	})
}

func TestResolveReadPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "path-utils-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	t.Run("should resolve existing file path", func(t *testing.T) {
		fileName := "test-file.txt"
		writeTestFile(t, filepath.Join(tempDir, fileName), "content")
		if got := ResolveReadPath(fileName, tempDir); got != filepath.Join(tempDir, fileName) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("should handle NFC vs NFD Unicode normalization", func(t *testing.T) {
		nfdFileName := "fileile.txt"
		nfcFileName := "file\u00e9.txt"
		if nfdFileName == nfcFileName {
			t.Fatal("test setup error")
		}
		writeTestFile(t, filepath.Join(tempDir, nfdFileName), "content")
		result := ResolveReadPath(nfcFileName, tempDir)
		if !strings.HasPrefix(result, tempDir) {
			t.Fatalf("result = %q", result)
		}
	})

	t.Run("should handle curly quotes vs straight quotes (macOS filenames)", func(t *testing.T) {
		curlyQuoteName := "Capture d’cran.txt"
		straightQuoteName := "Capture d'cran.txt"
		writeTestFile(t, filepath.Join(tempDir, curlyQuoteName), "content")
		if got := ResolveReadPath(straightQuoteName, tempDir); got != filepath.Join(tempDir, curlyQuoteName) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("should handle combined NFC + curly quote (French macOS screenshots)", func(t *testing.T) {
		nfcCurlyName := "Capture d’écran.txt"
		nfcStraightName := "Capture d'écran.txt"
		writeTestFile(t, filepath.Join(tempDir, nfcCurlyName), "content")
		if got := ResolveReadPath(nfcStraightName, tempDir); got != filepath.Join(tempDir, nfcCurlyName) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("should handle macOS screenshot AM/PM variant with narrow no-break space", func(t *testing.T) {
		macosName := "Screenshot 2024-01-01 at 10.00.00\u202FAM.png"
		userName := "Screenshot 2024-01-01 at 10.00.00 AM.png"
		writeTestFile(t, filepath.Join(tempDir, macosName), "content")
		if got := ResolveReadPath(userName, tempDir); got != filepath.Join(tempDir, macosName) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("should handle macOS screenshot lowercase am/pm variant (en_AU locale)", func(t *testing.T) {
		macosName := "Screenshot 2024-01-01 at 10.00.00\u202Fam.png"
		userName := "Screenshot 2024-01-01 at 10.00.00 am.png"
		writeTestFile(t, filepath.Join(tempDir, macosName), "content")
		if got := ResolveReadPath(userName, tempDir); got != filepath.Join(tempDir, macosName) {
			t.Fatalf("got %q", got)
		}
	})
}

func TestPathsCanonicalize(t *testing.T) {
	// Port of the canonicalizePath/normalizePath/isLocalPath cases of
	// test/paths.test.ts.
	t.Run("returns the real path for a regular file", func(t *testing.T) {
		dir := newTestDir(t)
		file := filepath.Join(dir, "file.txt")
		writeTestFile(t, file, "x")
		if got := utils.CanonicalizePath(file); got != file {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("resolves symlinks to their targets", func(t *testing.T) {
		dir := newTestDir(t)
		target := filepath.Join(dir, "target.txt")
		link := filepath.Join(dir, "link.txt")
		writeTestFile(t, target, "x")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if got := utils.CanonicalizePath(link); got != target {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("falls back to the raw path when the target does not exist", func(t *testing.T) {
		missing := filepath.Join(newTestDir(t), "missing.txt")
		if got := utils.CanonicalizePath(missing); got != missing {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("isLocalPath vocabulary", func(t *testing.T) {
		for _, value := range []string{"name", "./x", "file:///tmp/x"} {
			if !utils.IsLocalPath(value) {
				t.Fatalf("%q should be local", value)
			}
		}
		for _, value := range []string{"npm:pkg", "git:src", "https://x", "ssh://y"} {
			if utils.IsLocalPath(value) {
				t.Fatalf("%q should not be local", value)
			}
		}
	})
}

// --- file-mutation-queue ----------------------------------------------------

func TestWithFileMutationQueue(t *testing.T) {
	t.Run("serializes operations for the same file", func(t *testing.T) {
		var order []string
		path := "/tmp/file-mutation-queue-same"
		done := make(chan struct{}, 2)
		go func() {
			_, _ = WithFileMutationQueue(context.Background(), path, func() (struct{}, error) {
				order = append(order, "first:start")
				time.Sleep(30 * time.Millisecond)
				order = append(order, "first:end")
				return struct{}{}, nil
			})
			done <- struct{}{}
		}()
		time.Sleep(5 * time.Millisecond)
		go func() {
			_, _ = WithFileMutationQueue(context.Background(), path, func() (struct{}, error) {
				order = append(order, "second:start")
				order = append(order, "second:end")
				return struct{}{}, nil
			})
			done <- struct{}{}
		}()
		<-done
		<-done
		want := []string{"first:start", "first:end", "second:start", "second:end"}
		if strings.Join(order, ",") != strings.Join(want, ",") {
			t.Fatalf("order = %v", order)
		}
	})

	t.Run("preserves both parallel edits on the same file", func(t *testing.T) {
		dir := newTestDir(t)
		filePath := filepath.Join(dir, "parallel-edit.txt")
		writeTestFile(t, filePath, "alpha\nbeta\ngamma\n")

		editTool := CreateEditTool(dir, &EditToolOptions{
			Operations: &EditOperations{
				Access: func(ctx context.Context, path string) error { return nil },
				ReadFile: func(ctx context.Context, path string) ([]byte, error) {
					content, err := os.ReadFile(path)
					time.Sleep(30 * time.Millisecond)
					return content, err
				},
				WriteFile: func(ctx context.Context, path string, content string) error {
					time.Sleep(30 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o644)
				},
			},
		})

		type execResult struct {
			result agent.AgentToolResult
			err    error
		}
		done := make(chan execResult, 2)
		run := func(callID string, oldText string, newText string) {
			result, err := editTool.ExecuteWithToolContext(context.Background(), callID, map[string]any{
				"path": filePath,
				"edits": []any{
					map[string]any{"oldText": oldText, "newText": newText},
				},
			}, nil, nil)
			done <- execResult{agent.AgentToolResult(result), err}
		}
		go run("call-1", "alpha", "ALPHA")
		go run("call-2", "beta", "BETA")
		for i := 0; i < 2; i++ {
			if got := <-done; got.err != nil {
				t.Fatal(got.err)
			}
		}
		if content := readTestFile(t, filePath); content != "ALPHA\nBETA\ngamma\n" {
			t.Fatalf("content = %q", content)
		}
	})

	t.Run("shares the queue between edit and write", func(t *testing.T) {
		dir := newTestDir(t)
		filePath := filepath.Join(dir, "mixed.txt")
		writeTestFile(t, filePath, "original\n")

		editTool := CreateEditTool(dir, &EditToolOptions{
			Operations: &EditOperations{
				Access: func(ctx context.Context, path string) error { return nil },
				ReadFile: func(ctx context.Context, path string) ([]byte, error) {
					content, err := os.ReadFile(path)
					time.Sleep(30 * time.Millisecond)
					return content, err
				},
				WriteFile: func(ctx context.Context, path string, content string) error {
					time.Sleep(30 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o644)
				},
			},
		})
		writeTool := CreateWriteTool(dir, &WriteToolOptions{
			Operations: &WriteOperations{
				Mkdir: func(ctx context.Context, dir string) error { return nil },
				WriteFile: func(ctx context.Context, path string, content string) error {
					time.Sleep(10 * time.Millisecond)
					return os.WriteFile(path, []byte(content), 0o644)
				},
			},
		})

		editDone := make(chan error, 1)
		go func() {
			_, err := editTool.ExecuteWithToolContext(context.Background(), "call-1", map[string]any{
				"path": filePath,
				"edits": []any{
					map[string]any{"oldText": "original", "newText": "edited"},
				},
			}, nil, nil)
			editDone <- err
		}()
		time.Sleep(5 * time.Millisecond)
		writeDone := make(chan error, 1)
		go func() {
			_, err := writeTool.ExecuteWithToolContext(context.Background(), "call-2", map[string]any{
				"path": filePath, "content": "replacement\n",
			}, nil, nil)
			writeDone <- err
		}()
		if err := <-editDone; err != nil {
			t.Fatal(err)
		}
		if err := <-writeDone; err != nil {
			t.Fatal(err)
		}
		if content := readTestFile(t, filePath); content != "replacement\n" {
			t.Fatalf("content = %q", content)
		}
	})

	t.Run("keeps write queue locked while an aborted write is still in flight", func(t *testing.T) {
		dir := newTestDir(t)
		filePath := filepath.Join(dir, "abort-write.txt")
		writeTestFile(t, filePath, "original\n")
		firstWriteStarted := make(chan struct{})
		finishFirstWrite := make(chan struct{})
		secondWriteStarted := false

		writeTool := CreateWriteTool(dir, &WriteToolOptions{
			Operations: &WriteOperations{
				Mkdir: func(ctx context.Context, dir string) error { return nil },
				WriteFile: func(ctx context.Context, path string, content string) error {
					if strings.Contains(content, "first") {
						close(firstWriteStarted)
						<-finishFirstWrite
					} else if strings.Contains(content, "second") {
						secondWriteStarted = true
					}
					return os.WriteFile(path, []byte(content), 0o644)
				},
			},
		})

		ctx, cancel := context.WithCancel(context.Background())
		firstDone := make(chan error, 1)
		go func() {
			_, err := writeTool.ExecuteWithToolContext(ctx, "call-1", map[string]any{
				"path": filePath, "content": "first\n",
			}, nil, nil)
			firstDone <- err
		}()
		<-firstWriteStarted
		cancel()
		secondDone := make(chan error, 1)
		go func() {
			_, err := writeTool.ExecuteWithToolContext(context.Background(), "call-2", map[string]any{
				"path": filePath, "content": "second\n",
			}, nil, nil)
			secondDone <- err
		}()

		time.Sleep(20 * time.Millisecond)
		if secondWriteStarted {
			t.Fatal("second write started while the queue was locked")
		}
		close(finishFirstWrite)
		if err := <-firstDone; err == nil {
			t.Fatal("first write should fail after abort")
		}
		if err := <-secondDone; err != nil {
			t.Fatal(err)
		}
		if content := readTestFile(t, filePath); content != "second\n" {
			t.Fatalf("content = %q", content)
		}
	})
}

var _ = fmt.Sprintf
