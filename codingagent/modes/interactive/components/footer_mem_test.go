// footer_mem_test.go — the footer MEM segment (pig-only showResourceStats
// setting): default-off gating, rendering, sampler lifecycle, formatting.
package components

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

const mem52MB = int64(52 * 1024 * 1024)

// TestFooterMemHiddenByDefault: the showResourceStats setting gates the
// render — a sampled value alone must NOT surface the segment.
func TestFooterMemHiddenByDefault(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.rssBytes.Store(mem52MB)
	t.Cleanup(func() { footer.rssBytes.Store(0) })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if strings.Contains(statsLine, "MEM") {
		t.Fatalf("stats line %q shows MEM while showResourceStats is off", statsLine)
	}
}

// TestFooterMemShownWhenEnabledAndSampled: enabled + sampled → the segment
// renders as "| MEM 52MB", separated from the usage stats.
func TestFooterMemShownWhenEnabledAndSampled(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.rssBytes.Store(mem52MB)
	t.Cleanup(func() { footer.rssBytes.Store(0) })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	for _, want := range []string{"3.0%/1.0M (auto) | MEM 52MB"} {
		if !strings.Contains(statsLine, want) {
			t.Fatalf("stats line %q missing %q", statsLine, want)
		}
	}
}

// TestFooterMemHiddenWhenEnabledButUnsampled: enabled but no sample yet
// (no procfs / poller not fired) → no segment.
func TestFooterMemHiddenWhenEnabledButUnsampled(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if strings.Contains(statsLine, "MEM") {
		t.Fatalf("stats line %q shows MEM before the first sample", statsLine)
	}
}

// TestSetShowResourceStatsLifecycle: enabling starts the sampler with an
// immediate first sample; disabling stops it and clears the value.
func TestSetShowResourceStatsLifecycle(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	t.Cleanup(footer.Dispose)
	if _, ok := readRSSBytes(); !ok {
		t.Skip("no procfs on this platform")
	}

	footer.SetShowResourceStats(true)
	footer.mu.Lock()
	pollerRunning := footer.stopRSSPoller != nil
	footer.mu.Unlock()
	if !pollerRunning {
		t.Fatal("SetShowResourceStats(true) did not start the sampler")
	}
	if footer.rssBytes.Load() <= 0 {
		t.Fatal("SetShowResourceStats(true) did not sample RSS up front")
	}

	footer.SetShowResourceStats(false)
	footer.mu.Lock()
	pollerRunning = footer.stopRSSPoller != nil
	footer.mu.Unlock()
	if pollerRunning {
		t.Fatal("SetShowResourceStats(false) did not stop the sampler")
	}
	if footer.rssBytes.Load() != 0 {
		t.Fatal("SetShowResourceStats(false) left a stale sampled value")
	}
}

// TestStartRSSPollerSamplesIntoSink: the ticker populates the sink.
func TestStartRSSPollerSamplesIntoSink(t *testing.T) {
	if _, ok := readRSSBytes(); !ok {
		t.Skip("no procfs on this platform")
	}
	var sink atomic.Int64
	stop := startRSSPoller(10*time.Millisecond, &sink)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for sink.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sink.Load() <= 0 {
		t.Fatal("poller did not sample RSS into the sink")
	}
}

// TestStartRSSPollerStopIsIdempotent: calling stop twice must not panic.
func TestStartRSSPollerStopIsIdempotent(t *testing.T) {
	var sink atomic.Int64
	stop := startRSSPoller(time.Hour, &sink)
	stop()
	stop()
}

// TestReadRSSBytesSanity: on procfs platforms the sampled RSS is a
// plausible byte count.
func TestReadRSSBytesSanity(t *testing.T) {
	b, ok := readRSSBytes()
	if !ok {
		t.Skip("no procfs on this platform")
	}
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if b < mb || b > 64*gb {
		t.Fatalf("implausible RSS %d bytes", b)
	}
}

// TestFormatMemBytes locks the display format ("52MB", not "52M").
func TestFormatMemBytes(t *testing.T) {
	const kb = 1024
	const mb = 1024 * kb
	const gb = 1024 * mb
	cases := []struct {
		in   int64
		want string
	}{
		{512 * kb, "512KB"},
		{mb - 1, "1024KB"},
		{mb, "1.0MB"},
		{8_808_038, "8.4MB"}, // 8.4 * mb
		{52 * mb, "52MB"},
		{1_288_490_189, "1.2GB"}, // 1.2 * gb
	}
	for _, c := range cases {
		if got := FormatMemBytes(c.in); got != c.want {
			t.Fatalf("FormatMemBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
