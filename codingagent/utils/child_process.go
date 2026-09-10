// pi: src/utils/child-process.ts — spawn wrappers and the
// wait-with-stdio-grace helper.
//
// Segment deviations:
//   - cross-spawn's Windows argv repair has no Go counterpart (os/exec
//     handles Windows process creation natively).
//   - pig's spawnProcess returns the cmd plus manually created stdout/stderr
//     pipe pairs (os/exec's StdoutPipe must not be read past Wait; the
//     caller-driven data callbacks and the grace-timer finalize need reads
//     that outlive Wait).
//   - waitForChildProcess is ported as SpawnAndAwaitChild: pi's caller
//     attaches data listeners before awaiting; pig's onData replaces them
//     and runs under the same grace/idle semantics (data after exit re-arms
//     the 100ms timer).
package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ExitStdioGraceMs is the post-exit idle window before finalizing (pi:
// EXIT_STDIO_GRACE_MS).
const ExitStdioGraceMs = 100 * time.Millisecond

// ChildPipes holds the manually created stdio pipe ends for one spawned
// child. The parent closes the write ends immediately after start and the
// read ends after the child settles.
type ChildPipes struct {
	StdoutRead  *os.File
	StdoutWrite *os.File
	StderrRead  *os.File
	StderrWrite *os.File
}

// CloseWriteEnds releases the parent's pipe write ends so readers observe
// EOF once the child (and any surviving descendants) close theirs (node's
// pipe stdio setup does the same).
func (p *ChildPipes) CloseWriteEnds() {
	if p.StdoutWrite != nil {
		p.StdoutWrite.Close()
	}
	if p.StderrWrite != nil {
		p.StderrWrite.Close()
	}
}

// CloseReadEnds releases the parent's pipe read ends after settling.
func (p *ChildPipes) CloseReadEnds() {
	if p.StdoutRead != nil {
		p.StdoutRead.Close()
	}
	if p.StderrRead != nil {
		p.StderrRead.Close()
	}
}

// SpawnProcess spawns a command with piped stdout/stderr (pi: spawnProcess
// with ['ignore', 'pipe', 'pipe'] stdio). stdin wiring is the caller's job
// (pi: commandFromStdin passes "pipe" for stdin; pig callers set cmd.Stdin).
func SpawnProcess(command string, args []string, cwd string, env []string) (*exec.Cmd, *ChildPipes, error) {
	pipe := func() (*os.File, *os.File, error) { return os.Pipe() }
	stdoutRead, stdoutWrite, err := pipe()
	if err != nil {
		return nil, nil, err
	}
	stderrRead, stderrWrite, err := pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, nil, err
	}
	cmd := exec.Command(command, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = env
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	cmd.SysProcAttr = spawnSysProcAttr()
	return cmd, &ChildPipes{
		StdoutRead:  stdoutRead,
		StdoutWrite: stdoutWrite,
		StderrRead:  stderrRead,
		StderrWrite: stderrWrite,
	}, nil
}

// childProcessWaiter reproduces pi's waitForChildProcess state machine: it
// resolves once the child has exited and both stdio pipes have ended, or
// after the grace period post-exit if descendants keep the pipes open. Data
// arriving after exit re-arms the idle timer so an actively writing
// descendant keeps us reading (pi#5303).
type childProcessWaiter struct {
	mu           sync.Mutex
	exited       bool
	stdoutEnded  bool
	stderrEnded  bool
	exitCode     int
	haveExitCode bool
	finalized    bool
	postExitCh   chan struct{}
}

func newChildProcessWaiter() *childProcessWaiter {
	return &childProcessWaiter{postExitCh: make(chan struct{})}
}

func (w *childProcessWaiter) onStdoutEnd() {
	w.mu.Lock()
	w.stdoutEnded = true
	w.maybeFinalizeLocked()
	w.mu.Unlock()
}

func (w *childProcessWaiter) onStderrEnd() {
	w.mu.Lock()
	w.stderrEnded = true
	w.maybeFinalizeLocked()
	w.mu.Unlock()
}

// onDataAfterExit re-arms the grace timer when output arrives after exit.
func (w *childProcessWaiter) onDataAfterExit() {
	w.mu.Lock()
	if w.exited && !w.finalized {
		w.armIdleTimerLocked()
	}
	w.mu.Unlock()
}

func (w *childProcessWaiter) onExit(code int, haveCode bool) {
	w.mu.Lock()
	w.exited = true
	w.exitCode = code
	w.haveExitCode = haveCode
	w.maybeFinalizeLocked()
	if !w.finalized {
		w.armIdleTimerLocked()
	}
	w.mu.Unlock()
}

func (w *childProcessWaiter) maybeFinalizeLocked() {
	if w.exited && w.stdoutEnded && w.stderrEnded {
		w.finalizeLocked()
	}
}

func (w *childProcessWaiter) armIdleTimerLocked() {
	timer := time.AfterFunc(ExitStdioGraceMs, func() {
		w.mu.Lock()
		w.finalizeLocked()
		w.mu.Unlock()
	})
	// The timer goroutine owns finalization; nothing else to store.
	_ = timer
}

func (w *childProcessWaiter) finalizeLocked() {
	if w.finalized {
		return
	}
	w.finalized = true
	close(w.postExitCh)
}

// Settle waits for finalization and returns pi's close semantics: the exit
// code, or null (haveCode false) when the child was killed by a signal.
func (w *childProcessWaiter) Settle() (code int, haveCode bool) {
	<-w.postExitCh
	return w.exitCode, w.haveExitCode
}

// StartChild starts the spawned child and releases the parent's write ends.
// Returns the waiter used by AwaitChild. (pig addition: split from
// AwaitChild so callers can register timers/watchers against the live pid,
// matching pi's spawn → track → listen ordering.)
func StartChild(cmd *exec.Cmd, pipes *ChildPipes) (*childProcessWaiter, error) {
	if err := cmd.Start(); err != nil {
		pipes.CloseWriteEnds()
		pipes.CloseReadEnds()
		return nil, err
	}
	pipes.CloseWriteEnds()
	return newChildProcessWaiter(), nil
}

// AwaitChild waits for the spawned child per waitForChildProcess semantics,
// forwarding stdout/stderr chunks (with UTF-8 chunk decoding like node's
// setEncoding) to onData as they arrive. A read error on either pipe ends
// that stream; a spawn-side error surfaces through the returned error only
// when Wait itself reports a start failure handled by the caller.
func AwaitChild(cmd *exec.Cmd, pipes *ChildPipes, ctx context.Context, onData func(chunk []byte)) error {
	waiter, err := StartChild(cmd, pipes)
	if err != nil {
		return err
	}
	return AwaitChildWaiter(cmd, pipes, ctx, waiter, onData)
}

// AwaitChildWaiter is AwaitChild with a waiter created by StartChild.
func AwaitChildWaiter(cmd *exec.Cmd, pipes *ChildPipes, ctx context.Context, waiter *childProcessWaiter, onData func(chunk []byte)) error {
	readPipe := func(read *os.File, ended func()) {
		buf := make([]byte, 64*1024)
		for {
			n, readErr := read.Read(buf)
			if n > 0 && onData != nil {
				// pi passes raw Buffers; the text decoding happens in the
				// OutputAccumulator's streaming decoder.
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				onData(chunk)
			}
			if n > 0 {
				waiter.onDataAfterExit()
			}
			if readErr != nil {
				break
			}
		}
		ended()
	}
	go readPipe(pipes.StdoutRead, waiter.onStdoutEnd)
	go readPipe(pipes.StderrRead, waiter.onStderrEnd)

	go func() {
		_ = cmd.Wait()
		code := 0
		haveCode := false
		if cmd.ProcessState != nil {
			if cmd.ProcessState.Exited() {
				code = cmd.ProcessState.ExitCode()
				haveCode = true
			}
			// Signal kill: pi's close code null.
		}
		waiter.onExit(code, haveCode)
	}()

	// pi's waitForChildProcess has no external cancellation; ctx is honored
	// opportunistically when the caller aborts via process kill.
	select {
	case <-waiter.postExitCh:
	case <-ctx.Done():
		// The caller is expected to kill the tree; wait for the pipes to
		// settle after the kill lands (bounded by the grace timer).
		<-waiter.postExitCh
	}
	pipes.CloseReadEnds()
	return nil
}

// FormatSpawnFailure renders a spawnSync failure (pi: formatSpawnFailure).
func FormatSpawnFailure(err error, stderr string, stdout string, exitCode int) string {
	if err != nil {
		return err.Error()
	}
	if trimmed := strings.TrimSpace(stderr); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(stdout); trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("exit status %d", exitCode)
}
