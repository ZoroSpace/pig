// pi: core/timings.ts — central timing instrumentation for startup profiling.
// Enabled with PI_TIMING=1.
package core

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// timingEnabled is pi's module-level ENABLED constant.
var timingEnabled = os.Getenv("PI_TIMING") == "1"

// TimingNamespace collects one namespace's entries (pi: TimingNamespace).
type timingNamespaceState struct {
	timings  []timingEntry
	lastTime int64
}

type timingEntry struct {
	label string
	ms    int64
}

// TimingLabel is "main" | "extensions" (pi: TimingLabel).
type TimingLabel = string

const (
	TimingMain       TimingLabel = "main"
	TimingExtensions TimingLabel = "extensions"
)

var (
	timingsMu       sync.Mutex
	timingNamespace = map[TimingLabel]*timingNamespaceState{}
)

// ResetTimings clears one timing namespace (pi: resetTimings).
func ResetTimings(namespace TimingLabel) {
	if !timingEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	timingNamespace[namespace] = &timingNamespaceState{timings: []timingEntry{}, lastTime: time.Now().UnixMilli()}
}

// Time records one timing entry (pi: time).
func Time(label string, namespace TimingLabel) {
	if !timingEnabled {
		return
	}
	now := time.Now().UnixMilli()

	timingsMu.Lock()
	defer timingsMu.Unlock()
	ns, ok := timingNamespace[namespace]
	if !ok {
		ResetTimings(namespace)
		ns = timingNamespace[namespace]
	}
	ns.timings = append(ns.timings, timingEntry{label: label, ms: now - ns.lastTime})
	ns.lastTime = now
}

func printTimingGroup(title string, timings []timingEntry) {
	var printable []timingEntry
	for _, t := range timings {
		if t.ms >= 0 {
			printable = append(printable, t)
		}
	}
	if len(printable) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\n--- %s ---\n", title)
	total := int64(0)
	for _, t := range printable {
		fmt.Fprintf(os.Stderr, "  %s: %dms\n", t.label, t.ms)
		total += t.ms
	}
	fmt.Fprintf(os.Stderr, "  TOTAL: %dms\n", total)
	fmt.Fprintf(os.Stderr, "%s\n\n", repeatStr("-", len(title)+8))
}

func repeatStr(s string, n int) string { return strings.Repeat(s, n) }

// PrintTimings renders all collected timings (pi: printTimings).
func PrintTimings() {
	if !timingEnabled {
		return
	}
	timingsMu.Lock()
	defer timingsMu.Unlock()
	for namespace, ns := range timingNamespace {
		printTimingGroup(fmt.Sprintf("Startup Timings: %s", namespace), ns.timings)
	}
}
