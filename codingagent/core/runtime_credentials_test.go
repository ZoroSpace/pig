// Ported from pi's test/runtime-credentials.test.ts. The signal-forwarding
// test pins ctx identity (pig carries cancellation in the context instead of
// AuthOperationOptions.signal).
package core

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/auth"
)

func TestRuntimeCredentialsRuntimeOverridesMaskStoredCredentialsWithoutPersisting(t *testing.T) {
	storage := NewInMemoryAuthStorage(credData("anthropic", apiKeyCred("stored-key")))
	credentials := NewRuntimeCredentials(storage)

	credentials.SetRuntimeApiKey("anthropic", "runtime-key")
	got, err := credentials.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, apiKeyCred("runtime-key")) {
		t.Fatalf("overlay read = %v, want runtime-key", got)
	}
	stored, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(stored, apiKeyCred("stored-key")) {
		t.Fatalf("stored read = %v, want stored-key", stored)
	}

	credentials.RemoveRuntimeApiKey("anthropic")
	got, err = credentials.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, apiKeyCred("stored-key")) {
		t.Fatalf("read after removal = %v, want stored-key", got)
	}
}

func TestRuntimeCredentialsEnumerationMergesOverridesWithoutExposingKeys(t *testing.T) {
	storage := NewInMemoryAuthStorage(credData("anthropic",
		oauthCred("access", "refresh", time.Now().Add(60_000).UnixMilli())))
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeApiKey("anthropic", "runtime-key")
	credentials.SetRuntimeApiKey("openai", "other-runtime-key")

	got, err := credentials.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []auth.CredentialInfo{
		{ProviderID: "anthropic", Type: auth.CredentialTypeAPIKey},
		{ProviderID: "openai", Type: auth.CredentialTypeAPIKey},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
}

type ctxRecordingStore struct {
	mu        sync.Mutex
	ctxs      []context.Context
	ctxMarker any
}

func (s *ctxRecordingStore) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	s.mu.Lock()
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	return nil, nil
}

func (s *ctxRecordingStore) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	s.mu.Lock()
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	return nil, nil
}

func (s *ctxRecordingStore) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	s.mu.Lock()
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	return nil, nil
}

func (s *ctxRecordingStore) Delete(ctx context.Context, providerID string) error {
	s.mu.Lock()
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	return nil
}

func TestRuntimeCredentialsForwardsOperationContextsToThePersistentStore(t *testing.T) {
	store := &ctxRecordingStore{ctxMarker: "runtime-credentials-ctx"}
	ctx := context.WithValue(context.Background(), ctxMarkerKey("runtime-credentials-ctx"), true)
	credentials := NewRuntimeCredentials(store)

	if _, err := credentials.Read(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Modify(ctx, "anthropic", func(current auth.Credential) (auth.Credential, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}

	if len(store.ctxs) != 4 {
		t.Fatalf("recorded %d calls, want 4", len(store.ctxs))
	}
	for i, recorded := range store.ctxs {
		if recorded != ctx {
			t.Fatalf("call %d ctx identity mismatch", i)
		}
	}
}

type ctxMarkerKey string

func TestRuntimeCredentialsKeepsARuntimeOverrideWhenPersistentDeletionIsCancelled(t *testing.T) {
	storage := NewInMemoryAuthStorage(credData("anthropic", apiKeyCred("stored-key")))
	deleteErr := errors.New("cancelled")
	credentials := NewRuntimeCredentials(&failingDeleteStore{base: storage, err: deleteErr})
	credentials.SetRuntimeApiKey("anthropic", "runtime-key")

	err := credentials.Delete(context.Background(), "anthropic")
	if !errors.Is(err, deleteErr) {
		t.Fatalf("error = %v, want the store failure", err)
	}
	got, err := credentials.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, apiKeyCred("runtime-key")) {
		t.Fatalf("read = %v, want runtime-key override kept", got)
	}
}

type failingDeleteStore struct {
	base  *AuthStorage
	err   error
	calls int
}

func (s *failingDeleteStore) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	return s.base.Read(ctx, providerID)
}

func (s *failingDeleteStore) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	return s.base.List(ctx)
}

func (s *failingDeleteStore) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	return s.base.Modify(ctx, providerID, fn)
}

func (s *failingDeleteStore) Delete(ctx context.Context, providerID string) error {
	s.calls++
	return s.err
}

func TestRuntimeCredentialsDeleteClearsBothTheOverrideAndPersistedCredential(t *testing.T) {
	storage := NewInMemoryAuthStorage(credData("anthropic", apiKeyCred("stored-key")))
	credentials := NewRuntimeCredentials(storage)
	credentials.SetRuntimeApiKey("anthropic", "runtime-key")

	if err := credentials.Delete(context.Background(), "anthropic"); err != nil {
		t.Fatal(err)
	}
	got, err := credentials.Read(context.Background(), "anthropic")
	if err != nil || got != nil {
		t.Fatalf("read = %v, %v, want nil", got, err)
	}
	list, err := credentials.List(context.Background())
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %v, %v, want empty", list, err)
	}
}
