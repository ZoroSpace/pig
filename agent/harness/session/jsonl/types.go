// pi: src/harness/session/jsonl/types.ts — jsonl backend types.
//
// Deviations:
//   - JsonlSessionMetadata embeds session.SessionMetadata (pi: extends
//     SessionMetadata), flattening to the same JSON wire shape.
//   - sourceFormat's `3 | 4` union → int with SourceFormat constants.
//   - JsonlSessionRepoFileSystem (pi: Pick<FileSystem, ...>) is a Go
//     interface with the picked members; harness.FileSystem implementations
//     satisfy it structurally. Methods carry the ctx parameter per the
//     harness.FileSystem convention (the session layer passes
//     context.Background() — its TS source never forwards an abort signal).
//   - TS `modifiedAt`/`createdAt` stay int64 Unix ms.
package jsonl

import (
	"context"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
)

// Source format constants (pi: `sourceFormat: 3 | 4`).
const (
	SourceFormatV3 = 3
	SourceFormatV4 = 4
)

// JsonlSessionMetadata is one jsonl session's list metadata (pi:
// JsonlSessionMetadata).
type JsonlSessionMetadata struct {
	session.SessionMetadata
	Cwd  string `json:"cwd"`
	Path string `json:"path"`
	// ModifiedAt is the filesystem modification time as milliseconds since
	// Unix epoch.
	ModifiedAt int64 `json:"modifiedAt"`
	// SourceFormat is the on-disk format version the file was read from.
	SourceFormat int `json:"sourceFormat"`
	// LegacyParentSessionPath is present only when a v3 parent path could
	// not be resolved to a session id.
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
	// Metadata is opaque application-owned metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// JsonlSessionCreateOptions parameterizes jsonl session creation (pi:
// JsonlSessionCreateOptions).
type JsonlSessionCreateOptions struct {
	session.SessionCreateOptions
	Cwd string `json:"cwd"`
	// Metadata is opaque application-owned metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// JsonlSessionListOptions filters session listings (pi:
// JsonlSessionListOptions).
type JsonlSessionListOptions struct {
	// Cwd restricts the listing to one session directory.
	Cwd *string `json:"cwd,omitempty"`
}

// JsonlV4Header is the first line of a v4 session file (pi: JsonlV4Header).
type JsonlV4Header struct {
	Kind    string `json:"kind"` // always "header"
	Version int    `json:"version"`
	ID      string `json:"id"`
	// CreatedAt is Unix milliseconds.
	CreatedAt int64  `json:"createdAt"`
	Cwd       string `json:"cwd"`
	// ParentSessionID links the session to its source; nil when standalone.
	ParentSessionID *string `json:"parentSessionId,omitempty"`
	// LegacyParentSessionPath is preserved only when a v3 parent path could
	// not be resolved to a session id.
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
	// Metadata is opaque application-owned metadata.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// JsonlSessionRepoFileSystem is the filesystem surface the jsonl backend
// consumes (pi: Pick<FileSystem, ...>; see file-header note).
type JsonlSessionRepoFileSystem interface {
	AbsolutePath(ctx context.Context, path string) (string, error)
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadTextFile(ctx context.Context, path string) (string, error)
	ReadTextLines(ctx context.Context, path string, maxLines *int) ([]string, error)
	WriteFile(ctx context.Context, path string, content []byte) error
	AppendFile(ctx context.Context, path string, content []byte) error
	RenameFile(ctx context.Context, sourcePath string, destinationPath string) error
	FileInfo(ctx context.Context, path string) (harness.FileInfo, error)
	ListDir(ctx context.Context, path string) ([]harness.FileInfo, error)
	Exists(ctx context.Context, path string) (bool, error)
	CreateDir(ctx context.Context, path string, recursive *bool) error
	Remove(ctx context.Context, path string, recursive *bool, force *bool) error
}

// JsonlSessionRepoOptions parameterizes the jsonl repo (pi:
// JsonlSessionRepoOptions).
type JsonlSessionRepoOptions struct {
	Fs JsonlSessionRepoFileSystem
	// SessionsRoot is the root containing coding-agent-compatible
	// cwd-encoded session directories.
	SessionsRoot string
}
