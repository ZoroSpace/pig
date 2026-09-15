// resource_stats.go — process resource sampling for the footer resource
// segment (pig-only; pi has no counterpart).
//
// The footer renders on every frame, so values must be sampled off-frame
// and cached: a syscall (let alone ReadMemStats' brief stop-the-world) per
// render would tax every keystroke. One background poller samples on a
// slow ticker into resourceStats (atomic fields; Render only reads) and
// derives the anomaly alert flags from the same stream. 0 = unsampled.
package components

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Sampling cadence and anomaly thresholds (pig-only).
const (
	// footerRSSInterval is how often the resource poller samples. RSS moves
	// slowly (per-turn growth is ~100KB scale), so 5s is plenty responsive.
	footerRSSInterval = 5 * time.Second

	// clockTicksPerSecond is Linux's _SC_CLK_TCK (USER_HZ), the unit of
	// /proc/self/stat's utime/stime fields (gopsutil assumes the same).
	clockTicksPerSecond = 100

	// Goroutine leak signals. Trend beats thresholds: gLeakStreakTicks
	// consecutive monotonic increases (~50s) catch a leak long before any
	// absolute level would, while gAbsolute catches an established leak
	// that has plateaued (any non-increase resets the streak — a healthy
	// session oscillates, a leak ratchets).
	gLeakStreakTicks = 10
	gAbsolute        = 500

	// fdThreshold: a healthy pig holds O(10) descriptors; the oauth
	// callback listener leak grew unbounded
	// (docs/fix/2026-09-12-oauth-callback-listener-leak-flaky.md).
	fdThreshold = 200

	// CPU alert requires cpuThresholdPercent sustained for
	// cpuAlertStreakTicks consecutive windows (~10s): one window of heavy
	// streaming render is not an anomaly; a spinning event loop is.
	cpuThresholdPercent = 30
	cpuAlertStreakTicks = 2
)

// resourceStats is the poller's published state. All fields are atomic:
// the poller goroutine writes, footer Render reads.
type resourceStats struct {
	rssBytes   atomic.Int64 // resident set size; 0 = unsampled/unsupported
	heapBytes  atomic.Int64 // runtime HeapAlloc (live objects)
	goroutines atomic.Int64
	fdCount    atomic.Int64 // open descriptors; 0 = unavailable (no procfs)
	cpuPercent atomic.Int64 // last window's process CPU, rounded; 0 = no window yet

	alertG   atomic.Bool
	alertFD  atomic.Bool
	alertCPU atomic.Bool
}

// zero clears every published value: the segment hides (used when the
// poller stops on procfs loss or the segment is switched off).
func (s *resourceStats) zero() {
	s.rssBytes.Store(0)
	s.heapBytes.Store(0)
	s.goroutines.Store(0)
	s.fdCount.Store(0)
	s.cpuPercent.Store(0)
	s.alertG.Store(false)
	s.alertFD.Store(false)
	s.alertCPU.Store(false)
}

// sampleStatic fills the instantaneous values (everything except the CPU
// window rate, which needs two ticks). Used on segment enable so the
// values appear on the next render instead of one interval later.
func (s *resourceStats) sampleStatic() {
	r, ok := readResourceSample()
	if !ok {
		return
	}
	s.rssBytes.Store(r.rss)
	s.heapBytes.Store(r.heap)
	s.goroutines.Store(r.goroutines)
	s.fdCount.Store(r.fds)
}

// resourceReadings is one tick's raw readings (apply is pure over these,
// so the alert logic is unit-testable without time or procfs).
type resourceReadings struct {
	rss        int64
	heap       int64
	goroutines int64
	fds        int64
	cpuTicks   int64
	cpuOK      bool
}

// readResourceSample reads everything once. ok=false means procfs is
// unavailable (RSS is the segment's gate; without it the rest is moot).
func readResourceSample() (resourceReadings, bool) {
	var r resourceReadings
	rss, ok := readRSSBytes()
	if !ok {
		return r, false
	}
	r.rss = rss
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	r.heap = int64(m.HeapAlloc)
	r.goroutines = int64(runtime.NumGoroutine())
	r.fds, _ = readFDCount()
	r.cpuTicks, r.cpuOK = readProcessCPUTicks()
	return r, true
}

// resourcePoller holds the poller goroutine's private streak state.
type resourcePoller struct {
	stats *resourceStats

	prevCPUTicks int64
	haveCPU      bool
	prevG        int64
	haveG        bool
	gStreak      int
	cpuStreak    int
}

// apply folds one tick's readings into the published state. elapsed is the
// wall time since the previous tick (the CPU rate denominator).
func (p *resourcePoller) apply(r resourceReadings, elapsed time.Duration) {
	s := p.stats
	s.rssBytes.Store(r.rss)
	s.heapBytes.Store(r.heap)
	s.goroutines.Store(r.goroutines)
	s.fdCount.Store(r.fds)

	// CPU rate over this window; the first window has no denominator pair.
	if r.cpuOK {
		if p.haveCPU && elapsed > 0 {
			percent := float64(r.cpuTicks-p.prevCPUTicks) / clockTicksPerSecond / elapsed.Seconds() * 100
			if percent < 0 {
				percent = 0 // tick accounting can straddle two reads
			}
			s.cpuPercent.Store(int64(percent + 0.5))
		}
		p.prevCPUTicks = r.cpuTicks
		p.haveCPU = true
	} else {
		p.haveCPU = false
		s.cpuPercent.Store(0)
	}

	// Goroutine trend: any non-increase resets the streak.
	if p.haveG && r.goroutines > p.prevG {
		p.gStreak++
	} else {
		p.gStreak = 0
	}
	p.prevG = r.goroutines
	p.haveG = true

	s.alertG.Store(p.gStreak >= gLeakStreakTicks || r.goroutines >= gAbsolute)
	s.alertFD.Store(r.fds >= fdThreshold)

	if c := s.cpuPercent.Load(); c >= cpuThresholdPercent {
		p.cpuStreak++
	} else {
		p.cpuStreak = 0
	}
	s.alertCPU.Store(p.cpuStreak >= cpuAlertStreakTicks)
}

// startResourcePoller samples on a ticker until the returned stop is
// called (idempotent). Stops on procfs loss: all values zero, segment
// hides. The first sample lands after one interval; callers wanting
// instant values call resourceStats.sampleStatic first.
func startResourcePoller(interval time.Duration, stats *resourceStats) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var p resourcePoller
		p.stats = stats
		last := time.Now()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				r, ok := readResourceSample()
				if !ok {
					stats.zero()
					return
				}
				elapsed := now.Sub(last)
				last = now
				p.apply(r, elapsed)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// readRSSBytes reads the resident set size from /proc/self/statm (field 2,
// resident pages) scaled by the page size.
func readRSSBytes() (int64, bool) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || pages <= 0 {
		return 0, false
	}
	return pages * int64(os.Getpagesize()), true
}

// readFDCount counts open file descriptors (/proc/self/fd entries; the
// count includes the listing handle itself — ±1 noise is irrelevant).
func readFDCount() (int64, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return int64(len(entries)), true
}

// readProcessCPUTicks reads the process's cumulative CPU time
// (utime+stime, stat fields 14/15) in clock ticks from /proc/self/stat.
// comm (field 2) may contain spaces and parens, so parsing starts after
// its closing ')'.
func readProcessCPUTicks() (int64, bool) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	s := string(data)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 > len(s) {
		return 0, false
	}
	fields := strings.Fields(s[i+2:]) // fields[0] = state (field 3)
	if len(fields) < 13 {
		return 0, false
	}
	utime, errU := strconv.ParseInt(fields[11], 10, 64) // field 14
	stime, errS := strconv.ParseInt(fields[12], 10, 64) // field 15
	if errU != nil || errS != nil {
		return 0, false
	}
	return utime + stime, true
}

// FormatMemBytes formats a byte count for the footer resource segment
// ("52MB", "8.4MB", "512KB", "1.2GB").
func FormatMemBytes(b int64) string {
	const kb = 1024
	const mb = 1024 * kb
	const gb = 1024 * mb
	switch {
	case b < mb:
		return fmt.Sprintf("%dKB", (b+kb-1)/kb)
	case b < gb:
		m := float64(b) / mb
		if m < 10 {
			return fmt.Sprintf("%.1fMB", m)
		}
		return fmt.Sprintf("%dMB", int64(m+0.5))
	default:
		return fmt.Sprintf("%.1fGB", float64(b)/gb)
	}
}
