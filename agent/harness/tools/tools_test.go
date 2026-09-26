// Port of pi's packages/agent/test/harness/tools.test.ts.
//
// Segment deviations:
//   - The blocking/slow/late/timeout execution environments subclass
//     NodeExecutionEnv in TS; Go cannot override methods by embedding from
//     another package, so the tests wrap *env.NodeExecutionEnv with structs
//     implementing harness.ExecutionEnv. The deferred() promise gates become
//     channels.
//   - diff.applyPatch (used by pi to validate the edit tool's unified patch)
//     is not a harness dependency; a minimal applier lives in this test file.
package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
)

func textOutput(result agent.AgentToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func imageParts(result agent.AgentToolResult) []*types.ImageContent {
	var images []*types.ImageContent
	for _, content := range result.Content {
		if image, ok := content.(*types.ImageContent); ok {
			images = append(images, image)
		}
	}
	return images
}

func createContext(t *testing.T) (ExecutionToolContext, *env.NodeExecutionEnv) {
	t.Helper()
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	testEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	return ExecutionToolContext{Env: testEnv}, testEnv
}

func mustWriteFile(t *testing.T, e harness.FileSystem, path string, content string) {
	t.Helper()
	if err := e.WriteFile(context.Background(), path, []byte(content)); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, e harness.FileSystem, path string) string {
	t.Helper()
	content, err := e.ReadTextFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func background() context.Context { return context.Background() }

// --- execution environment wrappers -------------------------------------

// slowReadExecutionEnv delays ReadTextFile (pi: SlowReadExecutionEnv).
type slowReadExecutionEnv struct {
	*env.NodeExecutionEnv
}

func (e *slowReadExecutionEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	time.Sleep(20 * time.Millisecond)
	return e.NodeExecutionEnv.ReadTextFile(ctx, path)
}

// blockingWriteExecutionEnv gates the first write (pi:
// BlockingWriteExecutionEnv).
type blockingWriteExecutionEnv struct {
	*env.NodeExecutionEnv
	firstWriteStarted  chan struct{}
	finishFirstWrite   chan struct{}
	secondWriteStarted bool
}

func newBlockingWriteExecutionEnv(root string) *blockingWriteExecutionEnv {
	return &blockingWriteExecutionEnv{
		NodeExecutionEnv:  env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root}),
		firstWriteStarted: make(chan struct{}),
		finishFirstWrite:  make(chan struct{}),
	}
}

func (e *blockingWriteExecutionEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	switch string(content) {
	case "first\n":
		close(e.firstWriteStarted)
		<-e.finishFirstWrite
	case "second\n":
		e.secondWriteStarted = true
	}
	return e.NodeExecutionEnv.WriteFile(ctx, path, content)
}

// blockingEditExecutionEnv gates the first edit's write (pi:
// BlockingEditExecutionEnv).
type blockingEditExecutionEnv struct {
	*env.NodeExecutionEnv
	firstEditWriteStarted  chan struct{}
	finishFirstEditWrite   chan struct{}
	firstEditWriteSettled  bool
	secondEditWriteStarted bool
}

func newBlockingEditExecutionEnv(root string) *blockingEditExecutionEnv {
	return &blockingEditExecutionEnv{
		NodeExecutionEnv:      env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root}),
		firstEditWriteStarted: make(chan struct{}),
		finishFirstEditWrite:  make(chan struct{}),
	}
}

func (e *blockingEditExecutionEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	switch string(content) {
	case "ALPHA\nbeta\n":
		close(e.firstEditWriteStarted)
		<-e.finishFirstEditWrite
		// pi: super.writeFile(path, content) — no signal, so the inner
		// write succeeds even after abort.
		if err := e.NodeExecutionEnv.WriteFile(context.Background(), path, content); err != nil {
			return err
		}
		e.firstEditWriteSettled = true
		return nil
	case "ALPHA\nBETA\n", "alpha\nBETA\n":
		e.secondEditWriteStarted = true
	}
	return e.NodeExecutionEnv.WriteFile(ctx, path, content)
}

// lateOutputExecutionEnv emits a chunk after the execution settles (pi:
// LateOutputExecutionEnv).
type lateOutputExecutionEnv struct {
	*env.NodeExecutionEnv
}

func (e *lateOutputExecutionEnv) Exec(ctx context.Context, command string, options *harness.ShellExecOptions) (*harness.ShellExecResult, error) {
	if options != nil && options.OnStdout != nil {
		options.OnStdout("before\n")
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		if options != nil && options.OnStdout != nil {
			options.OnStdout("late\n")
		}
	}()
	return &harness.ShellExecResult{Stdout: "before\n", ExitCode: 0}, nil
}

// timeoutOutputExecutionEnv emits truncated output and then fails with a
// timeout (pi: TimeoutOutputExecutionEnv).
type timeoutOutputExecutionEnv struct {
	*env.NodeExecutionEnv
}

const truncatedOutputLines = utils.DefaultMaxLines + 1

func (e *timeoutOutputExecutionEnv) Exec(ctx context.Context, command string, options *harness.ShellExecOptions) (*harness.ShellExecResult, error) {
	lines := make([]string, truncatedOutputLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d", i+1)
	}
	output := strings.Join(lines, "\n") + "\n"
	if options != nil && options.OnStdout != nil {
		options.OnStdout(output)
	}
	timeout := 0.0
	if options != nil && options.Timeout != nil {
		timeout = *options.Timeout
	}
	return nil, harness.NewExecutionError(harness.ExecCodeTimeout, fmt.Sprintf("timeout:%v", timeout), nil)
}

// createTinyBmp builds a minimal valid BMP (pi: createTinyBmp).
func createTinyBmp() []byte {
	bytes := make([]byte, 58)
	putUint32LE := func(offset int, value uint32) {
		bytes[offset] = byte(value)
		bytes[offset+1] = byte(value >> 8)
		bytes[offset+2] = byte(value >> 16)
		bytes[offset+3] = byte(value >> 24)
	}
	putUint16LE := func(offset int, value uint16) {
		bytes[offset] = byte(value)
		bytes[offset+1] = byte(value >> 8)
	}
	bytes[0] = 0x42
	bytes[1] = 0x4d
	putUint32LE(2, uint32(len(bytes)))
	putUint32LE(10, 54)
	putUint32LE(14, 40)
	putUint32LE(18, 1)
	putUint32LE(22, 1)
	putUint16LE(26, 1)
	putUint16LE(28, 24)
	putUint32LE(34, 4)
	return bytes
}

// --- read ----------------------------------------------------------------

func TestReadTextWithOffsetsLimitsAndContinuationNotices(t *testing.T) {
	context, _ := createContext(t)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %d", i+1)
	}
	mustWriteFile(t, context.Env, "test.txt", strings.Join(lines, "\n"))

	result, err := CreateReadTool(nil).Execute(background(), "read-1", map[string]any{
		"path": "test.txt", "offset": float64(41), "limit": float64(20),
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	output := textOutput(result)
	if strings.Contains(output, "Line 40") || strings.Contains(output, "Line 61") {
		t.Fatalf("output leaks out-of-range lines: %q", output)
	}
	if !strings.Contains(output, "Line 41") || !strings.Contains(output, "Line 60") {
		t.Fatalf("output missing requested lines: %q", output)
	}
	if !strings.Contains(output, "[40 more lines in file. Use offset=61 to continue.]") {
		t.Fatalf("output missing continuation notice: %q", output)
	}
}

func TestReadTruncatesLargeTextByLineCount(t *testing.T) {
	context, _ := createContext(t)
	lines := make([]string, 2500)
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %d", i+1)
	}
	mustWriteFile(t, context.Env, "large.txt", strings.Join(lines, "\n"))

	result, err := CreateReadTool(nil).Execute(background(), "read-2", map[string]any{"path": "large.txt"}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput(result), "[Showing lines 1-2000 of 2500. Use offset=2001 to continue.]") {
		t.Fatalf("missing truncation notice: %q", textOutput(result))
	}
	details := result.Details.(*ReadToolDetails)
	truncation := details.Truncation
	if !truncation.Truncated || truncation.TruncatedBy != "lines" || truncation.TotalLines != 2500 || truncation.OutputLines != 2000 {
		t.Fatalf("truncation = %+v", truncation)
	}
}

func TestReadDoesNotCountTrailingNewlineAtTruncationLimit(t *testing.T) {
	context, _ := createContext(t)
	mustWriteFile(t, context.Env, "exact.txt", strings.Join(make([]string, 2000), "\n")+"\n")

	result, err := CreateReadTool(nil).Execute(background(), "read-exact", map[string]any{"path": "exact.txt"}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if result.Details != nil {
		t.Fatalf("details = %+v", result.Details)
	}
	if strings.Contains(textOutput(result), "Use offset=") {
		t.Fatalf("unexpected continuation notice: %q", textOutput(result))
	}
}

func TestReadRejectsOffsetsBeyondTheFile(t *testing.T) {
	context, _ := createContext(t)
	mustWriteFile(t, context.Env, "short.txt", "one\ntwo\nthree")

	_, err := CreateReadTool(nil).Execute(background(), "read-3", map[string]any{
		"path": "short.txt", "offset": float64(100),
	}, nil, context)
	if err == nil || err.Error() != "Offset 100 is beyond end of file (3 lines total)" {
		t.Fatalf("err = %v", err)
	}
}

func TestReadDetectsSupportedImagesByContent(t *testing.T) {
	context, _ := createContext(t)
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, context.Env, "image.txt", string(png))

	result, err := CreateReadTool(nil).Execute(background(), "read-4", map[string]any{"path": "image.txt"}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput(result), "Read image file [image/png]") {
		t.Fatalf("output = %q", textOutput(result))
	}
	images := imageParts(result)
	if len(images) != 1 || images[0].MimeType != "image/png" || images[0].Data != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("image parts = %+v", images)
	}
}

func TestReadDelegatesImageConversionToInjectedProcessor(t *testing.T) {
	context, _ := createContext(t)
	bmp := createTinyBmp()
	mustWriteFile(t, context.Env, "image.bmp", string(bmp))

	var receivedBytes []byte
	var receivedMimeType string
	var receivedAutoResize bool
	tool := CreateReadTool(&ReadToolOptions{
		AutoResizeImages: lo.ToPtr(false),
		ImageProcessor: func(bytes []byte, mimeType string, options ReadImageProcessorOptions) ReadImageProcessorResult {
			receivedBytes = bytes
			receivedMimeType = mimeType
			receivedAutoResize = options.AutoResizeImages
			return ReadImageProcessorResult{
				OK:       true,
				Data:     "converted",
				MimeType: "image/png",
				Hints:    []string{"[Image converted from image/bmp to image/png.]"},
			}
		},
	})

	result, err := tool.Execute(background(), "read-bmp", map[string]any{"path": "image.bmp"}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if receivedMimeType != "image/bmp" || receivedAutoResize {
		t.Fatalf("received = %s, %v", receivedMimeType, receivedAutoResize)
	}
	if string(receivedBytes) != string(bmp) {
		t.Fatal("processor received different bytes")
	}
	if !strings.Contains(textOutput(result), "[Image converted from image/bmp to image/png.]") {
		t.Fatalf("output = %q", textOutput(result))
	}
	images := imageParts(result)
	if len(images) != 1 || images[0].Data != "converted" || images[0].MimeType != "image/png" {
		t.Fatalf("image parts = %+v", images)
	}
}

// --- write -----------------------------------------------------------------

func TestWriteFilesAndCreatesParentDirectories(t *testing.T) {
	context, testEnv := createContext(t)
	result, err := CreateWriteTool().Execute(background(), "write-1", map[string]any{
		"path": "nested/dir/file.txt", "content": "hello",
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if textOutput(result) != "Successfully wrote 5 bytes to nested/dir/file.txt" {
		t.Fatalf("output = %q", textOutput(result))
	}
	if mustReadFile(t, testEnv, "nested/dir/file.txt") != "hello" {
		t.Fatal("file content mismatch")
	}
}

func TestWriteKeepsMutationQueueLockedUntilAbortedWriteSettles(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	blocked := newBlockingWriteExecutionEnv(root)
	tool := CreateWriteTool()
	ctx, cancel := context.WithCancel(context.Background())

	type execResult struct {
		result agent.AgentToolResult
		err    error
	}
	firstDone := make(chan execResult, 1)
	go func() {
		result, err := tool.Execute(ctx, "write-first", map[string]any{
			"path": "file.txt", "content": "first\n",
		}, nil, ExecutionToolContext{Env: blocked})
		firstDone <- execResult{result, err}
	}()
	<-blocked.firstWriteStarted
	cancel()
	secondDone := make(chan execResult, 1)
	go func() {
		result, err := tool.Execute(background(), "write-second", map[string]any{
			"path": "file.txt", "content": "second\n",
		}, nil, ExecutionToolContext{Env: blocked})
		secondDone <- execResult{result, err}
	}()

	time.Sleep(20 * time.Millisecond)
	if blocked.secondWriteStarted {
		t.Fatal("second write started while the queue was locked")
	}
	close(blocked.finishFirstWrite)
	first := <-firstDone
	if first.err == nil {
		t.Fatal("first write should fail after abort")
	}
	second := <-secondDone
	if second.err != nil {
		t.Fatal(second.err)
	}
	if mustReadFile(t, blocked, "file.txt") != "second\n" {
		t.Fatal("file content mismatch")
	}
}

// --- edit ------------------------------------------------------------------

func TestEditAppliesDisjointEditsAndReturnsBothDiffFormats(t *testing.T) {
	context, testEnv := createContext(t)
	original := "alpha\nbeta\ngamma\ndelta\n"
	mustWriteFile(t, context.Env, "edit.txt", original)

	result, err := CreateEditTool().Execute(background(), "edit-1", map[string]any{
		"path": "edit.txt",
		"edits": []any{
			map[string]any{"oldText": "alpha\n", "newText": "ALPHA\n"},
			map[string]any{"oldText": "gamma\n", "newText": "GAMMA\n"},
		},
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if textOutput(result) != "Successfully replaced 2 block(s) in edit.txt." {
		t.Fatalf("output = %q", textOutput(result))
	}
	details := result.Details.(*EditToolDetails)
	if !strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") {
		t.Fatalf("diff = %q", details.Diff)
	}
	patched, err := applyPatch(original, details.Patch)
	if err != nil {
		t.Fatal(err)
	}
	if patched != "ALPHA\nbeta\nGAMMA\ndelta\n" {
		t.Fatalf("patched = %q", patched)
	}
	if mustReadFile(t, testEnv, "edit.txt") != "ALPHA\nbeta\nGAMMA\ndelta\n" {
		t.Fatal("file content mismatch")
	}
}

func TestEditMatchesAllEditsAgainstOriginalAndRejectsOverlaps(t *testing.T) {
	context, testEnv := createContext(t)
	mustWriteFile(t, context.Env, "edit.txt", "one\ntwo\nthree\n")

	_, err := CreateEditTool().Execute(background(), "edit-2", map[string]any{
		"path": "edit.txt",
		"edits": []any{
			map[string]any{"oldText": "one\ntwo\n", "newText": "ONE\nTWO\n"},
			map[string]any{"oldText": "two\nthree\n", "newText": "TWO\nTHREE\n"},
		},
	}, nil, context)
	if err == nil || !regexp.MustCompile(`overlap`).MatchString(err.Error()) {
		t.Fatalf("err = %v", err)
	}
	if mustReadFile(t, testEnv, "edit.txt") != "one\ntwo\nthree\n" {
		t.Fatal("file must be unchanged")
	}
}

func TestEditRejectsMissingAndDuplicateTargetText(t *testing.T) {
	context, _ := createContext(t)
	mustWriteFile(t, context.Env, "edit.txt", "foo foo foo")
	tool := CreateEditTool()

	_, err := tool.Execute(background(), "edit-3", map[string]any{
		"path": "edit.txt",
		"edits": []any{
			map[string]any{"oldText": "bar", "newText": "baz"},
		},
	}, nil, context)
	if err == nil || !strings.Contains(err.Error(), "Could not find the exact text") {
		t.Fatalf("err = %v", err)
	}

	_, err = tool.Execute(background(), "edit-4", map[string]any{
		"path": "edit.txt",
		"edits": []any{
			map[string]any{"oldText": "foo", "newText": "bar"},
		},
	}, nil, context)
	if err == nil || !strings.Contains(err.Error(), "Found 3 occurrences") {
		t.Fatalf("err = %v", err)
	}
}

func TestEditKeepsMutationQueueLockedUntilAbortedEditWriteSettles(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	blocked := newBlockingEditExecutionEnv(root)
	mustWriteFile(t, blocked, "file.txt", "alpha\nbeta\n")
	tool := CreateEditTool()
	ctx, cancel := context.WithCancel(context.Background())

	type execResult struct {
		result agent.AgentToolResult
		err    error
	}
	firstDone := make(chan execResult, 1)
	go func() {
		result, err := tool.Execute(ctx, "edit-first", map[string]any{
			"path": "file.txt",
			"edits": []any{
				map[string]any{"oldText": "alpha", "newText": "ALPHA"},
			},
		}, nil, ExecutionToolContext{Env: blocked})
		firstDone <- execResult{result, err}
	}()
	<-blocked.firstEditWriteStarted
	cancel()
	secondDone := make(chan execResult, 1)
	go func() {
		result, err := tool.Execute(background(), "edit-second", map[string]any{
			"path": "file.txt",
			"edits": []any{
				map[string]any{"oldText": "beta", "newText": "BETA"},
			},
		}, nil, ExecutionToolContext{Env: blocked})
		secondDone <- execResult{result, err}
	}()

	time.Sleep(20 * time.Millisecond)
	if blocked.secondEditWriteStarted {
		t.Fatal("second edit started while the queue was locked")
	}
	close(blocked.finishFirstEditWrite)
	first := <-firstDone
	if first.err == nil || first.err.Error() != "Operation aborted" {
		t.Fatalf("first edit err = %v", first.err)
	}
	if second := <-secondDone; second.err != nil {
		t.Fatal(second.err)
	}
	if !blocked.firstEditWriteSettled {
		t.Fatal("first edit write never settled")
	}
	if mustReadFile(t, blocked, "file.txt") != "ALPHA\nBETA\n" {
		t.Fatal("file content mismatch")
	}
}

func TestEditSerializesConcurrentEditsThroughCanonicalAndSymlinkPaths(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	slow := &slowReadExecutionEnv{NodeExecutionEnv: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})}
	mustWriteFile(t, slow, "target.txt", "alpha\nbeta\ngamma\n")
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	tool := CreateEditTool()

	type execResult struct {
		result agent.AgentToolResult
		err    error
	}
	done := make(chan execResult, 2)
	run := func(callID string, path string, oldText string, newText string) {
		result, err := tool.Execute(background(), callID, map[string]any{
			"path": path,
			"edits": []any{
				map[string]any{"oldText": oldText, "newText": newText},
			},
		}, nil, ExecutionToolContext{Env: slow})
		done <- execResult{result, err}
	}
	go run("edit-target", "target.txt", "alpha", "ALPHA")
	go run("edit-link", "link.txt", "beta", "BETA")
	for i := 0; i < 2; i++ {
		if got := <-done; got.err != nil {
			t.Fatal(got.err)
		}
	}
	if mustReadFile(t, slow, "target.txt") != "ALPHA\nBETA\ngamma\n" {
		t.Fatalf("content = %q", mustReadFile(t, slow, "target.txt"))
	}
}

func TestEditEditsRegularFilesThroughSymlinks(t *testing.T) {
	context, testEnv := createContext(t)
	mustWriteFile(t, context.Env, "target.txt", "before\n")
	if err := os.Symlink(filepath.Join(testEnv.Cwd(), "target.txt"), filepath.Join(testEnv.Cwd(), "link.txt")); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateEditTool().Execute(background(), "edit-symlink", map[string]any{
		"path": "link.txt",
		"edits": []any{
			map[string]any{"oldText": "before", "newText": "after"},
		},
	}, nil, context); err != nil {
		t.Fatal(err)
	}
	if mustReadFile(t, testEnv, "target.txt") != "after\n" {
		t.Fatal("file content mismatch")
	}
}

func TestEditPreservesBomAndCrlfLineEndings(t *testing.T) {
	context, testEnv := createContext(t)
	mustWriteFile(t, context.Env, "edit.txt", "\uFEFFone\r\ntwo\r\n")

	if _, err := CreateEditTool().Execute(background(), "edit-5", map[string]any{
		"path": "edit.txt",
		"edits": []any{
			map[string]any{"oldText": "two", "newText": "TWO"},
		},
	}, nil, context); err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, testEnv, "edit.txt"); got != "\uFEFFone\r\nTWO\r\n" {
		t.Fatalf("content = %q", got)
	}
}

// --- bash ------------------------------------------------------------------

func TestBashExecutesCommandsAndCombinesStdoutAndStderr(t *testing.T) {
	context, _ := createContext(t)
	result, err := CreateBashTool(nil).Execute(background(), "bash-1", map[string]any{
		"command": "printf out; printf err >&2",
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput(result), "out") || !strings.Contains(textOutput(result), "err") {
		t.Fatalf("output = %q", textOutput(result))
	}
}

func TestBashReportsNonzeroExitsAndTimeouts(t *testing.T) {
	context, _ := createContext(t)
	tool := CreateBashTool(nil)

	_, err := tool.Execute(background(), "bash-2", map[string]any{"command": "printf failed; exit 7"}, nil, context)
	if err == nil {
		t.Fatal("expected error")
	}
	re := regexp.MustCompile(`failed[\s\S]*Command exited with code 7`)
	if !re.MatchString(err.Error()) {
		t.Fatalf("err = %v", err)
	}

	_, err = tool.Execute(background(), "bash-3", map[string]any{
		"command": "sleep 2", "timeout": 0.01,
	}, nil, context)
	if err == nil || !strings.Contains(err.Error(), "Command timed out after 0.01 seconds") {
		t.Fatalf("err = %v", err)
	}
}

func TestBashPreservesTruncatedOutputWhenACommandTimesOut(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	context := ExecutionToolContext{Env: &timeoutOutputExecutionEnv{
		NodeExecutionEnv: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root}),
	}}
	_, execErr := CreateBashTool(nil).Execute(background(), "bash-timeout-output", map[string]any{
		"command": "emit-output-then-time-out", "timeout": 0.05,
	}, nil, context)
	if execErr == nil {
		t.Fatal("expected error")
	}
	message := execErr.Error()
	if !strings.Contains(message, "Command timed out after 0.05 seconds") {
		t.Fatalf("message = %q", message)
	}
	re := regexp.MustCompile(`Full output: ([^\]\n]+)`)
	match := re.FindStringSubmatch(message)
	if match == nil {
		t.Fatalf("message = %q", message)
	}
	fullOutput := mustReadFile(t, context.Env, match[1])
	if !strings.Contains(fullOutput, "line-1\nline-2") {
		t.Fatalf("full output = %q", fullOutput)
	}
	if !strings.Contains(fullOutput, fmt.Sprintf("line-%d\nline-%d", utils.DefaultMaxLines, truncatedOutputLines)) {
		t.Fatalf("full output = %q", fullOutput)
	}
}

func TestBashIgnoresOutputCallbacksAfterExecutionSettles(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	testEnv := &lateOutputExecutionEnv{NodeExecutionEnv: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})}
	var updates []string
	result, err := CreateBashTool(nil).Execute(background(), "bash-late", map[string]any{
		"command": "late",
	}, func(partial agent.AgentToolResult) {
		updates = append(updates, textOutput(partial))
	}, ExecutionToolContext{Env: testEnv})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	if textOutput(result) != "before\n" {
		t.Fatalf("output = %q", textOutput(result))
	}
	for _, update := range updates {
		if strings.Contains(update, "late") {
			t.Fatalf("late update leaked: %q", updates)
		}
	}
}

func TestBashReportsTotalSizeOfOversizedFinalLine(t *testing.T) {
	context, _ := createContext(t)
	result, err := CreateBashTool(nil).Execute(background(), "bash-long-line", map[string]any{
		"command": "printf '%060000d' 0",
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`Showing last 50\.0KB of line 1 \(line is 58\.6KB\)\. Full output:`)
	if !re.MatchString(textOutput(result)) {
		t.Fatalf("output = %q", textOutput(result))
	}
}

func TestBashPreparesCommandCwdAndExplicitEnvironment(t *testing.T) {
	root, err := os.MkdirTemp("", "pig-tools-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	testEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{
		Cwd:      resolved,
		ShellEnv: map[string]string{"PI_BASH_PREPARE_INHERITED": "inherited"},
	})
	if err := testEnv.CreateDir(background(), "workspace", nil); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(resolved, "workspace")
	toolContext := ExecutionToolContext{Env: testEnv}

	var receivedContext ExecutionToolContext
	var receivedSignal context.Context
	tool := CreateBashTool(&BashToolOptions{
		CommandPrefix: "prefix=ready",
		Prepare: func(ctx context.Context, execution *BashExecution, ctxAny any) {
			receivedContext = ctxAny.(ExecutionToolContext)
			receivedSignal = ctx
			execution.Cwd = workspace
			execution.Env = map[string]string{"PI_BASH_PREPARE_EXPLICIT": "explicit"}
			execution.InheritEnv = false
			execution.Command += "\nprintf '%s:%s:%s:%s' \"$prefix\" \"${PI_BASH_PREPARE_INHERITED-}\" \"$PI_BASH_PREPARE_EXPLICIT\" \"$PWD\""
		},
	})

	result, err := tool.Execute(background(), "bash-prepare", map[string]any{"command": ":"}, nil, toolContext)
	if err != nil {
		t.Fatal(err)
	}
	if receivedContext.Env != toolContext.Env {
		t.Fatal("prepare received a different context")
	}
	if receivedSignal == nil || receivedSignal.Err() != nil {
		t.Fatal("prepare did not receive the execution context")
	}
	canonical, err := testEnv.CanonicalPath(background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if textOutput(result) != fmt.Sprintf("ready::explicit:%s", canonical) {
		t.Fatalf("output = %q", textOutput(result))
	}
}

func TestBashSupportsCommandPrefixes(t *testing.T) {
	context, _ := createContext(t)
	result, err := CreateBashTool(&BashToolOptions{CommandPrefix: "value=hello"}).Execute(background(), "bash-4", map[string]any{
		"command": "printf $value",
	}, nil, context)
	if err != nil {
		t.Fatal(err)
	}
	if textOutput(result) != "hello" {
		t.Fatalf("output = %q", textOutput(result))
	}
}

func TestBashCoalescesUpdatesAndPersistsTruncatedFullOutput(t *testing.T) {
	context, testEnv := createContext(t)
	var updates []agent.AgentToolResult
	result, err := CreateBashTool(nil).Execute(background(), "bash-5", map[string]any{
		"command": "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done",
	}, func(partial agent.AgentToolResult) {
		updates = append(updates, partial)
	}, context)
	if err != nil {
		t.Fatal(err)
	}

	if len(updates) >= 25 {
		t.Fatalf("update count = %d", len(updates))
	}
	details := result.Details.(*BashToolDetails)
	truncation := details.Truncation
	if !truncation.Truncated || truncation.TruncatedBy != "lines" || truncation.TotalLines != 3000 || truncation.OutputLines != 2000 {
		t.Fatalf("truncation = %+v", truncation)
	}
	if !strings.Contains(textOutput(result), "line-3000") {
		t.Fatalf("output = %q", textOutput(result))
	}
	if details.FullOutputPath == "" {
		t.Fatal("full output path missing")
	}
	finalUpdate := updates[len(updates)-1]
	if !strings.Contains(textOutput(finalUpdate), "line-3000") {
		t.Fatalf("final update = %q", textOutput(finalUpdate))
	}
	finalDetails := finalUpdate.Details.(*BashToolDetails)
	if finalDetails.Truncation.TotalLines != 3000 || finalDetails.Truncation.TotalBytes == 0 {
		t.Fatalf("final update details = %+v", finalDetails)
	}
	if finalDetails.FullOutputPath != details.FullOutputPath {
		t.Fatal("full output path changed between updates")
	}
	fullOutput := mustReadFile(t, testEnv, details.FullOutputPath)
	if !strings.Contains(fullOutput, "line-1\nline-2") || !strings.Contains(fullOutput, "line-2999\nline-3000") {
		t.Fatalf("full output = %q", fullOutput)
	}
}

// TestBashUpdateThrottleNeverOverlapsOnUpdate pins the throttle's consumer
// contract: onUpdate never overlaps itself. pi runs every emit site (direct
// call, setTimeout callback) on one event loop, so TS consumers can never
// observe concurrent onUpdate calls; the timer goroutine here is the
// setTimeout equivalent and must serialize the same way. Regression lock for
// docs/fix/2026-09-24-bash-update-throttle-onupdate-overlap.md (the S8-4/S9
// recorded bash-coalescing flake). Semantic assertion — red without the fix
// even without -race.
func TestBashUpdateThrottleNeverOverlapsOnUpdate(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	onUpdate := func(agent.AgentToolResult) {
		n := inFlight.Add(1)
		for {
			current := maxInFlight.Load()
			if n <= current || maxInFlight.CompareAndSwap(current, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // widen the overlap window
		inFlight.Add(-1)
	}
	throttle := &bashUpdateThrottle{
		onUpdate:   onUpdate,
		latest:     func() utils.ShellCaptureProgress { return utils.ShellCaptureProgress{} },
		lastUpdate: time.Now().Add(-95 * time.Millisecond), // next timer delay ≈ 5ms
	}

	// Timer-path emission enters onUpdate (sleeps 50ms).
	throttle.scheduleOutputUpdate()
	deadline := time.Now().Add(2 * time.Second)
	for inFlight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if inFlight.Load() == 0 {
		t.Fatal("timer emission never entered onUpdate")
	}

	// Direct-path emission while the timer emission is in flight: must wait
	// for it instead of overlapping (scheduleOutputUpdate re-arms dirty so
	// the direct emit passes the check).
	throttle.scheduleOutputUpdate()
	throttle.emitOutputUpdate()
	throttle.clearUpdateTimer()

	if max := maxInFlight.Load(); max != 1 {
		t.Fatalf("onUpdate overlapped itself: max concurrency = %d, want 1", max)
	}
}

// --- minimal unified patch applier (pi: diff.applyPatch, test-only) -------

// applyPatch parses the single-file unified patches produced by
// CreateTwoFilesPatch and applies them to the original text.
func applyPatch(original string, patch string) (string, error) {
	lines := strings.Split(patch, "\n")
	i := 0
	for i < len(lines) && (strings.HasPrefix(lines[i], "--- ") || strings.HasPrefix(lines[i], "+++ ")) {
		i++
	}
	resultLines := strings.SplitAfter(original, "\n")
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
		var oldLines, newLines []string
		for len(oldLines) < oldCount || len(newLines) < newCount {
			if i >= len(lines) {
				return "", errors.New("truncated patch")
			}
			content := lines[i]
			if content == `\ No newline at end of file` {
				i++
				continue
			}
			switch {
			case strings.HasPrefix(content, " "):
				oldLines = append(oldLines, content[1:]+"\n")
				newLines = append(newLines, content[1:]+"\n")
			case strings.HasPrefix(content, "-"):
				oldLines = append(oldLines, content[1:]+"\n")
			case strings.HasPrefix(content, "+"):
				newLines = append(newLines, content[1:]+"\n")
			default:
				return "", fmt.Errorf("unsupported patch line: %q", content)
			}
			i++
		}
		// Strip the synthesized trailing newline added to lines whose
		// original content had none at EOF (the patch marked it with
		// "\ No newline at end of file").
		trimEOF := func(slice []string) {
			if len(slice) > 0 && strings.HasSuffix(slice[len(slice)-1], "\n") &&
				!strings.HasSuffix(original, "\n") {
				last := len(original)
				_ = last
			}
		}
		trimEOF(oldLines)
		trimEOF(newLines)
		// Locate the old block (1-based oldStart) and splice.
		want := strings.Join(oldLines, "")
		offset := lineOffset(resultLines, oldStart)
		if offset < 0 {
			return "", errors.New("patch context not found")
		}
		var applied string
		for _, line := range resultLines {
			_ = line
			break
		}
		_ = applied
		before, rest := splitAtOffset(resultLines, offset, len(want))
		if !strings.HasPrefix(rest, want) {
			return "", errors.New("patch context mismatch")
		}
		var newBlock strings.Builder
		for _, line := range newLines {
			newBlock.WriteString(line)
		}
		resultLines = splitLinesKeepingEndings(before + newBlock.String())
	}
	return strings.Join(resultLines, ""), nil
}

// lineOffset returns the byte offset of the given 1-based line in the joined
// content.
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

// splitAtOffset splits kept lines at a byte offset and returns the text
// before it plus the remaining text.
func splitAtOffset(keptLines []string, offset int, _ int) (string, string) {
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

// splitLinesKeepingEndings splits content keeping each line's trailing
// newline.
func splitLinesKeepingEndings(content string) []string {
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

// pi: "detects the complete %s signature" — GIF87a/GIF89a are required; a
// bare "GIF" prefix no longer classifies as an image (#9755, 47a18e37b).
func TestDetectSupportedImageMimeTypeRequiresCompleteGifSignature(t *testing.T) {
	for _, signature := range []string{"GIF87a", "GIF89a"} {
		if got := DetectSupportedImageMimeType([]byte(signature)); got != "image/gif" {
			t.Fatalf("DetectSupportedImageMimeType(%q) = %q, want image/gif", signature, got)
		}
	}
	// pig-only: pins the #9755 regression itself — pi's it.each positives
	// pass under the old bare-"GIF" prefix check too.
	for _, prefix := range []string{"GIF", "GIF89", "GIF87x"} {
		if got := DetectSupportedImageMimeType([]byte(prefix)); got != "" {
			t.Fatalf("DetectSupportedImageMimeType(%q) = %q, want empty", prefix, got)
		}
	}
}
