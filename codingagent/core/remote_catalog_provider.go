// pi: src/core/remote-catalog-provider.ts — a persisted pi.dev catalog
// overlay on top of a static built-in provider.
//
// Segment deviations (declared):
//   - pi wraps providers via object spread ({...provider, getModels,
//     refreshModels}); pig embeds the base Provider interface in a struct.
//   - The object-form catalog ({providerA: [...], ...}) is iterated in
//     JSON document order via a token decoder (pi: Object.values insertion
//     order), matching pi's merge order.
//   - encodeURIComponent → url.PathEscape (provider ids are simple slugs;
//     edge-case escapes differ, no behavior pins them).
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent"
	agentutils "github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

const defaultCatalogBaseURL = "https://pi.dev"
const remoteCatalogAttemptTimeoutMs = 4_000

// RemoteCatalogRefreshIntervalMs is how long a stored catalog stays fresh
// before a refresh revalidates it (pi: REMOTE_CATALOG_REFRESH_INTERVAL_MS).
const RemoteCatalogRefreshIntervalMs = 4 * 60 * 60 * 1000

// remoteCatalogModelTypes lists the overlay entry types the client accepts;
// entries of unknown types are ignored (pi: REMOTE_CATALOG_MODEL_TYPES /
// isSupportedModelType, #9763).
var remoteCatalogModelTypes = map[ai.ModelType]bool{
	ai.ModelTypeChat:       true,
	ai.ModelTypeImage:      true,
	ai.ModelTypeClassifier: true,
}

// mergeModels overlays the dynamic catalog onto the baseline, keyed by
// (model type, id) so one upstream id can expose chat and image entries
// (pi: withRemoteCatalog merge key, v0.99.1 #9948).
func mergeModels(baseline []ai.Model, dynamic []ai.Model) []ai.Model {
	type modelKey struct {
		modelType ai.ModelType
		id        string
	}
	key := func(m ai.Model) modelKey { return modelKey{m.GetModelType(), m.ID} }
	merged := append([]ai.Model(nil), baseline...)
	index := make(map[modelKey]int, len(merged))
	for i := range merged {
		index[key(merged[i])] = i
	}
	for _, model := range dynamic {
		if i, ok := index[key(model)]; ok {
			merged[i] = model
		} else {
			index[key(model)] = len(merged)
			merged = append(merged, model)
		}
	}
	return merged
}

// parseCatalog decodes one provider catalog body (pi: parseCatalog): a
// top-level array, an object with a "models" array, or a generic object of
// model arrays/values. Entries without an "id" field are dropped.
func parseCatalog(providerID string, body []byte) ([]ai.Model, error) {
	entries, err := catalogEntries(body)
	if err != nil {
		return nil, fmt.Errorf("Invalid model catalog for provider %q", providerID)
	}
	var models []ai.Model
	for _, entry := range entries {
		var probe map[string]json.RawMessage
		if json.Unmarshal(entry, &probe) != nil || probe == nil {
			continue
		}
		if _, ok := probe["id"]; !ok {
			continue
		}
		var model ai.Model
		if err := json.Unmarshal(entry, &model); err != nil {
			continue
		}
		// A missing type decodes as chat (zero value), so legacy chat-only
		// shards keep working even when the server ignores the types=
		// parameter; unknown type values are dropped (pi: isSupportedModelType).
		if !remoteCatalogModelTypes[model.GetModelType()] {
			continue
		}
		model.Provider = ai.ProviderID(providerID)
		models = append(models, model)
	}
	return models, nil
}

// catalogEntries extracts the raw entry array from the three accepted
// catalog shapes, preserving document order for the object form.
func catalogEntries(body []byte) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch delim := tok.(type) {
	case json.Delim:
		if delim == '[' {
			var entries []json.RawMessage
			for dec.More() {
				var entry json.RawMessage
				if err := dec.Decode(&entry); err != nil {
					return nil, err
				}
				entries = append(entries, entry)
			}
			return entries, nil
		}
		if delim == '{' {
			// pi: a "models" array member wins; otherwise Object.values.
			var firstKey string
			var modelsArray []byte
			var values []json.RawMessage
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key := keyTok.(string)
				if firstKey == "" {
					firstKey = key
				}
				var value json.RawMessage
				if err := dec.Decode(&value); err != nil {
					return nil, err
				}
				if key == "models" {
					modelsArray = value
					continue
				}
				values = append(values, value)
			}
			if modelsArray != nil {
				// pi checks Array.isArray(value.models); a non-array "models"
				// member is an invalid catalog.
				var arr []json.RawMessage
				if json.Unmarshal(modelsArray, &arr) != nil {
					return nil, fmt.Errorf("models is not an array")
				}
				return arr, nil
			}
			return values, nil
		}
		return nil, fmt.Errorf("unexpected token")
	default:
		return nil, fmt.Errorf("unexpected catalog shape")
	}
}

// remoteModels returns the stored overlay models, suppressed when the
// generated catalog is at least as new (pi: remoteModels).
func remoteModels(entry *ai.ModelsStoreEntry, localGeneratedAt *int64) []ai.Model {
	if entry == nil {
		return nil
	}
	if !catalogIsNewer(entry.LastModified, localGeneratedAt) {
		return nil
	}
	return entry.Models
}

// catalogIsNewer reports whether a catalog is newer than the model data shipped
// with pig (pi: isNewer). A nil localGeneratedAt means "no generated catalog
// timestamp", so any catalog is newer.
func catalogIsNewer(lastModified *int64, localGeneratedAt *int64) bool {
	if localGeneratedAt == nil {
		return true
	}
	return lastModified != nil && *lastModified > *localGeneratedAt
}

// readCatalog reads a successful pi.dev catalog response. A missing or invalid
// Last-Modified header is 0 (pi: readCatalog).
func readCatalog(providerID string, response *agentutils.FetchResponse) ([]ai.Model, *int64, string, error) {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, nil, "", err
	}
	models, err := parseCatalog(providerID, body)
	if err != nil {
		return nil, nil, "", err
	}
	var lastModified int64
	if parsed, err := http.ParseTime(response.Header.Get("Last-Modified")); err == nil {
		lastModified = parsed.UnixMilli()
	}
	return models, lo.ToPtr(lastModified), response.Header.Get("Etag"), nil
}

// requestCatalog fetches one provider catalog from pi.dev (pi: requestCatalog).
// types= asks for every supported model type; a server that ignores the
// parameter still answers with the legacy chat-only shard, which parseCatalog
// accepts (pi: ?types=chat,image,classifier, #9763).
func requestCatalog(ctx context.Context, providerID string, catalogBaseURL string, validator string) (*agentutils.FetchResponse, error) {
	catalogURL := catalogBaseURL + "/api/models/providers/" + url.PathEscape(providerID) + "?types=chat,image,classifier"
	headers := map[string]string{
		"accept":     "application/json",
		"User-Agent": agentutils.GetPiUserAgent(codingagent.Version),
	}
	if validator != "" {
		headers["if-none-match"] = validator
	}
	return agentutils.FetchWithRetry(ctx, http.MethodGet, catalogURL, headers, &agentutils.FetchRetryOptions{
		AttemptTimeoutMs: lo.ToPtr(int64(remoteCatalogAttemptTimeoutMs)),
	})
}

type remoteCatalogProvider struct {
	ai.Provider
	mu               sync.Mutex
	dynamicModels    []ai.Model
	catalogBaseURL   string
	localGeneratedAt *int64
}

func (p *remoteCatalogProvider) setDynamicModels(models []ai.Model) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dynamicModels = models
}

func (p *remoteCatalogProvider) dynamicModelsSnapshot() []ai.Model {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dynamicModels
}

func (p *remoteCatalogProvider) GetModels() []ai.Model {
	// Chat-only overlay (pi: getModels only stacks chat entries).
	var chatOverlay []ai.Model
	for _, model := range p.dynamicModelsSnapshot() {
		if model.IsModelType(ai.ModelTypeChat) {
			chatOverlay = append(chatOverlay, model)
		}
	}
	return mergeModels(p.Provider.GetModels(), chatOverlay)
}

// GetAllModels returns the baseline plus overlay across every model type (pi:
// getAllModels stacks the full overlay).
func (p *remoteCatalogProvider) GetAllModels() []ai.Model {
	baseline := p.Provider.GetModels()
	if all, ok := p.Provider.(ai.ProviderAllModels); ok {
		baseline = all.GetAllModels()
	}
	return mergeModels(baseline, p.dynamicModelsSnapshot())
}

// RefreshModels refreshes the persisted catalog overlay (pi: refreshModels).
func (p *remoteCatalogProvider) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	stored := refreshCtx.Stored
	restored := make([]ai.Model, 0)
	for _, model := range remoteModels(stored, p.localGeneratedAt) {
		if string(model.Provider) == p.Provider.ID() {
			restored = append(restored, model)
		}
	}
	ok, err := refreshCtx.Publish(ai.ModelsPublication{
		Update: func() {
			p.setDynamicModels(restored)
		},
	})
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !refreshCtx.AllowNetwork || ctx.Err() != nil {
		return nil
	}
	if !refreshCtx.Force && stored != nil && stored.CheckedAt != nil && stored.LastModified != nil &&
		time.Now().UnixMilli()-*stored.CheckedAt < RemoteCatalogRefreshIntervalMs {
		return nil
	}

	// Only revalidate when a cached body backs the validator, so a 304 can
	// never leave the overlay empty.
	var validator string
	if stored != nil && len(stored.Models) > 0 {
		validator = stored.ETag
	}
	response, err := requestCatalog(ctx, p.Provider.ID(), p.catalogBaseURL, validator)
	if err != nil {
		return err
	}
	defer response.Close()
	if ctx.Err() != nil {
		return nil
	}
	checkedAt := time.Now().UnixMilli()
	// Unchanged: dynamicModels already holds the stored overlay, so only the
	// freshness window moves.
	if response.StatusCode == http.StatusNotModified && stored != nil {
		entry := cloneModelsStoreEntryCore(stored)
		entry.CheckedAt = &checkedAt
		_, err := refreshCtx.Publish(ai.ModelsPublication{Persist: entry})
		return err
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusNotImplemented {
		var models []ai.Model
		if stored != nil {
			models = stored.Models
		}
		entry := &ai.ModelsStoreEntry{
			Models:       models,
			CheckedAt:    &checkedAt,
			LastModified: lo.ToPtr(int64(0)),
		}
		_, err := refreshCtx.Publish(ai.ModelsPublication{Persist: entry})
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Transient failure: the cached body and its validator stay valid, so
		// keep the etag and let the next refresh revalidate instead of
		// downloading the catalog.
		var models []ai.Model
		if stored != nil {
			models = stored.Models
		}
		entry := &ai.ModelsStoreEntry{Models: models, CheckedAt: &checkedAt}
		if stored != nil {
			entry.ETag = stored.ETag
			entry.LastModified = stored.LastModified
		}
		if _, err := refreshCtx.Publish(ai.ModelsPublication{Persist: entry}); err != nil {
			return err
		}
		return fmt.Errorf("Model catalog request failed for %s: %d", p.Provider.ID(), response.StatusCode)
	}
	refreshed, lastModified, etag, err := readCatalog(p.Provider.ID(), response)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	entry := &ai.ModelsStoreEntry{
		Models:       refreshed,
		CheckedAt:    &checkedAt,
		LastModified: lastModified,
		ETag:         etag,
	}
	published := remoteModels(entry, p.localGeneratedAt)
	_, err = refreshCtx.Publish(ai.ModelsPublication{
		Persist: entry,
		Update: func() {
			p.setDynamicModels(published)
		},
	})
	return err
}

// WithRemoteCatalog adds a persisted pi.dev catalog overlay to a static
// built-in provider (pi: withRemoteCatalog); an empty catalogBaseURL
// defaults to https://pi.dev, and a nil localGeneratedAt means "no generated
// catalog timestamp".
func WithRemoteCatalog(provider ai.Provider, catalogBaseURL string, localGeneratedAt *int64) ai.Provider {
	if catalogBaseURL == "" {
		catalogBaseURL = defaultCatalogBaseURL
	}
	return &remoteCatalogProvider{
		Provider:         provider,
		catalogBaseURL:   catalogBaseURL,
		localGeneratedAt: localGeneratedAt,
	}
}

// PersonalizeCatalog replaces the catalog with a credential-specific model
// list (pi: PersonalizeCatalog).
type PersonalizeCatalog func(catalog []ai.Model, context *ai.RefreshModelsContext) ([]ai.Model, error)

type personalizedCatalogProvider struct {
	ai.Provider
	mu               sync.Mutex
	personalModels   []ai.Model
	personalize      PersonalizeCatalog
	catalogBaseURL   string
	localGeneratedAt *int64
}

func (p *personalizedCatalogProvider) setPersonalModels(models []ai.Model) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.personalModels = models
}

func (p *personalizedCatalogProvider) personalModelsSnapshot() []ai.Model {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.personalModels
}

// builtinModels returns the provider's full built-in catalog (pi:
// builtinModels = getAllModels?.() ?? getModels()).
func (p *personalizedCatalogProvider) builtinModels() []ai.Model {
	if all, ok := p.Provider.(ai.ProviderAllModels); ok {
		return all.GetAllModels()
	}
	return p.Provider.GetModels()
}

// getAllModels returns the saved personalized list, or the built-in models
// when nothing is saved (pi: getAllModels).
func (p *personalizedCatalogProvider) getAllModels() []ai.Model {
	if personal := p.personalModelsSnapshot(); len(personal) > 0 {
		return personal
	}
	return p.builtinModels()
}

func (p *personalizedCatalogProvider) GetModels() []ai.Model {
	var chat []ai.Model
	for _, model := range p.getAllModels() {
		if model.IsModelType(ai.ModelTypeChat) {
			chat = append(chat, model)
		}
	}
	return chat
}

// GetAllModels returns the saved personalized list across every model type
// (pi: getAllModels stacks the full list).
func (p *personalizedCatalogProvider) GetAllModels() []ai.Model {
	return p.getAllModels()
}

// RefreshModels refreshes the persisted personalized catalog (pi:
// withPersonalizedCatalog refreshModels). The saved entry holds the
// personalized list, not the pi.dev catalog, and replaces the catalog
// including offline. The refresh interval and etag revalidation are skipped.
// An empty list (nothing saved yet, or an empty catalog saved by an older pig
// after a failed or missing pi.dev catalog) falls back to the built-in models.
func (p *personalizedCatalogProvider) RefreshModels(ctx context.Context, refreshCtx *ai.RefreshModelsContext) error {
	stored := refreshCtx.Stored
	var restored []ai.Model
	if stored != nil {
		for _, model := range stored.Models {
			if string(model.Provider) == p.Provider.ID() {
				restored = append(restored, model)
			}
		}
	}
	ok, err := refreshCtx.Publish(ai.ModelsPublication{
		Update: func() {
			p.setPersonalModels(restored)
		},
	})
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if !refreshCtx.AllowNetwork || ctx.Err() != nil {
		return nil
	}

	response, err := requestCatalog(ctx, p.Provider.ID(), p.catalogBaseURL, "")
	if err != nil {
		return err
	}
	defer response.Close()
	if ctx.Err() != nil {
		return nil
	}
	var remote []ai.Model
	if response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusNotImplemented {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("Model catalog request failed for %s: %d", p.Provider.ID(), response.StatusCode)
		}
		models, lastModified, _, err := readCatalog(p.Provider.ID(), response)
		if err != nil {
			return err
		}
		if catalogIsNewer(lastModified, p.localGeneratedAt) {
			remote = models
		}
	}
	personalized, err := p.personalize(mergeModels(p.builtinModels(), remote), refreshCtx)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	checkedAt := time.Now().UnixMilli()
	_, err = refreshCtx.Publish(ai.ModelsPublication{
		Persist: &ai.ModelsStoreEntry{Models: personalized, CheckedAt: &checkedAt},
		Update: func() {
			p.setPersonalModels(personalized)
		},
	})
	return err
}

// WithPersonalizedCatalog wraps a built-in provider so that, on each network
// refresh, the catalog (built-in models plus a newer pi.dev catalog) is passed
// through personalize and the result is saved (pi: withPersonalizedCatalog).
// An empty catalogBaseURL defaults to https://pi.dev; a nil localGeneratedAt
// means "no generated catalog timestamp".
func WithPersonalizedCatalog(provider ai.Provider, personalize PersonalizeCatalog, catalogBaseURL string, localGeneratedAt *int64) ai.Provider {
	if catalogBaseURL == "" {
		catalogBaseURL = defaultCatalogBaseURL
	}
	return &personalizedCatalogProvider{
		Provider:         provider,
		personalize:      personalize,
		catalogBaseURL:   catalogBaseURL,
		localGeneratedAt: localGeneratedAt,
	}
}
