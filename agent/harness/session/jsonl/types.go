// pi: harness/session/jsonl/types.ts — JSONL storage constants, header and
// repo option types (format 4).
package jsonl

import (
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
)

// JSONLFormatVersion is the wire format version (pi: JSONL_FORMAT_VERSION).
const JSONLFormatVersion = 4

// JSONLStorageVersion is the storage-layout version (pi:
// JSONL_STORAGE_VERSION).
const JSONLStorageVersion = 1

// JsonlStorageHeader is the v4 file header (pi: JsonlStorageHeader).
type JsonlStorageHeader struct {
	V               int     `json:"v"` // JSONLFormatVersion
	Kind            string  `json:"kind"`
	ID              string  `json:"id"`
	StorageVersion  int     `json:"storageVersion"`
	CreatedAt       int64   `json:"createdAt"` // Unix ms
	Cwd             string  `json:"cwd"`
	ParentSessionID *string `json:"parentSessionId,omitempty"`
	// LegacyParentSessionPath survives when the legacy v3 parent file could
	// not be resolved to an id.
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
	// NextSeq is the sequence high-water mark written by snapshot rewrites.
	NextSeq *int64 `json:"nextSeq,omitempty"`
}

// JsonlStorageOptions parameterizes one storage file (pi:
// JsonlStorageOptions).
type JsonlStorageOptions struct {
	FileSystem harness.FileSystem
	Path       string
	// Now overrides the commit timestamp source.
	Now func() int64
}

// JsonlSessionMetadata is the JSONL session metadata (pi:
// JsonlSessionMetadata).
type JsonlSessionMetadata struct {
	session.SessionMetadata
	Cwd  string `json:"cwd"`
	Path string `json:"path"`
	// ModifiedAt is the filesystem modification time as milliseconds since
	// Unix epoch.
	ModifiedAt int64 `json:"modifiedAt"`
}

// JsonlSessionCreateOptions parameterizes creation (pi:
// JsonlSessionCreateOptions).
type JsonlSessionCreateOptions struct {
	session.SessionCreateOptions
	Cwd string
}

// JsonlSessionListOptions parameterizes listing (pi: JsonlSessionListOptions).
type JsonlSessionListOptions struct {
	Cwd *string
}

// JsonlSessionRepoOptions parameterizes the repo (pi: JsonlSessionRepoOptions).
type JsonlSessionRepoOptions struct {
	FileSystem   harness.FileSystem
	SessionsRoot string
	// Now overrides the timestamp source.
	Now func() int64
}
