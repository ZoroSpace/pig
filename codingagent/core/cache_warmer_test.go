// cache_warmer_test.go (pi: test/cache-warmer.test.ts, pi v0.86.0 #9668).
// pi drives the refresh timers with vi.useFakeTimers; pig stubs
// cacheWarmAfterFunc with a manual-fire fake clock (afterFuncClock) — each
// advance fires one armed refresh, matching advanceTimersByTimeAsync.
package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	aiProviders "github.com/ZoroSpace/pig/ai/providers"
	"github.com/ZoroSpace/pig/ai/types"
)

func warmTestModel(provider string, id string, base ai.Model, promptCache types.ModelPromptCache) ai.Model {
	model := base
	model.Provider = types.ProviderID(provider)
	model.ID = id
	model.PromptCache = promptCache
	return model
}

// warmUsage is the warm request's usage fixture (pi: warmUsage).
func warmUsage() types.Usage {
	return types.Usage{
		Input:       0,
		Output:      1,
		CacheRead:   100,
		CacheWrite:  0,
		TotalTokens: 101,
		Cost:        types.Cost{Input: 0, Output: 0, CacheRead: 0.01, CacheWrite: 0, Total: 0.01},
	}
}

// warmResponse builds the replay result (pi: response).
func warmResponse(model *ai.Model, stopReason types.StopReason) types.AssistantMessage {
	if stopReason == "" {
		stopReason = types.StopReasonLength
	}
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      warmUsage(),
		StopReason: stopReason,
		Timestamp:  0,
	}
}

// branchWithPrompt is the branch fixture (pi: branchWithPrompt).
func branchWithPrompt(promptTokens int64) []SessionEntry {
	warmModelFixtures()
	response := warmResponse(&warmAdaptiveModel, "")
	response.Usage.Output = 10
	response.Usage.CacheRead = promptTokens
	response.Usage.TotalTokens = promptTokens + 10
	return []SessionEntry{
		&SessionMessageEntry{
			EntryBase: EntryBase{Type: "message", ID: "a", Timestamp: "1970-01-01T00:00:00.000Z"},
			Message:   response,
		},
	}
}

// afterFuncClock replaces cacheWarmAfterFunc during the test (pi:
// vi.useFakeTimers + advanceTimersByTimeAsync).
type afterFuncClock struct {
	mu      sync.Mutex
	pending func()
	timer   *time.Timer
}

func newAfterFuncClock() *afterFuncClock {
	clock := &afterFuncClock{}
	clock.timer = time.AfterFunc(time.Hour, func() {})
	clock.timer.Stop()
	return clock
}

func (c *afterFuncClock) afterFunc(_ time.Duration, fn func()) *time.Timer {
	c.mu.Lock()
	c.pending = fn
	c.mu.Unlock()
	return c.timer
}

// advance fires one armed refresh (pi: advanceTimersByTimeAsync(delay) —
// async, so the refresh goroutine can park inside a pending stream).
func (c *afterFuncClock) advance() {
	c.mu.Lock()
	fn := c.pending
	c.pending = nil
	c.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

func (c *afterFuncClock) waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", desc)
}

// warmCall records one StreamSimple call (pi: calls entries).
type warmCall struct {
	model   *ai.Model
	options *ai.ModelsSimpleStreamOptions
	ctx     context.Context
}

// fakeWarmRuntime is the fakeRuntime fixture (pi: fakeRuntime).
type fakeWarmRuntime struct {
	mu            sync.Mutex
	warmer        *CacheWarmer
	calls         []warmCall
	events        []*CacheWarmingDecisionEvent
	warmedEntries []*UsageEntry
	appendUsageN  int
	appendUsageIn []*UsageEntry
	branch        []SessionEntry
	mode          CacheWarmingMode
	resultFn      func(model *ai.Model) (types.AssistantMessage, <-chan struct{})
	cancelledCtxs []context.Context
}

func (f *fakeWarmRuntime) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	f.mu.Lock()
	copied := *options
	f.calls = append(f.calls, warmCall{model: model, options: &copied, ctx: ctx})
	cancelled := ctx.Err() != nil
	if cancelled {
		f.cancelledCtxs = append(f.cancelledCtxs, ctx)
	}
	resultFn := f.resultFn
	f.mu.Unlock()
	stream := types.NewAssistantMessageEventStream()
	if cancelled {
		// pi: the replaced run's request was aborted before sending; the
		// result shape is irrelevant to the assertions.
		stream.End()
		return stream
	}
	if resultFn != nil {
		message, release := resultFn(model)
		if release != nil {
			<-release
		}
		stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Message: &message})
		stream.End()
		return stream
	}
	message := warmResponse(model, "")
	stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Message: &message})
	stream.End()
	return stream
}

func (f *fakeWarmRuntime) AppendUsage(kind string, provider string, model string, usage *types.Usage, note string) (*UsageEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendUsageN++
	entry := &UsageEntry{
		EntryBase: EntryBase{ID: fmt.Sprintf("usage-%s-%d", kind, f.appendUsageN)},
		Kind:      kind, Provider: provider, Model: model, Usage: usage, Note: note,
	}
	f.appendUsageIn = append(f.appendUsageIn, entry)
	return entry, nil
}

func (f *fakeWarmRuntime) GetBranch(string) []SessionEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.branch
}

// waitFor polls a condition (pi: vi.waitFor).
func (f *fakeWarmRuntime) waitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", desc)
}

func (f *fakeWarmRuntime) getModeValue() CacheWarmingMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

// newFakeWarmRuntime wires the warmer (pi: fakeRuntime).
func newFakeWarmRuntime(t *testing.T, branch []SessionEntry, mode CacheWarmingMode, resultFn func(model *ai.Model) (types.AssistantMessage, <-chan struct{}), decide func(event *CacheWarmingDecisionEvent) CacheWarmingAction) *fakeWarmRuntime {
	t.Helper()
	if branch == nil {
		branch = branchWithPrompt(100_000)
	}
	if mode == "" {
		mode = "idle"
	}
	f := &fakeWarmRuntime{branch: branch, mode: mode, resultFn: resultFn}
	decideFn := func(ctx context.Context, event *CacheWarmingDecisionEvent) (CacheWarmingAction, error) {
		f.mu.Lock()
		f.events = append(f.events, event)
		f.mu.Unlock()
		if decide != nil {
			return decide(event), nil
		}
		return event.Action, nil
	}
	f.warmer = NewCacheWarmer(f, f, f.getModeValue, decideFn)
	f.warmer.SetOnWarmed(func(entry *UsageEntry) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.warmedEntries = append(f.warmedEntries, entry)
	})
	t.Cleanup(func() { f.warmer.Cancel() })
	return f
}

func warmRequest(model *ai.Model, options *ai.ModelsSimpleStreamOptions) CacheWarmRequest {
	return CacheWarmRequest{Model: model, Context: &types.TranscriptContext{Messages: []types.Message{}}, Options: options}
}

func currentTrue() bool { return true }

func TestCacheWarmingEligibilityAndTiming(t *testing.T) {
	warmModelFixtures()
	// pi: "derives eligibility and timing from retention and provider behavior"
	longOptions := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong}}
	noneOptions := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}}
	envLong := &types.SimpleStreamOptions{}
	envLong.Env = types.ProviderEnv{"PI_CACHE_RETENTION": "long"}
	reasoningMedium := &types.SimpleStreamOptions{}
	reasoningMedium.Reasoning = types.ThinkingLevelMedium

	ttlCases := []struct {
		name    string
		model   *ai.Model
		options *types.SimpleStreamOptions
		want    int64
		wantOK  bool
	}{
		{"default", &warmAdaptiveModel, nil, 300_000, true},
		{"long", &warmAdaptiveModel, longOptions, 3_600_000, true},
		{"none", &warmAdaptiveModel, noneOptions, 0, false},
		{"env long", &warmAdaptiveModel, envLong, 3_600_000, true},
		{"openai long", &warmOpenAIModel, longOptions, 86_400_000, true},
		{"unknown", &warmUnknownModel, nil, 0, false},
	}
	for _, tc := range ttlCases {
		got, ok := GetPromptCacheTtlMs(tc.model, tc.options)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("%s: GetPromptCacheTtlMs = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.wantOK)
		}
	}

	delayCases := []struct {
		ttlMs  int64
		want   int64
		wantOK bool
	}{
		{300_000, 270_000, true},
		{60_000, 50_000, true},
		{10_000, 0, false},
	}
	for _, tc := range delayCases {
		got, ok := GetCacheWarmingDelayMs(tc.ttlMs)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("GetCacheWarmingDelayMs(%d) = (%d, %v), want (%d, %v)", tc.ttlMs, got, ok, tc.want, tc.wantOK)
		}
	}

	replayCases := []struct {
		name    string
		model   *ai.Model
		options *types.SimpleStreamOptions
		want    bool
	}{
		{"budget reasoning", &warmBudgetModel, reasoningMedium, false},
		{"budget no reasoning", &warmBudgetModel, nil, true},
		{"adaptive reasoning", &warmAdaptiveModel, reasoningMedium, true},
		{"openai reasoning", &warmOpenAIModel, reasoningMedium, true},
	}
	for _, tc := range replayCases {
		if got := IsReplayable(tc.model, tc.options); got != tc.want {
			t.Errorf("%s: IsReplayable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCacheWarmingReplaysProfitableRequests(t *testing.T) {
	warmModelFixtures()
	// pi: "replays profitable requests and preserves options across repeated
	// refreshes"
	clock := newAfterFuncClock()
	previous := cacheWarmAfterFunc
	cacheWarmAfterFunc = clock.afterFunc
	t.Cleanup(func() { cacheWarmAfterFunc = previous })

	f := newFakeWarmRuntime(t, nil, "", nil, nil)
	requestOptions := &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: types.SimpleStreamOptions{Reasoning: types.ThinkingLevelHigh},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(headers types.ProviderHeaders) (types.ProviderHeaders, error) {
				return headers, nil
			},
		},
	}
	requestOptions.SessionID = "s"
	f.warmer.Start(warmRequest(&warmAdaptiveModel, requestOptions), currentTrue)
	clock.advance()

	clock.waitFor(t, "first warm call", func() bool { return fakeCount(&f.mu, &f.calls) == 1 })
	clock.waitFor(t, "first warm usage", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.appendUsageN == 1 && len(f.warmedEntries) == 1
	})
	f.mu.Lock()
	call := f.calls[0]
	f.mu.Unlock()
	if call.model.ID != warmAdaptiveModel.ID {
		t.Fatalf("model = %s", call.model.ID)
	}
	if call.options.Reasoning != types.ThinkingLevelHigh || call.options.SessionID != "s" {
		t.Fatalf("options not preserved: %+v", call.options)
	}
	// pi asserts the transformHeaders callback is preserved across the
	// replay (identity in the options object).
	if call.options.TransformHeaders == nil {
		t.Fatal("transformHeaders not preserved")
	}
	if call.options.MaxTokens == nil || *call.options.MaxTokens != 1 {
		t.Fatalf("maxTokens = %v, want 1", call.options.MaxTokens)
	}
	if call.options.MaxRetries == nil || *call.options.MaxRetries != 0 {
		t.Fatalf("maxRetries = %v, want 0", call.options.MaxRetries)
	}
	f.mu.Lock()
	event := f.events[0]
	f.mu.Unlock()
	if event.Type != "cache_warming_decision" || event.ContinuationProbability != 1 || event.Action != CacheWarmingActionWarm {
		t.Fatalf("event = %+v", event)
	}
	if diff := absDiff(event.MissCost, 0.575); diff > 1e-9 {
		t.Fatalf("missCost = %v, want 0.575", event.MissCost)
	}
	if diff := absDiff(event.WarmCost, 0.050025); diff > 1e-9 {
		t.Fatalf("warmCost = %v, want 0.050025", event.WarmCost)
	}
	f.mu.Lock()
	if f.appendUsageN != 1 || f.appendUsageIn[0].Kind != "cache_warm" || f.appendUsageIn[0].Provider != string(warmAdaptiveModel.Provider) || f.appendUsageIn[0].Model != warmAdaptiveModel.ID {
		t.Fatalf("appendUsage = %+v", f.appendUsageIn)
	}
	if len(f.warmedEntries) != 1 || f.warmedEntries[0] != f.appendUsageIn[0] {
		t.Fatalf("warmedEntries = %+v, want the appendUsage result", f.warmedEntries)
	}
	f.mu.Unlock()

	clock.advance()
	clock.waitFor(t, "second warm call", func() bool { return fakeCount(&f.mu, &f.calls) == 2 })
}

// fakeCount reads a slice length under the fake's mutex.
func fakeCount(mu *sync.Mutex, slice *[]warmCall) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*slice)
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

// warmModelFixtures resolve the catalog models the pi suite uses (pi:
// getBuiltinModel("anthropic", "claude-opus-4-6") etc.). panics on a missing
// catalog entry — the sync.Once keeps them lazy for parallel test binaries.
var (
	warmModelsOnce    sync.Once
	warmAdaptiveModel ai.Model
	warmBudgetModel   ai.Model
	warmOpenAIModel   ai.Model
	warmUnknownModel  ai.Model
)

func warmModelFixtures() {
	warmModelsOnce.Do(func() {
		warmAdaptiveModel = mustWarmModel("anthropic", "claude-opus-4-6", types.ModelPromptCache{
			types.CacheRetentionShort: 300, types.CacheRetentionLong: 3600,
		})
		warmBudgetModel = mustWarmModel("anthropic", "claude-sonnet-4-5", types.ModelPromptCache{
			types.CacheRetentionShort: 300, types.CacheRetentionLong: 3600,
		})
		warmOpenAIModel = mustWarmModel("openai", "gpt-5", types.ModelPromptCache{
			types.CacheRetentionShort: 300, types.CacheRetentionLong: 86_400,
		})
		warmUnknownModel = warmAdaptiveModel
		warmUnknownModel.PromptCache = nil
	})
}

func mustWarmModel(provider string, id string, promptCache types.ModelPromptCache) ai.Model {
	model, ok := aiProviders.GetBuiltinModel(aiProviders.BuiltinProvider(provider), id)
	if !ok {
		panic("builtin model " + provider + "/" + id + " missing from catalog")
	}
	model.PromptCache = promptCache
	return model
}

func TestCacheWarmingAppliesEconomicDecisionsAndExtensionOverrides(t *testing.T) {
	warmModelFixtures()
	// pi: "applies economic decisions and extension overrides"
	clock := newAfterFuncClock()
	previous := cacheWarmAfterFunc
	cacheWarmAfterFunc = clock.afterFunc
	t.Cleanup(func() { cacheWarmAfterFunc = previous })

	unprofitable := newFakeWarmRuntime(t, branchWithPrompt(5_000), "", nil, nil)
	unprofitable.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	clock.advance()
	clock.waitFor(t, "unprofitable refresh ran", func() bool {
		unprofitable.mu.Lock()
		calls := len(unprofitable.calls)
		unprofitable.mu.Unlock()
		// Never hold f.mu while reading the warmer status: the refresh
		// goroutine takes f.mu (AppendUsage/GetBranch) before w.mu.
		return calls == 0 && unprofitable.warmer.GetStatus().State == "inactive"
	})
	status := unprofitable.warmer.GetStatus()
	if status.State != "inactive" || status.ExtensionOverride {
		t.Fatalf("status = %+v", status)
	}
	if status.Decision == nil || status.Decision.Action != CacheWarmingActionStop || !status.Decision.EconomicsAvailable {
		t.Fatalf("decision = %+v", status.Decision)
	}

	forced := newFakeWarmRuntime(t, branchWithPrompt(5_000), "", nil, func(*CacheWarmingDecisionEvent) CacheWarmingAction {
		return CacheWarmingActionWarm
	})
	forced.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	clock.advance()
	clock.waitFor(t, "forced warm entry", func() bool {
		forced.mu.Lock()
		calls, entries := len(forced.calls), len(forced.warmedEntries)
		forced.mu.Unlock()
		return calls == 1 && entries == 1
	})
	forced.mu.Lock()
	if len(forced.warmedEntries) != 1 || forced.warmedEntries[0].Note != "extension override" {
		t.Fatalf("warmedEntries = %+v", forced.warmedEntries)
	}
	forced.mu.Unlock()

	vetoed := newFakeWarmRuntime(t, nil, "", nil, func(*CacheWarmingDecisionEvent) CacheWarmingAction {
		return CacheWarmingActionStop
	})
	vetoed.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	clock.advance()
	clock.waitFor(t, "vetoed refresh ran", func() bool {
		// w.mu is taken outside f.mu (refresh holds w.mu while reading mode).
		return vetoed.warmer.GetStatus().ExtensionOverride
	})
	vetoedStatus := vetoed.warmer.GetStatus()
	if vetoedStatus.State != "inactive" || !vetoedStatus.ExtensionOverride {
		t.Fatalf("status = %+v", vetoedStatus)
	}

	unavailable := newFakeWarmRuntime(t, branchWithPrompt(0), "", nil, nil)
	unavailable.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	status = unavailable.warmer.GetStatus()
	if status.State != "inactive" || status.Reason != "cache economics unavailable" {
		t.Fatalf("status = %+v", status)
	}
	clock.advance()
	time.Sleep(10 * time.Millisecond)
	unavailable.mu.Lock()
	calls := len(unavailable.calls)
	unavailable.mu.Unlock()
	if calls != 0 {
		t.Fatalf("unavailable refresh sent %d calls", calls)
	}
}

func TestCacheWarmingStopsForUnsupportedRequests(t *testing.T) {
	warmModelFixtures()
	// pi: "stops for unsupported requests, context changes, and mode changes"
	clock := newAfterFuncClock()
	previous := cacheWarmAfterFunc
	cacheWarmAfterFunc = clock.afterFunc
	t.Cleanup(func() { cacheWarmAfterFunc = previous })

	unsupported := newFakeWarmRuntime(t, nil, "", nil, nil)

	unsupported.mode = "off"
	unsupported.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	if reason := unsupported.warmer.GetStatus().Reason; reason != "cache warming disabled" {
		t.Fatalf("reason = %q", reason)
	}

	unsupported.mode = "idle"
	unsupported.warmer.Start(warmRequest(&warmUnknownModel, nil), currentTrue)
	if reason := unsupported.warmer.GetStatus().Reason; reason != "cache lifetime unavailable" {
		t.Fatalf("reason = %q", reason)
	}

	unsupported.warmer.Start(warmRequest(&warmBudgetModel, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: types.SimpleStreamOptions{Reasoning: types.ThinkingLevelHigh},
	}), currentTrue)
	if reason := unsupported.warmer.GetStatus().Reason; reason != "request cannot be replayed safely" {
		t.Fatalf("reason = %q", reason)
	}

	stillCurrent := true
	unsupported.warmer.Start(warmRequest(&warmAdaptiveModel, nil), func() bool { return stillCurrent })
	stillCurrent = false
	if reason := unsupported.warmer.GetStatus().Reason; reason != "conversation context changed" {
		t.Fatalf("reason = %q", reason)
	}
	clock.advance()
	time.Sleep(10 * time.Millisecond)
	unsupported.mu.Lock()
	if len(unsupported.calls) != 0 {
		t.Fatalf("stale run sent %d calls", len(unsupported.calls))
	}
	unsupported.mu.Unlock()

	unsupported.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	unsupported.mode = "off"
	clock.advance()
	time.Sleep(10 * time.Millisecond)
	unsupported.mu.Lock()
	if len(unsupported.calls) != 0 {
		t.Fatalf("disabled-mode run sent %d calls", len(unsupported.calls))
	}
	unsupported.mu.Unlock()

	streaming := newFakeWarmRuntime(t, branchWithPrompt(400_000), "streaming", nil, nil)
	streaming.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	streaming.warmer.OnAgentSettled()
	if reason := streaming.warmer.GetStatus().Reason; reason != "agent run settled" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestCacheWarmingAbortsReplacedRequests(t *testing.T) {
	warmModelFixtures()
	// pi: "aborts replaced requests and does not record failed refreshes"
	clock := newAfterFuncClock()
	previous := cacheWarmAfterFunc
	cacheWarmAfterFunc = clock.afterFunc
	t.Cleanup(func() { cacheWarmAfterFunc = previous })

	release := make(chan struct{})
	pending := newFakeWarmRuntime(t, nil, "", func(model *ai.Model) (types.AssistantMessage, <-chan struct{}) {
		return warmResponse(model, ""), release
	}, nil)
	pending.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	clock.advance()
	pending.waitFor(t, "first warm call", func() bool { pending.mu.Lock(); defer pending.mu.Unlock(); return len(pending.calls) == 1 })
	pending.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	pending.mu.Lock()
	firstCtx := pending.calls[0].ctx
	pending.mu.Unlock()
	if firstCtx == nil || firstCtx.Err() == nil {
		t.Fatalf("replaced request was not aborted")
	}
	close(release)
	pending.warmer.Cancel()
	time.Sleep(20 * time.Millisecond)
	pending.mu.Lock()
	if len(pending.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (released refresh must not reschedule)", len(pending.calls))
	}
	if pending.appendUsageN != 0 {
		t.Fatalf("replaced refresh recorded usage")
	}
	pending.mu.Unlock()

	failed := newFakeWarmRuntime(t, nil, "", func(model *ai.Model) (types.AssistantMessage, <-chan struct{}) {
		return warmResponse(model, types.StopReasonError), nil
	}, nil)
	failed.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	clock.advance()
	failed.waitFor(t, "failed refresh ran", func() bool { failed.mu.Lock(); defer failed.mu.Unlock(); return len(failed.calls) == 1 })
	time.Sleep(10 * time.Millisecond)
	failed.mu.Lock()
	if failed.appendUsageN != 0 {
		t.Fatalf("failed refresh recorded usage")
	}
	failed.mu.Unlock()
}

func TestCacheWarmingFormatsStatusAndUsage(t *testing.T) {
	warmModelFixtures()
	// pi: "formats status and usage entries"
	decision := &CacheWarmingDecision{
		Phase:                   "idle",
		WarmCost:                0.013,
		MissCost:                0.621,
		ContinuationProbability: 0.6,
		ExpectedSavings:         0.36,
		EconomicsAvailable:      true,
		Action:                  CacheWarmingActionWarm,
	}
	got := FormatCacheWarmingStatus(CacheWarmingStatus{State: "scheduled", NextWarmAt: 222_000, Decision: decision}, 0)
	want := "Decision in 3m 42s (60% continuation probability, expected savings $0.360 >= $0.050 -> warm)"
	if got != want {
		t.Fatalf("FormatCacheWarmingStatus = %q, want %q", got, want)
	}

	usage := warmUsage()
	usage.Cost = types.Cost{Input: 0.00004, Output: 0.00005, CacheRead: 0.02940725, CacheWrite: 0, Total: 0.02949725}
	manager, err := InMemorySession("", nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manager.AppendUsage("cache_warm", string(warmAdaptiveModel.Provider), warmAdaptiveModel.ID, &usage, "extension override")
	if err != nil {
		t.Fatal(err)
	}
	usageText := FormatCacheWarmingUsage(entry)
	if usageText != "Cache warmed (extension override): $0.029497" {
		t.Fatalf("FormatCacheWarmingUsage = %q", usageText)
	}
}

func TestExtensionRunnerEmitCacheWarmingDecisionUsesLastOverride(t *testing.T) {
	// pi: "uses the last extension override"
	makeExt := func(path string, action CacheWarmingAction) *Extension {
		return &Extension{
			Path: path,
			Handlers: map[string][]ExtensionHandler{"cache_warming_decision": {func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
				return &CacheWarmingDecisionEventResult{Action: action}, nil
			}}},
		}
	}
	runner := NewExtensionRunner(
		[]*Extension{makeExt("warm", CacheWarmingActionWarm), makeExt("stop", CacheWarmingActionStop)},
		CreateExtensionRuntime(), "/tmp", &SessionManager{}, &ModelRegistry{},
	)
	t.Cleanup(func() { runner.Invalidate("test done") })
	action := runner.EmitCacheWarmingDecision(context.Background(), &CacheWarmingDecisionEvent{
		Type:                    "cache_warming_decision",
		WarmCost:                0.05,
		MissCost:                0.5,
		ContinuationProbability: 0.15,
		Action:                  CacheWarmingActionWarm,
	})
	if action != CacheWarmingActionStop {
		t.Fatalf("action = %v, want stop", action)
	}
}

// pi: "does not issue refreshes after their safe deadline" — a timer can run
// late after sleep or event-loop blockage; the late refresh is skipped and
// the run stopped (3390bd936).
func TestCacheWarmingSkipsLateRefreshes(t *testing.T) {
	warmModelFixtures()
	var now atomic.Int64
	f := newFakeWarmRuntime(t, nil, "", nil, nil)
	f.warmer.nowFn = func() time.Time { return time.UnixMilli(now.Load()) }
	f.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	run := f.warmer.run
	if run == nil {
		t.Fatal("expected an active cache-warming run")
	}

	// A five-minute cache is scheduled for 4m30s and retains 15 seconds of
	// the 30-second expiry margin. Simulate a timer delayed by sleep.
	now.Store(285_001)
	f.warmer.refresh(run)

	if calls := fakeCount(&f.mu, &f.calls); calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
	status := f.warmer.GetStatus()
	if status.State != "inactive" || status.Reason != "cache refresh deadline missed" {
		t.Fatalf("status = %+v, want inactive / cache refresh deadline missed", status)
	}
}

// pi: "rechecks the deadline after an extension decision" — the extension
// callback can itself burn time; the post-decision deadline check catches it.
func TestCacheWarmingRechecksDeadlineAfterExtensionDecision(t *testing.T) {
	warmModelFixtures()
	var now atomic.Int64
	f := newFakeWarmRuntime(t, nil, "", nil, func(event *CacheWarmingDecisionEvent) CacheWarmingAction {
		now.Store(285_001)
		return CacheWarmingActionWarm
	})
	f.warmer.nowFn = func() time.Time { return time.UnixMilli(now.Load()) }
	f.warmer.Start(warmRequest(&warmAdaptiveModel, nil), currentTrue)
	run := f.warmer.run
	if run == nil {
		t.Fatal("expected an active cache-warming run")
	}

	f.warmer.refresh(run)

	if calls := fakeCount(&f.mu, &f.calls); calls != 0 {
		t.Fatalf("calls = %d, want 0", calls)
	}
	status := f.warmer.GetStatus()
	if status.State != "inactive" || status.Reason != "cache refresh deadline missed" {
		t.Fatalf("status = %+v, want inactive / cache refresh deadline missed", status)
	}
}
