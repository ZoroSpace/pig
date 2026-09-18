// footer_resource_stats_test.go — the footer resource segment (pig-only
// showResourceStats setting): MEM/HEAP display, anomaly alert gating
// (goroutines / FDs / CPU), sampler lifecycle, and formatting.
package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

const testMem = int64(52 * 1024 * 1024)

// TestFooterMemHiddenByDefault: the showResourceStats setting gates the
// render — a sampled value alone must NOT surface the segment.
func TestFooterMemHiddenByDefault(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.res.memBytes.Store(testMem)
	t.Cleanup(func() { footer.res.zero() })
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
	footer.res.memBytes.Store(testMem)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if !strings.Contains(statsLine, "3.0%/1.0M (auto) | MEM52.0MB") {
		t.Fatalf("stats line %q missing %q", statsLine, "3.0%/1.0M (auto) | MEM52.0MB")
	}
}

// TestFooterMemHiddenWhenEnabledButUnsampled: enabled but no sample yet
// (poller not fired) → no segment.
func TestFooterMemHiddenWhenEnabledButUnsampled(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if strings.Contains(statsLine, "MEM") {
		t.Fatalf("stats line %q shows MEM before the first sample", statsLine)
	}
}

// TestFooterAlertsShownWhenFlagsSet: alert items render warning-colored,
// "·"-joined, with the sampled values; the always-on CPU item sits before
// MEM and takes its "!CPU" alert form (user request, 2026-09-16).
func TestFooterAlertsShownWhenFlagsSet(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	footer.res.goroutines.Store(387)
	footer.res.fdCount.Store(210)
	footer.res.cpuPercent.Store(97)
	footer.res.cpuValid.Store(true)
	footer.res.cpuSupported.Store(true)
	footer.res.alertG.Store(true)
	footer.res.alertFD.Store(true)
	footer.res.alertCPU.Store(true)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	want := "!CPU97%·MEM52.0MB·!G387·!FD210"
	if !strings.Contains(statsLine, want) {
		t.Fatalf("stats line %q missing %q", statsLine, want)
	}
}

// TestFooterAlertsHiddenWhenFlagsFalse: sampled values alone never surface
// alerts — only the poller's anomaly flags do. The always-on CPU renders
// plain ("CPU97%") when its alert flag is false.
func TestFooterAlertsHiddenWhenFlagsFalse(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	footer.res.goroutines.Store(387)
	footer.res.fdCount.Store(210)
	footer.res.cpuPercent.Store(97)
	footer.res.cpuValid.Store(true)
	footer.res.cpuSupported.Store(true)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	for _, alert := range []string{"!G", "!FD", "!CPU"} {
		if strings.Contains(statsLine, alert) {
			t.Fatalf("stats line %q shows %q without anomaly flags", statsLine, alert)
		}
	}
	if !strings.Contains(statsLine, "CPU97%·MEM52.0MB") {
		t.Fatalf("stats line %q missing always-on %q", statsLine, "CPU97%·MEM52.0MB")
	}
}

// TestFooterCPUPlaceholderBeforeFirstWindow: the CPU rate needs a
// denominator pair, so until the first window rate exists (cpuValid) the
// always-on item renders the "CPU-%" placeholder — never a stale percent.
// Without a platform sampler (cpuSupported false) the item hides entirely
// rather than showing a permanent placeholder.
func TestFooterCPUPlaceholderBeforeFirstWindow(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	footer.res.cpuSupported.Store(true)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if !strings.Contains(statsLine, "CPU-%·MEM52.0MB") {
		t.Fatalf("stats line %q missing %q", statsLine, "CPU-%·MEM52.0MB")
	}
	if strings.Contains(statsLine, "CPU0%") || strings.Contains(statsLine, "CPU97%") {
		t.Fatalf("stats line %q shows a rate before the first window", statsLine)
	}
}

// TestFooterCPUHiddenWhenUnsupported: a platform without a CPU sampler
// must not render a permanent placeholder.
func TestFooterCPUHiddenWhenUnsupported(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	if strings.Contains(statsLine, "CPU") {
		t.Fatalf("stats line %q shows CPU without a platform sampler", statsLine)
	}
	if !strings.Contains(statsLine, "| MEM52.0MB") {
		t.Fatalf("stats line %q missing %q", statsLine, "| MEM52.0MB")
	}
}

// TestSetShowResourceStatsLifecycle: enabling starts the poller with an
// immediate static sample; disabling stops it and clears every value.
func TestSetShowResourceStatsLifecycle(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	t.Cleanup(footer.Dispose)
	if _, ok := readMemBytes(); !ok {
		t.Skip("no procfs on this platform")
	}

	footer.SetShowResourceStats(true)
	footer.mu.Lock()
	pollerRunning := footer.stopResourcePoller != nil
	footer.mu.Unlock()
	if !pollerRunning {
		t.Fatal("SetShowResourceStats(true) did not start the poller")
	}
	if footer.res.memBytes.Load() <= 0 {
		t.Fatal("SetShowResourceStats(true) did not sample memory up front")
	}
	if footer.res.goroutines.Load() <= 0 {
		t.Fatal("SetShowResourceStats(true) did not sample the goroutine count")
	}

	footer.SetShowResourceStats(false)
	footer.mu.Lock()
	pollerRunning = footer.stopResourcePoller != nil
	footer.mu.Unlock()
	if pollerRunning {
		t.Fatal("SetShowResourceStats(false) did not stop the poller")
	}
	if footer.res.memBytes.Load() != 0 || footer.res.alertG.Load() {
		t.Fatal("SetShowResourceStats(false) left stale sampled state")
	}
}

// TestStartResourcePollerSamplesIntoSink: the ticker populates the stats.
func TestStartResourcePollerSamplesIntoSink(t *testing.T) {
	if _, ok := readMemBytes(); !ok {
		t.Skip("no procfs on this platform")
	}
	var stats resourceStats
	stop := startResourcePoller(10*time.Millisecond, &stats)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for stats.goroutines.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if stats.goroutines.Load() <= 0 {
		t.Fatal("poller did not sample into the stats")
	}
}

// TestStartResourcePollerCPUPlaceholderFirstSample: the poller's immediate
// first sample seeds the CPU baseline but computes no rate, so the item is
// in its placeholder state right after enable (cpuSupported set, cpuValid
// clear) — the first real number lands at the first tick.
func TestStartResourcePollerCPUPlaceholderFirstSample(t *testing.T) {
	if _, ok := readProcessCPUMicros(); !ok {
		t.Skip("getrusage unavailable on this platform")
	}
	var stats resourceStats
	stop := startResourcePoller(time.Hour, &stats)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for !stats.cpuSupported.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !stats.cpuSupported.Load() {
		t.Fatal("cpuSupported not set by the poller's immediate first sample")
	}
	if stats.cpuValid.Load() {
		t.Fatal("cpuValid set before any window rate existed")
	}
}

// TestStartResourcePollerStopIsIdempotent: calling stop twice must not panic.
func TestStartResourcePollerStopIsIdempotent(t *testing.T) {
	var stats resourceStats
	stop := startResourcePoller(time.Hour, &stats)
	stop()
	stop()
}

// TestReadMemBytesSanity: on procfs platforms the sampled memory is plausible.
func TestReadMemBytesSanity(t *testing.T) {
	b, ok := readMemBytes()
	if !ok {
		t.Skip("no procfs on this platform")
	}
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if b < mb || b > 64*gb {
		t.Fatalf("implausible memory sample %d bytes", b)
	}
}

// TestReadProcessCPUMicrosSanity: cumulative CPU time is positive and
// consistent across two reads (monotonic).
func TestReadProcessCPUMicrosSanity(t *testing.T) {
	a, ok := readProcessCPUMicros()
	if !ok {
		t.Skip("getrusage unavailable on this platform")
	}
	b, ok := readProcessCPUMicros()
	if !ok || b < a {
		t.Fatalf("CPU time not monotonic: %dµs then %dµs (ok=%v)", a, b, ok)
	}
}

// TestReadFDCountSanity: at least the procfs listing handle itself.
func TestReadFDCountSanity(t *testing.T) {
	n, ok := readFDCount()
	if !ok {
		t.Skip("no procfs on this platform")
	}
	if n < 1 {
		t.Fatalf("implausible FD count %d", n)
	}
}

// TestResourcePollerGoroutineStreak: the alert fires on gLeakStreakTicks
// consecutive monotonic increases, resets on any non-increase, and the
// absolute threshold catches a plateaued leak immediately.
func TestResourcePollerGoroutineStreak(t *testing.T) {
	var stats resourceStats
	p := resourcePoller{stats: &stats}
	readings := func(g int64) resourceReadings {
		return resourceReadings{mem: testMem, goroutines: g, fds: 10}
	}

	// The first sample seeds the baseline (no streak); 9 further increases
	// stay below gLeakStreakTicks.
	for i := int64(0); i < 10; i++ {
		p.apply(readings(100+i), 5*time.Second)
	}
	if stats.alertG.Load() {
		t.Fatalf("alert fired after 9 increases, want only at %d", gLeakStreakTicks)
	}
	// 10th increase → streak reaches gLeakStreakTicks.
	p.apply(readings(110), 5*time.Second)
	if !stats.alertG.Load() {
		t.Fatal("alert did not fire after 10 consecutive increases")
	}
	// Any non-increase resets the trend (plateau = no longer growing).
	p.apply(readings(109), 5*time.Second)
	if stats.alertG.Load() {
		t.Fatal("alert survived a plateaued goroutine count")
	}
	// Absolute threshold catches a plateaued leak immediately.
	p.apply(readings(gAbsolute), 5*time.Second)
	if !stats.alertG.Load() {
		t.Fatal("absolute goroutine threshold did not fire")
	}
}

// TestResourcePollerCPUAlertStreak: the alert needs cpuThresholdPercent for
// cpuAlertStreakTicks consecutive windows; the first window has no rate.
func TestResourcePollerCPUAlertStreak(t *testing.T) {
	var stats resourceStats
	p := resourcePoller{stats: &stats}
	reading := func(micros int64) resourceReadings {
		// Δ1_500_000µs per 5s window = 1.5s CPU / 5s = 30%.
		return resourceReadings{mem: testMem, cpuMicros: micros, cpuOK: true}
	}

	p.apply(reading(1_000_000), 5*time.Second) // first sample: no rate yet
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert fired on the first window")
	}
	if !stats.cpuSupported.Load() {
		t.Fatal("cpuSupported not set on a cpuOK reading")
	}
	if stats.cpuValid.Load() {
		t.Fatal("cpuValid set before any window rate existed")
	}
	p.apply(reading(2_500_000), 5*time.Second) // 30%
	if stats.cpuPercent.Load() != 30 {
		t.Fatalf("cpuPercent = %d, want 30", stats.cpuPercent.Load())
	}
	if !stats.cpuValid.Load() {
		t.Fatal("cpuValid not set after the first window rate")
	}
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert fired after a single hot window")
	}
	p.apply(reading(4_000_000), 5*time.Second) // 30% again
	if !stats.alertCPU.Load() {
		t.Fatal("CPU alert did not fire after two consecutive hot windows")
	}
	p.apply(reading(4_500_000), 5*time.Second) // 10%: cool
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert survived a cool window")
	}
}

// TestResourcePollerFDAlert: pure absolute threshold.
func TestResourcePollerFDAlert(t *testing.T) {
	var stats resourceStats
	p := resourcePoller{stats: &stats}
	p.apply(resourceReadings{mem: testMem, fds: fdThreshold - 10}, 5*time.Second)
	if stats.alertFD.Load() {
		t.Fatal("FD alert fired below the threshold")
	}
	p.apply(resourceReadings{mem: testMem, fds: fdThreshold + 10}, 5*time.Second)
	if !stats.alertFD.Load() {
		t.Fatal("FD alert did not fire above the threshold")
	}
	// Unavailable (0, no procfs) never alerts.
	p.apply(resourceReadings{mem: testMem}, 5*time.Second)
	if stats.alertFD.Load() {
		t.Fatal("FD alert fired with no FD reading")
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
		{52 * mb, "52.0MB"},
		{1_288_490_189, "1.2GB"}, // 1.2 * gb
	}
	for _, c := range cases {
		if got := FormatMemBytes(c.in); got != c.want {
			t.Fatalf("FormatMemBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
