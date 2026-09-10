// pi: src/core/model-runtime.ts — the configured pi-ai Models collection used
// by coding-agent and SDK consumers.
//
// Segment deviations (declared):
//   - pi's getAvailable/getAuth overloads (optional providerId, string|Model)
//     become distinct Go methods; the all-providers GetAvailable(ctx) keeps
//     the resolver-facing signature.
//   - Cancellation rides the Go context (pi: options.signal on create,
//     refresh, auth operations). ModelRuntimeAuthOverrides therefore has no
//     signal member.
//   - pi's per-provider credential promise chains serialize operations and
//     race callers against their signal; pig serializes on a per-provider
//     mutex and runs the task on a goroutine so a cancelled caller returns
//     immediately while the operation still holds the chain (same pattern as
//     InMemoryAuthStorageBackend).
//   - configureRadiusProviders: pig has no radius provider factory yet (the
//     radius OAuth provider is deferred with the OAuth batch, Phase 2
//     decision); the method rebuilds the builtin map and the radius branch
//     is left at its position.
//   - prepareRequest is generic over TOptions in pi; pig normalizes to a
//     preparedRequest and each caller assembles its own options shape.
//   - The snapshot and composition state are mutex-guarded (pi relies on the
//     event loop).
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/providers"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/samber/lo"
)

// modelRuntimeSnapshot is the cached availability view (pi:
// ModelRuntimeSnapshot).
type modelRuntimeSnapshot struct {
	all                 []ai.Model
	available           []ai.Model
	configuredProviders map[string]bool
	storedProviders     map[string]bool
	auth                map[string]*auth.AuthCheck
}

// CreateModelRuntimeOptions parameterize CreateModelRuntime (pi:
// CreateModelRuntimeOptions). Cancellation rides the context instead of the
// signal member.
type CreateModelRuntimeOptions struct {
	// Credentials is the credential storage; defaults to the file at
	// AuthPath (pi: credentials / authPath).
	Credentials auth.CredentialStore
	AuthPath    string
	// ModelsPath selects models.json: nil = the default agent-dir path, a
	// pointer to "" = no file (pi: modelsPath?: string | null).
	ModelsPath  *string
	ModelsStore ai.ModelsStore
	// ModelsStorePath overrides the catalog store location (pi:
	// modelsStorePath).
	ModelsStorePath string
	// AllowModelNetwork lets create() refresh model catalogs over the
	// network; defaults to false.
	AllowModelNetwork bool
	// ModelRefreshTimeoutMs bounds the create-time network model refresh.
	ModelRefreshTimeoutMs int64
	CatalogBaseURL        string
	// RefreshOnCreate skips the initial catalog and availability refresh
	// when false; static models remain available.
	RefreshOnCreate *bool
}

// ModelRuntimeAuthOverrides adjust one auth resolution (pi:
// ModelRuntimeAuthOverrides; pi's embedded AuthOperationOptions.signal rides
// the context).
type ModelRuntimeAuthOverrides struct {
	APIKey string
	Env    types.ProviderEnv
	// MinOAuthValidityMs requires this much remaining OAuth-token validity;
	// defaults to five minutes. 0 = the default (pi: undefined); use a
	// negative value to disable.
	MinOAuthValidityMs int64
}

func (o *ModelRuntimeAuthOverrides) toResolutionOverrides() *auth.AuthResolutionOverrides {
	if o == nil {
		return nil
	}
	return &auth.AuthResolutionOverrides{
		APIKey:             o.APIKey,
		Env:                o.Env,
		MinOAuthValidityMs: o.MinOAuthValidityMs,
	}
}

// CredentialSynchronizationOperation names one credential mutation (pi:
// CredentialSynchronizationOperation).
type CredentialSynchronizationOperation string

const (
	CredentialSyncLogin               CredentialSynchronizationOperation = "login"
	CredentialSyncLogout              CredentialSynchronizationOperation = "logout"
	CredentialSyncSetRuntimeApiKey    CredentialSynchronizationOperation = "setRuntimeApiKey"
	CredentialSyncRemoveRuntimeApiKey CredentialSynchronizationOperation = "removeRuntimeApiKey"
)

// CredentialSynchronizationError reports that credentials changed
// successfully but the local model/auth snapshot could not be synchronized
// (pi: CredentialSynchronizationError).
type CredentialSynchronizationError struct {
	ProviderID string
	Operation  CredentialSynchronizationOperation
	Credential auth.Credential
	Cause      error
}

func (e *CredentialSynchronizationError) Error() string {
	return fmt.Sprintf("Credential %s committed for %s, but local synchronization failed", e.Operation, e.ProviderID)
}

func (e *CredentialSynchronizationError) Unwrap() error { return e.Cause }

// mergeHeaders merges two header maps case-insensitively, override wins (pi:
// mergeHeaders).
func mergeHeaders(base, override types.ProviderHeaders) types.ProviderHeaders {
	if base == nil && override == nil {
		return nil
	}
	merged := types.ProviderHeaders{}
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range override {
		lowerName := strings.ToLower(name)
		for existingName := range merged {
			if strings.ToLower(existingName) == lowerName {
				delete(merged, existingName)
			}
		}
		merged[name] = value
	}
	return merged
}

// ModelRuntime is the configured Models collection (pi: ModelRuntime).
type ModelRuntime struct {
	models      ai.MutableModels
	credentials *RuntimeCredentials

	mu                       sync.Mutex
	defaultBuiltins          map[string]ai.Provider
	builtins                 map[string]ai.Provider
	nativeExtensionProviders map[string]ai.Provider
	extensionProviders       map[string]*ProviderConfigInput
	compositionErrors        map[string]string
	modelsPath               string
	modelNetworkEnabled      bool
	config                   *ModelConfig
	snapshot                 modelRuntimeSnapshot
	availabilityRefreshSeq   int
	availabilityErrorSeq     int
	providerAvailabilitySeq  map[string]int
	availabilityError        string

	credentialMu     sync.Mutex
	credentialChains map[string]*credentialChain
}

// credentialChain serializes credential operations per provider (pi:
// credentialOperations promise chains).
type credentialChain struct {
	mu sync.Mutex
}

// NewModelRuntime assembles the runtime over explicit inputs (pi: private
// constructor).
func NewModelRuntime(credentials *RuntimeCredentials, config *ModelConfig, modelsPath string, modelsStore ai.ModelsStore, providerList []ai.Provider, modelNetworkEnabled bool) *ModelRuntime {
	r := &ModelRuntime{
		credentials:              credentials,
		config:                   config,
		modelsPath:               modelsPath,
		modelNetworkEnabled:      modelNetworkEnabled,
		defaultBuiltins:          map[string]ai.Provider{},
		builtins:                 map[string]ai.Provider{},
		nativeExtensionProviders: map[string]ai.Provider{},
		extensionProviders:       map[string]*ProviderConfigInput{},
		compositionErrors:        map[string]string{},
		providerAvailabilitySeq:  map[string]int{},
		credentialChains:         map[string]*credentialChain{},
	}
	r.snapshot = modelRuntimeSnapshot{
		configuredProviders: map[string]bool{},
		storedProviders:     map[string]bool{},
		auth:                map[string]*auth.AuthCheck{},
	}
	for _, provider := range providerList {
		r.defaultBuiltins[provider.ID()] = provider
	}
	for providerID, provider := range r.defaultBuiltins {
		r.builtins[providerID] = provider
	}
	r.models = ai.NewModels(&ai.CreateModelsOptions{
		Credentials: credentials,
		ModelsStore: modelsStore,
	})
	r.rebuildProviders()
	return r
}

// CreateModelRuntime creates a fully configured runtime (pi:
// ModelRuntime.create).
func CreateModelRuntime(ctx context.Context, options *CreateModelRuntimeOptions) *ModelRuntime {
	var credentialStore auth.CredentialStore
	if options != nil && options.Credentials != nil {
		credentialStore = options.Credentials
	} else {
		credentialStore = CreateAuthStorage(lo.FromPtr(options).AuthPath)
	}
	credentials := NewRuntimeCredentials(credentialStore)

	modelsPath := filepath.Join(codingagent.GetAgentDir(), "models.json")
	if options != nil && options.ModelsPath != nil {
		modelsPath = *options.ModelsPath
	}
	config := LoadModelConfig(modelsPath)

	var modelsStore ai.ModelsStore
	if options != nil && options.ModelsStore != nil {
		modelsStore = options.ModelsStore
	} else if modelsPath != "" {
		storePath := filepath.Join(filepath.Dir(modelsPath), "models-store.json")
		if options != nil && options.ModelsStorePath != "" {
			storePath = options.ModelsStorePath
		}
		modelsStore = NewFileModelsStore(storePath)
	} else {
		modelsStore = &InMemoryCodingAgentModelsStore{}
	}

	builtinModelDataGeneratedAt := providers.GetBuiltinModelDataGeneratedAt()
	var providerList []ai.Provider
	for _, provider := range providers.BuiltinProviders() {
		// pi maps the radius provider through untouched; pig has no radius
		// provider yet (deferred with the OAuth batch).
		providerList = append(providerList, WithRemoteCatalog(provider, lo.FromPtr(options).CatalogBaseURL, lo.ToPtr(builtinModelDataGeneratedAt)))
	}

	// pi: process.env.PI_OFFLINE === undefined.
	networkEnabled := os.Getenv("PI_OFFLINE") == ""

	runtime := NewModelRuntime(credentials, config, modelsPath, modelsStore, providerList, networkEnabled)
	runtime.configureRadiusProviders()
	runtime.rebuildProviders()

	refreshFromNetwork := runtime.modelNetworkEnabled && options != nil && options.AllowModelNetwork
	refreshCtx := ctx
	if refreshFromNetwork && options != nil && options.ModelRefreshTimeoutMs > 0 {
		var cancel context.CancelFunc
		refreshCtx, cancel = context.WithTimeout(ctx, time.Duration(options.ModelRefreshTimeoutMs)*time.Millisecond)
		defer cancel()
	}
	if options == nil || options.RefreshOnCreate == nil || *options.RefreshOnCreate {
		allowNetwork := refreshFromNetwork
		runtime.Refresh(refreshCtx, &ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork})
	}
	return runtime
}

// configureRadiusProviders rebuilds the builtin map and adds radius gateway
// providers from models.json (pi: configureRadiusProviders). The radius
// provider factory is deferred with the OAuth batch; the rebuild stays.
func (r *ModelRuntime) configureRadiusProviders() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.builtins = map[string]ai.Provider{}
	for providerID, provider := range r.defaultBuiltins {
		r.builtins[providerID] = provider
	}
	// pi: for each config provider with oauth === "radius" and a baseUrl,
	// builtinProviderCatalog.radiusProvider({id, name, gateway}) is added to
	// builtins. pig has no radius provider factory yet — deferred with the
	// OAuth batch; config entries are skipped until then.
}

func (r *ModelRuntime) providerIDs() []string {
	seen := map[string]bool{}
	var ids []string
	appendID := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	r.mu.Lock()
	for id := range r.builtins {
		appendID(id)
	}
	for id := range r.nativeExtensionProviders {
		appendID(id)
	}
	configIDs := r.config.GetProviderIds()
	r.mu.Unlock()
	for _, id := range configIDs {
		appendID(id)
	}
	r.mu.Lock()
	for id := range r.extensionProviders {
		appendID(id)
	}
	r.mu.Unlock()
	return ids
}

func (r *ModelRuntime) configProvider(providerID string) *ModelsJsonProvider {
	r.mu.Lock()
	config := r.config
	r.mu.Unlock()
	if config == nil {
		return nil
	}
	return config.GetProvider(providerID)
}

func (r *ModelRuntime) extensionProvider(providerID string) *ProviderConfigInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.extensionProviders[providerID]
}

func (r *ModelRuntime) baseProvider(providerID string) ai.Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	if provider, ok := r.nativeExtensionProviders[providerID]; ok {
		return provider
	}
	return r.builtins[providerID]
}

// recomposeProvider rebuilds one provider from its layers (pi:
// recomposeProvider).
func (r *ModelRuntime) recomposeProvider(providerID string) {
	base := r.baseProvider(providerID)
	extension := r.extensionProvider(providerID)
	config := r.configProvider(providerID)
	if base == nil && config == nil && extension == nil {
		r.models.DeleteProvider(providerID)
		r.mu.Lock()
		delete(r.compositionErrors, providerID)
		r.mu.Unlock()
		return
	}
	if base != nil && config == nil && extension == nil {
		// No overlays: use the builtin untouched so its auth/login/stream
		// behavior is exact.
		r.models.SetProvider(base)
		r.mu.Lock()
		delete(r.compositionErrors, providerID)
		r.mu.Unlock()
		return
	}
	composed, err := ComposeModelProvider(providerID, base, r.configFor(providerID), extension)
	if err == nil {
		r.models.SetProvider(composed)
		r.mu.Lock()
		delete(r.compositionErrors, providerID)
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.compositionErrors[providerID] = err.Error()
	r.mu.Unlock()
	if base != nil {
		r.models.SetProvider(base)
	} else {
		r.models.DeleteProvider(providerID)
	}
}

// configFor snapshots the config pointer for one recomposition.
func (r *ModelRuntime) configFor(providerID string) *ModelConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

func (r *ModelRuntime) rebuildProviders() {
	r.models.ClearProviders()
	r.mu.Lock()
	r.compositionErrors = map[string]string{}
	r.mu.Unlock()
	for _, providerID := range r.providerIDs() {
		r.recomposeProvider(providerID)
	}
	r.updateModelSnapshot()
}

func (r *ModelRuntime) updateModelSnapshot() {
	all := append([]ai.Model(nil), r.models.GetModels()...)
	r.mu.Lock()
	defer r.mu.Unlock()
	configured := map[string]bool{}
	for providerID := range r.snapshot.configuredProviders {
		configured[providerID] = true
	}
	var available []ai.Model
	for _, model := range all {
		if configured[string(model.Provider)] {
			available = append(available, model)
		}
	}
	r.snapshot.all = all
	r.snapshot.available = available
}

// runAvailabilityRefresh recomputes the availability snapshot (pi:
// runAvailabilityRefresh).
func (r *ModelRuntime) runAvailabilityRefresh(ctx context.Context, seq, errorSeq int) error {
	providerList := r.models.GetProviders()
	available, err := r.models.GetAvailable(ctx, "")
	if err != nil {
		return err
	}
	authChecks := map[string]*auth.AuthCheck{}
	for _, provider := range providerList {
		check, err := r.models.CheckAuth(ctx, provider.ID())
		if err != nil {
			return err
		}
		authChecks[provider.ID()] = check
	}
	credentialList, err := r.credentials.List(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq != r.availabilityRefreshSeq {
		return nil
	}
	configuredProviders := map[string]bool{}
	for providerID, check := range authChecks {
		if check != nil {
			configuredProviders[providerID] = true
		}
	}
	storedProviders := map[string]bool{}
	for _, entry := range credentialList {
		storedProviders[entry.ProviderID] = true
	}
	r.snapshot = modelRuntimeSnapshot{
		all:                 append([]ai.Model(nil), r.models.GetModels()...),
		available:           append([]ai.Model(nil), available...),
		configuredProviders: configuredProviders,
		storedProviders:     storedProviders,
		auth:                authChecks,
	}
	if errorSeq == r.availabilityErrorSeq {
		r.availabilityError = ""
	}
	return nil
}

// queueAvailabilityRefresh schedules a full availability pass (pi:
// queueAvailabilityRefresh).
func (r *ModelRuntime) queueAvailabilityRefresh(ctx context.Context) error {
	r.mu.Lock()
	r.availabilityRefreshSeq++
	seq := r.availabilityRefreshSeq
	r.availabilityErrorSeq++
	errorSeq := r.availabilityErrorSeq
	for providerID := range r.providerAvailabilitySeq {
		r.providerAvailabilitySeq[providerID]++
	}
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	err := r.runAvailabilityRefresh(ctx, seq, errorSeq)
	if err != nil {
		r.mu.Lock()
		if errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
			r.availabilityError = err.Error()
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

// refreshProviderAvailability refreshes one provider's availability (pi:
// refreshProviderAvailability).
func (r *ModelRuntime) refreshProviderAvailability(ctx context.Context, providerID string) error {
	// Invalidate any full availability pass that started before this
	// credential change.
	r.mu.Lock()
	r.availabilityRefreshSeq++
	r.providerAvailabilitySeq[providerID]++
	providerSeq := r.providerAvailabilitySeq[providerID]
	r.availabilityErrorSeq++
	errorSeq := r.availabilityErrorSeq
	snapshot := r.snapshot.clone()
	r.mu.Unlock()

	available, err := raceCtx(ctx, func() ([]ai.Model, error) { return r.models.GetAvailable(ctx, providerID) })
	if err != nil {
		r.recordAvailabilityError(providerID, providerSeq, errorSeq, ctx, err)
		return err
	}
	check, err := raceCtx(ctx, func() (*auth.AuthCheck, error) { return r.models.CheckAuth(ctx, providerID) })
	if err != nil {
		r.recordAvailabilityError(providerID, providerSeq, errorSeq, ctx, err)
		return err
	}
	credential, err := raceCtx(ctx, func() (auth.Credential, error) { return r.credentials.Read(ctx, providerID) })
	if err != nil {
		r.recordAvailabilityError(providerID, providerSeq, errorSeq, ctx, err)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerAvailabilitySeq[providerID] != providerSeq {
		return nil
	}
	configuredProviders := copySet(snapshot.configuredProviders)
	_ = available
	storedProviders := copySet(snapshot.storedProviders)
	authByProvider := copyAuthMap(snapshot.auth)
	if check != nil {
		configuredProviders[providerID] = true
		authByProvider[providerID] = check
	} else {
		delete(configuredProviders, providerID)
		delete(authByProvider, providerID)
	}
	if credential != nil {
		storedProviders[providerID] = true
	} else {
		delete(storedProviders, providerID)
	}
	all := append([]ai.Model(nil), r.models.GetModels()...)
	availableByID := map[string]ai.Model{}
	for _, model := range snapshot.available {
		if string(model.Provider) != providerID {
			availableByID[string(model.Provider)+"\x00"+model.ID] = model
		}
	}
	for _, model := range available {
		availableByID[string(model.Provider)+"\x00"+model.ID] = model
	}
	var newAvailable []ai.Model
	for _, model := range all {
		if entry, ok := availableByID[string(model.Provider)+"\x00"+model.ID]; ok {
			newAvailable = append(newAvailable, entry)
		}
	}
	r.snapshot = modelRuntimeSnapshot{
		all:                 all,
		available:           newAvailable,
		configuredProviders: configuredProviders,
		storedProviders:     storedProviders,
		auth:                authByProvider,
	}
	if errorSeq == r.availabilityErrorSeq {
		r.availabilityError = ""
	}
	return nil
}

func (r *ModelRuntime) recordAvailabilityError(providerID string, providerSeq, errorSeq int, ctx context.Context, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providerAvailabilitySeq[providerID] == providerSeq && errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
		r.availabilityError = err.Error()
	}
}

// raceCtx races a blocking call against the context (pi: pi-ai races auth
// checks with the operation signal so a cancelled caller stops waiting; the
// underlying call may leak, as an unresolved promise would).
func raceCtx[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	ch := make(chan outcome, 1)
	go func() {
		value, err := fn()
		ch <- outcome{value, err}
	}()
	select {
	case out := <-ch:
		return out.value, out.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func copySet(source map[string]bool) map[string]bool {
	out := make(map[string]bool, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func copyAuthMap(source map[string]*auth.AuthCheck) map[string]*auth.AuthCheck {
	out := make(map[string]*auth.AuthCheck, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func (s modelRuntimeSnapshot) clone() modelRuntimeSnapshot {
	return modelRuntimeSnapshot{
		all:                 append([]ai.Model(nil), s.all...),
		available:           append([]ai.Model(nil), s.available...),
		configuredProviders: copySet(s.configuredProviders),
		storedProviders:     copySet(s.storedProviders),
		auth:                copyAuthMap(s.auth),
	}
}

// GetProviders lists the registered providers (pi: getProviders).
func (r *ModelRuntime) GetProviders() []ai.Provider { return r.models.GetProviders() }

// GetProvider returns one provider; nil when absent (pi: getProvider).
func (r *ModelRuntime) GetProvider(providerID string) ai.Provider {
	return r.models.GetProvider(providerID)
}

// GetModels lists models, optionally restricted to one provider (pi:
// getModels(providerId?)).
func (r *ModelRuntime) GetModels(providerID string) []ai.Model {
	all := r.models.GetModels()
	if providerID == "" {
		return all
	}
	var filtered []ai.Model
	for _, model := range all {
		if string(model.Provider) == providerID {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

// GetModel looks up one model (pi: getModel).
func (r *ModelRuntime) GetModel(providerID, modelID string) (ai.Model, bool) {
	return r.models.GetModel(providerID, modelID)
}

// CheckAuth reports provider auth configuration without refreshing OAuth
// (pi: checkAuth).
func (r *ModelRuntime) CheckAuth(ctx context.Context, providerID string) (*auth.AuthCheck, error) {
	return r.models.CheckAuth(ctx, providerID)
}

// GetAvailable refreshes the availability snapshot and returns it (pi:
// getAvailable() with no provider id).
func (r *ModelRuntime) GetAvailable(ctx context.Context) ([]ai.Model, error) {
	if err := r.queueAvailabilityRefresh(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ai.Model(nil), r.snapshot.available...), nil
}

// GetAvailableForProvider returns one provider's available models (pi:
// getAvailable(providerId)).
func (r *ModelRuntime) GetAvailableForProvider(ctx context.Context, providerID string) ([]ai.Model, error) {
	r.mu.Lock()
	errorSeq := r.availabilityErrorSeq + 1
	r.availabilityErrorSeq = errorSeq
	r.mu.Unlock()
	available, err := r.models.GetAvailable(ctx, providerID)
	if err != nil {
		r.mu.Lock()
		if errorSeq == r.availabilityErrorSeq && ctx.Err() == nil {
			r.availabilityError = err.Error()
		}
		r.mu.Unlock()
		return nil, err
	}
	r.mu.Lock()
	if errorSeq == r.availabilityErrorSeq {
		r.availabilityError = ""
	}
	r.mu.Unlock()
	return available, nil
}

// GetAvailableSnapshot returns the cached available list (pi:
// getAvailableSnapshot).
func (r *ModelRuntime) GetAvailableSnapshot() []ai.Model {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ai.Model(nil), r.snapshot.available...)
}

// GetError aggregates config, composition, and availability errors (pi:
// getError).
func (r *ModelRuntime) GetError() string {
	r.mu.Lock()
	compositionErrors := make(map[string]string, len(r.compositionErrors))
	for key, value := range r.compositionErrors {
		compositionErrors[key] = value
	}
	availabilityError := r.availabilityError
	config := r.config
	r.mu.Unlock()
	var errs []string
	if config != nil {
		if configError := config.GetError(); configError != "" {
			errs = append(errs, configError)
		}
	}
	for _, providerID := range sortedKeys(compositionErrors) {
		errs = append(errs, fmt.Sprintf("Provider %q: %s", providerID, compositionErrors[providerID]))
	}
	if availabilityError != "" {
		errs = append(errs, "Availability refresh: "+availabilityError)
	}
	return strings.Join(errs, "\n\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// GetRegisteredProviderConfig returns one extension registration (pi:
// getRegisteredProviderConfig).
func (r *ModelRuntime) GetRegisteredProviderConfig(providerID string) *ProviderConfigInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.extensionProviders[providerID]
}

// GetRegisteredProviderIds lists registered extension provider ids (pi:
// getRegisteredProviderIds).
func (r *ModelRuntime) GetRegisteredProviderIds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	var ids []string
	for id := range r.extensionProviders {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for id := range r.nativeExtensionProviders {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// GetRegisteredNativeProvider returns one native extension provider (pi:
// getRegisteredNativeProvider).
func (r *ModelRuntime) GetRegisteredNativeProvider(providerID string) ai.Provider {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nativeExtensionProviders[providerID]
}

// GetCompatibilityRequestConfig is a compatibility fallback for ModelRegistry
// when provider auth is unconfigured (pi: getCompatibilityRequestConfig).
func (r *ModelRuntime) GetCompatibilityRequestConfig(model ai.Model) CompatibilityRequestConfig {
	config, err := ResolveCompatibilityRequestConfig(
		model,
		r.configProvider(string(model.Provider)),
		r.extensionProvider(string(model.Provider)),
	)
	if err != nil {
		return CompatibilityRequestConfig{}
	}
	return config
}

// IsUsingOAuth reports whether the provider currently authenticates via OAuth
// (pi: isUsingOAuth).
func (r *ModelRuntime) IsUsingOAuth(providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	check := r.snapshot.auth[providerID]
	return check != nil && check.Type == auth.AuthTypeOAuth
}

// IsUsingSubscription reports whether the OAuth auth is subscription-backed
// (pi: isUsingSubscription).
func (r *ModelRuntime) IsUsingSubscription(providerID string) bool {
	if !r.IsUsingOAuth(providerID) {
		return false
	}
	provider := r.models.GetProvider(providerID)
	if provider == nil {
		return false
	}
	oauth := provider.Auth().OAuth
	return oauth != nil && oauth.IsSubscription()
}

// HasConfiguredAuth reports whether provider auth is configured (pi:
// hasConfiguredAuth).
func (r *ModelRuntime) HasConfiguredAuth(providerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot.configuredProviders[providerID]
}

// GetAuth resolves provider-scoped auth (pi: getAuth(string) overload).
func (r *ModelRuntime) GetAuth(ctx context.Context, providerID string, overrides *ModelRuntimeAuthOverrides) (*auth.AuthResult, error) {
	return r.models.GetAuth(ctx, providerID, overrides.toResolutionOverrides())
}

// GetAuthForModel resolves provider auth plus static and configured model
// headers (pi: getAuth(Model) overload).
func (r *ModelRuntime) GetAuthForModel(ctx context.Context, model *ai.Model, overrides *ModelRuntimeAuthOverrides) (*auth.AuthResult, error) {
	resolution, err := r.models.GetAuthForModel(ctx, model, overrides.toResolutionOverrides())
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, nil
	}
	env := map[string]string{}
	for key, value := range resolution.Env {
		env[key] = value
	}
	if overrides != nil {
		for key, value := range overrides.Env {
			env[key] = value
		}
	}
	configuredHeaders, err := ResolveConfiguredModelHeaders(
		*model,
		r.configProvider(string(model.Provider)),
		r.extensionProvider(string(model.Provider)),
		env,
	)
	if err != nil {
		return nil, err
	}
	merged, err := withConfiguredAuth(resolution.Auth, configuredHeaders, false)
	if err != nil {
		return nil, err
	}
	// pi merges headers only ({...resolution.auth, headers: merged}); the
	// authHeader bearer injection is not applied here.
	resolution.Auth.Headers = merged.Headers
	return resolution, nil
}

// enqueueCredentialOperation serializes credential operations per provider
// (pi: enqueueCredentialOperation). pi races the started marker against the
// signal: a caller cancelled before its operation begins rejects immediately
// (and the queued operation never runs); once the operation has started the
// caller waits for it, while cancellation still propagates into the task.
func (r *ModelRuntime) enqueueCredentialOperation(ctx context.Context, providerID string, task func(context.Context) error) error {
	r.credentialMu.Lock()
	chain := r.credentialChains[providerID]
	if chain == nil {
		chain = &credentialChain{}
		r.credentialChains[providerID] = chain
	}
	r.credentialMu.Unlock()

	outcome := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		chain.mu.Lock()
		defer chain.mu.Unlock()
		if err := ctx.Err(); err != nil {
			outcome <- err
			close(started)
			return
		}
		close(started)
		outcome <- task(ctx)
	}()
	select {
	case err := <-outcome:
		return err
	case <-ctx.Done():
		select {
		case <-started:
			return <-outcome
		default:
			// pi: raceWithAbortSignal — the caller rejects immediately while
			// the operation keeps the chain until it settles.
			return ctx.Err()
		}
	}
}

// synchronizeCredentialState refreshes models and availability after a
// credential change (pi: synchronizeCredentialState).
func (r *ModelRuntime) synchronizeCredentialState(ctx context.Context, providerID string, operation CredentialSynchronizationOperation, credential auth.Credential) error {
	err := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		r.recomposeProvider(providerID)
		r.mu.Lock()
		compositionError := r.compositionErrors[providerID]
		r.mu.Unlock()
		if compositionError != "" {
			return errors.New(compositionError)
		}
		result := r.models.Refresh(ctx, &ai.ModelsRefreshOptions{
			AllowNetwork: lo.ToPtr(false),
			Providers:    []string{providerID},
		})
		if result.Aborted {
			return ctx.Err()
		}
		if refreshError, ok := result.Errors[providerID]; ok {
			return refreshError
		}
		r.updateModelSnapshot()
		return r.refreshProviderAvailability(ctx, providerID)
	}()
	if err != nil {
		return &CredentialSynchronizationError{
			ProviderID: providerID,
			Operation:  operation,
			Credential: credential,
			Cause:      err,
		}
	}
	return nil
}

// SetRuntimeApiKey sets a runtime override and synchronizes state (pi:
// setRuntimeApiKey).
func (r *ModelRuntime) SetRuntimeApiKey(ctx context.Context, providerID string, apiKey string) error {
	return r.enqueueCredentialOperation(ctx, providerID, func(ctx context.Context) error {
		r.credentials.SetRuntimeApiKey(providerID, apiKey)
		return r.synchronizeCredentialState(ctx, providerID, CredentialSyncSetRuntimeApiKey,
			&auth.ApiKeyCredential{Key: apiKey})
	})
}

// RemoveRuntimeApiKey clears a runtime override and synchronizes state (pi:
// removeRuntimeApiKey).
func (r *ModelRuntime) RemoveRuntimeApiKey(ctx context.Context, providerID string) error {
	return r.enqueueCredentialOperation(ctx, providerID, func(ctx context.Context) error {
		r.credentials.RemoveRuntimeApiKey(providerID)
		return r.synchronizeCredentialState(ctx, providerID, CredentialSyncRemoveRuntimeApiKey, nil)
	})
}

// ListCredentials lists credential metadata (pi: listCredentials).
func (r *ModelRuntime) ListCredentials(ctx context.Context) ([]auth.CredentialInfo, error) {
	return r.credentials.List(ctx)
}

// GetProviderAuthStatus reports the auth status across layers (pi:
// getProviderAuthStatus).
func (r *ModelRuntime) GetProviderAuthStatus(providerID string) AuthStatus {
	if r.credentials.HasRuntimeApiKey(providerID) {
		return AuthStatus{Configured: true, Source: "runtime"}
	}
	r.mu.Lock()
	stored := r.snapshot.storedProviders[providerID]
	authCheck := r.snapshot.auth[providerID]
	r.mu.Unlock()
	if stored {
		return AuthStatus{Configured: true, Source: "stored"}
	}
	if configured := ConfiguredRequestAuthStatus(r.configProvider(providerID), r.extensionProvider(providerID)); configured != nil {
		return *configured
	}
	if authCheck != nil {
		return AuthStatus{Configured: true, Source: "environment", Label: authCheck.Source}
	}
	return AuthStatus{Configured: false}
}

// preparedRequest carries the prepareRequest outcome (pi: the prepared
// object).
type preparedRequest struct {
	provider ai.Provider
	model    *ai.Model
	apiKey   string
	headers  types.ProviderHeaders
	env      types.ProviderEnv
}

func (r *ModelRuntime) prepareRequest(ctx context.Context, model *ai.Model, options *ModelRuntimeRequestOptions) (*preparedRequest, error) {
	provider := r.models.GetProvider(string(model.Provider))
	if provider == nil {
		return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Unknown provider: %s", model.Provider), nil)
	}
	var overrides *ModelRuntimeAuthOverrides
	if options != nil {
		overrides = &ModelRuntimeAuthOverrides{APIKey: options.APIKey, Env: options.Env}
	}
	resolution, err := r.GetAuthForModel(ctx, model, overrides)
	if err != nil {
		return nil, err
	}
	if resolution == nil {
		return nil, auth.NewModelsError(auth.ModelsErrorAuth, fmt.Sprintf("Provider is not configured: %s", model.Provider), nil)
	}

	var headers types.ProviderHeaders
	if options != nil {
		headers = options.Headers
	}
	mergedHeaders := mergeHeaders(resolution.Auth.Headers, headers)
	if options != nil && options.TransformHeaders != nil {
		transformed, err := options.TransformHeaders(mergedHeaders)
		if err != nil {
			return nil, err
		}
		mergedHeaders = transformed
	}
	var env types.ProviderEnv
	if len(resolution.Env) > 0 || (options != nil && len(options.Env) > 0) {
		env = types.ProviderEnv{}
		for key, value := range resolution.Env {
			env[key] = value
		}
		if options != nil {
			for key, value := range options.Env {
				env[key] = value
			}
		}
	}
	effectiveModel := model
	if resolution.Auth.BaseURL != "" {
		withBase := *model
		withBase.BaseURL = resolution.Auth.BaseURL
		effectiveModel = &withBase
	}
	prepared := &preparedRequest{
		provider: provider,
		model:    effectiveModel,
		env:      env,
		headers:  mergedHeaders,
	}
	if options == nil || options.APIKey == "" {
		prepared.apiKey = resolution.Auth.APIKey
	} else {
		prepared.apiKey = options.APIKey
	}
	return prepared, nil
}

// ModelRuntimeRequestOptions carries the request fields prepareRequest
// consumes across the pi option shapes (apiKey, headers, env,
// transformHeaders).
type ModelRuntimeRequestOptions struct {
	APIKey           string
	Headers          types.ProviderHeaders
	Env              types.ProviderEnv
	TransformHeaders func(headers types.ProviderHeaders) (types.ProviderHeaders, error)
}

// applyPrepared applies the prepared request fields onto the caller's
// provider request options (pi: {...providerOptions, apiKey: ..., headers,
// env}).
func (r *ModelRuntime) applyPrepared(prepared *preparedRequest, requestOptions *types.ProviderRequestOptions) *types.ProviderRequestOptions {
	out := &types.ProviderRequestOptions{}
	if requestOptions != nil {
		*out = *requestOptions
	}
	if out.APIKey == "" {
		out.APIKey = prepared.apiKey
	}
	// prepared.headers already is merge(resolution, caller) passed through
	// the caller's transformHeaders (pi: headers).
	out.Headers = prepared.headers
	if prepared.env != nil {
		merged := types.ProviderEnv{}
		for key, value := range prepared.env {
			merged[key] = value
		}
		for key, value := range out.Env {
			merged[key] = value
		}
		out.Env = merged
	}
	return out
}

// Stream dispatches one streaming request (pi: stream).
func (r *ModelRuntime) Stream(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsStreamOptions) *ai.AssistantMessageEventStream {
	return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
		var request *ModelRuntimeRequestOptions
		var providerOptions *types.ProviderRequestOptions
		if options != nil {
			request = &ModelRuntimeRequestOptions{
				APIKey:           options.APIKey,
				Headers:          options.Headers,
				Env:              options.Env,
				TransformHeaders: options.TransformHeaders,
			}
			providerOptions = &options.ProviderRequestOptions
		}
		prepared, err := r.prepareRequest(ctx, model, request)
		if err != nil {
			return nil, err
		}
		streamOptions := &ai.StreamOptions{}
		if options != nil {
			streamOptions = &options.StreamOptions
		}
		streamOptions.ProviderRequestOptions = *r.applyPrepared(prepared, providerOptions)
		return prepared.provider.Stream(ctx, prepared.model, llmContext, streamOptions), nil
	})
}

// Complete is Stream plus Result (pi: complete).
func (r *ModelRuntime) Complete(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsStreamOptions) (*ai.AssistantMessage, error) {
	// Stream errors are encoded in the final message (pi: complete awaits
	// result()).
	message := r.Stream(ctx, model, llmContext, options).Result()
	return &message, nil
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
func (r *ModelRuntime) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	return ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
		var request *ModelRuntimeRequestOptions
		var providerOptions *types.ProviderRequestOptions
		if options != nil {
			request = &ModelRuntimeRequestOptions{
				APIKey:           options.APIKey,
				Headers:          options.Headers,
				Env:              options.Env,
				TransformHeaders: options.TransformHeaders,
			}
			providerOptions = &options.ProviderRequestOptions
		}
		prepared, err := r.prepareRequest(ctx, model, request)
		if err != nil {
			return nil, err
		}
		simpleOptions := &ai.SimpleStreamOptions{}
		if options != nil {
			simpleOptions = &options.SimpleStreamOptions
		}
		simpleOptions.ProviderRequestOptions = *r.applyPrepared(prepared, providerOptions)
		return prepared.provider.StreamSimple(ctx, prepared.model, llmContext, simpleOptions), nil
	})
}

// CompleteSimple is StreamSimple plus Result (pi: completeSimple).
func (r *ModelRuntime) CompleteSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsSimpleStreamOptions) (*ai.AssistantMessage, error) {
	message := r.StreamSimple(ctx, model, llmContext, options).Result()
	return &message, nil
}

// FetchDeferred fetches a deferred response to completion (pi:
// fetchDeferred).
func (r *ModelRuntime) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredFetchOptions) (*ai.AssistantMessage, error) {
	message, err := func() (*ai.AssistantMessage, error) {
		var request *ModelRuntimeRequestOptions
		var providerOptions *types.ProviderRequestOptions
		if options != nil {
			request = &ModelRuntimeRequestOptions{
				APIKey:           options.APIKey,
				Headers:          options.Headers,
				Env:              options.Env,
				TransformHeaders: options.TransformHeaders,
			}
			providerOptions = &options.ProviderRequestOptions
		}
		prepared, err := r.prepareRequest(ctx, model, request)
		if err != nil {
			return nil, err
		}
		deferredStreamer, ok := prepared.provider.(ai.ProviderDeferredStreamer)
		if !ok {
			return nil, auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil)
		}
		fetchOptions := &ai.DeferredFetchOptions{}
		if options != nil {
			fetchOptions = &options.DeferredFetchOptions
		}
		fetchOptions.ProviderRequestOptions = *r.applyPrepared(prepared, providerOptions)
		stream := deferredStreamer.FetchDeferred(ctx, prepared.model, handle, fetchOptions)
		message := stream.Result()
		return &message, nil
	}()
	if err != nil {
		return nil, err
	}
	return message, nil
}

// CancelDeferred cancels one deferred response (pi: cancelDeferred).
func (r *ModelRuntime) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredCancelOptions) error {
	var request *ModelRuntimeRequestOptions
	var providerOptions *types.ProviderRequestOptions
	if options != nil {
		request = &ModelRuntimeRequestOptions{
			APIKey:           options.APIKey,
			Headers:          options.Headers,
			Env:              options.Env,
			TransformHeaders: options.TransformHeaders,
		}
		providerOptions = &options.ProviderRequestOptions
	}
	prepared, err := r.prepareRequest(ctx, model, request)
	if err != nil {
		return err
	}
	deferredStreamer, ok := prepared.provider.(ai.ProviderDeferredStreamer)
	if !ok {
		return auth.NewModelsError(auth.ModelsErrorProvider, fmt.Sprintf("Provider %s does not support deferred responses", model.Provider), nil)
	}
	// pi: DeferredCancelOptions — pig aliases it to ProviderRequestOptions.
	cancelOptions := &ai.ProviderRequestOptions{}
	if providerOptions != nil {
		*cancelOptions = *providerOptions
	}
	preparedOptions := r.applyPrepared(prepared, providerOptions)
	*cancelOptions = *preparedOptions
	return deferredStreamer.CancelDeferred(ctx, prepared.model, handle, cancelOptions)
}

// Login runs a provider login flow and synchronizes state (pi: login).
func (r *ModelRuntime) Login(ctx context.Context, providerID string, authType auth.AuthType, interaction auth.AuthInteraction) (auth.Credential, error) {
	var credential auth.Credential
	err := r.enqueueCredentialOperation(ctx, providerID, func(ctx context.Context) error {
		stored, err := r.models.Login(ctx, providerID, authType, interaction)
		if err != nil {
			return err
		}
		credential = stored
		return r.synchronizeCredentialState(ctx, providerID, CredentialSyncLogin, stored)
	})
	if err != nil {
		return nil, err
	}
	return credential, nil
}

// Logout removes the stored credential and synchronizes state (pi: logout).
func (r *ModelRuntime) Logout(ctx context.Context, providerID string) error {
	return r.enqueueCredentialOperation(ctx, providerID, func(ctx context.Context) error {
		if err := r.models.Logout(ctx, providerID); err != nil {
			return err
		}
		return r.synchronizeCredentialState(ctx, providerID, CredentialSyncLogout, nil)
	})
}

// Refresh reloads models.json, recomposes providers, and refreshes catalogs
// and availability (pi: refresh).
func (r *ModelRuntime) Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	modelsPath := ""
	r.mu.Lock()
	modelsPath = r.modelsPath
	r.mu.Unlock()
	r.mu.Lock()
	r.config = LoadModelConfig(modelsPath)
	r.mu.Unlock()
	r.configureRadiusProviders()
	if options != nil && options.Providers != nil {
		seen := map[string]bool{}
		for _, providerID := range options.Providers {
			if seen[providerID] {
				continue
			}
			seen[providerID] = true
			r.recomposeProvider(providerID)
		}
		r.updateModelSnapshot()
	} else {
		r.rebuildProviders()
	}

	allowNetwork := r.modelNetworkEnabled
	if options != nil && options.AllowNetwork != nil {
		allowNetwork = *options.AllowNetwork
	}
	refreshOptions := &ai.ModelsRefreshOptions{}
	if options != nil {
		*refreshOptions = *options
	}
	refreshOptions.AllowNetwork = &allowNetwork
	result := r.models.Refresh(ctx, refreshOptions)
	errors := make(map[string]error, len(result.Errors))
	for key, value := range result.Errors {
		errors[key] = value
	}
	r.updateModelSnapshot()
	if options != nil && options.Providers != nil {
		seen := map[string]bool{}
		for _, providerID := range options.Providers {
			if seen[providerID] {
				continue
			}
			seen[providerID] = true
			if err := r.refreshProviderAvailability(ctx, providerID); err != nil {
				if ctx.Err() == nil {
					errors[providerID] = err
				}
			}
		}
	} else {
		// Availability errors are recorded by the latest pass; refreshed
		// models remain usable.
		_ = r.queueAvailabilityRefresh(ctx)
	}
	return ai.ModelsRefreshResult{
		Aborted: result.Aborted || ctx.Err() != nil,
		Errors:  errors,
	}
}

// RegisterNativeProvider registers a Go-native provider (pi:
// registerNativeProvider).
func (r *ModelRuntime) RegisterNativeProvider(provider ai.Provider) error {
	if strings.TrimSpace(provider.ID()) == "" {
		return errors.New("Provider id must not be empty.")
	}
	r.mu.Lock()
	delete(r.extensionProviders, provider.ID())
	r.nativeExtensionProviders[provider.ID()] = provider
	r.mu.Unlock()
	r.recomposeProvider(provider.ID())
	r.updateModelSnapshot()
	// pi fires `void this.refresh({ allowNetwork: false })`; pig runs it
	// synchronously — a background rebuild would expose a transient
	// provider-registry window to concurrent readers (declared deviation).
	allowNetwork := false
	r.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork})
	return nil
}

// RegisterProvider registers an extension provider config (pi:
// registerProvider).
func (r *ModelRuntime) RegisterProvider(providerID string, config *ProviderConfigInput) error {
	// Validate the incoming registration on its own, like the legacy
	// registry: a broken re-registration must throw without touching the
	// stored config.
	if err := ValidateExtensionProvider(providerID, r.baseProvider(providerID), r.configProvider(providerID), config); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.nativeExtensionProviders, providerID)
	// Re-registration merges defined values over the previous registration
	// and preserves undefined ones, matching the legacy ModelRegistry
	// contract.
	effective := &ProviderConfigInput{}
	if previous, ok := r.extensionProviders[providerID]; ok && previous != nil {
		*effective = *previous
	}
	if config != nil {
		if config.Name != "" {
			effective.Name = config.Name
		}
		if config.BaseURL != "" {
			effective.BaseURL = config.BaseURL
		}
		if config.APIKey != "" {
			effective.APIKey = config.APIKey
		}
		if config.API != "" {
			effective.API = config.API
		}
		if config.StreamSimple != nil {
			effective.StreamSimple = config.StreamSimple
		}
		if config.Headers != nil {
			effective.Headers = config.Headers
		}
		if config.AuthHeader != nil {
			effective.AuthHeader = config.AuthHeader
		}
		if config.OAuth != nil {
			effective.OAuth = config.OAuth
		}
		if config.Models != nil {
			effective.Models = config.Models
		}
		if config.RefreshModels != nil {
			effective.RefreshModels = config.RefreshModels
		}
	}
	r.extensionProviders[providerID] = effective
	effectiveConfig := effective
	r.mu.Unlock()

	r.recomposeProvider(providerID)
	r.updateModelSnapshot()

	r.mu.Lock()
	stored := r.snapshot.storedProviders[providerID]
	authSnapshot := copyAuthMap(r.snapshot.auth)
	all := append([]ai.Model(nil), r.snapshot.all...)
	r.mu.Unlock()
	configuredStatus := ConfiguredRequestAuthStatus(r.configProvider(providerID), effectiveConfig)
	if stored || (configuredStatus != nil && configuredStatus.Configured) {
		r.mu.Lock()
		configuredProviders := copySet(r.snapshot.configuredProviders)
		configuredProviders[providerID] = true
		r.mu.Unlock()
		// Provisional entry until the async refresh lands; never clobber a
		// real check result.
		if _, ok := authSnapshot[providerID]; !ok {
			authType := auth.AuthTypeAPIKey
			var source string
			if effectiveConfig.OAuth != nil && effectiveConfig.APIKey == "" {
				authType = auth.AuthTypeOAuth
			}
			source = "configured provider"
			authSnapshot[providerID] = &auth.AuthCheck{Type: authType, Source: source}
		}
		var available []ai.Model
		for _, model := range all {
			if configuredProviders[string(model.Provider)] {
				available = append(available, model)
			}
		}
		r.mu.Lock()
		r.snapshot.auth = authSnapshot
		r.snapshot.configuredProviders = configuredProviders
		r.snapshot.available = available
		r.mu.Unlock()
	}
	// Synchronous for the same reason as RegisterNativeProvider (declared).
	allowNetwork := false
	r.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork})
	return nil
}

// UnregisterProvider removes an extension registration (pi:
// unregisterProvider).
func (r *ModelRuntime) UnregisterProvider(providerID string) {
	r.mu.Lock()
	delete(r.extensionProviders, providerID)
	delete(r.nativeExtensionProviders, providerID)
	r.mu.Unlock()
	r.recomposeProvider(providerID)
	r.updateModelSnapshot()
	// Synchronous for the same reason as RegisterNativeProvider (declared).
	allowNetwork := false
	r.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: &allowNetwork})
}
