// pi: src/harness/utils/shell-output.ts — shell execution with bounded
// tail capture and full-output spilling.
//
// Deviations:
//   - TS serializes the async appendFile calls through a promise chain
//     (writeChain); pig's FileSystem.AppendFile is synchronous, so the chain
//     collapses to sequential inline writes with the same ordering and
//     error-stop semantics (first write failure stops later ones and is
//     surfaced after exec completes).
//   - TS strings are UTF-16; Go strings are UTF-8. sanitizeBinaryOutput
//     iterates runes (pi iterates code points); trimToLastUtf8Bytes slices
//     at a UTF-8 continuation boundary (pi re-decodes with replacement).
//   - pi's options.abortSignal and exec's abortSignal are carried by the
//     leading context.Context; callback exceptions (TS try/catch) are
//     recovered as callback_error ExecutionErrors.
package utils

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent/harness"
)

// ShellCaptureProgress is the streaming capture snapshot handed to
// ShellCaptureOptions.OnChunk (pi: ShellCaptureProgress).
type ShellCaptureProgress struct {
	// Output is the tail output, truncated when the whole capture exceeded
	// the limits.
	Output string
	// Truncation describes the truncation state of the capture.
	Truncation TruncationResult
	// FullOutputPath points at the spilled full-output file, empty when not
	// requested.
	FullOutputPath string
	// LastLineBytes is the byte length of the currently open line.
	LastLineBytes int
}

// ShellCaptureOptions parameterizes ExecuteShellWithCapture (pi:
// ShellCaptureOptions; the ShellExecOptions onStdout/onStderr members are
// replaced by the single OnChunk here, mirroring pi's Omit).
type ShellCaptureOptions struct {
	// Cwd is the working directory for the command (pi: ShellExecOptions.cwd).
	Cwd string
	// Env holds environment variables for the command (pi: env).
	Env map[string]string
	// InheritEnv controls whether default environment variables are
	// inherited; nil = pi's true default.
	InheritEnv *bool
	// Timeout is the command timeout in seconds; nil = no timeout (pi:
	// timeout).
	Timeout *float64
	// OnChunk is called with each sanitized output chunk and a lazy progress
	// getter (pi: onChunk(chunk, getProgress)).
	OnChunk func(chunk string, getProgress func() ShellCaptureProgress)
	// ReturnExecutionErrors returns shell execution failures with captured
	// output instead of as a failed result (pi: returnExecutionErrors).
	ReturnExecutionErrors bool
	// pi's abortSignal is intentionally absent: cancellation travels as the
	// context.Context passed to ExecuteShellWithCapture.
}

// ShellCaptureResult is the final capture outcome (pi: ShellCaptureResult;
// extends ShellCaptureProgress via embedding).
type ShellCaptureResult struct {
	ShellCaptureProgress
	// ExitCode is the command's exit code; nil when cancelled or when the
	// command failed to run.
	ExitCode *int
	// Cancelled reports whether the execution was cancelled.
	Cancelled bool
	// Truncated reports whether the captured output was truncated.
	Truncated bool
	// ExecutionError carries the failure when ReturnExecutionErrors is set
	// and the command failed to run.
	ExecutionError *harness.ExecutionError
}

// toExecutionError normalizes an arbitrary error into an ExecutionError
// (pi: toExecutionError).
func toExecutionError(err error) *harness.ExecutionError {
	if execErr, ok := err.(*harness.ExecutionError); ok {
		return execErr
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	return harness.NewExecutionError(harness.ExecCodeUnknown, message, err)
}

// SanitizeBinaryOutput strips control characters that would corrupt the
// captured text, keeping tab/newline/carriage-return (pi:
// sanitizeBinaryOutput). In TS this also drops UTF-16 line/paragraph
// separators (0xfff9-0xfffb are interlinear annotation anchors); the same
// rune range is dropped here.
func SanitizeBinaryOutput(str string) string {
	var b strings.Builder
	b.Grow(len(str))
	for _, char := range str {
		code := char
		if code == 0x09 || code == 0x0a || code == 0x0d {
			b.WriteRune(char)
			continue
		}
		if code <= 0x1f {
			continue
		}
		if code >= 0xfff9 && code <= 0xfffb {
			continue
		}
		b.WriteRune(char)
	}
	return b.String()
}

// trimToLastUtf8Bytes trims text to its last maxBytes UTF-8 bytes without
// splitting a character (pi: trimToLastUtf8Bytes).
func trimToLastUtf8Bytes(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	for start < len(text) && text[start]&0xc0 == 0x80 {
		start++
	}
	return text[start:]
}

// shellCaptureState holds the mutable capture accumulators (pi's closure
// variables). pig addition: a mutex guards the accumulators — pi's stdout/
// stderr data callbacks serialize on the event loop, while pig's exec reads
// both pipes on separate goroutines; arrival-order interleaving is preserved
// by lock acquisition order.
type shellCaptureState struct {
	mu                  sync.Mutex
	options             *ShellCaptureOptions
	maxOutputBytes      int
	tailOutput          string
	totalBytes          int
	completedLines      int
	hasOpenLine         bool
	currentLineBytes    int
	fullOutputPath      string
	fullOutputRequested bool
	acceptingOutput     bool
	captureError        *harness.ExecutionError
}

// ensureFullOutputFile creates the spill file with initial content (pi:
// ensureFullOutputFile). Synchronous: the TS promise-chain plumbing
// collapses here (see header note).
func (s *shellCaptureState) ensureFullOutputFile(ctx context.Context, env harness.ExecutionEnv, initialContent string) {
	if s.fullOutputRequested || s.captureError != nil {
		return
	}
	s.fullOutputRequested = true
	prefix := "bash-"
	suffix := ".log"
	tempFile, err := env.CreateTempFile(ctx, &prefix, &suffix)
	if err != nil {
		s.captureError = toExecutionError(err)
		return
	}
	s.fullOutputPath = tempFile
	if err := env.AppendFile(ctx, tempFile, []byte(initialContent)); err != nil {
		s.captureError = toExecutionError(err)
	}
}

// appendFullOutput appends a chunk to the spill file (pi: appendFullOutput).
func (s *shellCaptureState) appendFullOutput(ctx context.Context, env harness.ExecutionEnv, text string) {
	if !s.fullOutputRequested || s.captureError != nil {
		return
	}
	if s.fullOutputPath == "" {
		s.captureError = harness.NewExecutionError(harness.ExecCodeUnknown, "Full output path was not created", nil)
		return
	}
	if err := env.AppendFile(ctx, s.fullOutputPath, []byte(text)); err != nil {
		s.captureError = toExecutionError(err)
	}
}

// createProgress builds the current capture snapshot (pi: createProgress).
func (s *shellCaptureState) createProgress() ShellCaptureProgress {
	tailTruncation := TruncateTail(s.tailOutput, nil)
	totalLines := s.completedLines
	if s.hasOpenLine {
		totalLines++
	}
	truncated := totalLines > DefaultMaxLines || s.totalBytes > DefaultMaxBytes
	truncatedBy := ""
	if truncated {
		if tailTruncation.TruncatedBy != "" {
			truncatedBy = tailTruncation.TruncatedBy
		} else if s.totalBytes > DefaultMaxBytes {
			truncatedBy = TruncatedByBytes
		} else {
			truncatedBy = TruncatedByLines
		}
	}
	truncation := tailTruncation
	truncation.Truncated = truncated
	truncation.TruncatedBy = truncatedBy
	truncation.TotalLines = totalLines
	truncation.TotalBytes = s.totalBytes
	output := s.tailOutput
	if truncated {
		output = truncation.Content
	}
	return ShellCaptureProgress{
		Output:         output,
		Truncation:     truncation,
		FullOutputPath: s.fullOutputPath,
		LastLineBytes:  s.currentLineBytes,
	}
}

// onChunk ingests one raw output chunk (pi: the onChunk closure). Errors
// from the consumer callback are recovered into captureError, mirroring the
// TS try/catch.
func (s *shellCaptureState) onChunk(ctx context.Context, env harness.ExecutionEnv, chunk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.acceptingOutput {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.captureError = harness.NewExecutionError(harness.ExecCodeCallbackError, fmt.Sprintf("%v", r), nil)
		}
	}()

	text := strings.ReplaceAll(SanitizeBinaryOutput(chunk), "\r", "")
	textBytes := len(text)
	s.totalBytes += textBytes
	newlineCount := strings.Count(text, "\n")
	s.completedLines += newlineCount
	if lastNewline := strings.LastIndex(text, "\n"); lastNewline >= 0 {
		trailingText := text[lastNewline+1:]
		s.currentLineBytes = len(trailingText)
		s.hasOpenLine = len(trailingText) > 0
	} else if len(text) > 0 {
		s.currentLineBytes += textBytes
		s.hasOpenLine = true
	}

	s.tailOutput += text
	totalLines := s.completedLines
	if s.hasOpenLine {
		totalLines++
	}
	if (s.totalBytes > DefaultMaxBytes || totalLines > DefaultMaxLines) && !s.fullOutputRequested {
		s.ensureFullOutputFile(ctx, env, s.tailOutput)
	} else if s.fullOutputRequested {
		s.appendFullOutput(ctx, env, text)
	}
	s.tailOutput = trimToLastUtf8Bytes(s.tailOutput, s.maxOutputBytes)
	if s.options != nil && s.options.OnChunk != nil {
		s.options.OnChunk(text, s.createProgress)
	}
}

// ExecuteShellWithCapture executes a shell command while capturing bounded
// tail output and spilling the full output to a file when limits are hit
// (pi: executeShellWithCapture). Execution failures return a
// *harness.ExecutionError as the concrete error (pi: Result collapse).
func ExecuteShellWithCapture(ctx context.Context, env harness.ExecutionEnv, command string, options *ShellCaptureOptions) (*ShellCaptureResult, error) {
	s := &shellCaptureState{
		options:         options,
		maxOutputBytes:  DefaultMaxBytes * 2,
		acceptingOutput: true,
	}

	execOptions := &harness.ShellExecOptions{}
	if options != nil {
		execOptions.Cwd = options.Cwd
		execOptions.Env = options.Env
		execOptions.InheritEnv = options.InheritEnv
		execOptions.Timeout = options.Timeout
	}
	// pi wires the onChunk closure unconditionally (capture happens even
	// when the caller passes no options).
	execOptions.OnStdout = func(chunk string) { s.onChunk(ctx, env, chunk) }
	execOptions.OnStderr = func(chunk string) { s.onChunk(ctx, env, chunk) }

	result, err := env.Exec(ctx, command, execOptions)
	s.mu.Lock()
	s.acceptingOutput = false
	s.mu.Unlock()
	s.mu.Lock()
	progress := s.createProgress()
	s.mu.Unlock()
	if progress.Truncation.Truncated && !s.fullOutputRequested {
		s.mu.Lock()
		s.ensureFullOutputFile(ctx, env, s.tailOutput)
		s.mu.Unlock()
	}
	// pi awaits writeChain here; pig's writes already completed inline (see
	// header note), so only the recorded error is checked.
	if s.captureError != nil {
		return nil, s.captureError
	}
	s.mu.Lock()
	progress = s.createProgress()
	s.mu.Unlock()

	if err != nil {
		execErr := toExecutionError(err)
		if execErr.Code == harness.ExecCodeAborted || ctx.Err() != nil {
			return &ShellCaptureResult{
				ShellCaptureProgress: progress,
				ExitCode:             nil,
				Cancelled:            true,
				Truncated:            progress.Truncation.Truncated,
			}, nil
		}
		if options != nil && options.ReturnExecutionErrors {
			return &ShellCaptureResult{
				ShellCaptureProgress: progress,
				ExitCode:             nil,
				Cancelled:            false,
				Truncated:            progress.Truncation.Truncated,
				ExecutionError:       execErr,
			}, nil
		}
		return nil, execErr
	}

	cancelled := ctx.Err() != nil
	var exitCode *int
	if !cancelled {
		code := result.ExitCode
		exitCode = &code
	}
	return &ShellCaptureResult{
		ShellCaptureProgress: progress,
		ExitCode:             exitCode,
		Cancelled:            cancelled,
		Truncated:            progress.Truncation.Truncated,
	}, nil
}
