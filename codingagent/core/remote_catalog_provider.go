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

func mergeModels(baseline []ai.Model, dynamic []ai.Model) []ai.Model {
	merged := append([]ai.Model(nil), baseline...)
	for _, model := range dynamic {
		index := -1
		for i := range merged {
			if merged[i].ID == model.ID {
				index = i
				break
			}
		}
		if index >= 0 {
			merged[index] = model
		} else {
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
	if localGeneratedAt != nil && (entry.LastModified == nil || *entry.LastModified <= *localGeneratedAt) {
		return nil
	}
	return entry.Models
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
	return mergeModels(p.Provider.GetModels(), p.dynamicModelsSnapshot())
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
	catalogURL := p.catalogBaseURL + "/api/models/providers/" + url.PathEscape(p.Provider.ID())
	headers := map[string]string{
		"accept":     "application/json",
		"User-Agent": agentutils.GetPiUserAgent(codingagent.Version),
	}
	if validator != "" {
		headers["if-none-match"] = validator
	}
	response, err := agentutils.FetchWithRetry(ctx, http.MethodGet, catalogURL, headers, &agentutils.FetchRetryOptions{
		AttemptTimeoutMs: lo.ToPtr(int64(remoteCatalogAttemptTimeoutMs)),
	})
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
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	refreshed, err := parseCatalog(p.Provider.ID(), body)
	if err != nil {
		return err
	}
	var lastModified int64
	if parsed, err := http.ParseTime(response.Header.Get("Last-Modified")); err == nil {
		lastModified = parsed.UnixMilli()
	}
	if ctx.Err() != nil {
		return nil
	}
	entry := &ai.ModelsStoreEntry{
		Models:       refreshed,
		CheckedAt:    &checkedAt,
		LastModified: lo.ToPtr(lastModified),
		ETag:         response.Header.Get("Etag"),
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
