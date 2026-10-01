// Ported from pi's test/model-runtime-credential-sync.test.ts. pi's
// AbortSignal-based assertions use contexts; the error-shape assertions use
// errors.As on CredentialSynchronizationError.
package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

type syncTestProvider struct {
	id   string
	auth auth.ProviderAuth
}

func (p *syncTestProvider) ID() string                     { return p.id }
func (p *syncTestProvider) Name() string                   { return p.id }
func (p *syncTestProvider) BaseURL() string                { return "" }
func (p *syncTestProvider) Headers() types.ProviderHeaders { return nil }
func (p *syncTestProvider) Auth() auth.ProviderAuth        { return p.auth }
func (p *syncTestProvider) GetModels() []ai.Model {
	return []ai.Model{{
		ID: "dynamic", Name: "Dynamic", API: ai.ApiOpenAICompletions, Provider: ai.ProviderID(p.id),
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
		ContextWindow: 1000, MaxTokens: 100,
	}}
}
func (p *syncTestProvider) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}
func (p *syncTestProvider) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}

// syncTestAPIKeyAuth is the provider stub's api-key method with overridable
// login/check hooks.
type syncTestAPIKeyAuth struct {
	id        string
	mu        *sync.Mutex
	loginHook func() (auth.Credential, error)
	checkHook func(credential *auth.ApiKeyCredential) (*auth.AuthCheck, error)
}

func (a *syncTestAPIKeyAuth) Name() string { return "API key" }

func (a *syncTestAPIKeyAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	a.mu.Lock()
	hook := a.loginHook
	a.mu.Unlock()
	if hook != nil {
		credential, err := hook()
		if err != nil {
			return nil, err
		}
		return credential.(*auth.ApiKeyCredential), nil
	}
	// pi: login: async () => ({ type: "api_key", key: `${id}-key` }).
	return &auth.ApiKeyCredential{Key: a.id + "-key"}, nil
}

func (a *syncTestAPIKeyAuth) Check(input auth.ApiKeyAuthInput) (*auth.AuthCheck, error) {
	a.mu.Lock()
	hook := a.checkHook
	a.mu.Unlock()
	if hook != nil {
		return hook(input.Credential)
	}
	if input.Credential != nil {
		return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "stored"}, nil
	}
	return nil, nil
}

func (a *syncTestAPIKeyAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	if input.Credential != nil {
		return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
	}
	return nil, nil
}

func newSyncProvider(id string, mu *sync.Mutex, hooks *providerHooks) *syncTestProvider {
	apiKey := &syncTestAPIKeyAuth{id: id, mu: mu}
	if hooks != nil {
		apiKey.loginHook = hooks.login
		apiKey.checkHook = hooks.check
	}
	return &syncTestProvider{id: id, auth: auth.ProviderAuth{APIKey: apiKey}}
}

type providerHooks struct {
	login func() (auth.Credential, error)
	check func(credential *auth.ApiKeyCredential) (*auth.AuthCheck, error)
}

func runtimeWithSyncProvider(t *testing.T, registered *syncTestProvider, credentials auth.CredentialStore, mu *sync.Mutex, hooks *providerHooks) *ModelRuntime {
	t.Helper()
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	if err := runtime.RegisterNativeProvider(registered); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{registered.id}})
	return runtime
}

var noopAuthInteraction = &noopInteraction{}

type noopInteraction struct{}

func (i *noopInteraction) Prompt(prompt auth.AuthPrompt) (string, error) { return "unused", nil }
func (i *noopInteraction) Notify(event auth.AuthEvent)                   {}

func TestModelRuntimePublishesLocallyConsistentAvailabilityBeforeLoginAndLogoutResolve(t *testing.T) {
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := runtimeWithSyncProvider(t, newSyncProvider("dynamic", &sync.Mutex{}, nil), credentials, &sync.Mutex{}, nil)
	ctx := context.Background()

	if _, err := runtime.Login(ctx, "dynamic", auth.AuthTypeAPIKey, noopAuthInteraction, nil); err != nil {
		t.Fatal(err)
	}
	if !runtime.HasConfiguredAuth("dynamic") {
		t.Fatal("dynamic should be configured after login")
	}
	snapshotIDs := map[string]bool{}
	for _, model := range runtime.GetAvailableSnapshot() {
		snapshotIDs[model.ID] = true
	}
	if !snapshotIDs["dynamic"] {
		t.Fatal("available snapshot missing dynamic")
	}
	stored, err := credentials.Read(ctx, "dynamic")
	if err != nil {
		t.Fatal(err)
	}
	apiKey, ok := stored.(*auth.ApiKeyCredential)
	if !ok || apiKey.Key != "dynamic-key" {
		t.Fatalf("stored = %+v, want dynamic-key", stored)
	}

	if err := runtime.Logout(ctx, "dynamic"); err != nil {
		t.Fatal(err)
	}
	if runtime.HasConfiguredAuth("dynamic") {
		t.Fatal("dynamic should be unconfigured after logout")
	}
	for _, model := range runtime.GetAvailableSnapshot() {
		if string(model.Provider) == "dynamic" {
			t.Fatalf("available snapshot still has dynamic provider: %+v", model)
		}
	}
	stored, err = credentials.Read(ctx, "dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if stored != nil {
		t.Fatalf("stored = %+v, want nil", stored)
	}
}

func TestModelRuntimeOrdersSameProviderCredentialOperationsThroughLocalSynchronization(t *testing.T) {
	var mu sync.Mutex
	started := make(chan struct{})
	blocked := make(chan struct{})
	hooks := &providerHooks{
		login: func() (auth.Credential, error) {
			mu.Lock()
			firstStart := started
			mu.Unlock()
			closeOnce(firstStart)
			<-blocked
			return &auth.ApiKeyCredential{Key: "ordered-key"}, nil
		},
	}
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := runtimeWithSyncProvider(t, newSyncProvider("ordered", &mu, hooks), credentials, &mu, hooks)

	ctx := context.Background()
	loginDone := make(chan error, 1)
	go func() {
		_, err := runtime.Login(ctx, "ordered", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
		loginDone <- err
	}()
	<-started
	logoutDone := make(chan error, 1)
	go func() { logoutDone <- runtime.Logout(ctx, "ordered") }()
	time.Sleep(20 * time.Millisecond)
	stored, err := credentials.Read(ctx, "ordered")
	if err != nil || stored != nil {
		t.Fatalf("stored during login = %+v, %v, want nil", stored, err)
	}

	close(blocked)
	if err := <-loginDone; err != nil {
		t.Fatal(err)
	}
	if err := <-logoutDone; err != nil {
		t.Fatal(err)
	}
	stored, err = credentials.Read(ctx, "ordered")
	if err != nil || stored != nil {
		t.Fatalf("stored after logout = %+v, %v, want nil", stored, err)
	}
	if runtime.HasConfiguredAuth("ordered") {
		t.Fatal("ordered should be unconfigured")
	}
}

var onceDo = map[chan struct{}]*sync.Once{}
var onceMu sync.Mutex

func closeOnce(ch chan struct{}) {
	onceMu.Lock()
	once := onceDo[ch]
	if once == nil {
		once = &sync.Once{}
		onceDo[ch] = once
	}
	onceMu.Unlock()
	once.Do(func() { close(ch) })
}

func TestModelRuntimeAllowsDifferentProvidersToRunCredentialFlowsConcurrently(t *testing.T) {
	var mu sync.Mutex
	blocked := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	hooksOne := &providerHooks{
		login: func() (auth.Credential, error) {
			closeOnce(firstStarted)
			<-blocked
			return &auth.ApiKeyCredential{Key: "one"}, nil
		},
	}
	hooksTwo := &providerHooks{
		login: func() (auth.Credential, error) {
			closeOnce(secondStarted)
			<-blocked
			return &auth.ApiKeyCredential{Key: "two"}, nil
		},
	}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterNativeProvider(newSyncProvider("one", &mu, hooksOne)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(newSyncProvider("two", &mu, hooksTwo)); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{"one", "two"}})

	ctx := context.Background()
	oneDone := make(chan error, 1)
	twoDone := make(chan error, 1)
	go func() {
		_, err := runtime.Login(ctx, "one", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
		oneDone <- err
	}()
	go func() {
		_, err := runtime.Login(ctx, "two", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
		twoDone <- err
	}()

	waitAll(t, 2*time.Second, firstStarted, secondStarted)
	close(blocked)
	if err := <-oneDone; err != nil {
		t.Fatal(err)
	}
	if err := <-twoDone; err != nil {
		t.Fatal(err)
	}
}

func waitAll(t *testing.T, timeout time.Duration, channels ...chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for _, ch := range channels {
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
			t.Fatal("channel not closed before deadline")
		}
	}
}

func TestModelRuntimeDoesNotWaitForUnrelatedProviderAvailabilityDuringLocalSynchronization(t *testing.T) {
	var mu sync.Mutex
	stallUnrelated := false
	hooksTarget := &providerHooks{}
	hooksUnrelated := &providerHooks{
		check: func(credential *auth.ApiKeyCredential) (*auth.AuthCheck, error) {
			mu.Lock()
			stall := stallUnrelated
			mu.Unlock()
			if stall {
				<-make(chan struct{}) // block forever (pi: never-resolving promise)
			}
			return nil, nil
		},
	}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterNativeProvider(newSyncProvider("target", &mu, hooksTarget)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(newSyncProvider("unrelated", &mu, hooksUnrelated)); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{"target", "unrelated"}})
	mu.Lock()
	stallUnrelated = true
	mu.Unlock()

	ctx := context.Background()
	if _, err := runtime.Login(ctx, "target", auth.AuthTypeAPIKey, noopAuthInteraction, nil); err != nil {
		t.Fatal(err)
	}
	if !runtime.HasConfiguredAuth("target") {
		t.Fatal("target should be configured")
	}
	result := runtime.Refresh(ctx, &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{"target"}})
	if result.Aborted {
		t.Fatal("target-scoped refresh aborted unexpectedly")
	}
}

func TestModelRuntimeReportsCancellationThatOccursDuringProviderScopedAvailability(t *testing.T) {
	var mu sync.Mutex
	blockAvailability := false
	started := make(chan struct{})
	hooks := &providerHooks{
		check: func(credential *auth.ApiKeyCredential) (*auth.AuthCheck, error) {
			mu.Lock()
			block := blockAvailability
			mu.Unlock()
			if block {
				closeOnce(started)
				<-make(chan struct{})
			}
			if credential != nil {
				return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "stored"}, nil
			}
			return nil, nil
		},
	}
	registered := newSyncProvider("cancelled-availability", &mu, hooks)
	runtime := runtimeWithSyncProvider(t, registered, NewInMemoryAuthStorage(authStorageData{}), &mu, hooks)
	ctx := context.Background()
	if err := runtime.SetRuntimeApiKey(ctx, registered.ID(), "key"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	blockAvailability = true
	mu.Unlock()

	refreshCtx, cancel := context.WithCancel(context.Background())
	refreshDone := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		refreshDone <- runtime.Refresh(refreshCtx, &ai.ModelsRefreshOptions{
			AllowNetwork: lo.ToPtr(false),
			Providers:    []string{registered.ID()},
		})
	}()
	<-started
	cancel()
	result := <-refreshDone
	if !result.Aborted {
		t.Fatal("refresh should report aborted")
	}
}

func TestModelRuntimeDoesNotRunNetworkRefreshInsideTheCredentialOperationChain(t *testing.T) {
	var mu sync.Mutex
	networkCalls := 0
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	registered := &recordingDynamicProvider{
		id: "local-only", mu: &mu, networkCalls: &networkCalls, loginKey: "local-only-key",
	}
	if err := runtime.RegisterNativeProvider(registered); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{registered.ID()}})

	ctx := context.Background()
	if _, err := runtime.Login(ctx, "local-only", auth.AuthTypeAPIKey, noopAuthInteraction, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	calls := networkCalls
	mu.Unlock()
	if calls != 0 {
		t.Fatalf("network refresh calls = %d, want 0", calls)
	}
	if !runtime.HasConfiguredAuth("local-only") {
		t.Fatal("local-only should be configured")
	}
}

func TestModelRuntimeKeepsProviderScopedRefreshesFromSupersedingUnrelatedProviders(t *testing.T) {
	var mu sync.Mutex
	blocked := make(chan struct{})
	firstStarted := make(chan struct{})
	var firstCtx context.Context
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: credentials,
		ModelsPath:  lo.ToPtr(""),
	})
	one := &recordingDynamicProvider{
		id: "one", mu: &mu, loginKey: "one-key",
		started: firstStarted, blocked: blocked, ctxOut: &firstCtx,
	}
	two := &recordingDynamicProvider{id: "two", mu: &mu, loginKey: "two-key"}
	if err := runtime.RegisterNativeProvider(one); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(two); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{"one", "two"}})
	if err := runtime.SetRuntimeApiKey(context.Background(), "one", "one-key"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetRuntimeApiKey(context.Background(), "two", "two-key"); err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan ai.ModelsRefreshResult, 1)
	go func() {
		firstDone <- runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(true), Providers: []string{"one"}})
	}()
	<-firstStarted
	// A refresh scoped to "two" must not supersede the stalled "one" refresh.
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(true), Providers: []string{"two"}})
	mu.Lock()
	aborted := firstCtx != nil && firstCtx.Err() != nil
	mu.Unlock()
	if aborted {
		t.Fatal("the one refresh context was superseded by a two-scoped refresh")
	}

	close(blocked)
	result := <-firstDone
	if result.Aborted {
		t.Fatal("first refresh reported aborted")
	}
}

func TestModelRuntimeWaitsForACommittedCredentialMutationToSettleBeforeReportingCancellation(t *testing.T) {
	var mu sync.Mutex
	var stored auth.Credential
	committed := make(chan struct{})
	mutationFinished := make(chan struct{})
	credentials := &delayedCommitStore{
		committed:        committed,
		mutationFinished: mutationFinished,
		mu:               &mu,
		stored:           &stored,
	}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: credentials,
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterNativeProvider(newSyncProvider("delayed-commit", &sync.Mutex{}, nil)); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{"delayed-commit"}})

	loginCtx, cancel := context.WithCancel(context.Background())
	loginDone := make(chan error, 1)
	settled := false
	go func() {
		_, err := runtime.Login(loginCtx, "delayed-commit", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
		mu.Lock()
		settled = true
		mu.Unlock()
		loginDone <- err
	}()
	<-committed
	cancel()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	isSettled := settled
	mu.Unlock()
	if isSettled {
		t.Fatal("login settled before the mutation finished")
	}
	close(mutationFinished)
	err := <-loginDone
	mu.Lock()
	isSettled = settled
	mu.Unlock()
	if !isSettled {
		t.Fatal("login did not settle")
	}
	var syncErr *CredentialSynchronizationError
	if !errors.As(err, &syncErr) {
		t.Fatalf("error = %v, want CredentialSynchronizationError", err)
	}
	apiKey, ok := syncErr.Credential.(*auth.ApiKeyCredential)
	if !ok || apiKey.Key != "delayed-commit-key" {
		t.Fatalf("credential = %+v", syncErr.Credential)
	}
	mu.Lock()
	finalStored := stored
	mu.Unlock()
	finalKey, ok := finalStored.(*auth.ApiKeyCredential)
	if !ok || finalKey.Key != "delayed-commit-key" {
		t.Fatalf("stored = %+v, want delayed-commit-key", finalStored)
	}
}

type delayedCommitStore struct {
	committed        chan struct{}
	mutationFinished chan struct{}
	mu               *sync.Mutex
	stored           *auth.Credential
}

func (s *delayedCommitStore) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.stored, nil
}

func (s *delayedCommitStore) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if *s.stored == nil {
		return nil, nil
	}
	return []auth.CredentialInfo{{ProviderID: "delayed-commit", Type: (*s.stored).CredentialType()}}, nil
}

func (s *delayedCommitStore) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	s.mu.Lock()
	next, err := fn(*s.stored)
	if err == nil && next != nil {
		*s.stored = next
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	closeOnce(s.committed)
	<-s.mutationFinished
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.stored, nil
}

func (s *delayedCommitStore) Delete(ctx context.Context, providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.stored = nil
	return nil
}

func TestModelRuntimeReportsATypedErrorWhenCancellationInterruptsPostCommitSynchronization(t *testing.T) {
	var mu sync.Mutex
	blockCacheRefresh := false
	cacheRefreshStarted := make(chan struct{})
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	registered := &blockingRestoreProvider{
		id:       "cancelled-sync",
		mu:       &mu,
		block:    &blockCacheRefresh,
		started:  cacheRefreshStarted,
		loginKey: "cancelled-sync-key",
	}
	if err := runtime.RegisterNativeProvider(registered); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{registered.ID()}})
	mu.Lock()
	blockCacheRefresh = true
	mu.Unlock()

	loginCtx, cancel := context.WithCancel(context.Background())
	loginDone := make(chan error, 1)
	go func() {
		_, err := runtime.Login(loginCtx, "cancelled-sync", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
		loginDone <- err
	}()
	<-cacheRefreshStarted
	cancel()
	err := <-loginDone
	var syncErr *CredentialSynchronizationError
	if !errors.As(err, &syncErr) {
		t.Fatalf("error = %v, want CredentialSynchronizationError", err)
	}
	if syncErr.ProviderID != "cancelled-sync" || syncErr.Operation != CredentialSyncLogin {
		t.Fatalf("error = %+v", syncErr)
	}
	apiKey, ok := syncErr.Credential.(*auth.ApiKeyCredential)
	if !ok || apiKey.Key != "cancelled-sync-key" {
		t.Fatalf("credential = %+v", syncErr.Credential)
	}
	stored, err := credentials.Read(context.Background(), "cancelled-sync")
	if err != nil {
		t.Fatal(err)
	}
	storedKey, ok := stored.(*auth.ApiKeyCredential)
	if !ok || storedKey.Key != "cancelled-sync-key" {
		t.Fatalf("stored = %+v, want cancelled-sync-key", stored)
	}
}

func TestModelRuntimeReportsCommittedCredentialsWhenLocalSynchronizationFails(t *testing.T) {
	var mu sync.Mutex
	failCacheRefresh := false
	credentials := NewInMemoryAuthStorage(authStorageData{})
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	registered := &failingRestoreProvider{
		id:       "broken-sync",
		mu:       &mu,
		fail:     &failCacheRefresh,
		loginKey: "broken-sync-key",
	}
	if err := runtime.RegisterNativeProvider(registered); err != nil {
		t.Fatal(err)
	}
	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false), Providers: []string{registered.ID()}})
	mu.Lock()
	failCacheRefresh = true
	mu.Unlock()

	_, err := runtime.Login(context.Background(), "broken-sync", auth.AuthTypeAPIKey, noopAuthInteraction, nil)
	var syncErr *CredentialSynchronizationError
	if !errors.As(err, &syncErr) {
		t.Fatalf("error = %v, want CredentialSynchronizationError", err)
	}
	if syncErr.ProviderID != "broken-sync" || syncErr.Operation != CredentialSyncLogin {
		t.Fatalf("error = %+v", syncErr)
	}
	apiKey, ok := syncErr.Credential.(*auth.ApiKeyCredential)
	if !ok || apiKey.Key != "broken-sync-key" {
		t.Fatalf("credential = %+v", syncErr.Credential)
	}
	stored, err := credentials.Read(context.Background(), "broken-sync")
	if err != nil {
		t.Fatal(err)
	}
	storedKey, ok := stored.(*auth.ApiKeyCredential)
	if !ok || storedKey.Key != "broken-sync-key" {
		t.Fatalf("stored = %+v, want broken-sync-key", stored)
	}
}

// blockingRestoreProvider is a native provider whose non-network restore
// phase blocks (or throws) after login, to exercise synchronization failures.
type blockingRestoreProvider struct {
	id       string
	mu       *sync.Mutex
	block    *bool
	started  chan struct{}
	loginKey string
}

func (p *blockingRestoreProvider) ID() string                     { return p.id }
func (p *blockingRestoreProvider) Name() string                   { return p.id }
func (p *blockingRestoreProvider) BaseURL() string                { return "" }
func (p *blockingRestoreProvider) Headers() types.ProviderHeaders { return nil }
func (p *blockingRestoreProvider) Auth() auth.ProviderAuth {
	return auth.ProviderAuth{APIKey: &staticAPIKeyAuth{name: "API key", key: p.loginKey}}
}
func (p *blockingRestoreProvider) GetModels() []ai.Model {
	return []ai.Model{{
		ID: "dynamic", Name: "Dynamic", API: ai.ApiOpenAICompletions, Provider: ai.ProviderID(p.id),
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
		ContextWindow: 1000, MaxTokens: 100,
	}}
}
func (p *blockingRestoreProvider) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}
func (p *blockingRestoreProvider) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}

func (p *blockingRestoreProvider) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	if refreshCtx.AllowNetwork {
		return nil
	}
	p.mu.Lock()
	block := *p.block
	p.mu.Unlock()
	if block {
		if p.started != nil {
			closeOnce(p.started)
		}
		<-make(chan struct{})
	}
	return nil
}

// failingRestoreProvider throws during the restore phase.
type failingRestoreProvider struct {
	id       string
	mu       *sync.Mutex
	fail     *bool
	loginKey string
}

func (p *failingRestoreProvider) ID() string                     { return p.id }
func (p *failingRestoreProvider) Name() string                   { return p.id }
func (p *failingRestoreProvider) BaseURL() string                { return "" }
func (p *failingRestoreProvider) Headers() types.ProviderHeaders { return nil }
func (p *failingRestoreProvider) Auth() auth.ProviderAuth {
	return auth.ProviderAuth{APIKey: &staticAPIKeyAuth{name: "API key", key: p.loginKey}}
}
func (p *failingRestoreProvider) GetModels() []ai.Model {
	return []ai.Model{{
		ID: "dynamic", Name: "Dynamic", API: ai.ApiOpenAICompletions, Provider: ai.ProviderID(p.id),
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
		ContextWindow: 1000, MaxTokens: 100,
	}}
}
func (p *failingRestoreProvider) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}
func (p *failingRestoreProvider) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}

func (p *failingRestoreProvider) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	if refreshCtx.AllowNetwork {
		return nil
	}
	p.mu.Lock()
	fail := *p.fail
	p.mu.Unlock()
	if fail {
		return errors.New("cache restore failed")
	}
	return nil
}

// recordingDynamicProvider is a native provider whose dynamic refresh phase
// records network-phase invocations and can block (pi: refreshModels stubs).
type recordingDynamicProvider struct {
	id           string
	mu           *sync.Mutex
	networkCalls *int
	loginKey     string
	// started and blocked (with ctxOut) turn the network phase into a
	// stalled request for the superseding test.
	started chan struct{}
	blocked chan struct{}
	ctxOut  *context.Context
}

func (p *recordingDynamicProvider) ID() string                     { return p.id }
func (p *recordingDynamicProvider) Name() string                   { return p.id }
func (p *recordingDynamicProvider) BaseURL() string                { return "" }
func (p *recordingDynamicProvider) Headers() types.ProviderHeaders { return nil }
func (p *recordingDynamicProvider) Auth() auth.ProviderAuth {
	return auth.ProviderAuth{APIKey: &staticAPIKeyAuth{name: "API key", key: p.loginKey}}
}
func (p *recordingDynamicProvider) GetModels() []ai.Model {
	return []ai.Model{{
		ID: "dynamic", Name: "Dynamic", API: ai.ApiOpenAICompletions, Provider: ai.ProviderID(p.id),
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
		ContextWindow: 1000, MaxTokens: 100,
	}}
}
func (p *recordingDynamicProvider) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}
func (p *recordingDynamicProvider) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}

func (p *recordingDynamicProvider) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	if refreshCtx.AllowNetwork {
		if p.networkCalls != nil {
			p.mu.Lock()
			*p.networkCalls++
			p.mu.Unlock()
		}
		if p.started != nil {
			p.mu.Lock()
			*p.ctxOut = refreshCtx.Context
			p.mu.Unlock()
			closeOnce(p.started)
			if p.blocked != nil {
				<-p.blocked
			}
		}
	}
	return nil
}

// staticAPIKeyAuth resolves only from a stored credential.
type staticAPIKeyAuth struct {
	name string
	key  string
}

func (a *staticAPIKeyAuth) Name() string { return a.name }

func (a *staticAPIKeyAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	return &auth.ApiKeyCredential{Key: a.key}, nil
}

func (a *staticAPIKeyAuth) Check(input auth.ApiKeyAuthInput) (*auth.AuthCheck, error) {
	if input.Credential != nil {
		return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "stored"}, nil
	}
	return nil, nil
}

func (a *staticAPIKeyAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	if input.Credential != nil {
		return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
	}
	return nil, nil
}
