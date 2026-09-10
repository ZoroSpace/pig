// pi: src/core/source-info.ts — resource source provenance.
//
// Lives in utils (not core) because the theme package needs the SourceInfo
// type and cannot import core (core → core/tools and, since T5a-2,
// core/tools → theme). core aliases these types so its public API is
// unchanged.
package utils

// SourceScope identifies where a resource came from (pi: SourceScope).
type SourceScope = string

// Source scope values (pi: "user" | "project" | "temporary").
const (
	SourceScopeUser      SourceScope = "user"
	SourceScopeProject   SourceScope = "project"
	SourceScopeTemporary SourceScope = "temporary"
)

// SourceOrigin identifies how a resource was referenced (pi: SourceOrigin).
type SourceOrigin = string

// Source origin values (pi: "package" | "top-level").
const (
	SourceOriginPackage  SourceOrigin = "package"
	SourceOriginTopLevel SourceOrigin = "top-level"
)

// SourceInfo records where a loaded resource came from (pi: SourceInfo).
type SourceInfo struct {
	Path    string       `json:"path"`
	Source  string       `json:"source"`
	Scope   SourceScope  `json:"scope"`
	Origin  SourceOrigin `json:"origin"`
	BaseDir string       `json:"baseDir,omitempty"`
}
