// pi: core/resource-loader.ts — the ResourceLoader interface, restricted to
// the consumer surface AgentSession uses (pi: the ResourceLoader interface
// at the top of resource-loader.ts; the full loader implementation arrives
// with the resource-loader/extensions batch, see the batch plan). The
// getThemes/getSystemPromptSource/getAppendSystemPromptSources members are
// declared here so the interface shape stays visible, but return
// placeholder values until the theme and source-info batches land.
package core

// ResourceExtensionPathMetadata describes where an extension-contributed
// resource came from (pi: buildExtensionResourcePaths' metadata literal).
type ResourceExtensionPathMetadata struct {
	// Source labels the contributing extension (pi: source).
	Source string
	// Scope is always "temporary" for extension-contributed paths.
	Scope string
	// Origin is always "top-level" for extension-contributed paths.
	Origin string
	// BaseDir is the extension's directory; "" = none (pi: baseDir?).
	BaseDir string
}

// ResourceExtensionPath is one extension-contributed resource path (pi:
// { path, metadata }).
type ResourceExtensionPath struct {
	Path     string
	Metadata ResourceExtensionPathMetadata
}

// ResourceExtensionPaths bundles the extension-contributed resource paths
// (pi: ResourceExtensionPaths).
type ResourceExtensionPaths struct {
	SkillPaths  []ResourceExtensionPath
	PromptPaths []ResourceExtensionPath
	ThemePaths  []ResourceExtensionPath
}

// AgentsFile is one loaded AGENTS.md-style context file (pi:
// getAgentsFiles' { path, content } members).
type AgentsFile struct {
	Path    string
	Content string
}

// ResourceLoader supplies the session-scoped resources to AgentSession (pi:
// ResourceLoader).
type ResourceLoader interface {
	// GetExtensions returns the loaded extensions result (pi:
	// getExtensions).
	GetExtensions() *LoadExtensionsResult
	// GetSkills returns the discovered skills (pi: getSkills).
	GetSkills() ([]Skill, []ResourceDiagnostic)
	// GetPrompts returns the discovered prompt templates (pi: getPrompts).
	GetPrompts() ([]PromptTemplate, []ResourceDiagnostic)
	// GetAgentsFiles returns the AGENTS.md-style context files (pi:
	// getAgentsFiles).
	GetAgentsFiles() []AgentsFile
	// GetSystemPrompt returns the custom system prompt override; ok=false =
	// none (pi: getSystemPrompt: string | undefined).
	GetSystemPrompt() (string, bool)
	// GetSystemPromptSource returns the override source path; ok=false =
	// none (pi: getSystemPromptSource).
	GetSystemPromptSource() (string, bool)
	// GetAppendSystemPrompt returns the append system prompt segments (pi:
	// getAppendSystemPrompt).
	GetAppendSystemPrompt() []string
	// GetAppendSystemPromptSources returns the append source paths (pi:
	// getAppendSystemPromptSources).
	GetAppendSystemPromptSources() []string
	// GetThemes returns the loaded themes and diagnostics (pi: getThemes;
	// themes arrive with the theme batch).
	GetThemes() ([]any, []ResourceDiagnostic)
	// ExtendResources merges extension-contributed resource paths (pi:
	// extendResources).
	ExtendResources(paths ResourceExtensionPaths)
	// Reload reloads all resources (pi: reload(options?)).
	Reload(options *ResourceLoaderReloadOptions) error
}
