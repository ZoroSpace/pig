// pi: src/harness/env/nodejs.ts — os-backed ExecutionEnv (FileSystem +
// Shell).
//
// Segment deviations:
//   - Result<T, FileError> → (T, error) with *FileError; node errno codes →
//     FileErrorCode mapping in toFileError (ENOENT/EACCES/EPERM/ENOTDIR/
//     EISDIR/EINVAL/ABORT_ERR), everything else "unknown".
//   - abortSignal parameters → context.Context; the pre-read abortResult
//     checks collapse to a ctx.Err() probe at method entry (and per line in
//     ReadTextLines), returning the "aborted" FileError. exec's abortSignal
//     becomes the leading ctx: a Done() watcher kills the process tree,
//     matching pi's onAbort listener.
//   - TS path.resolve/join/basename → path/filepath; resolvePath keeps the
//     ~ and file:// expansions. fileURLToPath is approximated with net/url
//     (host ignored, %XX decoding via url.PathUnescape); malformed URLs stay
//     ordinary paths, preserving the non-throwing contract.
//   - readTextLines streams via bufio.Scanner with a raised max token size
//     (pi relies on readline's crlfDelay: Infinity for CRLF; the scanner
//     splits on \n and keeps a trailing \r, matching TS line contents for
//     LF files — CRLF files keep \r on each line, which the JSONL consumers
//     never produce).
//   - exec: node's spawn(streams, detached, windowsHide) → os/exec with a
//     manually created stdout/stderr pipe pair (os/exec's StdoutPipe must
//     not be read past Wait; pi's waitForChildProcess keeps reading through
//     the 100ms post-exit grace window). setEncoding('utf8') chunk decoding
//     is reproduced by an incremental UTF-8 chunk decoder so multi-byte
//     sequences split across reads still arrive as single chunks.
//   - Result<{stdout, stderr, exitCode}, ExecutionError> →
//     (*harness.ShellExecResult, error) with *ExecutionError; node's close
//     code null (signal kill) maps to exitCode 0 (pi: `code ?? 0`).
package env

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/samber/lo"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"os/exec"
	ospath "path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ZoroSpace/pig/agent/harness"
)

// NodeExecutionEnvOptions parameterizes NodeExecutionEnv (pi: the
// constructor options object).
type NodeExecutionEnvOptions struct {
	// Cwd is the working directory for relative paths.
	Cwd string
	// ShellPath overrides the shell used by Exec; empty auto-detects.
	ShellPath string
	// ShellEnv holds extra default variables merged over the process
	// environment when Exec inherits it (pi: shellEnv,
	// NodeJS.ProcessEnv).
	ShellEnv map[string]string
}

// NodeExecutionEnv is the local-process execution environment (pi:
// NodeExecutionEnv class).
type NodeExecutionEnv struct {
	cwd string
	// shellPath overrides the shell; empty = auto-detect (pi: shellPath?).
	shellPath string
	// shellEnv holds extra default variables for inherited-env executions
	// (pi: shellEnv?).
	shellEnv map[string]string
	// activeChildPids tracks live shell processes for cleanup (pi:
	// activeChildPids).
	activeChildPidsMu sync.Mutex
	activeChildPids   map[int]struct{}
}

// NewNodeExecutionEnv builds the environment (pi: constructor
// { cwd, shellPath?, shellEnv? }).
func NewNodeExecutionEnv(options NodeExecutionEnvOptions) *NodeExecutionEnv {
	return &NodeExecutionEnv{
		cwd:             options.Cwd,
		shellPath:       options.ShellPath,
		shellEnv:        options.ShellEnv,
		activeChildPids: map[int]struct{}{},
	}
}

// Cwd implements FileSystem.
func (e *NodeExecutionEnv) Cwd() string { return e.cwd }

// resolvePath expands ~ and file:// URLs, then absolutizes against cwd
// (pi: resolvePath).
func resolvePath(cwd string, path string) string {
	normalized := path
	if normalized == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = home
		}
	} else if strings.HasPrefix(normalized, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			normalized = ospath.Join(home, normalized[2:])
		}
	} else if strings.HasPrefix(normalized, "file://") {
		if decoded, ok := fileURLToPath(normalized); ok {
			normalized = decoded
		}
		// Keep malformed URLs as ordinary paths so filesystem methods
		// preserve their non-throwing contract.
	}
	if ospath.IsAbs(normalized) {
		return ospath.Clean(normalized)
	}
	return ospath.Clean(ospath.Join(cwd, normalized))
}

// fileURLToPath approximates node's fileURLToPath for POSIX paths.
func fileURLToPath(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	decoded, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return "", false
	}
	return decoded, true
}

// fileKindFromStats maps a dir-entry/lstat result to a FileKind, nil when
// unsupported (pi: fileKindFromStats).
func fileKindFromStats(mode fs.FileMode) harness.FileKind {
	switch {
	case mode&fs.ModeSymlink != 0:
		return harness.FileKindSymlink
	case mode.IsRegular():
		return harness.FileKindFile
	case mode.IsDir():
		return harness.FileKindDirectory
	default:
		return ""
	}
}

// fileInfoFromStats builds FileInfo from an lstat result (pi:
// fileInfoFromStats).
func fileInfoFromStats(path string, entry fs.FileInfo) (harness.FileInfo, error) {
	kind := fileKindFromStats(entry.Mode())
	if kind == "" {
		return harness.FileInfo{}, harness.NewFileError(harness.FileCodeInvalid, "Unsupported file type", path, nil)
	}
	return harness.FileInfo{
		Name:    ospath.Base(path),
		Path:    path,
		Kind:    kind,
		Size:    entry.Size(),
		MtimeMs: entry.ModTime().UnixMilli(),
	}, nil
}

// toFileError maps an os error to a FileError (pi: toFileError).
func toFileError(err error, fallbackPath string) *harness.FileError {
	var fileErr *harness.FileError
	if errors.As(err, &fileErr) {
		return fileErr
	}
	path := fallbackPath
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return harness.NewFileError(harness.FileCodeNotFound, err.Error(), path, err)
	case errors.Is(err, fs.ErrPermission):
		return harness.NewFileError(harness.FileCodePermission, err.Error(), path, err)
	case errors.Is(err, syscall.ENOTDIR):
		return harness.NewFileError(harness.FileCodeNotDirectory, err.Error(), path, err)
	case errors.Is(err, syscall.EISDIR):
		return harness.NewFileError(harness.FileCodeIsDirectory, err.Error(), path, err)
	case errors.Is(err, syscall.EINVAL):
		return harness.NewFileError(harness.FileCodeInvalid, err.Error(), path, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return harness.NewFileError(harness.FileCodeAborted, "aborted", path, err)
	default:
		var errno syscall.Errno
		if errors.As(err, &errno) {
			switch errno {
			case syscall.EACCES, syscall.EPERM:
				return harness.NewFileError(harness.FileCodePermission, err.Error(), path, err)
			case syscall.ENOTDIR:
				return harness.NewFileError(harness.FileCodeNotDirectory, err.Error(), path, err)
			case syscall.EISDIR:
				return harness.NewFileError(harness.FileCodeIsDirectory, err.Error(), path, err)
			case syscall.EINVAL:
				return harness.NewFileError(harness.FileCodeInvalid, err.Error(), path, err)
			}
		}
		return harness.NewFileError(harness.FileCodeUnknown, err.Error(), path, err)
	}
}

// abortErr is pi's abortResult check: a non-cancelled ctx yields nil.
func abortErr(ctx context.Context, path string) *harness.FileError {
	if err := ctx.Err(); err != nil {
		return harness.NewFileError(harness.FileCodeAborted, "aborted", path, err)
	}
	return nil
}

func (e *NodeExecutionEnv) AbsolutePath(ctx context.Context, path string) (string, error) {
	return resolvePath(e.cwd, path), nil
}

func (e *NodeExecutionEnv) JoinPath(ctx context.Context, parts []string) (string, error) {
	return ospath.Join(parts...), nil
}

// resolveTimeoutMs validates the timeout in seconds and converts it to
// milliseconds (pi: resolveTimeoutMs).
func resolveTimeoutMs(timeout *float64) (float64, error) {
	if timeout == nil {
		return 0, nil
	}
	if !isFinite(*timeout) || *timeout <= 0 {
		return 0, harness.NewExecutionError(harness.ExecCodeTimeout, "Invalid timeout: must be a finite number of seconds", nil)
	}
	timeoutMs := *timeout * 1000
	if timeoutMs > maxTimeoutMs {
		return 0, harness.NewExecutionError(harness.ExecCodeTimeout,
			fmt.Sprintf("Invalid timeout: maximum is %s seconds", formatJSNumber(maxTimeoutSeconds)), nil)
	}
	return timeoutMs, nil
}

// maxTimeoutMs and maxTimeoutSeconds mirror pi's constants; the seconds
// value formats as "2147483.647" (pi: MAX_TIMEOUT_MS / 1000).
const (
	maxTimeoutMs      = 2147483647.0
	maxTimeoutSeconds = maxTimeoutMs / 1000
	exitStdioGraceMs  = 100
)

// isFinite reports whether f is finite (TS Number.isFinite).
func isFinite(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f)
}

// formatJSNumber formats a float the way JS String(number) does for the
// values that appear in error messages (shortest round-trip form).
func formatJSNumber(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return s
}

// pathExists reports whether the path is accessible (pi: pathExists).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runCommand spawns a command, captures stdout, and kills it after timeoutMs
// (pi: runCommand). Failures resolve as empty output with a nil status; a
// signal kill yields -1 (pi: null, indistinguishable from "not zero" at the
// only call site).
func runCommand(command string, args []string, timeoutMs float64) (string, *int) {
	cmd := exec.Command(command, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil
	}
	cmd.SysProcAttr = spawnSysProcAttr()
	if err := cmd.Start(); err != nil {
		return "", nil
	}
	timer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
		killProcessTree(cmd.Process.Pid)
	})
	var stdoutBuf strings.Builder
	buf := make([]byte, 64*1024)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			stdoutBuf.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	timer.Stop()
	waitErr := cmd.Wait()
	if waitErr != nil && cmd.ProcessState == nil {
		return stdoutBuf.String(), nil
	}
	status := cmd.ProcessState.ExitCode()
	return stdoutBuf.String(), &status
}

// findBashOnPath locates bash via which/where (pi: findBashOnPath). Returns
// "" when not found.
func findBashOnPath() string {
	var stdout string
	var status *int
	if runtime.GOOS == "windows" {
		stdout, status = runCommand("where", []string{"bash.exe"}, 5000)
	} else {
		stdout, status = runCommand("which", []string{"bash"}, 5000)
	}
	if status == nil || *status != 0 || stdout == "" {
		return ""
	}
	firstMatch := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n", 2)[0])
	if firstMatch != "" && pathExists(firstMatch) {
		return firstMatch
	}
	return ""
}

// ShellConfig selects the shell binary and argument transport (pi:
// ShellConfig).
type ShellConfig struct {
	Shell string
	Args  []string
	// CommandTransport is "" (argv) or "stdin" (pi: "argv" | "stdin" |
	// undefined; undefined collapses to "").
	CommandTransport string
}

// legacyWslBashPathRe matches Windows' legacy WSL bash paths (pi:
// isLegacyWslBashPath).
var legacyWslBashPathRe = regexp.MustCompile(`^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$`)

// isLegacyWslBashPath reports whether the path is a legacy WSL bash (pi:
// isLegacyWslBashPath).
func isLegacyWslBashPath(path string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(path, "/", "\\"))
	return legacyWslBashPathRe.MatchString(normalized)
}

// getBashShellConfig picks the command transport for a bash path (pi:
// getBashShellConfig).
func getBashShellConfig(shell string) ShellConfig {
	if isLegacyWslBashPath(shell) {
		return ShellConfig{Shell: shell, Args: []string{"-s"}, CommandTransport: "stdin"}
	}
	return ShellConfig{Shell: shell, Args: []string{"-c"}}
}

// getShellConfig resolves the shell for one execution (pi: getShellConfig).
// pi resolves the custom shell path against the process working directory
// (its test chdirs); pig resolves against the environment cwd so the spawn
// uses an absolute path (os/exec resolves relative command paths against the
// process cwd, not cmd.Dir).
func getShellConfig(cwd string, customShellPath string) (ShellConfig, error) {
	if customShellPath != "" {
		// pi makes the transport decision on the raw path and spawns it
		// relative to the process cwd; pig resolves for the spawn (os/exec
		// resolves relative command paths against the process cwd, not
		// cmd.Dir) but keeps the legacy-WSL check on the raw path.
		resolved := resolvePath(cwd, customShellPath)
		if pathExists(resolved) {
			config := getBashShellConfig(customShellPath)
			config.Shell = resolved
			return config, nil
		}
		return ShellConfig{}, harness.NewExecutionError(harness.ExecCodeShellUnavailable,
			fmt.Sprintf("Custom shell path not found: %s", customShellPath), nil)
	}
	if runtime.GOOS == "windows" {
		var candidates []string
		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates, ospath.Join(programFiles, "Git", "bin", "bash.exe"))
		}
		if programFilesX86 := os.Getenv("ProgramFiles(x86)"); programFilesX86 != "" {
			candidates = append(candidates, ospath.Join(programFilesX86, "Git", "bin", "bash.exe"))
		}
		for _, candidate := range candidates {
			if pathExists(candidate) {
				return getBashShellConfig(candidate), nil
			}
		}
		if bashOnPath := findBashOnPath(); bashOnPath != "" {
			return getBashShellConfig(bashOnPath), nil
		}
		detail := ""
		for _, path := range candidates {
			detail += fmt.Sprintf("  %s\n", path)
		}
		return ShellConfig{}, harness.NewExecutionError(harness.ExecCodeShellUnavailable,
			"No bash shell found. Options:\n"+
				"  1. Install Git for Windows: https://git-scm.com/download/win\n"+
				"  2. Add your bash to PATH (Cygwin, MSYS2, etc.)\n"+
				"  3. Configure an explicit shellPath\n\n"+
				fmt.Sprintf("Searched Git Bash in:\n%s", detail), nil)
	}
	if pathExists("/bin/bash") {
		return getBashShellConfig("/bin/bash"), nil
	}
	if bashOnPath := findBashOnPath(); bashOnPath != "" {
		return getBashShellConfig(bashOnPath), nil
	}
	return ShellConfig{Shell: "sh", Args: []string{"-c"}}, nil
}

// getShellEnv builds the child environment (pi: getShellEnv). Without
// inheritance only the extra map is used; with inheritance the process
// environment is the base, overlaid by shellEnv then extraEnv.
func getShellEnv(shellEnv map[string]string, extraEnv map[string]string, inheritEnv bool) []string {
	if !inheritEnv {
		env := make([]string, 0, len(extraEnv))
		for key, value := range extraEnv {
			env = append(env, key+"="+value)
		}
		return env
	}
	merged := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			merged[key] = value
		}
	}
	for key, value := range shellEnv {
		merged[key] = value
	}
	for key, value := range extraEnv {
		merged[key] = value
	}
	env := make([]string, 0, len(merged))
	for key, value := range merged {
		env = append(env, key+"="+value)
	}
	return env
}

// killProcessTree force-kills a process and its group (pi:
// killProcessTree).
func killProcessTree(pid int) {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
		cmd.SysProcAttr = spawnSysProcAttr()
		_ = cmd.Start() // pi ignores errors.
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// spawnSysProcAttr is defined per-platform in spawn_unix.go / spawn_windows.go
// (the syscall.SysProcAttr fields differ across platforms).

// processWaiter reproduces pi's waitForChildProcess: it resolves once the
// child has exited and both stdio pipes have ended, or after a 100ms grace
// period post-exit if grandchildren keep the pipes open.
type processWaiter struct {
	mu            sync.Mutex
	exited        bool
	stdoutEnded   bool
	stderrEnded   bool
	exitCode      int
	finalized     bool
	postExitTimer *time.Timer
	done          chan struct{}
}

func newProcessWaiter() *processWaiter {
	return &processWaiter{done: make(chan struct{})}
}

func (w *processWaiter) onStdoutEnd() {
	w.mu.Lock()
	w.stdoutEnded = true
	w.maybeFinalizeLocked()
	w.mu.Unlock()
}

func (w *processWaiter) onStderrEnd() {
	w.mu.Lock()
	w.stderrEnded = true
	w.maybeFinalizeLocked()
	w.mu.Unlock()
}

// onDataAfterExit re-arms the grace timer when output arrives after exit
// (pi: onData → armIdleTimer).
func (w *processWaiter) onDataAfterExit() {
	w.mu.Lock()
	if w.exited && !w.finalized {
		w.armIdleTimerLocked()
	}
	w.mu.Unlock()
}

func (w *processWaiter) onExit(code int) {
	w.mu.Lock()
	w.exited = true
	w.exitCode = code
	w.maybeFinalizeLocked()
	if !w.finalized {
		w.armIdleTimerLocked()
	}
	w.mu.Unlock()
}

func (w *processWaiter) maybeFinalizeLocked() {
	if w.exited && w.stdoutEnded && w.stderrEnded {
		w.finalizeLocked()
	}
}

func (w *processWaiter) armIdleTimerLocked() {
	if w.postExitTimer != nil {
		w.postExitTimer.Stop()
	}
	w.postExitTimer = time.AfterFunc(exitStdioGraceMs*time.Millisecond, func() {
		w.mu.Lock()
		w.finalizeLocked()
		w.mu.Unlock()
	})
}

func (w *processWaiter) finalizeLocked() {
	if w.finalized {
		return
	}
	w.finalized = true
	if w.postExitTimer != nil {
		w.postExitTimer.Stop()
	}
	close(w.done)
}

func (w *processWaiter) wait() {
	<-w.done
}

// utf8ChunkDecoder decodes a byte stream into UTF-8 string chunks the way
// node's setEncoding('utf8') does: bytes split across reads are buffered
// until the sequence completes. Invalid bytes pass through unchanged (node
// would produce U+FFFD; downstream consumers treat both as opaque text).
type utf8ChunkDecoder struct {
	pending []byte
}

func (d *utf8ChunkDecoder) chunk(data []byte) string {
	if len(d.pending) > 0 {
		d.pending = append(d.pending, data...)
		data = d.pending
	}
	if utf8.Valid(data) {
		d.pending = d.pending[:0]
		return string(data)
	}
	// Longest valid prefix.
	end := len(data)
	for end > 0 && !utf8.Valid(data[:end]) {
		end--
	}
	// Keep any trailing incomplete sequence buffered.
	keep := end
	for keep < len(data) {
		r, size := utf8.DecodeRune(data[keep:])
		if r == utf8.RuneError && size <= 1 {
			keep = len(data) // invalid byte: emit immediately
			break
		}
		if keep+size > len(data) {
			break // incomplete sequence: buffer it
		}
		keep += size
	}
	chunkText := string(data[:keep])
	d.pending = append(d.pending[:0], data[keep:]...)
	return chunkText
}

// Exec implements Shell (pi: NodeExecutionEnv.exec). Failures return a
// *harness.ExecutionError as the concrete error (pi: Result collapse); ctx
// carries the caller's cancellation (pi: options.abortSignal).
func (e *NodeExecutionEnv) Exec(ctx context.Context, command string, options *harness.ShellExecOptions) (*harness.ShellExecResult, error) {
	if ctx.Err() != nil {
		return nil, harness.NewExecutionError(harness.ExecCodeAborted, "aborted", nil)
	}
	var timeoutRef *float64
	if options != nil {
		timeoutRef = options.Timeout
	}
	timeoutMs, err := resolveTimeoutMs(timeoutRef)
	if err != nil {
		return nil, err
	}

	cwd := e.cwd
	if options != nil && options.Cwd != "" {
		cwd = resolvePath(e.cwd, options.Cwd)
	}
	shellConfig, err := getShellConfig(e.cwd, e.shellPath)
	if err != nil {
		return nil, err
	}
	if !pathExists(cwd) {
		return nil, harness.NewExecutionError(harness.ExecCodeSpawnError,
			fmt.Sprintf("Working directory does not exist: %s\nCannot execute bash commands.", cwd), nil)
	}

	inheritEnv := true
	var extraEnv map[string]string
	var onStdout, onStderr func(string)
	if options != nil {
		if options.InheritEnv != nil {
			inheritEnv = *options.InheritEnv
		}
		extraEnv = options.Env
		onStdout = options.OnStdout
		onStderr = options.OnStderr
	}

	stdoutAcc, stderrAcc := &strings.Builder{}, &strings.Builder{}
	var stateMu sync.Mutex
	var callbackError *harness.ExecutionError

	// commandFromStdin / args selection (pi: spawn's argv building).
	commandFromStdin := shellConfig.CommandTransport == "stdin"
	args := shellConfig.Args
	if !commandFromStdin {
		args = append(append([]string{}, shellConfig.Args...), command)
	}
	cmd := exec.Command(shellConfig.Shell, args...)
	cmd.Dir = cwd
	cmd.Env = getShellEnv(e.shellEnv, extraEnv, inheritEnv)
	cmd.SysProcAttr = spawnSysProcAttr()

	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, harness.NewExecutionError(harness.ExecCodeSpawnError, err.Error(), err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, harness.NewExecutionError(harness.ExecCodeSpawnError, err.Error(), err)
	}
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	if commandFromStdin {
		stdinRead, stdinWrite, pipeErr := os.Pipe()
		if pipeErr != nil {
			stdoutRead.Close()
			stdoutWrite.Close()
			stderrRead.Close()
			stderrWrite.Close()
			return nil, harness.NewExecutionError(harness.ExecCodeSpawnError, pipeErr.Error(), pipeErr)
		}
		cmd.Stdin = stdinRead
		go func() {
			// pi: child.stdin?.on("error", () => {}) — write errors ignored.
			_, _ = io.WriteString(stdinWrite, command)
			stdinWrite.Close()
		}()
	}

	if err := cmd.Start(); err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		stderrRead.Close()
		stderrWrite.Close()
		return nil, harness.NewExecutionError(harness.ExecCodeSpawnError, err.Error(), err)
	}
	// Release the parent's write ends so readers see EOF when the child (and
	// any surviving grandchildren) close theirs — node's pipe setup does the
	// same.
	stdoutWrite.Close()
	stderrWrite.Close()
	e.setActivePID(cmd.Process.Pid)

	var timedOut atomic.Bool
	stopTimeout := func() {}
	if timeoutMs != 0 {
		timer := time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() {
			timedOut.Store(true)
			killProcessTree(cmd.Process.Pid)
		})
		stopTimeout = func() { timer.Stop() }
	}

	// pi: options.abortSignal listener → ctx watcher kills the tree.
	abortDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessTree(cmd.Process.Pid)
		case <-abortDone:
		}
	}()

	waiter := newProcessWaiter()
	stdoutDecoder := &utf8ChunkDecoder{}
	stderrDecoder := &utf8ChunkDecoder{}
	var activePID atomic.Int64
	activePID.Store(int64(cmd.Process.Pid))
	readPipe := func(read *os.File, acc *strings.Builder, cb func(string), decoder *utf8ChunkDecoder, ended func()) {
		buf := make([]byte, 64*1024)
		for {
			n, readErr := read.Read(buf)
			if n > 0 {
				chunkText := decoder.chunk(buf[:n])
				if chunkText != "" {
					stateMu.Lock()
					acc.WriteString(chunkText)
					stateMu.Unlock()
					if cb != nil {
						func() {
							defer func() {
								if r := recover(); r != nil {
									cause := fmt.Errorf("%v", r)
									stateMu.Lock()
									if callbackError == nil {
										callbackError = harness.NewExecutionError(harness.ExecCodeCallbackError, cause.Error(), cause)
									}
									stateMu.Unlock()
									if pid := activePID.Load(); pid != 0 {
										killProcessTree(int(pid))
									}
								}
							}()
							cb(chunkText)
						}()
					}
					waiter.onDataAfterExit()
				}
			}
			if readErr != nil {
				break
			}
		}
		ended()
	}
	go readPipe(stdoutRead, stdoutAcc, onStdout, stdoutDecoder, waiter.onStdoutEnd)
	go readPipe(stderrRead, stderrAcc, onStderr, stderrDecoder, waiter.onStderrEnd)

	go func() {
		_ = cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			if cmd.ProcessState.Exited() {
				code = cmd.ProcessState.ExitCode()
			}
			// Signal kill leaves code 0: pi's close code null → `code ?? 0`.
		}
		waiter.onExit(code)
	}()

	waiter.wait()
	stopTimeout()
	close(abortDone)
	e.clearActivePID(cmd.Process.Pid)
	stdoutRead.Close()
	stderrRead.Close()

	stateMu.Lock()
	savedCallbackError := callbackError
	stateMu.Unlock()
	if savedCallbackError != nil {
		return nil, savedCallbackError
	}
	if timedOut.Load() {
		timeoutSeconds := 0.0
		if options != nil && options.Timeout != nil {
			timeoutSeconds = *options.Timeout
		}
		return nil, harness.NewExecutionError(harness.ExecCodeTimeout,
			fmt.Sprintf("timeout:%s", formatJSNumber(timeoutSeconds)), nil)
	}
	if ctx.Err() != nil {
		return nil, harness.NewExecutionError(harness.ExecCodeAborted, "aborted", nil)
	}
	return &harness.ShellExecResult{
		Stdout:   stdoutAcc.String(),
		Stderr:   stderrAcc.String(),
		ExitCode: waiter.exitCode,
	}, nil
}

func (e *NodeExecutionEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	resolved := resolvePath(e.cwd, path)
	if abort := abortErr(ctx, resolved); abort != nil {
		return "", abort
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", toFileError(err, resolved)
	}
	if !utf8.Valid(content) {
		// node decodes as utf8 (replacement chars for invalid bytes);
		// match that leniency instead of failing.
		return string(content), nil
	}
	return string(content), nil
}

func (e *NodeExecutionEnv) ReadTextLines(ctx context.Context, path string, maxLines *int) ([]string, error) {
	resolved := resolvePath(e.cwd, path)
	if abort := abortErr(ctx, resolved); abort != nil {
		return nil, abort
	}
	if maxLines != nil && *maxLines <= 0 {
		return []string{}, nil
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	defer file.Close()
	lines := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if abort := abortErr(ctx, resolved); abort != nil {
			return nil, abort
		}
		lines = append(lines, scanner.Text())
		if maxLines != nil && len(lines) >= *maxLines {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, toFileError(err, resolved)
	}
	return lines, nil
}

func (e *NodeExecutionEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	resolved := resolvePath(e.cwd, path)
	if abort := abortErr(ctx, resolved); abort != nil {
		return nil, abort
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	return content, nil
}

func (e *NodeExecutionEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	resolved := resolvePath(e.cwd, path)
	if abort := abortErr(ctx, resolved); abort != nil {
		return abort
	}
	if err := os.MkdirAll(ospath.Dir(resolved), 0o755); err != nil {
		return toFileError(err, resolved)
	}
	if abort := abortErr(ctx, resolved); abort != nil {
		return abort
	}
	if err := os.WriteFile(resolved, content, 0o644); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *NodeExecutionEnv) AppendFile(ctx context.Context, path string, content []byte) error {
	resolved := resolvePath(e.cwd, path)
	if err := os.MkdirAll(ospath.Dir(resolved), 0o755); err != nil {
		return toFileError(err, resolved)
	}
	file, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return toFileError(err, resolved)
	}
	defer file.Close()
	if _, err := file.Write(content); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *NodeExecutionEnv) RenameFile(ctx context.Context, sourcePath string, destinationPath string) error {
	source := resolvePath(e.cwd, sourcePath)
	destination := resolvePath(e.cwd, destinationPath)
	if abort := abortErr(ctx, destination); abort != nil {
		return abort
	}
	if err := os.Rename(source, destination); err != nil {
		return toFileError(err, source)
	}
	return nil
}

func (e *NodeExecutionEnv) FileInfo(ctx context.Context, path string) (harness.FileInfo, error) {
	resolved := resolvePath(e.cwd, path)
	entry, err := os.Lstat(resolved)
	if err != nil {
		return harness.FileInfo{}, toFileError(err, resolved)
	}
	info, err := fileInfoFromStats(resolved, entry)
	if err != nil {
		return harness.FileInfo{}, toFileError(err, resolved)
	}
	return info, nil
}

func (e *NodeExecutionEnv) ListDir(ctx context.Context, path string) ([]harness.FileInfo, error) {
	resolved := resolvePath(e.cwd, path)
	if abort := abortErr(ctx, resolved); abort != nil {
		return nil, abort
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	infos := []harness.FileInfo{}
	for _, entry := range entries {
		if abort := abortErr(ctx, resolved); abort != nil {
			return nil, abort
		}
		entryPath := ospath.Join(resolved, entry.Name())
		entryInfo, err := entry.Info()
		if err != nil {
			return nil, toFileError(err, entryPath)
		}
		info, err := fileInfoFromStats(entryPath, entryInfo)
		if err != nil {
			continue // pi skips unsupported kinds (info.ok false)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (e *NodeExecutionEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	resolved := resolvePath(e.cwd, path)
	resolvedSymlinks, err := ospath.EvalSymlinks(resolved)
	if err != nil {
		return "", toFileError(err, resolved)
	}
	return resolvedSymlinks, nil
}

func (e *NodeExecutionEnv) Exists(ctx context.Context, path string) (bool, error) {
	_, err := e.FileInfo(ctx, path)
	if err == nil {
		return true, nil
	}
	var fileErr *harness.FileError
	if errors.As(err, &fileErr) {
		if fileErr.Code == harness.FileCodeNotFound {
			return false, nil
		}
	}
	return false, err
}

func (e *NodeExecutionEnv) CreateDir(ctx context.Context, path string, recursive *bool) error {
	resolved := resolvePath(e.cwd, path)
	wantRecursive := true
	if recursive != nil {
		wantRecursive = *recursive
	}
	if wantRecursive {
		if err := os.MkdirAll(resolved, 0o755); err != nil {
			return toFileError(err, resolved)
		}
		return nil
	}
	if err := os.Mkdir(resolved, 0o755); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *NodeExecutionEnv) Remove(ctx context.Context, path string, recursive *bool, force *bool) error {
	resolved := resolvePath(e.cwd, path)
	wantRecursive := false
	if recursive != nil {
		wantRecursive = *recursive
	}
	wantForce := false
	if force != nil {
		wantForce = *force
	}
	if wantForce {
		if err := os.RemoveAll(resolved); err != nil {
			return toFileError(err, resolved)
		}
		return nil
	}
	err := os.Remove(resolved)
	if err != nil && wantRecursive && errors.Is(err, fs.ErrNotExist) {
		// node's rm with recursive:true succeeds when the path is already
		// gone even with force:false.
		return nil
	}
	if err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

func (e *NodeExecutionEnv) CreateTempDir(ctx context.Context, prefix *string) (string, error) {
	wantPrefix := "tmp-"
	if prefix != nil {
		wantPrefix = *prefix
	}
	dir, err := os.MkdirTemp("", wantPrefix)
	if err != nil {
		return "", toFileError(err, "")
	}
	return dir, nil
}

func (e *NodeExecutionEnv) CreateTempFile(ctx context.Context, prefix *string, suffix *string) (string, error) {
	wantPrefix := ""
	if prefix != nil {
		wantPrefix = *prefix
	}
	wantSuffix := ""
	if suffix != nil {
		wantSuffix = *suffix
	}
	dir, err := e.CreateTempDir(ctx, lo.ToPtr("tmp-"))
	if err != nil {
		return "", err
	}
	filePath := ospath.Join(dir, fmt.Sprintf("%s%s%s", wantPrefix, uuidString(), wantSuffix))
	if err := os.WriteFile(filePath, []byte{}, 0o644); err != nil {
		return "", toFileError(err, filePath)
	}
	return filePath, nil
}

// Cleanup implements ExecutionEnv (pi: cleanup): kill all active shell
// processes, then clear the set. Best-effort and never fails session flow.
func (e *NodeExecutionEnv) Cleanup() error {
	e.activeChildPidsMu.Lock()
	pids := make([]int, 0, len(e.activeChildPids))
	for pid := range e.activeChildPids {
		pids = append(pids, pid)
	}
	e.activeChildPids = map[int]struct{}{}
	e.activeChildPidsMu.Unlock()
	for _, pid := range pids {
		killProcessTree(pid)
	}
	return nil
}

// setActivePID records a live child process (pi: activeChildPids.add).
func (e *NodeExecutionEnv) setActivePID(pid int) {
	e.activeChildPidsMu.Lock()
	e.activeChildPids[pid] = struct{}{}
	e.activeChildPidsMu.Unlock()
}

// clearActivePID drops a settled child process (pi: activeChildPids.delete).
func (e *NodeExecutionEnv) clearActivePID(pid int) {
	e.activeChildPidsMu.Lock()
	delete(e.activeChildPids, pid)
	e.activeChildPidsMu.Unlock()
}

// uuidString produces a random UUID-shaped string for temp file names.
func uuidString() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
