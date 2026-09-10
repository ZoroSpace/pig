// pi: src/core/models-store.ts — locked JSON-backed storage for dynamically
// refreshed provider catalogs.
//
// Segment deviations (declared):
//   - InMemoryCodingAgentModelsStore is behaviorally identical to pig/ai's
//     InMemoryModelsStore (as pi's copy is to pi-ai's), so pig aliases the
//     ai implementation instead of duplicating it.
//   - models-store.json key order: pig marshals maps with sorted keys where
//     JSON.stringify preserves insertion order (content equivalent; no
//     consumer observes the order through the API).
//   - The file lock reuses FileAuthStorageBackend (pi does the same).
package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// InMemoryCodingAgentModelsStore is the non-persistent ModelsStore used by
// the coding agent (pi: InMemoryCodingAgentModelsStore; identical in
// behavior to ai's InMemoryModelsStore).
type InMemoryCodingAgentModelsStore = ai.InMemoryModelsStore

// storedModels is the models-store.json shape (pi: Record<string,
// ModelsStoreEntry>).
type storedModels map[string]*ai.ModelsStoreEntry

func defaultModelsStorePath() string {
	return filepath.Join(codingagent.GetAgentDir(), "models-store.json")
}

// sharedModelsFileReadState lets FileModelsStore instances on the same path
// share a read cache (pi: module-level sharedModelsFileReadState; the first
// path wins for the process lifetime).
var sharedModelsFileReadState struct {
	mu   sync.Mutex
	path string
	file *modelsFileReadState
}

type modelsFileReadState struct {
	mu       sync.Mutex
	data     storedModels
	revision string
	reload   *modelsFileReload
}

// modelsFileReload coalesces concurrent file reloads (pi: ModelsFileReload).
type modelsFileReload struct {
	ctx     context.Context
	cancel  context.CancelFunc
	data    storedModels
	err     error
	done    chan struct{}
	readers int
}

// FileModelsStore is the locked JSON-backed catalog storage (pi:
// FileModelsStore).
type FileModelsStore struct {
	storage   *FileAuthStorageBackend
	path      string
	readState *modelsFileReadState
}

// NewFileModelsStore builds a catalog store; an empty path defaults to
// <agentDir>/models-store.json (pi: constructor default).
func NewFileModelsStore(path string) *FileModelsStore {
	if path == "" {
		path = defaultModelsStorePath()
	}
	normalized := utils.NormalizePath(path, nil)
	s := &FileModelsStore{
		storage:   NewFileAuthStorageBackend(normalized),
		path:      normalized,
		readState: &modelsFileReadState{data: storedModels{}},
	}
	sharedModelsFileReadState.mu.Lock()
	if sharedModelsFileReadState.path == normalized && sharedModelsFileReadState.file != nil {
		s.readState = sharedModelsFileReadState.file
	}
	if sharedModelsFileReadState.file == nil {
		sharedModelsFileReadState.path = normalized
		sharedModelsFileReadState.file = s.readState
	}
	sharedModelsFileReadState.mu.Unlock()
	return s
}

func (s *FileModelsStore) parse(content *string) (storedModels, error) {
	if content == nil || *content == "" {
		return storedModels{}, nil
	}
	var data storedModels
	if err := json.Unmarshal([]byte(utils.StripBom(*content)), &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *FileModelsStore) updateReadState(state *modelsFileReadState, data storedModels, revision string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.data = data
	state.revision = revision
}

func (s *FileModelsStore) reloadFromStorage(ctx context.Context, state *modelsFileReadState) (storedModels, error) {
	return WithLockAsync(ctx, s.storage, func(content *string) (storedModels, *string, error) {
		data, err := s.parse(content)
		if err != nil {
			return nil, nil, err
		}
		s.updateReadState(state, data, utils.GetFileRevision(s.path))
		return data, nil, nil
	})
}

// readLatest returns the freshest known data, coalescing concurrent reloads
// (pi: readLatest).
func (s *FileModelsStore) readLatest(ctx context.Context, state *modelsFileReadState) (storedModels, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	revision := utils.GetFileRevision(s.path)
	state.mu.Lock()
	if revision != "" && revision == state.revision {
		data := state.data
		state.mu.Unlock()
		return data, nil
	}
	if state.reload == nil {
		reloadCtx, cancel := context.WithCancel(context.Background())
		reload := &modelsFileReload{ctx: reloadCtx, cancel: cancel, done: make(chan struct{})}
		state.reload = reload
		go func() {
			defer close(reload.done)
			reload.data, reload.err = s.reloadFromStorage(reloadCtx, state)
			state.mu.Lock()
			if state.reload == reload {
				state.reload = nil
			}
			state.mu.Unlock()
		}()
	}
	reload := state.reload
	reload.readers++
	state.mu.Unlock()

	defer func() {
		state.mu.Lock()
		reload.readers--
		if reload.readers == 0 && state.reload == reload {
			state.reload = nil
			reload.cancel()
		}
		state.mu.Unlock()
	}()

	select {
	case <-reload.done:
		return reload.data, reload.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *FileModelsStore) Read(ctx context.Context, providerID string) (*ai.ModelsStoreEntry, error) {
	data, err := s.readLatest(ctx, s.readState)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entry := data[providerID]
	if entry == nil {
		return nil, nil
	}
	return cloneModelsStoreEntryCore(entry), nil
}

func (s *FileModelsStore) Write(ctx context.Context, providerID string, entry *ai.ModelsStoreEntry) error {
	var latest storedModels
	_, err := WithLockAsync(ctx, s.storage, func(content *string) (any, *string, error) {
		current, err := s.parse(content)
		if err != nil {
			return nil, nil, err
		}
		current[providerID] = cloneModelsStoreEntryCore(entry)
		latest = current
		serialized, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		out := string(serialized)
		return nil, &out, nil
	})
	if err != nil {
		return err
	}
	// pi: updateReadState(this.readState, latest) — the revision is cleared so
	// the next read revalidates.
	s.updateReadState(s.readState, latest, "")
	return nil
}

func (s *FileModelsStore) Delete(ctx context.Context, providerID string) error {
	var latest storedModels
	_, err := WithLockAsync(ctx, s.storage, func(content *string) (any, *string, error) {
		current, err := s.parse(content)
		if err != nil {
			return nil, nil, err
		}
		delete(current, providerID)
		latest = current
		serialized, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		out := string(serialized)
		return nil, &out, nil
	})
	if err != nil {
		return err
	}
	s.updateReadState(s.readState, latest, "")
	return nil
}

// cloneModelsStoreEntryCore deep-copies an entry (pi: structuredClone; every
// ModelsStoreEntry field is JSON-roundtrip-safe).
func cloneModelsStoreEntryCore(entry *ai.ModelsStoreEntry) *ai.ModelsStoreEntry {
	if entry == nil {
		return nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	out := new(ai.ModelsStoreEntry)
	if err := json.Unmarshal(data, out); err != nil {
		return nil
	}
	return out
}
