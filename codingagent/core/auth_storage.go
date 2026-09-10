// pi: src/core/auth-storage.ts — CredentialStore implementation backed by
// auth.json. Provider auth orchestration belongs to ModelRuntime and pig/ai
// Models.
//
// Segment deviations (declared):
//   - pi's AuthStorageBackend has a sync withLock<T> and an async
//     withLockAsync<T>; Go interface methods cannot be generic, so the
//     backend interface carries a non-generic LockFunction and package-level
//     generic helpers (WithLock/WithLockAsync) recover the typed result
//     (same precedent as harness events.go OnTyped). Cancellation rides the
//     Go context (pi: AuthOperationOptions.signal); the sync path passes
//     context.Background().
//   - proper-lockfile → an O_EXCL lockfile at "<path>.lock"; release =
//     removal. The lock content is the creation mtime; an async acquisition
//     steals locks older than the stale window (proper-lockfile semantics).
//     The acquire primitives are package vars so tests can substitute
//     pi's vi.spyOn(lockfile, "lock") seam. pi's synchronous retry loop
//     busy-waits to stay synchronous; pig sleeps.
//   - auth.json key order: JSON.stringify preserves object insertion order;
//     pig tracks the parsed key order explicitly and marshals in that order
//     (JS spread semantics: an updated provider keeps its position, a new
//     one is appended) so files stay line-compatible.
//   - resolveConfigValue returns string | undefined in pi; an undefined
//     resolved key collapses to "" here (pi's {...credential, key: undefined}
//     reads as an absent key downstream; omitempty gives the same JSON).
//   - pi propagates reload failures only when the caller passed a signal and
//     otherwise falls back to the cached snapshot; pig always carries a
//     context, so cancellation propagates while storage/parse failures fall
//     back to the cache (declared collapse of the two pi branches).
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// authStorageData is the auth.json shape: one credential per provider id,
// with the file's key order preserved (pi: Record<string, Credential> plus
// JSON.parse insertion order).
type authStorageData struct {
	keys []string
	byID map[string]auth.Credential
}

func newAuthStorageData() authStorageData {
	return authStorageData{byID: map[string]auth.Credential{}}
}

// set stores or replaces a credential (JS spread semantics: replacement
// keeps its position, insertion appends).
func (d *authStorageData) set(providerID string, credential auth.Credential) {
	if d.byID == nil {
		d.byID = map[string]auth.Credential{}
	}
	if _, ok := d.byID[providerID]; !ok {
		d.keys = append(d.keys, providerID)
	}
	d.byID[providerID] = credential
}

func (d *authStorageData) delete(providerID string) {
	delete(d.byID, providerID)
	for i, key := range d.keys {
		if key == providerID {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

// clone deep-copies the data (pi: structuredClone on credential values).
func (d *authStorageData) clone() authStorageData {
	out := newAuthStorageData()
	out.keys = append([]string(nil), d.keys...)
	for _, key := range d.keys {
		out.byID[key] = cloneCredential(d.byID[key])
	}
	return out
}

// marshalIndent renders the data the way JSON.stringify(data, null, 2) does
// in pi, preserving key order.
func (d *authStorageData) marshalIndent() ([]byte, error) {
	if len(d.keys) == 0 {
		return []byte("{}"), nil
	}
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, key := range d.keys {
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		valueJSON, err := json.MarshalIndent(d.byID[key], "  ", "  ")
		if err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(keyJSON)
		buf.WriteString(": ")
		buf.Write(valueJSON)
		if i < len(d.keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}")
	return buf.Bytes(), nil
}

// cloneCredential deep-copies one credential (pi: structuredClone; every
// credential field is JSON-roundtrip-safe).
func cloneCredential(credential auth.Credential) auth.Credential {
	if credential == nil {
		return nil
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return nil
	}
	out, err := auth.UnmarshalCredential(data)
	if err != nil {
		return nil
	}
	return out
}

// parseAuthStorageData decodes auth.json content (pi: JSON.parse; the
// content is trusted to hold Credential shapes — validation lives on the
// read-only store only).
func parseAuthStorageData(content *string) (authStorageData, error) {
	if content == nil || *content == "" {
		return newAuthStorageData(), nil
	}
	stripped := utils.StripBom(*content)
	keys, err := jsonObjectKeyOrder([]byte(stripped))
	if err != nil {
		return authStorageData{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stripped), &raw); err != nil {
		return authStorageData{}, err
	}
	data := newAuthStorageData()
	for _, key := range keys {
		rawCredential, ok := raw[key]
		if !ok {
			continue
		}
		credential, err := auth.UnmarshalCredential(rawCredential)
		if err != nil {
			return authStorageData{}, err
		}
		data.set(key, credential)
	}
	return data, nil
}

// jsonObjectKeyOrder extracts top-level object key order (pig addition to
// keep auth.json line-compatible; encoding/json sorts map keys).
func jsonObjectKeyOrder(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("auth.json: expected an object")
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("auth.json: expected a string key")
		}
		keys = append(keys, key)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return keys, nil
}

// LockFunction is the withLock callback (pi: (current: string | undefined) =>
// LockResult<T>). It receives the current file content (nil when the file
// does not exist) and returns the operation result plus the next file
// content to persist (nil = leave the file unchanged).
type LockFunction func(current *string) (result any, next *string, err error)

// AuthStorageBackend is the storage abstraction behind AuthStorage (pi:
// AuthStorageBackend). pi's withLock/withLockAsync differ only in
// lock-acquisition retry policy; both are context-aware here.
type AuthStorageBackend interface {
	// WithLock runs fn under the storage lock with the synchronous retry
	// policy (pi: withLock; no cancellation).
	WithLock(ctx context.Context, fn LockFunction) error
	// WithLockAsync runs fn under the storage lock with the async retry
	// policy: stale-aware acquisition, exponential backoff with jitter, and
	// cancellation (pi: withLockAsync).
	WithLockAsync(ctx context.Context, fn LockFunction) error
}

// WithLock runs fn under the backend lock and returns its typed result (pi:
// LockResult unwrapping). Generic helper because Go interfaces cannot have
// generic methods.
func WithLock[T any](ctx context.Context, backend AuthStorageBackend, fn func(current *string) (T, *string, error)) (T, error) {
	var result T
	err := backend.WithLock(ctx, func(current *string) (any, *string, error) {
		r, next, err := fn(current)
		if err != nil {
			return nil, nil, err
		}
		return r, next, nil
	})
	return result, err
}

// WithLockAsync is WithLock with the async retry policy (pi: withLockAsync).
// The result is captured only when fn itself succeeds; pi discards it on any
// later throw the same way (callers must check err first).
func WithLockAsync[T any](ctx context.Context, backend AuthStorageBackend, fn func(current *string) (T, *string, error)) (T, error) {
	var result T
	err := backend.WithLockAsync(ctx, func(current *string) (any, *string, error) {
		r, next, err := fn(current)
		if err == nil {
			result = r
		}
		return r, next, err
	})
	return result, err
}

// The mode applies only on creation so administrator-managed modes and ACLs
// remain intact (pi: AUTH_FILE_WRITE_OPTIONS).
const authFileMode fs.FileMode = 0o600

const (
	syncLockMaxAttempts = 10
	syncLockDelay       = 20 * time.Millisecond
	asyncLockStale      = 30 * time.Second
	asyncLockMaxDelay   = 2 * time.Second
)

// lockFilePath is the lockfile location for a managed file (pig addition;
// proper-lockfile appends .lock the same way).
func lockFilePath(path string) string { return path + ".lock" }

// fsLockOptions parameterize the async file lock (pi: proper-lockfile
// options {realpath: false, retries: 0, stale, onCompromised}).
type fsLockOptions struct {
	stale         time.Duration
	onCompromised func(error)
}

// fsLockSyncAcquire acquires the lock file in a single attempt (pi:
// lockfile.lockSync(path, {realpath: false})). A held lock yields an error
// with code "ELOCKED". Package var so tests can substitute lock behavior
// (pi mocks via vi.spyOn(lockfile, "lockSync")).
var fsLockSyncAcquire = fsLockSyncAcquireDefault

// fsLockAcquire is the async single-attempt acquisition (pi:
// lockfile.lock(path, {realpath: false, retries: 0, stale, onCompromised})).
var fsLockAcquire = fsLockAcquireDefault

// acquireLockFileOnce creates the lock file exclusively; its content is the
// creation mtime (proper-lockfile stores the same and verifies it). When
// stale > 0 a lock older than the window is stolen once.
func acquireLockFileOnce(path string, stale time.Duration, onCompromised func(error)) (func() error, error) {
	lockPath := lockFilePath(path)
	for attempt := 0; attempt < 2; attempt++ {
		now := time.Now()
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
		if err == nil {
			content := strconv.FormatInt(now.UnixMilli(), 10)
			if _, werr := f.WriteString(content); werr != nil {
				f.Close()
				return nil, werr
			}
			if cerr := f.Close(); cerr != nil {
				return nil, cerr
			}
			// Compromise verification (pi: proper-lockfile mtime check after
			// acquisition): re-read and compare; a mismatch means another
			// owner holds the lock.
			data, rerr := os.ReadFile(lockPath)
			if rerr != nil || string(data) != content {
				err := fmt.Errorf("lock file %s was compromised", lockPath)
				if onCompromised != nil {
					onCompromised(err)
				}
				return nil, err
			}
			released := false
			return func() error {
				if released {
					return nil
				}
				released = true
				return os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		// Held. Steal a stale lock once (pi: proper-lockfile steals locks
		// older than the stale window during acquisition).
		if stale > 0 {
			if info, serr := os.Stat(lockPath); serr == nil && time.Since(info.ModTime()) > stale {
				_ = os.Remove(lockPath)
				continue
			}
		}
		return nil, &utils.NodeErr{
			Code:    "ELOCKED",
			Message: fmt.Sprintf("Lock file is already being held: %s", lockPath),
			Err:     err,
		}
	}
	return nil, &utils.NodeErr{
		Code:    "ELOCKED",
		Message: fmt.Sprintf("Lock file is already being held: %s", lockPath),
	}
}

func fsLockSyncAcquireDefault(path string) (func() error, error) {
	return acquireLockFileOnce(path, 0, nil)
}

func fsLockAcquireDefault(path string, options *fsLockOptions) (func() error, error) {
	if options == nil {
		options = &fsLockOptions{}
	}
	return acquireLockFileOnce(path, options.stale, options.onCompromised)
}

func defaultAuthJSONPath() string {
	return filepath.Join(codingagent.GetAgentDir(), "auth.json")
}

// FileAuthStorageBackend is the auth.json-backed storage backend (pi:
// FileAuthStorageBackend).
type FileAuthStorageBackend struct {
	authPath string
}

// NewFileAuthStorageBackend builds a backend for authPath; an empty path
// defaults to <agentDir>/auth.json (pi: constructor default).
func NewFileAuthStorageBackend(authPath string) *FileAuthStorageBackend {
	if authPath == "" {
		authPath = defaultAuthJSONPath()
	}
	return &FileAuthStorageBackend{authPath: utils.NormalizePath(authPath, nil)}
}

func (b *FileAuthStorageBackend) ensureParentDir() {
	dir := filepath.Dir(b.authPath)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		_ = os.MkdirAll(dir, 0o700)
	}
}

func (b *FileAuthStorageBackend) ensureFileExists() {
	if _, err := os.Stat(b.authPath); errors.Is(err, fs.ErrNotExist) {
		f, err := os.OpenFile(b.authPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, authFileMode)
		if err != nil {
			return
		}
		_, _ = f.WriteString("{}")
		_ = f.Close()
	}
}

func (b *FileAuthStorageBackend) readCurrent() *string {
	data, err := os.ReadFile(b.authPath)
	if err != nil {
		return nil
	}
	content := string(data)
	return &content
}

func (b *FileAuthStorageBackend) writeNext(next *string) error {
	// The mode applies only on creation (pi: writeFileSync mode option).
	f, err := os.OpenFile(b.authPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, authFileMode)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(*next); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// acquireLockSyncWithRetry is the synchronous ELOCKED retry loop (pi:
// acquireLockSyncWithRetry; pi busy-waits to stay synchronous, pig sleeps).
func (b *FileAuthStorageBackend) acquireLockSyncWithRetry() (func() error, error) {
	var lastErr error
	for attempt := 1; attempt <= syncLockMaxAttempts; attempt++ {
		release, err := fsLockSyncAcquire(b.authPath)
		if err == nil {
			return release, nil
		}
		if code := utils.NodeErrCodeOf(err); code != "ELOCKED" || attempt == syncLockMaxAttempts {
			return nil, err
		}
		lastErr = err
		time.Sleep(syncLockDelay)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("Failed to acquire auth storage lock")
}

func (b *FileAuthStorageBackend) WithLock(ctx context.Context, fn LockFunction) error {
	_ = ctx // pi's sync path carries no cancellation
	b.ensureParentDir()
	b.ensureFileExists()

	release, err := b.acquireLockSyncWithRetry()
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	current := b.readCurrent()
	_, next, err := fn(current)
	if err != nil {
		return err
	}
	if next != nil {
		return b.writeNext(next)
	}
	return nil
}

// acquireLockAsync is the async acquisition loop: stale-aware single-attempt
// locks with exponential backoff (capped) and jitter until the 30s deadline
// (pi: acquireLockAsync).
func (b *FileAuthStorageBackend) acquireLockAsync(ctx context.Context) (release func() error, compromised func() error, err error) {
	deadline := time.Now().Add(asyncLockStale)
	var compromisedErr error
	onCompromised := func(err error) {
		if compromisedErr == nil {
			compromisedErr = err
		}
	}
	for retry := 0; ; retry++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		release, err := fsLockAcquire(b.authPath, &fsLockOptions{stale: asyncLockStale, onCompromised: onCompromised})
		if err == nil {
			if compromisedErr != nil {
				_ = release()
				return nil, nil, compromisedErr
			}
			if err := ctx.Err(); err != nil {
				_ = release()
				return nil, nil, err
			}
			return release, func() error { return compromisedErr }, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if code := utils.NodeErrCodeOf(err); code != "ELOCKED" {
			return nil, nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, nil, err
		}
		baseDelay := time.Duration(math.Min(float64(10*time.Millisecond)*math.Pow(2, float64(retry)), float64(asyncLockMaxDelay/2)))
		delay := time.Duration(float64(baseDelay) * (1 + rand.Float64()))
		if remaining := time.Until(deadline); delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *FileAuthStorageBackend) WithLockAsync(ctx context.Context, fn LockFunction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.ensureParentDir()
	b.ensureFileExists()

	release, compromised, err := b.acquireLockAsync(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	throwIfCompromised := func() error {
		return compromised()
	}
	if err := throwIfCompromised(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := b.readCurrent()
	_, next, err := fn(current)
	if err != nil {
		return err
	}
	if err := throwIfCompromised(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if next != nil {
		if err := b.writeNext(next); err != nil {
			return err
		}
	}
	return throwIfCompromised()
}

// InMemoryAuthStorageBackend keeps the file content in memory (pi:
// InMemoryAuthStorageBackend). pi serializes operations on a promise chain
// and races each caller against its signal: a cancelled waiter never runs
// fn, and a cancelled active callback settles before the chain is released
// without committing. pig holds a mutex for the same serialization and runs
// fn on a goroutine so the caller observes cancellation immediately.
type InMemoryAuthStorageBackend struct {
	mu    sync.Mutex
	value *string
}

func (b *InMemoryAuthStorageBackend) WithLock(ctx context.Context, fn LockFunction) error {
	_ = ctx // pi's sync path carries no cancellation
	b.mu.Lock()
	defer b.mu.Unlock()
	_, next, err := fn(b.value)
	if err != nil {
		return err
	}
	if next != nil {
		b.value = next
	}
	return nil
}

type inMemoryLockOutcome struct {
	err error
}

func (b *InMemoryAuthStorageBackend) WithLockAsync(ctx context.Context, fn LockFunction) error {
	outcomeCh := make(chan inMemoryLockOutcome, 1)
	go func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if err := ctx.Err(); err != nil {
			outcomeCh <- inMemoryLockOutcome{err: err}
			return
		}
		_, next, err := fn(b.value)
		if err != nil {
			outcomeCh <- inMemoryLockOutcome{err: err}
			return
		}
		// pi: throwIfAborted after fn — a cancelled active callback settles
		// without committing.
		if err := ctx.Err(); err != nil {
			outcomeCh <- inMemoryLockOutcome{err: err}
			return
		}
		if next != nil {
			b.value = next
		}
		outcomeCh <- inMemoryLockOutcome{}
	}()
	select {
	case out := <-outcomeCh:
		return out.err
	case <-ctx.Done():
		// pi: raceWithAbortSignal — the caller rejects immediately while the
		// operation keeps the chain until it settles.
		return ctx.Err()
	}
}

// ReadOnlyAuthStorage is a read-only CredentialStore over auth.json (pi:
// ReadOnlyAuthStorage).
type ReadOnlyAuthStorage struct {
	authPath string
	mu       sync.Mutex
	data     authStorageData
	loaded   bool
}

// NewReadOnlyAuthStorage builds a read-only store; an empty path defaults to
// <agentDir>/auth.json (pi: constructor default).
func NewReadOnlyAuthStorage(authPath string) *ReadOnlyAuthStorage {
	if authPath == "" {
		authPath = defaultAuthJSONPath()
	}
	return &ReadOnlyAuthStorage{authPath: utils.NormalizePath(authPath, nil)}
}

// validateCredential mirrors pi's load() shape checks; the error texts are
// preserved verbatim.
func validateCredential(providerID string, raw json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("Invalid auth.json credential for provider %q", providerID)
	}
	var probe struct {
		Type    string   `json:"type"`
		Access  *string  `json:"access"`
		Refresh *string  `json:"refresh"`
		Expires *float64 `json:"expires"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Type == "api_key" {
		validKey := true
		if keyRaw, present := value["key"]; present {
			var key string
			if json.Unmarshal(keyRaw, &key) != nil {
				validKey = false
			}
		}
		validEnv := true
		if envRaw, present := value["env"]; present {
			// pi: env must be an object (null fails the !== null check) whose
			// values are all strings.
			if string(envRaw) == "null" {
				validEnv = false
			} else {
				var env map[string]string
				if json.Unmarshal(envRaw, &env) != nil {
					validEnv = false
				}
			}
		}
		if validKey && validEnv {
			return nil
		}
	} else if probe.Type == "oauth" &&
		probe.Access != nil && probe.Refresh != nil && probe.Expires != nil &&
		!math.IsInf(*probe.Expires, 0) && !math.IsNaN(*probe.Expires) {
		return nil
	}
	return fmt.Errorf("Invalid auth.json credential for provider %q", providerID)
}

func (s *ReadOnlyAuthStorage) load() (authStorageData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.data, nil
	}
	contentBytes, err := os.ReadFile(s.authPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.data = newAuthStorageData()
			s.loaded = true
			return s.data, nil
		}
		return authStorageData{}, fmt.Errorf("Failed to read auth.json: %s", err.Error())
	}
	content := utils.StripBom(string(contentBytes))
	keys, err := jsonObjectKeyOrder([]byte(content))
	if err != nil {
		return authStorageData{}, errors.New("Invalid auth.json: expected an object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil || raw == nil {
		return authStorageData{}, errors.New("Invalid auth.json: expected an object")
	}
	data := newAuthStorageData()
	for _, providerID := range keys {
		rawCredential, ok := raw[providerID]
		if !ok {
			continue
		}
		if err := validateCredential(providerID, rawCredential); err != nil {
			return authStorageData{}, err
		}
		credential, err := auth.UnmarshalCredential(rawCredential)
		if err != nil {
			return authStorageData{}, fmt.Errorf("Invalid auth.json credential for provider %q", providerID)
		}
		data.set(providerID, credential)
	}
	s.data = data
	s.loaded = true
	return s.data, nil
}

func (s *ReadOnlyAuthStorage) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	credential := data.byID[providerID]
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, nil
	}
	apiKey, isAPIKey := credential.(*auth.ApiKeyCredential)
	if !isAPIKey || apiKey.Key == "" || IsCommandConfigValue(apiKey.Key) {
		return cloneCredential(credential), nil
	}
	resolved, ok := ResolveConfigValue(apiKey.Key, map[string]string(apiKey.Env))
	out := cloneCredential(credential).(*auth.ApiKeyCredential)
	if ok {
		out.Key = resolved
	} else {
		out.Key = ""
	}
	return out, nil
}

func (s *ReadOnlyAuthStorage) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	credentials := make([]auth.CredentialInfo, 0, len(data.keys))
	for _, providerID := range data.keys {
		credentials = append(credentials, auth.CredentialInfo{
			ProviderID: providerID,
			Type:       data.byID[providerID].CredentialType(),
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return credentials, nil
}

func (s *ReadOnlyAuthStorage) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	_ = ctx
	_ = providerID
	_ = fn
	return nil, errors.New("Read-only credential storage cannot modify auth.json")
}

func (s *ReadOnlyAuthStorage) Delete(ctx context.Context, providerID string) error {
	_ = ctx
	_ = providerID
	return errors.New("Read-only credential storage cannot modify auth.json")
}

// sharedAuthFileReadState lets AuthStorage instances on the same path share
// a read cache (pi: module-level sharedAuthFileReadState; the first path
// wins for the process lifetime).
var sharedAuthFileReadState struct {
	mu       sync.Mutex
	authPath string
	state    *authFileReadState
}

type authFileReadState struct {
	mu       sync.Mutex
	data     authStorageData
	revision string
	reload   *authFileReload
}

// authFileReload coalesces concurrent file reloads (pi: AuthFileReload).
type authFileReload struct {
	ctx     context.Context
	cancel  context.CancelFunc
	data    authStorageData
	err     error
	done    chan struct{}
	readers int
}

// AuthStorage is the credential storage backed by a JSON file (pi:
// AuthStorage).
type AuthStorage struct {
	storage   AuthStorageBackend
	authPath  string // "" = non-file storage (fromStorage / inMemory)
	readState *authFileReadState
}

// NewAuthStorage builds an AuthStorage over a backend; authPath enables the
// shared file read cache and revision checks (pi: private constructor).
func NewAuthStorage(storage AuthStorageBackend, authPath string) *AuthStorage {
	s := &AuthStorage{storage: storage, authPath: authPath}
	s.readState = &authFileReadState{data: newAuthStorageData()}
	if authPath != "" {
		sharedAuthFileReadState.mu.Lock()
		if sharedAuthFileReadState.authPath == authPath && sharedAuthFileReadState.state != nil {
			s.readState = sharedAuthFileReadState.state
		}
		if sharedAuthFileReadState.state == nil {
			sharedAuthFileReadState.authPath = authPath
			sharedAuthFileReadState.state = s.readState
		}
		sharedAuthFileReadState.mu.Unlock()

		if revision := utils.GetFileRevision(authPath); revision != "" && revision == s.readState.revision {
			return s
		}
	}
	s.Reload()
	return s
}

// CreateAuthStorage opens the file-backed credential store (pi:
// AuthStorage.create); an empty path defaults to <agentDir>/auth.json.
func CreateAuthStorage(authPath string) *AuthStorage {
	if authPath == "" {
		authPath = defaultAuthJSONPath()
	}
	normalized := utils.NormalizePath(authPath, nil)
	return NewAuthStorage(NewFileAuthStorageBackend(normalized), normalized)
}

// AuthStorageFromStorage builds an AuthStorage over an explicit backend (pi:
// AuthStorage.fromStorage).
func AuthStorageFromStorage(storage AuthStorageBackend) *AuthStorage {
	return NewAuthStorage(storage, "")
}

// NewInMemoryAuthStorage builds an in-memory credential store seeded with
// data; a nil data seeds an empty store (pi: AuthStorage.inMemory).
func NewInMemoryAuthStorage(data authStorageData) *AuthStorage {
	storage := &InMemoryAuthStorageBackend{}
	seed := data
	if seed.byID == nil {
		seed = newAuthStorageData()
	}
	serialized, err := seed.marshalIndent()
	if err == nil {
		content := string(serialized)
		_ = storage.WithLock(context.Background(), func(current *string) (any, *string, error) {
			return nil, &content, nil
		})
	}
	return AuthStorageFromStorage(storage)
}

func (s *AuthStorage) parseStorageData(content *string) (authStorageData, error) {
	if content == nil || *content == "" {
		return newAuthStorageData(), nil
	}
	return parseAuthStorageData(content)
}

func (s *AuthStorage) updateReadState(data authStorageData, revision string) {
	s.readState.mu.Lock()
	defer s.readState.mu.Unlock()
	s.readState.data = data
	s.readState.revision = revision
}

// Reload rereads credentials from storage, keeping the last valid snapshot
// on failure (pi: reload).
func (s *AuthStorage) Reload() {
	var content *string
	var revision string
	err := s.storage.WithLock(context.Background(), func(current *string) (any, *string, error) {
		content = current
		if s.authPath != "" {
			revision = utils.GetFileRevision(s.authPath)
		}
		return nil, nil, nil
	})
	if err != nil {
		// Preserve the last valid in-memory snapshot.
		return
	}
	data, err := s.parseStorageData(content)
	if err != nil {
		// Preserve the last valid in-memory snapshot.
		return
	}
	s.updateReadState(data, revision)
}

func (s *AuthStorage) reloadFromStorageAsync(ctx context.Context) (authStorageData, error) {
	return WithLockAsync(ctx, s.storage, func(content *string) (authStorageData, *string, error) {
		currentData, err := s.parseStorageData(content)
		if err != nil {
			return authStorageData{}, nil, err
		}
		revision := ""
		if s.authPath != "" {
			revision = utils.GetFileRevision(s.authPath)
		}
		s.updateReadState(currentData, revision)
		return currentData, nil, nil
	})
}

// readLatestData returns the freshest known data, coalescing concurrent
// reloads (pi: readLatestData).
func (s *AuthStorage) readLatestData(ctx context.Context) (authStorageData, error) {
	if err := ctx.Err(); err != nil {
		return authStorageData{}, err
	}
	if s.authPath == "" {
		data, err := s.reloadFromStorageAsync(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return authStorageData{}, ctx.Err()
			}
			s.readState.mu.Lock()
			cached := s.readState.data
			s.readState.mu.Unlock()
			return cached, nil
		}
		return data, nil
	}
	revision := utils.GetFileRevision(s.authPath)
	s.readState.mu.Lock()
	if revision != "" && revision == s.readState.revision {
		data := s.readState.data
		s.readState.mu.Unlock()
		return data, nil
	}
	if s.readState.reload == nil {
		reloadCtx, cancel := context.WithCancel(context.Background())
		reload := &authFileReload{ctx: reloadCtx, cancel: cancel, done: make(chan struct{})}
		s.readState.reload = reload
		go func() {
			defer close(reload.done)
			reload.data, reload.err = s.reloadFromStorageAsync(reloadCtx)
			s.readState.mu.Lock()
			if s.readState.reload == reload {
				s.readState.reload = nil
			}
			s.readState.mu.Unlock()
		}()
	}
	reload := s.readState.reload
	reload.readers++
	s.readState.mu.Unlock()

	defer func() {
		s.readState.mu.Lock()
		reload.readers--
		if reload.readers == 0 && s.readState.reload == reload {
			s.readState.reload = nil
			reload.cancel()
		}
		s.readState.mu.Unlock()
	}()

	select {
	case <-reload.done:
		if reload.err != nil {
			if ctx.Err() != nil {
				return authStorageData{}, ctx.Err()
			}
			s.readState.mu.Lock()
			cached := s.readState.data
			s.readState.mu.Unlock()
			return cached, nil
		}
		return reload.data, nil
	case <-ctx.Done():
		return authStorageData{}, ctx.Err()
	}
}

func (s *AuthStorage) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	data, err := s.readLatestData(ctx)
	if err != nil {
		return nil, err
	}
	credential := data.byID[providerID]
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, nil
	}
	apiKey, isAPIKey := credential.(*auth.ApiKeyCredential)
	if !isAPIKey || apiKey.Key == "" {
		return cloneCredential(credential), nil
	}
	resolved, ok := ResolveConfigValue(apiKey.Key, map[string]string(apiKey.Env))
	out := cloneCredential(credential).(*auth.ApiKeyCredential)
	if ok {
		out.Key = resolved
	} else {
		out.Key = ""
	}
	return out, nil
}

func (s *AuthStorage) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	var latestData authStorageData
	var revision string
	result, err := WithLockAsync(ctx, s.storage, func(content *string) (auth.Credential, *string, error) {
		currentData, err := s.parseStorageData(content)
		if err != nil {
			return nil, nil, err
		}
		next, err := fn(currentData.byID[providerID])
		if err != nil {
			return nil, nil, err
		}
		if next == nil {
			// pi: leaving the entry unchanged keeps the stored credential and
			// refreshes the read-state snapshot from disk.
			latestData = currentData
			if s.authPath != "" {
				revision = utils.GetFileRevision(s.authPath)
			}
			return currentData.byID[providerID], nil, nil
		}
		merged := currentData.clone()
		merged.set(providerID, next)
		latestData = merged
		serialized, err := merged.marshalIndent()
		if err != nil {
			return nil, nil, err
		}
		nextContent := string(serialized)
		return next, &nextContent, nil
	})
	if err != nil {
		return nil, err
	}
	s.updateReadState(latestData, revision)
	return result, nil
}

func (s *AuthStorage) Delete(ctx context.Context, providerID string) error {
	var latestData authStorageData
	_, err := WithLockAsync(ctx, s.storage, func(content *string) (any, *string, error) {
		currentData, err := s.parseStorageData(content)
		if err != nil {
			return nil, nil, err
		}
		currentData.delete(providerID)
		latestData = currentData
		serialized, err := currentData.marshalIndent()
		if err != nil {
			return nil, nil, err
		}
		nextContent := string(serialized)
		return nil, &nextContent, nil
	})
	if err != nil {
		return err
	}
	// pi: updateReadState(latestData) — the revision is cleared so the next
	// read revalidates.
	s.updateReadState(latestData, "")
	return nil
}

// List returns credential metadata without resolving configured key values
// (pi: list).
func (s *AuthStorage) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	data, err := s.readLatestData(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries := make([]auth.CredentialInfo, 0, len(data.keys))
	for _, providerID := range data.keys {
		entries = append(entries, auth.CredentialInfo{
			ProviderID: providerID,
			Type:       data.byID[providerID].CredentialType(),
		})
	}
	return entries, nil
}

// ReadStoredCredential performs a one-off synchronous read of a stored
// credential from an auth.json file, without instantiating a store or
// resolving configured key values (pi: readStoredCredential); an empty path
// defaults to <agentDir>/auth.json. Any failure yields nil.
func ReadStoredCredential(providerID string, authPath string) auth.Credential {
	if authPath == "" {
		authPath = defaultAuthJSONPath()
	}
	data, err := os.ReadFile(utils.NormalizePath(authPath, nil))
	if err != nil {
		return nil
	}
	content := string(data)
	parsed, err := parseAuthStorageData(&content)
	if err != nil {
		return nil
	}
	return parsed.byID[providerID]
}

// NewInMemoryAuthStorageWithCredentials builds an in-memory AuthStorage
// seeded with one api key per provider id (pi: AuthStorage.inMemory({
// provider: { type: "api_key", key } })). Exported for out-of-package test
// scaffolding; authStorageData stays unexported.
func NewInMemoryAuthStorageWithCredentials(credentials map[string]string) *AuthStorage {
	data := newAuthStorageData()
	ids := make([]string, 0, len(credentials))
	for id := range credentials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		data.keys = append(data.keys, id)
		data.byID[id] = &auth.ApiKeyCredential{Key: credentials[id]}
	}
	return NewInMemoryAuthStorage(data)
}
