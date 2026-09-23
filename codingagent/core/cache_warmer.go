// pi: src/core/cache-warmer.ts (new in v0.86.0) — keeps one prompt cache
// entry alive by re-sending its request with a one-token output cap before
// the entry expires.
//
// Segment deviations:
//   - AbortController → context.WithCancel; timers → time.AfterFunc.
//     pi's run.timer.unref?.() has no Go equivalent: AfterFunc timers do not
//     keep a Go process alive at exit.
//   - pi's async decide callback → sync callback with ctx; a returned error
//     falls back to pi's own decision (pi: try/catch around await).
//   - status/decision getters are Get* methods (Go has no property getters).
//   - pig's usage entries carry Timestamp as an ISO string on EntryBase;
//     pi's UsageEntry.timestamp is likewise a string.
package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

// maxWarmingAgeMs: streaming warming never continues past this long after
// the real request that started it (pi: MAX_WARMING_AGE_MS).
const maxWarmingAgeMs = int64(60 * 60_000)

// maxIdleWarmingAgeMs: idle warming uses a shorter horizon because
// continuation estimates become less reliable with age (pi:
// MAX_IDLE_WARMING_AGE_MS).
const maxIdleWarmingAgeMs = int64(30 * 60_000)

// cacheWarmingMinimumExpectedSavings: a refresh is sent only when it is
// expected to save at least this many dollars (pi:
// CACHE_WARMING_MINIMUM_EXPECTED_SAVINGS).
const cacheWarmingMinimumExpectedSavings = 0.05

// idleContinuationProbability: chance that a real request arrives before the
// cache entry expires while the agent sits idle. Measured from pi's own
// usage; per-session estimates were not better than this constant (pi:
// IDLE_CONTINUATION_PROBABILITY).
const idleContinuationProbability = 0.15

// GetCacheWarmingDelayMs refreshes at 90% of the TTL while preserving at
// least ten seconds of margin (pi: getCacheWarmingDelayMs). ok=false when
// the TTL is too short.
func GetCacheWarmingDelayMs(ttlMs int64) (int64, bool) {
	if ttlMs <= 10_000 {
		return 0, false
	}
	// Math.floor(Math.min(ttlMs * 0.9, ttlMs - 10_000))
	delay := ttlMs * 9 / 10
	if alt := ttlMs - 10_000; alt < delay {
		delay = alt
	}
	if delay < 1 {
		delay = 1
	}
	return delay, true
}

// GetPromptCacheTtlMs returns the lifetime of the prompt cache entry a
// request writes, from the model's promptCache tier for the retention the
// request used (pi: getPromptCacheTtlMs). ok=false when the model has no
// lifetime for that tier or caching is off.
func GetPromptCacheTtlMs(model *ai.Model, options *types.SimpleStreamOptions) (int64, bool) {
	retention := types.CacheRetentionShort
	if options != nil && options.CacheRetention != "" {
		retention = options.CacheRetention
	} else if options != nil && aiutils.GetProviderEnvValue("PI_CACHE_RETENTION", options.Env) == "long" {
		retention = types.CacheRetentionLong
	}
	if retention == types.CacheRetentionNone {
		return 0, false
	}
	seconds, ok := model.PromptCache[retention]
	if !ok {
		return 0, false
	}
	return seconds * 1000, true
}

// IsReplayable reports whether replaying the request with a one-token output
// cap leaves its cache entry untouched (pi: isReplayable). Anthropic's
// budget-based thinking (Claude models without adaptive thinking) derives
// budget_tokens from max_tokens; the replay would get a different budget,
// which Anthropic keys the message cache on, and the model could still think
// for thousands of tokens.
func IsReplayable(model *ai.Model, options *types.SimpleStreamOptions) bool {
	if options == nil || options.Reasoning == "" {
		return true
	}
	if model.API != types.ApiAnthropicMessages {
		return true
	}
	if compat, ok := model.Compat.(*types.AnthropicMessagesCompat); ok {
		return compat != nil && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking
	}
	return false
}

// lastPromptTokens returns the prompt size of the most recent real request
// on the branch, as reported by the provider (pi: lastPromptTokens).
func lastPromptTokens(entries []SessionEntry) int64 {
	for index := len(entries) - 1; index >= 0; index-- {
		messageEntry, ok := entries[index].(*SessionMessageEntry)
		if !ok || messageEntry.Message == nil {
			continue
		}
		if assistant, ok := messageEntry.Message.(ai.AssistantMessage); ok {
			return assistant.Usage.Input + assistant.Usage.CacheRead + assistant.Usage.CacheWrite
		}
		if assistant, ok := messageEntry.Message.(*ai.AssistantMessage); ok {
			return assistant.Usage.Input + assistant.Usage.CacheRead + assistant.Usage.CacheWrite
		}
	}
	return 0
}

// cacheWarmPrice prices the given token amounts on the model (pi: price).
func cacheWarmPrice(model *ai.Model, input, output, cacheRead, cacheWrite int64) float64 {
	usage := types.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + output + cacheRead + cacheWrite,
		Cost: types.Cost{
			Input:      0,
			Output:     0,
			CacheRead:  0,
			CacheWrite: 0,
			Total:      0,
		},
	}
	return ai.CalculateCost(model, &usage).Total
}

// CacheWarmingAction is pi's warm-or-stop decision outcome (pi:
// CacheWarmingAction).
type CacheWarmingAction string

const (
	CacheWarmingActionWarm CacheWarmingAction = "warm"
	CacheWarmingActionStop CacheWarmingAction = "stop"
)

// CacheWarmingDecision holds the inputs and outcome of one warm-or-stop
// decision, as shown by /session (pi: CacheWarmingDecision).
type CacheWarmingDecision struct {
	// Phase is "streaming" while the agent run that sent the request is
	// still active.
	Phase string
	// WarmCost is the price of this refresh: a cache read of the prompt plus
	// one output token.
	WarmCost float64
	// MissCost is the extra price of the next real request if the cache
	// entry is lost.
	MissCost float64
	// ContinuationProbability is the estimated chance that a real request
	// arrives before the entry expires.
	ContinuationProbability float64
	// ExpectedSavings is `continuationProbability * missCost - warmCost`.
	ExpectedSavings float64
	// EconomicsAvailable is false when the prompt size or the model's
	// prices are unknown.
	EconomicsAvailable bool
	// Action is pi's decision: "warm" when expectedSavings is at least
	// $0.05.
	Action CacheWarmingAction
}

// CacheWarmingDecisionEvent is fired before each refresh with pi's decision
// filled in. Everything else an extension might want (model, idle state,
// context size) is on the context (pi: CacheWarmingDecisionEvent).
type CacheWarmingDecisionEvent struct {
	Type                    string `json:"type"`
	WarmCost                float64
	MissCost                float64
	ContinuationProbability float64
	Action                  CacheWarmingAction
}

func (e *CacheWarmingDecisionEvent) EventType() string { return e.Type }

// CacheWarmingDecisionEventResult is one handler's result (pi:
// CacheWarmingDecisionEventResult).
type CacheWarmingDecisionEventResult struct {
	// Action overrides whether this refresh is sent; "stop" ends warming
	// until the next real request.
	Action CacheWarmingAction `json:"action,omitempty"`
}

// CacheWarmingStatus is the current warming state (pi: CacheWarmingStatus).
type CacheWarmingStatus struct {
	// State: "scheduled" = a refresh timer is armed; "refreshing" = a warm
	// request is in flight; "inactive" = nothing scheduled.
	State string
	// Reason explains why nothing is scheduled.
	Reason string
	// NextWarmAt is the next refresh time (Unix ms); 0 = unset.
	NextWarmAt int64
	// Decision is the pending decision, or the decision that stopped
	// warming.
	Decision *CacheWarmingDecision
	// ExtensionOverride is true when an extension changed decision.action.
	ExtensionOverride bool
}

// CacheWarmRequest is the request whose prompt cache entry should be kept
// warm, exactly as it was sent (pi: CacheWarmRequest).
type CacheWarmRequest struct {
	Model   *ai.Model
	Context *types.TranscriptContext
	Options *ai.ModelsSimpleStreamOptions
}

// simpleStreamOptions returns the simple options slice (pig addition: the
// ModelsSimpleStreamOptions wrapper embeds them).
func (r *CacheWarmRequest) simpleStreamOptions() *types.SimpleStreamOptions {
	if r.Options == nil {
		return nil
	}
	return &r.Options.SimpleStreamOptions
}

// cacheWarmActiveRun is one scheduled warming run (pi: ActiveRun).
type cacheWarmActiveRun struct {
	CacheWarmRequest
	// ctx cancels the in-flight refresh (pi: controller.signal).
	ctx context.Context
	// isCurrent reports whether the session's model or messages still match
	// the request.
	isCurrent func() bool
	ttlMs     int64
	delayMs   int64
	// refreshDeadlineAt is the latest safe time to send this refresh,
	// leaving half the original expiry margin (pi: refreshDeadlineAt).
	refreshDeadlineAt int64
	startedAt         int64
	cancel            context.CancelFunc
	phase             string // "streaming" | "idle"
	nextWarmAt        int64
	timer             *time.Timer
	// extensionOverride is set while a refresh that an extension forced is
	// in flight.
	extensionOverride bool
}

// CacheWarmerSurface is the cache-warmer surface AgentSession consumes (pi:
// Pick<CacheWarmer, "cancel" | "status" | "onAgentSettled" | "onModeChanged"
// | "onWarmed">).
type CacheWarmerSurface interface {
	Cancel()
	GetStatus() CacheWarmingStatus
	OnAgentSettled()
	OnModeChanged()
	SetOnWarmed(func(entry *UsageEntry))
}

// CacheWarmer keeps one prompt cache entry warm (pi: CacheWarmer).
type CacheWarmer struct {
	mu       sync.Mutex
	run      *cacheWarmActiveRun
	inactive CacheWarmingStatus
	models   streamSimpleOnly
	session  usageAppendingSessionManager
	getMode  func() CacheWarmingMode
	// decide lets extensions override event.action; failures fall back to
	// pi's decision.
	decide func(ctx context.Context, event *CacheWarmingDecisionEvent) (CacheWarmingAction, error)
	// onWarmed is called with the persisted usage entry after each
	// successful refresh (pi: onWarmed property; pig: SetOnWarmed).
	onWarmed func(entry *UsageEntry)
	nowFn    func() time.Time
}

// streamSimpleOnly is the model runtime slice CacheWarmer consumes (pi:
// Pick<ModelRuntime, "streamSimple">).
type streamSimpleOnly interface {
	StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream
}

// usageAppendingSessionManager is the session manager slice CacheWarmer
// consumes (pi: Pick<SessionManager, "appendUsage" | "getBranch">).
type usageAppendingSessionManager interface {
	AppendUsage(kind string, provider string, model string, usage *types.Usage, note string) (*UsageEntry, error)
	GetBranch(leafID string) []SessionEntry
}

// NewCacheWarmer constructs the warmer (pi: constructor).
func NewCacheWarmer(
	models streamSimpleOnly,
	session usageAppendingSessionManager,
	getMode func() CacheWarmingMode,
	decide func(ctx context.Context, event *CacheWarmingDecisionEvent) (CacheWarmingAction, error),
) *CacheWarmer {
	return &CacheWarmer{
		models:   models,
		session:  session,
		getMode:  getMode,
		decide:   decide,
		inactive: CacheWarmingStatus{State: "inactive", Reason: "waiting for first request"},
		nowFn:    time.Now,
	}
}

// SetOnWarmed installs the warmed-usage callback (pi: onWarmed property).
func (w *CacheWarmer) SetOnWarmed(fn func(entry *UsageEntry)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onWarmed = fn
}

// GetStatus returns the current cache-warming state (pi: get status).
func (w *CacheWarmer) GetStatus() CacheWarmingStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.getMode() == "off" {
		return CacheWarmingStatus{State: "inactive", Reason: "cache warming disabled"}
	}
	run := w.run
	if run == nil {
		return w.inactive
	}
	if !run.isCurrent() {
		return CacheWarmingStatus{State: "inactive", Reason: "conversation context changed"}
	}
	decision := w.evaluate(run)
	refreshing := run.timer == nil
	if !decision.EconomicsAvailable && !refreshing {
		return CacheWarmingStatus{State: "inactive", Reason: "cache economics unavailable"}
	}
	return CacheWarmingStatus{
		State:             lo.Ternary(refreshing, "refreshing", "scheduled"),
		NextWarmAt:        run.nextWarmAt,
		Decision:          decision,
		ExtensionOverride: run.extensionOverride,
	}
}

// Start replaces any previous run and keeps the prompt cache entry written
// by `request` warm while `isCurrent` holds (pi: start). Warm requests
// never extend the fixed safety windows.
func (w *CacheWarmer) Start(request CacheWarmRequest, isCurrent func() bool) {
	w.mu.Lock()
	w.clearRunLocked()
	mode := w.getMode()
	if mode == "off" {
		w.stopLocked("cache warming disabled", nil, false)
		w.mu.Unlock()
		return
	}
	if !IsReplayable(request.Model, request.simpleStreamOptions()) {
		w.stopLocked("request cannot be replayed safely", nil, false)
		w.mu.Unlock()
		return
	}
	ttlMs, ok := GetPromptCacheTtlMs(request.Model, request.simpleStreamOptions())
	if !ok {
		reason := "cache lifetime unavailable"
		if request.Options != nil && request.Options.CacheRetention == types.CacheRetentionNone {
			reason = "request disabled prompt caching"
		}
		w.stopLocked(reason, nil, false)
		w.mu.Unlock()
		return
	}
	delayMs, ok := GetCacheWarmingDelayMs(ttlMs)
	if !ok {
		w.stopLocked("cache lifetime unavailable", nil, false)
		w.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &cacheWarmActiveRun{
		CacheWarmRequest:  request,
		ctx:               ctx,
		isCurrent:         isCurrent,
		ttlMs:             ttlMs,
		delayMs:           delayMs,
		refreshDeadlineAt: 0,
		startedAt:         w.nowFn().UnixMilli(),
		cancel:            cancel,
		phase:             "streaming",
	}
	w.run = run
	w.mu.Unlock()
	w.schedule(run)
}

// OnAgentSettled reconciles the run after an agent run settles (pi:
// onAgentSettled).
func (w *CacheWarmer) OnAgentSettled() {
	w.mu.Lock()
	run := w.run
	if run == nil {
		w.mu.Unlock()
		return
	}
	if w.getMode() == "streaming" {
		w.stopLocked("agent run settled", nil, false)
		w.mu.Unlock()
		return
	}
	run.phase = "idle"
	deadline := run.startedAt + maxIdleWarmingAgeMs
	if run.nextWarmAt > deadline || w.nowFn().UnixMilli() >= deadline {
		w.stopLocked("30-minute idle safety limit reached", nil, false)
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
}

// OnModeChanged reconciles an active run after the persisted warming mode
// changes (pi: onModeChanged).
func (w *CacheWarmer) OnModeChanged() {
	w.mu.Lock()
	run := w.run
	if run == nil {
		w.mu.Unlock()
		return
	}
	if reason := w.getModeStopReason(run); reason != "" {
		w.stopLocked(reason, nil, false)
	}
	w.mu.Unlock()
}

// Cancel stops warming (pi: cancel).
func (w *CacheWarmer) Cancel() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked("inactive", nil, false)
}

// clearRunLocked drops the active run, canceling its timer and context.
func (w *CacheWarmer) clearRunLocked() {
	run := w.run
	if run == nil {
		return
	}
	w.run = nil
	if run.timer != nil {
		run.timer.Stop()
		run.timer = nil
	}
	run.cancel()
}

// stopLocked clears the run and records the inactive status.
func (w *CacheWarmer) stopLocked(reason string, decision *CacheWarmingDecision, extensionOverride bool) {
	w.clearRunLocked()
	w.inactive = CacheWarmingStatus{
		State:             "inactive",
		Reason:            reason,
		Decision:          decision,
		ExtensionOverride: extensionOverride,
	}
}

// cacheWarmAfterFunc arms the refresh timer (pi: setTimeout; test seam for
// the fake-timer cache-warmer tests — the pi suite drives refreshes with
// vi.useFakeTimers, which has no Go equivalent).
var cacheWarmAfterFunc = func(delay time.Duration, fn func()) *time.Timer {
	return time.AfterFunc(delay, fn)
}

// schedule arms the next refresh timer (pi: schedule).
func (w *CacheWarmer) schedule(run *cacheWarmActiveRun) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run != run {
		return
	}
	run.extensionOverride = false
	run.nextWarmAt = w.nowFn().UnixMilli() + run.delayMs
	// A timer can run late after sleep or event-loop blockage. Keep half of
	// the planned pre-expiry margin for that delay and request dispatch; a
	// late refresh is likely a full-price cache write, not a cache warm.
	run.refreshDeadlineAt = run.nextWarmAt + (run.ttlMs-run.delayMs)/2
	deadline := run.startedAt
	if run.phase == "idle" {
		deadline += maxIdleWarmingAgeMs
	} else {
		deadline += maxWarmingAgeMs
	}
	if run.nextWarmAt > deadline || w.nowFn().UnixMilli() >= deadline {
		if run.phase == "idle" {
			w.stopLocked("30-minute idle safety limit reached", nil, false)
		} else {
			w.stopLocked("one-hour safety limit reached", nil, false)
		}
		return
	}
	delay := time.Duration(run.nextWarmAt-w.nowFn().UnixMilli()) * time.Millisecond
	if delay < 0 {
		delay = 0
	}
	run.timer = cacheWarmAfterFunc(delay, func() { w.refresh(run) })
}

// refresh runs one warm request (pi: refresh).
func (w *CacheWarmer) refresh(run *cacheWarmActiveRun) {
	w.mu.Lock()
	run.timer = nil
	valid := w.validateRunLocked(run)
	var deadlineMissed bool
	if valid {
		deadlineMissed = w.refreshDeadlineMissedLocked(run)
	}
	w.mu.Unlock()
	if !valid || deadlineMissed {
		return
	}

	decision := w.evaluate(run)
	action := decision.Action
	if w.decide != nil {
		if override, err := w.decide(context.Background(), &CacheWarmingDecisionEvent{
			Type:                    "cache_warming_decision",
			WarmCost:                decision.WarmCost,
			MissCost:                decision.MissCost,
			ContinuationProbability: decision.ContinuationProbability,
			Action:                  action,
		}); err == nil {
			action = override
		}
		// Extension failures fall back to pi's own decision.
	}

	w.mu.Lock()
	valid = w.validateRunLocked(run)
	if valid {
		deadlineMissed = w.refreshDeadlineMissedLocked(run)
	}
	w.mu.Unlock()
	if !valid || deadlineMissed {
		return
	}
	extensionOverride := action != decision.Action
	if action == CacheWarmingActionStop {
		reason := "expected savings below threshold"
		if extensionOverride {
			reason = "stopped by extension"
		} else if !decision.EconomicsAvailable {
			reason = "cache economics unavailable"
		}
		w.mu.Lock()
		w.stopLocked(reason, decision, extensionOverride)
		w.mu.Unlock()
		return
	}

	w.mu.Lock()
	run.extensionOverride = extensionOverride
	w.mu.Unlock()

	options := &ai.ModelsSimpleStreamOptions{}
	if run.Options != nil {
		*options = *run.Options
	}
	// pi: { ...run.options, maxTokens: 1, maxRetries: 0,
	// signal: run.controller.signal } — the replay caps output at one token
	// and never retries; the run context carries the signal.
	options.MaxTokens = lo.ToPtr(int64(1))
	options.MaxRetries = lo.ToPtr(0)
	message := w.models.StreamSimple(run.ctx, run.Model, &ai.Context{Messages: run.Context.Messages}, options).Result()
	w.mu.Lock()
	valid = w.validateRunLocked(run)
	w.mu.Unlock()
	if valid && message.StopReason != types.StopReasonError && message.StopReason != types.StopReasonAborted {
		provider := string(message.Provider)
		model := message.Model
		if message.ResponseModel != "" {
			model = message.ResponseModel
		}
		entry, err := w.session.AppendUsage("cache_warm", provider, model, &message.Usage, lo.Ternary(extensionOverride, "extension override", ""))
		if err == nil {
			w.mu.Lock()
			onWarmed := w.onWarmed
			w.mu.Unlock()
			if onWarmed != nil {
				onWarmed(entry)
			}
		}
		// Cache warming is best-effort and must not affect the active agent
		// run (pi: swallow appendUsage/onWarmed errors).
	}
	w.mu.Lock()
	stillCurrent := w.run == run
	w.mu.Unlock()
	if stillCurrent {
		w.schedule(run)
	}
}

// refreshDeadlineMissed reports whether the current time passed the run's
// refresh deadline, stopping the run when it has (pi:
// refreshDeadlineMissed). Callers must hold w.mu.
func (w *CacheWarmer) refreshDeadlineMissedLocked(run *cacheWarmActiveRun) bool {
	if w.nowFn().UnixMilli() <= run.refreshDeadlineAt {
		return false
	}
	w.stopLocked("cache refresh deadline missed", nil, false)
	return true
}

// validateRunLocked stops a stale run (pi: validateRun). Callers must hold
// w.mu.
func (w *CacheWarmer) validateRunLocked(run *cacheWarmActiveRun) bool {
	if w.run != run {
		return false
	}
	reason := w.getModeStopReason(run)
	if reason == "" && !run.isCurrent() {
		reason = "conversation context changed"
	}
	if reason != "" {
		w.stopLocked(reason, nil, false)
		return false
	}
	return true
}

// getModeStopReason returns why the mode stops the run, or "" (pi:
// getModeStopReason). Callers must hold w.mu.
func (w *CacheWarmer) getModeStopReason(run *cacheWarmActiveRun) string {
	mode := w.getMode()
	if mode == "off" {
		return "cache warming disabled"
	}
	if mode == "streaming" && run.phase == "idle" {
		return "agent run settled"
	}
	return ""
}

// evaluate computes the warm-or-stop decision (pi: evaluate).
func (w *CacheWarmer) evaluate(run *cacheWarmActiveRun) *CacheWarmingDecision {
	model := run.Model
	promptTokens := lastPromptTokens(w.session.GetBranch(""))
	cacheHitCost := cacheWarmPrice(model, 0, 0, promptTokens, 0)
	var cacheMissCost float64
	if model.Cost.CacheWrite > 0 {
		cacheMissCost = cacheWarmPrice(model, 0, 0, 0, promptTokens)
	} else {
		cacheMissCost = cacheWarmPrice(model, promptTokens, 0, 0, 0)
	}
	warmCost := cacheWarmPrice(model, 0, 1, promptTokens, 0)
	missCost := cacheMissCost - cacheHitCost
	if missCost < 0 {
		missCost = 0
	}
	continuationProbability := 1.0
	if run.phase == "idle" {
		continuationProbability = idleContinuationProbability
	}
	economicsAvailable := promptTokens > 0 && (cacheHitCost > 0 || cacheMissCost > 0)
	expectedSavings := continuationProbability*missCost - warmCost
	action := CacheWarmingActionStop
	if expectedSavings >= cacheWarmingMinimumExpectedSavings {
		action = CacheWarmingActionWarm
	}
	return &CacheWarmingDecision{
		Phase:                   run.phase,
		WarmCost:                warmCost,
		MissCost:                missCost,
		ContinuationProbability: continuationProbability,
		ExpectedSavings:         expectedSavings,
		EconomicsAvailable:      economicsAvailable,
		Action:                  action,
	}
}

// FormatCacheWarmingDollars renders a dollar amount (pi: formatDollars).
func FormatCacheWarmingDollars(value float64) string {
	if value < 0 {
		return fmt.Sprintf("-$%.3f", -value)
	}
	return fmt.Sprintf("$%.3f", value)
}

// formatCacheWarmingEconomics renders the decision economics (pi:
// formatCacheWarmingEconomics).
func formatCacheWarmingEconomics(decision *CacheWarmingDecision) string {
	if !decision.EconomicsAvailable {
		return "cache economics unavailable"
	}
	probability := int(decision.ContinuationProbability*100 + 0.5)
	var probabilityText string
	if decision.Phase == "streaming" {
		probabilityText = fmt.Sprintf("%d%% continuation probability while agent is running", probability)
	} else {
		probabilityText = fmt.Sprintf("%d%% continuation probability", probability)
	}
	comparison := "<"
	if decision.Action == CacheWarmingActionWarm {
		comparison = ">="
	}
	return fmt.Sprintf("%s, expected savings %s %s $%.3f", probabilityText, FormatCacheWarmingDollars(decision.ExpectedSavings), comparison, cacheWarmingMinimumExpectedSavings)
}

// formatCacheWarmingDecisionTime renders when the next decision happens
// (pi: formatCacheWarmingDecisionTime).
func formatCacheWarmingDecisionTime(nextWarmAt int64, now int64) string {
	if nextWarmAt == 0 || nextWarmAt <= now {
		return "Decision now"
	}
	remainingSeconds := int((nextWarmAt - now + 999) / 1000)
	hours := remainingSeconds / 3600
	remainingSeconds %= 3600
	minutes := remainingSeconds / 60
	seconds := remainingSeconds % 60
	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return "Decision in " + strings.Join(parts, " ")
}

// FormatCacheWarmingStatus renders the one-line status for /session (pi:
// formatCacheWarmingStatus).
func FormatCacheWarmingStatus(status CacheWarmingStatus, now int64) string {
	decision := status.Decision
	// A decision is attached once pi (or an extension) acted on it;
	// "inactive" without one never got that far.
	if decision == nil || (status.State == "inactive" && !decision.EconomicsAvailable && !status.ExtensionOverride) {
		reason := status.Reason
		if reason == "" {
			reason = "unknown reason"
		}
		return "Inactive (" + reason + ")"
	}
	var details string
	if status.ExtensionOverride {
		details = "extension override, " + formatCacheWarmingEconomics(decision)
	} else {
		details = formatCacheWarmingEconomics(decision) + " -> " + string(decision.Action)
	}
	switch status.State {
	case "inactive":
		return "Stopped (" + details + ")"
	case "refreshing":
		return "Warming cache (" + details + ")"
	default:
		return formatCacheWarmingDecisionTime(status.NextWarmAt, now) + " (" + details + ")"
	}
}

// FormatCacheWarmingUsage renders the one-line transcript text for persisted
// cache-warming usage (pi: formatCacheWarmingUsage).
func FormatCacheWarmingUsage(entry *UsageEntry) string {
	note := ""
	if entry.Note != "" {
		note = " (" + entry.Note + ")"
	}
	cost := fmt.Sprintf("%.6f", entry.Usage.Cost.Total)
	// pi: .replace(/(\.\d{3}\d*?)0+$/, "$1") — trim trailing zeros after
	// the third decimal.
	if idx := strings.LastIndex(cost, "."); idx >= 0 {
		frac := cost[idx+1:]
		if len(frac) > 3 {
			trimmed := strings.TrimRight(frac, "0")
			if len(trimmed) > 3 {
				cost = cost[:idx+1] + trimmed
			} else {
				cost = cost[:idx+1] + frac // keep at least 3 decimals when all zeros
			}
		}
	}
	return "Cache warmed" + note + ": $" + cost
}
