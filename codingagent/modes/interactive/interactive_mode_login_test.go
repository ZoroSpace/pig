// interactive_mode_login_test.go (pi: test/oauth-selector.test.ts — the
// getLoginProviderOptions projection case; the OAuthSelectorComponent render
// cases live in the components package).
package interactive

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"context"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
)

// fakeAuthAPIKey is a named API-key auth (pi's fake method { name, login }).
type fakeAuthAPIKey struct{ name string }

func (f *fakeAuthAPIKey) Name() string { return f.name }
func (f *fakeAuthAPIKey) Resolve(auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	return nil, nil
}

// fakeAuthOAuth is a named OAuth auth (pi's fake method { name, login }).
type fakeAuthOAuth struct{ name string }

func (f *fakeAuthOAuth) Name() string         { return f.name }
func (f *fakeAuthOAuth) IsSubscription() bool { return false }
func (f *fakeAuthOAuth) LoginLabel() string   { return "" }
func (f *fakeAuthOAuth) Login(context.Context, auth.AuthInteraction, *auth.LoginOptions) (*auth.OAuthCredential, error) {
	return nil, nil
}
func (f *fakeAuthOAuth) Refresh(context.Context, *auth.OAuthCredential) (*auth.OAuthCredential, error) {
	return nil, nil
}
func (f *fakeAuthOAuth) ToAuth(*auth.OAuthCredential) (*auth.ModelAuth, error) {
	return nil, nil
}

// fakeAuthProvider is a minimal provider carrying fake auth methods (pi's
// fake providers in the getLoginProviderOptions test).
type fakeAuthProvider struct {
	id     string
	name   string
	oauth  auth.OAuthAuth
	apiKey auth.ApiKeyAuth
	models []ai.Model
}

func (f *fakeAuthProvider) ID() string                     { return f.id }
func (f *fakeAuthProvider) Name() string                   { return f.name }
func (f *fakeAuthProvider) BaseURL() string                { return "" }
func (f *fakeAuthProvider) Headers() types.ProviderHeaders { return nil }
func (f *fakeAuthProvider) Auth() auth.ProviderAuth {
	return auth.ProviderAuth{APIKey: f.apiKey, OAuth: f.oauth}
}
func (f *fakeAuthProvider) GetModels() []ai.Model { return f.models }
func (f *fakeAuthProvider) Stream(context.Context, *ai.Model, *ai.TranscriptContext, *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return ai.NewAssistantMessageEventStream()
}
func (f *fakeAuthProvider) StreamSimple(context.Context, *ai.Model, *ai.TranscriptContext, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return ai.NewAssistantMessageEventStream()
}

func TestGetLoginProviderOptionsProjectsProviderAuthWithoutFiltering(t *testing.T) {
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)

	runtime := session.GetModelRuntime()
	if err := runtime.RegisterNativeProvider(&fakeAuthProvider{
		id:     "anthropic",
		name:   "Anthropic",
		oauth:  &fakeAuthOAuth{name: "Anthropic (Claude Pro/Max)"},
		apiKey: &fakeAuthAPIKey{name: "Anthropic API key"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(&fakeAuthProvider{
		id:     "google-vertex",
		name:   "Google Vertex AI",
		apiKey: &fakeAuthAPIKey{name: "Google Cloud credentials"},
	}); err != nil {
		t.Fatal(err)
	}

	apiKeyType := auth.AuthTypeAPIKey
	apiKeyOptions := mode.getLoginProviderOptions(&apiKeyType)
	// pig adaptation: the real runtime also carries the builtin apiKey
	// providers (pi's test isolates via a fake `this`), so the projection is
	// asserted by locating the fake providers instead of exact-list equality.
	var anthropicKey, googleKey *components.AuthSelectorProvider
	for i := range apiKeyOptions {
		switch apiKeyOptions[i].ID {
		case "anthropic":
			anthropicKey = &apiKeyOptions[i]
		case "google-vertex":
			googleKey = &apiKeyOptions[i]
		}
	}
	if anthropicKey == nil || googleKey == nil {
		t.Fatalf("api_key options missing fake providers: %+v", apiKeyOptions)
	}
	if anthropicKey.Name != "Anthropic" || anthropicKey.AuthType != auth.AuthTypeAPIKey {
		t.Fatalf("api_key anthropic = %+v, want Anthropic/API key", anthropicKey)
	}
	if name := components.AuthSelectorMethodName(anthropicKey.Method); name != "Anthropic API key" {
		t.Fatalf("api_key method = %q, want Anthropic API key", name)
	}
	if googleKey.Name != "Google Vertex AI" || googleKey.AuthType != auth.AuthTypeAPIKey {
		t.Fatalf("api_key google-vertex = %+v, want Google Vertex AI/API key", googleKey)
	}
	if name := components.AuthSelectorMethodName(googleKey.Method); name != "Google Cloud credentials" {
		t.Fatalf("api_key method = %q, want Google Cloud credentials", name)
	}

	oauthType := auth.AuthTypeOAuth
	oauthOptions := mode.getLoginProviderOptions(&oauthType)
	// A2: the builtin set now carries anthropic + kimi-coding OAuth methods
	// alongside the fakes, so locate the fake anthropic entry and assert the
	// builtin kimi-coding method by name.
	var anthropicOAuth, kimiOAuth *components.AuthSelectorProvider
	for i := range oauthOptions {
		switch oauthOptions[i].ID {
		case "anthropic":
			anthropicOAuth = &oauthOptions[i]
		case "kimi-coding":
			kimiOAuth = &oauthOptions[i]
		}
	}
	if anthropicOAuth == nil || anthropicOAuth.Name != "Anthropic" || anthropicOAuth.AuthType != auth.AuthTypeOAuth {
		t.Fatalf("oauth anthropic = %+v, want anthropic/oauth", anthropicOAuth)
	}
	if kimiOAuth == nil {
		t.Fatal("oauth options missing builtin kimi-coding")
	}
	if name := components.AuthSelectorMethodName(kimiOAuth.Method); name != "Kimi Code (subscription)" {
		t.Fatalf("kimi-coding oauth method = %q, want Kimi Code (subscription)", name)
	}
}

// --- post-login model discovery (pi: test/suite/regressions/7027-credential-refresh-hang.test.ts,
// "post-login model discovery" describe) ---
//
// pig adaptations (declared): pi spies session.model / session.setModel /
// runtime.refresh / runtime.getAvailableSnapshot on a mock `this`; pig
// installs the unknown model through Agent.State() (SetModel validates
// against the runtime, which the unknown placeholder would fail), observes
// SetModel through session state + persisted settings, and swaps
// modelRuntimeRefreshFn (seam mirroring cacheWarmAfterFunc) for the
// runtime.refresh spy. The 15s bound is pinned by inspecting the refresh
// context deadline instead of pi's fake timers.

type countingRenderTUI struct {
	tui.TUI
	requests atomic.Int32
	mu       sync.Mutex
}

func (s *countingRenderTUI) RequestRender(_ bool) { s.requests.Add(1) }

// RunExclusive backs InteractiveMode.mutateUI in tests: background UI
// goroutines (e.g. the post-login refresh goroutine) serialize with the
// test's own renders here the way the real TuiBase serializes them in
// production. Plain mutex — safe because nothing under these paths nests a
// second mutateUI call (the real TuiBase.lock is reentrant; this one is not).
func (s *countingRenderTUI) RunExclusive(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// stubModelRuntimeRefresh swaps the post-login refresh seam for the test.
func stubModelRuntimeRefresh(t *testing.T, fn func(ctx context.Context, runtime *core.ModelRuntime, options *ai.ModelsRefreshOptions) ai.ModelsRefreshResult) {
	t.Helper()
	original := modelRuntimeRefreshFn
	modelRuntimeRefreshFn = fn
	t.Cleanup(func() { modelRuntimeRefreshFn = original })
}

// newLoginTestMode extends newSubmitTestMode with the footer fields
// completeProviderAuthentication touches.
func newLoginTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	m := newSubmitTestMode(t)
	m.defaultEditor = m.editor.(*components.CustomEditor)
	m.footerDataProviderView = core.NewFooterDataProvider(m.session().SessionManager.GetCwd())
	m.footer = components.NewFooterComponent(m.session(), m.footerDataProviderView)
	return m
}

// installUnknownModel pins the session model to the unknown placeholder
// (pi: vi.spyOn(session, "model", "get").mockReturnValue(unknownModel)).
func installUnknownModel(session *core.AgentSession) *ai.Model {
	unknown := &ai.Model{ID: "unknown", Name: "Unknown", API: "unknown", Provider: "unknown"}
	session.Agent.State().Model = unknown
	return unknown
}

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// blockAfterStart returns a refresh stub that signals start and blocks until
// release is closed (pi: the never/finish-gated refresh mock).
func blockAfterStart(t *testing.T) (started chan struct{}, release chan struct{}) {
	t.Helper()
	started = make(chan struct{})
	release = make(chan struct{})
	var once sync.Once
	stubModelRuntimeRefresh(t, func(context.Context, *core.ModelRuntime, *ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
		once.Do(func() { close(started) })
		<-release
		return ai.ModelsRefreshResult{}
	})
	return started, release
}

func discoveryModels(ids ...string) []core.ProviderConfigInputModel {
	models := make([]core.ProviderConfigInputModel, 0, len(ids))
	for _, id := range ids {
		models = append(models, core.ProviderConfigInputModel{ID: id, Name: id, API: types.ApiOpenAICompletions})
	}
	return models
}

func TestPostLoginDiscoverySelectsPreferredDefault(t *testing.T) {
	if got := core.DefaultModelPerProvider["radius"]; got != "balanced" {
		t.Fatalf("DefaultModelPerProvider[radius] = %q, want balanced", got)
	}
	tests := []struct {
		name     string
		modelIDs []string
		want     string
	}{
		{"selects balanced from the refreshed catalog", []string{"fast", "balanced"}, "balanced"},
		{"falls back to catalog order when the default is missing", []string{"fast", "powerful"}, "fast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLoginTestMode(t)
			session := m.session()
			unknown := installUnknownModel(session)
			counter := &countingRenderTUI{}
			m.ui = counter
			started, release := blockAfterStart(t)

			m.completeProviderAuthentication(context.Background(), "radius", "Radius", auth.AuthTypeOAuth, unknown)
			<-started
			registerTestProvider(t, session, "radius", discoveryModels(tt.modelIDs...))
			// pig race note: the test goroutine must not touch the session or
			// containers until the mode goroutine is done — its final
			// RequestRender is the completion signal.
			baseline := counter.requests.Load()
			close(release)
			waitFor(t, 2*time.Second, "refresh goroutine to finish", func() bool {
				return counter.requests.Load() > baseline
			})
			waitFor(t, 2*time.Second, tt.want+" to be selected", func() bool {
				current := session.Model()
				return current != nil && current.ID == tt.want
			})
			current := session.Model()
			if string(current.Provider) != "radius" {
				t.Fatalf("selected provider = %q, want radius", current.Provider)
			}
			// The counter is not a reliable completion signal here (showStatus
			// inside the goroutine's finishAuthentication also bumps it before
			// the tail is done) — poll under mutateUI, which serializes the
			// render with the goroutine's remaining mutations.
			var rendered string
			waitFor(t, 2*time.Second, "credentials status to render", func() bool {
				m.mutateUI(func() {
					rendered = normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
				})
				return strings.Contains(rendered, "Credentials saved")
			})
			if !strings.Contains(rendered, "Credentials saved") {
				t.Fatalf("missing credentials status, got:\n%s", rendered)
			}
			if strings.Contains(rendered, "no models are available") || strings.Contains(rendered, "Error") {
				t.Fatalf("unexpected error after discovery, got:\n%s", rendered)
			}
			// persist: true — the selection lands in the settings defaults.
			if provider, ok := m.settingsManager().GetDefaultProvider(); !ok || provider != "radius" {
				t.Fatalf("persisted default provider = %q (ok=%v), want radius", provider, ok)
			}
			if id, ok := m.settingsManager().GetDefaultModel(); !ok || id != tt.want {
				t.Fatalf("persisted default model = %q (ok=%v), want %q", id, ok, tt.want)
			}
		})
	}
}

func TestPostLoginDiscoveryReportsEmptyCatalogOnlyAfterRefresh(t *testing.T) {
	m := newLoginTestMode(t)
	session := m.session()
	unknown := installUnknownModel(session)
	counter := &countingRenderTUI{}
	m.ui = counter
	started, release := blockAfterStart(t)

	m.completeProviderAuthentication(context.Background(), "radius", "Radius", auth.AuthTypeOAuth, unknown)
	<-started
	// Before the refresh resolves the goroutine is parked inside the stub:
	// reading session state and the container here is race-free.
	if session.Model() != unknown {
		t.Fatalf("model changed before refresh resolved: %+v", session.Model())
	}
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
	if !strings.Contains(rendered, "Credentials saved") {
		t.Fatalf("missing credentials status, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "no models are available") {
		t.Fatalf("empty-catalog error shown before refresh resolved, got:\n%s", rendered)
	}

	close(release)
	// The counter is not a reliable completion signal (showStatus inside the
	// goroutine's finishAuthentication bumps it mid-tail) — poll under
	// mutateUI, which serializes the render with the goroutine's mutations
	// (2026-09-26 race: plain renderAllChildren raced its AddChild).
	waitFor(t, 2*time.Second, "empty-catalog status to render", func() bool {
		m.mutateUI(func() {
			rendered = normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
		})
		return strings.Contains(rendered, "no models are available")
	})
	if !strings.Contains(rendered, "no models are available") {
		t.Fatalf("empty-catalog error missing after refresh, got:\n%s", rendered)
	}
	if session.Model() != unknown {
		t.Fatalf("SetModel was called on an empty catalog: %+v", session.Model())
	}
}

func TestPostLoginDiscoveryPreservesModelSelectedDuringRefresh(t *testing.T) {
	m := newLoginTestMode(t)
	session := m.session()
	unknown := installUnknownModel(session)
	counter := &countingRenderTUI{}
	m.ui = counter
	started, release := blockAfterStart(t)

	m.completeProviderAuthentication(context.Background(), "radius", "Radius", auth.AuthTypeOAuth, unknown)
	<-started
	registerTestProvider(t, session, "radius", discoveryModels("fast", "balanced"))
	var fast *ai.Model
	for _, model := range session.GetModelRuntime().GetAvailableSnapshot() {
		if model.ID == "fast" {
			picked := model
			fast = &picked
			break
		}
	}
	if fast == nil {
		t.Fatal("fast model not registered into the snapshot")
	}
	if err := session.SetModel(context.Background(), fast, nil); err != nil {
		t.Fatalf("SetModel during refresh: %v", err)
	}

	baseline := counter.requests.Load()
	close(release)
	// The goroutine's final RequestRender signals it passed the decision point.
	waitFor(t, 2*time.Second, "refresh goroutine to finish", func() bool {
		return counter.requests.Load() > baseline
	})
	if session.Model() != fast {
		t.Fatalf("selected model was replaced: %+v", session.Model())
	}
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
	if strings.Contains(rendered, "no models are available") || strings.Contains(rendered, "Selected ") {
		t.Fatalf("discovery overrode the user selection, got:\n%s", rendered)
	}
}

func TestPostLoginDiscoveryBoundsRefreshTo15Seconds(t *testing.T) {
	m := newLoginTestMode(t)
	session := m.session()
	unknown := installUnknownModel(session)
	counter := &countingRenderTUI{}
	m.ui = counter

	deadlineDelta := make(chan time.Duration, 1)
	stubModelRuntimeRefresh(t, func(ctx context.Context, _ *core.ModelRuntime, _ *ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
		if deadline, ok := ctx.Deadline(); ok {
			deadlineDelta <- time.Until(deadline)
		} else {
			deadlineDelta <- -1
		}
		return ai.ModelsRefreshResult{Aborted: true}
	})

	// Baseline before the call: the goroutine's renders strictly follow the
	// synchronous ones, so this cannot miss its completion signal.
	m.completeProviderAuthentication(context.Background(), "stalled-login", "Stalled Login", auth.AuthTypeAPIKey, unknown)

	delta := <-deadlineDelta
	if delta < 14500*time.Millisecond || delta > 15500*time.Millisecond {
		t.Fatalf("refresh deadline = %s, want ~15s", delta)
	}
	// The goroutine's ShowWarning lands after the stub returns, and the
	// pre-goroutine showStatus already bumped the render counter, so the
	// counter cannot serve as a completion signal here — poll under
	// mutateUI, which serializes the render with the goroutine's AddChild
	// calls (2026-09-26 race: plain renderAllChildren raced them).
	var rendered string
	waitFor(t, 2*time.Second, "timeout warning to render", func() bool {
		m.mutateUI(func() {
			rendered = normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
		})
		return strings.Contains(rendered, "timed out; using cached models")
	})
	if !strings.Contains(rendered, "timed out; using cached models") {
		t.Fatalf("timeout warning missing, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Saved API key for Stalled Login, but its model catalog refresh timed out; using cached models.") {
		t.Fatalf("missing timeout warning, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "no default model is configured") {
		t.Fatalf("missing no-default selection error, got:\n%s", rendered)
	}
	if session.Model() != unknown {
		t.Fatalf("SetModel was called: %+v", session.Model())
	}
}

func TestMaybeWarnAboutAnthropicSubscriptionAuthGates(t *testing.T) {
	// Settings gate (pi: getWarnings().anthropicExtraUsage === false skips
	// the check entirely) and provider gate (non-anthropic models are
	// no-ops). Both hit early returns, so no auth fixtures are needed.
	disabled := false
	m := newLoginTestMode(t)
	m.settingsManager().SetWarnings(core.WarningSettings{AnthropicExtraUsage: &disabled})
	m.maybeWarnAboutAnthropicSubscriptionAuth(nil)
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 200))
	if strings.Contains(rendered, "Anthropic subscription auth is active") {
		t.Fatalf("warning shown while anthropicExtraUsage=false:\n%s", rendered)
	}

	m2 := newLoginTestMode(t)
	m2.maybeWarnAboutAnthropicSubscriptionAuth(&ai.Model{ID: "m", Provider: "deepseek"})
	rendered2 := normalizeRenderedOutput(renderAllChildren(m2.chatContainer, 200))
	if strings.Contains(rendered2, "Anthropic subscription auth is active") {
		t.Fatalf("warning shown for non-anthropic provider:\n%s", rendered2)
	}
}
