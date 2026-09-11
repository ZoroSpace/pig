// utils/panic_log.go — pig panic containment (pig-internal; no pi
// counterpart). pi is single-threaded JS: an uncaught throw crashes the Bun
// process and the terminal scrolls the stack away. pig is goroutine-based,
// so panics are contained at hot-path spawn sites (recover + log + keep
// running) and every containment writes a persistent report here.
//
// Log location: `$PIG_CODING_AGENT_DIR/logs/` or `~/.pig/agent/logs/`
// (mirrors codingagent/config.go's directory resolution; tui is a leaf
// package with the same sync duty — see tui/tui.go). The 20 newest reports
// are kept, older ones are pruned on each write.
//
// Limits (documented, by design):
//   - Fatal runtime errors (concurrent map writes, OOM, stack overflow) are
//     not recoverable in Go and still kill the process — nothing can log
//     them from inside the dying runtime.
//   - A contained panic may leave the failing unit half-updated (e.g. a
//     partially built frame); the report is the source of truth for
//     diagnosis. See docs/feat/PANIC-RESILIENCE.md.
package utils

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sync/atomic"
	"time"
)

// panicLogSequence disambiguates reports written within the same nanosecond
// timestamp bucket (two goroutines containing panics simultaneously).
var panicLogSequence atomic.Uint64

// PanicLogDirectory returns the directory panic reports are written to:
// `$PIG_CODING_AGENT_DIR/logs` falling back to `~/.pig/agent/logs` (and
// finally the OS temp directory when the home directory is unavailable).
// The env name and fallback mirror codingagent/config.go's ConfigDirName
// resolution; this leaf package cannot import codingagent, so the two must
// be kept in sync (same duty as tui/tui.go's copy).
func PanicLogDirectory() string {
	base := os.Getenv("PIG_CODING_AGENT_DIR")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return os.TempDir()
		}
		base = filepath.Join(home, ".pig", "agent")
	}
	return filepath.Join(base, "logs")
}

// LogPanic writes a panic report for the given component and returns the
// report path (empty when the write failed — logging is best-effort and
// must never panic or block the recovering goroutine). The report contains
// the panic value, the panicking goroutine's stack, and a full goroutine
// dump (deadlock context after containment).
func LogPanic(component string, r any) string {
	directory := PanicLogDirectory()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ""
	}
	name := time.Now().Format("20060102-150405.000000000") + fmt.Sprintf("-%d", panicLogSequence.Add(1))
	path := filepath.Join(directory, "panic-"+name+".log")

	report := buildPanicReport(component, r)
	if err := os.WriteFile(path, report, 0o600); err != nil {
		return ""
	}
	prunePanicReports(directory)
	return path
}

// RecoverPanic recovers the current goroutine's panic, logs it, and lets
// execution continue — the defer-site containment helper:
//
//	defer utils.RecoverPanic("component.name")
func RecoverPanic(component string) {
	if r := recover(); r != nil {
		LogPanic(component, r)
	}
}

// Guard runs fn synchronously and contains a panic, returning the report
// path and whether fn panicked. Used at dispatch sites that need to react
// (show a UI notice, count the failure) rather than silently continue.
func Guard(component string, fn func()) (logPath string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			logPath = LogPanic(component, r)
			panicked = true
		}
	}()
	fn()
	return "", false
}

// buildPanicReport assembles the report bytes (never panics).
func buildPanicReport(component string, r any) []byte {
	report := fmt.Sprintf("pig panic report\ncomponent: %s\ntime: %s\npid: %d (%s %s/%s)\n%s\npanic value: %v\npanic type: %T\n\n=== panicking goroutine stack ===\n%s",
		component,
		time.Now().Format("2006-01-02 15:04:05.000 MST"),
		os.Getpid(),
		runtime.Version(), runtime.GOOS, runtime.GOARCH,
		buildInfoLine(),
		r, r,
		debug.Stack(),
	)
	report += allGoroutinesDump()
	return []byte(report)
}

// buildInfoLine extracts the module version and VCS revision from the build
// info (local builds report "(devel)"; the revision is the useful bit).
func buildInfoLine() string {
	version := "(unknown)"
	revision := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				revision = setting.Value
			}
		}
	}
	if revision != "" {
		return fmt.Sprintf("version: %s (vcs %s)", version, revision)
	}
	return fmt.Sprintf("version: %s", version)
}

// allGoroutinesDump renders the runtime's goroutine profile; failures are
// swallowed (the panicking goroutine's stack above is the essential part).
func allGoroutinesDump() string {
	profile := pprof.Lookup("goroutine")
	if profile == nil {
		return ""
	}
	defer func() { _ = recover() }()
	var buffer bytes.Buffer
	// debug=1: one stack line per group + the stacks themselves.
	if err := profile.WriteTo(&buffer, 1); err != nil {
		return ""
	}
	return "\n=== all goroutines ===\n" + buffer.String()
}

// prunePanicReports keeps the 20 newest panic-*.log reports in the
// directory. Best-effort: concurrent pruners (multiple pig processes) may
// race harmlessly.
func prunePanicReports(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type report struct {
		path    string
		modTime time.Time
	}
	var reports []report
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		reports = append(reports, report{filepath.Join(directory, entry.Name()), info.ModTime()})
	}
	if len(reports) <= 20 {
		return
	}
	// Newest first (name embeds a monotonic timestamp; ModTime as fallback).
	for i := 0; i < len(reports); i++ {
		for j := i + 1; j < len(reports); j++ {
			if reports[j].modTime.After(reports[i].modTime) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}
	for _, stale := range reports[20:] {
		_ = os.Remove(stale.path)
	}
}
