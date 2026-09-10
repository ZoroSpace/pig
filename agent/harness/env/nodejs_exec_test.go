// Ports of the exec half of pi's packages/agent/test/harness/
// nodejs-env.test.ts (the FileSystem half arrives with the env test batch
// that owns nodejs.go's FileSystem members). The win32-only
// "settles after the shell exits when a detached descendant retains
// inherited stdio" case is skipped: its node child_process/detached
// scaffolding has no Go counterpart, and the POSIX tree-kill path is covered
// by the cleanup test below.
package env

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/utils"
)

func newTestEnv(t *testing.T, options NodeExecutionEnvOptions) (*NodeExecutionEnv, string) {
	t.Helper()
	root, err := os.MkdirTemp("", "pig-env-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	// bash reports $PWD via getcwd, which canonicalizes symlinks (macOS
	// /tmp → /private/tmp); pre-resolve so comparisons match.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	options.Cwd = resolved
	return NewNodeExecutionEnv(options), resolved
}

func mustExec(t *testing.T, e *NodeExecutionEnv, ctx context.Context, command string, options *harness.ShellExecOptions) *harness.ShellExecResult {
	t.Helper()
	result, err := e.Exec(ctx, command, options)
	if err != nil {
		t.Fatalf("exec %q failed: %v", command, err)
	}
	return result
}

func TestExecCwdAndEnvOverrides(t *testing.T) {
	e, root := newTestEnv(t, NodeExecutionEnvOptions{})
	result := mustExec(t, e, context.Background(), `printf '%s:%s' "$PWD" "$NODE_ENV_TEST"`, &harness.ShellExecOptions{
		Env: map[string]string{"NODE_ENV_TEST": "ok"},
	})
	if result.Stdout != root+":ok" || result.Stderr != "" || result.ExitCode != 0 {
		t.Fatalf("got %+v", result)
	}
}

func TestExecShellEnvStringOverrides(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		expected  string
	}{
		{"a missing override preserves the base value", nil, "x:/stale/parent.jsonl"},
		{"an empty override shadows the base value", map[string]string{"PI_SESSION_FILE": ""}, "x:"},
		{"a string override replaces the base value", map[string]string{"PI_SESSION_FILE": "/sessions/current.jsonl"}, "x:/sessions/current.jsonl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := newTestEnv(t, NodeExecutionEnvOptions{
				ShellEnv: map[string]string{
					"PI_SESSION_FILE":            "/stale/parent.jsonl",
					"PI_CODING_AGENT":            "true",
					"PI_NODE_ENV_PRESERVED_TEST": "preserved",
				},
			})
			result := mustExec(t, e, context.Background(),
				`printf '%s:%s|%s|%s' "${PI_SESSION_FILE+x}" "${PI_SESSION_FILE-}" "$PI_CODING_AGENT" "$PI_NODE_ENV_PRESERVED_TEST"`,
				&harness.ShellExecOptions{Env: tc.overrides})
			if result.Stdout != tc.expected+"|true|preserved" {
				t.Fatalf("stdout = %q", result.Stdout)
			}
		})
	}
}

func TestExecReplaceInheritedEnvironment(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{
		ShellEnv: map[string]string{"PI_NODE_ENV_CONFIGURED_TEST": "configured"},
	})
	result := mustExec(t, e, context.Background(),
		`printf '%s:%s:%s' "${PI_NODE_ENV_INHERITED_TEST-}" "${PI_NODE_ENV_CONFIGURED_TEST-}" "${PI_NODE_ENV_EXPLICIT_TEST-}"`,
		&harness.ShellExecOptions{
			InheritEnv: lo.ToPtr(false),
			Env:        map[string]string{"PI_NODE_ENV_EXPLICIT_TEST": "explicit"},
		})
	if result.Stdout != "::explicit" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestExecStdinTransportForLegacyWSLBashPaths(t *testing.T) {
	_, root := newTestEnv(t, NodeExecutionEnvOptions{})
	// pi mocks process.platform = win32 and resolves the shell via the
	// custom shellPath; the transport decision is path-based, so pig writes
	// the fake bash at the literal legacy path and lets getShellConfig pick
	// the stdin transport. Windows is skipped (taskkill-only tree kills).
	if isWindows() {
		t.Skip("legacy WSL path mapping is POSIX-filename based")
	}
	shellPath := filepath.Join(root, `C:\Windows\System32\bash.exe`)
	script := "#!/bin/sh\nprintf 'args:%s\\n' \"$*\" >&2\nexec /bin/bash \"$@\"\n"
	if err := os.WriteFile(shellPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	wslEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: `C:\Windows\System32\bash.exe`})
	result := mustExec(t, wslEnv, context.Background(), `name='World'; echo "Hello, ${name}!"`, nil)
	if result.Stdout != "Hello, World!\n" || result.Stderr != "args:-s\n" || result.ExitCode != 0 {
		t.Fatalf("got %+v", result)
	}
}

func TestCleanupTerminatesActiveShellProcesses(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	ctx := context.Background()
	type execResult struct {
		result *harness.ShellExecResult
		err    error
	}
	done := make(chan execResult, 1)
	go func() {
		result, err := e.Exec(ctx, "touch started; sleep 60", nil)
		done <- execResult{result, err}
	}()
	started := false
	for attempt := 0; attempt < 100; attempt++ {
		exists, err := e.Exists(ctx, "started")
		if err == nil && exists {
			started = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !started {
		t.Fatal("command never created the marker file")
	}
	if err := e.Cleanup(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("exec failed: %v", got.err)
		}
		if got.result.ExitCode != 0 {
			t.Fatalf("exit code = %d (pi: null close code → 0)", got.result.ExitCode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not settle after cleanup")
	}
}

func TestExecStreamsStdoutAndStderrChunks(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	var mu sync.Mutex
	stdout, stderr := "", ""
	result := mustExec(t, e, context.Background(), "printf out; printf err >&2", &harness.ShellExecOptions{
		OnStdout: func(chunk string) { mu.Lock(); stdout += chunk; mu.Unlock() },
		OnStderr: func(chunk string) { mu.Lock(); stderr += chunk; mu.Unlock() },
	})
	mu.Lock()
	defer mu.Unlock()
	if result.Stdout != "out" || result.Stderr != "err" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if stdout != "out" || stderr != "err" {
		t.Fatalf("chunks: %q / %q", stdout, stderr)
	}
}

func TestExecMissingWorkingDirectory(t *testing.T) {
	_, root := newTestEnv(t, NodeExecutionEnvOptions{})
	e2 := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: filepath.Join(root, "missing")})
	_, err := e2.Exec(context.Background(), "printf ok", nil)
	execErr, ok := err.(*harness.ExecutionError)
	if !ok || execErr.Code != harness.ExecCodeSpawnError || !strings.Contains(execErr.Error(), "Working directory does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecNonZeroExitCodeIsSuccessfulResult(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	result := mustExec(t, e, context.Background(), "exit 7", nil)
	if result.Stdout != "" || result.Stderr != "" || result.ExitCode != 7 {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecTimeout(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	timeout := 0.01
	_, err := e.Exec(context.Background(), "sleep 5", &harness.ShellExecOptions{Timeout: &timeout})
	execErr, ok := err.(*harness.ExecutionError)
	if !ok || execErr.Code != harness.ExecCodeTimeout {
		t.Fatalf("err = %v", err)
	}
}

func TestExecCallbackError(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	_, err := e.Exec(context.Background(), "printf out", &harness.ShellExecOptions{
		OnStdout: func(chunk string) { panic("callback failed") },
	})
	execErr, ok := err.(*harness.ExecutionError)
	if !ok || execErr.Code != harness.ExecCodeCallbackError || !strings.HasPrefix(execErr.Error(), "callback failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecShellUnavailableAndSpawnError(t *testing.T) {
	e, root := newTestEnv(t, NodeExecutionEnvOptions{})
	_ = e
	missingShellEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: filepath.Join(root, "missing-shell")})
	if _, err := missingShellEnv.Exec(context.Background(), "printf ok", nil); err == nil {
		t.Fatal("expected shell_unavailable")
	} else if execErr := err.(*harness.ExecutionError); execErr.Code != harness.ExecCodeShellUnavailable {
		t.Fatalf("err = %v", err)
	}

	shellPath := filepath.Join(root, "not-executable-shell")
	ctx := context.Background()
	if err := e.WriteFile(ctx, shellPath, []byte("not executable")); err != nil {
		t.Fatal(err)
	}
	spawnErrorEnv := NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: root, ShellPath: shellPath})
	if _, err := spawnErrorEnv.Exec(ctx, "printf ok", nil); err == nil {
		t.Fatal("expected spawn_error")
	} else if execErr := err.(*harness.ExecutionError); execErr.Code != harness.ExecCodeSpawnError {
		t.Fatalf("err = %v", err)
	}
}

func TestExecAborted(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	type execResult struct {
		result *harness.ShellExecResult
		err    error
	}
	done := make(chan execResult, 1)
	go func() {
		result, err := e.Exec(ctx, "sleep 5", nil)
		done <- execResult{result, err}
	}()
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("expected aborted error")
		}
		if execErr := got.err.(*harness.ExecutionError); execErr.Code != harness.ExecCodeAborted {
			t.Fatalf("err = %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aborted exec did not settle")
	}
}

func TestExecLargeShellOutputSpillsToFullOutputFile(t *testing.T) {
	e, _ := newTestEnv(t, NodeExecutionEnvOptions{})
	result, err := utils.ExecuteShellWithCapture(context.Background(), e, "yes line | head -n 15000", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.FullOutputPath == "" {
		t.Fatalf("result = %+v", result.ShellCaptureProgress)
	}
	fullOutput, err := e.ReadTextFile(context.Background(), result.FullOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Split(fullOutput, "\n")) <= 10000 {
		t.Fatal("full output too short")
	}
	if len(result.Output) >= len(fullOutput) {
		t.Fatal("tail output should be shorter than full output")
	}
}

// isWindows reports the target platform (the pi test branches on
// process.platform).
func isWindows() bool { return runtime.GOOS == "windows" }
