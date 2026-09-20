package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/auth"
)

func ptr[T any](v T) *T { return &v }

func TestCalculateCost(t *testing.T) {
	model := &Model{
		ID:   "m",
		Cost: ModelCost{ModelCostRates: ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
	}
	usage := &Usage{Input: 1000000, Output: 100000, CacheRead: 500000, CacheWrite: 200000}
	cost := CalculateCost(model, usage)
	if cost.Input != 3 || cost.Output != 1.5 {
		t.Fatalf("input/output: %+v", cost)
	}
	if cost.CacheRead != 0.15 {
		t.Fatalf("cacheRead: %v", cost.CacheRead)
	}
	if cost.CacheWrite != 0.75 {
		t.Fatalf("cacheWrite: %v", cost.CacheWrite)
	}
	if cost.Total != cost.Input+cost.Output+cost.CacheRead+cost.CacheWrite {
		t.Fatalf("total: %+v", cost)
	}
}

func TestCalculateCostTiersAndLongWrite(t *testing.T) {
	model := &Model{
		Cost: ModelCost{
			ModelCostRates: ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
			Tiers: []ModelCostTier{
				{ModelCostRates: ModelCostRates{Input: 6, Output: 22.5, CacheRead: 0.6, CacheWrite: 7.5}, InputTokensAbove: 1000000},
			},
		},
	}
	// 1.2M input tokens: above the 1M tier threshold, so tier rates apply.
	// CacheWrite1h is a subset of CacheWrite (pi semantics).
	usage := &Usage{Input: 1200000, Output: 0, CacheRead: 0, CacheWrite: 200000, CacheWrite1h: ptr(int64(200000))}
	cost := CalculateCost(model, usage)
	// Anthropic charges 2x base input for 1h cache writes: 6 * 2 * 200000 / 1e6.
	if cost.CacheWrite != 2.4 {
		t.Fatalf("cacheWrite with 1h: %v", cost.CacheWrite)
	}
	if cost.Input != 7.2 {
		t.Fatalf("tiered input: %v", cost.Input)
	}
}

func TestGetSupportedThinkingLevels(t *testing.T) {
	off := &Model{Reasoning: false}
	if got := GetSupportedThinkingLevels(off); !reflect.DeepEqual(got, []ModelThinkingLevel{ModelThinkingOff}) {
		t.Fatalf("off: %v", got)
	}

	reasoning := &Model{Reasoning: true}
	// No map: everything except xhigh/max.
	if got := GetSupportedThinkingLevels(reasoning); len(got) != 5 || got[0] != ModelThinkingOff {
		t.Fatalf("reasoning: %v", got)
	}

	// Explicit null marks unsupported; xhigh/max require a mapping.
	mapped := &Model{
		Reasoning:        true,
		ThinkingLevelMap: ThinkingLevelMap{ModelThinkingOff: ptr("off"), ModelThinkingLow: nil},
	}
	got := GetSupportedThinkingLevels(mapped)
	for _, level := range got {
		if level == ModelThinkingLow || level == ModelThinkingXHigh || level == ModelThinkingMax {
			t.Fatalf("unexpected level %v in %v", level, got)
		}
	}
}

func TestClampThinkingLevel(t *testing.T) {
	model := &Model{Reasoning: true}
	if got := ClampThinkingLevel(model, ModelThinkingMax); got != ModelThinkingHigh {
		t.Fatalf("clamp max -> %v", got)
	}
	if got := ClampThinkingLevel(model, ModelThinkingOff); got != ModelThinkingOff {
		t.Fatalf("clamp off -> %v", got)
	}
	limited := &Model{
		Reasoning:        true,
		ThinkingLevelMap: ThinkingLevelMap{ModelThinkingOff: ptr("x"), ModelThinkingLow: ptr("x"), ModelThinkingMedium: nil, ModelThinkingHigh: nil},
	}
	// medium and high are explicitly null (unsupported): nearest higher is
	// also unsupported, so the clamp falls down to low.
	if got := ClampThinkingLevel(limited, ModelThinkingMedium); got != ModelThinkingLow {
		t.Fatalf("clamp medium -> %v", got)
	}
	// absent mapping on a non-xhigh/max level means supported (pi).
	absent := &Model{Reasoning: true}
	if got := ClampThinkingLevel(absent, ModelThinkingMedium); got != ModelThinkingMedium {
		t.Fatalf("clamp medium on unmapped model -> %v", got)
	}
	// unsupported on a reasoning=false model: off.
	if got := ClampThinkingLevel(&Model{Reasoning: false}, ModelThinkingHigh); got != ModelThinkingOff {
		t.Fatalf("clamp on non-reasoning -> %v", got)
	}
}

func TestModelsAreEqual(t *testing.T) {
	a := &Model{ID: "x", Provider: "openai"}
	if !ModelsAreEqual(a, &Model{ID: "x", Provider: ProviderOpenAI}) {
		t.Fatal("want equal")
	}
	if ModelsAreEqual(a, &Model{ID: "x", Provider: "anthropic"}) {
		t.Fatal("want unequal provider")
	}
	if ModelsAreEqual(a, nil) {
		t.Fatal("nil never equal")
	}
}

func TestMergeHeaders(t *testing.T) {
	got := mergeHeaders(
		ProviderHeaders{"X-A": ptr("1"), "x-b": ptr("2")},
		ProviderHeaders{"X-B": ptr("3")},
	)
	if len(got) != 2 {
		t.Fatalf("headers: %v", got)
	}
	if got["X-A"] == nil || *got["X-A"] != "1" {
		t.Fatalf("x-a: %v", got["X-A"])
	}
	if got["X-B"] == nil || *got["X-B"] != "3" {
		t.Fatalf("override must replace case-insensitively: %v", got["X-B"])
	}
	if mergeHeaders(nil, nil) != nil {
		t.Fatal("nil merge should be nil")
	}
}

func TestCredentialJSONRoundtrip(t *testing.T) {
	apiKey := &auth.ApiKeyCredential{Key: "sk-1", Env: ProviderEnv{"GATEWAY": "g1"}}
	data, err := json.Marshal(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"type":"api_key","key":"sk-1","env":{"GATEWAY":"g1"}}` {
		t.Fatalf("api_key json: %s", data)
	}
	decoded, err := auth.UnmarshalCredential(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(*auth.ApiKeyCredential); !ok || decoded.CredentialType() != auth.CredentialTypeAPIKey {
		t.Fatalf("decoded: %#v", decoded)
	}

	oauth := &auth.OAuthCredential{OAuthCredentials: auth.OAuthCredentials{
		Refresh: "r", Access: "a", Expires: 123,
		Extra: map[string]any{"account_id": "acc"},
	}}
	data, err = json.Marshal(oauth)
	if err != nil {
		t.Fatal(err)
	}
	var back auth.OAuthCredential
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Refresh != "r" || back.Expires != 123 || back.Extra["account_id"] != "acc" {
		t.Fatalf("oauth roundtrip: %+v (%s)", back, data)
	}
}

func TestInMemoryCredentialStoreModify(t *testing.T) {
	store := &auth.InMemoryCredentialStore{}
	ctx := context.Background()

	// nil fn result leaves the entry unchanged and returns the current.
	post, err := store.Modify(ctx, "p", func(current auth.Credential) (auth.Credential, error) {
		if current != nil {
			t.Fatal("expected empty store")
		}
		return nil, nil
	})
	if err != nil || post != nil {
		t.Fatalf("empty modify: %v %v", post, err)
	}

	key := &auth.ApiKeyCredential{Key: "k"}
	if _, err := store.Modify(ctx, "p", func(auth.Credential) (auth.Credential, error) { return key, nil }); err != nil {
		t.Fatal(err)
	}
	got, err := store.Read(ctx, "p")
	if err != nil || got.CredentialType() != auth.CredentialTypeAPIKey {
		t.Fatalf("read: %v %v", got, err)
	}
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 1 || infos[0].ProviderID != "p" {
		t.Fatalf("list: %v %v", infos, err)
	}
	if err := store.Delete(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Read(ctx, "p")
	if err != nil || got != nil {
		t.Fatalf("post-delete read: %v %v", got, err)
	}
}

func TestInMemoryModelsStoreClone(t *testing.T) {
	store := &InMemoryModelsStore{}
	ctx := context.Background()
	entry := &ModelsStoreEntry{Models: []Model{{ID: "m1", Provider: ProviderOpenAI}}, CheckedAt: ptr(int64(5))}
	if err := store.Write(ctx, "p", entry); err != nil {
		t.Fatal(err)
	}
	entry.CheckedAt = ptr(int64(9)) // mutate original; stored copy must be isolated
	got, err := store.Read(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if *got.CheckedAt != 5 || len(got.Models) != 1 {
		t.Fatalf("stored entry not isolated: %+v", got)
	}
	if err := store.Delete(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Read(ctx, "p")
	if err != nil || got != nil {
		t.Fatalf("post-delete: %v %v", got, err)
	}
}

type stubAuth struct{ key string }

func (s *stubAuth) Name() string { return "stub key" }
func (s *stubAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	if v, ok := input.Ctx.Env("STUB_KEY"); ok {
		return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: v}, Source: "STUB_KEY"}, nil
	}
	if s.key != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: s.key}, Source: "stored"}, nil
	}
	return nil, nil
}

type stubProvider struct {
	id      string
	streams ProviderStreams
	models  []Model
	auth    auth.ProviderAuth
}

func (p *stubProvider) ID() string               { return p.id }
func (p *stubProvider) Name() string             { return p.id }
func (p *stubProvider) BaseURL() string          { return "" }
func (p *stubProvider) Headers() ProviderHeaders { return nil }
func (p *stubProvider) Auth() auth.ProviderAuth  { return p.auth }
func (p *stubProvider) GetModels() []Model       { return p.models }
func (p *stubProvider) Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	return p.streams.Stream(ctx, model, llmContext, options)
}
func (p *stubProvider) StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	return p.streams.StreamSimple(ctx, model, llmContext, options)
}

type stubStreams struct{}

func (stubStreams) Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	s := NewAssistantMessageEventStream()
	msg := &AssistantMessage{
		Role: RoleAssistant, Content: []Content{NewTextContent("auth ok: " + options.APIKey)},
		API: model.API, Provider: model.Provider, Model: model.ID,
		StopReason: StopReasonStop, Timestamp: 1,
	}
	s.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: msg})
	return s
}
func (stubStreams) StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	return stubStreams{}.Stream(ctx, model, llmContext, &StreamOptions{ProviderRequestOptions: options.ProviderRequestOptions})
}

func TestModelsAuthResolutionAndStream(t *testing.T) {
	model := Model{ID: "stub-model", API: ApiOpenAICompletions, Provider: "stub", Input: []ModelInput{ModelInputText}}
	provider := &stubProvider{
		id:      "stub",
		streams: stubStreams{},
		models:  []Model{model},
		auth:    auth.ProviderAuth{APIKey: &stubAuth{key: "stored-key"}},
	}

	m := NewModels(nil)
	m.SetProvider(provider)

	// Unconfigured ambient source -> pi surfaces setup failures as stream
	// error events: CompleteSimple resolves with a stopReason=error message,
	// no Go error.
	bad := &stubProvider{
		id: "stub", streams: stubStreams{}, models: []Model{model},
		auth: auth.ProviderAuth{APIKey: &stubAuth{}},
	}
	mBad := NewModels(nil)
	mBad.SetProvider(bad)
	msg, err := mBad.CompleteSimple(context.Background(), &model, &Context{Messages: []Message{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != StopReasonError || !strings.Contains(msg.ErrorMessage, "Provider is not configured") {
		t.Fatalf("want setup error message, got %+v (%v)", msg, msg.ErrorMessage)
	}

	// Configured via stored/ambient key: CompleteSimple succeeds and the key
	// reached the provider options.
	msg, err = m.CompleteSimple(context.Background(), &model, &Context{Messages: []Message{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text() != "auth ok: stored-key" {
		t.Fatalf("text: %q", msg.Text())
	}

	// GetAvailable reflects configured auth.
	available, err := m.GetAvailable(context.Background(), "")
	if err != nil || len(available) != 1 {
		t.Fatalf("available: %v %v", available, err)
	}
}

func TestNewProviderMergesDynamicModels(t *testing.T) {
	baseline := []Model{{ID: "static", Provider: "p"}}
	provider := CreateProvider(&CreateProviderOptions{
		ID:     "p",
		Auth:   auth.ProviderAuth{APIKey: &stubAuth{key: "k"}},
		Models: baseline,
		API:    stubStreams{},
	})
	got := provider.GetModels()
	if len(got) != 1 || got[0].ID != "static" {
		t.Fatalf("baseline: %v", got)
	}

	// Simulate a dynamic overlay publication.
	composed := provider.(*composedProvider)
	composed.mu.Lock()
	composed.dynamicModels = []Model{{ID: "dynamic", Provider: "p"}, {ID: "static", Provider: "p"}}
	composed.mu.Unlock()

	got = provider.GetModels()
	if len(got) != 2 {
		t.Fatalf("merged: %v", got)
	}
	// dynamic overlay replaces the baseline entry with the same id.
	if got[0].ID != "dynamic" && got[1].ID != "dynamic" {
		t.Fatalf("overlay missing: %v", got)
	}
	for _, model := range got {
		if model.ID == "static" && model.Provider != "p" {
			t.Fatalf("replaced entry wrong: %+v", model)
		}
	}
}

// stubRefreshingProvider extends stubProvider with a dynamicProvider
// RefreshModels implementation (pi: a provider with refreshModels).
type stubRefreshingProvider struct {
	stubProvider
	refresh func(ctx context.Context, refreshCtx *RefreshModelsContext) error
}

func (p *stubRefreshingProvider) RefreshModels(ctx context.Context, refreshCtx *RefreshModelsContext) error {
	return p.refresh(ctx, refreshCtx)
}

// Regression (docs/fix/2026-09-14-tui-turn-memory-leak.md): Refresh must
// cancel the per-provider refresh context once the refresh settles. pi's
// AbortController is garbage-collected when the promise resolves, but a Go
// cancelCtx stays registered in its parent's children map until canceled —
// one leaked registration per provider per refresh. The operation has
// settled by the time Refresh returns (or the context was already canceled
// by a supersede), so Done must be closed.
func TestModelsRefreshCancelsPerProviderRefreshContext(t *testing.T) {
	refreshContexts := make(chan context.Context, 1)
	provider := &stubRefreshingProvider{
		stubProvider: stubProvider{
			id:      "refreshing",
			streams: stubStreams{},
			models:  []Model{{ID: "m", API: ApiOpenAICompletions, Provider: "refreshing"}},
		},
		refresh: func(ctx context.Context, refreshCtx *RefreshModelsContext) error {
			refreshContexts <- ctx
			return nil
		},
	}
	m := NewModels(nil)
	m.SetProvider(provider)

	result := m.Refresh(context.Background(), &ModelsRefreshOptions{Providers: []string{"refreshing"}})
	if len(result.Errors) != 0 {
		t.Fatalf("refresh errors: %v", result.Errors)
	}
	var refreshCtx context.Context
	select {
	case refreshCtx = <-refreshContexts:
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshModels never invoked")
	}
	select {
	case <-refreshCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("refresh context not canceled after Refresh returned (Refresh leaks the ctx registration)")
	}
}
