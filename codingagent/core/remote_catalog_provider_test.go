// Ported from pi's test/remote-catalog-provider.test.ts. The global fetch
// mock becomes an httptest server (catalogBaseURL is injectable); the
// stalled-request test drives pig/ai Models' generation-checked refresh.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/samber/lo"
)

type notUsedStreams struct{}

func (notUsedStreams) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("not used")
}

func (notUsedStreams) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("not used")
}

type stubCatalogAuth struct{}

func (stubCatalogAuth) Name() string { return "Test" }

func (stubCatalogAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	return &auth.AuthResult{}, nil
}

func catalogTestModel(id string) ai.Model {
	return ai.Model{
		ID:            id,
		Name:          id,
		API:           ai.ApiOpenAICompletions,
		Provider:      "test-provider",
		BaseURL:       "https://example.test/v1",
		Reasoning:     false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

// catalogModelJSON is the wire shape of catalogTestModel(id).
func catalogModelJSON(id string) string {
	content, err := json.Marshal(catalogTestModel(id))
	if err != nil {
		panic(err)
	}
	return string(content)
}

// catalogKeyedJSON renders a keyed catalog body ({key: model}), the shape
// pi's test responses use.
func catalogKeyedJSON(key, id string) string {
	content, err := json.Marshal(map[string]any{key: catalogTestModel(id)})
	if err != nil {
		panic(err)
	}
	return string(content)
}

func testProvider(catalogBaseURL string, localGeneratedAt *int64) ai.Provider {
	base := ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "test-provider",
		Auth:   auth.ProviderAuth{APIKey: stubCatalogAuth{}},
		Models: []ai.Model{catalogTestModel("static")},
		API:    notUsedStreams{},
	})
	return WithRemoteCatalog(base, catalogBaseURL, localGeneratedAt)
}

type catalogRefresher interface {
	RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error
}

// refreshProvider drives one refresh phase directly (pi: refreshProvider
// helper with a publish callback over an in-memory store).
func refreshProvider(t *testing.T, provider ai.Provider, store ai.ModelsStore, force bool) error {
	t.Helper()
	ctx := context.Background()
	providerID := provider.ID()
	publish := func(publication ai.ModelsPublication) (bool, error) {
		if publication.DeletePersist {
			if err := store.Delete(ctx, providerID); err != nil {
				return false, err
			}
		} else if publication.Persist != nil {
			if err := store.Write(ctx, providerID, publication.Persist); err != nil {
				return false, err
			}
		}
		if publication.Update != nil {
			publication.Update()
		}
		return true, nil
	}
	stored, err := store.Read(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	refresher, ok := provider.(catalogRefresher)
	if !ok {
		t.Fatal("provider does not support RefreshModels")
	}
	return refresher.RefreshModels(ctx, &ai.RefreshModelsContext{
		Credential:   &auth.ApiKeyCredential{},
		Stored:       stored,
		Publish:      publish,
		AllowNetwork: true,
		Force:        force,
		Context:      ctx,
	})
}

func mustReadStore(t *testing.T, store ai.ModelsStore, providerID string) *ai.ModelsStoreEntry {
	t.Helper()
	entry, err := store.Read(context.Background(), providerID)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

type catalogResponse struct {
	status  int
	body    string
	headers map[string]string
	// hold, when non-nil, blocks the response until closed (pi: a pending
	// promise stood in for this via the fetch mock).
	hold chan struct{}
}

// catalogServer serves a queued response series and records request headers.
type catalogServer struct {
	*httptest.Server
	mu     sync.Mutex
	calls  int
	logged []http.Header
}

func newCatalogServer(t *testing.T, responses []catalogResponse) *catalogServer {
	t.Helper()
	s := &catalogServer{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		call := s.calls
		s.calls++
		var response catalogResponse
		if call < len(responses) {
			response = responses[call]
		} else if len(responses) > 0 {
			// pi's fetch mocks create fresh responses per call; repeating the
			// last queued response gives the same unbounded behavior.
			response = responses[len(responses)-1]
		}
		s.logged = append(s.logged, r.Header.Clone())
		s.mu.Unlock()
		if response.hold != nil {
			<-response.hold
		}
		for key, value := range response.headers {
			w.Header().Set(key, value)
		}
		w.WriteHeader(response.status)
		if response.body != "" {
			_, _ = w.Write([]byte(response.body))
		}
	}
	s.Server = httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(s.Server.Close)
	return s
}

func (s *catalogServer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *catalogServer) requestHeader(call int, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if call >= len(s.logged) {
		return ""
	}
	return s.logged[call].Get(key)
}

func TestRemoteCatalogParsesKeyedCatalogsSendsVersionHeadersObservesTTLAndSupportsForcedRefreshes(t *testing.T) {
	body, err := json.Marshal(map[string]any{"dynamic": catalogTestModel("dynamic")})
	if err != nil {
		t.Fatal(err)
	}
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: string(body), headers: map[string]string{"content-type": "application/json"}},
	})

	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}
	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if err := refreshProvider(t, provider, store, true); err != nil {
		t.Fatal(err)
	}

	got := modelIDs(provider.GetModels())
	if len(got) != 2 || got[0] != "static" || got[1] != "dynamic" {
		t.Fatalf("models = %v, want [static dynamic]", got)
	}
	stored := mustReadStore(t, store, provider.ID())
	if got := modelIDs(stored.Models); len(got) != 1 || got[0] != "dynamic" {
		t.Fatalf("stored = %v, want [dynamic]", got)
	}
	if calls := server.callCount(); calls != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
	if ua := server.requestHeader(0, "User-Agent"); !strings.Contains(ua, fmt.Sprintf("pi/%s", codingagent.Version)) {
		t.Fatalf("user agent = %q, want prefix pi/%s", ua, codingagent.Version)
	}
}

func TestRemoteCatalogPrefersTheNewerOfTheGeneratedAndRemoteCatalogs(t *testing.T) {
	localGeneratedAt := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).UnixMilli()
	older := catalogKeyedJSON("old", "old")
	newer := catalogKeyedJSON("newer", "newer")
	newerHeader := time.UnixMilli(localGeneratedAt + 60_000).UTC().Format(http.TimeFormat)
	olderHeader := time.UnixMilli(localGeneratedAt - 60_000).UTC().Format(http.TimeFormat)
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: older, headers: map[string]string{"last-modified": olderHeader}},
		{status: 200, body: newer, headers: map[string]string{"last-modified": newerHeader}},
	})
	provider := testProvider(server.URL, &localGeneratedAt)
	store := &ai.InMemoryModelsStore{}

	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 1 || got[0] != "static" {
		t.Fatalf("models = %v, want [static]", got)
	}

	if err := refreshProvider(t, provider, store, true); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 2 || got[0] != "static" || got[1] != "newer" {
		t.Fatalf("models = %v, want [static newer]", got)
	}
	stored := mustReadStore(t, store, provider.ID())
	if stored.LastModified == nil || *stored.LastModified != time.UnixMilli(localGeneratedAt+60_000).UnixMilli() {
		t.Fatalf("lastModified = %v, want %d", stored.LastModified, localGeneratedAt+60_000)
	}
}

func TestRemoteCatalogRevalidatesAStoredCatalogWithItsEtagAndKeepsTheOverlayOn304(t *testing.T) {
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: catalogKeyedJSON("dynamic", "dynamic"), headers: map[string]string{"content-type": "application/json", "etag": `"catalog-1"`}},
		{status: 304, headers: map[string]string{"etag": `"catalog-1"`}},
	})
	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}

	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if validator := server.requestHeader(0, "If-None-Match"); validator != "" {
		t.Fatalf("first request sent if-none-match %q", validator)
	}
	stored := mustReadStore(t, store, provider.ID())
	if stored.ETag != `"catalog-1"` {
		t.Fatalf("etag = %q, want \"catalog-1\"", stored.ETag)
	}
	checkedAt := *stored.CheckedAt

	if err := refreshProvider(t, provider, store, true); err != nil {
		t.Fatal(err)
	}
	if validator := server.requestHeader(1, "If-None-Match"); validator != `"catalog-1"` {
		t.Fatalf("if-none-match = %q, want \"catalog-1\"", validator)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 2 || got[0] != "static" || got[1] != "dynamic" {
		t.Fatalf("models = %v, want [static dynamic]", got)
	}
	stored = mustReadStore(t, store, provider.ID())
	if got := modelIDs(stored.Models); len(got) != 1 || got[0] != "dynamic" {
		t.Fatalf("stored = %v, want [dynamic]", got)
	}
	if stored.ETag != `"catalog-1"` {
		t.Fatalf("etag = %q, want \"catalog-1\"", stored.ETag)
	}
	if stored.CheckedAt == nil || *stored.CheckedAt < checkedAt {
		t.Fatalf("checkedAt = %v, want >= %d", stored.CheckedAt, checkedAt)
	}
}

func TestRemoteCatalogDropsAStaleEtagWhenTheOverlayBecomesUnavailable(t *testing.T) {
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: catalogKeyedJSON("dynamic", "dynamic"), headers: map[string]string{"content-type": "application/json", "etag": `"catalog-1"`}},
		{status: 501, body: "not implemented"},
	})
	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}

	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if err := refreshProvider(t, provider, store, true); err != nil {
		t.Fatal(err)
	}
	stored := mustReadStore(t, store, provider.ID())
	if stored.ETag != "" {
		t.Fatalf("etag = %q, want empty", stored.ETag)
	}
}

func TestRemoteCatalogKeepsTheEtagAndOverlayAfterATransientFailure(t *testing.T) {
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: catalogKeyedJSON("dynamic", "dynamic"), headers: map[string]string{"content-type": "application/json", "etag": `"catalog-1"`}},
		{status: 429, body: "rate limited"},
		{status: 429, body: "rate limited"},
		{status: 429, body: "rate limited"},
		{status: 304, headers: map[string]string{"etag": `"catalog-1"`}},
	})
	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}

	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	err := refreshProvider(t, provider, store, true)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v, want 429 failure", err)
	}

	stored := mustReadStore(t, store, provider.ID())
	if stored.ETag != `"catalog-1"` {
		t.Fatalf("etag = %q, want \"catalog-1\"", stored.ETag)
	}
	if got := modelIDs(stored.Models); len(got) != 1 || got[0] != "dynamic" {
		t.Fatalf("stored = %v, want [dynamic]", got)
	}

	if err := refreshProvider(t, provider, store, true); err != nil {
		t.Fatal(err)
	}
	if validator := server.requestHeader(4, "If-None-Match"); validator != `"catalog-1"` {
		t.Fatalf("if-none-match = %q, want \"catalog-1\"", validator)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 2 || got[0] != "static" || got[1] != "dynamic" {
		t.Fatalf("models = %v, want [static dynamic]", got)
	}
}

func TestRemoteCatalogLetsANewerCatalogRequestBypassAStalledOlderRequestWithoutStalePublication(t *testing.T) {
	var once sync.Once
	finishFirst := make(chan struct{})
	closeFinishFirst := func() { once.Do(func() { close(finishFirst) }) }
	// The stalled handler must be released even on failure, or the server
	// cleanup would block on the outstanding request.
	t.Cleanup(closeFinishFirst)
	server := newCatalogServer(t, []catalogResponse{
		{status: 200, body: catalogKeyedJSON("older", "older"), headers: map[string]string{"content-type": "application/json"}, hold: finishFirst},
		{status: 200, body: catalogKeyedJSON("newer", "newer"), headers: map[string]string{"content-type": "application/json"}},
	})

	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}
	models := ai.NewModels(&ai.CreateModelsOptions{ModelsStore: store})
	models.SetProvider(provider)

	providerID := provider.ID()
	firstDone := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		firstDone <- models.Refresh(context.Background(), &ai.ModelsRefreshOptions{Providers: []string{providerID}, Force: true})
	}()
	// Wait until the first refresh's request has reached the stalled handler
	// before starting the second (pi: await firstStarted).
	waitDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitDeadline) && server.callCount() == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if server.callCount() == 0 {
		t.Fatal("first refresh never reached the stalled handler")
	}

	second := models.Refresh(context.Background(), &ai.ModelsRefreshOptions{Providers: []string{providerID}, Force: true})
	if len(second.Errors) > 0 {
		t.Fatalf("second refresh errors: %v", second.Errors)
	}
	closeFinishFirst()
	first := <-firstDone
	if len(first.Errors) > 0 {
		t.Fatalf("first refresh errors: %v", first.Errors)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 2 || got[0] != "static" || got[1] != "newer" {
		t.Fatalf("models = %v, want [static newer]", got)
	}

	closeFinishFirst()
	time.Sleep(20 * time.Millisecond)
	if got := modelIDs(provider.GetModels()); len(got) != 2 || got[0] != "static" || got[1] != "newer" {
		t.Fatalf("models after stalled response = %v, want [static newer]", got)
	}
	stored := mustReadStore(t, store, providerID)
	if got := modelIDs(stored.Models); len(got) != 1 || got[0] != "newer" {
		t.Fatalf("stored = %v, want [newer]", got)
	}
}

func TestRemoteCatalogTreatsUnimplementedPiDevCatalogRoutesAsAnUnavailableOverlay(t *testing.T) {
	server := newCatalogServer(t, []catalogResponse{
		{status: 501, body: "not implemented"},
	})
	provider := testProvider(server.URL, nil)
	store := &ai.InMemoryModelsStore{}

	if err := refreshProvider(t, provider, store, false); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 1 || got[0] != "static" {
		t.Fatalf("models = %v, want [static]", got)
	}
	stored := mustReadStore(t, store, provider.ID())
	if len(stored.Models) != 0 {
		t.Fatalf("stored models = %v, want empty", stored.Models)
	}
	if stored.CheckedAt == nil {
		t.Fatal("checkedAt missing")
	}
}

// refreshProviderPhase drives one refresh phase with an explicit allowNetwork
// flag (pi: refreshProvider with { allowNetwork }).
func refreshProviderPhase(t *testing.T, provider ai.Provider, store ai.ModelsStore, allowNetwork bool, force bool) error {
	t.Helper()
	ctx := context.Background()
	providerID := provider.ID()
	publish := func(publication ai.ModelsPublication) (bool, error) {
		if publication.DeletePersist {
			if err := store.Delete(ctx, providerID); err != nil {
				return false, err
			}
		} else if publication.Persist != nil {
			if err := store.Write(ctx, providerID, publication.Persist); err != nil {
				return false, err
			}
		}
		if publication.Update != nil {
			publication.Update()
		}
		return true, nil
	}
	stored, err := store.Read(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	refresher, ok := provider.(catalogRefresher)
	if !ok {
		t.Fatal("provider does not support RefreshModels")
	}
	return refresher.RefreshModels(ctx, &ai.RefreshModelsContext{
		Credential:   &auth.ApiKeyCredential{},
		Stored:       stored,
		Publish:      publish,
		AllowNetwork: allowNetwork,
		Force:        force,
		Context:      ctx,
	})
}

func personalizedBuiltinProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "test-provider",
		Auth:   auth.ProviderAuth{APIKey: stubCatalogAuth{}},
		Models: []ai.Model{catalogTestModel("static")},
		API:    notUsedStreams{},
	})
}

// TestPersonalizedCatalogKeepsTheSavedListWhenPersonalizationFails pins that a
// failed personalization leaves the saved list in place and saves nothing (pi:
// "keeps the saved list and saves nothing when personalization fails").
func TestPersonalizedCatalogKeepsTheSavedListWhenPersonalizationFails(t *testing.T) {
	server := newCatalogServer(t, []catalogResponse{{status: http.StatusNotFound, body: "not found"}})
	store := &ai.InMemoryModelsStore{}
	saved := &ai.ModelsStoreEntry{Models: []ai.Model{catalogTestModel("allowed")}, CheckedAt: lo.ToPtr(int64(1))}
	if err := store.Write(context.Background(), "test-provider", saved); err != nil {
		t.Fatal(err)
	}
	personalize := func(catalog []ai.Model, refreshCtx *ai.RefreshModelsContext) ([]ai.Model, error) {
		return nil, fmt.Errorf("user models request failed")
	}
	provider := WithPersonalizedCatalog(personalizedBuiltinProvider(), personalize, server.URL, nil)

	err := refreshProviderPhase(t, provider, store, true, false)
	if err == nil || !strings.Contains(err.Error(), "user models request failed") {
		t.Fatalf("err = %v, want user models request failed", err)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 1 || got[0] != "allowed" {
		t.Fatalf("models = %v, want [allowed]", got)
	}
	stored := mustReadStore(t, store, provider.ID())
	if len(stored.Models) != 1 || stored.Models[0].ID != "allowed" || stored.CheckedAt == nil || *stored.CheckedAt != 1 {
		t.Fatalf("stored = %+v, want the saved list unchanged", stored)
	}
}

// TestPersonalizedCatalogRestoresSavedListAndFallsBackWhenEmpty pins the
// offline restore behavior (pi: "restores a saved list as is, and falls back
// to the built-in models only when it is empty").
func TestPersonalizedCatalogRestoresSavedListAndFallsBackWhenEmpty(t *testing.T) {
	personalize := func(catalog []ai.Model, refreshCtx *ai.RefreshModelsContext) ([]ai.Model, error) {
		return nil, fmt.Errorf("not used offline")
	}
	store := &ai.InMemoryModelsStore{}
	provider := WithPersonalizedCatalog(personalizedBuiltinProvider(), personalize, "", nil)

	// Older pig versions stored the plain pi.dev catalog here, and saved an
	// empty list after a missing pi.dev catalog or a failed first request.
	// Only such an entry can be empty, so an empty list falls back to the
	// built-in models.
	if err := store.Write(context.Background(), provider.ID(), &ai.ModelsStoreEntry{Models: []ai.Model{}, CheckedAt: lo.ToPtr(int64(1))}); err != nil {
		t.Fatal(err)
	}
	if err := refreshProviderPhase(t, provider, store, false, false); err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(provider.GetModels()); len(got) != 1 || got[0] != "static" {
		t.Fatalf("models = %v, want [static]", got)
	}

	// A key that may use no chat models keeps its image models and shows no
	// chat models.
	imageModel := ai.Model{
		Type:     ai.ModelTypeImage,
		ID:       "image",
		Name:     "image",
		API:      ai.ImageApiOpenRouter,
		Provider: "test-provider",
		BaseURL:  "https://example.test/v1",
		Input:    []ai.ModelInput{ai.ModelInputText},
		Output:   []ai.ModelInput{ai.ModelInputImage},
	}
	if err := store.Write(context.Background(), provider.ID(), &ai.ModelsStoreEntry{Models: []ai.Model{imageModel}, CheckedAt: lo.ToPtr(int64(1))}); err != nil {
		t.Fatal(err)
	}
	if err := refreshProviderPhase(t, provider, store, false, false); err != nil {
		t.Fatal(err)
	}
	if got := provider.GetModels(); len(got) != 0 {
		t.Fatalf("chat models = %v, want empty", got)
	}
	all, ok := provider.(interface{ GetAllModels() []ai.Model })
	if !ok {
		t.Fatal("provider does not expose GetAllModels")
	}
	if got := modelIDs(all.GetAllModels()); len(got) != 1 || got[0] != "image" {
		t.Fatalf("all models = %v, want [image]", got)
	}
}
