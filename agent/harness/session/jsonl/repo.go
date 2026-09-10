// pi: src/harness/session/jsonl/repo.ts — the jsonl SessionRepo: create /
// open / list / delete / fork over cwd-encoded session directories.
//
// Deviations:
//   - pi's JsonlSessionRepo implements SessionRepo<JsonlSessionMetadata,
//     JsonlSessionCreateOptions, JsonlSessionListOptions>; the Go base
//     session.SessionRepo interface dropped those generics, so this repo
//     exposes the widened signatures directly (the conformance test re-narrows
//     with a cwd-injecting adapter, mirroring pi's withDefaultSessionCwd).
//   - activeCreateDestinations (Set) gains a mutex — pi relies on the event
//     loop for same-process create/fork race protection.
//   - rootPromise memoization → mutex-guarded field (same reason).
//   - sessionFileName reproduces toISOString().replace(/[:.]/g, "-")
//     byte-for-byte (UTC, millisecond precision).
//   - Date.now() → time.Now().UnixMilli(); uuidv7() → ai.UUIDv7().
package jsonl

import (
	"context"
	"fmt"
	"github.com/samber/lo"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
)

// repoFSContext mirrors storageFSContext: pi's repo never aborts fs calls.
var repoFSContext = context.Background()

// sessionIDPattern constrains session ids (pi: SESSION_ID_PATTERN).
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// validateSessionID rejects ids outside the pattern (pi: validateSessionId).
func validateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return session.NewSessionError(
			session.CodeInvalidPayload,
			"Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character",
		)
	}
	return nil
}

// jsonlSessionDirectoryName encodes a cwd into one directory name (pi:
// jsonlSessionDirectoryName).
func jsonlSessionDirectoryName(cwd string) string {
	stripped := cwd
	if len(stripped) > 0 && (stripped[0] == '/' || stripped[0] == '\\') {
		stripped = stripped[1:]
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	return "--" + replacer.Replace(stripped) + "--"
}

// jsonlSessionsRoot resolves the sessions root (pi: jsonlSessionsRoot).
func jsonlSessionsRoot(options JsonlSessionRepoOptions) (string, error) {
	path, err := options.Fs.AbsolutePath(repoFSContext, options.SessionsRoot)
	if err != nil {
		return "", fileResult(err, "Failed to resolve sessions root "+options.SessionsRoot)
	}
	return path, nil
}

// jsonlSessionDirectory resolves one cwd's session directory (pi:
// jsonlSessionDirectory).
func jsonlSessionDirectory(fs JsonlSessionRepoFileSystem, sessionsRoot string, cwd string) (string, error) {
	path, err := fs.JoinPath(repoFSContext, []string{sessionsRoot, jsonlSessionDirectoryName(cwd)})
	if err != nil {
		return "", fileResult(err, "Failed to resolve sessions directory for "+cwd)
	}
	return path, nil
}

// jsonlSessionDirectories enumerates the session directories to scan (pi:
// jsonlSessionDirectories).
func jsonlSessionDirectories(options JsonlSessionRepoOptions, cwd *string) ([]string, error) {
	sessionsRoot, err := jsonlSessionsRoot(options)
	if err != nil {
		return nil, err
	}
	if cwd != nil {
		resolvedCwd, err := options.Fs.AbsolutePath(repoFSContext, *cwd)
		if err != nil {
			return nil, fileResult(err, "Failed to resolve session cwd "+*cwd)
		}
		directory, err := jsonlSessionDirectory(options.Fs, sessionsRoot, resolvedCwd)
		if err != nil {
			return nil, err
		}
		exists, err := options.Fs.Exists(repoFSContext, directory)
		if err != nil {
			return nil, fileResult(err, "Failed to check sessions directory "+directory)
		}
		if !exists {
			return []string{}, nil
		}
		return []string{directory}, nil
	}
	rootExists, err := options.Fs.Exists(repoFSContext, sessionsRoot)
	if err != nil {
		return nil, fileResult(err, "Failed to check sessions directory "+sessionsRoot)
	}
	if !rootExists {
		return nil, nil
	}
	entries, err := options.Fs.ListDir(repoFSContext, sessionsRoot)
	if err != nil {
		return nil, fileResult(err, "Failed to list sessions directory "+sessionsRoot)
	}
	directories := []string{}
	for _, entry := range entries {
		if entry.Kind == "directory" || entry.Kind == "symlink" {
			directories = append(directories, entry.Path)
		}
	}
	return directories, nil
}

// ListJsonlSessionMetadata lists sessions under the root, newest modified
// first (pi: listJsonlSessionMetadata).
func ListJsonlSessionMetadata(options JsonlSessionRepoOptions, query *JsonlSessionListOptions) ([]JsonlSessionMetadata, error) {
	metadata := []JsonlSessionMetadata{}
	var cwd *string
	if query != nil {
		cwd = query.Cwd
	}
	directories, err := jsonlSessionDirectories(options, cwd)
	if err != nil {
		return nil, err
	}
	for _, directory := range directories {
		entries, err := options.Fs.ListDir(repoFSContext, directory)
		if err != nil {
			return nil, fileResult(err, "Failed to list sessions directory "+directory)
		}
		for _, file := range entries {
			if file.Kind == "directory" || !strings.HasSuffix(file.Name, ".jsonl") {
				continue
			}
			lines, err := options.Fs.ReadTextLines(repoFSContext, file.Path, lo.ToPtr(1))
			if err != nil {
				return nil, fileResult(err, "Failed to read session header "+file.Path)
			}
			if len(lines) == 0 || lines[0] == "" {
				continue
			}
			header, err := ParseHeader(lines[0])
			if err != nil {
				continue
			}
			metadata = append(metadata, MetadataFromHeader(header, file.Path, file.MtimeMs))
		}
	}
	sort.SliceStable(metadata, func(left, right int) bool {
		return metadata[left].ModifiedAt > metadata[right].ModifiedAt
	})
	return metadata, nil
}

// LoadJsonlSessionStorageByMetadata loads one session's storage by metadata
// (pi: loadJsonlSessionStorage; renamed — the static LoadJsonlSessionStorage
// took the pi JsonlSessionStorage.load name).
func LoadJsonlSessionStorageByMetadata(options JsonlSessionRepoOptions, metadata JsonlSessionMetadata) (*JsonlSessionStorage, error) {
	exists, err := options.Fs.Exists(repoFSContext, metadata.Path)
	if err != nil {
		return nil, fileResult(err, "Failed to check session "+metadata.Path)
	}
	if !exists {
		return nil, session.NewSessionError(session.CodeNotFound, "Session not found: "+metadata.ID)
	}
	storage, err := LoadJsonlSessionStorage(options.Fs, metadata.Path)
	if err != nil {
		return nil, err
	}
	loadedMetadata, err := storage.GetJsonlMetadata()
	if err != nil {
		return nil, err
	}
	if loadedMetadata.ID != metadata.ID {
		return nil, session.NewSessionError(session.CodeInvalidEntry, "Session id does not match header: "+metadata.ID)
	}
	return storage, nil
}

// sessionFileName builds the durable file name (pi: sessionFileName).
func sessionFileName(createdAt int64, id string) string {
	timestamp := time.UnixMilli(createdAt).UTC().Format("2006-01-02T15-04-05.000") + "Z"
	timestamp = strings.NewReplacer(":", "-", ".", "-").Replace(timestamp)
	return fmt.Sprintf("%s_%s.jsonl", timestamp, id)
}

// JsonlSessionRepo manages jsonl sessions under one root (pi:
// JsonlSessionRepo class).
type JsonlSessionRepo struct {
	fs                     JsonlSessionRepoFileSystem
	sessionsRootInput      string
	activeCreateDestMu     sync.Mutex
	activeCreateDestations map[string]struct{}
	rootMu                 sync.Mutex
	rootResolved           *string
}

// NewJsonlSessionRepo builds a repo (pi: constructor).
func NewJsonlSessionRepo(options JsonlSessionRepoOptions) *JsonlSessionRepo {
	return &JsonlSessionRepo{
		fs:                     options.Fs,
		sessionsRootInput:      options.SessionsRoot,
		activeCreateDestations: map[string]struct{}{},
	}
}

// Create writes a new session file (pi: create).
func (r *JsonlSessionRepo) Create(options *JsonlSessionCreateOptions) (*session.Session, error) {
	destination, err := r.resolveCreateDestination(options)
	if err != nil {
		return nil, err
	}
	var created *session.Session
	if err := r.claimCreateDestination(destination, func() error {
		header, path, err := r.prepareCreate(destination, options)
		if err != nil {
			return err
		}
		storage, err := CreateJsonlSessionStorage(r.fs, path, header)
		if err != nil {
			return err
		}
		created = session.NewSession(storage, nil)
		return nil
	}); err != nil {
		return nil, err
	}
	return created, nil
}

// Open opens an existing session for writing (pi: open).
func (r *JsonlSessionRepo) Open(metadata *JsonlSessionMetadata) (*session.Session, error) {
	storage, err := r.loadStorage(metadata)
	if err != nil {
		return nil, err
	}
	return session.NewSession(storage, nil), nil
}

// List lists session metadata (pi: list).
func (r *JsonlSessionRepo) List(options *JsonlSessionListOptions) ([]JsonlSessionMetadata, error) {
	return r.listDirect(options)
}

// Delete removes one session file (pi: delete).
func (r *JsonlSessionRepo) Delete(metadata *JsonlSessionMetadata) error {
	return fileResult(
		r.fs.Remove(repoFSContext, metadata.Path, nil, lo.ToPtr(true)),
		"Failed to delete session "+metadata.Path,
	)
}

// JsonlForkCreateOptions is pi's `ForkOptions & JsonlSessionCreateOptions`
// intersection on fork.
type JsonlForkCreateOptions struct {
	session.ForkOptions
	JsonlSessionCreateOptions
}

// Fork copies a session into a new file (pi: fork).
func (r *JsonlSessionRepo) Fork(source *JsonlSessionMetadata, options *JsonlForkCreateOptions) (*session.Session, error) {
	sourceStorage, err := r.loadStorage(source)
	if err != nil {
		return nil, err
	}
	createOptions := *options
	if createOptions.ParentSessionID == nil {
		parent := source.ID
		createOptions.ParentSessionID = &parent
	}
	destination, err := r.resolveCreateDestination(&createOptions.JsonlSessionCreateOptions)
	if err != nil {
		return nil, err
	}
	var forked *session.Session
	if err := r.claimCreateDestination(destination, func() error {
		header, path, err := r.prepareCreate(destination, &createOptions.JsonlSessionCreateOptions)
		if err != nil {
			return err
		}
		storage, err := sourceStorage.Fork(path, header, &createOptions.ForkOptions)
		if err != nil {
			return err
		}
		forked = session.NewSession(storage, nil)
		return nil
	}); err != nil {
		return nil, err
	}
	return forked, nil
}

// loadStorage loads by metadata (pi: loadStorage).
func (r *JsonlSessionRepo) loadStorage(metadata *JsonlSessionMetadata) (*JsonlSessionStorage, error) {
	return LoadJsonlSessionStorageByMetadata(JsonlSessionRepoOptions{Fs: r.fs, SessionsRoot: r.sessionsRootInput}, *metadata)
}

// createDestination carries a resolved create target (pi: { id, cwd }).
type createDestination struct {
	id  string
	cwd string
}

// resolveCreateDestination resolves id and cwd for a create/fork (pi:
// resolveCreateDestination).
func (r *JsonlSessionRepo) resolveCreateDestination(options *JsonlSessionCreateOptions) (createDestination, error) {
	id := ""
	if options.ID != nil {
		id = *options.ID
	} else {
		id = ai.UUIDv7()
	}
	if err := validateSessionID(id); err != nil {
		return createDestination{}, err
	}
	cwd, err := r.fs.AbsolutePath(repoFSContext, options.Cwd)
	if err != nil {
		return createDestination{}, fileResult(err, "Failed to resolve session cwd "+options.Cwd)
	}
	return createDestination{id: id, cwd: cwd}, nil
}

// claimCreateDestination prevents same-process create/fork races for one
// logical destination. The durable filename includes a timestamp, so the
// filesystem existence check alone can let two concurrent calls both decide
// the same {cwd, id} is free and publish duplicate sessions. (pi:
// claimCreateDestination.)
func (r *JsonlSessionRepo) claimCreateDestination(destination createDestination, operation func() error) error {
	key := destination.cwd + "\x00" + destination.id
	r.activeCreateDestMu.Lock()
	if _, active := r.activeCreateDestations[key]; active {
		r.activeCreateDestMu.Unlock()
		return session.NewSessionError(session.CodeAlreadyExists, "Session already exists: "+destination.id)
	}
	r.activeCreateDestations[key] = struct{}{}
	r.activeCreateDestMu.Unlock()
	defer func() {
		r.activeCreateDestMu.Lock()
		delete(r.activeCreateDestations, key)
		r.activeCreateDestMu.Unlock()
	}()
	return operation()
}

// prepareCreate computes the header and destination path (pi:
// prepareCreate).
func (r *JsonlSessionRepo) prepareCreate(destination createDestination, options *JsonlSessionCreateOptions) (JsonlV4Header, string, error) {
	id, cwd := destination.id, destination.cwd
	exists, err := r.sessionIDExists(id, cwd)
	if err != nil {
		return JsonlV4Header{}, "", err
	}
	if exists {
		return JsonlV4Header{}, "", session.NewSessionError(session.CodeAlreadyExists, "Session already exists: "+id)
	}
	createdAt := time.Now().UnixMilli()
	sessionDirectory, err := r.sessionDirectory(cwd)
	if err != nil {
		return JsonlV4Header{}, "", err
	}
	path, err := r.fs.JoinPath(repoFSContext, []string{sessionDirectory, sessionFileName(createdAt, id)})
	if err != nil {
		return JsonlV4Header{}, "", fileResult(err, "Failed to resolve path for session "+id)
	}
	if options.Metadata != nil {
		if err := session.AssertJsonSerializable(options.Metadata); err != nil {
			return JsonlV4Header{}, "", err
		}
	}
	header := JsonlV4Header{
		Kind:            "header",
		Version:         4,
		ID:              id,
		CreatedAt:       createdAt,
		Cwd:             cwd,
		ParentSessionID: options.ParentSessionID,
		Metadata:        options.Metadata,
	}
	recursive := true
	if err := fileResult(r.fs.CreateDir(repoFSContext, sessionDirectory, &recursive), "Failed to create sessions directory"); err != nil {
		return JsonlV4Header{}, "", err
	}
	return header, path, nil
}

// listDirect lists through the repo's own options (pi: listDirect).
func (r *JsonlSessionRepo) listDirect(options *JsonlSessionListOptions) ([]JsonlSessionMetadata, error) {
	return ListJsonlSessionMetadata(JsonlSessionRepoOptions{Fs: r.fs, SessionsRoot: r.sessionsRootInput}, options)
}

// sessionIDExists reports whether any file for the id exists in the cwd
// directory (pi: sessionIdExists).
func (r *JsonlSessionRepo) sessionIDExists(id string, cwd string) (bool, error) {
	suffix := "_" + id + ".jsonl"
	directory, err := r.sessionDirectory(cwd)
	if err != nil {
		return false, err
	}
	exists, err := r.fs.Exists(repoFSContext, directory)
	if err != nil {
		return false, fileResult(err, "Failed to check sessions directory "+directory)
	}
	if !exists {
		return false, nil
	}
	entries, err := r.fs.ListDir(repoFSContext, directory)
	if err != nil {
		return false, fileResult(err, "Failed to list sessions directory "+directory)
	}
	for _, entry := range entries {
		if entry.Kind != "directory" && strings.HasSuffix(entry.Name, suffix) {
			return true, nil
		}
	}
	return false, nil
}

// sessionDirectory resolves one cwd's directory against the memoized root
// (pi: sessionDirectory).
func (r *JsonlSessionRepo) sessionDirectory(cwd string) (string, error) {
	root, err := r.root()
	if err != nil {
		return "", err
	}
	path, err := r.fs.JoinPath(repoFSContext, []string{root, jsonlSessionDirectoryName(cwd)})
	if err != nil {
		return "", fileResult(err, "Failed to resolve sessions directory for "+cwd)
	}
	return path, nil
}

// root memoizes the resolved sessions root (pi: root).
func (r *JsonlSessionRepo) root() (string, error) {
	r.rootMu.Lock()
	defer r.rootMu.Unlock()
	if r.rootResolved != nil {
		return *r.rootResolved, nil
	}
	resolved, err := r.fs.AbsolutePath(repoFSContext, r.sessionsRootInput)
	if err != nil {
		return "", fileResult(err, "Failed to resolve sessions root "+r.sessionsRootInput)
	}
	r.rootResolved = &resolved
	return resolved, nil
}
