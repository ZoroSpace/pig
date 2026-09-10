// pi: src/core/tools/output-accumulator.ts — incremental streaming-output
// tracking with bounded memory.
//
// Segment deviations:
//   - TextDecoder streaming decode → a utf8Decoder helper that buffers
//     incomplete trailing sequences across append() calls (node's
//     TextDecoder({stream:true}) semantics).
//   - The WriteStream temp file → os.File appends; closeTempFile's promise
//     collapses to Close.
package tools

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ZoroSpace/pig/agent/harness/utils"
)

// OutputAccumulatorOptions parameterize the accumulator (pi:
// OutputAccumulatorOptions).
type OutputAccumulatorOptions struct {
	// MaxLines overrides the line limit; nil = DEFAULT_MAX_LINES.
	MaxLines *int
	// MaxBytes overrides the byte limit; nil = DEFAULT_MAX_BYTES.
	MaxBytes *int
	// TempFilePrefix names the spill file; "" = pi's "pi-output".
	TempFilePrefix string
}

// OutputSnapshot is a display snapshot (pi: OutputSnapshot).
type OutputSnapshot struct {
	Content        string
	Truncation     utils.TruncationResult
	FullOutputPath string
}

// defaultTempFilePath builds the spill file path (pi: defaultTempFilePath).
func defaultTempFilePath(prefix string) string {
	id := randomHex(8)
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s.log", prefix, id))
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0"
	}
	return fmt.Sprintf("%x", buf)
}

// OutputAccumulator incrementally tracks streaming output with bounded
// memory (pi: OutputAccumulator). pig addition: a mutex guards the
// accumulators — pi's data callbacks serialize on the event loop, while
// pig's exec reads both pipes on separate goroutines.
type OutputAccumulator struct {
	mu sync.Mutex

	maxLines        int
	maxBytes        int
	maxRollingBytes int
	tempFilePrefix  string
	decoder         utf8Decoder

	rawChunks                [][]byte
	tailText                 string
	tailBytes                int
	tailStartsAtLineBoundary bool
	totalRawBytes            int
	totalDecodedBytes        int
	completedLines           int
	totalLines               int
	currentLineBytes         int
	hasOpenLine              bool
	finished                 bool
	tempFilePath             string
	tempFile                 *os.File
}

// NewOutputAccumulator builds the accumulator (pi: constructor).
func NewOutputAccumulator(options *OutputAccumulatorOptions) *OutputAccumulator {
	maxLines := utils.DefaultMaxLines
	maxBytes := utils.DefaultMaxBytes
	tempFilePrefix := "pi-output"
	if options != nil {
		if options.MaxLines != nil {
			maxLines = *options.MaxLines
		}
		if options.MaxBytes != nil {
			maxBytes = *options.MaxBytes
		}
		if options.TempFilePrefix != "" {
			tempFilePrefix = options.TempFilePrefix
		}
	}
	acc := &OutputAccumulator{
		maxLines:        maxLines,
		maxBytes:        maxBytes,
		maxRollingBytes: maxBytes * 2,
		tempFilePrefix:  tempFilePrefix,
		// pi initializes the boundary flag true (pi:
		// tailStartsAtLineBoundary = true).
		tailStartsAtLineBoundary: true,
	}
	if acc.maxRollingBytes < 1 {
		acc.maxRollingBytes = 1
	}
	return acc
}

// Append decodes and ingests one raw output chunk (pi: append).
func (a *OutputAccumulator) Append(data []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return fmt.Errorf("Cannot append to a finished output accumulator")
	}
	a.totalRawBytes += len(data)
	a.appendDecodedTextLocked(a.decoder.decode(data))

	if a.tempFile != nil || a.shouldUseTempFileLocked() {
		if err := a.ensureTempFileLocked(); err != nil {
			return err
		}
		if a.tempFile != nil {
			if _, err := a.tempFile.Write(data); err != nil {
				return err
			}
		}
	} else if len(data) > 0 {
		a.rawChunks = append(a.rawChunks, append([]byte{}, data...))
	}
	return nil
}

// Finish flushes the streaming decoder and spills to the temp file when
// needed (pi: finish).
func (a *OutputAccumulator) Finish() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finished {
		return
	}
	a.finished = true
	a.appendDecodedTextLocked(a.decoder.decode(nil))
	if a.shouldUseTempFileLocked() {
		_ = a.ensureTempFileLocked()
	}
}

// Snapshot builds the current display snapshot (pi: snapshot).
func (a *OutputAccumulator) Snapshot(persistIfTruncated bool) OutputSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	tailTruncation := utils.TruncateTail(a.getSnapshotTextLocked(), &utils.TruncationOptions{MaxLines: &a.maxLines, MaxBytes: &a.maxBytes})
	truncated := a.totalLines > a.maxLines || a.totalDecodedBytes > a.maxBytes
	truncatedBy := ""
	if truncated {
		if tailTruncation.TruncatedBy != "" {
			truncatedBy = tailTruncation.TruncatedBy
		} else if a.totalDecodedBytes > a.maxBytes {
			truncatedBy = utils.TruncatedByBytes
		} else {
			truncatedBy = utils.TruncatedByLines
		}
	}
	truncation := tailTruncation
	truncation.Truncated = truncated
	truncation.TruncatedBy = truncatedBy
	truncation.TotalLines = a.totalLines
	truncation.TotalBytes = a.totalDecodedBytes
	truncation.MaxLines = a.maxLines
	truncation.MaxBytes = a.maxBytes

	if persistIfTruncated && truncation.Truncated {
		_ = a.ensureTempFileLocked()
	}
	return OutputSnapshot{
		Content:        truncation.Content,
		Truncation:     truncation,
		FullOutputPath: a.tempFilePath,
	}
}

// CloseTempFile closes the spill file (pi: closeTempFile).
func (a *OutputAccumulator) CloseTempFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tempFile == nil {
		return nil
	}
	file := a.tempFile
	a.tempFile = nil
	return file.Close()
}

// GetLastLineBytes returns the byte length of the currently open line (pi:
// getLastLineBytes).
func (a *OutputAccumulator) GetLastLineBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentLineBytes
}

// appendDecodedTextLocked ingests decoded text (pi: appendDecodedText).
func (a *OutputAccumulator) appendDecodedTextLocked(text string) {
	if len(text) == 0 {
		return
	}
	bytes := len(text)
	a.totalDecodedBytes += bytes
	a.tailText += text
	a.tailBytes += bytes
	if a.tailBytes > a.maxRollingBytes*2 {
		a.trimTailLocked()
	}

	newlines := strings.Count(text, "\n")
	lastNewline := strings.LastIndex(text, "\n")
	if newlines == 0 {
		a.currentLineBytes += bytes
		a.hasOpenLine = true
	} else {
		a.completedLines += newlines
		tail := text[lastNewline+1:]
		a.currentLineBytes = len(tail)
		a.hasOpenLine = len(tail) > 0
	}
	a.totalLines = a.completedLines
	if a.hasOpenLine {
		a.totalLines++
	}
}

// trimTailLocked trims the decoded tail to the rolling byte budget (pi:
// trimTail).
func (a *OutputAccumulator) trimTailLocked() {
	if len(a.tailText) <= a.maxRollingBytes {
		a.tailBytes = len(a.tailText)
		return
	}
	start := len(a.tailText) - a.maxRollingBytes
	for start < len(a.tailText) && a.tailText[start]&0xc0 == 0x80 {
		start++
	}
	if start == 0 {
		// pi: keep the current flag when nothing is trimmed.
	} else {
		a.tailStartsAtLineBoundary = a.tailText[start-1] == 0x0a
	}
	a.tailText = a.tailText[start:]
	a.tailBytes = len(a.tailText)
}

// getSnapshotTextLocked drops the partially decoded first line when the tail
// was trimmed mid-line (pi: getSnapshotText).
func (a *OutputAccumulator) getSnapshotTextLocked() string {
	if a.tailStartsAtLineBoundary {
		return a.tailText
	}
	firstNewline := strings.Index(a.tailText, "\n")
	if firstNewline == -1 {
		return a.tailText
	}
	return a.tailText[firstNewline+1:]
}

// shouldUseTempFileLocked reports whether the full output must be preserved
// (pi: shouldUseTempFile).
func (a *OutputAccumulator) shouldUseTempFileLocked() bool {
	return a.totalRawBytes > a.maxBytes || a.totalDecodedBytes > a.maxBytes || a.totalLines > a.maxLines
}

// ensureTempFileLocked opens the spill file and flushes the buffered raw
// chunks (pi: ensureTempFile).
func (a *OutputAccumulator) ensureTempFileLocked() error {
	if a.tempFilePath != "" {
		return nil
	}
	a.tempFilePath = defaultTempFilePath(a.tempFilePrefix)
	file, err := os.OpenFile(a.tempFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	a.tempFile = file
	for _, chunk := range a.rawChunks {
		if _, err := file.Write(chunk); err != nil {
			return err
		}
	}
	a.rawChunks = nil
	return nil
}

// utf8Decoder decodes a byte stream into strings the way node's
// TextDecoder({stream:true}) does: incomplete trailing sequences are
// buffered until the next decode call (or flushed by decode(nil)). Invalid
// bytes pass through unchanged (declared in the package header).
type utf8Decoder struct {
	pending []byte
}

func (d *utf8Decoder) decode(data []byte) string {
	if len(data) > 0 {
		d.pending = append(d.pending, data...)
	}
	buf := d.pending
	if len(buf) == 0 {
		return ""
	}
	keep := 0
	if buf[len(buf)-1]&0x80 != 0 {
		// The last byte belongs to a multi-byte sequence; find its start.
		start := len(buf) - 1
		for start > 0 && len(buf)-start < 4 && buf[start]&0xc0 == 0x80 {
			start--
		}
		seqLen := len(buf) - start
		first := buf[start]
		want := 0
		switch {
		case first&0xe0 == 0xc0:
			want = 2
		case first&0xf0 == 0xe0:
			want = 3
		case first&0xf8 == 0xf0:
			want = 4
		}
		if want > 0 && seqLen < want {
			keep = seqLen
		}
	}
	out := buf[:len(buf)-keep]
	d.pending = append([]byte{}, buf[len(buf)-keep:]...)
	return string(out)
}
