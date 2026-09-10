// Ported from pi's test/models-store.test.ts. Lock seams stand in for pi's
// vi.spyOn(lockfile, "lock") mocks.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
)

func catalogModel(provider, id string) ai.Model {
	return ai.Model{
		ID:            id,
		Name:          id,
		API:           ai.ApiOpenAICompletions,
		Provider:      ai.ProviderID(provider),
		BaseURL:       "https://example.test/v1",
		Reasoning:     false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0}},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

func resetSharedModelsCache(t *testing.T) {
	t.Helper()
	sharedModelsFileReadState.mu.Lock()
	sharedModelsFileReadState.path = ""
	sharedModelsFileReadState.file = nil
	sharedModelsFileReadState.mu.Unlock()
}

func writeModelsJSON(t *testing.T, path string, data map[string]any) {
	t.Helper()
	content, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	testBumpMtime(t, path)
}

func modelIDs(models []ai.Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func TestFileModelsStorePersistsProviderCatalogsWithoutReplacingUnrelatedProviders(t *testing.T) {
	resetSharedModelsCache(t)
	tempDir := t.TempDir()
	sharedModelsPath := filepath.Join(tempDir, "models-store.json")
	store := NewFileModelsStore(sharedModelsPath)
	ctx := context.Background()

	if err := store.Write(ctx, "one", &ai.ModelsStoreEntry{Models: []ai.Model{catalogModel("one", "m1")}, CheckedAt: int64Ptr(100)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, "two", &ai.ModelsStoreEntry{Models: []ai.Model{catalogModel("two", "m2")}, CheckedAt: int64Ptr(200)}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewFileModelsStore(sharedModelsPath)
	one, err := reloaded.Read(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(one.Models); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("one = %v, want [m1]", got)
	}
	if one.CheckedAt == nil || *one.CheckedAt != 100 {
		t.Fatalf("one checkedAt = %v, want 100", one.CheckedAt)
	}
	two, err := reloaded.Read(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(two.Models); len(got) != 1 || got[0] != "m2" {
		t.Fatalf("two = %v, want [m2]", got)
	}

	if err := reloaded.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if got, err := reloaded.Read(ctx, "one"); err != nil || got != nil {
		t.Fatalf("one after delete = %v, %v, want nil", got, err)
	}
	two, err = reloaded.Read(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(two.Models); len(got) != 1 || got[0] != "m2" {
		t.Fatalf("two after delete = %v, want [m2]", got)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestFileModelsStorePreservesTheModeOfAnExistingModelsFile(t *testing.T) {
	if syscall.Getuid() == 0 {
		t.Skip("mode assertions are unreliable as root")
	}
	resetSharedModelsCache(t)
	tempDir := t.TempDir()
	managedModelsPath := filepath.Join(tempDir, "managed-mode.json")
	if err := os.WriteFile(managedModelsPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(managedModelsPath, 0o660); err != nil {
		t.Fatal(err)
	}
	store := NewFileModelsStore(managedModelsPath)

	if err := store.Write(context.Background(), "one", &ai.ModelsStoreEntry{Models: []ai.Model{catalogModel("one", "m1")}, CheckedAt: int64Ptr(100)}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(managedModelsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %o, want 660", info.Mode().Perm())
	}
}

func TestFileModelsStoreCoalescesFileReloadsAcrossConcurrentReadersAndInterleavedStorageInstances(t *testing.T) {
	resetSharedModelsCache(t)
	tempDir := t.TempDir()
	sharedModelsPath := filepath.Join(tempDir, "models-store.json")
	writeModelsJSON(t, sharedModelsPath, map[string]any{
		"one": map[string]any{"models": []any{catalogModel("one", "old")}},
		"two": map[string]any{"models": []any{catalogModel("two", "m2")}},
	})
	first := NewFileModelsStore(sharedModelsPath)
	second := NewFileModelsStore(sharedModelsPath)
	lockCalls := withLockSpy(t)

	ctx := context.Background()
	type readResult struct {
		entry *ai.ModelsStoreEntry
		err   error
	}
	var wg sync.WaitGroup
	oneCh := make(chan readResult, 1)
	twoCh := make(chan readResult, 1)
	missingCh := make(chan readResult, 1)
	wg.Add(3)
	go func() { defer wg.Done(); entry, err := first.Read(ctx, "one"); oneCh <- readResult{entry, err} }()
	go func() { defer wg.Done(); entry, err := second.Read(ctx, "two"); twoCh <- readResult{entry, err} }()
	go func() { defer wg.Done(); entry, err := first.Read(ctx, "missing"); missingCh <- readResult{entry, err} }()
	wg.Wait()
	one := <-oneCh
	two := <-twoCh
	missing := <-missingCh
	if one.err != nil || two.err != nil || missing.err != nil {
		t.Fatalf("reads failed: %v %v %v", one.err, two.err, missing.err)
	}
	if got := modelIDs(one.entry.Models); len(got) != 1 || got[0] != "old" {
		t.Fatalf("one = %v, want [old]", got)
	}
	if got := modelIDs(two.entry.Models); len(got) != 1 || got[0] != "m2" {
		t.Fatalf("two = %v, want [m2]", got)
	}
	if missing.entry != nil {
		t.Fatalf("missing = %v, want nil", missing.entry)
	}
	if calls := lockCalls(); calls != 1 {
		t.Fatalf("lock calls = %d, want 1", calls)
	}

	if got, err := second.Read(ctx, "one"); err != nil || len(got.Models) != 1 || got.Models[0].ID != "old" {
		t.Fatalf("cached read = %v, %v, want [old]", got, err)
	}
	if calls := lockCalls(); calls != 1 {
		t.Fatalf("lock calls after cached read = %d, want 1", calls)
	}

	otherPath := filepath.Join(tempDir, "other-models-store.json")
	writeModelsJSON(t, otherPath, map[string]any{})
	other := NewFileModelsStore(otherPath)
	if got, err := other.Read(ctx, "one"); err != nil || got != nil {
		t.Fatalf("other read 1 = %v, %v, want nil", got, err)
	}
	if got, err := other.Read(ctx, "one"); err != nil || got != nil {
		t.Fatalf("other read 2 = %v, %v, want nil", got, err)
	}
	third := NewFileModelsStore(sharedModelsPath)
	writeModelsJSON(t, sharedModelsPath, map[string]any{
		"one": map[string]any{"models": []any{catalogModel("one", "newest-model")}},
	})
	var firstReload, thirdReload readResult
	wg.Add(2)
	go func() { defer wg.Done(); entry, err := first.Read(ctx, "one"); firstReload = readResult{entry, err} }()
	go func() { defer wg.Done(); entry, err := third.Read(ctx, "one"); thirdReload = readResult{entry, err} }()
	wg.Wait()
	if firstReload.err != nil || thirdReload.err != nil {
		t.Fatalf("reloads failed: %v %v", firstReload.err, thirdReload.err)
	}
	if got := modelIDs(firstReload.entry.Models); len(got) != 1 || got[0] != "newest-model" {
		t.Fatalf("first reload = %v, want [newest-model]", got)
	}
	if got := modelIDs(thirdReload.entry.Models); len(got) != 1 || got[0] != "newest-model" {
		t.Fatalf("third reload = %v, want [newest-model]", got)
	}
	if calls := lockCalls(); calls != 3 {
		t.Fatalf("lock calls = %d, want 3", calls)
	}
}

func TestFileModelsStoreKeepsACoalescedReloadAliveWhileAnotherReaderIsStillWaiting(t *testing.T) {
	resetSharedModelsCache(t)
	tempDir := t.TempDir()
	sharedModelsPath := filepath.Join(tempDir, "models-store.json")
	writeModelsJSON(t, sharedModelsPath, map[string]any{
		"one": map[string]any{"models": []any{catalogModel("one", "stored")}},
	})
	store := NewFileModelsStore(sharedModelsPath)

	realAcquire := fsLockAcquire
	granted := make(chan struct{})
	var releaseCalls, calls int
	var mu sync.Mutex
	fsLockAcquire = func(path string, options *fsLockOptions) (func() error, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-granted
		mu.Lock()
		releaseCalls++
		mu.Unlock()
		return func() error { return nil }, nil
	}
	t.Cleanup(func() { fsLockAcquire = realAcquire })

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	type readResult struct {
		entry *ai.ModelsStoreEntry
		err   error
	}
	firstCh := make(chan readResult, 1)
	secondCh := make(chan readResult, 1)
	go func() {
		entry, err := store.Read(firstCtx, "one")
		firstCh <- readResult{entry, err}
	}()
	go func() {
		entry, err := store.Read(secondCtx, "one")
		secondCh <- readResult{entry, err}
	}()

	waitFor(t, 2*time.Second, func() bool {
		store.readState.mu.Lock()
		defer store.readState.mu.Unlock()
		return store.readState.reload != nil && store.readState.reload.readers == 2
	})

	cancelFirst()
	first := <-firstCh
	if !errors.Is(first.err, context.Canceled) {
		t.Fatalf("first error = %v, want context.Canceled", first.err)
	}
	close(granted)
	second := <-secondCh
	if second.err != nil {
		t.Fatal(second.err)
	}
	if got := modelIDs(second.entry.Models); len(got) != 1 || got[0] != "stored" {
		t.Fatalf("second read = %v, want [stored]", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("lock calls = %d, want 1", calls)
	}
	if releaseCalls != 1 {
		t.Fatalf("release calls = %d, want 1", releaseCalls)
	}
}

func TestFileModelsStoreCancelsACatalogWriteWaitingForAHeldFileLockWithoutWritingLater(t *testing.T) {
	resetSharedModelsCache(t)
	tempDir := t.TempDir()
	sharedModelsPath := filepath.Join(tempDir, "models-store.json")
	writeModelsJSON(t, sharedModelsPath, map[string]any{
		"one": map[string]any{"models": []any{catalogModel("one", "existing")}},
	})
	store := NewFileModelsStore(sharedModelsPath)

	lockFile, err := os.OpenFile(lockFilePath(sharedModelsPath), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	lockFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- store.Write(ctx, "two", &ai.ModelsStoreEntry{Models: []ai.Model{catalogModel("two", "cancelled")}})
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	os.Remove(lockFilePath(sharedModelsPath))
	time.Sleep(150 * time.Millisecond)

	content, err := os.ReadFile(sharedModelsPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatal(err)
	}
	if _, hasOne := stored["one"]; !hasOne {
		t.Fatalf("one entry lost: %v", stored)
	}
	if _, hasTwo := stored["two"]; hasTwo {
		t.Fatalf("two written despite cancellation: %v", stored)
	}
}
