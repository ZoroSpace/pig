// pi: src/core/crash-log.ts — best-effort crash persistence so the next
// start can point the user at `/bug` (pi v0.86.0). Deviation: pi reads
// `error.stack` off Error objects; Go errors carry no stack, so the caller
// passes one when available (Input.Stack) and nil otherwise.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

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
