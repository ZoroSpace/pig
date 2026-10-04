package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Persistent OAuth credentials for remote MCP servers, modeled on pi's
// extensions/mcp/oauth.ts (McpOAuthCredentialStore, 5806068c2): one JSON file
// (<agentDir>/mcp-auth.json) holding per-server state keyed by server name and
// URL, so servers sharing a URL keep separate accounts. pig has no legacy
// URL-only keys: pig never shipped MCP OAuth before and ~/.pig is isolated
// from ~/.pi, so pi's old-format file is never read (S12-4 §0.3).

// OAuthClientInformation is the stored client registration (dynamic or
// pre-registered).
type OAuthClientInformation struct {
	ClientID         string   `json:"client_id"`
	ClientSecret     string   `json:"client_secret,omitempty"`
	TokenAuthMethod  string   `json:"token_auth_method,omitempty"` // "client_secret_basic" | "client_secret_post" | "none"
	RedirectURIs     []string `json:"redirect_uris,omitempty"`
	ClientIDIssuedAt int64    `json:"client_id_issued_at,omitempty"`
}

// OAuthTokens is a token response (pi OAuthTokens).
type OAuthTokens struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
}

// OAuthServerState is one server's stored OAuth state.
type OAuthServerState struct {
	ServerURL         string                  `json:"serverUrl"`
	ClientInformation *OAuthClientInformation `json:"clientInformation,omitempty"`
	Tokens            *OAuthTokens            `json:"tokens,omitempty"`
	// TokensExpireAt is when the access token expires (ms since epoch), from
	// ExpiresIn at save time.
	TokensExpireAt int64  `json:"tokensExpireAt,omitempty"`
	TokenEndpoint  string `json:"tokenEndpoint,omitempty"` // refreshes reuse the discovered endpoint
	Scope          string `json:"scope,omitempty"`         // scope granted/requested at last sign-in
	Resource       string `json:"resource,omitempty"`      // RFC 8707 resource indicator sent with the flow
}

// CredentialStore persists per-server OAuth state in one JSON file. Safe for
// concurrent use; file reads/writes and the cross-process refresh lock are
// serialized through flock (unix). On windows the lock is a no-op (declared
// deviation: refreshes serialize in-process only).
type CredentialStore struct {
	path string
	// lockDir holds refresh-lock files; empty disables cross-process locking.
	lockDir string

	mu     sync.Mutex
	fileMu *flockFile
}

// NewCredentialStore builds a store backed by path. The refresh-lock files
// live in lockDir (usually the agent dir); empty lockDir means refreshes are
// only serialized within this process.
func NewCredentialStore(path, lockDir string) *CredentialStore {
	return &CredentialStore{path: path, lockDir: lockDir, fileMu: &flockFile{}}
}

// storeKey is the per-server key: name and URL, so servers sharing a URL keep
// separate accounts.
func storeKey(name, serverURL string) string { return name + "|" + serverURL }

func parseStates(content []byte) (map[string]*OAuthServerState, error) {
	states := map[string]*OAuthServerState{}
	if len(strings.TrimSpace(string(content))) == 0 {
		return states, nil
	}
	if err := json.Unmarshal(content, &states); err != nil {
		return nil, err
	}
	return states, nil
}

func serializeStates(states map[string]*OAuthServerState) []byte {
	out, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return []byte("{}\n") // unreachable: states is JSON-safe by construction
	}
	return append(out, '\n')
}

func (s *CredentialStore) withFileLock(fn func() error) error {
	if s.fileMu == nil {
		return fn()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fileMu.lock(s.path); err != nil {
		return err
	}
	defer s.fileMu.unlock()
	return fn()
}

// Load returns the stored state for the server, or nil when none. State for
// another server URL is ignored so credentials never leak across servers
// (pi: provider.own).
func (s *CredentialStore) Load(name, serverURL string) (*OAuthServerState, error) {
	var state *OAuthServerState
	err := s.withFileLock(func() error {
		content, err := os.ReadFile(s.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		states, err := parseStates(content)
		if err != nil {
			return fmt.Errorf("parse %s: %w", s.path, err)
		}
		if stored := states[storeKey(name, serverURL)]; stored != nil && stored.ServerURL == serverURL {
			state = stored
		}
		return nil
	})
	return state, err
}

// Save writes the state for the server, creating the file (and its directory)
// when missing.
func (s *CredentialStore) Save(name, serverURL string, state *OAuthServerState) error {
	return s.withFileLock(func() error {
		return s.mutate(name, serverURL, func(states map[string]*OAuthServerState) {
			states[storeKey(name, serverURL)] = state
		})
	})
}

// Remove deletes the server's stored credentials and reports whether any were
// stored.
func (s *CredentialStore) Remove(name, serverURL string) (bool, error) {
	removed := false
	err := s.withFileLock(func() error {
		return s.mutate(name, serverURL, func(states map[string]*OAuthServerState) {
			if _, ok := states[storeKey(name, serverURL)]; ok {
				delete(states, storeKey(name, serverURL))
				removed = true
			}
		})
	})
	return removed, err
}

func (s *CredentialStore) mutate(name, serverURL string, update func(map[string]*OAuthServerState)) error {
	states := map[string]*OAuthServerState{}
	content, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		states, err = parseStates(content)
		if err != nil {
			return fmt.Errorf("parse %s: %w", s.path, err)
		}
	}
	update(states)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(s.path, serializeStates(states), 0o600)
}

// WithRefreshLock runs fn while no other process refreshes the server's
// tokens (pi: McpOAuthServerStore.withRefreshLock). Held from reading the
// tokens to saving the rotated ones; without it, two processes refreshing
// with the same refresh token lose the grant when the server rotates.
func (s *CredentialStore) WithRefreshLock(name, serverURL string, fn func() error) error {
	if s.lockDir == "" {
		return fn()
	}
	if err := os.MkdirAll(s.lockDir, 0o700); err != nil {
		return err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(storeKey(name, serverURL))))[:16]
	lockPath := filepath.Join(s.lockDir, "mcp-auth-refresh-"+hash+".lock")
	return withFlockWait(lockPath, 25*time.Second, fn)
}

// Tokens returns the stored tokens of a server without taking over anything
// (pi: McpOAuthCredentialStore.tokens — the /mcp panel notices sign-ins done
// by another process).
func (s *CredentialStore) Tokens(name, serverURL string) *OAuthTokens {
	state, err := s.Load(name, serverURL)
	if err != nil || state == nil {
		return nil
	}
	return state.Tokens
}

// writeFileAtomic writes via temp file + rename in the target directory.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

var errLockTimeout = errors.New("timed out waiting for the MCP OAuth refresh lock")
