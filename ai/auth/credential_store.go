package auth

import (
	"context"
	"sync"
)

// pi: auth/credential-store.ts.

// InMemoryCredentialStore is the default in-memory credential store. Apps
// inject persistent stores. Keyed by provider id, one credential per
// provider; see CredentialStore. Writes are serialized per provider.
type InMemoryCredentialStore struct {
	mu          sync.Mutex
	locks       map[string]*sync.Mutex
	credentials map[string]Credential
}

func (s *InMemoryCredentialStore) lockFor(providerID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	if s.locks[providerID] == nil {
		s.locks[providerID] = &sync.Mutex{}
	}
	return s.locks[providerID]
}

// Read returns the stored credential, possibly expired; (nil, nil) when
// missing.
func (s *InMemoryCredentialStore) Read(ctx context.Context, providerID string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.credentials[providerID], nil
}

// List returns stored credential metadata without exposing secrets.
func (s *InMemoryCredentialStore) List(ctx context.Context) ([]CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CredentialInfo, 0, len(s.credentials))
	for providerID, credential := range s.credentials {
		out = append(out, CredentialInfo{ProviderID: providerID, Type: credential.CredentialType()})
	}
	return out, nil
}

// Modify is the serialized write path: fn runs under the provider's lock and
// sees the current credential; returning nil leaves the entry unchanged. The
// post-write credential is returned.
func (s *InMemoryCredentialStore) Modify(ctx context.Context, providerID string, fn func(current Credential) (Credential, error)) (Credential, error) {
	lock := s.lockFor(providerID)
	lock.Lock()
	defer lock.Unlock()

	var current Credential
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	current = s.credentials[providerID]
	s.mu.Unlock()

	next, err := fn(current)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if next != nil {
		s.mu.Lock()
		if s.credentials == nil {
			s.credentials = map[string]Credential{}
		}
		s.credentials[providerID] = next
		s.mu.Unlock()
		return next, nil
	}
	return current, nil
}

// Delete removes a credential (logout), serialized against Modify.
func (s *InMemoryCredentialStore) Delete(ctx context.Context, providerID string) error {
	lock := s.lockFor(providerID)
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.credentials, providerID)
	s.mu.Unlock()
	return nil
}
