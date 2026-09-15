// rss.go — process RSS sampling for the footer MEM segment.
//
// The footer renders on every frame, so the value must be sampled
// off-frame and cached: reading procfs per render would add a syscall to
// every keystroke. The sampler stores the resident set size in an atomic;
// 0 means "not sampled" (footer hides the segment — also the case on
// platforms without procfs).
package components

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// footerRSSInterval is how often the RSS sampler reads procfs. RSS moves
// slowly (per-turn growth is ~100KB scale), so 5s is plenty responsive.
const footerRSSInterval = 5 * time.Second

// startRSSPoller samples the process RSS on a ticker into sink until the
// returned stop is called. The first sample lands after one interval, not
// immediately (callers that need an instant value read readRSSBytes once
// before starting the poller). Stops on read failure (procfs unavailable,
// e.g. non-Linux) — the stale/zero value is then simply never updated.
func startRSSPoller(interval time.Duration, sink *atomic.Int64) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				b, ok := readRSSBytes()
				if !ok {
					return
				}
				sink.Store(b)
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

// FormatMemBytes formats a byte count for the footer MEM segment
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
