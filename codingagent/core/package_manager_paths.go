// pi: core/package-manager.ts — the settings-driven resource-path resolution
// half only (resolve() minus package sources): local settings entries,
// pattern enable/disable, and auto-discovered resource directories. The
// npm/git package installation machinery (install/update/remove, semver,
// git clone, pi-manifest extension entry discovery) arrives with the
// package-manager/extension batch (Phase 7) and is declared at
// resolvePackageSources (absent here) / resolveExtensionEntries below — pig
// cannot run TS extensions before the sidecar host lands, so extension
// auto-discovery contributes nothing yet. The ignore machinery (pi:
// addIgnoreRules/prefixIgnorePattern/IGNORE_FILE_NAMES) is reused from
// skills.go, which ports the same pi source functions.
package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

// PathMetadata describes one resolved resource path (pi: PathMetadata).
type PathMetadata struct {
	Source string
	Scope  SourceScope
	Origin string // "package" | "top-level"
	// BaseDir is the directory the path was resolved from (pi: baseDir?);
	// "" = none.
	BaseDir string
}

// ResolvedResource is one resolved path with enablement (pi:
// ResolvedResource).
type ResolvedResource struct {
	Path     string
	Enabled  bool
	Metadata PathMetadata
}

// ResolvedPaths groups the four resource kinds (pi: ResolvedPaths).
type ResolvedPaths struct {
	Extensions []ResolvedResource
	Skills     []ResolvedResource
	Prompts    []ResolvedResource
	Themes     []ResolvedResource
}

// ResourceType is one of the four resource kinds (pi: ResourceType).
type ResourceType = string

const (
	ResourceExtensions ResourceType = "extensions"
	ResourceSkills     ResourceType = "skills"
	ResourcePrompts    ResourceType = "prompts"
	ResourceThemes     ResourceType = "themes"
)

// resourceTypes is pi's RESOURCE_TYPES order.
var resourceTypes = []ResourceType{ResourceExtensions, ResourceSkills, ResourcePrompts, ResourceThemes}

// filePatterns matches pi's FILE_PATTERNS regexes as predicate functions.
var filePatterns = map[ResourceType]func(name string) bool{
	ResourceExtensions: func(name string) bool { return strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".js") },
	ResourceSkills:     func(name string) bool { return strings.HasSuffix(name, ".md") },
	ResourcePrompts:    func(name string) bool { return strings.HasSuffix(name, ".md") },
	ResourceThemes:     func(name string) bool { return strings.HasSuffix(name, ".json") },
}

// resourcePrecedenceRank computes the precedence rank used for collision
// resolution ("first wins") (pi: resourcePrecedenceRank). Lower = higher
// precedence: 0 project+settings, 1 project+auto, 2 user+settings,
// 3 user+auto, 4 package resource.
func resourcePrecedenceRank(m PathMetadata) int {
	if m.Origin == "package" {
		return 4
	}
	scopeBase := 2
	if m.Scope == SourceScopeProject {
		scopeBase = 0
	}
	if m.Source == "local" {
		return scopeBase
	}
	return scopeBase + 1
}

func toPosixPath(p string) string {
	return filepath.ToSlash(p)
}

func getHomeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// newIgnoreMatcher loads the ignore files of dir relative to rootDir (pi:
// addIgnoreRules). Reuses the skills.go matcher (same pi source functions).
func newIgnoreMatcher(dir string, rootDir string) *skillIgnoreMatcher {
	ig := &skillIgnoreMatcher{}
	addIgnoreRules(ig, dir, rootDir)
	return ig
}

func hasGlobPattern(s string) bool {
	return strings.Contains(s, "*") || strings.Contains(s, "?")
}

func isPattern(s string) bool {
	return strings.HasPrefix(s, "!") || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-")
}

// splitPatterns splits settings entries into plain paths and override
// patterns (pi: splitPatterns).
func splitPatterns(entries []string) (plain []string, patterns []string) {
	for _, entry := range entries {
		if isPattern(entry) {
			patterns = append(patterns, entry)
		} else {
			plain = append(plain, entry)
		}
	}
	return plain, patterns
}

// collectFiles walks dir collecting files matching filePattern (pi:
// collectFiles). filePattern is the FILE_PATTERNS regex as a predicate.
func collectFiles(dir string, filePattern func(string) bool, skipNodeModules bool, ig *skillIgnoreMatcher, rootDir string) []string {
	var files []string
	if _, err := os.Stat(dir); err != nil {
		return files
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	if ig == nil {
		ig = newIgnoreMatcher(dir, root)
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if skipNodeModules && entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		isFile := entry.Type().IsRegular()

		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stats.IsDir()
			isFile = stats.Mode().IsRegular()
		}

		relPath := toPosixPath(relPath(root, fullPath))
		ignorePath := relPath
		if isDir {
			ignorePath = relPath + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		if isDir {
			files = append(files, collectFiles(fullPath, filePattern, skipNodeModules, ig, root)...)
		} else if isFile && filePattern(entry.Name()) {
			files = append(files, fullPath)
		}
	}
	return files
}

func relPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

// SkillDiscoveryMode distinguishes the two skill layouts (pi:
// SkillDiscoveryMode).
type SkillDiscoveryMode = string

const (
	SkillModePi     SkillDiscoveryMode = "pi"
	SkillModeAgents SkillDiscoveryMode = "agents"
)

// collectSkillEntries discovers SKILL.md files under dir (pi:
// collectSkillEntries). "pi" mode only counts root-level SKILL.md files;
// "agents" mode only counts nested ones.
func collectSkillEntries(dir string, mode SkillDiscoveryMode, ig *skillIgnoreMatcher, rootDir string) []string {
	var entries []string
	if _, err := os.Stat(dir); err != nil {
		return entries
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	if ig == nil {
		ig = newIgnoreMatcher(dir, root)
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}

	for _, dirEntry := range dirEntries {
		if dirEntry.Name() != "SKILL.md" {
			continue
		}

		fullPath := filepath.Join(dir, dirEntry.Name())
		isFile := dirEntry.Type().IsRegular()
		if dirEntry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = stats.Mode().IsRegular()
		}

		rel := toPosixPath(relPath(root, fullPath))
		if isFile && !ig.ignores(rel) {
			entries = append(entries, fullPath)
			return entries
		}
	}

	for _, dirEntry := range dirEntries {
		if strings.HasPrefix(dirEntry.Name(), ".") || dirEntry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, dirEntry.Name())
		isDir := dirEntry.IsDir()
		isFile := dirEntry.Type().IsRegular()

		if dirEntry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stats.IsDir()
			isFile = stats.Mode().IsRegular()
		}

		rel := toPosixPath(relPath(root, fullPath))
		ignorePath := rel
		if isDir {
			ignorePath = rel + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		shouldIncludeMarkdownFile := isFile &&
			strings.HasSuffix(dirEntry.Name(), ".md") &&
			((mode == SkillModePi && dir == root) || (mode == SkillModeAgents && dir != root))
		if shouldIncludeMarkdownFile {
			entries = append(entries, fullPath)
			continue
		}

		if !isDir {
			continue
		}

		entries = append(entries, collectSkillEntries(fullPath, mode, ig, root)...)
	}

	return entries
}

func collectAutoSkillEntries(dir string, mode SkillDiscoveryMode) []string {
	return collectSkillEntries(dir, mode, nil, "")
}

func findGitRepoRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		dir = startDir
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// collectAncestorAgentsSkillDirs lists the .agents/skills dirs from startDir
// up to the git repo root inclusive (pi: collectAncestorAgentsSkillDirs).
func collectAncestorAgentsSkillDirs(startDir string) []string {
	var skillDirs []string
	resolvedStartDir, err := filepath.Abs(startDir)
	if err != nil {
		resolvedStartDir = startDir
	}
	gitRepoRoot := findGitRepoRoot(resolvedStartDir)

	dir := resolvedStartDir
	for {
		skillDirs = append(skillDirs, filepath.Join(dir, ".agents", "skills"))
		if gitRepoRoot != "" && dir == gitRepoRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return skillDirs
}

// collectAutoPromptEntries lists root-level .md files of dir (pi:
// collectAutoPromptEntries).
func collectAutoPromptEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); err != nil {
		return entries
	}

	ig := newIgnoreMatcher(dir, dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = stats.Mode().IsRegular()
		}

		rel := toPosixPath(relPath(dir, fullPath))
		if ig.ignores(rel) {
			continue
		}

		if isFile && strings.HasSuffix(entry.Name(), ".md") {
			entries = append(entries, fullPath)
		}
	}
	return entries
}

// collectAutoThemeEntries lists root-level .json files of dir (pi:
// collectAutoThemeEntries).
func collectAutoThemeEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); err != nil {
		return entries
	}

	ig := newIgnoreMatcher(dir, dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isFile = stats.Mode().IsRegular()
		}

		rel := toPosixPath(relPath(dir, fullPath))
		if ig.ignores(rel) {
			continue
		}

		if isFile && strings.HasSuffix(entry.Name(), ".json") {
			entries = append(entries, fullPath)
		}
	}
	return entries
}

// resolveExtensionEntries is deferred with the extension batch (pi:
// resolveExtensionEntries reads package.json pi manifests and index.ts/js
// entry files; pig's sidecar host arrives in Phase 7). Declared at the file
// head.
func resolveExtensionEntries(dir string) []string {
	return nil
}

// collectAutoExtensionEntries discovers extension entry files under dir (pi:
// collectAutoExtensionEntries). Entry resolution is deferred with the
// extension batch (see resolveExtensionEntries), so the scan contributes
// nothing yet; the structure is kept for the later batch to fill in.
func collectAutoExtensionEntries(dir string) []string {
	var entries []string
	if _, err := os.Stat(dir); err != nil {
		return entries
	}

	ig := newIgnoreMatcher(dir, dir)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return entries
	}
	for _, entry := range dirEntries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		isFile := entry.Type().IsRegular()

		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir = stats.IsDir()
			isFile = stats.Mode().IsRegular()
		}

		rel := toPosixPath(relPath(dir, fullPath))
		ignorePath := rel
		if isDir {
			ignorePath = rel + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		if isFile && (strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".js")) {
			entries = append(entries, fullPath)
		} else if isDir {
			resolvedEntries := resolveExtensionEntries(fullPath)
			entries = append(entries, resolvedEntries...)
		}
	}
	return entries
}

// matchesAnyPattern applies glob patterns against a path, its name, and —
// for SKILL.md files — the parent directory (pi: matchesAnyPattern).
func matchesAnyPattern(filePath string, patterns []string, baseDir string) bool {
	rel := toPosixPath(relPath(baseDir, filePath))
	name := filepath.Base(filePath)
	filePathPosix := toPosixPath(filePath)
	isSkillFile := name == "SKILL.md"
	parentRel := ""
	parentName := ""
	parentDirPosix := ""
	if isSkillFile {
		parentDir := filepath.Dir(filePath)
		parentRel = toPosixPath(relPath(baseDir, parentDir))
		parentName = filepath.Base(parentDir)
		parentDirPosix = toPosixPath(parentDir)
	}

	for _, pattern := range patterns {
		normalizedPattern := toPosixPath(utils.NormalizePath(pattern, nil))
		if utils.Minimatch(normalizedPattern, rel) ||
			utils.Minimatch(normalizedPattern, name) ||
			utils.Minimatch(normalizedPattern, filePathPosix) {
			return true
		}
		if !isSkillFile {
			return false
		}
		if utils.Minimatch(normalizedPattern, parentRel) ||
			utils.Minimatch(normalizedPattern, parentName) ||
			utils.Minimatch(normalizedPattern, parentDirPosix) {
			return true
		}
	}
	return false
}

func normalizeExactPattern(pattern string) string {
	normalized := pattern
	if strings.HasPrefix(normalized, "./") || strings.HasPrefix(normalized, ".\\") {
		normalized = normalized[2:]
	}
	return toPosixPath(utils.NormalizePath(normalized, nil))
}

func matchesAnyExactPattern(filePath string, patterns []string, baseDir string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel := toPosixPath(relPath(baseDir, filePath))
	name := filepath.Base(filePath)
	filePathPosix := toPosixPath(filePath)
	isSkillFile := name == "SKILL.md"
	parentRel := ""
	parentDirPosix := ""
	if isSkillFile {
		parentDir := filepath.Dir(filePath)
		parentRel = toPosixPath(relPath(baseDir, parentDir))
		parentDirPosix = toPosixPath(parentDir)
	}

	for _, pattern := range patterns {
		normalized := normalizeExactPattern(pattern)
		if normalized == rel || normalized == filePathPosix {
			return true
		}
		if !isSkillFile {
			return false
		}
		if normalized == parentRel || normalized == parentDirPosix {
			return true
		}
	}
	return false
}

func getOverridePatterns(entries []string) []string {
	var overrides []string
	for _, pattern := range entries {
		if strings.HasPrefix(pattern, "!") || strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-") {
			overrides = append(overrides, pattern)
		}
	}
	return overrides
}

// isEnabledByOverrides evaluates the !/+/- override patterns for one path
// (pi: isEnabledByOverrides).
func isEnabledByOverrides(filePath string, patterns []string, baseDir string) bool {
	overrides := getOverridePatterns(patterns)
	var excludes, forceIncludes, forceExcludes []string
	for _, pattern := range overrides {
		switch {
		case strings.HasPrefix(pattern, "!"):
			excludes = append(excludes, pattern[1:])
		case strings.HasPrefix(pattern, "+"):
			forceIncludes = append(forceIncludes, pattern[1:])
		case strings.HasPrefix(pattern, "-"):
			forceExcludes = append(forceExcludes, pattern[1:])
		}
	}

	enabled := true
	if len(excludes) > 0 && matchesAnyPattern(filePath, excludes, baseDir) {
		enabled = false
	}
	if len(forceIncludes) > 0 && matchesAnyExactPattern(filePath, forceIncludes, baseDir) {
		enabled = true
	}
	if len(forceExcludes) > 0 && matchesAnyExactPattern(filePath, forceExcludes, baseDir) {
		enabled = false
	}
	return enabled
}

// applyPatterns applies include/exclude/force patterns to the collected
// paths and returns the enabled set (pi: applyPatterns).
func applyPatterns(allPaths []string, patterns []string, baseDir string) map[string]bool {
	var includes, excludes, forceIncludes, forceExcludes []string

	for _, p := range patterns {
		switch {
		case strings.HasPrefix(p, "+"):
			forceIncludes = append(forceIncludes, p[1:])
		case strings.HasPrefix(p, "-"):
			forceExcludes = append(forceExcludes, p[1:])
		case strings.HasPrefix(p, "!"):
			excludes = append(excludes, p[1:])
		default:
			includes = append(includes, p)
		}
	}

	// Step 1: Apply includes (or all if no includes).
	var result []string
	if len(includes) == 0 {
		result = append([]string{}, allPaths...)
	} else {
		for _, filePath := range allPaths {
			if matchesAnyPattern(filePath, includes, baseDir) {
				result = append(result, filePath)
			}
		}
	}

	// Step 2: Apply excludes.
	if len(excludes) > 0 {
		var filtered []string
		for _, filePath := range result {
			if !matchesAnyPattern(filePath, excludes, baseDir) {
				filtered = append(filtered, filePath)
			}
		}
		result = filtered
	}

	// Step 3: Force-include (add back from allPaths, overriding exclusions).
	if len(forceIncludes) > 0 {
		for _, filePath := range allPaths {
			if !containsString(result, filePath) && matchesAnyExactPattern(filePath, forceIncludes, baseDir) {
				result = append(result, filePath)
			}
		}
	}

	// Step 4: Force-exclude (remove even if included or force-included).
	if len(forceExcludes) > 0 {
		var filtered []string
		for _, filePath := range result {
			if !matchesAnyExactPattern(filePath, forceExcludes, baseDir) {
				filtered = append(filtered, filePath)
			}
		}
		result = filtered
	}

	enabled := make(map[string]bool, len(result))
	for _, path := range result {
		enabled[path] = true
	}
	return enabled
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// resourceAccumulator collects per-kind paths (pi: ResourceAccumulator —
// Maps become maps keyed by path, preserving pi's first-add-wins).
type resourceAccumulator struct {
	extensions map[string]resourceAccumulatorEntry
	skills     map[string]resourceAccumulatorEntry
	prompts    map[string]resourceAccumulatorEntry
	themes     map[string]resourceAccumulatorEntry
}

type resourceAccumulatorEntry struct {
	metadata PathMetadata
	enabled  bool
}

func newResourceAccumulator() *resourceAccumulator {
	return &resourceAccumulator{
		extensions: map[string]resourceAccumulatorEntry{},
		skills:     map[string]resourceAccumulatorEntry{},
		prompts:    map[string]resourceAccumulatorEntry{},
		themes:     map[string]resourceAccumulatorEntry{},
	}
}

func (a *resourceAccumulator) add(resourceType ResourceType, path string, metadata PathMetadata, enabled bool) {
	var target map[string]resourceAccumulatorEntry
	switch resourceType {
	case ResourceExtensions:
		target = a.extensions
	case ResourceSkills:
		target = a.skills
	case ResourcePrompts:
		target = a.prompts
	default:
		target = a.themes
	}
	if _, ok := target[path]; ok {
		return
	}
	target[path] = resourceAccumulatorEntry{metadata: metadata, enabled: enabled}
}

// collectResourceFiles collects resource files from one settings entry (pi:
// collectResourceFiles).
func collectResourceFiles(dir string, resourceType ResourceType) []string {
	if resourceType == ResourceSkills {
		return collectSkillEntries(dir, SkillModePi, nil, "")
	}
	if resourceType == ResourceExtensions {
		return collectAutoExtensionEntries(dir)
	}
	return collectFiles(dir, filePatterns[resourceType], true, nil, "")
}

func mustSlice(v []string, ok bool) []string {
	if !ok {
		return nil
	}
	return v
}

// ResolveSettingsResourcePaths resolves resource paths from settings local
// entries plus auto-discovery, without package sources (pi: resolve() with
// the resolvePackageSources half deferred to the package-manager batch — no
// configured packages can resolve until npm/git installation lands).
func ResolveSettingsResourcePaths(settingsManager *SettingsManager, cwd string, agentDir string) ResolvedPaths {
	accumulator := newResourceAccumulator()
	globalSettings := settingsManager.GetGlobalSettings()
	projectSettings := settingsManager.GetProjectSettings()

	globalBaseDir := agentDir
	projectBaseDir := filepath.Join(cwd, codingagent.ConfigDirName)

	for _, resourceType := range resourceTypes {
		projectEntries, _ := settingsStringSlice(projectSettings, resourceType)
		globalEntries, _ := settingsStringSlice(globalSettings, resourceType)
		resolveLocalEntries(projectEntries, resourceType, accumulator, PathMetadata{
			Source: "local",
			Scope:  SourceScopeProject,
			Origin: "top-level",
		}, projectBaseDir)
		resolveLocalEntries(globalEntries, resourceType, accumulator, PathMetadata{
			Source: "local",
			Scope:  SourceScopeUser,
			Origin: "top-level",
		}, globalBaseDir)
	}

	addAutoDiscoveredResources(accumulator, settingsManager, globalSettings, projectSettings, cwd, globalBaseDir, projectBaseDir)

	return toResolvedPaths(accumulator)
}

// resolveLocalEntries expands one settings string list into concrete files
// (pi: resolveLocalEntries).
func resolveLocalEntries(entries []string, resourceType ResourceType, accumulator *resourceAccumulator, metadata PathMetadata, baseDir string) {
	if len(entries) == 0 {
		return
	}

	plain, patterns := splitPatterns(entries)
	resolvedPlain := make([]string, 0, len(plain))
	for _, p := range plain {
		resolvedPlain = append(resolvedPlain, resolvePathFromBase(p, baseDir))
	}

	var allFiles []string
	for _, resolved := range resolvedPlain {
		allFiles = append(allFiles, collectResourceFiles(resolved, resourceType)...)
	}

	enabledPaths := applyPatterns(allFiles, patterns, baseDir)

	for _, f := range allFiles {
		accumulator.add(resourceType, f, metadata, enabledPaths[f])
	}
}

// resolvePathFromBase resolves one settings entry relative to its base dir
// (pi: resolvePathFromBase).
func resolvePathFromBase(entry string, baseDir string) string {
	if filepath.IsAbs(entry) {
		return filepath.Clean(entry)
	}
	return filepath.Join(baseDir, entry)
}

// addAutoDiscoveredResources adds the conventional resource directories (pi:
// addAutoDiscoveredResources).
func addAutoDiscoveredResources(
	accumulator *resourceAccumulator,
	settingsManager *SettingsManager,
	globalSettings Settings,
	projectSettings Settings,
	cwd string,
	globalBaseDir string,
	projectBaseDir string,
) {
	userMetadata := PathMetadata{Source: "auto", Scope: SourceScopeUser, Origin: "top-level", BaseDir: globalBaseDir}
	projectMetadata := PathMetadata{Source: "auto", Scope: SourceScopeProject, Origin: "top-level", BaseDir: projectBaseDir}

	userOverrides := map[ResourceType][]string{
		ResourceExtensions: mustSlice(settingsStringSlice(globalSettings, "extensions")),
		ResourceSkills:     mustSlice(settingsStringSlice(globalSettings, "skills")),
		ResourcePrompts:    mustSlice(settingsStringSlice(globalSettings, "prompts")),
		ResourceThemes:     mustSlice(settingsStringSlice(globalSettings, "themes")),
	}
	projectOverrides := map[ResourceType][]string{
		ResourceExtensions: mustSlice(settingsStringSlice(projectSettings, "extensions")),
		ResourceSkills:     mustSlice(settingsStringSlice(projectSettings, "skills")),
		ResourcePrompts:    mustSlice(settingsStringSlice(projectSettings, "prompts")),
		ResourceThemes:     mustSlice(settingsStringSlice(projectSettings, "themes")),
	}

	userDirs := map[ResourceType]string{
		ResourceExtensions: filepath.Join(globalBaseDir, "extensions"),
		ResourceSkills:     filepath.Join(globalBaseDir, "skills"),
		ResourcePrompts:    filepath.Join(globalBaseDir, "prompts"),
		ResourceThemes:     filepath.Join(globalBaseDir, "themes"),
	}
	projectDirs := map[ResourceType]string{
		ResourceExtensions: filepath.Join(projectBaseDir, "extensions"),
		ResourceSkills:     filepath.Join(projectBaseDir, "skills"),
		ResourcePrompts:    filepath.Join(projectBaseDir, "prompts"),
		ResourceThemes:     filepath.Join(projectBaseDir, "themes"),
	}
	userAgentsSkillsDir := filepath.Join(getHomeDir(), ".agents", "skills")
	projectTrusted := settingsManager.IsProjectTrusted()
	var projectAgentsSkillDirs []string
	if projectTrusted {
		for _, dir := range collectAncestorAgentsSkillDirs(cwd) {
			if filepath.Clean(dir) != filepath.Clean(userAgentsSkillsDir) {
				projectAgentsSkillDirs = append(projectAgentsSkillDirs, dir)
			}
		}
	}

	addResources := func(resourceType ResourceType, paths []string, metadata PathMetadata, overrides []string, baseDir string) {
		for _, path := range paths {
			enabled := isEnabledByOverrides(path, overrides, baseDir)
			accumulator.add(resourceType, path, metadata, enabled)
		}
	}

	if projectTrusted {
		// Project extensions from .pi/
		addResources(ResourceExtensions, collectAutoExtensionEntries(projectDirs[ResourceExtensions]), projectMetadata, projectOverrides[ResourceExtensions], projectBaseDir)

		// Project skills from .pi/
		addResources(ResourceSkills, collectAutoSkillEntries(projectDirs[ResourceSkills], SkillModePi), projectMetadata, projectOverrides[ResourceSkills], projectBaseDir)
	}

	// Project skills from .agents/ (each with its own baseDir)
	for _, agentsSkillsDir := range projectAgentsSkillDirs {
		agentsBaseDir := filepath.Dir(agentsSkillsDir) // the .agents directory
		agentsMetadata := PathMetadata{
			Source:  projectMetadata.Source,
			Scope:   projectMetadata.Scope,
			Origin:  projectMetadata.Origin,
			BaseDir: agentsBaseDir,
		}
		addResources(ResourceSkills, collectAutoSkillEntries(agentsSkillsDir, SkillModeAgents), agentsMetadata, projectOverrides[ResourceSkills], agentsBaseDir)
	}

	if projectTrusted {
		addResources(ResourcePrompts, collectAutoPromptEntries(projectDirs[ResourcePrompts]), projectMetadata, projectOverrides[ResourcePrompts], projectBaseDir)
		addResources(ResourceThemes, collectAutoThemeEntries(projectDirs[ResourceThemes]), projectMetadata, projectOverrides[ResourceThemes], projectBaseDir)
	}

	// User extensions from ~/.pi/agent/
	addResources(ResourceExtensions, collectAutoExtensionEntries(userDirs[ResourceExtensions]), userMetadata, userOverrides[ResourceExtensions], globalBaseDir)

	// User skills from ~/.pi/agent/
	addResources(ResourceSkills, collectAutoSkillEntries(userDirs[ResourceSkills], SkillModePi), userMetadata, userOverrides[ResourceSkills], globalBaseDir)

	// User skills from ~/.agents/ (with its own baseDir)
	userAgentsBaseDir := filepath.Dir(userAgentsSkillsDir)
	userAgentsMetadata := PathMetadata{
		Source:  userMetadata.Source,
		Scope:   userMetadata.Scope,
		Origin:  userMetadata.Origin,
		BaseDir: userAgentsBaseDir,
	}
	addResources(ResourceSkills, collectAutoSkillEntries(userAgentsSkillsDir, SkillModeAgents), userAgentsMetadata, userOverrides[ResourceSkills], userAgentsBaseDir)

	addResources(ResourcePrompts, collectAutoPromptEntries(userDirs[ResourcePrompts]), userMetadata, userOverrides[ResourcePrompts], globalBaseDir)
	addResources(ResourceThemes, collectAutoThemeEntries(userDirs[ResourceThemes]), userMetadata, userOverrides[ResourceThemes], globalBaseDir)
}

// toResolvedPaths flattens the accumulator with precedence sorting and
// canonical dedupe (pi: toResolvedPaths).
func toResolvedPaths(accumulator *resourceAccumulator) ResolvedPaths {
	mapToResolved := func(entries map[string]resourceAccumulatorEntry) []ResolvedResource {
		resolved := make([]ResolvedResource, 0, len(entries))
		for path, entry := range entries {
			resolved = append(resolved, ResolvedResource{Path: path, Enabled: entry.enabled, Metadata: entry.metadata})
		}
		sort.SliceStable(resolved, func(i, j int) bool {
			return resourcePrecedenceRank(resolved[i].Metadata) < resourcePrecedenceRank(resolved[j].Metadata)
		})

		seen := map[string]bool{}
		out := make([]ResolvedResource, 0, len(resolved))
		for _, entry := range resolved {
			canonicalPath := utils.CanonicalizePath(entry.Path)
			if seen[canonicalPath] {
				continue
			}
			seen[canonicalPath] = true
			out = append(out, entry)
		}
		return out
	}

	return ResolvedPaths{
		Extensions: mapToResolved(accumulator.extensions),
		Skills:     mapToResolved(accumulator.skills),
		Prompts:    mapToResolved(accumulator.prompts),
		Themes:     mapToResolved(accumulator.themes),
	}
}

// EnabledPaths extracts the enabled paths of one resource list (pi's
// getEnabledPaths helper in resource-loader.ts).
func EnabledPaths(resources []ResolvedResource) []string {
	return lo.Map(lo.Filter(resources, func(r ResolvedResource, _ int) bool { return r.Enabled }), func(r ResolvedResource, _ int) string {
		return r.Path
	})
}
