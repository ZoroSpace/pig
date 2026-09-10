// Ported from pi's test/auth-storage.test.ts. Lock seams (fsLockAcquire /
// fsLockSyncAcquire) stand in for pi's vi.spyOn(lockfile, "lock") mocks; the
// shared read-state caches are reset per test because pig tests use fresh
// temp dirs where pi reused one process-lifetime path.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func resetSharedAuthCaches(t *testing.T) {
	t.Helper()
	sharedAuthFileReadState.mu.Lock()
	sharedAuthFileReadState.authPath = ""
	sharedAuthFileReadState.state = nil
	sharedAuthFileReadState.mu.Unlock()
	sharedModelsFileReadState.mu.Lock()
	sharedModelsFileReadState.path = ""
	sharedModelsFileReadState.file = nil
	sharedModelsFileReadState.mu.Unlock()
}

// credData builds an authStorageData from ordered key/value pairs (map
// literals would randomize the insertion order the storage preserves).
func credData(pairs ...any) authStorageData {
	d := newAuthStorageData()
	for i := 0; i+1 < len(pairs); i += 2 {
		d.set(pairs[i].(string), pairs[i+1].(auth.Credential))
	}
	return d
}

func apiKeyCred(key string) *auth.ApiKeyCredential {
	return &auth.ApiKeyCredential{Key: key}
}

func oauthCred(access, refresh string, expires int64) *auth.OAuthCredential {
	return &auth.OAuthCredential{OAuthCredentials: auth.OAuthCredentials{Access: access, Refresh: refresh, Expires: expires}}
}

var testWriteTick int64

func testBumpMtime(t *testing.T, path string) {
	t.Helper()
	// Revision detection is mtime-based; the container filesystem's coarse
	// timestamp granularity can hide rapid successive writes, so each test
	// write advances the mtime explicitly.
	testWriteTick++
	stamp := time.Now().Add(time.Duration(testWriteTick) * time.Second)
	_ = os.Chtimes(path, stamp, stamp)
}

// writeAuthJSON writes the given ordered key/value pairs (pi's test writes
// rely on JS object insertion order).
func writeAuthJSON(t *testing.T, path string, pairs ...any) {
	t.Helper()
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		key, err := json.Marshal(pairs[i].(string))
		if err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(pairs[i+1])
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, string(key)+":"+string(value))
	}
	content := "{" + strings.Join(parts, ",") + "}"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	testBumpMtime(t, path)
}

func readAuthJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// withLockSpy wraps fsLockAcquire with a counting passthrough (pi:
// vi.spyOn(lockfile, "lock")).
func withLockSpy(t *testing.T) func() int {
	t.Helper()
	realAcquire := fsLockAcquire
	var mu sync.Mutex
	calls := 0
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return realAcquire(path, options)
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

func TestAuthStorageReadsAndResolvesStoredAPIKeyCredentials(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	t.Setenv("TEST_AUTH_STORAGE_KEY", "environment-key")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "$TEST_AUTH_STORAGE_KEY"})
	storage := CreateAuthStorage(authJSONPath)
	credential, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(credential, apiKeyCred("environment-key")) {
		t.Fatalf("got %v, want environment-key credential", credential)
	}
}

func TestAuthStorageResolvesCommandBackedAPIKeyCredentials(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "!printf 'command-key'"})
	storage := CreateAuthStorage(authJSONPath)
	credential, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(credential, apiKeyCred("command-key")) {
		t.Fatalf("got %v, want command-key credential", credential)
	}
}

func TestAuthStorageReturnsOAuthCredentialsUnchanged(t *testing.T) {
	credential := oauthCred("access-token", "refresh-token", time.Now().Add(60_000).UnixMilli())
	storage := NewInMemoryAuthStorage(credData("anthropic", credential))
	got, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, credential) {
		t.Fatalf("got %v, want stored oauth credential", got)
	}
}

func TestAuthStorageCredentialScopedEnvTakesPrecedenceAndRemainsInspectable(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{
			"type": "api_key",
			"key":  "$SCOPED_KEY",
			"env":  map[string]any{"SCOPED_KEY": "scoped-value", "REGION": "test-region"},
		})
	storage := CreateAuthStorage(authJSONPath)
	credential, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	apiKey, ok := credential.(*auth.ApiKeyCredential)
	if !ok {
		t.Fatalf("want api_key credential, got %T", credential)
	}
	if apiKey.Key != "scoped-value" {
		t.Fatalf("key = %q, want scoped-value", apiKey.Key)
	}
	if apiKey.Env["SCOPED_KEY"] != "scoped-value" || apiKey.Env["REGION"] != "test-region" {
		t.Fatalf("env = %v, want scoped entries", apiKey.Env)
	}
}

func TestAuthStorageCoalescesFileReloadsAcrossConcurrentReadersAndStorageInstances(t *testing.T) {
	resetSharedAuthCaches(t)
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"})
	first := CreateAuthStorage(authJSONPath)
	second := CreateAuthStorage(authJSONPath)
	lockCalls := withLockSpy(t)

	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "new"},
		"openai", map[string]any{"type": "api_key", "key": "openai-key"})

	ctx := context.Background()
	var wg sync.WaitGroup
	var anthropic, openai auth.Credential
	var credentials []auth.CredentialInfo
	var errs [3]error
	wg.Add(3)
	go func() { defer wg.Done(); anthropic, errs[0] = first.Read(ctx, "anthropic") }()
	go func() { defer wg.Done(); openai, errs[1] = second.Read(ctx, "openai") }()
	go func() { defer wg.Done(); credentials, errs[2] = first.List(ctx) }()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent read %d failed: %v", i, err)
		}
	}
	if !equalCredentials(anthropic, apiKeyCred("new")) {
		t.Fatalf("anthropic = %v, want new", anthropic)
	}
	if !equalCredentials(openai, apiKeyCred("openai-key")) {
		t.Fatalf("openai = %v, want openai-key", openai)
	}
	wantList := []auth.CredentialInfo{
		{ProviderID: "anthropic", Type: auth.CredentialTypeAPIKey},
		{ProviderID: "openai", Type: auth.CredentialTypeAPIKey},
	}
	if len(credentials) != 2 || credentials[0] != wantList[0] || credentials[1] != wantList[1] {
		t.Fatalf("list = %v, want %v", credentials, wantList)
	}
	if calls := lockCalls(); calls != 1 {
		t.Fatalf("lock calls = %d, want 1", calls)
	}

	if got, err := second.Read(ctx, "anthropic"); err != nil || !equalCredentials(got, apiKeyCred("new")) {
		t.Fatalf("cached read = %v, %v, want new", got, err)
	}
	if calls := lockCalls(); calls != 1 {
		t.Fatalf("lock calls after cached read = %d, want 1", calls)
	}

	otherPath := filepath.Join(tempDir, "other-auth.json")
	writeAuthJSON(t, otherPath,
		"other", map[string]any{"type": "api_key", "key": "other-key"})
	otherFirst := CreateAuthStorage(otherPath)
	otherSecond := CreateAuthStorage(otherPath)
	if _, err := otherFirst.Read(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := otherSecond.Read(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := otherFirst.List(ctx); err != nil {
		t.Fatal(err)
	}
	if calls := lockCalls(); calls != 1 {
		t.Fatalf("lock calls after other-path reads = %d, want 1", calls)
	}

	third := CreateAuthStorage(authJSONPath)
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "newest"})
	var firstReload, thirdReload auth.Credential
	wg.Add(2)
	go func() { defer wg.Done(); firstReload, errs[0] = first.Read(ctx, "anthropic") }()
	go func() { defer wg.Done(); thirdReload, errs[1] = third.Read(ctx, "anthropic") }()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("reload reads failed: %v %v", errs[0], errs[1])
	}
	if !equalCredentials(firstReload, apiKeyCred("newest")) || !equalCredentials(thirdReload, apiKeyCred("newest")) {
		t.Fatalf("reloads = %v / %v, want newest", firstReload, thirdReload)
	}
	if calls := lockCalls(); calls != 2 {
		t.Fatalf("lock calls = %d, want 2", calls)
	}
}

func TestAuthStorageKeepsCoalescedReloadAliveWhileAnotherCredentialReaderIsWaiting(t *testing.T) {
	resetSharedAuthCaches(t)
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"})
	storage := CreateAuthStorage(authJSONPath)
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "new-value"})

	realAcquire := fsLockAcquire
	granted := make(chan struct{})
	releaseCalls := make(chan struct{}, 4)
	var calls int32 = 0
	var mu sync.Mutex
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-granted
		releaseCalls <- struct{}{}
		return func() error { return nil }, nil
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx := context.Background()
	type readResult struct {
		credential auth.Credential
		err        error
	}
	firstCh := make(chan readResult, 1)
	secondCh := make(chan readResult, 1)
	go func() {
		credential, err := storage.Read(firstCtx, "anthropic")
		firstCh <- readResult{credential, err}
	}()
	go func() {
		credential, err := storage.Read(secondCtx, "anthropic")
		secondCh <- readResult{credential, err}
	}()

	// Wait until both readers registered on the coalesced reload (pi relies
	// on microtask ordering to guarantee this).
	waitFor(t, 2*time.Second, func() bool {
		storage.readState.mu.Lock()
		defer storage.readState.mu.Unlock()
		return storage.readState.reload != nil && storage.readState.reload.readers == 2
	})

	cancelFirst()
	first := <-firstCh
	if !errors.Is(first.err, context.Canceled) {
		t.Fatalf("first read error = %v, want context.Canceled", first.err)
	}
	close(granted)
	second := <-secondCh
	if second.err != nil {
		t.Fatal(second.err)
	}
	if !equalCredentials(second.credential, apiKeyCred("new-value")) {
		t.Fatalf("second read = %v, want new-value", second.credential)
	}
	mu.Lock()
	gotCalls := calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("lock calls = %d, want 1", gotCalls)
	}
	select {
	case <-releaseCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("release was not called")
	}
}

func TestAuthStorageCreatesNewAuthFilesWithOwnerOnlyPermissions(t *testing.T) {
	if syscall.Getuid() == 0 {
		t.Skip("mode assertions are unreliable as root")
	}
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	CreateAuthStorage(authJSONPath)
	info, err := os.Stat(authJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestAuthStoragePreservesTheModeOfAnExistingAuthFile(t *testing.T) {
	if syscall.Getuid() == 0 {
		t.Skip("mode assertions are unreliable as root")
	}
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"})
	if err := os.Chmod(authJSONPath, 0o660); err != nil {
		t.Fatal(err)
	}
	storage := CreateAuthStorage(authJSONPath)
	if _, err := storage.Modify(context.Background(), "anthropic", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("new"), nil
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(authJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %o, want 660", info.Mode().Perm())
	}
}

func TestAuthStorageModifyPersistsCredentialPreservingUnrelatedExternalEdits(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"})
	storage := CreateAuthStorage(authJSONPath)
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"},
		"openai", map[string]any{"type": "api_key", "key": "external"})

	if _, err := storage.Modify(context.Background(), "anthropic", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("new"), nil
	}); err != nil {
		t.Fatal(err)
	}

	data := readAuthJSON(t, authJSONPath)
	if data["openai"].(map[string]any)["key"] != "external" {
		t.Fatalf("openai entry lost: %v", data)
	}
	if data["anthropic"].(map[string]any)["key"] != "new" {
		t.Fatalf("anthropic entry not updated: %v", data)
	}
}

func TestAuthStorageModifyWithUndefinedLeavesCurrentCredentialUnchanged(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	storage := CreateAuthStorage(authJSONPath)
	result, err := storage.Modify(context.Background(), "anthropic", func(current auth.Credential) (auth.Credential, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(result, apiKeyCred("stored")) {
		t.Fatalf("modify returned %v, want stored credential", result)
	}
	got, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, apiKeyCred("stored")) {
		t.Fatalf("read = %v, want stored credential", got)
	}
}

func TestAuthStorageSerializesConcurrentModifications(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath)
	first := CreateAuthStorage(authJSONPath)
	second := CreateAuthStorage(authJSONPath)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = first.Modify(ctx, "anthropic", func(current auth.Credential) (auth.Credential, error) {
			return apiKeyCred("anthropic-key"), nil
		})
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = second.Modify(ctx, "openai", func(current auth.Credential) (auth.Credential, error) {
			return apiKeyCred("openai-key"), nil
		})
	}()
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("modifies failed: %v %v", errs[0], errs[1])
	}
	data := readAuthJSON(t, authJSONPath)
	if data["anthropic"].(map[string]any)["key"] != "anthropic-key" || data["openai"].(map[string]any)["key"] != "openai-key" {
		t.Fatalf("file = %v, want both credentials", data)
	}
}

func TestAuthStorageDeleteRemovesOneCredentialWhilePreservingOthers(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "anthropic-key"},
		"openai", map[string]any{"type": "api_key", "key": "openai-key"})
	storage := CreateAuthStorage(authJSONPath)
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "anthropic-key"},
		"openai", map[string]any{"type": "api_key", "key": "openai-key"},
		"google", map[string]any{"type": "api_key", "key": "external-key"})
	ctx := context.Background()
	if err := storage.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	credentials, err := storage.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []auth.CredentialInfo{
		{ProviderID: "openai", Type: auth.CredentialTypeAPIKey},
		{ProviderID: "google", Type: auth.CredentialTypeAPIKey},
	}
	if len(credentials) != 2 || credentials[0] != want[0] || credentials[1] != want[1] {
		t.Fatalf("list = %v, want %v", credentials, want)
	}
	if got, err := storage.Read(ctx, "anthropic"); err != nil || got != nil {
		t.Fatalf("anthropic = %v, %v, want nil", got, err)
	}
	if got, err := storage.Read(ctx, "openai"); err != nil || !equalCredentials(got, apiKeyCred("openai-key")) {
		t.Fatalf("openai = %v, %v", got, err)
	}
	if got, err := storage.Read(ctx, "google"); err != nil || !equalCredentials(got, apiKeyCred("external-key")) {
		t.Fatalf("google = %v, %v", got, err)
	}
}

func TestAuthStorageInMemoryImplementsTheSameCredentialStoreBehavior(t *testing.T) {
	storage := NewInMemoryAuthStorage(credData("anthropic", apiKeyCred("initial")))
	ctx := context.Background()
	got, err := storage.Read(ctx, "anthropic")
	if err != nil || !equalCredentials(got, apiKeyCred("initial")) {
		t.Fatalf("initial read = %v, %v", got, err)
	}
	if _, err := storage.Modify(ctx, "anthropic", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("updated"), nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = storage.Read(ctx, "anthropic")
	if err != nil || !equalCredentials(got, apiKeyCred("updated")) {
		t.Fatalf("updated read = %v, %v", got, err)
	}
	if err := storage.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	credentials, err := storage.List(ctx)
	if err != nil || len(credentials) != 0 {
		t.Fatalf("list = %v, %v, want empty", credentials, err)
	}
}

func TestAuthStorageDoesNotWriteAfterLockAcquisitionFailureAndRecoversOnRetry(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	storage := CreateAuthStorage(authJSONPath)

	realAcquire := fsLockAcquire
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		return nil, errors.New("lock unavailable")
	}
	_, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("new"), nil
	})
	if err == nil || err.Error() != "lock unavailable" {
		t.Fatalf("error = %v, want lock unavailable", err)
	}
	data := readAuthJSON(t, authJSONPath)
	if _, hasOpenAI := data["openai"]; hasOpenAI {
		t.Fatalf("openai written despite lock failure: %v", data)
	}

	fsLockAcquire = realAcquire
	if _, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("new"), nil
	}); err != nil {
		t.Fatal(err)
	}
	data = readAuthJSON(t, authJSONPath)
	if data["anthropic"].(map[string]any)["key"] != "stored" || data["openai"].(map[string]any)["key"] != "new" {
		t.Fatalf("file = %v, want both credentials", data)
	}
}

func TestAuthStorageRetriesABrieflyContendedFileLock(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	backend := NewFileAuthStorageBackend(authJSONPath)

	realAcquire := fsLockAcquire
	var calls, releaseCalls int
	var mu sync.Mutex
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		if calls == 1 {
			return nil, &utils.NodeErr{Code: "ELOCKED", Message: "locked"}
		}
		mu.Lock()
		releaseCalls++
		mu.Unlock()
		return func() error { return nil }, nil
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })

	updateCalls := 0
	err := backend.WithLockAsync(context.Background(), func(current *string) (any, *string, error) {
		mu.Lock()
		updateCalls++
		mu.Unlock()
		return nil, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("lock calls = %d, want 2", calls)
	}
	if updateCalls != 1 {
		t.Fatalf("update calls = %d, want 1", updateCalls)
	}
	if releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", releaseCalls)
	}
}

func TestAuthStorageSurfacesACompromisedFileStorageLock(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	backend := NewFileAuthStorageBackend(authJSONPath)

	compromised := errors.New("lock compromised")
	realAcquire := fsLockAcquire
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		if options != nil && options.onCompromised != nil {
			options.onCompromised(compromised)
		}
		return func() error { return nil }, nil
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })

	updateCalled := false
	err := backend.WithLockAsync(context.Background(), func(current *string) (any, *string, error) {
		updateCalled = true
		empty := "{}"
		return nil, &empty, nil
	})
	if !errors.Is(err, compromised) {
		t.Fatalf("error = %v, want compromised error", err)
	}
	if updateCalled {
		t.Fatal("update ran despite compromised lock")
	}
	data := readAuthJSON(t, authJSONPath)
	if data["anthropic"].(map[string]any)["key"] != "stored" {
		t.Fatalf("file = %v, want stored credential", data)
	}
}

func TestAuthStoragePreAbortedFileOperationsDoNotCreateBackingFileOrRunMutation(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	backend := NewFileAuthStorageBackend(authJSONPath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	updateCalled := false
	err := backend.WithLockAsync(ctx, func(current *string) (any, *string, error) {
		updateCalled = true
		empty := "{}"
		return nil, &empty, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if updateCalled {
		t.Fatal("update ran despite pre-aborted context")
	}
	if _, err := os.Stat(authJSONPath); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("backing file stat = %v, want missing file", err)
	}
}

func TestAuthStorageAbortsWhileWaitingForHeldFileLockWithoutRunningMutationLater(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	// Hold the lock the way a competing process would.
	lockFile, err := os.OpenFile(lockFilePath(authJSONPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	lockFile.Close()

	backend := NewFileAuthStorageBackend(authJSONPath)
	ctx, cancel := context.WithCancel(context.Background())
	updateCalled := false
	var mu sync.Mutex
	type lockResult struct{ err error }
	done := make(chan lockResult, 1)
	go func() {
		err := backend.WithLockAsync(ctx, func(current *string) (any, *string, error) {
			mu.Lock()
			updateCalled = true
			mu.Unlock()
			empty := "{}"
			return nil, &empty, nil
		})
		done <- lockResult{err}
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", result.err)
	}
	os.Remove(lockFilePath(authJSONPath))
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	called := updateCalled
	mu.Unlock()
	if called {
		t.Fatal("update ran after cancellation")
	}
	data := readAuthJSON(t, authJSONPath)
	if data["anthropic"].(map[string]any)["key"] != "stored" {
		t.Fatalf("file = %v, want stored credential", data)
	}
}

func TestAuthStorageReleasesFileLockAcquiredConcurrentlyWithCancellationBeforeMutation(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	backend := NewFileAuthStorageBackend(authJSONPath)

	ctx, cancel := context.WithCancel(context.Background())
	realAcquire := fsLockAcquire
	var releaseCalls int
	var mu sync.Mutex
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		cancel() // pi: the lock resolves concurrently with the abort
		mu.Lock()
		releaseCalls++
		mu.Unlock()
		return func() error { return nil }, nil
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })

	updateCalled := false
	err := backend.WithLockAsync(ctx, func(current *string) (any, *string, error) {
		mu.Lock()
		updateCalled = true
		mu.Unlock()
		empty := "{}"
		return nil, &empty, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if updateCalled {
		t.Fatal("update ran despite cancellation")
	}
	if releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", releaseCalls)
	}
}

func TestAuthStorageHoldsFileLockUntilCancelledActiveCallbackSettlesWithoutCommitting(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	backend := NewFileAuthStorageBackend(authJSONPath)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	blocked := make(chan struct{})
	type lockResult struct{ err error }
	pendingDone := make(chan lockResult, 1)
	go func() {
		err := backend.WithLockAsync(ctx, func(current *string) (any, *string, error) {
			close(started)
			<-blocked
			next := `{"openai":{"type":"api_key","key":"cancelled"}}`
			return nil, &next, nil
		})
		pendingDone <- lockResult{err}
	}()
	<-started
	cancel()

	var competingCalled atomic.Bool
	competingDone := make(chan error, 1)
	go func() {
		err := backend.WithLockAsync(context.Background(), func(current *string) (any, *string, error) {
			competingCalled.Store(true)
			next := `{"google":{"type":"api_key","key":"committed"}}`
			return nil, &next, nil
		})
		competingDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if competingCalled.Load() {
		t.Fatal("competing mutation ran before the cancelled callback settled")
	}
	close(blocked)
	if result := <-pendingDone; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("pending error = %v, want context.Canceled", result.err)
	}
	if err := <-competingDone; err != nil {
		t.Fatal(err)
	}
	if !competingCalled.Load() {
		t.Fatal("competing mutation did not run")
	}
	data := readAuthJSON(t, authJSONPath)
	if data["google"].(map[string]any)["key"] != "committed" {
		t.Fatalf("file = %v, want google committed", data)
	}
}

func TestAuthStorageCancelsASignalledCredentialReadWaitingForAHeldFileLock(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "old"})
	storage := CreateAuthStorage(authJSONPath)
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "new-value"})

	lockFile, err := os.OpenFile(lockFilePath(authJSONPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	lockFile.Close()
	lockCalls := withLockSpy(t)

	ctx, cancel := context.WithCancel(context.Background())
	type readResult struct {
		credential auth.Credential
		err        error
	}
	pending := make(chan readResult, 1)
	go func() {
		credential, err := storage.Read(ctx, "anthropic")
		pending <- readResult{credential, err}
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	result := <-pending
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", result.err)
	}
	os.Remove(lockFilePath(authJSONPath))
	time.Sleep(150 * time.Millisecond)
	// pi pins exactly 1 lock call: in JS the caller's abort rejects the race
	// and aborts the shared reload in microtasks, deterministically before the
	// reload loop's next timer-driven attempt. Go preemption opens a
	// check-then-act window in acquireLockAsync (ctx.Err() check →
	// fsLockAcquire), so one benign extra attempt can slip in before the
	// cancellation is observed. The cancelled waiter still always returns
	// context.Canceled and never completes the reload, so accept the bounded
	// extra attempt (≤ 2 in every interleaving).
	if calls := lockCalls(); calls < 1 || calls > 2 {
		t.Fatalf("lock calls = %d, want 1..2", calls)
	}
	got, err := storage.Read(context.Background(), "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCredentials(got, apiKeyCred("new-value")) {
		t.Fatalf("read = %v, want new-value", got)
	}
}

func TestAuthStorageSerializesInMemoryMutationsAcrossProviders(t *testing.T) {
	storage := NewInMemoryAuthStorage(authStorageData{})
	started := make(chan struct{})
	blocked := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(context.Background(), "anthropic", func(current auth.Credential) (auth.Credential, error) {
			close(started)
			<-blocked
			return apiKeyCred("anthropic-key"), nil
		})
		firstDone <- err
	}()
	<-started
	secondCalled := false
	var mu sync.Mutex
	secondDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
			mu.Lock()
			secondCalled = true
			mu.Unlock()
			return apiKeyCred("openai-key"), nil
		})
		secondDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	called := secondCalled
	mu.Unlock()
	if called {
		t.Fatal("second mutation ran before the first settled")
	}
	close(blocked)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if !secondCalled {
		t.Fatal("second mutation did not run")
	}
	ctx := context.Background()
	if got, err := storage.Read(ctx, "anthropic"); err != nil || !equalCredentials(got, apiKeyCred("anthropic-key")) {
		t.Fatalf("anthropic = %v, %v", got, err)
	}
	if got, err := storage.Read(ctx, "openai"); err != nil || !equalCredentials(got, apiKeyCred("openai-key")) {
		t.Fatalf("openai = %v, %v", got, err)
	}
}

func TestAuthStorageCancelsAQueuedInMemoryMutationWithoutRunningItLater(t *testing.T) {
	storage := NewInMemoryAuthStorage(authStorageData{})
	started := make(chan struct{})
	blocked := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(context.Background(), "anthropic", func(current auth.Credential) (auth.Credential, error) {
			close(started)
			<-blocked
			return apiKeyCred("anthropic-key"), nil
		})
		firstDone <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	secondCalled := false
	secondDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(ctx, "openai", func(current auth.Credential) (auth.Credential, error) {
			secondCalled = true
			return apiKeyCred("openai-key"), nil
		})
		secondDone <- err
	}()
	// pi: the queued caller rejects as soon as its signal aborts, before the
	// active operation settles.
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("second error = %v, want context.Canceled", err)
	}
	if secondCalled {
		t.Fatal("cancelled queued mutation ran")
	}
	close(blocked)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if secondCalled {
		t.Fatal("cancelled queued mutation ran later")
	}
	if got, err := storage.Read(context.Background(), "openai"); err != nil || got != nil {
		t.Fatalf("openai = %v, %v, want nil", got, err)
	}
}

func TestAuthStoragePreservesStoredCredentialAfterCancellingActiveRefreshMutation(t *testing.T) {
	previous := oauthCred("expired", "refresh-token", 0)
	storage := NewInMemoryAuthStorage(credData("oauth", previous))
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	blocked := make(chan struct{})
	pendingDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(ctx, "oauth", func(current auth.Credential) (auth.Credential, error) {
			close(started)
			<-blocked
			return oauthCred("refreshed", "refresh-token", time.Now().Add(60_000).UnixMilli()), nil
		})
		pendingDone <- err
	}()
	<-started
	cancel()
	if err := <-pendingDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("pending error = %v, want context.Canceled", err)
	}

	competingCalled := false
	var mu sync.Mutex
	competingDone := make(chan error, 1)
	go func() {
		_, err := storage.Modify(context.Background(), "other", func(current auth.Credential) (auth.Credential, error) {
			mu.Lock()
			competingCalled = true
			mu.Unlock()
			return apiKeyCred("other"), nil
		})
		competingDone <- err
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	called := competingCalled
	mu.Unlock()
	if called {
		t.Fatal("competing mutation ran before the cancelled callback settled")
	}
	close(blocked)
	if err := <-competingDone; err != nil {
		t.Fatal(err)
	}
	if !competingCalled {
		t.Fatal("competing mutation did not run")
	}
	got, err := storage.Read(context.Background(), "oauth")
	if err != nil || !equalCredentials(got, previous) {
		t.Fatalf("oauth = %v, %v, want previous", got, err)
	}
}

func TestAuthStorageTranslatesACredentialStoreRefreshFailureAndAllowsALaterRetry(t *testing.T) {
	providerID := "oauth-provider"
	base := NewInMemoryAuthStorage(credData(providerID, oauthCred("expired-access", "refresh-token", 0)))
	var mu sync.Mutex
	failNextModify := true
	credentials := &failingModifyStore{base: base, fail: &failNextModify, mu: &mu}

	provider := ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   providerID,
		Name: "OAuth Provider",
		Auth: auth.ProviderAuth{OAuth: &refreshableOAuth{}},
		API:  notUsedStreams{},
	})
	models := ai.NewModels(&ai.CreateModelsOptions{Credentials: credentials})
	models.SetProvider(provider)

	_, err := models.GetAuth(context.Background(), providerID, nil)
	var modelsErr *auth.ModelsError
	if !errors.As(err, &modelsErr) || modelsErr.Code != auth.ModelsErrorAuth {
		t.Fatalf("error = %v, want ModelsError code auth", err)
	}
	result, err := models.GetAuth(context.Background(), providerID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey != "refreshed-access" {
		t.Fatalf("api key = %q, want refreshed-access", result.Auth.APIKey)
	}
}

type failingModifyStore struct {
	base *AuthStorage
	mu   *sync.Mutex
	fail *bool
}

func (s *failingModifyStore) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	return s.base.Read(ctx, providerID)
}

func (s *failingModifyStore) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	return s.base.List(ctx)
}

func (s *failingModifyStore) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	s.mu.Lock()
	fail := *s.fail
	*s.fail = false
	s.mu.Unlock()
	if fail {
		return nil, errors.New("credential store unavailable")
	}
	return s.base.Modify(ctx, providerID, fn)
}

func (s *failingModifyStore) Delete(ctx context.Context, providerID string) error {
	return s.base.Delete(ctx, providerID)
}

// refreshableOAuth mirrors pi's test oauth auth: refresh succeeds, ToAuth
// exposes the access token as the API key.
type refreshableOAuth struct{}

func (o *refreshableOAuth) Name() string         { return "OAuth" }
func (o *refreshableOAuth) IsSubscription() bool { return false }
func (o *refreshableOAuth) LoginLabel() string   { return "" }
func (o *refreshableOAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.OAuthCredential, error) {
	return nil, errors.New("not used")
}

func (o *refreshableOAuth) Refresh(ctx context.Context, credential *auth.OAuthCredential) (*auth.OAuthCredential, error) {
	out := *credential
	out.Access = "refreshed-access"
	out.Expires = time.Now().Add(60_000).UnixMilli()
	return &out, nil
}

func (o *refreshableOAuth) ToAuth(credential *auth.OAuthCredential) (*auth.ModelAuth, error) {
	return &auth.ModelAuth{APIKey: credential.Access}, nil
}

func TestAuthStorageDoesNotOverwriteMalformedAuthFiles(t *testing.T) {
	tempDir := t.TempDir()
	authJSONPath := filepath.Join(tempDir, "auth.json")
	writeAuthJSON(t, authJSONPath,
		"anthropic", map[string]any{"type": "api_key", "key": "stored"})
	storage := CreateAuthStorage(authJSONPath)
	if err := os.WriteFile(authJSONPath, []byte("{invalid-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("new"), nil
	})
	if err == nil {
		t.Fatal("want an error for malformed auth.json")
	}
	content, err := os.ReadFile(authJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "{invalid-json" {
		t.Fatalf("file = %q, want untouched malformed content", content)
	}
}

// equalCredentials compares credentials by JSON shape (pi: toEqual).
func equalCredentials(a, b auth.Credential) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	var av, bv any
	if json.Unmarshal(aj, &av) != nil || json.Unmarshal(bj, &bv) != nil {
		return false
	}
	return fmt.Sprint(av) == fmt.Sprint(bv)
}

// waitFor polls condition until the deadline.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
