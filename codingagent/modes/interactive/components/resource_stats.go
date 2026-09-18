// resource_stats.go — process resource sampling for the footer resource
// segment (pig-only; pi has no counterpart).
//
// The footer renders on every frame, so values must be sampled off-frame
// and cached: a syscall (let alone a fork/exec) per render would tax every
// keystroke. One background poller samples on a slow ticker into
// resourceStats (atomic fields; Render only reads) and derives the anomaly
// alert flags from the same stream. 0 = unsampled.
//
// Platform split lives in resource_stats_linux.go (procfs),
// resource_stats_darwin.go (/usr/bin/footprint phys_footprint, ps RSS
// fallback, /dev/fd) and resource_stats_windows.go (psapi working set,
// GetProcessTimes); everything here is portable.
package components

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Sampling cadence and anomaly thresholds (pig-only).
var (
	// footerSampleInterval is how often the resource poller samples. Memory
	// moves slowly (per-turn growth is ~100KB scale), so 5s is plenty
	// responsive. A var (not const) so tests can shorten it.
	footerSampleInterval = 5 * time.Second
)

const (
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
	memBytes     atomic.Int64 // memory charged to the process (darwin: phys_footprint, matching Activity Monitor's "Memory" column; linux: statm resident); 0 = unsampled/unsupported
	goroutines   atomic.Int64
	fdCount      atomic.Int64 // open descriptors; 0 = unavailable
	cpuPercent   atomic.Int64 // last window's process CPU, rounded; 0 = no window yet (or genuinely idle)
	cpuValid     atomic.Bool  // a window rate has been computed (second sample onward); gates the real number over the CPU placeholder
	cpuSupported atomic.Bool  // the platform yields CPU readings; gates the always-on CPU item (windows stub hides it entirely)

	alertG   atomic.Bool
	alertFD  atomic.Bool
	alertCPU atomic.Bool
}

// zero clears every published value: the segment hides (used when the
// poller stops on sampler loss or the segment is switched off).
func (s *resourceStats) zero() {
	s.memBytes.Store(0)
	s.goroutines.Store(0)
	s.fdCount.Store(0)
	s.cpuPercent.Store(0)
	s.cpuValid.Store(false)
	s.cpuSupported.Store(false)
	s.alertG.Store(false)
	s.alertFD.Store(false)
	s.alertCPU.Store(false)
}

// sampleStatic fills the instantaneous values (everything except the CPU
// window rate, which needs two samples). Used on segment enable so the
// values appear on the next render instead of one interval later; the
// CPU item renders its placeholder from this first sample (cpuSupported)
// while the real rate waits for the second.
func (s *resourceStats) sampleStatic() {
	r, ok := readResourceSample()
	if !ok {
		return
	}
	s.memBytes.Store(r.mem)
	s.goroutines.Store(r.goroutines)
	s.fdCount.Store(r.fds)
	s.cpuSupported.Store(r.cpuOK)
}

// resourceReadings is one tick's raw readings (apply is pure over these,
// so the alert logic is unit-testable without time or platform probes).
type resourceReadings struct {
	mem        int64
	goroutines int64
	fds        int64
	cpuMicros  int64
	cpuOK      bool
}

// readResourceSample reads everything once. ok=false means the platform
// sampler is unavailable (RSS is the segment's gate; without it the rest
// is moot).
func readResourceSample() (resourceReadings, bool) {
	var r resourceReadings
	mem, ok := readMemBytes()
	if !ok {
		return r, false
	}
	r.mem = mem
	r.goroutines = int64(runtime.NumGoroutine())
	r.fds, _ = readFDCount()
	r.cpuMicros, r.cpuOK = readProcessCPUMicros()
	return r, true
}

// resourcePoller holds the poller goroutine's private streak state.
type resourcePoller struct {
	stats *resourceStats

	prevCPUMicros int64
	haveCPU       bool
	prevG         int64
	haveG         bool
	gStreak       int
	cpuStreak     int
}

// apply folds one tick's readings into the published state. elapsed is the
// wall time since the previous tick (the CPU rate denominator).
func (p *resourcePoller) apply(r resourceReadings, elapsed time.Duration) {
	s := p.stats
	s.memBytes.Store(r.mem)
	s.goroutines.Store(r.goroutines)
	s.fdCount.Store(r.fds)

	// CPU rate over this window; the first sample has no denominator pair.
	if r.cpuOK {
		s.cpuSupported.Store(true)
		if p.haveCPU && elapsed > 0 {
			percent := float64(r.cpuMicros-p.prevCPUMicros) / 1e6 / elapsed.Seconds() * 100
			if percent < 0 {
				percent = 0 // accounting can straddle two reads
			}
			s.cpuPercent.Store(int64(percent + 0.5))
			s.cpuValid.Store(true) // a rate exists → the footer's real CPU number may render
		}
		p.prevCPUMicros = r.cpuMicros
		p.haveCPU = true
	} else {
		p.haveCPU = false
		s.cpuPercent.Store(0)
		s.cpuValid.Store(false)
		s.cpuSupported.Store(false)
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
// called (idempotent). Stops on sampler loss: all values zero, segment
// hides. Samples once immediately before the first tick: that seed gives
// the CPU rate its baseline at t0, so the first real number lands after
// one interval (~5s); until then the footer shows the "CPU-%"
// placeholder — a process-lifetime average is not representative of
// current activity, so no rate is published before a full window exists.
// Callers additionally call resourceStats.sampleStatic so MEM and the
// placeholder render before the goroutine runs.
//
// onSample fires after every ticker sample (not the immediate seed): the
// TUI is event-driven and does not redraw while idle, so without this
// fresh values would surface only on the next keystroke. The callback is
// also what keeps the callback off the pre-Start path: the first tick
// lands well after the TUI has started.
func startResourcePoller(interval time.Duration, stats *resourceStats, onSample func()) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var p resourcePoller
		p.stats = stats
		last := time.Now()
		r, ok := readResourceSample()
		if !ok {
			stats.zero()
			return
		}
		p.apply(r, 0) // elapsed=0: seeds baselines, computes no CPU rate
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
				if onSample != nil {
					onSample()
				}
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// FormatMemBytes formats a byte count for the footer resource segment
// ("52.0MB", "8.4MB", "512KB", "1.2GB"). MB always carries one decimal
// place, matching Activity Monitor's Memory column (user request,
// 2026-09-16).
func FormatMemBytes(b int64) string {
	const kb = 1024
	const mb = 1024 * kb
	const gb = 1024 * mb
	switch {
	case b < mb:
		return fmt.Sprintf("%dKB", (b+kb-1)/kb)
	case b < gb:
		return fmt.Sprintf("%.1fMB", float64(b)/mb)
	default:
		return fmt.Sprintf("%.1fGB", float64(b)/gb)
	}
}
