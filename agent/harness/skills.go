// pi: src/harness/skills.ts — skill loading from SKILL.md trees.
//
// Segment deviations:
//   - Result<T, E> collapses to (T, error) / extra return values; failures
//     surface as diagnostics and never fail the load call.
//   - pi's `ignore` npm package → github.com/sabhiram/go-gitignore. Only
//     add(patterns) + ignores(path) are consumed; the matcher is recompiled
//     from the accumulated pattern list on each add. gitignore semantics are
//     approximated for the directory-suffix forms pi feeds it (pi appends
//     "/" to directory paths itself); no pi test pins the divergent edges.
//   - parseFrontmatter/resolveKind are shared with prompt_templates.go (pi
//     duplicates them verbatim; see that file's header note).
//   - pi's recursive async walk collapses to direct recursion; env calls use
//     a background context (the TS source passes no abort signals).
//   - localeCompare sorting applies only to directory entries and root files
//     here; byte-order strings.Compare is used (ASCII names).
package harness

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gitignore "github.com/sabhiram/go-gitignore"
)

const (
	maxNameLength        = 64
	maxDescriptionLength = 1024
)

// ignoreFileNames are the ignore files honored while walking skill
// directories (pi: IGNORE_FILE_NAMES).
var ignoreFileNames = []string{".gitignore", ".ignore", ".fdignore"}

// ignoreMatcher accumulates gitignore patterns and tests relative paths (pi:
// the `ignore` npm package's IgnoreMatcher).
type ignoreMatcher struct {
	lines   []string
	matcher *gitignore.GitIgnore
}

// add appends patterns and recompiles (pi: ig.add(patterns)).
func (m *ignoreMatcher) add(patterns []string) {
	m.lines = append(m.lines, patterns...)
	m.matcher = gitignore.CompileIgnoreLines(m.lines...)
}

// ignores reports whether a relative path is ignored (pi:
// ig.ignores(path)). With no patterns, nothing is ignored.
func (m *ignoreMatcher) ignores(path string) bool {
	if m.matcher == nil {
		return false
	}
	return m.matcher.MatchesPath(path)
}

// SkillDiagnosticCode is the stable diagnostic vocabulary for skill loading
// (pi: SkillDiagnosticCode).
type SkillDiagnosticCode string

const (
	SkillDiagFileInfoFailed  SkillDiagnosticCode = "file_info_failed"
	SkillDiagListFailed      SkillDiagnosticCode = "list_failed"
	SkillDiagReadFailed      SkillDiagnosticCode = "read_failed"
	SkillDiagParseFailed     SkillDiagnosticCode = "parse_failed"
	SkillDiagInvalidMetadata SkillDiagnosticCode = "invalid_metadata"
)

// SkillDiagnostic is a warning produced while loading skills (pi:
// SkillDiagnostic).
type SkillDiagnostic struct {
	// Type is the diagnostic severity. Currently only warnings are emitted
	// (pi's literal "warning").
	Type    string              `json:"type"`
	Code    SkillDiagnosticCode `json:"code"`
	Message string              `json:"message"`
	Path    string              `json:"path"`
}

// FormatSkillInvocation formats a skill invocation prompt, optionally
// appending additional user instructions (pi: formatSkillInvocation).
func FormatSkillInvocation(skill Skill, additionalInstructions string) string {
	skillBlock := "<skill name=\"" + skill.Name + "\" location=\"" + skill.FilePath + "\">\n" +
		"References are relative to " + dirnameEnvPath(skill.FilePath) + ".\n\n" +
		skill.Content + "\n</skill>"
	if additionalInstructions != "" {
		return skillBlock + "\n\n" + additionalInstructions
	}
	return skillBlock
}

// LoadSkills loads skills from one or more directories (pi: loadSkills).
//
// Traverses directories recursively, loads `SKILL.md` files, loads direct
// root `.md` files with skill frontmatter, honors ignore files, and returns
// diagnostics for invalid declared skill files. Missing input directories
// are skipped.
func LoadSkills(env ExecutionEnv, dirs []string) ([]Skill, []SkillDiagnostic) {
	var skills []Skill
	var diagnostics []SkillDiagnostic
	for _, dir := range dirs {
		rootInfo, err := env.FileInfo(context.Background(), dir)
		if err != nil {
			if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
				diagnostics = append(diagnostics, SkillDiagnostic{
					Type:    "warning",
					Code:    SkillDiagFileInfoFailed,
					Message: err.Error(),
					Path:    dir,
				})
			}
			continue
		}
		if kind, _ := resolveSkillKind(env, rootInfo, &diagnostics); kind != FileKindDirectory {
			continue
		}
		result := loadSkillsFromDirInternal(env, rootInfo.Path, true, &ignoreMatcher{}, rootInfo.Path)
		skills = append(skills, result.skills...)
		diagnostics = append(diagnostics, result.diagnostics...)
	}
	return skills, diagnostics
}

// SourcedSkill pairs a loaded skill with its provenance source (pi: the
// returned Array<{ skill: TSkill; source: TSource }> shape).
type SourcedSkill[TSource any] struct {
	Skill  Skill
	Source TSource
}

// SourcedSkillDiagnostic is a skill diagnostic tagged with its provenance
// source (pi: SkillDiagnostic & { source: TSource }).
type SourcedSkillDiagnostic[TSource any] struct {
	SkillDiagnostic
	Source TSource
}

// LoadSourcedSkills loads skills from source-tagged directories (pi:
// loadSourcedSkills<TSource, TSkill>).
//
// Source values are preserved exactly and attached to every loaded skill and
// diagnostic. The agent package does not interpret source values;
// applications define their own provenance shape. pi's TSkill generic
// collapses to Skill (see LoadSourcedPromptTemplates).
func LoadSourcedSkills[TSource any](
	env ExecutionEnv,
	inputs []SourcedInput[TSource],
	mapSkill func(Skill, TSource) Skill,
) ([]SourcedSkill[TSource], []SourcedSkillDiagnostic[TSource]) {
	var skills []SourcedSkill[TSource]
	var diagnostics []SourcedSkillDiagnostic[TSource]
	for _, input := range inputs {
		loaded, diags := LoadSkills(env, []string{input.Path})
		for _, skill := range loaded {
			if mapSkill != nil {
				skill = mapSkill(skill, input.Source)
			}
			skills = append(skills, SourcedSkill[TSource]{Skill: skill, Source: input.Source})
		}
		for _, diagnostic := range diags {
			diagnostics = append(diagnostics, SourcedSkillDiagnostic[TSource]{
				SkillDiagnostic: diagnostic,
				Source:          input.Source,
			})
		}
	}
	return skills, diagnostics
}

// skillWalkResult bundles one walk step's findings (pi: the internal
// { skills, diagnostics } return shapes).
type skillWalkResult struct {
	skills      []Skill
	diagnostics []SkillDiagnostic
}

// loadSkillsFromDirInternal walks one directory level (pi:
// loadSkillsFromDirInternal). The first SKILL.md in a directory wins; root
// markdown files load only when includeRootFiles is set; subdirectories
// recurse looking for SKILL.md files only.
func loadSkillsFromDirInternal(
	env ExecutionEnv,
	dir string,
	includeRootFiles bool,
	ig *ignoreMatcher,
	rootDir string,
) skillWalkResult {
	var result skillWalkResult

	dirInfo, err := env.FileInfo(context.Background(), dir)
	if err != nil {
		if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
			result.diagnostics = append(result.diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagFileInfoFailed,
				Message: err.Error(),
				Path:    dir,
			})
		}
		return result
	}
	if kind, _ := resolveSkillKind(env, dirInfo, &result.diagnostics); kind != FileKindDirectory {
		return result
	}

	addIgnoreRules(env, ig, dir, rootDir, &result.diagnostics)

	entries, err := env.ListDir(context.Background(), dir)
	if err != nil {
		result.diagnostics = append(result.diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    SkillDiagListFailed,
			Message: err.Error(),
			Path:    dir,
		})
		return result
	}

	for _, entry := range entries {
		if entry.Name != "SKILL.md" {
			continue
		}
		fullPath := entry.Path
		kind, _ := resolveSkillKind(env, entry, &result.diagnostics)
		if kind != FileKindFile {
			continue
		}
		relPath := relativeEnvPath(rootDir, fullPath)
		if ig.ignores(relPath) {
			continue
		}

		fileResult := loadSkillFromFile(env, fullPath, dirInfo.Name)
		if fileResult.skill != nil {
			result.skills = append(result.skills, *fileResult.skill)
		}
		result.diagnostics = append(result.diagnostics, fileResult.diagnostics...)
		return result
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.Compare(entries[i].Name, entries[j].Name) < 0
	})
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") || entry.Name == "node_modules" {
			continue
		}
		fullPath := entry.Path
		kind, _ := resolveSkillKind(env, entry, &result.diagnostics)
		if kind == "" {
			continue
		}

		relPath := relativeEnvPath(rootDir, fullPath)
		ignorePath := relPath
		if kind == FileKindDirectory {
			ignorePath = relPath + "/"
		}
		if ig.ignores(ignorePath) {
			continue
		}

		if kind == FileKindDirectory {
			sub := loadSkillsFromDirInternal(env, fullPath, false, ig, rootDir)
			result.skills = append(result.skills, sub.skills...)
			result.diagnostics = append(result.diagnostics, sub.diagnostics...)
			continue
		}

		if kind != FileKindFile || !includeRootFiles || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		fileResult := loadSkillFromFile(env, fullPath, dirInfo.Name)
		if fileResult.skill != nil {
			result.skills = append(result.skills, *fileResult.skill)
		}
		result.diagnostics = append(result.diagnostics, fileResult.diagnostics...)
	}

	return result
}

// addIgnoreRules loads the directory's ignore files into the matcher (pi:
// addIgnoreRules). Patterns are prefixed with the directory's path relative
// to the walk root.
func addIgnoreRules(env ExecutionEnv, ig *ignoreMatcher, dir string, rootDir string, diagnostics *[]SkillDiagnostic) {
	relativeDir := relativeEnvPath(rootDir, dir)
	prefix := ""
	if relativeDir != "" {
		prefix = relativeDir + "/"
	}

	for _, filename := range ignoreFileNames {
		ignorePath, err := env.JoinPath(context.Background(), []string{dir, filename})
		if err != nil {
			*diagnostics = append(*diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagFileInfoFailed,
				Message: err.Error(),
				Path:    dir,
			})
			continue
		}
		info, err := env.FileInfo(context.Background(), ignorePath)
		if err != nil {
			if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
				*diagnostics = append(*diagnostics, SkillDiagnostic{
					Type:    "warning",
					Code:    SkillDiagFileInfoFailed,
					Message: err.Error(),
					Path:    ignorePath,
				})
			}
			continue
		}
		if info.Kind != FileKindFile {
			continue
		}
		content, err := env.ReadTextFile(context.Background(), ignorePath)
		if err != nil {
			*diagnostics = append(*diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagReadFailed,
				Message: err.Error(),
				Path:    ignorePath,
			})
			continue
		}
		var patterns []string
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimRight(line, "\r")
			prefixed := prefixIgnorePattern(line, prefix)
			if prefixed != nil {
				patterns = append(patterns, *prefixed)
			}
		}
		if len(patterns) > 0 {
			ig.add(patterns)
		}
	}
}

// prefixIgnorePattern rewrites one ignore-file line relative to the walk
// root (pi: prefixIgnorePattern). A nil result drops the line.
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
	pattern = strings.TrimPrefix(pattern, "/")
	prefixed := pattern
	if prefix != "" {
		prefixed = prefix + pattern
	}
	if negated {
		prefixed = "!" + prefixed
	}
	return &prefixed
}

// loadSkillFromFile parses one candidate skill file (pi: loadSkillFromFile).
// A nil skill means the file is not a skill; declared SKILL.md files with
// unparsable frontmatter report parse_failed, other files are silent.
func loadSkillFromFile(env ExecutionEnv, filePath string, parentDirName string) skillFileResult {
	var diagnostics []SkillDiagnostic
	isDeclaredSkill := false
	trimmed := strings.TrimRight(filePath, "/\\")
	if idx := strings.LastIndexAny(trimmed, "/\\"); idx >= 0 {
		isDeclaredSkill = trimmed[idx+1:] == "SKILL.md"
	} else {
		isDeclaredSkill = trimmed == "SKILL.md"
	}
	rawContent, err := env.ReadTextFile(context.Background(), filePath)
	if err != nil {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    SkillDiagReadFailed,
			Message: err.Error(),
			Path:    filePath,
		})
		return skillFileResult{skill: nil, diagnostics: diagnostics}
	}

	frontmatter, body, parseErr := parseFrontmatter(rawContent)
	if parseErr != nil {
		if isDeclaredSkill {
			diagnostics = append(diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagParseFailed,
				Message: parseErr.Error(),
				Path:    filePath,
			})
		}
		return skillFileResult{skill: nil, diagnostics: diagnostics}
	}

	description, hasDescription := frontmatter["description"].(string)
	if !isDeclaredSkill && (!hasDescription || strings.TrimSpace(description) == "") {
		return skillFileResult{skill: nil, diagnostics: diagnostics}
	}

	for _, message := range validateDescription(hasDescription, description) {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    SkillDiagInvalidMetadata,
			Message: message,
			Path:    filePath,
		})
	}

	frontmatterName, hasName := frontmatter["name"].(string)
	name := parentDirName
	if hasName && frontmatterName != "" {
		name = frontmatterName
	}
	for _, message := range validateName(name, parentDirName) {
		diagnostics = append(diagnostics, SkillDiagnostic{
			Type:    "warning",
			Code:    SkillDiagInvalidMetadata,
			Message: message,
			Path:    filePath,
		})
	}

	if !hasDescription || strings.TrimSpace(description) == "" {
		return skillFileResult{skill: nil, diagnostics: diagnostics}
	}

	disableModelInvocation := false
	if v, ok := frontmatter["disable-model-invocation"].(bool); ok && v {
		disableModelInvocation = true
	}
	return skillFileResult{
		skill: &Skill{
			Name:                   name,
			Description:            description,
			Content:                body,
			FilePath:               filePath,
			DisableModelInvocation: &disableModelInvocation,
		},
		diagnostics: diagnostics,
	}
}

// skillFileResult bundles one file's outcome (pi: the internal { skill:
// Skill | null, diagnostics } return shape).
type skillFileResult struct {
	skill       *Skill
	diagnostics []SkillDiagnostic
}

// validateName checks a skill name against pi's rules (pi: validateName).
func validateName(name string, parentDirName string) []string {
	var errors []string
	if name != parentDirName {
		errors = append(errors, "name \""+name+"\" does not match parent directory \""+parentDirName+"\"")
	}
	if len(name) > maxNameLength {
		errors = append(errors, "name exceeds "+strconv.Itoa(maxNameLength)+" characters ("+strconv.Itoa(len(name))+")")
	}
	if !nameCharsetRe.MatchString(name) {
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

// validateDescription checks a skill description (pi: validateDescription).
// pi's `undefined` input is modeled as (present=false, description="").
func validateDescription(present bool, description string) []string {
	var errors []string
	if !present || strings.TrimSpace(description) == "" {
		errors = append(errors, "description is required")
	} else if len(description) > maxDescriptionLength {
		errors = append(errors, "description exceeds "+strconv.Itoa(maxDescriptionLength)+" characters ("+strconv.Itoa(len(description))+")")
	}
	return errors
}

// nameCharsetRe matches pi's /^[a-z0-9-]+$/.
var nameCharsetRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// resolveSkillKind is the skills-module wrapper over the shared resolveKind
// (pi: skills.ts's own resolveKind copy; identical to prompt-templates.ts's).
func resolveSkillKind(env ExecutionEnv, info FileInfo, diagnostics *[]SkillDiagnostic) (FileKind, bool) {
	if info.Kind == FileKindFile || info.Kind == FileKindDirectory {
		return info.Kind, true
	}
	canonicalPath, err := env.CanonicalPath(context.Background(), info.Path)
	if err != nil {
		if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
			*diagnostics = append(*diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagFileInfoFailed,
				Message: err.Error(),
				Path:    info.Path,
			})
		}
		return "", false
	}
	target, err := env.FileInfo(context.Background(), canonicalPath)
	if err != nil {
		if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
			*diagnostics = append(*diagnostics, SkillDiagnostic{
				Type:    "warning",
				Code:    SkillDiagFileInfoFailed,
				Message: err.Error(),
				Path:    info.Path,
			})
		}
		return "", false
	}
	if target.Kind == FileKindFile || target.Kind == FileKindDirectory {
		return target.Kind, true
	}
	return "", false
}

// dirnameEnvPath returns the parent directory of an environment path (pi:
// dirnameEnvPath; Windows-drive-aware, separator-agnostic).
func dirnameEnvPath(path string) string {
	normalized := strings.TrimRight(path, "/\\")
	separatorIndex := max(strings.LastIndex(normalized, "/"), strings.LastIndex(normalized, "\\"))
	if separatorIndex == 2 && normalized[1] == ':' {
		return normalized[:3]
	}
	if separatorIndex <= 0 {
		return "/"
	}
	return normalized[:separatorIndex]
}

// relativeEnvPath relativizes path against root in the environment's path
// namespace (pi: relativeEnvPath; backslashes normalized, trailing
// separators stripped, unmatched paths keep their leading separators).
func relativeEnvPath(root string, path string) string {
	normalizedRoot := strings.TrimRight(strings.ReplaceAll(root, "\\", "/"), "/")
	normalizedPath := strings.TrimRight(strings.ReplaceAll(path, "\\", "/"), "/")
	if normalizedPath == normalizedRoot {
		return ""
	}
	if strings.HasPrefix(normalizedPath, normalizedRoot+"/") {
		return normalizedPath[len(normalizedRoot)+1:]
	}
	return strings.TrimLeft(normalizedPath, "/")
}
