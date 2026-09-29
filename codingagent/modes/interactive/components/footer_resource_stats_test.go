// footer_resource_stats_test.go — the footer resource segment (pig-only
// showResourceStats setting): MEM/HEAP display, anomaly alert gating
// (goroutines / FDs / CPU), sampler lifecycle, and formatting.
package components

import (
	"strings"
	"sync/atomic"
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
// "·"-joined, with the sampled values; the CPU item surfaces only on
// anomaly and joins the alert items after MEM (user request, 2026-09-28).
func TestFooterAlertsShownWhenFlagsSet(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	footer.res.goroutines.Store(387)
	footer.res.fdCount.Store(210)
	footer.res.cpuPercent.Store(97)
	footer.res.alertG.Store(true)
	footer.res.alertFD.Store(true)
	footer.res.alertCPU.Store(true)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	want := "MEM52.0MB·!CPU97%·!G387·!FD210"
	if !strings.Contains(statsLine, want) {
		t.Fatalf("stats line %q missing %q", statsLine, want)
	}
}

// TestFooterAlertsHiddenWhenFlagsFalse: sampled values alone never surface
// alerts — only the poller's anomaly flags do. A valid CPU rate with no
// anomaly renders nothing (user request, 2026-09-28: no always-on CPU).
func TestFooterAlertsHiddenWhenFlagsFalse(t *testing.T) {
	initTestTheme(t)
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	footer.showResourceStats = true
	footer.res.memBytes.Store(testMem)
	footer.res.goroutines.Store(387)
	footer.res.fdCount.Store(210)
	footer.res.cpuPercent.Store(97)
	t.Cleanup(func() { footer.res.zero() })
	statsLine := utils.StripAnsi(footer.Render(120)[1])
	for _, alert := range []string{"!G", "!FD", "!CPU"} {
		if strings.Contains(statsLine, alert) {
			t.Fatalf("stats line %q shows %q without anomaly flags", statsLine, alert)
		}
	}
	if strings.Contains(statsLine, "CPU") {
		t.Fatalf("stats line %q renders a CPU item outside anomaly", statsLine)
	}
	if !strings.Contains(statsLine, "| MEM52.0MB") {
		t.Fatalf("stats line %q missing %q", statsLine, "| MEM52.0MB")
	}
}

// TestFooterCPUHiddenWhenUnsupported: a platform without a CPU sampler
// never surfaces the CPU item (no rate → no anomaly possible).
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
	stop := startResourcePoller(10*time.Millisecond, &stats, nil)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for stats.goroutines.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if stats.goroutines.Load() <= 0 {
		t.Fatal("poller did not sample into the stats")
	}
}

// TestStartResourcePollerSeedsCPUBaseline: the poller's immediate first
// sample seeds the CPU baseline but computes no rate, so no rate is
// published before the first tick — the first real rate lands at the
// first tick, and nothing CPU-related can alert before a full window.
func TestStartResourcePollerSeedsCPUBaseline(t *testing.T) {
	if _, ok := readProcessCPUMicros(); !ok {
		t.Skip("getrusage unavailable on this platform")
	}
	var stats resourceStats
	stop := startResourcePoller(time.Hour, &stats, nil)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for stats.cpuPercent.Load() == 0 && !stats.alertCPU.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if stats.cpuPercent.Load() != 0 {
		t.Fatalf("cpuPercent = %d before any window rate existed", stats.cpuPercent.Load())
	}
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert fired before any window rate existed")
	}
}

// TestStartResourcePollerStopIsIdempotent: calling stop twice must not panic.
func TestStartResourcePollerStopIsIdempotent(t *testing.T) {
	var stats resourceStats
	stop := startResourcePoller(time.Hour, &stats, nil)
	stop()
	stop()
}

// TestStartResourcePollerNotifiesOnSample: onSample fires after every
// ticker sample (not the immediate seed) so the footer can wake the render
// loop — the TUI is event-driven and idle frames would otherwise keep the
// fresh values invisible until the next keystroke.
func TestStartResourcePollerNotifiesOnSample(t *testing.T) {
	if _, ok := readMemBytes(); !ok {
		t.Skip("no procfs on this platform")
	}
	var stats resourceStats
	var notifications atomic.Int32
	stop := startResourcePoller(10*time.Millisecond, &stats, func() { notifications.Add(1) })
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for notifications.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := notifications.Load(); got < 2 {
		t.Fatalf("onSample fired %d times, want at least 2 (two ticker samples)", got)
	}
}

// TestFooterResourceSampleTriggersRender: with the segment enabled, the
// poller's sample callback reaches the injected TUI as RequestRender.
func TestFooterResourceSampleTriggersRender(t *testing.T) {
	initTestTheme(t)
	if _, ok := readMemBytes(); !ok {
		t.Skip("no procfs on this platform")
	}
	footer := NewFooterComponentWithView(nil, footerUsageView(nil), createFooterData(1))
	t.Cleanup(footer.Dispose)
	fake := &fakeFooterTUI{}
	footer.SetTUI(fake)
	oldInterval := footerSampleInterval
	footerSampleInterval = 10 * time.Millisecond
	t.Cleanup(func() { footerSampleInterval = oldInterval })
	footer.SetShowResourceStats(true)
	t.Cleanup(func() { footer.SetShowResourceStats(false) })
	deadline := time.Now().Add(2 * time.Second)
	for fake.renders.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fake.renders.Load() == 0 {
		t.Fatal("poller samples did not reach the TUI as RequestRender")
	}
}

// fakeFooterTUI records render requests (FooterTUI test seam).
type fakeFooterTUI struct {
	renders atomic.Int32
}

func (f *fakeFooterTUI) RequestRender(force bool) { f.renders.Add(1) }

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
// The threshold is a single-core rate, so sustained ordinary load below it
// must stay silent (2026-09-29: 30% → 70% killed the false positives).
func TestResourcePollerCPUAlertStreak(t *testing.T) {
	var stats resourceStats
	p := resourcePoller{stats: &stats}
	// Δ per 5s window → percent: 3_000_000µs = 60%, 3_500_000µs = 70%.
	reading := func(micros int64) resourceReadings {
		return resourceReadings{mem: testMem, cpuMicros: micros, cpuOK: true}
	}

	p.apply(reading(1_000_000), 5*time.Second) // first sample: no rate yet
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert fired on the first window")
	}
	if stats.cpuPercent.Load() != 0 {
		t.Fatalf("cpuPercent = %d before any window rate existed", stats.cpuPercent.Load())
	}

	// 60% for two straight windows: below the threshold, must not alert.
	p.apply(reading(4_000_000), 5*time.Second) // 60%
	if stats.cpuPercent.Load() != 60 {
		t.Fatalf("cpuPercent = %d, want 60", stats.cpuPercent.Load())
	}
	p.apply(reading(7_000_000), 5*time.Second) // 60% again
	if stats.alertCPU.Load() {
		t.Fatalf("CPU alert fired at %d%% sustained, below the %d%% threshold", stats.cpuPercent.Load(), cpuThresholdPercent)
	}

	// 70% for two straight windows; the 60% pair must not have primed the
	// streak, so the first 70% window is still only one.
	p.apply(reading(10_500_000), 5*time.Second) // 70%
	if stats.cpuPercent.Load() != 70 {
		t.Fatalf("cpuPercent = %d, want 70", stats.cpuPercent.Load())
	}
	if stats.alertCPU.Load() {
		t.Fatal("CPU alert fired after a single hot window")
	}
	p.apply(reading(14_000_000), 5*time.Second) // 70% again
	if !stats.alertCPU.Load() {
		t.Fatal("CPU alert did not fire after two consecutive hot windows")
	}
	p.apply(reading(14_500_000), 5*time.Second) // 10%: cool
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
