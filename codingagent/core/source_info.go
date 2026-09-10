// pi: src/core/source-info.ts — resource source provenance.
//
// The SourceInfo/SourceScope/SourceOrigin types live in codingagent/utils
// (the theme package needs the type and cannot import core); they are
// aliased here so core's public API is unchanged.
//
// createSourceInfo now builds from PathMetadata in package_manager_paths.go
// (the npm/git half of package-manager.ts remains deferred).
package core

import "github.com/ZoroSpace/pig/codingagent/utils"

// SourceScope identifies where a resource came from (pi: SourceScope).
type SourceScope = utils.SourceScope

// Source scope values (pi: "user" | "project" | "temporary").
const (
	SourceScopeUser      = utils.SourceScopeUser
	SourceScopeProject   = utils.SourceScopeProject
	SourceScopeTemporary = utils.SourceScopeTemporary
)

// SourceOrigin identifies how a resource was referenced (pi: SourceOrigin).
type SourceOrigin = utils.SourceOrigin

// Source origin values (pi: "package" | "top-level").
const (
	SourceOriginPackage  = utils.SourceOriginPackage
	SourceOriginTopLevel = utils.SourceOriginTopLevel
)

// SourceInfo records where a loaded resource came from (pi: SourceInfo).
type SourceInfo = utils.SourceInfo

// SyntheticSourceInfoOptions carries createSyntheticSourceInfo's optional
// fields (pi: inline options object; "" = undefined).
type SyntheticSourceInfoOptions struct {
	Source  string
	Scope   SourceScope
	Origin  SourceOrigin
	BaseDir string
}

// CreateSyntheticSourceInfo builds a SourceInfo without package metadata
// (pi: createSyntheticSourceInfo).
func CreateSyntheticSourceInfo(path string, options SyntheticSourceInfoOptions) SourceInfo {
	scope := options.Scope
	if scope == "" {
		scope = SourceScopeTemporary
	}
	origin := options.Origin
	if origin == "" {
		origin = SourceOriginTopLevel
	}
	return SourceInfo{
		Path:    path,
		Source:  options.Source,
		Scope:   scope,
		Origin:  origin,
		BaseDir: options.BaseDir,
	}
}

// CreateSourceInfo builds a SourceInfo from package-manager path metadata
// (pi: createSourceInfo). Previously deferred with the package-manager
// batch; PathMetadata now lives in package_manager_paths.go.
func CreateSourceInfo(path string, metadata PathMetadata) SourceInfo {
	return SourceInfo{
		Path:    path,
		Source:  metadata.Source,
		Scope:   metadata.Scope,
		Origin:  metadata.Origin,
		BaseDir: metadata.BaseDir,
	}
}
