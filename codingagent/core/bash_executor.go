// pi: src/core/bash-executor.ts — bash command execution with streaming
// support and cancellation. A unified bash execution implementation used by
// AgentSession.executeBash() (interactive and RPC modes) and by modes that
// need bash execution directly.
//
// Segment deviations (declared):
//   - pi's options.signal → the Go context; the cancelled flag derives from
//     ctx.Err() (pi: signal.aborted).
//   - The temp file write stream becomes a plain file handle.
//   - pi's TextDecoder(stream:true) → a local UTF-8 chunk decoder with the
//     same streaming semantics (incomplete sequences buffered, invalid bytes
//     become U+FFFD; the no-argument decode() flush is flush()).
//   - The unfinished-ANSI buffering + split (pi 27c7b6ff4) lives in
//     utils.SplitIncompleteAnsiSuffix; the abort path is folded into the
//     single post-exec result assembly like pi's try/catch.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	harnessutils "github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// BashExecutorOptions parameterize ExecuteBashWithOperations (pi:
// BashExecutorOptions).
type BashExecutorOptions struct {
	// OnChunk receives streaming output chunks (already sanitized).
	OnChunk func(chunk string)
	// Context carries cancellation (pi: signal).
	Context context.Context
}

// BashResult is the outcome of one bash execution (pi: BashResult).
type BashResult struct {
	// Output is the combined stdout+stderr output (sanitized, possibly
	// truncated).
	Output string `json:"output"`
	// ExitCode is the process exit code; nil when killed/cancelled (pi:
	// undefined — JSON.stringify omits the key).
	ExitCode *int `json:"exitCode,omitempty"`
	// Cancelled reports whether the command was cancelled via the context.
	Cancelled bool `json:"cancelled"`
	// Truncated reports whether the output was truncated.
	Truncated bool `json:"truncated"`
	// FullOutputPath is the temp file containing the full output when the
	// output exceeded the truncation threshold (pi: fullOutputPath?).
	FullOutputPath string `json:"fullOutputPath,omitempty"`
}

// bashExecutorDecoder decodes streamed bytes the way node's
// TextDecoder(stream:true) does: incomplete sequences buffer until complete
// (completed by a later chunk or flushed at end of stream), invalid bytes
// become U+FFFD. Truncation is detected with utf8.FullRune: DecodeRune
// reports an incomplete prefix and an invalid encoding identically as
// (RuneError, 1), so the size alone cannot distinguish "buffer for later"
// from "replace now".
type bashExecutorDecoder struct {
	pending []byte
}

func (d *bashExecutorDecoder) chunk(data []byte) string {
	if len(d.pending) > 0 {
		d.pending = append(d.pending, data...)
		data = d.pending
		d.pending = nil
	}
	var out []byte
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size <= 1 {
			if len(data) < utf8.UTFMax && !utf8.FullRune(data) {
				// Incomplete sequence: buffer the remainder.
				d.pending = append(d.pending, data...)
				break
			}
			out = append(out, []byte("\uFFFD")...)
			data = data[1:]
			continue
		}
		out = append(out, data[:size]...)
		data = data[size:]
	}
	return string(out)
}

// flush drains a buffered incomplete sequence at end of stream the way
// node's no-argument decoder.decode() does: a truncated sequence decodes to
// a single U+FFFD. chunk() only ever buffers one incomplete rune prefix, so
// the pending bytes are always exactly one truncated sequence.
func (d *bashExecutorDecoder) flush() string {
	if len(d.pending) == 0 {
		return ""
	}
	d.pending = nil
	return "\uFFFD"
}

// ExecuteBashWithOperations executes a bash command using custom
// BashOperations, used for remote execution (SSH, containers, etc.) (pi:
// executeBashWithOperations).
func ExecuteBashWithOperations(ctx context.Context, command string, cwd string, operations tools.BashOperations, options *BashExecutorOptions) (BashResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var outputChunks []string
	outputBytes := 0
	maxOutputBytes := harnessutils.DefaultMaxBytes * 2

	tempFilePath := ""
	var tempFile *os.File
	totalBytes := 0

	ensureTempFile := func() error {
		if tempFilePath != "" {
			return nil
		}
		id := make([]byte, 8)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		tempFilePath = filepath.Join(os.TempDir(), "pi-bash-"+hex.EncodeToString(id)+".log")
		file, err := os.OpenFile(tempFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		tempFile = file
		for _, chunk := range outputChunks {
			if _, err := tempFile.WriteString(chunk); err != nil {
				return err
			}
		}
		return nil
	}

	var decoder bashExecutorDecoder
	// Unfinished escape sequence at the end of the previous chunk, completed
	// by the next chunk (pi: pendingAnsi).
	pendingAnsi := ""

	appendText := func(rawText string) {
		// Sanitize: strip ANSI, replace binary garbage, normalize newlines.
		text := codingagentutils.SanitizeBinaryOutput(codingagentutils.StripAnsi(rawText))
		text = strings.ReplaceAll(text, "\r", "")
		if text == "" {
			return
		}

		// Start writing to the temp file once the threshold is exceeded.
		if totalBytes > harnessutils.DefaultMaxBytes {
			_ = ensureTempFile()
		}
		if tempFile != nil {
			_, _ = tempFile.WriteString(text)
		}

		// Keep the rolling buffer.
		outputChunks = append(outputChunks, text)
		outputBytes += len(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
		}

		// Stream to the callback.
		if options != nil && options.OnChunk != nil {
			options.OnChunk(text)
		}
	}

	onData := func(data []byte) {
		totalBytes += len(data)
		complete, pending := codingagentutils.SplitIncompleteAnsiSuffix(pendingAnsi + decoder.chunk(data))
		pendingAnsi = pending
		appendText(complete)
	}

	flushOutput := func() {
		rest := pendingAnsi + decoder.flush()
		pendingAnsi = ""
		appendText(rest)
	}

	exitCode, err := operations.Exec(ctx, command, cwd, &tools.BashExecOptions{OnData: onData})
	// An aborted command still returns the output it produced so far: flush
	// and assemble the result on both the success and the abort path; only a
	// non-abort error rethrows (pi: single-exit try/catch).
	if err != nil && ctx.Err() == nil {
		if tempFile != nil {
			_ = tempFile.Close()
		}
		return BashResult{}, err
	}

	flushOutput()
	fullOutput := strings.Join(outputChunks, "")
	truncationResult := harnessutils.TruncateTail(fullOutput, nil)
	if truncationResult.Truncated {
		_ = ensureTempFile()
	}
	if tempFile != nil {
		_ = tempFile.Close()
	}
	cancelled := ctx.Err() != nil
	result := BashResult{
		Output:    fullOutput,
		Cancelled: cancelled,
		Truncated: truncationResult.Truncated,
	}
	if truncationResult.Truncated {
		result.Output = truncationResult.Content
	}
	if !cancelled && exitCode != nil {
		result.ExitCode = exitCode
	}
	result.FullOutputPath = tempFilePath
	return result, nil
}
