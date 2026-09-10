// pi: harness/session/jsonl/repo.ts — file-backed format-4 session
// repository lifecycle (create/open/list/delete/fork).
package jsonl

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// harnessFileInfo aliases harness.FileInfo.
type harnessFileInfo = harness.FileInfo

const harnessFileKindDirectory = harness.FileKindDirectory

// sessionIDPattern validates session ids (pi: SESSION_ID_PATTERN).
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// validateSessionID enforces the session id shape (pi: validateSessionId).
func validateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
	}
	return nil
}

// sessionDirectoryName encodes one cwd into a directory name (pi:
// jsonlSessionDirectoryName): strip the leading slash, then turn /, \ and :
// into -.
func sessionDirectoryName(cwd string) string {
	trimmed := strings.TrimLeft(cwd, "/\\")
	trimmed = regexp.MustCompile(`[/\\:]`).ReplaceAllString(trimmed, "-")
	return "--" + trimmed + "--"
}

// sessionFileName builds one session file name (pi: sessionFileName):
// ISO timestamp with [:.] replaced by '-', then the escaped id.
func sessionFileName(createdAt int64, id string) string {
	timestamp := time.UnixMilli(createdAt).UTC().Format("2006-01-02T15-04-05-000Z")
	return fmt.Sprintf("%s_%s.jsonl", timestamp, url.QueryEscape(id))
}

// metadataFromHeader builds list metadata from a v4 header (pi:
// metadataFromHeader).
func metadataFromHeader(header JsonlStorageHeader, path string, modifiedAt int64) JsonlSessionMetadata {
	metadata := JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{
			ID:             header.ID,
			CreatedAt:      header.CreatedAt,
			StorageVersion: header.StorageVersion,
		},
		Cwd:        header.Cwd,
		Path:       path,
		ModifiedAt: modifiedAt,
	}
	cwd := header.Cwd
	metadata.SessionMetadata.Cwd = &cwd
	if header.ParentSessionID != nil {
		metadata.SessionMetadata.ParentSessionID = header.ParentSessionID
	}
	if header.LegacyParentSessionPath != nil {
		metadata.SessionMetadata.LegacyParentSessionPath = header.LegacyParentSessionPath
	}
	return metadata
}

// JsonlSessionRepo is the file-backed repo (pi: JsonlSessionRepo).
type JsonlSessionRepo struct {
	fileSystem        harnessFileSystem
	sessionsRootInput string
	now               func() int64

	mu             sync.Mutex
	openSessions   map[string]*JsonlStorage
	pendingCreates map[string]struct{}
	closed         bool
}

// NewJsonlSessionRepo builds one repo (pi: constructor).
func NewJsonlSessionRepo(options JsonlSessionRepoOptions) *JsonlSessionRepo {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	return &JsonlSessionRepo{
		fileSystem:        options.FileSystem,
		sessionsRootInput: options.SessionsRoot,
		now:               now,
		openSessions:      map[string]*JsonlStorage{},
		pendingCreates:    map[string]struct{}{},
	}
}

func (r *JsonlSessionRepo) assertOpen() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("JsonlSessionRepo is closed")
	}
	return nil
}

func (r *JsonlSessionRepo) root(ctx context.Context) (string, error) {
	path, err := r.fileSystem.AbsolutePath(ctx, r.sessionsRootInput)
	if err != nil {
		return "", fmt.Errorf("Failed to resolve sessions root %s: %w", r.sessionsRootInput, err)
	}
	return path, nil
}

func (r *JsonlSessionRepo) sessionDirectory(cwd string, ctx context.Context) (string, error) {
	root, err := r.root(ctx)
	if err != nil {
		return "", err
	}
	path, err := r.fileSystem.JoinPath(ctx, []string{root, sessionDirectoryName(cwd)})
	if err != nil {
		return "", fmt.Errorf("Failed to resolve sessions directory for %s: %w", cwd, err)
	}
	return path, nil
}

// sessionDirectories lists all cwd directories under the root (pi:
// sessionDirectories).
func (r *JsonlSessionRepo) sessionDirectories(root string, ctx context.Context) ([]string, error) {
	entries, err := r.fileSystem.ListDir(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("Failed to list sessions root %s: %w", root, err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.Kind == harnessFileKindDirectory {
			directories = append(directories, entry.Path)
		}
	}
	return directories, nil
}

// resolveCreateDestination resolves the cwd and id for a new session (pi:
// resolveCreateDestination).
func (r *JsonlSessionRepo) resolveCreateDestination(cwdInput string, id *string, createdAt int64, ctx context.Context) (string, string, error) {
	destinationID := ""
	if id != nil {
		destinationID = *id
	} else {
		destinationID = aiutils.UUIDv7WithTime(createdAt)
	}
	cwd, err := r.fileSystem.AbsolutePath(ctx, cwdInput)
	if err != nil {
		return "", "", fmt.Errorf("Failed to resolve session cwd %s: %w", cwdInput, err)
	}
	return cwd, destinationID, nil
}

func (r *JsonlSessionRepo) Create(options JsonlSessionCreateOptions, ctx context.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	opts := options
	if err := validateSessionIDOrGenerated(opts.ID); err != nil {
		return nil, err
	}
	createdAt := r.now()
	cwd, id, err := r.resolveCreateDestination(opts.Cwd, opts.ID, createdAt, ctx)
	if err != nil {
		return nil, err
	}
	key := r.sessionKey(cwd, id)
	r.mu.Lock()
	_, open := r.openSessions[key]
	_, pending := r.pendingCreates[key]
	if open || pending {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session already exists: %s", id)
	}
	r.pendingCreates[key] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pendingCreates, key)
		r.mu.Unlock()
	}()

	path, err := r.resolveNewSessionPath(cwd, createdAt, id, ctx)
	if err != nil {
		return nil, err
	}
	var parentSessionID *string
	if opts.ParentSessionID != nil {
		parentSessionID = opts.ParentSessionID
	}
	header := JsonlStorageHeader{
		V:               JSONLFormatVersion,
		Kind:            "header",
		ID:              id,
		StorageVersion:  JSONLStorageVersion,
		CreatedAt:       createdAt,
		Cwd:             cwd,
		ParentSessionID: parentSessionID,
	}
	storage, err := CreateJsonlStorage(&JsonlStorageOptions{FileSystem: r.fileSystem, Path: path, Now: r.now}, header, nil, ctx)
	if err != nil {
		return nil, err
	}
	info, err := r.fileSystem.FileInfo(ctx, path)
	if err != nil {
		_ = storage.Close(ctx)
		_ = r.fileSystem.Remove(ctx, path, nil, lo.ToPtr(true))
		return nil, fmt.Errorf("Failed to read session %s: %w", path, err)
	}
	return r.publishOpenSession(metadataFromHeader(header, path, info.MtimeMs), storage, key)
}

func validateSessionIDOrGenerated(id *string) error {
	if id == nil {
		return nil
	}
	return validateSessionID(*id)
}

func (r *JsonlSessionRepo) Open(metadata JsonlSessionMetadata, ctx context.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	key := r.sessionKey(metadata.Cwd, metadata.ID)
	r.mu.Lock()
	_, open := r.openSessions[key]
	r.mu.Unlock()
	if open {
		return nil, fmt.Errorf("Session is already open: %s", metadata.ID)
	}
	storage, err := r.loadStorage(metadata, ctx)
	if err != nil {
		return nil, err
	}
	return r.publishOpenSession(metadata, storage, key)
}

func (r *JsonlSessionRepo) List(options *JsonlSessionListOptions, ctx context.Context) ([]JsonlSessionMetadata, error) {
	listOptions := options
	if listOptions == nil {
		listOptions = &JsonlSessionListOptions{}
	}
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	var cwd *string
	if listOptions.Cwd != nil {
		resolved, err := r.fileSystem.AbsolutePath(ctx, *listOptions.Cwd)
		if err != nil {
			return nil, fmt.Errorf("Failed to resolve session cwd %s: %w", *listOptions.Cwd, err)
		}
		cwd = &resolved
	}
	root, err := r.root(ctx)
	if err != nil {
		return nil, err
	}
	exists, err := r.fileSystem.Exists(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("Failed to check sessions root %s: %w", root, err)
	}
	if !exists {
		return []JsonlSessionMetadata{}, nil
	}
	var directories []string
	if cwd == nil {
		directories, err = r.sessionDirectories(root, ctx)
		if err != nil {
			return nil, err
		}
	} else {
		directory, err := r.sessionDirectory(*cwd, ctx)
		if err != nil {
			return nil, err
		}
		directories = []string{directory}
	}
	var metadata []JsonlSessionMetadata
	for _, directory := range directories {
		listed, err := r.listDirectory(directory, cwd, ctx)
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, listed...)
	}
	sortMetadata(metadata)
	return metadata, nil
}

// sortMetadata orders by createdAt desc, then id, then cwd (pi:
// metadata.sort).
func sortMetadata(metadata []JsonlSessionMetadata) {
	less := func(i, j int) bool {
		if metadata[i].CreatedAt != metadata[j].CreatedAt {
			return metadata[i].CreatedAt > metadata[j].CreatedAt
		}
		if metadata[i].ID != metadata[j].ID {
			return metadata[i].ID < metadata[j].ID
		}
		return metadata[i].Cwd < metadata[j].Cwd
	}
	// insertion sort keeps the ordering stable and dependency-free
	for i := 1; i < len(metadata); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			metadata[j], metadata[j-1] = metadata[j-1], metadata[j]
		}
	}
}

func (r *JsonlSessionRepo) Delete(metadata JsonlSessionMetadata, ctx context.Context) error {
	if err := r.assertOpen(); err != nil {
		return err
	}
	key := r.sessionKey(metadata.Cwd, metadata.ID)
	r.mu.Lock()
	_, open := r.openSessions[key]
	r.mu.Unlock()
	if open {
		return fmt.Errorf("Session is open: %s", metadata.ID)
	}
	exists, err := r.fileSystem.Exists(ctx, metadata.Path)
	if err != nil {
		return fmt.Errorf("Failed to check session %s: %w", metadata.Path, err)
	}
	if !exists {
		return fmt.Errorf("Session file does not exist: %s", metadata.Path)
	}
	if err := r.fileSystem.Remove(ctx, metadata.Path, nil, nil); err != nil {
		return fmt.Errorf("Failed to delete session %s: %w", metadata.Path, err)
	}
	return nil
}

func (r *JsonlSessionRepo) Fork(source JsonlSessionMetadata, options session.ForkOptions, ctx context.Context) (session.Session, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	createdAt := r.now()
	r.mu.Lock()
	sourceStorage := r.openSessions[r.sessionKey(source.Cwd, source.ID)]
	r.mu.Unlock()
	var sourceSnapshot session.ForkSourceSnapshot
	var err error
	if sourceStorage == nil {
		sourceSnapshot, err = r.loadClosedForkSourceSnapshot(source, ctx)
	} else {
		sourceSnapshot, err = sourceStorage.CaptureForkSource(ctx)
	}
	if err != nil {
		return nil, err
	}
	cwd, id, err := r.resolveCreateDestination(source.Cwd, options.ID, createdAt, ctx)
	if err != nil {
		return nil, err
	}
	destinationKey := r.sessionKey(cwd, id)
	r.mu.Lock()
	_, open := r.openSessions[destinationKey]
	_, pending := r.pendingCreates[destinationKey]
	if open || pending {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session already exists: %s", id)
	}
	r.pendingCreates[destinationKey] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pendingCreates, destinationKey)
		r.mu.Unlock()
	}()

	path, err := r.resolveNewSessionPath(cwd, createdAt, id, ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := session.CreateForkSnapshot(sourceSnapshot, options)
	if err != nil {
		return nil, err
	}
	header := JsonlStorageHeader{
		V:               JSONLFormatVersion,
		Kind:            "header",
		ID:              id,
		StorageVersion:  JSONLStorageVersion,
		CreatedAt:       createdAt,
		Cwd:             cwd,
		ParentSessionID: &source.ID,
	}
	storage, err := CreateJsonlStorageFromForkSnapshot(&JsonlStorageOptions{FileSystem: r.fileSystem, Path: path, Now: r.now}, header, snapshot, ctx)
	if err != nil {
		return nil, err
	}
	info, err := r.fileSystem.FileInfo(ctx, path)
	if err != nil {
		_ = storage.Close(ctx)
		_ = r.fileSystem.Remove(ctx, path, nil, lo.ToPtr(true))
		return nil, fmt.Errorf("Failed to read session %s: %w", path, err)
	}
	return r.publishOpenSession(metadataFromHeader(header, path, info.MtimeMs), storage, destinationKey)
}

func (r *JsonlSessionRepo) resolveNewSessionPath(cwd string, createdAt int64, id string, ctx context.Context) (string, error) {
	directory, err := r.sessionDirectory(cwd, ctx)
	if err != nil {
		return "", err
	}
	if err := r.assertSessionIDAvailable(directory, id, ctx); err != nil {
		return "", err
	}
	if err := r.fileSystem.CreateDir(ctx, directory, nil); err != nil {
		return "", fmt.Errorf("Failed to create sessions directory %s: %w", directory, err)
	}
	return r.fileSystem.JoinPath(ctx, []string{directory, sessionFileName(createdAt, id)})
}

func (r *JsonlSessionRepo) assertSessionIDAvailable(directory string, id string, ctx context.Context) error {
	exists, err := r.fileSystem.Exists(ctx, directory)
	if err != nil {
		return fmt.Errorf("Failed to check sessions directory %s: %w", directory, err)
	}
	if !exists {
		return nil
	}
	suffix := "_" + url.QueryEscape(id) + ".jsonl"
	entries, err := r.fileSystem.ListDir(ctx, directory)
	if err != nil {
		return fmt.Errorf("Failed to list sessions directory %s: %w", directory, err)
	}
	for _, entry := range entries {
		if entry.Kind != harnessFileKindDirectory && strings.HasSuffix(entry.Name, suffix) {
			return fmt.Errorf("Session already exists: %s", id)
		}
	}
	return nil
}

// listDirectory lists the sessions in one cwd directory (pi: listDirectory).
func (r *JsonlSessionRepo) listDirectory(directory string, cwd *string, ctx context.Context) ([]JsonlSessionMetadata, error) {
	exists, err := r.fileSystem.Exists(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("Failed to check sessions directory %s: %w", directory, err)
	}
	if !exists {
		return nil, nil
	}
	files, err := r.fileSystem.ListDir(ctx, directory)
	if err != nil {
		return nil, fmt.Errorf("Failed to list sessions directory %s: %w", directory, err)
	}
	var metadata []JsonlSessionMetadata
	for _, file := range files {
		if file.Kind == harnessFileKindDirectory || !strings.HasSuffix(file.Name, ".jsonl") {
			continue
		}
		discovered, err := r.readSessionMetadata(file, ctx)
		if err != nil {
			return nil, err
		}
		if discovered == nil {
			continue
		}
		// Directory encoding is lossy: /a/b and /a-b both map to --a-b--.
		if cwd == nil || discovered.Cwd == *cwd {
			metadata = append(metadata, *discovered)
		}
	}
	return metadata, nil
}

// readSessionMetadata reads the header of one session file (pi:
// readSessionMetadata).
func (r *JsonlSessionRepo) readSessionMetadata(file harnessFileInfo, ctx context.Context) (*JsonlSessionMetadata, error) {
	lines, err := r.fileSystem.ReadTextLines(ctx, file.Path, lo.ToPtr(1))
	if err != nil {
		return nil, fmt.Errorf("Failed to read session header %s: %w", file.Path, err)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	parsedHeader, err := ParseJsonlSessionHeader(lines[0])
	if err != nil {
		return nil, nil
	}
	if parsedHeader.Format == "v3-legacy" {
		base, err := MetadataFromLegacyV3Header(r.fileSystem, parsedHeader.V3Legacy, ctx)
		if err != nil {
			return nil, err
		}
		metadata := JsonlSessionMetadata{
			SessionMetadata: base,
			Cwd:             metadataCwd(base),
			Path:            file.Path,
			ModifiedAt:      file.MtimeMs,
		}
		return &metadata, nil
	}
	metadata := metadataFromHeader(*parsedHeader.V4, file.Path, file.MtimeMs)
	return &metadata, nil
}

// loadClosedForkSourceSnapshot loads a closed source session for forking
// (pi: loadClosedForkSourceSnapshot).
func (r *JsonlSessionRepo) loadClosedForkSourceSnapshot(source JsonlSessionMetadata, ctx context.Context) (session.ForkSourceSnapshot, error) {
	storage, err := r.loadStorage(source, ctx)
	if err != nil {
		return session.ForkSourceSnapshot{}, err
	}
	snapshot, err := storage.CaptureForkSource(ctx)
	closeErr := storage.Close(ctx)
	if err != nil {
		return session.ForkSourceSnapshot{}, err
	}
	if closeErr != nil {
		return session.ForkSourceSnapshot{}, closeErr
	}
	return snapshot, nil
}

// publishOpenSession registers one open storage and wraps it in a session
// (pi: publishOpenSession).
func (r *JsonlSessionRepo) publishOpenSession(metadata JsonlSessionMetadata, storage *JsonlStorage, key string) (*session.StorageBackedSession, error) {
	r.mu.Lock()
	if _, open := r.openSessions[key]; open {
		r.mu.Unlock()
		return nil, fmt.Errorf("Session is already open: %s", metadata.ID)
	}
	r.openSessions[key] = storage
	r.mu.Unlock()
	sess := session.NewStorageBackedSession(metadata.SessionMetadata, storage, session.StorageBackedSessionOptions{
		OnClose: func() {
			r.mu.Lock()
			if r.openSessions[key] == storage {
				delete(r.openSessions, key)
			}
			r.mu.Unlock()
		},
	})
	sess.SetTypedMetadata(metadata)
	return sess, nil
}

func (r *JsonlSessionRepo) sessionKey(cwd string, id string) string {
	return cwd + "\x00" + id
}

// loadStorage loads and validates one session storage (pi: loadStorage).
func (r *JsonlSessionRepo) loadStorage(metadata JsonlSessionMetadata, ctx context.Context) (*JsonlStorage, error) {
	exists, err := r.fileSystem.Exists(ctx, metadata.Path)
	if err != nil {
		return nil, fmt.Errorf("Failed to check session %s: %w", metadata.Path, err)
	}
	if !exists {
		return nil, fmt.Errorf("Session file does not exist: %s", metadata.Path)
	}
	storage, err := OpenJsonlStorage(&JsonlStorageOptions{FileSystem: r.fileSystem, Path: metadata.Path, Now: r.now}, ctx)
	if err != nil {
		return nil, err
	}
	header := storage.Header()
	if header.ID != metadata.ID || header.Cwd != metadata.Cwd {
		_ = storage.Close(ctx)
		return nil, fmt.Errorf("Session identity does not match header: %s", metadata.ID)
	}
	if header.StorageVersion != JSONLStorageVersion {
		_ = storage.Close(ctx)
		return nil, fmt.Errorf("Session %s uses unsupported storage version %d", metadata.ID, header.StorageVersion)
	}
	return storage, nil
}

// Close marks the repo closed. pi leaves a TODO: "Define ownership semantics
// before deciding whether repository close should close session handles." —
// close() resolves immediately without closing open sessions. pig mirrors
// that.
func (r *JsonlSessionRepo) Close(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// Compile-time interface instantiation (pi: JsonlSessionRepo implements
// SessionRepo<JsonlSessionMetadata, JsonlSessionCreateOptions,
// JsonlSessionListOptions>).
var _ session.SessionRepo[JsonlSessionMetadata, JsonlSessionCreateOptions, *JsonlSessionListOptions] = (*JsonlSessionRepo)(nil)
