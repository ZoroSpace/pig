// pi: src/harness/utils/truncate.ts — shared truncation utilities for tool
// outputs.
//
// Truncation is based on two independent limits - whichever is hit first
// wins:
//   - Line limit (default: 2000 lines)
//   - Byte limit (default: 50KB)
//
// Never returns partial lines (except bash tail truncation edge case).
//
// Deviations:
//   - TS strings are UTF-16 code-unit sequences; Go strings are UTF-8 bytes.
//     utf8ByteLength collapses to len(string). Lone surrogates are
//     unrepresentable in Go, so replaceUnpairedSurrogates has no counterpart
//     (decoder-level U+FFFD replaces invalid input before we ever see it).
//   - truncateLine counts characters as UTF-16 code units in TS (.length);
//     the Go counterpart counts runes, the closest analog for "characters".
//   - TruncationOptions optional members are pointers (nil = pi's undefined
//     default); truncatedBy is "" when TS yields null.
package utils

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// DefaultMaxLines is the default line limit (pi: DEFAULT_MAX_LINES).
	DefaultMaxLines = 2000
	// DefaultMaxBytes is the default byte limit: 50KB (pi: DEFAULT_MAX_BYTES).
	DefaultMaxBytes = 50 * 1024
	// GrepMaxLineLength is the max chars per grep match line (pi:
	// GREP_MAX_LINE_LENGTH).
	GrepMaxLineLength = 500
)

// TruncatedBy names the limit that was hit: "", "lines", or "bytes" (pi:
// truncatedBy: "lines" | "bytes" | null).
const (
	TruncatedByLines string = "lines"
	TruncatedByBytes string = "bytes"
)

// TruncationResult reports one truncation outcome (pi: TruncationResult).
type TruncationResult struct {
	// Content is the truncated content.
	Content string
	// Truncated reports whether truncation occurred.
	Truncated bool
	// TruncatedBy names the limit that was hit: "", "lines", or "bytes"
	// (pi: truncatedBy, "" ↔ null).
	TruncatedBy string
	// TotalLines is the total number of lines in the original content.
	TotalLines int
	// TotalBytes is the total number of bytes in the original content.
	TotalBytes int
	// OutputLines is the number of complete lines in the truncated output.
	OutputLines int
	// OutputBytes is the number of bytes in the truncated output.
	OutputBytes int
	// LastLinePartial reports whether the last line was partially truncated
	// (only for tail truncation edge case).
	LastLinePartial bool
	// FirstLineExceedsLimit reports whether the first line exceeded the byte
	// limit (for head truncation).
	FirstLineExceedsLimit bool
	// MaxLines is the max lines limit that was applied.
	MaxLines int
	// MaxBytes is the max bytes limit that was applied.
	MaxBytes int
}

// TruncationOptions parameterizes truncation (pi: TruncationOptions).
type TruncationOptions struct {
	// MaxLines is the maximum number of lines (default: 2000). nil = default.
	MaxLines *int
	// MaxBytes is the maximum number of bytes (default: 50KB). nil = default.
	MaxBytes *int
}

// FormatSize formats bytes as human-readable size (pi: formatSize).
func FormatSize(bytes int64) string {
	if bytes < 1024 {
		return strconv.FormatInt(bytes, 10) + "B"
	} else if bytes < 1024*1024 {
		return strconv.FormatFloat(float64(bytes)/1024, 'f', 1, 64) + "KB"
	}
	return strconv.FormatFloat(float64(bytes)/(1024*1024), 'f', 1, 64) + "MB"
}

// splitLinesForCounting splits by "\n" and drops the trailing empty segment
// when content ends with a newline (pi: splitLinesForCounting).
func splitLinesForCounting(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// resolveLimits applies option defaults (pi: the maxLines/maxBytes ?? …).
func resolveLimits(options *TruncationOptions) (maxLines int, maxBytes int) {
	maxLines = DefaultMaxLines
	maxBytes = DefaultMaxBytes
	if options != nil {
		if options.MaxLines != nil {
			maxLines = *options.MaxLines
		}
		if options.MaxBytes != nil {
			maxBytes = *options.MaxBytes
		}
	}
	return maxLines, maxBytes
}

// TruncateHead truncates content from the head (keep first N lines/bytes)
// (pi: truncateHead). Suitable for file reads where you want to see the
// beginning.
//
// Never returns partial lines. If first line exceeds byte limit, returns
// empty content with FirstLineExceedsLimit=true.
func TruncateHead(content string, options *TruncationOptions) TruncationResult {
	maxLines, maxBytes := resolveLimits(options)

	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	// Check if no truncation needed
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TruncatedBy: "",
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Check if first line alone exceeds byte limit
	firstLineBytes := len(lines[0])
	if firstLineBytes > maxBytes {
		return TruncationResult{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           TruncatedByBytes,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			LastLinePartial:       false,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	// Collect complete lines that fit
	var outputLinesArr []string
	outputBytesCount := 0
	truncatedBy := TruncatedByLines

	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := lines[i]
		lineBytes := len(line)
		if i > 0 {
			lineBytes++ // +1 for newline
		}

		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = TruncatedByBytes
			break
		}

		outputLinesArr = append(outputLinesArr, line)
		outputBytesCount += lineBytes
	}

	// If we exited due to line limit
	if len(outputLinesArr) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = TruncatedByLines
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	finalOutputBytes := len(outputContent)

	return TruncationResult{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(outputLinesArr),
		OutputBytes: finalOutputBytes,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

// TruncateTail truncates content from the tail (keep last N lines/bytes)
// (pi: truncateTail). Suitable for bash output where you want to see the end
// (errors, final results).
//
// May return partial first line if the last line of original content exceeds
// byte limit.
func TruncateTail(content string, options *TruncationOptions) TruncationResult {
	maxLines, maxBytes := resolveLimits(options)

	totalBytes := len(content)
	lines := splitLinesForCounting(content)
	totalLines := len(lines)

	// Check if no truncation needed
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     content,
			Truncated:   false,
			TruncatedBy: "",
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Work backwards from the end
	var outputLinesArr []string
	outputBytesCount := 0
	truncatedBy := TruncatedByLines
	lastLinePartial := false

	for i := len(lines) - 1; i >= 0 && len(outputLinesArr) < maxLines; i-- {
		line := lines[i]
		lineBytes := len(line)
		if len(outputLinesArr) > 0 {
			lineBytes++ // +1 for newline
		}

		if outputBytesCount+lineBytes > maxBytes {
			truncatedBy = TruncatedByBytes
			// Edge case: if we haven't added ANY lines yet and this line exceeds maxBytes,
			// take the end of the line (partial)
			if len(outputLinesArr) == 0 {
				truncatedLine := truncateStringToBytesFromEnd(line, maxBytes)
				outputLinesArr = []string{truncatedLine}
				outputBytesCount = len(truncatedLine)
				lastLinePartial = true
			}
			break
		}

		// pi unshifts; building the slice front-first. Prepend by shifting.
		outputLinesArr = append([]string{line}, outputLinesArr...)
		outputBytesCount += lineBytes
	}

	// If we exited due to line limit
	if len(outputLinesArr) >= maxLines && outputBytesCount <= maxBytes {
		truncatedBy = TruncatedByLines
	}

	outputContent := strings.Join(outputLinesArr, "\n")
	finalOutputBytes := len(outputContent)

	return TruncationResult{
		Content:         outputContent,
		Truncated:       true,
		TruncatedBy:     truncatedBy,
		TotalLines:      totalLines,
		TotalBytes:      totalBytes,
		OutputLines:     len(outputLinesArr),
		OutputBytes:     finalOutputBytes,
		LastLinePartial: lastLinePartial,
		MaxLines:        maxLines,
		MaxBytes:        maxBytes,
	}
}

// truncateStringToBytesFromEnd truncates a string to fit within a byte limit
// (from the end) (pi: truncateStringToBytesFromEnd). Handles multi-byte UTF-8
// characters correctly.
func truncateStringToBytesFromEnd(str string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}

	outputBytes := 0
	start := len(str)
	for end := len(str); end > 0; {
		// Walk back one full rune (pi walks UTF-16 units; surrogates are
		// unrepresentable in Go, so rune granularity is exact).
		characterStart := end - 1
		for characterStart > 0 && !utf8.RuneStart(str[characterStart]) {
			characterStart--
		}
		characterBytes := end - characterStart
		if outputBytes+characterBytes > maxBytes {
			break
		}
		outputBytes += characterBytes
		start = characterStart
		end = characterStart
	}

	return str[start:]
}

// TruncateLine truncates a single line to max characters, adding [truncated]
// suffix (pi: truncateLine). Used for grep match lines. The second return is
// wasTruncated.
func TruncateLine(line string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(line) <= maxChars {
		return line, false
	}
	runes := []rune(line)
	return string(runes[:maxChars]) + "... [truncated]", true
}
