// pi: core/resource-loader.ts — DefaultResourceLoader, the no-extension /
// no-package subset. The extension loading halves (loadExtensionsCached,
// loadExtensionFactories, clearExtensionCache, addExtensionConflictDiagnostics)
// arrive with the extension/sidecar batch (Phase 7) — until then
// extensionsResult is always the empty result and extension conflict
// diagnostics cannot occur (declared at loadFinalExtensionSet). Theme
// loading is deferred with the theme batch (Phase 6/8) — declared at
// updateThemesFromPaths/loadThemes. Package sources resolve only through the
// settings half in package_manager_paths.go (pi: resolve()'s npm/git half
// deferred). The override hooks and no* flags keep pi's full shape so the
// later batches fill in without re-shaping this struct.
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// ResourceLoaderReloadOptions carries reload-time inputs (pi:
// ResourceLoaderReloadOptions).
type ResourceLoaderReloadOptions struct {
	// ResolveProjectTrust decides project trust during reload (pi:
	// resolveProjectTrust). Called with the pre-trust extensions result when
	// set.
	ResolveProjectTrust func(extensionsResult *LoadExtensionsResult) (bool, error)
}

// DefaultResourceLoaderOptions configures the loader (pi:
// DefaultResourceLoaderOptions).
type DefaultResourceLoaderOptions struct {
	Cwd             string
	AgentDir        string
	SettingsManager *SettingsManager
	EventBus        EventBus
	// PackageManager is deferred with the package-manager batch (pi:
	// packageManager / DefaultPackageManager).
	AdditionalExtensionPaths      []string
	AdditionalSkillPaths          []string
	AdditionalPromptTemplatePaths []string
	AdditionalThemePaths          []string
	// ExtensionFactories is deferred with the extension batch (pi:
	// extensionFactories: InlineExtension[]).
	NoExtensions       bool
	NoSkills           bool
	NoPromptTemplates  bool
	NoThemes           bool
	NoContextFiles     bool
	SystemPrompt       string
	AppendSystemPrompt []string
	// ExtensionsOverride is deferred with the extension batch (pi:
	// extensionsOverride).
	SkillsOverride             func(base LoadSkillsResult) LoadSkillsResult
	PromptsOverride            func(base PromptLoaderResult) PromptLoaderResult
	ThemesOverride             func(base ThemeLoaderResult) ThemeLoaderResult
	AgentsFilesOverride        func(base []AgentsFile) []AgentsFile
	SystemPromptOverride       func(base string, ok bool) (string, bool)
	AppendSystemPromptOverride func(base []string) []string
}

// PromptLoaderResult bundles prompts and diagnostics (pi's inline
// { prompts, diagnostics } shape).
type PromptLoaderResult struct {
	Prompts     []PromptTemplate
	Diagnostics []ResourceDiagnostic
}

// ThemeLoaderResult bundles themes and diagnostics (pi's inline
// { themes, diagnostics } shape). Theme is deferred with the theme batch;
// the result keeps the diagnostics half.
type ThemeLoaderResult struct {
	Themes      []any
	Diagnostics []ResourceDiagnostic
}

// resourcePathMetadataEntry pairs a resolved path with its metadata (pi's
// resourceMetadataByPath Map<string, PathMetadata>).
type resourcePathMetadata map[string]PathMetadata

// DefaultResourceLoader is the main resource loader (pi:
// DefaultResourceLoader).
type DefaultResourceLoader struct {
	cwd             string
	agentDir        string
	settingsManager *SettingsManager
	eventBus        EventBus
	// packageManager is deferred with the package-manager batch (pi:
	// DefaultPackageManager).
	additionalExtensionPaths      []string
	additionalSkillPaths          []string
	additionalPromptTemplatePaths []string
	additionalThemePaths          []string
	noExtensions                  bool
	noSkills                      bool
	noPromptTemplates             bool
	noThemes                      bool
	noContextFiles                bool
	systemPromptSource            string
	appendSystemPromptSource      []string
	skillsOverride                func(base LoadSkillsResult) LoadSkillsResult
	promptsOverride               func(base PromptLoaderResult) PromptLoaderResult
	themesOverride                func(base ThemeLoaderResult) ThemeLoaderResult
	agentsFilesOverride           func(base []AgentsFile) []AgentsFile
	systemPromptOverride          func(base string, ok bool) (string, bool)
	appendSystemPromptOverride    func(base []string) []string

	extensionsResult              *LoadExtensionsResult
	skills                        []Skill
	skillDiagnostics              []ResourceDiagnostic
	prompts                       []PromptTemplate
	promptDiagnostics             []ResourceDiagnostic
	themeDiagnostics              []ResourceDiagnostic
	agentsFiles                   []AgentsFile
	systemPrompt                  string
	hasSystemPrompt               bool
	systemPromptSourcePath        string
	appendSystemPrompt            []string
	appendSystemPromptSourcePaths []string
	lastSkillPaths                []string
	extensionSkillSourceInfos     map[string]SourceInfo
	extensionPromptSourceInfos    map[string]SourceInfo
	resourceMetadataByPath        resourcePathMetadata
	lastPromptPaths               []string
	lastThemePaths                []string
	loaded                        bool
}

// NewDefaultResourceLoader builds the loader (pi: constructor).
func NewDefaultResourceLoader(options DefaultResourceLoaderOptions) *DefaultResourceLoader {
	l := &DefaultResourceLoader{}
	l.cwd = utils.ResolvePath(options.Cwd, "", nil)
	l.agentDir = utils.ResolvePath(options.AgentDir, "", nil)
	if options.SettingsManager != nil {
		l.settingsManager = options.SettingsManager
	} else {
		l.settingsManager = CreateSettingsManager(l.cwd, l.agentDir, nil)
	}
	if options.EventBus != nil {
		l.eventBus = options.EventBus
	} else {
		l.eventBus = CreateEventBus()
	}
	l.additionalExtensionPaths = options.AdditionalExtensionPaths
	l.additionalSkillPaths = options.AdditionalSkillPaths
	l.additionalPromptTemplatePaths = options.AdditionalPromptTemplatePaths
	l.additionalThemePaths = options.AdditionalThemePaths
	l.noExtensions = options.NoExtensions
	l.noSkills = options.NoSkills
	l.noPromptTemplates = options.NoPromptTemplates
	l.noThemes = options.NoThemes
	l.noContextFiles = options.NoContextFiles
	l.systemPromptSource = options.SystemPrompt
	l.appendSystemPromptSource = options.AppendSystemPrompt
	l.skillsOverride = options.SkillsOverride
	l.promptsOverride = options.PromptsOverride
	l.themesOverride = options.ThemesOverride
	l.agentsFilesOverride = options.AgentsFilesOverride
	l.systemPromptOverride = options.SystemPromptOverride
	l.appendSystemPromptOverride = options.AppendSystemPromptOverride

	l.extensionsResult = &LoadExtensionsResult{
		Extensions: []*Extension{},
		Errors:     []LoadExtensionError{},
		Runtime:    CreateExtensionRuntime(),
	}
	l.skills = []Skill{}
	l.skillDiagnostics = []ResourceDiagnostic{}
	l.prompts = []PromptTemplate{}
	l.promptDiagnostics = []ResourceDiagnostic{}
	l.agentsFiles = []AgentsFile{}
	l.appendSystemPrompt = []string{}
	l.appendSystemPromptSourcePaths = []string{}
	l.lastSkillPaths = []string{}
	l.extensionSkillSourceInfos = map[string]SourceInfo{}
	l.extensionPromptSourceInfos = map[string]SourceInfo{}
	l.resourceMetadataByPath = resourcePathMetadata{}
	l.lastPromptPaths = []string{}
	l.lastThemePaths = []string{}
	return l
}

// GetExtensions returns the loaded extensions result (pi: getExtensions).
func (l *DefaultResourceLoader) GetExtensions() *LoadExtensionsResult {
	return l.extensionsResult
}

// GetSkills returns skills and diagnostics (pi: getSkills).
func (l *DefaultResourceLoader) GetSkills() ([]Skill, []ResourceDiagnostic) {
	return l.skills, l.skillDiagnostics
}

// GetPrompts returns prompts and diagnostics (pi: getPrompts).
func (l *DefaultResourceLoader) GetPrompts() ([]PromptTemplate, []ResourceDiagnostic) {
	return l.prompts, l.promptDiagnostics
}

// GetThemes returns themes and diagnostics (pi: getThemes). Theme loading is
// deferred with the theme batch; only diagnostics are populated today.
func (l *DefaultResourceLoader) GetThemes() ([]any, []ResourceDiagnostic) {
	return nil, l.themeDiagnostics
}

// GetAgentsFiles returns the context files (pi: getAgentsFiles).
func (l *DefaultResourceLoader) GetAgentsFiles() []AgentsFile {
	return l.agentsFiles
}

// GetSystemPrompt returns the custom system prompt override (pi:
// getSystemPrompt). ok=false = undefined.
func (l *DefaultResourceLoader) GetSystemPrompt() (string, bool) {
	return l.systemPrompt, l.hasSystemPrompt
}

// GetSystemPromptSource returns the system prompt source path (pi:
// getSystemPromptSource).
func (l *DefaultResourceLoader) GetSystemPromptSource() (string, bool) {
	if l.systemPromptSourcePath == "" {
		return "", false
	}
	return l.systemPromptSourcePath, true
}

// GetAppendSystemPrompt returns the append segments (pi:
// getAppendSystemPrompt).
func (l *DefaultResourceLoader) GetAppendSystemPrompt() []string {
	return l.appendSystemPrompt
}

// GetAppendSystemPromptSources returns append source paths (pi:
// getAppendSystemPromptSources).
func (l *DefaultResourceLoader) GetAppendSystemPromptSources() []string {
	return l.appendSystemPromptSourcePaths
}

// ExtendResources merges extension-contributed resource paths (pi:
// extendResources). pi's PathMetadata entries carry baseDir; pig reuses
// PathMetadata with the extension metadata literal mapped by the caller.
func (l *DefaultResourceLoader) ExtendResources(paths ResourceExtensionPaths) {
	skillPaths := l.normalizeExtensionPaths(paths.SkillPaths)
	promptPaths := l.normalizeExtensionPaths(paths.PromptPaths)
	// Theme paths are deferred with the theme batch (pi: themePaths).

	for _, entry := range skillPaths {
		extSkillMetadata := PathMetadata{Source: entry.Metadata.Source, Scope: entry.Metadata.Scope, Origin: entry.Metadata.Origin, BaseDir: entry.Metadata.BaseDir}
		l.extensionSkillSourceInfos[entry.Path] = CreateSourceInfo(entry.Path, extSkillMetadata)
	}
	for _, entry := range promptPaths {
		extPromptMetadata := PathMetadata{Source: entry.Metadata.Source, Scope: entry.Metadata.Scope, Origin: entry.Metadata.Origin, BaseDir: entry.Metadata.BaseDir}
		l.extensionPromptSourceInfos[entry.Path] = CreateSourceInfo(entry.Path, extPromptMetadata)
	}

	if len(skillPaths) > 0 {
		merged := make([]string, 0, len(l.lastSkillPaths)+len(skillPaths))
		for _, entry := range skillPaths {
			merged = append(merged, entry.Path)
		}
		l.lastSkillPaths = l.mergePaths(l.lastSkillPaths, merged)
		l.updateSkillsFromPaths(l.lastSkillPaths, l.resourceMetadataByPath)
	}

	if len(promptPaths) > 0 {
		merged := make([]string, 0, len(l.lastPromptPaths)+len(promptPaths))
		for _, entry := range promptPaths {
			merged = append(merged, entry.Path)
		}
		l.lastPromptPaths = l.mergePaths(l.lastPromptPaths, merged)
		l.updatePromptsFromPaths(l.lastPromptPaths, l.resourceMetadataByPath)
	}
}

// Reload reloads all resources (pi: reload).
func (l *DefaultResourceLoader) Reload(options *ResourceLoaderReloadOptions) error {
	ResetTimings(TimingExtensions)

	// pi: if (this.loaded) clearExtensionCache() — deferred with the
	// extension batch.

	var preTrustExtensions *LoadExtensionsResult
	if options != nil && options.ResolveProjectTrust != nil {
		var err error
		preTrustExtensions, err = l.loadProjectTrustExtensions()
		if err != nil {
			return err
		}
		projectTrusted, err := options.ResolveProjectTrust(preTrustExtensions)
		if err != nil {
			return err
		}
		l.settingsManager.SetProjectTrusted(projectTrusted)
	}

	// reload() preserves SettingsManager.projectTrusted and reloads settings
	// for that trust state.
	l.settingsManager.Reload()
	resolvedPaths := ResolveSettingsResourcePaths(l.settingsManager, l.cwd, l.agentDir)
	cliExtensionPaths := l.resolveExtensionSourcesLocalOnly(l.additionalExtensionPaths)
	// Kept on the instance so post-reload passes (extendResources) can still
	// resolve package metadata.
	l.resourceMetadataByPath = resourcePathMetadata{}
	metadataByPath := l.resourceMetadataByPath

	l.extensionSkillSourceInfos = map[string]SourceInfo{}
	l.extensionPromptSourceInfos = map[string]SourceInfo{}

	getEnabledResources := func(resources []ResolvedResource) []ResolvedResource {
		for _, r := range resources {
			if _, ok := metadataByPath[r.Path]; !ok {
				metadataByPath[r.Path] = r.Metadata
			}
		}
		var enabled []ResolvedResource
		for _, r := range resources {
			if r.Enabled {
				enabled = append(enabled, r)
			}
		}
		return enabled
	}
	getEnabledPaths := func(resources []ResolvedResource) []string {
		var paths []string
		for _, r := range getEnabledResources(resources) {
			paths = append(paths, r.Path)
		}
		return paths
	}
	enabledExtensions := getEnabledPaths(resolvedPaths.Extensions)
	enabledSkillResources := getEnabledResources(resolvedPaths.Skills)
	enabledPrompts := getEnabledPaths(resolvedPaths.Prompts)
	// enabledThemes are deferred with the theme batch (pi: resolvedPaths.
	// themes feeding updateThemesFromPaths).

	var enabledSkills []string
	for _, resource := range enabledSkillResources {
		enabledSkills = append(enabledSkills, l.mapSkillPath(resource, metadataByPath))
	}

	// Add CLI paths metadata
	for _, r := range cliExtensionPaths.Extensions {
		if _, ok := metadataByPath[r.Path]; !ok {
			metadataByPath[r.Path] = PathMetadata{Source: "cli", Scope: SourceScopeTemporary, Origin: SourceOriginTopLevel}
		}
	}
	for _, r := range cliExtensionPaths.Skills {
		if _, ok := metadataByPath[r.Path]; !ok {
			metadataByPath[r.Path] = PathMetadata{Source: "cli", Scope: SourceScopeTemporary, Origin: SourceOriginTopLevel}
		}
	}

	cliEnabledExtensions := getEnabledPaths(cliExtensionPaths.Extensions)
	cliEnabledSkills := getEnabledPaths(cliExtensionPaths.Skills)
	cliEnabledPrompts := getEnabledPaths(cliExtensionPaths.Prompts)

	var extensionPaths []string
	if l.noExtensions {
		extensionPaths = cliEnabledExtensions
	} else {
		extensionPaths = l.mergePaths(cliEnabledExtensions, enabledExtensions)
	}

	extensionsResult := l.loadFinalExtensionSet(extensionPaths)
	for _, p := range l.additionalExtensionPaths {
		if utils.IsLocalPath(p) {
			resolved := l.resolveResourcePath(p)
			if _, err := os.Stat(resolved); err != nil {
				extensionsResult.Errors = append(extensionsResult.Errors, LoadExtensionError{
					Path:  resolved,
					Error: fmt.Sprintf("Extension path does not exist: %s", resolved),
				})
			}
		}
	}
	l.extensionsResult = extensionsResult

	var skillPaths []string
	if l.noSkills {
		skillPaths = l.mergePaths(cliEnabledSkills, l.additionalSkillPaths)
	} else {
		skillPaths = l.mergePaths(append(append([]string{}, cliEnabledSkills...), enabledSkills...), l.additionalSkillPaths)
	}

	l.lastSkillPaths = skillPaths
	l.updateSkillsFromPaths(skillPaths, metadataByPath)
	for _, p := range l.additionalSkillPaths {
		if utils.IsLocalPath(p) {
			resolved := l.resolveResourcePath(p)
			if _, err := os.Stat(resolved); err != nil && !containsDiagnosticPath(l.skillDiagnostics, resolved) {
				l.skillDiagnostics = append(l.skillDiagnostics, ResourceDiagnostic{
					Type:    "error",
					Message: "Skill path does not exist",
					Path:    resolved,
				})
			}
		}
	}

	var promptPaths []string
	if l.noPromptTemplates {
		promptPaths = l.mergePaths(cliEnabledPrompts, l.additionalPromptTemplatePaths)
	} else {
		promptPaths = l.mergePaths(append(append([]string{}, cliEnabledPrompts...), enabledPrompts...), l.additionalPromptTemplatePaths)
	}

	l.lastPromptPaths = promptPaths
	l.updatePromptsFromPaths(promptPaths, metadataByPath)
	for _, p := range l.additionalPromptTemplatePaths {
		if utils.IsLocalPath(p) {
			resolved := l.resolveResourcePath(p)
			if _, err := os.Stat(resolved); err != nil && !containsDiagnosticPath(l.promptDiagnostics, resolved) {
				l.promptDiagnostics = append(l.promptDiagnostics, ResourceDiagnostic{
					Type:    "error",
					Message: "Prompt template path does not exist",
					Path:    resolved,
				})
			}
		}
	}

	// Theme loading is deferred with the theme batch (pi: themePaths merge +
	// updateThemesFromPaths). lastThemePaths bookkeeping is kept so the later
	// batch slots in without re-shaping reload().
	l.lastThemePaths = l.mergePaths(l.lastThemePaths, l.additionalThemePaths)

	var agentsFiles []AgentsFile
	if !l.noContextFiles {
		agentsFiles = LoadProjectContextFiles(l.cwd, l.agentDir)
	}
	if l.agentsFilesOverride != nil {
		agentsFiles = l.agentsFilesOverride(agentsFiles)
	}
	l.agentsFiles = agentsFiles

	systemPromptSource := l.systemPromptSource
	if systemPromptSource == "" {
		systemPromptSource = l.discoverSystemPromptFile()
	}
	baseSystemPrompt, baseSystemPromptOK := resolvePromptInput(systemPromptSource, "system prompt")
	if l.systemPromptOverride != nil {
		l.systemPrompt, l.hasSystemPrompt = l.systemPromptOverride(baseSystemPrompt, baseSystemPromptOK)
	} else {
		l.systemPrompt, l.hasSystemPrompt = baseSystemPrompt, baseSystemPromptOK
	}
	if systemPromptSource != "" {
		if _, err := os.Stat(systemPromptSource); err == nil {
			l.systemPromptSourcePath = utils.ResolvePath(systemPromptSource, "", nil)
		}
	}

	appendSources := l.appendSystemPromptSource
	if appendSources == nil {
		discovered := l.discoverAppendSystemPromptFile()
		if discovered != "" {
			appendSources = []string{discovered}
		} else {
			appendSources = []string{}
		}
	}
	var baseAppend []string
	for _, s := range appendSources {
		if resolved, ok := resolvePromptInput(s, "append system prompt"); ok {
			baseAppend = append(baseAppend, resolved)
		}
	}
	if l.appendSystemPromptOverride != nil {
		l.appendSystemPrompt = l.appendSystemPromptOverride(baseAppend)
	} else {
		l.appendSystemPrompt = baseAppend
	}
	l.appendSystemPromptSourcePaths = []string{}
	for _, source := range appendSources {
		if _, err := os.Stat(source); err == nil {
			l.appendSystemPromptSourcePaths = append(l.appendSystemPromptSourcePaths, utils.ResolvePath(source, "", nil))
		}
	}
	l.loaded = true
	return nil
}

func containsDiagnosticPath(diagnostics []ResourceDiagnostic, path string) bool {
	for _, d := range diagnostics {
		if d.Path == path {
			return true
		}
	}
	return false
}

// loadProjectTrustExtensions forces untrusted project settings for the
// bootstrap pass (pi: loadProjectTrustExtensions). This keeps project-local
// extensions/packages out while still loading user/global and temporary CLI
// extensions.
func (l *DefaultResourceLoader) loadProjectTrustExtensions() (*LoadExtensionsResult, error) {
	l.settingsManager.SetProjectTrusted(false)
	l.settingsManager.Reload()
	return l.loadCurrentExtensionSet()
}

// loadCurrentExtensionSet loads the extension set for the current trust
// state (pi: loadCurrentExtensionSet). Without the extension loader this
// always returns the empty result.
func (l *DefaultResourceLoader) loadCurrentExtensionSet() (*LoadExtensionsResult, error) {
	return &LoadExtensionsResult{
		Extensions: []*Extension{},
		Errors:     []LoadExtensionError{},
		Runtime:    l.extensionsResult.Runtime,
	}, nil
}

// loadFinalExtensionSet assembles the final extensions result (pi:
// loadFinalExtensionSet). The extension loading halves are deferred with the
// extension/sidecar batch (Phase 7): pig cannot load TS extensions yet, so
// the result is always empty and conflict diagnostics cannot occur.
func (l *DefaultResourceLoader) loadFinalExtensionSet(extensionPaths []string) *LoadExtensionsResult {
	return &LoadExtensionsResult{
		Extensions: []*Extension{},
		Errors:     []LoadExtensionError{},
		Runtime:    l.extensionsResult.Runtime,
	}
}

// resolveExtensionSourcesLocalOnly resolves CLI-provided extension sources
// that are local paths (pi: packageManager.resolveExtensionSources with
// { temporary: true }). npm/git sources are deferred with the package-manager
// batch; they produce nothing today.
func (l *DefaultResourceLoader) resolveExtensionSourcesLocalOnly(sources []string) ResolvedPaths {
	paths := ResolvedPaths{}
	for _, source := range sources {
		if !utils.IsLocalPath(source) {
			continue
		}
		resolved := l.resolveResourcePath(source)
		metadata := PathMetadata{Source: "cli", Scope: SourceScopeTemporary, Origin: SourceOriginTopLevel}
		paths.Extensions = append(paths.Extensions, ResolvedResource{Path: resolved, Enabled: true, Metadata: metadata})
	}
	return paths
}

// mapSkillPath maps auto/package skill directories to their SKILL.md (pi:
// mapSkillPath).
func (l *DefaultResourceLoader) mapSkillPath(resource ResolvedResource, metadataByPath resourcePathMetadata) string {
	if resource.Metadata.Source != "auto" && resource.Metadata.Origin != "package" {
		return resource.Path
	}
	stats, err := os.Stat(resource.Path)
	if err != nil || !stats.IsDir() {
		return resource.Path
	}
	skillFile := filepath.Join(resource.Path, "SKILL.md")
	if _, err := os.Stat(skillFile); err == nil {
		if _, ok := metadataByPath[skillFile]; !ok {
			metadataByPath[skillFile] = resource.Metadata
		}
		return skillFile
	}
	return resource.Path
}

// normalizeExtensionPaths resolves entry paths and their base dirs (pi:
// normalizeExtensionPaths). pig's extension metadata arrives as
// PathMetadata.
func (l *DefaultResourceLoader) normalizeExtensionPaths(entries []ResourceExtensionPath) []ResourceExtensionPath {
	out := make([]ResourceExtensionPath, 0, len(entries))
	for _, entry := range entries {
		metadata := entry.Metadata
		if metadata.BaseDir != "" {
			metadata.BaseDir = l.resolveResourcePath(metadata.BaseDir)
		}
		out = append(out, ResourceExtensionPath{
			Path:     l.resolveResourcePath(entry.Path),
			Metadata: metadata,
		})
	}
	return out
}

// updateSkillsFromPaths loads skills for the resolved paths (pi:
// updateSkillsFromPaths).
func (l *DefaultResourceLoader) updateSkillsFromPaths(skillPaths []string, metadataByPath resourcePathMetadata) {
	var skillsResult LoadSkillsResult
	if l.noSkills && len(skillPaths) == 0 {
		skillsResult = LoadSkillsResult{Skills: []Skill{}, Diagnostics: []ResourceDiagnostic{}}
	} else {
		skillsResult = LoadSkills(LoadSkillsOptions{
			Cwd:             l.cwd,
			AgentDir:        l.agentDir,
			SkillPaths:      skillPaths,
			IncludeDefaults: false,
		})
	}
	if l.skillsOverride != nil {
		skillsResult = l.skillsOverride(skillsResult)
	}
	l.skills = make([]Skill, 0, len(skillsResult.Skills))
	for _, skill := range skillsResult.Skills {
		sourceInfo := l.findSourceInfoForPath(skill.FilePath, l.extensionSkillSourceInfos, metadataByPath)
		if sourceInfo == nil {
			if skill.SourceInfo.Path != "" {
				sourceInfo = &skill.SourceInfo
			} else {
				sourceInfo = loPtrSourceInfo(l.getDefaultSourceInfoForPath(skill.FilePath))
			}
		}
		skill.SourceInfo = *sourceInfo
		l.skills = append(l.skills, skill)
	}
	l.skillDiagnostics = skillsResult.Diagnostics
}

// updatePromptsFromPaths loads prompt templates for the resolved paths (pi:
// updatePromptsFromPaths).
func (l *DefaultResourceLoader) updatePromptsFromPaths(promptPaths []string, metadataByPath resourcePathMetadata) {
	var promptsResult PromptLoaderResult
	if l.noPromptTemplates && len(promptPaths) == 0 {
		promptsResult = PromptLoaderResult{Prompts: []PromptTemplate{}, Diagnostics: []ResourceDiagnostic{}}
	} else {
		loaded := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             l.cwd,
			AgentDir:        l.agentDir,
			PromptPaths:     promptPaths,
			IncludeDefaults: false,
		})
		// pi: loading diagnostics come first, then the dedupe collision
		// diagnostics (#9830).
		deduped := l.dedupePrompts(loaded.Templates)
		promptsResult = PromptLoaderResult{
			Prompts:     deduped.Prompts,
			Diagnostics: append(append([]ResourceDiagnostic{}, loaded.Diagnostics...), deduped.Diagnostics...),
		}
	}
	if l.promptsOverride != nil {
		promptsResult = l.promptsOverride(promptsResult)
	}
	l.prompts = make([]PromptTemplate, 0, len(promptsResult.Prompts))
	for _, prompt := range promptsResult.Prompts {
		sourceInfo := l.findSourceInfoForPath(prompt.FilePath, l.extensionPromptSourceInfos, metadataByPath)
		if sourceInfo == nil {
			if prompt.SourceInfo.Path != "" {
				sourceInfo = &prompt.SourceInfo
			} else {
				sourceInfo = loPtrSourceInfo(l.getDefaultSourceInfoForPath(prompt.FilePath))
			}
		}
		prompt.SourceInfo = *sourceInfo
		l.prompts = append(l.prompts, prompt)
	}
	l.promptDiagnostics = promptsResult.Diagnostics
}

// updateThemesFromPaths is deferred with the theme batch (pi:
// updateThemesFromPaths); the structure is kept so the later batch fills in
// the loading half.
func (l *DefaultResourceLoader) updateThemesFromPaths(themePaths []string, metadataByPath resourcePathMetadata) {
	l.themeDiagnostics = []ResourceDiagnostic{}
}

// findSourceInfoForPath resolves the SourceInfo for one resource path (pi:
// findSourceInfoForPath).
func (l *DefaultResourceLoader) findSourceInfoForPath(resourcePath string, extraSourceInfos map[string]SourceInfo, metadataByPath resourcePathMetadata) *SourceInfo {
	if resourcePath == "" {
		return nil
	}

	if strings.HasPrefix(resourcePath, "<") {
		info := l.getDefaultSourceInfoForPath(resourcePath)
		return &info
	}

	normalizedResourcePath := filepath.Clean(resourcePath)
	if extraSourceInfos != nil {
		for sourcePath, sourceInfo := range extraSourceInfos {
			normalizedSourcePath := filepath.Clean(sourcePath)
			if normalizedResourcePath == normalizedSourcePath ||
				strings.HasPrefix(normalizedResourcePath, normalizedSourcePath+string(filepath.Separator)) {
				info := sourceInfo
				info.Path = resourcePath
				return &info
			}
		}
	}

	if metadataByPath != nil {
		exact, ok := metadataByPath[normalizedResourcePath]
		if !ok {
			exact, ok = metadataByPath[resourcePath]
		}
		if ok {
			info := CreateSourceInfo(resourcePath, exact)
			return &info
		}

		for sourcePath, metadata := range metadataByPath {
			normalizedSourcePath := filepath.Clean(sourcePath)
			if normalizedResourcePath == normalizedSourcePath ||
				strings.HasPrefix(normalizedResourcePath, normalizedSourcePath+string(filepath.Separator)) {
				info := CreateSourceInfo(resourcePath, metadata)
				return &info
			}
		}
	}

	return nil
}

// getDefaultSourceInfoForPath derives the default provenance from the file
// location (pi: getDefaultSourceInfoForPath).
func (l *DefaultResourceLoader) getDefaultSourceInfoForPath(filePath string) SourceInfo {
	if strings.HasPrefix(filePath, "<") && strings.HasSuffix(filePath, ">") {
		inner := filePath[1 : len(filePath)-1]
		source := inner
		if idx := strings.Index(inner, ":"); idx >= 0 {
			source = inner[:idx]
		}
		if source == "" {
			source = "temporary"
		}
		return SourceInfo{
			Path:    filePath,
			Source:  source,
			Scope:   SourceScopeTemporary,
			Origin:  SourceOriginTopLevel,
			BaseDir: "",
		}
	}

	normalizedPath := filepath.Clean(filePath)
	agentRoots := []string{
		filepath.Join(l.agentDir, "skills"),
		filepath.Join(l.agentDir, "prompts"),
		filepath.Join(l.agentDir, "themes"),
		filepath.Join(l.agentDir, "extensions"),
	}
	projectRoots := []string{
		filepath.Join(l.cwd, codingagent.ConfigDirName, "skills"),
		filepath.Join(l.cwd, codingagent.ConfigDirName, "prompts"),
		filepath.Join(l.cwd, codingagent.ConfigDirName, "themes"),
		filepath.Join(l.cwd, codingagent.ConfigDirName, "extensions"),
	}

	for _, root := range agentRoots {
		if l.isUnderPath(normalizedPath, root) {
			return SourceInfo{Path: filePath, Source: "local", Scope: SourceScopeUser, Origin: SourceOriginTopLevel, BaseDir: root}
		}
	}

	for _, root := range projectRoots {
		if l.isUnderPath(normalizedPath, root) {
			return SourceInfo{Path: filePath, Source: "local", Scope: SourceScopeProject, Origin: SourceOriginTopLevel, BaseDir: root}
		}
	}

	baseDir := normalizedPath
	if stats, err := os.Stat(normalizedPath); err != nil || !stats.IsDir() {
		baseDir = filepath.Dir(filepath.Clean(normalizedPath))
	}
	return SourceInfo{
		Path:    filePath,
		Source:  "local",
		Scope:   SourceScopeTemporary,
		Origin:  SourceOriginTopLevel,
		BaseDir: baseDir,
	}
}

// mergePaths merges path lists with canonical dedupe (pi: mergePaths).
func (l *DefaultResourceLoader) mergePaths(primary []string, additional []string) []string {
	var merged []string
	seen := map[string]bool{}

	for _, p := range append(append([]string{}, primary...), additional...) {
		resolved := l.resolveResourcePath(p)
		canonicalPath := utils.CanonicalizePath(resolved)
		if seen[canonicalPath] {
			continue
		}
		seen[canonicalPath] = true
		merged = append(merged, resolved)
	}

	return merged
}

// resolveResourcePath resolves one resource path relative to cwd (pi:
// resolveResourcePath).
func (l *DefaultResourceLoader) resolveResourcePath(p string) string {
	trim := true
	return utils.ResolvePath(p, l.cwd, &utils.PathInputOptions{Trim: &trim})
}

// dedupePrompts keeps the first prompt per name (pi: dedupePrompts).
func (l *DefaultResourceLoader) dedupePrompts(prompts []PromptTemplate) PromptLoaderResult {
	seen := map[string]PromptTemplate{}
	var diagnostics []ResourceDiagnostic
	var order []string

	for _, prompt := range prompts {
		if existing, ok := seen[prompt.Name]; ok {
			diagnostics = append(diagnostics, ResourceDiagnostic{
				Type:    "collision",
				Message: fmt.Sprintf("name \"/%s\" collision", prompt.Name),
				Path:    prompt.FilePath,
				Collision: &ResourceCollision{
					ResourceType: "prompt",
					Name:         prompt.Name,
					WinnerPath:   existing.FilePath,
					LoserPath:    prompt.FilePath,
				},
			})
		} else {
			seen[prompt.Name] = prompt
			order = append(order, prompt.Name)
		}
	}

	result := PromptLoaderResult{Prompts: []PromptTemplate{}, Diagnostics: diagnostics}
	for _, name := range order {
		result.Prompts = append(result.Prompts, seen[name])
	}
	return result
}

// discoverSystemPromptFile locates SYSTEM.md (pi: discoverSystemPromptFile).
func (l *DefaultResourceLoader) discoverSystemPromptFile() string {
	projectPath := filepath.Join(l.cwd, codingagent.ConfigDirName, "SYSTEM.md")
	if l.settingsManager.IsProjectTrusted() {
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath
		}
	}

	globalPath := filepath.Join(l.agentDir, "SYSTEM.md")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}

	return ""
}

// discoverAppendSystemPromptFile locates APPEND_SYSTEM.md (pi:
// discoverAppendSystemPromptFile).
func (l *DefaultResourceLoader) discoverAppendSystemPromptFile() string {
	projectPath := filepath.Join(l.cwd, codingagent.ConfigDirName, "APPEND_SYSTEM.md")
	if l.settingsManager.IsProjectTrusted() {
		if _, err := os.Stat(projectPath); err == nil {
			return projectPath
		}
	}

	globalPath := filepath.Join(l.agentDir, "APPEND_SYSTEM.md")
	if _, err := os.Stat(globalPath); err == nil {
		return globalPath
	}

	return ""
}

// isUnderPath reports whether target is root or below it (pi: isUnderPath).
func (l *DefaultResourceLoader) isUnderPath(target string, root string) bool {
	normalizedRoot := filepath.Clean(root)
	if target == normalizedRoot {
		return true
	}
	prefix := normalizedRoot
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(target, prefix)
}

// resolvePromptInput reads a prompt file when the input is a path (pi:
// resolvePromptInput). ok=false = undefined input.
func resolvePromptInput(input string, description string) (string, bool) {
	if input == "" {
		return "", false
	}

	if _, err := os.Stat(input); err == nil {
		data, err := os.ReadFile(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not read %s file %s: %v\n", description, input, err)
			return input, true
		}
		return utils.StripBom(string(data)), true
	}

	return input, true
}

// ResourceLoader interface compile-time check.
var _ ResourceLoader = (*DefaultResourceLoader)(nil)

// loPtrSourceInfo avoids taking the address of a loop variable copy.
func loPtrSourceInfo(info SourceInfo) *SourceInfo { return &info }

// loadContextFileFromDir loads the first context file of dir (pi:
// loadContextFileFromDir). ok=false = none.
func loadContextFileFromDir(dir string) (AgentsFile, bool) {
	candidates := []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"}
	for _, filename := range candidates {
		filePath := filepath.Join(dir, filename)
		if _, err := os.Stat(filePath); err != nil {
			continue
		}
		stats, err := os.Stat(filePath)
		if err != nil || !stats.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not read %s: %v\n", filePath, err)
			continue
		}
		return AgentsFile{Path: filePath, Content: utils.StripBom(string(data))}, true
	}
	return AgentsFile{}, false
}

// GitPaths is the git layout probe result (pi: GitPaths in
// footer-data-provider.ts).
type GitPaths struct {
	RepoDir      string
	CommonGitDir string
	HeadPath     string
}

// findGitPaths walks up from cwd to the nearest .git (pi: findGitPaths in
// footer-data-provider.ts; the worktree shadow logic consumes it).
func findGitPaths(cwd string) *GitPaths {
	dir := cwd
	for {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			stats, err := os.Stat(gitPath)
			if err != nil {
				return nil
			}
			if stats.Mode().IsRegular() {
				content, err := os.ReadFile(gitPath)
				if err != nil {
					return nil
				}
				text := strings.TrimSpace(string(content))
				if strings.HasPrefix(text, "gitdir: ") {
					// pi: resolve(dir, gitdir) — node's resolve treats an
					// absolute second argument as-is; emulate that (worktree
					// fixtures use absolute gitdir paths).
					gitDirRef := strings.TrimSpace(text[len("gitdir: "):])
					gitDir := gitDirRef
					if !filepath.IsAbs(gitDir) {
						gitDir = filepath.Clean(filepath.Join(dir, gitDir))
					}
					headPath := filepath.Join(gitDir, "HEAD")
					if _, err := os.Stat(headPath); err != nil {
						return nil
					}
					commonDirPath := filepath.Join(gitDir, "commondir")
					commonGitDir := gitDir
					if data, err := os.ReadFile(commonDirPath); err == nil {
						commonGitDir = filepath.Clean(filepath.Join(gitDir, strings.TrimSpace(string(data))))
					}
					return &GitPaths{RepoDir: dir, CommonGitDir: commonGitDir, HeadPath: headPath}
				}
			} else if stats.IsDir() {
				headPath := filepath.Join(gitPath, "HEAD")
				if _, err := os.Stat(headPath); err != nil {
					return nil
				}
				return &GitPaths{RepoDir: dir, CommonGitDir: gitPath, HeadPath: headPath}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// findShadowedContextFile returns the main repo's context file that a nested
// linked worktree's own copy shadows (pi: findShadowedContextFile). Both
// occupy the same logical repository scope, so loading both applies that
// context twice. Returns "" when nothing is shadowed. The result is
// canonicalized (realpath), because `git worktree add` writes the `.git`
// file's `gitdir:` target in realpath form while cwd may still be symlinked
// (macOS /tmp -> /private/tmp).
func findShadowedContextFile(cwd string) string {
	gitPaths := findGitPaths(cwd)
	if gitPaths == nil {
		return ""
	}
	commonGitDir := utils.CanonicalizePath(gitPaths.CommonGitDir)
	worktreeRoot := utils.CanonicalizePath(gitPaths.RepoDir)
	mainRepoRoot := filepath.Dir(commonGitDir)
	// False for an ordinary repo, where the two are the same dir, and for a
	// sibling worktree (`git worktree add ../feat`), whose main repo is not
	// an ancestor.
	if !strings.HasPrefix(worktreeRoot, mainRepoRoot+string(filepath.Separator)) {
		return ""
	}
	// dirname of the common git dir is the main worktree root only when that
	// dir is itself checked out from the same repo. In a bare layout
	// (`proj/.bare` + `proj/main`) it is just the directory holding `.bare`,
	// which tracks nothing; a submodule's gitdir has no `commondir`, so it
	// lands under `.git/modules`.
	if utils.CanonicalizePath(filepath.Join(mainRepoRoot, ".git")) != commonGitDir {
		return ""
	}
	worktreeContextFile, ok := loadContextFileFromDir(worktreeRoot)
	if !ok {
		return ""
	}
	return filepath.Join(mainRepoRoot, filepath.Base(worktreeContextFile.Path))
}

// LoadProjectContextFiles loads the AGENTS.md-style context files from the
// agent dir and every ancestor of cwd (pi: loadProjectContextFiles).
func LoadProjectContextFiles(cwd string, agentDir string) []AgentsFile {
	resolvedCwd := utils.ResolvePath(cwd, "", nil)
	resolvedAgentDir := utils.ResolvePath(agentDir, "", nil)

	var contextFiles []AgentsFile
	seenPaths := map[string]bool{}

	globalContext, ok := loadContextFileFromDir(resolvedAgentDir)
	if ok {
		contextFiles = append(contextFiles, globalContext)
		seenPaths[globalContext.Path] = true
	}

	var ancestorContextFiles []AgentsFile

	shadowedContextFile := findShadowedContextFile(resolvedCwd)
	currentDir := resolvedCwd

	for {
		contextFile, ok := loadContextFileFromDir(currentDir)
		// pi: canonicalizePath(contextFile?.path ?? "") — a missing file can
		// never equal the (always real) shadowed path, so skip directly.
		isShadowed := ok && shadowedContextFile != "" &&
			utils.CanonicalizePath(contextFile.Path) == utils.CanonicalizePath(shadowedContextFile)
		if ok && !isShadowed && !seenPaths[contextFile.Path] {
			// pi unshifts — the closest ancestor ends up last, preserving
			// outer-to-inner order.
			ancestorContextFiles = append([]AgentsFile{contextFile}, ancestorContextFiles...)
			seenPaths[contextFile.Path] = true
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}

	contextFiles = append(contextFiles, ancestorContextFiles...)

	return contextFiles
}
