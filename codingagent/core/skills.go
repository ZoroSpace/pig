// pi: src/core/skills.ts — coding-agent skill loading (discovery rules,
// Agent Skills spec validation, collision handling) and prompt formatting.
//
// This is coding-agent's own skills loader (pi's core/skills.ts); the agent
// package has a separate harness copy, and pig mirrors that duplication.
//
// Segment deviations:
//   - npm `ignore` → github.com/sabhiram/go-gitignore (harness precedent;
//     edge-case pattern differences beyond pi's locked tests are accepted).
//   - readdirSync/readdir entries → os.ReadDir + os.Stat for symlink
//     resolution; read errors inside the scan are swallowed like pi's
//     try/catch.
//   - description truncation for prompts uses rune counts (TS UTF-16 code
//     units; established precedent).
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	gitignore "github.com/sabhiram/go-gitignore"
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// MaxNameLength is the max name length per spec (pi: MAX_NAME_LENGTH).
const MaxNameLength = 64

// MaxDescriptionLength is the max description length per spec (pi:
// MAX_DESCRIPTION_LENGTH).
const MaxDescriptionLength = 1024

// ignoreFileNames are the ignore files consulted during skill discovery (pi:
// IGNORE_FILE_NAMES).
var ignoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// skillIgnoreMatcher accumulates gitignore patterns and tests relative
// paths (pi: IgnoreMatcher = ReturnType<typeof ignore>).
type skillIgnoreMatcher struct {
	patterns []string
	compiled *gitignore.GitIgnore
}

// add appends patterns and recompiles (pi: ig.add(patterns)).
func (m *skillIgnoreMatcher) add(patterns []string) {
	m.patterns = append(m.patterns, patterns...)
	m.compiled = gitignore.CompileIgnoreLines(m.patterns...)
}

// ignores reports whether a relative path is ignored (pi:
// ig.ignores(path)). With no patterns, nothing is ignored.
func (m *skillIgnoreMatcher) ignores(path string) bool {
	if m.compiled == nil {
		return false
	}
	return m.compiled.MatchesPath(path)
}

// prefixIgnorePattern roots a pattern at its directory (pi:
// prefixIgnorePattern); nil = drop the line.
func prefixIgnorePattern(line string, prefix string) *string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "\\#") {
		return nil
	}

	pattern := line
	negated := false

	if strings.HasPrefix(pattern, "!") {
		negated = true
		pattern = pattern[1:]
	} else if strings.HasPrefix(pattern, "\\!") {
		pattern = pattern[1:]
	}

	if strings.HasPrefix(pattern, "/") {
		pattern = pattern[1:]
	}

	prefixed := pattern
	if prefix != "" {
		prefixed = prefix + pattern
	}
	if negated {
		prefixed = "!" + prefixed
	}
	return &prefixed
}

// addIgnoreRules loads the directory's ignore files into the matcher (pi:
// addIgnoreRules).
func addIgnoreRules(ig *skillIgnoreMatcher, dir string, rootDir string) {
	relativeDir, err := filepath.Rel(rootDir, dir)
	if err != nil {
		relativeDir = dir
	}
	prefix := ""
	if relativeDir != "" && relativeDir != "." {
		prefix = filepath.ToSlash(relativeDir) + "/"
	}

	for _, filename := range ignoreFileNames {
		ignorePath := filepath.Join(dir, filename)
		content, err := os.ReadFile(ignorePath)
		if err != nil {
			continue
		}
		var patterns []string
		for _, line := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
			if prefixed := prefixIgnorePattern(line, prefix); prefixed != nil {
				patterns = append(patterns, *prefixed)
			}
		}
		if len(patterns) > 0 {
			ig.add(patterns)
		}
	}
}

// SkillFrontmatter is the parsed skill frontmatter shape (pi:
// SkillFrontmatter; unknown fields pass through the map).
type SkillFrontmatter struct {
	Name                      string
	Description               string
	DisableModelInvocation    bool
	DisableModelInvocationSet bool
}

// Skill is one loaded skill (pi: Skill).
type Skill struct {
	Name        string
	Description string
	FilePath    string
	BaseDir     string
	SourceInfo  SourceInfo
	// DisableModelInvocation hides the skill from the system prompt (it can
	// only be invoked explicitly via /skill:name commands).
	DisableModelInvocation bool
}

// LoadSkillsResult bundles loaded skills and diagnostics (pi:
// LoadSkillsResult).
type LoadSkillsResult struct {
	Skills      []Skill
	Diagnostics []ResourceDiagnostic
}

// skillNameRe matches valid skill names (pi: /^[a-z0-9-]+$/).
var skillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validateSkillName validates the skill name per Agent Skills spec (pi:
// validateName; unexported in pi).
func validateSkillName(name string) []string {
	var errors []string

	// TS `.length` counts UTF-16 code units; Go rune count is the established
	// equivalent (byte len would wrongly flag CJK descriptions).
	if utf8.RuneCountInString(name) > MaxNameLength {
		errors = append(errors, fmt.Sprintf("name exceeds %d characters (%d)", MaxNameLength, utf8.RuneCountInString(name)))
	}

	if !skillNameRe.MatchString(name) {
		errors = append(errors, "name contains invalid characters (must be lowercase a-z, 0-9, hyphens only)")
	}

	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		errors = append(errors, "name must not start or end with a hyphen")
	}

	if strings.Contains(name, "--") {
		errors = append(errors, "name must not contain consecutive hyphens")
	}

	return errors
}

// validateSkillDescription validates the description per Agent Skills spec
// (pi: validateDescription; unexported in pi).
func validateSkillDescription(description any) []string {
	var errors []string

	text, ok := description.(string)
	if !ok || strings.TrimSpace(text) == "" {
		errors = append(errors, "description is required")
	} else if utf8.RuneCountInString(text) > MaxDescriptionLength {
		errors = append(errors, fmt.Sprintf("description exceeds %d characters (%d)", MaxDescriptionLength, utf8.RuneCountInString(text)))
	}

	return errors
}

// LoadSkillsFromDirOptions carries loadSkillsFromDir options (pi:
// LoadSkillsFromDirOptions).
type LoadSkillsFromDirOptions struct {
	// Dir is the directory to scan for skills.
	Dir string
	// Source is the source identifier for these skills.
	Source string
}

// createSkillSourceInfo builds the SourceInfo for one skill (pi:
// createSkillSourceInfo).
func createSkillSourceInfo(filePath string, baseDir string, source string) SourceInfo {
	switch source {
	case "user":
		return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{
			Source:  "local",
			Scope:   SourceScopeUser,
			BaseDir: baseDir,
		})
	case "project":
		return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{
			Source:  "local",
			Scope:   SourceScopeProject,
			BaseDir: baseDir,
		})
	case "path":
		return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{
			Source:  "local",
			BaseDir: baseDir,
		})
	default:
		return CreateSyntheticSourceInfo(filePath, SyntheticSourceInfoOptions{
			Source:  source,
			BaseDir: baseDir,
		})
	}
}

// LoadSkillsFromDir loads skills from a directory (pi: loadSkillsFromDir).
//
// Discovery rules:
//   - if a directory contains SKILL.md, treat it as a skill root and do not
//     recurse further
//   - otherwise, load direct .md children in the root
//   - recurse into subdirectories to find SKILL.md
func LoadSkillsFromDir(options LoadSkillsFromDirOptions) LoadSkillsResult {
	return loadSkillsFromDirInternal(options.Dir, options.Source, true, nil, "")
}

// skillFileResult is loadSkillFromFile's result (pi: inline object).
type skillFileResult struct {
	skill       *Skill
	diagnostics []ResourceDiagnostic
}

// loadSkillsFromDirInternal is the recursive scan (pi:
// loadSkillsFromDirInternal). rootDir "" = dir.
func loadSkillsFromDirInternal(dir string, source string, includeRootFiles bool, ignoreMatcher *skillIgnoreMatcher, rootDir string) LoadSkillsResult {
	var skills []Skill
	var diagnostics []ResourceDiagnostic

	if _, err := os.Stat(dir); err != nil {
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	root := rootDir
	if root == "" {
		root = dir
	}
	ig := ignoreMatcher
	if ig == nil {
		ig = &skillIgnoreMatcher{}
	}
	addIgnoreRules(ig, dir, root)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	// First pass: a SKILL.md makes this directory a skill root (pi's first
	// for loop; load and return).
	for _, entry := range entries {
		if entry.Name() != "SKILL.md" {
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

		relPath, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		if !isFile || ig.ignores(relPath) {
			continue
		}

		result := loadSkillFromFile(fullPath, source)
		if result.skill != nil {
			skills = append(skills, *result.skill)
		}
		diagnostics = append(diagnostics, result.diagnostics...)
		return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
	}

	// Second pass: recurse into subdirectories, load root .md files when
	// allowed.
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		// Skip node_modules to avoid scanning dependencies
		if entry.Name() == "node_modules" {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())

		// For symlinks, check if they point to a directory and follow them
		isDirectory := entry.IsDir()
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				// Broken symlink, skip it
				continue
			}
			isDirectory = stats.IsDir()
			isFile = stats.Mode().IsRegular()
		}

		relPath, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		ignorePath := relPath
		if isDirectory {
			ignorePath = relPath + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		if isDirectory {
			subResult := loadSkillsFromDirInternal(fullPath, source, false, ig, root)
			skills = append(skills, subResult.Skills...)
			diagnostics = append(diagnostics, subResult.Diagnostics...)
			continue
		}

		if !isFile || !includeRootFiles || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		result := loadSkillFromFile(fullPath, source)
		if result.skill != nil {
			skills = append(skills, *result.skill)
		}
		diagnostics = append(diagnostics, result.diagnostics...)
	}

	return LoadSkillsResult{Skills: skills, Diagnostics: diagnostics}
}

// loadSkillFromFile parses and validates one skill file (pi:
// loadSkillFromFile).
func loadSkillFromFile(filePath string, source string) skillFileResult {
	var diagnostics []ResourceDiagnostic
	isDeclaredSkill := filepath.Base(filePath) == "SKILL.md"

	rawContent, err := os.ReadFile(filePath)
	if err != nil {
		message := "failed to read skill file"
		if err.Error() != "" {
			message = err.Error()
		}
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: message, Path: filePath})
		return skillFileResult{diagnostics: diagnostics}
	}

	frontmatter, _, err := utils.ParseFrontmatter(string(rawContent))
	if err != nil {
		if isDeclaredSkill {
			message := "failed to parse skill file"
			if err.Error() != "" {
				message = err.Error()
			}
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: message, Path: filePath})
		}
		return skillFileResult{diagnostics: diagnostics}
	}

	description, hasDescription := frontmatter["description"].(string)
	hasDescription = hasDescription && strings.TrimSpace(description) != ""
	if !isDeclaredSkill && !hasDescription {
		return skillFileResult{diagnostics: diagnostics}
	}

	skillDir := filepath.Dir(filePath)
	parentDirName := filepath.Base(skillDir)

	// Validate description
	for _, diagErr := range validateSkillDescription(frontmatter["description"]) {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: diagErr, Path: filePath})
	}

	// Use name from frontmatter, or fall back to parent directory name
	name := parentDirName
	if frontmatterName, ok := frontmatter["name"].(string); ok {
		name = frontmatterName
	}

	// Validate name
	for _, diagErr := range validateSkillName(name) {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: diagErr, Path: filePath})
	}

	// Still load the skill even with warnings, unless description is missing
	// or empty.
	if !hasDescription {
		return skillFileResult{diagnostics: diagnostics}
	}

	return skillFileResult{
		skill: &Skill{
			Name:                   name,
			Description:            description,
			FilePath:               filePath,
			BaseDir:                skillDir,
			SourceInfo:             createSkillSourceInfo(filePath, skillDir, source),
			DisableModelInvocation: frontmatter["disable-model-invocation"] == true,
		},
		diagnostics: diagnostics,
	}
}

// FormatSkillsForPrompt formats skills for inclusion in a system prompt (pi:
// formatSkillsForPrompt).
//
// Uses XML format per Agent Skills standard.
// See: https://agentskills.io/integrate-skills
//
// Skills with disableModelInvocation=true are excluded from the prompt
// (they can only be invoked explicitly via /skill:name commands).
func FormatSkillsForPrompt(skills []Skill) string {
	var visibleSkills []Skill
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visibleSkills = append(visibleSkills, skill)
		}
	}

	if len(visibleSkills) == 0 {
		return ""
	}

	lines := []string{
		"\n\nThe following skills provide specialized instructions for specific tasks.",
		"Use the read tool to load a skill's file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}

	for _, skill := range visibleSkills {
		lines = append(lines, "  <skill>")
		lines = append(lines, "    <name>"+escapeXml(skill.Name)+"</name>")
		lines = append(lines, "    <description>"+escapeXml(skill.Description)+"</description>")
		lines = append(lines, "    <location>"+escapeXml(skill.FilePath)+"</location>")
		lines = append(lines, "  </skill>")
	}

	lines = append(lines, "</available_skills>")

	return strings.Join(lines, "\n")
}

// escapeXml escapes XML special characters (pi: escapeXml).
func escapeXml(str string) string {
	str = strings.ReplaceAll(str, "&", "&amp;")
	str = strings.ReplaceAll(str, "<", "&lt;")
	str = strings.ReplaceAll(str, ">", "&gt;")
	str = strings.ReplaceAll(str, "\"", "&quot;")
	return strings.ReplaceAll(str, "'", "&apos;")
}

// LoadSkillsOptions carries the loadSkills options (pi: LoadSkillsOptions).
type LoadSkillsOptions struct {
	// Cwd is the working directory for project-local skills.
	Cwd string
	// AgentDir is the agent config directory for global skills.
	AgentDir string
	// SkillPaths are explicit skill paths (files or directories).
	SkillPaths []string
	// IncludeDefaults includes the default skills directories.
	IncludeDefaults bool
}

// LoadSkills loads skills from all configured locations (pi: loadSkills).
// Returns skills and any validation diagnostics.
func LoadSkills(options LoadSkillsOptions) LoadSkillsResult {
	agentDir := options.AgentDir
	skillPaths := options.SkillPaths
	includeDefaults := options.IncludeDefaults

	resolvedCwd := resolveAgainstProcessCwd(options.Cwd)
	resolvedAgentDir := resolveAgainstProcessCwd(agentDir)
	if agentDir == "" {
		resolvedAgentDir = resolveAgainstProcessCwd(codingagent.GetAgentDir())
	}

	skillMap := map[string]Skill{}
	realPathSet := map[string]struct{}{}
	var allDiagnostics []ResourceDiagnostic
	var collisionDiagnostics []ResourceDiagnostic

	addSkills := func(result LoadSkillsResult) {
		allDiagnostics = append(allDiagnostics, result.Diagnostics...)
		for _, skill := range result.Skills {
			// Resolve symlinks to detect duplicate files
			realPath := utils.CanonicalizePath(skill.FilePath)

			// Skip silently if we've already loaded this exact file (via
			// symlink)
			if _, seen := realPathSet[realPath]; seen {
				continue
			}

			existing, ok := skillMap[skill.Name]
			if ok {
				collisionDiagnostics = append(collisionDiagnostics, ResourceDiagnostic{
					Type:    "collision",
					Message: fmt.Sprintf("name %q collision", skill.Name),
					Path:    skill.FilePath,
					Collision: &ResourceCollision{
						ResourceType: "skill",
						Name:         skill.Name,
						WinnerPath:   existing.FilePath,
						LoserPath:    skill.FilePath,
					},
				})
			} else {
				skillMap[skill.Name] = skill
				realPathSet[realPath] = struct{}{}
			}
		}
	}

	if includeDefaults {
		addSkills(loadSkillsFromDirInternal(filepath.Join(resolvedAgentDir, "skills"), "user", true, nil, ""))
		addSkills(loadSkillsFromDirInternal(filepath.Join(resolvedCwd, codingagent.ConfigDirName, "skills"), "project", true, nil, ""))
	}

	userSkillsDir := filepath.Join(resolvedAgentDir, "skills")
	projectSkillsDir := filepath.Join(resolvedCwd, codingagent.ConfigDirName, "skills")

	isUnderPath := func(target string, root string) bool {
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

	getSource := func(resolvedPath string) string {
		if !includeDefaults {
			if isUnderPath(resolvedPath, userSkillsDir) {
				return "user"
			}
			if isUnderPath(resolvedPath, projectSkillsDir) {
				return "project"
			}
		}
		return "path"
	}

	for _, rawPath := range skillPaths {
		resolvedPath := utils.ResolvePath(rawPath, resolvedCwd, &utils.PathInputOptions{Trim: lo.ToPtr(true)})
		if _, err := os.Stat(resolvedPath); err != nil {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type:    "warning",
				Message: "skill path does not exist",
				Path:    resolvedPath,
			})
			continue
		}

		stats, err := os.Stat(resolvedPath)
		if err != nil {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type:    "warning",
				Message: err.Error(),
				Path:    resolvedPath,
			})
			continue
		}
		source := getSource(resolvedPath)
		if stats.IsDir() {
			addSkills(loadSkillsFromDirInternal(resolvedPath, source, true, nil, ""))
		} else if stats.Mode().IsRegular() && strings.HasSuffix(resolvedPath, ".md") {
			result := loadSkillFromFile(resolvedPath, source)
			if result.skill != nil {
				addSkills(LoadSkillsResult{Skills: []Skill{*result.skill}, Diagnostics: result.diagnostics})
			} else {
				allDiagnostics = append(allDiagnostics, result.diagnostics...)
			}
		} else {
			allDiagnostics = append(allDiagnostics, ResourceDiagnostic{
				Type:    "warning",
				Message: "skill path is not a markdown file",
				Path:    resolvedPath,
			})
		}
	}

	// pi: Array.from(skillMap.values()) — Map insertion order; pig sorts by
	// name for determinism (declared deviation: TS Map preserves insertion
	// order, Go maps are unordered).
	names := make([]string, 0, len(skillMap))
	for name := range skillMap {
		names = append(names, name)
	}
	sort.Strings(names)
	skills := make([]Skill, 0, len(skillMap))
	for _, name := range names {
		skills = append(skills, skillMap[name])
	}

	return LoadSkillsResult{
		Skills:      skills,
		Diagnostics: append(allDiagnostics, collisionDiagnostics...),
	}
}
