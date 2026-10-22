package ai

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai/auth"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// pi: models.ts.

// ModelsPublication is a provider-selected catalog publication.
type ModelsPublication struct {
	// Persist is the provider-selected persisted catalog; nil leaves storage
	// unchanged. (pi: persist?: ModelsStoreEntry | null)
	Persist *ModelsStoreEntry
	// DeletePersist deletes the persisted catalog (pi: persist: null).
	DeletePersist bool
	// Update optionally applies a synchronous update of provider-private
	// in-memory catalog state.
	Update func()
}

// RefreshModelsContext is one provider refresh phase.
type RefreshModelsContext struct {
	// auth.Credential is the effective configured credential. OAuth credentials
	// are refreshed before network access.
	Credential auth.Credential
	// Stored is an immutable provider-scoped catalog snapshot captured
	// before this refresh phase.
	Stored *ModelsStoreEntry
	// Publish is a generation-checked publication. Persistence policy
	// remains provider-owned; the update runs synchronously only after the
	// selected persistence mutation.
	Publish func(publication ModelsPublication) (bool, error)
	// AllowNetwork is false during offline/cache-only initialization.
	AllowNetwork bool
	// Force bypasses provider freshness checks and fetches immediately when
	// network access is allowed.
	Force bool
	// BaseURL is the registered provider base URL, including configuration
	// overrides that wrappers apply outside the provider receiving this
	// context (pi: baseUrl).
	BaseURL string
	// Context is always present, including when the public refresh caller
	// omits its context (pi: signal).
	Context context.Context
}

// ModelsRefreshOptions select which providers Models.Refresh touches.
type ModelsRefreshOptions struct {
	// AllowNetwork defaults to true.
	AllowNetwork *bool
	// Providers restricts refresh to these provider ids. Unknown and static
	// providers are ignored.
	Providers []string
	// Force bypasses provider freshness checks and fetches immediately when
	// network access is allowed.
	Force bool
}

// ModelsRefreshResult reports the outcome of Models.Refresh.
type ModelsRefreshResult struct {
	Aborted bool
	Errors  map[string]error
}

// ModelsRequestTransforms transform fully assembled model/auth/request
// headers before provider dispatch.
type ModelsRequestTransforms struct {
	TransformHeaders func(headers ProviderHeaders) (ProviderHeaders, error)
}

// ModelsStreamOptions are StreamOptions plus the Models-level request
// transforms (pi: ModelsApiStreamOptions; pi's per-API ApiStreamOptions
// conditional collapses to StreamOptions).
type ModelsStreamOptions struct {
	StreamOptions
	ModelsRequestTransforms
}

// ModelsSimpleStreamOptions are SimpleStreamOptions plus the request
// transforms.
type ModelsSimpleStreamOptions struct {
	SimpleStreamOptions
	ModelsRequestTransforms
}

// ModelsDeferredFetchOptions are DeferredFetchOptions plus the request
// transforms.
type ModelsDeferredFetchOptions struct {
	DeferredFetchOptions
	ModelsRequestTransforms
}

// ModelsDeferredCancelOptions are DeferredCancelOptions plus the request
// transforms.
type ModelsDeferredCancelOptions struct {
	ProviderRequestOptions
	ModelsRequestTransforms
}

// Provider is the concrete runtime unit. It owns id/name/base metadata, auth
// methods, model listing, and stream behavior. pi's TApi parameter exists for
// TypeScript call-site typing; every API shares one Model shape, so pig uses
// one concrete interface.
type Provider interface {
	ID() string
	Name() string
	// BaseURL is the provider base URL; empty when unset (pi: optional
	// baseUrl).
	BaseURL() string
	// Headers are provider-wide request headers; nil when unset.
	Headers() ProviderHeaders
	// Auth holds the provider auth methods. Required: at least one of
	// APIKey/OAuth. Every provider has auth semantics — even providers with
	// only ambient credentials and keyless local servers provide APIKey auth
	// whose Resolve reports whether the provider is configured.
	Auth() auth.ProviderAuth
	// GetModels returns the current known models, synchronously. Static
	// providers return their catalog; dynamic providers return the list as
	// of the last RefreshModels (empty before the first). Must not fail;
	// Models treats a failing implementation as having no models.
	GetModels() []Model
	// Stream dispatches one streaming request for the model's API. ctx carries
	// the caller's cancellation (pi: options.signal).
	Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream
	// StreamSimple dispatches one simple streaming request.
	StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream
}

// ProviderModelFilter is the optional provider policy for
// credential-specific model availability (pi: optional filterModels).
// Provider.GetModels remains the complete synchronous catalog; Models
// .GetAvailable applies this filter after confirming that provider auth is
// configured.
type ProviderModelFilter interface {
	FilterModels(models []Model, credential auth.Credential) []Model
}

// ProviderAllModels is the optional mixed-type catalog access (pi: optional
// getAllModels member, v0.99.1 #9948). Providers that do not implement it
// expose only their GetModels list through Models.GetAllModels.
type ProviderAllModels interface {
	GetAllModels() []Model
}

// ProviderAllModelsFilter is the optional credential-specific availability
// policy over the mixed-type catalog (pi: optional filterAllModels). When a
// provider does not implement it, Models.GetAllAvailable filters chat ids
// with FilterModels and passes non-chat entries through.
type ProviderAllModelsFilter interface {
	FilterAllModels(models []Model, credential auth.Credential) []Model
}

// dynamicProvider is the optional refresh capability of a provider (pi:
// optional refreshModels member; Required<Pick<Provider, "refreshModels">>
// narrowing in pi's refresh flow). Dynamic providers restore Context.Stored
// and optionally fetch a newer list using the effective credential;
// implementations retain their previous list on failure, publish persistence
// and synchronous state changes through refreshCtx.Publish, and honor
// refreshCtx.Context for blocking work.
type dynamicProvider interface {
	RefreshModels(ctx context.Context, refreshCtx *RefreshModelsContext) error
}

// ProviderDeferredStreamer is the optional deferred-response capability of a
// provider (pi: optional fetchDeferred/cancelDeferred members).
type ProviderDeferredStreamer interface {
	FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream
	CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error
}

// Models is the read-only runtime collection of providers plus auth
// application and stream convenience (pi: interface Models). Providers own
// stream behavior; Models resolves auth and delegates each request to the
// provider that owns the model. Registry mutation requires MutableModels —
// extensions and internal consumers receive this view so they cannot change
// the provider set.
type Models interface {
	GetProviders() []Provider
	GetProvider(id string) Provider
	// GetModels returns last-known models from every provider (or one
	// provider via GetProviderModels). Best-effort: a provider whose
	// GetModels fails yields no models.
	GetModels() []Model
	// GetModel looks up one model against last-known lists (pi: getModel).
	// pi's hasApi type guard is a TS narrowing tool with no Go counterpart;
	// compare Model.API directly.
	GetModel(providerID, id string) (Model, bool)
	// Refresh refreshes selected configured dynamic providers concurrently
	// (all when options.Providers is omitted). Provider errors and
	// cancellation are returned without failing the call; static, unknown,
	// and unconfigured providers are skipped.
	Refresh(ctx context.Context, options *ModelsRefreshOptions) ModelsRefreshResult
	// CheckAuth reports whether a provider has complete auth configuration
	// without refreshing OAuth; nil when the provider is unknown or
	// unconfigured.
	CheckAuth(ctx context.Context, providerID string) (*auth.AuthCheck, error)
	// GetAvailable returns chat models whose providers have complete auth
	// configuration (pi: getAvailable, chat-only since v0.99.1 #9948).
	// providerID optionally restricts the result to one provider.
	GetAvailable(ctx context.Context, providerID string) ([]Model, error)
	// GetAllAvailable returns models of every type whose providers have
	// complete auth configuration (pi: getAllAvailable). Providers with a
	// filterAllModels policy apply it; otherwise the chat FilterModels
	// result decides chat availability and non-chat entries pass through.
	GetAllAvailable(ctx context.Context, providerID string) ([]Model, error)
	// GetAvailableOfType is GetAllAvailable narrowed to one model type (pi:
	// getAvailableOfType).
	GetAvailableOfType(ctx context.Context, modelType ModelType, providerID string) ([]Model, error)
	// GetModelsOfType returns last-known models of one type from every
	// provider (pi: getModelsOfType). Best-effort like GetModels.
	GetModelsOfType(modelType ModelType) []Model
	// GetModelOfType looks up one model of the given type against last-known
	// lists (pi: getModelOfType).
	GetModelOfType(modelType ModelType, providerID, id string) (Model, bool)
	// GetAllModels returns last-known models of every type from every
	// provider (pi: getAllModels). Best-effort: a provider whose listing
	// fails yields no models.
	GetAllModels() []Model
	// GetAuth resolves provider-scoped auth by provider id (pi: getAuth
	// string overload). A nil result means the provider is unknown or
	// unconfigured. Fails with a ModelsError of code "oauth" when a token
	// refresh fails (the stored credential is preserved for retry; re-login
	// fixes it) and code "auth" when api-key resolution or the credential
	// store fails. Request paths surface failures as stream errors.
	GetAuth(ctx context.Context, providerID string, overrides *auth.AuthResolutionOverrides) (*auth.AuthResult, error)
	// GetAuthForModel resolves provider auth plus static model headers when
	// passed a model (pi: getAuth(Model) overload).
	GetAuthForModel(ctx context.Context, model *Model, overrides *auth.AuthResolutionOverrides) (*auth.AuthResult, error)
	// Login runs a provider-owned login flow and persists its returned
	// credential.
	Login(ctx context.Context, providerID string, authType auth.AuthType, interaction auth.AuthInteraction, options *auth.LoginOptions) (auth.Credential, error)
	// Logout removes the stored credential for a provider.
	Logout(ctx context.Context, providerID string) error
	// Stream resolves auth lazily and delegates to the owning provider.
	Stream(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) *AssistantMessageEventStream
	// Complete is Stream plus Result (pi: complete).
	Complete(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) (*AssistantMessage, error)
	// StreamSimple resolves auth lazily and delegates to the owning provider.
	StreamSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream
	// CompleteSimple is StreamSimple plus Result (pi: completeSimple).
	CompleteSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error)
	// StreamDeferred resolves auth lazily and returns the deferred-response
	// stream (pi: streamDeferred; fetchDeferred became its Result() wrapper).
	StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream
	// FetchDeferred fetches a deferred response to completion.
	FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error)
	// CancelDeferred cancels one deferred response.
	CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error
}

// MutableModels extends Models with provider-registry mutation (pi: interface
// MutableModels). Provider ids are unique; SetProvider upserts by
// provider.ID().
type MutableModels interface {
	Models
	SetProvider(provider Provider)
	DeleteProvider(id string)
	ClearProviders()
}

// CreateModelsOptions select the stores and auth context; nil members fall
// back to in-memory/ambient defaults.
type CreateModelsOptions struct {
	Credentials auth.CredentialStore
	ModelsStore ModelsStore
	AuthContext auth.AuthContext
	// Identity is the default identity of every request; a request's identity
	// overrides it field by field.
	Identity *ClientIdentity
}

// refreshController is the AbortController counterpart (pi:
// refreshControllers map): the refresh's cancellable context plus its cancel.
// pi compares controllers by reference for identity-checked cleanup; Go
// funcs are not comparable, so the pair lives in a struct and the struct
// pointer is compared instead.
type refreshController struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// modelsImpl is the ModelsImpl counterpart.
type modelsImpl struct {
	mu        sync.Mutex
	providers map[string]Provider

	credentials auth.CredentialStore
	modelsStore ModelsStore
	authContext auth.AuthContext
	identity    *ClientIdentity

	refreshMu          sync.Mutex
	refreshGenerations map[string]int
	refreshControllers map[string]*refreshController

	publicationMu    sync.Mutex
	publicationLocks map[string]*sync.Mutex
}

// NewModels is the createModels counterpart; it returns the mutable view.
func NewModels(options *CreateModelsOptions) MutableModels {
	m := &modelsImpl{
		providers:          map[string]Provider{},
		refreshGenerations: map[string]int{},
		refreshControllers: map[string]*refreshController{},
		publicationLocks:   map[string]*sync.Mutex{},
	}
	if options != nil {
		m.identity = options.Identity
		m.credentials = options.Credentials
		m.modelsStore = options.ModelsStore
		m.authContext = options.AuthContext
	}
	if m.credentials == nil {
		m.credentials = &auth.InMemoryCredentialStore{}
	}
	if m.modelsStore == nil {
		m.modelsStore = &InMemoryModelsStore{}
	}
	if m.authContext == nil {
		m.authContext = auth.DefaultProviderAuthContext()
	}
	return m
}

// SetProvider upserts by provider id; provider ids are unique.
func (m *modelsImpl) SetProvider(provider Provider) {
	m.supersedeProviderRefresh(provider.ID())
	m.mu.Lock()
	m.providers[provider.ID()] = provider
	m.mu.Unlock()
}

// DeleteProvider removes one provider.
func (m *modelsImpl) DeleteProvider(id string) {
	m.supersedeProviderRefresh(id)
	m.mu.Lock()
	delete(m.providers, id)
	m.mu.Unlock()
}

// ClearProviders removes every provider and supersedes their refreshes.
func (m *modelsImpl) ClearProviders() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.providers))
	for id := range m.providers {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	m.refreshMu.Lock()
	for id := range m.refreshControllers {
		ids = append(ids, id)
	}
	m.refreshMu.Unlock()
	for _, id := range ids {
		m.supersedeProviderRefresh(id)
	}
	m.mu.Lock()
	m.providers = map[string]Provider{}
	m.mu.Unlock()
}

// GetProviders returns every registered provider.
func (m *modelsImpl) GetProviders() []Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		out = append(out, provider)
	}
	return out
}

// GetProvider returns one provider by id.
func (m *modelsImpl) GetProvider(id string) Provider {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.providers[id]
}

// GetModels returns last-known models from every provider. Best-effort: the
// contract says GetModels must not fail, so no per-provider recovery is
// needed here (pi also guards a throwing getModels with an empty result).
func (m *modelsImpl) GetModels() []Model {
	m.mu.Lock()
	providers := make([]Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		providers = append(providers, provider)
	}
	m.mu.Unlock()
	var models []Model
	for _, provider := range providers {
		models = append(models, provider.GetModels()...)
	}
	return models
}

// GetProviderModels returns last-known models from one provider; empty when
// the provider is unknown.
func (m *modelsImpl) GetProviderModels(providerID string) []Model {
	m.mu.Lock()
	provider := m.providers[providerID]
	m.mu.Unlock()
	if provider == nil {
		return nil
	}
	return provider.GetModels()
}

// GetModel looks up one model against last-known lists (pi: getModel).
// pi's hasApi type guard is a TS narrowing tool with no Go counterpart;
// compare Model.API directly.
func (m *modelsImpl) GetModel(providerID, id string) (Model, bool) {
	for _, model := range m.GetProviderModels(providerID) {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func (m *modelsImpl) supersedeProviderRefresh(providerID string) int {
	m.refreshMu.Lock()
	if m.refreshGenerations == nil {
		m.refreshGenerations = map[string]int{}
	}
	if m.refreshControllers == nil {
		m.refreshControllers = map[string]*refreshController{}
	}
	generation := m.refreshGenerations[providerID] + 1
	m.refreshGenerations[providerID] = generation
	controller := m.refreshControllers[providerID]
	if controller != nil {
		delete(m.refreshControllers, providerID)
		controller.cancel()
	}
	m.refreshMu.Unlock()
	return generation
}

func (m *modelsImpl) beginProviderRefresh(ctx context.Context, providerID string) (int, *refreshController) {
	generation := m.supersedeProviderRefresh(providerID)
	refreshCtx, cancel := context.WithCancel(ctx)
	controller := &refreshController{ctx: refreshCtx, cancel: cancel}
	m.refreshMu.Lock()
	m.refreshControllers[providerID] = controller
	m.refreshMu.Unlock()
	return generation, controller
}

func (m *modelsImpl) publicationLock(providerID string) *sync.Mutex {
	m.publicationMu.Lock()
	defer m.publicationMu.Unlock()
	if m.publicationLocks == nil {
		m.publicationLocks = map[string]*sync.Mutex{}
	}
	if m.publicationLocks[providerID] == nil {
		m.publicationLocks[providerID] = &sync.Mutex{}
	}
	return m.publicationLocks[providerID]
}

// publishProviderModels serializes publications per provider (pi: promise
// chain) and applies generation-checked persistence followed by the
// synchronous update.
func (m *modelsImpl) publishProviderModels(providerID string, generation int, ctx context.Context, publication ModelsPublication) (bool, error) {
	lock := m.publicationLock(providerID)
	lock.Lock()
	defer lock.Unlock()

	current := func() int {
		m.refreshMu.Lock()
		defer m.refreshMu.Unlock()
		return m.refreshGenerations[providerID]
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if current() != generation {
		return false, nil
	}

	if publication.DeletePersist {
		if err := m.modelsStore.Delete(ctx, providerID); err != nil {
			return false, err
		}
	} else if publication.Persist != nil {
		if err := m.modelsStore.Write(ctx, providerID, cloneModelsStoreEntry(publication.Persist)); err != nil {
			return false, err
		}
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}
	if current() != generation {
		return false, nil
	}
	if publication.Update != nil {
		publication.Update()
	}
	return true, nil
}

func (m *modelsImpl) runProviderRefreshPhase(ctx context.Context, provider dynamicProvider, providerID string, baseURL string, credential auth.Credential, allowNetwork bool, force bool, generation int) error {
	stored, err := m.modelsStore.Read(ctx, providerID)
	if err != nil {
		return err
	}
	return provider.RefreshModels(ctx, &RefreshModelsContext{
		Credential: credential,
		Stored:     stored,
		Publish: func(publication ModelsPublication) (bool, error) {
			return m.publishProviderModels(providerID, generation, ctx, publication)
		},
		AllowNetwork: allowNetwork,
		Force:        force,
		BaseURL:      baseURL,
		Context:      ctx,
	})
}

// Refresh refreshes selected configured dynamic providers concurrently (all
// when options.Providers is omitted). Provider errors and cancellation are
// returned without failing the call; static, unknown, and unconfigured
// providers are skipped.
func (m *modelsImpl) Refresh(ctx context.Context, options *ModelsRefreshOptions) ModelsRefreshResult {
	allowNetwork := true
	force := false
	var selected map[string]struct{}
	if options != nil {
		if options.AllowNetwork != nil {
			allowNetwork = *options.AllowNetwork
		}
		force = options.Force
		if options.Providers != nil {
			selected = map[string]struct{}{}
			for _, id := range options.Providers {
				selected[id] = struct{}{}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ModelsRefreshResult{Aborted: true, Errors: map[string]error{}}
	}

	m.mu.Lock()
	type refreshTarget struct {
		provider Provider
		dyn      dynamicProvider
	}
	refreshable := make([]refreshTarget, 0, len(m.providers))
	for _, provider := range m.providers {
		if selected != nil {
			if _, ok := selected[provider.ID()]; !ok {
				continue
			}
		}
		if dyn, ok := provider.(dynamicProvider); ok {
			// A composed provider without FetchModels is static (pi skips it
			// because refreshModels is undefined); its RefreshModels method
			// would return immediately, but skip the wasted offline phase.
			if composed, ok := provider.(*composedProvider); ok && composed.refreshModels == nil {
				continue
			}
			refreshable = append(refreshable, refreshTarget{provider: provider, dyn: dyn})
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	var errorsMu sync.Mutex
	errors := map[string]error{}
	for _, target := range refreshable {
		wg.Add(1)
		go func(target refreshTarget) {
			provider := target.provider
			dyn := target.dyn
			providerID := provider.ID()
			defer wg.Done()
			generation, controller := m.beginProviderRefresh(ctx, providerID)
			refreshCtx := controller.ctx

			operation := func() error {
				storedCredential, credentialErr := m.readCredential(refreshCtx, providerID)

				// Restore cached provider state before auth resolution or
				// network access.
				if err := m.runProviderRefreshPhase(refreshCtx, dyn, providerID, provider.BaseURL(), storedCredential, false, false, generation); err != nil {
					return err
				}
				if credentialErr != nil {
					return credentialErr
				}
				if !allowNetwork || refreshCtx.Err() != nil {
					return nil
				}

				credential, err := m.resolveRefreshCredential(refreshCtx, provider, storedCredential)
				if err != nil {
					return err
				}
				if credential == nil {
					return nil
				}
				return m.runProviderRefreshPhase(refreshCtx, dyn, providerID, provider.BaseURL(), credential, true, force, generation)
			}

			_, err := waitOperation(refreshCtx, func() (struct{}, error) {
				return struct{}{}, operation()
			})
			if err != nil && refreshCtx.Err() == nil && ctx.Err() == nil {
				errorsMu.Lock()
				errors[providerID] = err
				errorsMu.Unlock()
			}
			m.refreshMu.Lock()
			if m.refreshControllers[providerID] == controller {
				// Still this generation's controller: remove it (pi:
				// identity-checked delete without abort — the operation has
				// settled, so nothing in flight is aborted).
				delete(m.refreshControllers, providerID)
			}
			m.refreshMu.Unlock()
			// pi has no counterpart: the refresh's AbortController goes out of
			// scope and is garbage-collected, while a Go cancelCtx stays
			// registered in its parent's children map until canceled — one
			// leaked registration per refresh (each startup refresh fans out
			// to every dynamic provider). waitOperation has returned, so the
			// operation has settled, or the context was already canceled by a
			// supersede or the parent (cancel is idempotent and a no-op there).
			controller.cancel()
		}(target)
	}
	wg.Wait()

	return ModelsRefreshResult{Aborted: ctx.Err() != nil, Errors: errors}
}

func (m *modelsImpl) resolveRefreshCredential(ctx context.Context, provider Provider, stored auth.Credential) (auth.Credential, error) {
	if storedOAuth, ok := stored.(*auth.OAuthCredential); ok {
		oauth := provider.Auth().OAuth
		if oauth == nil {
			return nil, nil
		}
		if time.Now().UnixMilli() < storedOAuth.Expires {
			return storedOAuth, nil
		}
		if ctx.Err() != nil {
			return nil, nil
		}
		// A refresh that has started survives cancellation or a superseding
		// model refresh, so a rotated refresh token is always persisted; a
		// newer refresh then sees the fresh credential (pi v1.0.3 bde882c74).
		return auth.RefreshStoredOAuthCredential(ctx, m.credentials, provider.ID(), oauth,
			func(current *auth.OAuthCredential) bool {
				return time.Now().UnixMilli() >= current.Expires
			})
	}

	apiKey := provider.Auth().APIKey
	if apiKey == nil {
		return nil, nil
	}
	var credential *auth.ApiKeyCredential
	if storedKey, ok := stored.(*auth.ApiKeyCredential); ok {
		credential = storedKey
	}
	result, err := apiKey.Resolve(auth.ApiKeyAuthInput{Ctx: m.authContext, Credential: credential, Context: ctx})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return &auth.ApiKeyCredential{Key: result.Auth.APIKey, Env: result.Env}, nil
}

func (m *modelsImpl) readCredential(ctx context.Context, providerID string) (auth.Credential, error) {
	credential, err := m.credentials.Read(ctx, providerID)
	if err != nil {
		return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("Credential store read failed for %s", providerID), err)
	}
	return credential, nil
}

func (m *modelsImpl) checkProviderAuth(ctx context.Context, provider Provider, credential auth.Credential) (*auth.AuthCheck, error) {
	if storedOAuth, ok := credential.(*auth.OAuthCredential); ok && storedOAuth != nil {
		if provider.Auth().OAuth != nil {
			return &auth.AuthCheck{Source: "OAuth", Type: auth.AuthTypeOAuth}, nil
		}
		return nil, nil
	}
	apiKey := provider.Auth().APIKey
	if apiKey == nil {
		return nil, nil
	}
	if checker, ok := apiKey.(auth.ApiKeyAuthChecker); ok {
		var inputCredential *auth.ApiKeyCredential
		if storedKey, ok := credential.(*auth.ApiKeyCredential); ok {
			inputCredential = storedKey
		}
		check, err := checker.Check(auth.ApiKeyAuthInput{Ctx: m.authContext, Credential: inputCredential, Context: ctx})
		if err != nil {
			return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("API key auth check failed for provider %s", provider.ID()), err)
		}
		return check, nil
	}
	resolution, err := auth.ResolveProviderAuth(ctx, provider, m.credentials, m.authContext, nil)
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, nil
	}
	return &auth.AuthCheck{Source: resolution.Source, Type: auth.AuthTypeAPIKey}, nil
}

// CheckAuth reports whether a provider has complete auth configuration
// without refreshing OAuth; nil when the provider is unknown or
// unconfigured.
func (m *modelsImpl) CheckAuth(ctx context.Context, providerID string) (*auth.AuthCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, nil
	}
	credential, err := m.readCredential(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return m.checkProviderAuth(ctx, provider, credential)
}

// providerCheck pairs a provider with its resolved credential and auth state
// for the availability loops.
type providerCheck struct {
	provider   Provider
	credential auth.Credential
	auth       *auth.AuthCheck
}

// checkProviders resolves credentials and auth state for one provider (or
// every registered one).
func (m *modelsImpl) checkProviders(ctx context.Context, providerID string) ([]providerCheck, error) {
	var providers []Provider
	if providerID != "" {
		if provider := m.GetProvider(providerID); provider != nil {
			providers = []Provider{provider}
		}
	} else {
		providers = m.GetProviders()
	}

	checks := make([]providerCheck, len(providers))
	for i, provider := range providers {
		credential, err := m.readCredential(ctx, provider.ID())
		if err != nil {
			return nil, err
		}
		auth, err := m.checkProviderAuth(ctx, provider, credential)
		if err != nil {
			return nil, err
		}
		checks[i] = providerCheck{provider: provider, credential: credential, auth: auth}
	}
	return checks, nil
}

// GetAvailable returns chat models whose providers have complete auth
// configuration (pi: getAvailable). providerID optionally restricts the
// result to one provider.
func (m *modelsImpl) GetAvailable(ctx context.Context, providerID string) ([]Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	checks, err := m.checkProviders(ctx, providerID)
	if err != nil {
		return nil, err
	}

	var out []Model
	for _, entry := range checks {
		if entry.auth == nil {
			continue
		}
		models := entry.provider.GetModels()
		if filter, ok := entry.provider.(ProviderModelFilter); ok {
			models = filter.FilterModels(models, entry.credential)
		}
		for _, model := range models {
			if model.IsModelType(ModelTypeChat) {
				out = append(out, model)
			}
		}
	}
	return out, nil
}

// GetAllAvailable returns models of every type whose providers have complete
// auth configuration (pi: getAllAvailable).
func (m *modelsImpl) GetAllAvailable(ctx context.Context, providerID string) ([]Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	checks, err := m.checkProviders(ctx, providerID)
	if err != nil {
		return nil, err
	}

	var out []Model
	for _, entry := range checks {
		if entry.auth == nil {
			continue
		}
		all := providerAllModels(entry.provider)
		if filter, ok := entry.provider.(ProviderAllModelsFilter); ok {
			out = append(out, filter.FilterAllModels(all, entry.credential)...)
			continue
		}
		// Default: the chat FilterModels result decides chat availability;
		// non-chat entries pass through (pi: availableChatIds from
		// filterModels(getModels(), credential)).
		availableChatIDs := map[string]bool{}
		chat := entry.provider.GetModels()
		if filter, ok := entry.provider.(ProviderModelFilter); ok {
			chat = filter.FilterModels(chat, entry.credential)
		}
		for _, model := range chat {
			availableChatIDs[model.ID] = true
		}
		for _, model := range all {
			if !model.IsModelType(ModelTypeChat) || availableChatIDs[model.ID] {
				out = append(out, model)
			}
		}
	}
	return out, nil
}

// GetAvailableOfType is GetAllAvailable narrowed to one model type (pi:
// getAvailableOfType).
func (m *modelsImpl) GetAvailableOfType(ctx context.Context, modelType ModelType, providerID string) ([]Model, error) {
	available, err := m.GetAllAvailable(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return modelsOfType(available, modelType), nil
}

// providerAllModels reads the full catalog from a provider (pi:
// getAllModels ?? getModels).
func providerAllModels(provider Provider) []Model {
	if all, ok := provider.(ProviderAllModels); ok {
		return all.GetAllModels()
	}
	return provider.GetModels()
}

// modelsOfType filters a mixed list to one model type.
func modelsOfType(models []Model, modelType ModelType) []Model {
	var out []Model
	for _, model := range models {
		if model.IsModelType(modelType) {
			out = append(out, model)
		}
	}
	return out
}

// GetModelsOfType returns last-known models of one type from every provider
// (pi: getModelsOfType).
func (m *modelsImpl) GetModelsOfType(modelType ModelType) []Model {
	return modelsOfType(m.GetAllModels(), modelType)
}

// GetModelOfType looks up one model of the given type against last-known
// lists (pi: getModelOfType).
func (m *modelsImpl) GetModelOfType(modelType ModelType, providerID, id string) (Model, bool) {
	m.mu.Lock()
	provider := m.providers[providerID]
	m.mu.Unlock()
	if provider == nil {
		return Model{}, false
	}
	for _, model := range providerAllModels(provider) {
		if model.ID == id && model.IsModelType(modelType) {
			return model, true
		}
	}
	return Model{}, false
}

// GetAllModels returns last-known models of every type from every provider
// (pi: getAllModels).
func (m *modelsImpl) GetAllModels() []Model {
	m.mu.Lock()
	providers := make([]Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		providers = append(providers, provider)
	}
	m.mu.Unlock()
	var models []Model
	for _, provider := range providers {
		models = append(models, providerAllModels(provider)...)
	}
	return models
}

// GetAuth resolves provider-scoped auth by provider id (pi: getAuth(string)
// overload). A nil result means the provider is unknown or unconfigured.
// Fails with a auth.ModelsError of code "oauth" when a token refresh fails (the
// stored credential is preserved for retry; re-login fixes it) and code
// "auth" when api-key resolution or the credential store fails. Request
// paths surface failures as stream errors.
func (m *modelsImpl) GetAuth(ctx context.Context, providerID string, overrides *auth.AuthResolutionOverrides) (*auth.AuthResult, error) {
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, nil
	}
	return auth.ResolveProviderAuth(ctx, provider, m.credentials, m.authContext, overrides)
}

// mergeHeaders merges override over base case-insensitively; a nil result
// means "no headers".
func mergeHeaders(base, override ProviderHeaders) ProviderHeaders {
	if base == nil && override == nil {
		return nil
	}
	merged := ProviderHeaders{}
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range override {
		lower := strings.ToLower(name)
		for existing := range merged {
			if strings.ToLower(existing) == lower {
				delete(merged, existing)
			}
		}
		merged[name] = value
	}
	return merged
}

// GetAuthForModel resolves provider auth plus static model headers when
// passed a model (pi: getAuth(Model) overload).
func (m *modelsImpl) GetAuthForModel(ctx context.Context, model *Model, overrides *auth.AuthResolutionOverrides) (*auth.AuthResult, error) {
	provider := m.GetProvider(string(model.Provider))
	if provider == nil {
		return nil, nil
	}
	result, err := auth.ResolveProviderAuth(ctx, provider, m.credentials, m.authContext, overrides)
	if err != nil || result == nil || model.Headers == nil {
		return result, err
	}
	merged := &auth.AuthResult{Auth: result.Auth, Env: result.Env, Source: result.Source}
	merged.Auth.Headers = mergeHeaders(result.Auth.Headers, stringHeaders(model.Headers))
	return merged, nil
}

// Login runs a provider-owned login flow and persists its returned
// credential. pi guards the race between store mutation and flow abort with
// a started flag; Go collapses it into ctx cancellation inside Modify.
func (m *modelsImpl) Login(ctx context.Context, providerID string, authType auth.AuthType, interaction auth.AuthInteraction, options *auth.LoginOptions) (auth.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Unknown provider: %s", providerID), nil)
	}
	var credential auth.Credential
	switch authType {
	case auth.AuthTypeOAuth:
		oauth := provider.Auth().OAuth
		if oauth == nil {
			return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("%s does not support %s login", provider.Name(), authType), nil)
		}
		if _, ok := oauth.(interface {
			Login(ctx context.Context, interaction auth.AuthInteraction, options *auth.LoginOptions) (*auth.OAuthCredential, error)
		}); !ok {
			return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("%s does not support %s login", provider.Name(), authType), nil)
		}
		stored, err := oauth.Login(ctx, interaction, options)
		if err != nil {
			return nil, err
		}
		credential = stored
	case auth.AuthTypeAPIKey:
		apiKey := provider.Auth().APIKey
		login, ok := apiKey.(auth.ApiKeyAuthLogin)
		if apiKey == nil || !ok {
			return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("%s does not support %s login", provider.Name(), authType), nil)
		}
		stored, err := login.Login(ctx, interaction)
		if err != nil {
			return nil, err
		}
		credential = stored
	default:
		return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("%s does not support %s login", provider.Name(), authType), nil)
	}

	if _, err := m.credentials.Modify(ctx, providerID, func(auth.Credential) (auth.Credential, error) {
		return credential, nil
	}); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("auth.Credential store modify failed for %s", providerID), err)
	}
	return credential, nil
}

// Logout removes the stored credential for a provider.
func (m *modelsImpl) Logout(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.credentials.Delete(ctx, providerID); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("auth.Credential store delete failed for %s", providerID), err)
	}
	return nil
}

func (m *modelsImpl) requireProvider(model *Model) (Provider, error) {
	provider := m.GetProvider(string(model.Provider))
	if provider == nil {
		return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Unknown provider: %s", model.Provider), nil)
	}
	return provider, nil
}

// resolvedRequest carries the auth-applied request pieces.
type resolvedRequest struct {
	requestModel *Model
	apiKey       string
	headers      ProviderHeaders
	identity     *ClientIdentity
	env          ProviderEnv
}

// stringHeaders converts a plain header map to the ProviderHeaders shape
// (pi: Model.headers is Record<string, string> while ProviderHeaders values
// are string|null).
func stringHeaders(headers map[string]string) ProviderHeaders {
	if headers == nil {
		return nil
	}
	out := make(ProviderHeaders, len(headers))
	for name, value := range headers {
		copied := value
		out[name] = &copied
	}
	return out
}

// resolveRequestAuth is the core of pi's applyAuth: explicit request options
// win per-field; the Models-only transform runs last.
func (m *modelsImpl) resolveRequestAuth(ctx context.Context, model *Model, options ProviderRequestOptions, sessionID string, transforms *ModelsRequestTransforms) (*resolvedRequest, error) {
	if _, err := m.requireProvider(model); err != nil {
		return nil, err
	}
	resolution, err := m.GetAuthForModel(ctx, model, &auth.AuthResolutionOverrides{APIKey: options.APIKey, Env: options.Env})
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("Provider is not configured: %s", model.Provider), nil)
	}
	auth := resolution.Auth

	apiKey := options.APIKey
	if apiKey == "" {
		apiKey = auth.APIKey
	}
	requestModel := model
	if auth.BaseURL != "" {
		copied := *model
		copied.BaseURL = auth.BaseURL
		requestModel = &copied
	}
	target := ClientIdentityTarget{Provider: string(requestModel.Provider), BaseURL: requestModel.BaseURL}
	headers := mergeHeaders(aiutils.GetOpenCodeSessionHeaders(target, sessionID), auth.Headers)
	headers = mergeHeaders(headers, options.Headers)
	if transforms != nil && transforms.TransformHeaders != nil {
		headers, err = transforms.TransformHeaders(headers)
		if err != nil {
			return nil, err
		}
	}
	var env ProviderEnv
	if resolution.Env != nil || options.Env != nil {
		env = ProviderEnv{}
		for k, v := range resolution.Env {
			env[k] = v
		}
		for k, v := range options.Env {
			env[k] = v
		}
	}
	identity := aiutils.MergeClientIdentity(m.identity, options.Identity)
	return &resolvedRequest{requestModel: requestModel, apiKey: apiKey, headers: headers, identity: identity, env: env}, nil
}

func applyResolvedToRequestOptions(options *ProviderRequestOptions, resolved *resolvedRequest) {
	options.APIKey = resolved.apiKey
	options.Headers = resolved.headers
	options.Identity = resolved.identity
	options.Env = resolved.env
}

// Stream resolves auth lazily and delegates to the owning provider. The
// caller's Context is normalized into a TranscriptContext before dispatch
// (pi: normalizeContext at the top of stream).
func (m *modelsImpl) Stream(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) *AssistantMessageEventStream {
	transcript := aiutils.NormalizeContext(llmContext)
	return LazyStream(model, func() (*AssistantMessageEventStream, error) {
		provider, err := m.requireProvider(model)
		if err != nil {
			return nil, err
		}
		var base StreamOptions
		var transforms *ModelsRequestTransforms
		if options != nil {
			base = options.StreamOptions
			transforms = &options.ModelsRequestTransforms
		}
		resolved, err := m.resolveRequestAuth(ctx, model, base.ProviderRequestOptions, base.SessionID, transforms)
		if err != nil {
			return nil, err
		}
		applyResolvedToRequestOptions(&base.ProviderRequestOptions, resolved)
		return provider.Stream(ctx, resolved.requestModel, transcript, &base), nil
	})
}

// Complete is Stream plus Result (pi: complete).
func (m *modelsImpl) Complete(ctx context.Context, model *Model, llmContext *Context, options *ModelsStreamOptions) (*AssistantMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := m.Stream(ctx, model, llmContext, options).Result()
	return &result, nil
}

// StreamSimple resolves auth lazily and delegates to the owning provider.
func (m *modelsImpl) StreamSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) *AssistantMessageEventStream {
	transcript := aiutils.NormalizeContext(llmContext)
	return LazyStream(model, func() (*AssistantMessageEventStream, error) {
		provider, err := m.requireProvider(model)
		if err != nil {
			return nil, err
		}
		var base SimpleStreamOptions
		var transforms *ModelsRequestTransforms
		if options != nil {
			base = options.SimpleStreamOptions
			transforms = &options.ModelsRequestTransforms
		}
		resolved, err := m.resolveRequestAuth(ctx, model, base.ProviderRequestOptions, base.SessionID, transforms)
		if err != nil {
			return nil, err
		}
		applyResolvedToRequestOptions(&base.ProviderRequestOptions, resolved)
		return provider.StreamSimple(ctx, resolved.requestModel, transcript, &base), nil
	})
}

// CompleteSimple is StreamSimple plus Result (pi: completeSimple).
func (m *modelsImpl) CompleteSimple(ctx context.Context, model *Model, llmContext *Context, options *ModelsSimpleStreamOptions) (*AssistantMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := m.StreamSimple(ctx, model, llmContext, options).Result()
	return &result, nil
}

// StreamDeferred resolves auth lazily and returns the deferred-response
// stream (pi: streamDeferred).
func (m *modelsImpl) StreamDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) *AssistantMessageEventStream {
	return LazyStream(model, func() (*AssistantMessageEventStream, error) {
		provider, err := m.requireProvider(model)
		if err != nil {
			return nil, err
		}
		deferred, ok := provider.(ProviderDeferredStreamer)
		if !ok {
			return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil)
		}
		var base DeferredFetchOptions
		var transforms *ModelsRequestTransforms
		if options != nil {
			base = options.DeferredFetchOptions
			transforms = &options.ModelsRequestTransforms
		}
		resolved, err := m.resolveRequestAuth(ctx, model, base.ProviderRequestOptions, "", transforms)
		if err != nil {
			return nil, err
		}
		applyResolvedToRequestOptions(&base.ProviderRequestOptions, resolved)
		return deferred.FetchDeferred(ctx, resolved.requestModel, handle, &base), nil
	})
}

// FetchDeferred fetches a deferred response to completion; it is
// StreamDeferred plus Result (pi: fetchDeferred as the thin wrapper).
func (m *modelsImpl) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredFetchOptions) (*AssistantMessage, error) {
	result := m.StreamDeferred(ctx, model, handle, options).Result()
	return &result, nil
}

// CancelDeferred cancels one deferred response.
func (m *modelsImpl) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *ModelsDeferredCancelOptions) error {
	provider, err := m.requireProvider(model)
	if err != nil {
		return err
	}
	deferred, ok := provider.(ProviderDeferredStreamer)
	if !ok {
		return auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil)
	}
	var base DeferredCancelOptions
	var transforms *ModelsRequestTransforms
	if options != nil {
		base = options.ProviderRequestOptions
		transforms = &options.ModelsRequestTransforms
	}
	resolved, err := m.resolveRequestAuth(ctx, model, base, "", transforms)
	if err != nil {
		return err
	}
	cancelOptions := DeferredCancelOptions(base)
	applyResolvedToRequestOptions(&cancelOptions, resolved)
	return deferred.CancelDeferred(ctx, resolved.requestModel, handle, &cancelOptions)
}

// CreateProviderOptions are the parts CreateProvider builds a provider from.
type CreateProviderOptions struct {
	ID string
	// Name is the display name; defaults to ID.
	Name    string
	BaseURL string
	Headers ProviderHeaders
	// Auth is required — every provider has auth semantics, even
	// ambient/keyless ones.
	Auth auth.ProviderAuth
	// Models is the static baseline model list (empty for purely dynamic
	// providers).
	Models []Model
	// FetchModels fetches a dynamic model overlay. CreateProvider restores and
	// publishes it transactionally (pi: fetchModels).
	FetchModels func(ctx context.Context, refreshCtx *RefreshModelsContext) ([]Model, error)
	// FilterModels optionally filters credential-specific availability.
	FilterModels func(models []Model, credential auth.Credential) []Model
	// FilterAllModels optionally filters credential-specific availability
	// over the mixed-type catalog (pi: optional filterAllModels provider
	// member). Providers created with it satisfy ProviderAllModelsFilter;
	// those without keep the default chat-FilterModels + non-chat
	// pass-through availability.
	FilterAllModels func(models []Model, credential auth.Credential) []Model
	// API is the single stream implementation for all models.
	API ProviderStreams
	// APIByAPI dispatches on model.API for mixed-API providers; a model
	// whose API has no entry produces a stream error. pi expresses the
	// single/map choice as ProviderStreams | Partial<Record<...>>.
	APIByAPI map[string]ProviderStreams
}

// CreateProvider builds a provider from parts (pi: createProvider). Built-in
// provider factories and models.json custom providers both go through this.
func CreateProvider(input *CreateProviderOptions) Provider {
	p := &composedProvider{
		id:             input.ID,
		name:           input.Name,
		baseURL:        input.BaseURL,
		headers:        input.Headers,
		auth:           input.Auth,
		baselineModels: input.Models,
		api:            input.API,
		apiByAPI:       input.APIByAPI,
		filterModels:   input.FilterModels,
	}
	if p.name == "" {
		p.name = input.ID
	}
	if input.FetchModels != nil {
		p.refreshModels = func(ctx context.Context, refreshCtx *RefreshModelsContext) error {
			if refreshCtx.Stored != nil {
				var restored []Model
				for _, model := range refreshCtx.Stored.Models {
					if string(model.Provider) == input.ID && hasKnownModelType(model) {
						restored = append(restored, model)
					}
				}
				// pi assigns dynamicModels only inside publish's update, so a
				// superseded/aborted publication leaves state untouched.
				published, err := refreshCtx.Publish(ModelsPublication{
					Update: func() {
						p.mu.Lock()
						p.dynamicModels = restored
						p.mu.Unlock()
					},
				})
				if err != nil {
					return err
				}
				if !published {
					return nil
				}
			}
			if !refreshCtx.AllowNetwork || refreshCtx.Context.Err() != nil {
				return nil
			}
			fetched, err := input.FetchModels(refreshCtx.Context, refreshCtx)
			if err != nil {
				return err
			}
			if refreshCtx.Context.Err() != nil {
				return nil
			}
			// Drop fetched entries with unknown type values (pi:
			// filter(hasKnownModelType), v0.99.1 #9948).
			var refreshed []Model
			for _, model := range fetched {
				if hasKnownModelType(model) {
					refreshed = append(refreshed, model)
				}
			}
			now := time.Now().UnixMilli()
			_, err = refreshCtx.Publish(ModelsPublication{
				Persist: &ModelsStoreEntry{Models: refreshed, CheckedAt: &now},
				Update: func() {
					p.mu.Lock()
					p.dynamicModels = refreshed
					p.mu.Unlock()
				},
			})
			return err
		}
	}
	if input.FilterAllModels != nil {
		return &allModelsFilterProvider{composedProvider: p, filter: input.FilterAllModels}
	}
	return p
}

// allModelsFilterProvider wraps composedProvider with the optional
// filterAllModels policy (pi: a provider literal that sets filterAllModels).
// It exists so providers without the policy keep the default availability
// path instead of matching ProviderAllModelsFilter with a nil filter.
type allModelsFilterProvider struct {
	*composedProvider
	filter func(models []Model, credential auth.Credential) []Model
}

// FilterAllModels applies the provider's credential-specific availability
// policy over the mixed-type catalog.
func (p *allModelsFilterProvider) FilterAllModels(models []Model, credential auth.Credential) []Model {
	return p.filter(models, credential)
}

// composedProvider is the CreateProvider implementation. pi builds a closure
// object; pig uses a struct. It implements Provider and, when any API
// implementation supports deferred responses, ProviderDeferredStreamer and
// ProviderModelFilter as applicable.
type composedProvider struct {
	id      string
	name    string
	baseURL string
	headers ProviderHeaders
	auth    auth.ProviderAuth

	mu             sync.Mutex
	baselineModels []Model
	dynamicModels  []Model

	api           ProviderStreams
	apiByAPI      map[string]ProviderStreams
	filterModels  func(models []Model, credential auth.Credential) []Model
	refreshModels func(ctx context.Context, refreshCtx *RefreshModelsContext) error
}

func (p *composedProvider) ID() string               { return p.id }
func (p *composedProvider) Name() string             { return p.name }
func (p *composedProvider) BaseURL() string          { return p.baseURL }
func (p *composedProvider) Headers() ProviderHeaders { return p.headers }
func (p *composedProvider) Auth() auth.ProviderAuth  { return p.auth }

// modelKey is the catalog overlay identity: model type plus id (pi:
// createProvider's dynamic overlay key changed from id to (getModelType, id)
// in v0.99.1 #9948 so one upstream id can expose chat and image entries).
type modelKey struct {
	modelType ModelType
	id        string
}

func modelKeyOf(m Model) modelKey {
	return modelKey{modelType: m.GetModelType(), id: m.ID}
}

// mergedModels returns the full merged baseline + dynamic catalog; a dynamic
// model replaces the baseline entry with the same (type, id) (pi:
// createProvider's currentModels closure).
func (p *composedProvider) mergedModels() []Model {
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := make([]Model, 0, len(p.baselineModels)+len(p.dynamicModels))
	index := make(map[modelKey]int, len(p.baselineModels))
	for _, model := range p.baselineModels {
		index[modelKeyOf(model)] = len(merged)
		merged = append(merged, model)
	}
	for _, model := range p.dynamicModels {
		if i, ok := index[modelKeyOf(model)]; ok {
			merged[i] = model
		} else {
			index[modelKeyOf(model)] = len(merged)
			merged = append(merged, model)
		}
	}
	return merged
}

// GetModels returns the merged chat catalog (pi: getModels =
// currentModels().filter(isModelType "chat")); a dynamic model replaces the
// baseline entry with the same (type, id).
func (p *composedProvider) GetModels() []Model {
	merged := p.mergedModels()
	chat := make([]Model, 0, len(merged))
	for _, model := range merged {
		if model.IsModelType(ModelTypeChat) {
			chat = append(chat, model)
		}
	}
	return chat
}

// GetAllModels returns the merged catalog of every model type (pi:
// getAllModels).
func (p *composedProvider) GetAllModels() []Model {
	return p.mergedModels()
}

// hasKnownModelType reports whether the entry's type is a known model type
// or absent (chat default); fetched catalogs drop everything else (pi:
// hasKnownModelType, v0.99.1 #9948).
func hasKnownModelType(m Model) bool {
	switch m.Type {
	case "", ModelTypeChat, ModelTypeImage, ModelTypeClassifier:
		return true
	default:
		return false
	}
}

// FilterModels applies the configured filter; without one, models pass
// through unchanged (pi: filterModels ?? models).
func (p *composedProvider) FilterModels(models []Model, credential auth.Credential) []Model {
	if p.filterModels == nil {
		return models
	}
	return p.filterModels(models, credential)
}

func (p *composedProvider) RefreshModels(ctx context.Context, refreshCtx *RefreshModelsContext) error {
	if p.refreshModels == nil {
		return nil
	}
	return p.refreshModels(ctx, refreshCtx)
}

func (p *composedProvider) apiFor(model *Model) ProviderStreams {
	if p.api != nil {
		return p.api
	}
	return p.apiByAPI[string(model.API)]
}

func (p *composedProvider) Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	streams := p.apiFor(model)
	if streams == nil {
		return LazyStream(model, func() (*AssistantMessageEventStream, error) {
			return nil, auth.NewModelsError(auth.ModelsErrorStream, fmt.Sprintf("Provider %s has no API implementation for %q", p.id, model.API), nil)
		})
	}
	return streams.Stream(ctx, model, llmContext, options)
}

func (p *composedProvider) StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	streams := p.apiFor(model)
	if streams == nil {
		return LazyStream(model, func() (*AssistantMessageEventStream, error) {
			return nil, auth.NewModelsError(auth.ModelsErrorStream, fmt.Sprintf("Provider %s has no API implementation for %q", p.id, model.API), nil)
		})
	}
	return streams.StreamSimple(ctx, model, llmContext, options)
}

// FetchDeferred routes to the API implementation owning the model, failing
// per-model when it does not support deferred responses (pi: createProvider's
// fetchDeferred closure, assigned whenever any API implementation has the
// capability).
func (p *composedProvider) FetchDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	implementation := p.apiFor(model)
	deferred, ok := implementation.(DeferredStreamer)
	if implementation == nil || !ok {
		return LazyStream(model, func() (*AssistantMessageEventStream, error) {
			return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses for %q", p.id, model.API), nil)
		})
	}
	return deferred.FetchDeferred(ctx, model, handle, options)
}

// CancelDeferred routes to the API implementation owning the model.
func (p *composedProvider) CancelDeferred(ctx context.Context, model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	implementation := p.apiFor(model)
	deferred, ok := implementation.(DeferredStreamer)
	if implementation == nil || !ok {
		return auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s cannot cancel deferred responses for %q", p.id, model.API), nil)
	}
	return deferred.CancelDeferred(ctx, model, handle, options)
}

// ModelsAreEqual checks whether two models are equal by comparing both their
// ModelsAreEqual checks whether two models are equal by comparing their
// type, id, and provider; nil models are never equal (pi: modelsAreEqual,
// type-aware since v0.99.1 #9948).
func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider && a.GetModelType() == b.GetModelType()
}

// CalculateCost fills Usage.Cost from the model's rates. Anthropic charges
// 2x base input for 1h cache writes. pi mutates the passed usage in place;
// pig does the same and returns the cost for convenience.
func CalculateCost(model *Model, usage *Usage) *Cost {
	inputTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	rates := model.Cost.ModelCostRates
	matchedThreshold := int64(-1)
	for i := range model.Cost.Tiers {
		tier := &model.Cost.Tiers[i]
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > matchedThreshold {
			rates = tier.ModelCostRates
			matchedThreshold = tier.InputTokensAbove
		}
	}

	longWrite := int64(0)
	if usage.CacheWrite1h != nil {
		longWrite = *usage.CacheWrite1h
	}
	shortWrite := usage.CacheWrite - longWrite
	usage.Cost.Input = (rates.Input / 1_000_000) * float64(usage.Input)
	usage.Cost.Output = (rates.Output / 1_000_000) * float64(usage.Output)
	usage.Cost.CacheRead = (rates.CacheRead / 1_000_000) * float64(usage.CacheRead)
	usage.Cost.CacheWrite = (rates.CacheWrite*float64(shortWrite) + rates.Input*2*float64(longWrite)) / 1_000_000
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return &usage.Cost
}

// extendedThinkingLevels orders all model thinking levels.
var extendedThinkingLevels = []ModelThinkingLevel{
	ModelThinkingOff, ModelThinkingMinimal, ModelThinkingLow, ModelThinkingMedium,
	ModelThinkingHigh, ModelThinkingXHigh, ModelThinkingMax,
}

// GetSupportedThinkingLevels lists the thinking levels a model supports:
// null in ThinkingLevelMap marks a level unsupported, xhigh/max additionally
// require an explicit mapping (pi: getSupportedThinkingLevels).
func GetSupportedThinkingLevels(model *Model) []ModelThinkingLevel {
	if !model.Reasoning {
		return []ModelThinkingLevel{ModelThinkingOff}
	}
	var out []ModelThinkingLevel
	for _, level := range extendedThinkingLevels {
		var value *string
		var present bool
		if model.ThinkingLevelMap != nil {
			value, present = model.ThinkingLevelMap[level]
		}
		if present && value == nil {
			continue
		}
		if level == ModelThinkingXHigh || level == ModelThinkingMax {
			if !present {
				continue
			}
		}
		out = append(out, level)
	}
	return out
}

// ClampThinkingLevel returns the nearest supported thinking level: the level
// itself when supported, else the closest higher one, else the closest lower
// one, else the first supported level.
func ClampThinkingLevel(model *Model, level ModelThinkingLevel) ModelThinkingLevel {
	available := GetSupportedThinkingLevels(model)
	for _, candidate := range available {
		if candidate == level {
			return level
		}
	}
	requestedIndex := -1
	for i, candidate := range extendedThinkingLevels {
		if candidate == level {
			requestedIndex = i
			break
		}
	}
	if requestedIndex == -1 {
		if len(available) > 0 {
			return available[0]
		}
		return ModelThinkingOff
	}
	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		for _, candidate := range available {
			if candidate == extendedThinkingLevels[i] {
				return candidate
			}
		}
	}
	for i := requestedIndex - 1; i >= 0; i-- {
		for _, candidate := range available {
			if candidate == extendedThinkingLevels[i] {
				return candidate
			}
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return ModelThinkingOff
}
