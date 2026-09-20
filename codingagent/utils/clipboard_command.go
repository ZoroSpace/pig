// clipboard_command.go (pi: src/utils/clipboard-command.ts, new in v0.86.0)
// — the shared clipboard subprocess runner.
//
// Segment deviations:
//   - pi returns Promise<Buffer | undefined>; pig returns ([]byte, bool)
//     with ok=false mapping pi's undefined and ok=true mapping a Buffer
//     (possibly empty).
//   - pi's "close" event fires when stdio settles; cmd.Run() waits for the
//     process exit, which is the same observable for clipboard tools (the
//     daemonizing-writer concern is handled by discarding stdout on the
//     write path, matching pi's stdio ["pipe", "ignore", "ignore"]).
//   - overflow aborts with SIGKILL in both (exec.CommandContext kills on
//     deadline; the oversized write itself errors out the capture).
package utils

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// clipboardCommandOptions parameterize runClipboardCommand (pi: the options
// param).
type clipboardCommandOptions struct {
	// Input feeds the child's stdin. When set the child's stdout is
	// discarded (writers do not need it, and daemonizing writers must not
	// retain the pipe).
	Input string
	// HasInput distinguishes pi's `input === undefined` from an empty
	// string input.
	HasInput bool
	// TimeoutMs bounds the whole command; 0 = 3000.
	TimeoutMs int64
	// MaxBufferBytes caps the captured stdout; 0 = 50 MiB.
	MaxBufferBytes int
}

// errClipboardBufferOverflow aborts the capture when stdout exceeds the
// buffer budget (pi: length > maxBufferBytes → abort()).
var errClipboardBufferOverflow = errors.New("clipboard output exceeds buffer limit")

// limitedBuffer is a bytes.Buffer that fails once over budget.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, errClipboardBufferOverflow
	}
	return l.buf.Write(p)
}

// runClipboardCommand runs one clipboard command (pi: runClipboardCommand).
// ok=false means the command failed (pi: undefined); ok=true carries the
// captured stdout, possibly empty (an empty buffer is a successful result).
func runClipboardCommand(command string, args []string, options *clipboardCommandOptions) ([]byte, bool) {
	timeout := time.Duration(3000) * time.Millisecond
	maxBuffer := 50 * 1024 * 1024
	if options != nil {
		if options.TimeoutMs > 0 {
			timeout = time.Duration(options.TimeoutMs) * time.Millisecond
		}
		if options.MaxBufferBytes > 0 {
			maxBuffer = options.MaxBufferBytes
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Stderr = nil

	var stdout limitedBuffer
	stdout.max = maxBuffer
	if options != nil && options.HasInput {
		// pi: stdio ["pipe", "ignore", "ignore"] — a writer's stdout is
		// ignored so daemonizing writers cannot retain the pipe.
		cmd.Stdin = strings.NewReader(options.Input)
		cmd.Stdout = nil
	} else {
		cmd.Stdout = &stdout
	}

	if err := cmd.Run(); err != nil {
		// pi: "error" events and non-zero close codes both resolve
		// undefined; a writer exiting before consuming all input is
		// tolerated on the write path (stdin errors are swallowed).
		return nil, false
	}
	return stdout.buf.Bytes(), true
}
