package mcp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCredentialStoreSaveLoadRemove(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), t.TempDir())

	if state, err := store.Load("docs", "https://mcp.example.com/mcp"); err != nil || state != nil {
		t.Fatalf("empty store load: state=%v err=%v", state, err)
	}
	if removed, err := store.Remove("docs", "https://mcp.example.com/mcp"); err != nil || removed {
		t.Fatalf("remove on empty store: removed=%v err=%v", removed, err)
	}

	state := &OAuthServerState{
		ServerURL: "https://mcp.example.com/mcp",
		Tokens:    &OAuthTokens{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", ExpiresIn: 3600, Scope: "read write"},
	}
	if err := store.Save("docs", "https://mcp.example.com/mcp", state); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Same URL, different server name: separate accounts (5806068c2).
	other, err := store.Load("other", "https://mcp.example.com/mcp")
	if err != nil || other != nil {
		t.Fatalf("state leaked across server names: %v %v", other, err)
	}
	// Same name, different URL: also separate.
	if other, err := store.Load("docs", "https://other.example.com/mcp"); err != nil || other != nil {
		t.Fatalf("state leaked across URLs: %v %v", other, err)
	}

	loaded, err := store.Load("docs", "https://mcp.example.com/mcp")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Tokens == nil || loaded.Tokens.AccessToken != "at" || loaded.Tokens.RefreshToken != "rt" {
		t.Fatalf("round-trip mismatch: %+v", loaded)
	}

	removed, err := store.Remove("docs", "https://mcp.example.com/mcp")
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if state, err := store.Load("docs", "https://mcp.example.com/mcp"); err != nil || state != nil {
		t.Fatalf("post-remove load: state=%v err=%v", state, err)
	}
}

func TestCredentialStoreStateForOtherServerIgnored(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), "")
	// A tampered file whose entry claims another server's URL is ignored.
	path := store.path
	if err := os.WriteFile(path, []byte(`{"docs|https://a/mcp":{"serverUrl":"https://b/mcp","tokens":{"access_token":"stolen","token_type":"Bearer"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load("docs", "https://a/mcp")
	if err != nil || state != nil {
		t.Fatalf("cross-server state was not ignored: %v %v", state, err)
	}
}

func TestCredentialStoreCorruptFile(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), "")
	if err := os.WriteFile(store.path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("docs", "https://a/mcp"); err == nil {
		t.Fatal("expected an error for a corrupt store file")
	}
}

func TestCredentialStoreConcurrentSaves(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), t.TempDir())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := &OAuthServerState{ServerURL: "https://a/mcp", Tokens: &OAuthTokens{AccessToken: "at", TokenType: "Bearer"}}
			if err := store.Save("docs", "https://a/mcp", state); err != nil {
				t.Errorf("save %d: %v", i, err)
			}
			if _, err := store.Load("docs", "https://a/mcp"); err != nil {
				t.Errorf("load %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
}

func TestCredentialStoreTokensHelper(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), "")
	if tokens := store.Tokens("docs", "https://a/mcp"); tokens != nil {
		t.Fatalf("expected nil tokens, got %+v", tokens)
	}
	if err := store.Save("docs", "https://a/mcp", &OAuthServerState{
		ServerURL: "https://a/mcp",
		Tokens:    &OAuthTokens{AccessToken: "at", TokenType: "Bearer"},
	}); err != nil {
		t.Fatal(err)
	}
	if tokens := store.Tokens("docs", "https://a/mcp"); tokens == nil || tokens.AccessToken != "at" {
		t.Fatalf("tokens helper: %+v", tokens)
	}
}

func TestCredentialStoreRefreshLockSerializes(t *testing.T) {
	store := NewCredentialStore(filepath.Join(t.TempDir(), "mcp-auth.json"), t.TempDir())
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithRefreshLock("docs", "https://a/mcp", func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	// A second lock attempt for the same server cannot enter while held.
	acquired := make(chan error, 1)
	go func() {
		acquired <- store.WithRefreshLock("docs", "https://a/mcp", func() error { return nil })
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second refresh lock entered while held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := <-acquired; err != nil {
		t.Fatalf("second lock after release: %v", err)
	}
}
