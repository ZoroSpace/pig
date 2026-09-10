// pi: src/core/runtime-credentials.ts — async credential store overlay for
// non-persistent runtime API keys.
package core

import (
	"context"
	"sync"

	"github.com/ZoroSpace/pig/ai/auth"
)

// RuntimeCredentials overlays runtime API-key overrides on a persistent
// credential store (pi: RuntimeCredentials).
type RuntimeCredentials struct {
	mu sync.Mutex
	// store is the persistent store beneath the overlay.
	store auth.CredentialStore
	// overrides are the non-persistent runtime API keys (pi: Map<string,
	// string>).
	overrides map[string]string
}

// NewRuntimeCredentials builds an overlay on store (pi: constructor).
func NewRuntimeCredentials(store auth.CredentialStore) *RuntimeCredentials {
	return &RuntimeCredentials{store: store, overrides: map[string]string{}}
}

// SetRuntimeApiKey sets a non-persistent runtime override (pi:
// setRuntimeApiKey).
func (c *RuntimeCredentials) SetRuntimeApiKey(providerID string, apiKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overrides[providerID] = apiKey
}

// RemoveRuntimeApiKey clears one runtime override (pi: removeRuntimeApiKey).
func (c *RuntimeCredentials) RemoveRuntimeApiKey(providerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.overrides, providerID)
}

// HasRuntimeApiKey reports whether a runtime override exists (pi:
// hasRuntimeApiKey).
func (c *RuntimeCredentials) HasRuntimeApiKey(providerID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.overrides[providerID]
	return ok
}

func (c *RuntimeCredentials) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	override, ok := c.overrides[providerID]
	c.mu.Unlock()
	if ok {
		return &auth.ApiKeyCredential{Key: override}, nil
	}
	return c.store.Read(ctx, providerID)
}

func (c *RuntimeCredentials) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	stored, err := c.store.List(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// pi: build an insertion-ordered map of stored entries, then overlay
	// runtime overrides; pig keeps the stored order and appends new override
	// keys.
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := make([]auth.CredentialInfo, 0, len(stored)+len(c.overrides))
	seen := map[string]int{}
	for _, entry := range stored {
		seen[entry.ProviderID] = len(entries)
		entries = append(entries, entry)
	}
	for providerID := range c.overrides {
		if idx, ok := seen[providerID]; ok {
			entries[idx] = auth.CredentialInfo{ProviderID: providerID, Type: auth.CredentialTypeAPIKey}
			continue
		}
		entries = append(entries, auth.CredentialInfo{ProviderID: providerID, Type: auth.CredentialTypeAPIKey})
	}
	return entries, nil
}

func (c *RuntimeCredentials) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	return c.store.Modify(ctx, providerID, fn)
}

func (c *RuntimeCredentials) Delete(ctx context.Context, providerID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.store.Delete(ctx, providerID); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.overrides, providerID)
	return nil
}
