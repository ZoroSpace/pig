//go:build darwin

// resource_stats_darwin.go — macOS samplers for the footer resource segment
// (pig-only). See resource_stats.go for the shared poller.
//
// The memory metric is phys_footprint (what Activity Monitor's "内存"
// column shows: anonymous + dirty file-backed + compressed pages charged
// to the process), not ps's RSS. RSS counts every resident page including
// the clean file-backed pages of pig's own ~39MB binary (code, embedded
// CJK dictionaries), which the footprint accounting does not charge to
// the process — empirically ps RSS 41.6MB vs footprint 23.3MB for the
// same interactive session.
//
// There is no pure-Go syscall path to phys_footprint (probed 2026-09-16):
// proc_info PIDINFO flavors expose only virtual/resident (proc_taskinfo
// has no footprint field), the raw PROC_INFO_CALL_PIDRUSAGE syscall
// returns an empty buffer (its copyout lives behind the libproc wrapper),
// and mach task_info(TASK_VM_INFO) is a MIG call requiring cgo, which
// pig's fully static build (CGO_ENABLED=0) rules out. The number
// therefore comes from spawning the system /usr/bin/footprint — Apple's
// own tool, no root for our own pid, ~15ms per sample (vs ~1-2ms for ps;
// imperceptible at the 5s cadence). Verified identical to vmmap's
// "Physical footprint" on a 50MB probe (56164880 B vs 53.8M).
package components

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// footprintPath caches the resolved tool path; LookPath on every sample
// would re-scan PATH, and a missing tool stays missing.
var footprintPath = sync.OnceValue(func() string {
	path, err := exec.LookPath("footprint")
	if err != nil {
		return ""
	}
	return path
})

// readMemBytes reads the memory charged to the process (phys_footprint)
// via /usr/bin/footprint; falls back to ps RSS when the tool is missing
// or its output format changes (older macOS, sandboxed spawns) so the
// segment degrades to the pre-2026-09-16 semantics instead of vanishing.
func readMemBytes() (int64, bool) {
	if b, ok := readFootprintBytes(); ok {
		return b, true
	}
	return readRSSBytes()
}

// readFootprintBytes spawns `footprint -f bytes <pid>` and parses the
// "Footprint: <N> B" line out of its summary.
func readFootprintBytes() (int64, bool) {
	path := footprintPath()
	if path == "" {
		return 0, false
	}
	out, err := exec.Command(path, "-f", "bytes", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0, false
	}
	return parseFootprintOutput(out)
}

// parseFootprintOutput extracts the byte count from footprint's
// "… Footprint: 56164880 B (16384 bytes per page)" line.
func parseFootprintOutput(out []byte) (int64, bool) {
	const marker = "Footprint: "
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+len(marker):])
		if len(fields) < 2 || fields[1] != "B" {
			return 0, false
		}
		b, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || b <= 0 {
			return 0, false
		}
		return b, true
	}
	return 0, false
}

// readRSSBytes reads the current resident set size by asking ps
// (`ps -o rss= -p <pid>` prints kilobytes for our own pid); the fallback
// memory sampler when /usr/bin/footprint is unavailable.
func readRSSBytes() (int64, bool) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0, false
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || kb <= 0 {
		return 0, false
	}
	return kb * 1024, true
}

// readFDCount counts open file descriptors via /dev/fd (fdesc lists the
// calling process's descriptors).
func readFDCount() (int64, bool) {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, false
	}
	return int64(len(entries)), true
}
