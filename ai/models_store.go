package ai

import (
	"context"
	"encoding/json"
	"sync"
)

// pi: models-store.ts.

// ModelsStoreEntry is one persisted provider catalog.
type ModelsStoreEntry struct {
	Models []Model `json:"models"`
	// LastModified is the Unix timestamp from the remote catalog's
	// Last-Modified header.
	LastModified *int64 `json:"lastModified,omitempty"`
	// CheckedAt is the Unix timestamp of the last completed remote check.
	CheckedAt *int64 `json:"checkedAt,omitempty"`
	// ETag is the opaque validator from the remote catalog's ETag header,
	// stored verbatim (quotes included) and echoed back as If-None-Match.
	ETag string `json:"etag,omitempty"`
}

// cloneModelsStoreEntry deep-copies an entry (pi: structuredClone; every
// ModelsStoreEntry field is JSON-roundtrip-safe).
func cloneModelsStoreEntry(entry *ModelsStoreEntry) *ModelsStoreEntry {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	out := new(ModelsStoreEntry)
	if err := json.Unmarshal(data, out); err != nil {
		return nil
	}
	return out
}

// ModelsStore is persistent model catalog storage keyed by provider id.
// Cancellation rides the Go context (pi: ModelsStoreOperationOptions
// .signal).
type ModelsStore interface {
	Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error)
	Write(ctx context.Context, providerID string, entry *ModelsStoreEntry) error
	Delete(ctx context.Context, providerID string) error
}

// InMemoryModelsStore is the default non-persistent ModelsStore.
type InMemoryModelsStore struct {
	mu      sync.Mutex
	entries map[string]*ModelsStoreEntry
}

func (s *InMemoryModelsStore) Read(ctx context.Context, providerID string) (*ModelsStoreEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[providerID]
	if entry == nil {
		return nil, nil
	}
	return cloneModelsStoreEntry(entry), nil
}

func (s *InMemoryModelsStore) Write(ctx context.Context, providerID string, entry *ModelsStoreEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]*ModelsStoreEntry{}
	}
	s.entries[providerID] = cloneModelsStoreEntry(entry)
	return nil
}

func (s *InMemoryModelsStore) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, providerID)
	return nil
}
