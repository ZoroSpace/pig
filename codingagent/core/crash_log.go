// pi: src/core/crash-log.ts — best-effort crash persistence so the next
// start can point the user at `/bug` (pi v0.86.0). Deviation: pi reads
// `error.stack` off Error objects; Go errors carry no stack, so the caller
// passes one when available (Input.Stack) and nil otherwise.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/codingagent"
)

// CrashRecordKind discriminates the crash origin (pi: CrashRecord["kind"]).
type CrashRecordKind string

const (
	// CrashKindUncaughtException is an uncaught panic (pi:
	// "uncaught_exception").
	CrashKindUncaughtException CrashRecordKind = "uncaught_exception"
	// CrashKindFatalError is a fatal runtime error path (pi: "fatal_error").
	CrashKindFatalError CrashRecordKind = "fatal_error"
)

// maxCrashRecords caps the crash log ring (pi: MAX_CRASH_RECORDS).
const maxCrashRecords = 5

// maxCrashAge drops stale records (pi: MAX_AGE = 7d).
const maxCrashAge = 7 * 24 * time.Hour

// CrashRecord is one persisted crash entry (pi: CrashRecord).
type CrashRecord struct {
	Timestamp   string          `json:"timestamp"`
	Version     string          `json:"version"`
	Kind        CrashRecordKind `json:"kind"`
	Message     string          `json:"message"`
	Stack       *string         `json:"stack"`
	SessionFile *string         `json:"sessionFile"`
	Cwd         string          `json:"cwd"`
	Notified    *bool           `json:"notified,omitempty"`
}

func crashLogPath() string {
	return filepath.Join(codingagent.GetAgentDir(), "crashes.json")
}

// ReadCrashLog returns the persisted crash records, skipping malformed ones
// (pi: readCrashLog). Records must carry string timestamp + message.
func ReadCrashLog(path string) []CrashRecord {
	if path == "" {
		path = crashLogPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var records []CrashRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil
	}
	out := make([]CrashRecord, 0, len(records))
	for _, record := range records {
		if record.Timestamp == "" || record.Message == "" {
			continue
		}
		out = append(out, record)
	}
	return out
}

func writeCrashLog(records []CrashRecord, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// CrashRecordInput is the recordCrash parameter (pi: the options object).
type CrashRecordInput struct {
	Kind        CrashRecordKind
	Error       error
	SessionFile string // "" = null (pi: sessionFile?)
	Cwd         string
	// Stack attaches a stack trace when the caller has one (pig deviation:
	// Go errors carry no stack; pi reads error.stack). "" = null.
	Stack string
}

// extensionStackMetadata is the Extension subset the stack matcher consumes
// (pi: Pick<Extension, "path" | "resolvedPath" | "sourceInfo">; pig takes the
// full *Extension, whose extra fields are ignored).
type extensionStackMetadata = Extension

// normalizeStackPath normalizes a path for stack matching: backslashes to
// slashes, trailing slashes stripped (pi: normalizeStackPath).
func normalizeStackPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.TrimRight(value, "/")
}

// jsURISpace reports whether r is in the JS regex \s class (WhiteSpace +
// LineTerminator; pi matches stack-frame text with \\s under the u flag, so
// the full ECMAScript set is honored — Go's regexp \\s is ASCII-only).
func jsURISpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// stackFrameLine reports whether a stack line matches pi's /^\s+at\\s/u
// frame filter.
func stackFrameLine(line string) bool {
	runes := []rune(line)
	i := 0
	for i < len(runes) && jsURISpace(runes[i]) {
		i++
	}
	if i == 0 || i+2 >= len(runes) {
		return false
	}
	if runes[i] != 'a' || runes[i+1] != 't' {
		return false
	}
	return jsURISpace(runes[i+2])
}

// unhex decodes one hex digit, -1 when invalid (JS hexDigit production).
func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// jsReservedURIByte reports whether the byte is in the ECMAScript reserved
// URI set that decodeURI leaves percent-encoded.
func jsReservedURIByte(b byte) bool {
	switch b {
	case ';', '/', '?', ':', '@', '&', '=', '+', '$', ',', '#':
		return true
	}
	return false
}

// jsDecodeURI implements the JS decodeURI subset used for stack lines (pi:
// decodeURI(line) in a try/catch): decodes %XX escapes — multi-byte UTF-8
// sequences as one unit — except the reserved set, which stays encoded.
// Malformed sequences return the input unchanged (pi: URIError → catch →
// original line).
func jsDecodeURI(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	parseEscape := func(s string, i int) (int, bool) {
		if i+2 >= len(s) || s[i] != '%' {
			return 0, false
		}
		hi, lo := unhex(s[i+1]), unhex(s[i+2])
		if hi < 0 || lo < 0 {
			return 0, false
		}
		return hi<<4 | lo, true
	}
	var out []byte
	out = make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			out = append(out, s[i])
			i++
			continue
		}
		v, ok := parseEscape(s, i)
		if !ok {
			return s
		}
		if v < 0x80 {
			if jsReservedURIByte(byte(v)) {
				out = append(out, s[i:i+3]...)
			} else {
				out = append(out, byte(v))
			}
			i += 3
			continue
		}
		// Multi-byte: collect the run of ≥0x80 escapes and validate it as
		// UTF-8 (pi decodes the exact UTF-8 sequence or throws).
		var raw []byte
		j := i
		for j < len(s) {
			v2, ok2 := parseEscape(s, j)
			if !ok2 || v2 < 0x80 {
				break
			}
			raw = append(raw, byte(v2))
			j += 3
		}
		if !utf8.Valid(raw) {
			return s
		}
		out = append(out, raw...)
		i = j
	}
	return string(out)
}

// stackContainsPath reports whether the stack haystack contains targetPath
// (pi: stackContainsPath). includeDescendants also matches subdirectories;
// Windows drive-letter targets (c:/…) match case-insensitively (pi:
// /^[a-z]:\//iu).
func stackContainsPath(stack string, targetPath string, includeDescendants bool) bool {
	target := normalizeStackPath(targetPath)
	if target == "" || strings.HasPrefix(target, "<") {
		return false
	}
	caseInsensitive := len(target) >= 3
	if caseInsensitive {
		c := target[0]
		caseInsensitive = ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) && target[1] == ':' && target[2] == '/'
	}
	haystack, needle := stack, target
	if caseInsensitive {
		haystack = strings.ToLower(stack)
		needle = strings.ToLower(target)
	}
	if includeDescendants {
		return strings.Contains(haystack, needle+"/")
	}
	base := 0
	for {
		idx := strings.Index(haystack[base:], needle)
		if idx == -1 {
			return false
		}
		index := base + idx
		rest := haystack[index+len(needle):]
		if rest == "" {
			return true
		}
		// pi: next === undefined | ":" | ")" | \\s. The match end is always a
		// rune boundary (UTF-8 is self-synchronizing), so decode the rune.
		next, _ := utf8.DecodeRuneInString(rest)
		if next == ':' || next == ')' || jsURISpace(next) {
			return true
		}
		base = index + len(needle)
	}
}

// extensionStackSourceIsSingleFile reports whether the package source is a
// single script file (pi: !/^(?:npm:|git:|https?:\/\/|ssh:\/\/)/u.test(
// source) && /\.[cm]?[jt]s$/u.test(source)).
func extensionStackSourceIsSingleFile(source string) bool {
	for _, prefix := range []string{"npm:", "git:", "https://", "http://", "ssh://"} {
		if strings.HasPrefix(source, prefix) {
			return false
		}
	}
	for _, suffix := range []string{".js", ".ts", ".cjs", ".cts", ".mjs", ".mts"} {
		if strings.HasSuffix(source, suffix) {
			return true
		}
	}
	return false
}

// extensionStackIsDirectoryEntry reports whether the resolved path is an
// index entry file (pi: /\/index\.[cm]?[jt]s$/u).
func extensionStackIsDirectoryEntry(resolvedPath string) bool {
	for _, suffix := range []string{"/index.js", "/index.ts", "/index.cjs", "/index.cts", "/index.mjs", "/index.mts"} {
		if strings.HasSuffix(resolvedPath, suffix) {
			return true
		}
	}
	return false
}

// FindExtensionStackMatches returns the labels of loaded extensions whose
// source files appear in a stack trace (pi: findExtensionStackMatches;
// v0.87.0). stack "" = no matches (pi: undefined → []). Paths are matched
// with backslash/trailing-slash normalization, decodeURI'd frames, and
// case-insensitive matching for Windows drive-letter roots; the error line
// itself is dropped so paths quoted in the message never attribute.
func FindExtensionStackMatches(stack string, extensions []*extensionStackMetadata) []string {
	if stack == "" {
		return nil
	}
	lines := strings.Split(stack, "\n")[1:]
	var frames []string
	for _, line := range lines {
		if stackFrameLine(line) {
			frames = append(frames, jsDecodeURI(line))
		}
	}
	normalizedStack := strings.ReplaceAll(strings.Join(frames, "\n"), "\\", "/")
	var matches []string
	seen := map[string]bool{}
	for _, extension := range extensions {
		resolvedPath := normalizeStackPath(extension.ResolvedPath)
		singleFilePackage := extension.SourceInfo.Origin == SourceOriginPackage &&
			extensionStackSourceIsSingleFile(extension.SourceInfo.Source)
		// pi: extension.sourceInfo.baseDir truthiness — "" is not a root.
		packageRoot := ""
		if extension.SourceInfo.Origin == SourceOriginPackage && !singleFilePackage && extension.SourceInfo.BaseDir != "" {
			packageRoot = extension.SourceInfo.BaseDir
		}
		slashIndex := strings.LastIndex(resolvedPath, "/")
		directoryEntry := extensionStackIsDirectoryEntry(resolvedPath)
		matched := false
		switch {
		case packageRoot != "":
			matched = stackContainsPath(normalizedStack, packageRoot, true)
		case directoryEntry && slashIndex != -1:
			matched = stackContainsPath(normalizedStack, resolvedPath[:slashIndex], true)
		default:
			matched = stackContainsPath(normalizedStack, resolvedPath, false)
		}
		if !matched {
			continue
		}
		label := extension.Path
		if extension.SourceInfo.Origin == SourceOriginPackage && extension.SourceInfo.Source != "" {
			label = extension.SourceInfo.Source
		}
		if !seen[label] {
			seen[label] = true
			matches = append(matches, label)
		}
	}
	return matches
}

// RecordCrash persists one crash entry, keeping the newest maxCrashRecords
// (pi: recordCrash — "best-effort persistence for callers that are already
// crashing"). Returns the record, or nil when nothing was written.
func RecordCrash(crash CrashRecordInput, path string) *CrashRecord {
	if path == "" {
		path = crashLogPath()
	}
	message := ""
	if crash.Error != nil {
		message = crash.Error.Error()
	}
	var stack *string
	if crash.Stack != "" {
		stack = lo.ToPtr(crash.Stack)
	}
	var sessionFile *string
	if crash.SessionFile != "" {
		sessionFile = lo.ToPtr(crash.SessionFile)
	}
	record := CrashRecord{
		Timestamp:   time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Version:     codingagent.Version,
		Kind:        crash.Kind,
		Message:     message,
		Stack:       stack,
		SessionFile: sessionFile,
		Cwd:         crash.Cwd,
	}
	records := append(ReadCrashLog(path), record)
	if len(records) > maxCrashRecords {
		records = records[len(records)-maxCrashRecords:]
	}
	if writeCrashLog(records, path) != nil {
		return nil
	}
	return &record
}

// TakeUnnotifiedCrash returns the newest recent unannounced crash, marking
// pending records as announced (pi: takeUnnotifiedCrash).
func TakeUnnotifiedCrash(path string, now time.Time) *CrashRecord {
	if path == "" {
		path = crashLogPath()
	}
	if now.IsZero() {
		now = time.Now()
	}
	records := ReadCrashLog(path)
	var crash *CrashRecord
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.Notified != nil && *record.Notified {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, record.Timestamp)
		// Date.parse failure → NaN comparison → false (pi): skip.
		if err != nil || now.Sub(ts) > maxCrashAge {
			continue
		}
		crash = &records[i]
		break
	}
	if crash == nil {
		return nil
	}
	// Showing the notice again is harmless (pi comment).
	for i := range records {
		if records[i].Notified == nil || !*records[i].Notified {
			records[i].Notified = lo.ToPtr(true)
		}
	}
	if writeCrashLog(records, path) != nil {
		return crash
	}
	return crash
}

// ClearCrashLog removes the crash log; the records can be attached again if
// cleanup fails (pi: clearCrashLog).
func ClearCrashLog(path string) {
	if path == "" {
		path = crashLogPath()
	}
	_ = os.Remove(path)
}
