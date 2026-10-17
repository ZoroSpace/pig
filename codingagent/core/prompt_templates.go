// pi: src/core/prompt-templates.ts — coding-agent prompt template loading
// and argument expansion.
//
// This is coding-agent's own prompt-template loader (pi's core/
// prompt-templates.ts); the agent package has a separate harness copy, and
// pig mirrors that duplication.
//
// Segment deviations:
//   - pi's substituteArgs uses one JS regex with a replace callback; Go's
//     RE2 has no replace-with-groups, so the same pattern is matched with
//     FindAllStringSubmatchIndex and spliced manually (same alternation
//     order and semantics).
//   - `char === '"'` / `/\s/` char tests iterate runes with a JS-\s
//     whitespace set (Go's unicode.IsSpace misses U+00A0/U+FEFF class
//     members that JS \s includes).
//   - description first-line truncation uses rune counts (TS UTF-16 code
//     units; established precedent).
//   - non-string truthy frontmatter values (pi's type lies, e.g. a number
//     description) fall back to the derived description instead of being
//     propagated (declared).
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// PromptTemplate represents a prompt template loaded from a markdown file
// (pi: PromptTemplate).
type PromptTemplate struct {
	Name         string
	Description  string
	ArgumentHint string // pi: argumentHint? ("" = undefined)
	Content      string
	SourceInfo   SourceInfo
	// FilePath is the absolute path to the template file.
	FilePath string
}

// ParseCommandArgs parses command arguments respecting quoted strings
// (bash-style); returns the argument array (pi: parseCommandArgs).
//
// Byte-level iteration mirrors TS's UTF-16 code-unit iteration for all
// multibyte content (bytes reassemble); the only divergence is non-ASCII
// JS-\s whitespace (e.g. U+00A0) acting as a separator, which pi's tests
// never exercise (declared).
func ParseCommandArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	// hasToken tracks whether a token has started, so quoted empty strings
	// ("" or '') are kept as arguments (S19-2, pi 9fe3f3716).
	hasToken := false
	inQuote := byte(0)

	for i := 0; i < len(argsString); i++ {
		char := argsString[i]

		if inQuote != 0 {
			if char == inQuote {
				inQuote = 0
			} else {
				current.WriteByte(char)
			}
		} else if char == '"' || char == '\'' {
			inQuote = char
			hasToken = true
		} else if char == ' ' || char == '\t' || char == '\n' || char == '\v' || char == '\f' || char == '\r' {
			if hasToken {
				args = append(args, current.String())
				current.Reset()
				hasToken = false
			}
		} else {
			current.WriteByte(char)
			hasToken = true
		}
	}

	if hasToken {
		args = append(args, current.String())
	}

	return args
}

// substituteArgsPattern matches the same shapes as pi's regex, in the same
// alternation order: ${N|ARGUMENTS|@:-default}, ${@:N[:L]}, then
// $ARGUMENTS|$@|$N.
var substituteArgsPattern = regexp.MustCompile(`\$\{(\d+|ARGUMENTS|@):-([^}]*)\}|\$\{@:(\d+)(?::(\d+))?\}|\$(ARGUMENTS|@|\d+)`)

// SubstituteArgs substitutes argument placeholders in template content (pi:
// substituteArgs). Supports:
//   - $1, $2, ... for positional args
//   - $@ and $ARGUMENTS for all args
//   - ${N:-default} for positional arg N with default when missing/empty
//   - ${@:-default} and ${ARGUMENTS:-default} for all args with a default
//     when empty
//   - ${@:N} for args from Nth onwards (bash-style slicing)
//   - ${@:N:L} for L args starting from Nth
//
// Note: Replacement happens on the template string only. Argument and
// default values containing patterns like $1, $@, or $ARGUMENTS are NOT
// recursively substituted.
func SubstituteArgs(content string, args []string) string {
	allArgs := strings.Join(args, " ")

	var result strings.Builder
	lastIndex := 0

	for _, loc := range substituteArgsPattern.FindAllStringSubmatchIndex(content, -1) {
		group := func(i int) (string, bool) {
			if i*2 >= len(loc) || loc[i*2] < 0 {
				return "", false
			}
			return content[loc[i*2]:loc[i*2+1]], true
		}
		defaultTarget, hasDefaultTarget := group(1)
		defaultValue, hasDefaultValue := group(2)
		sliceStart, hasSliceStart := group(3)
		sliceLength, hasSliceLength := group(4)
		simple, hasSimple := group(5)

		replacement := ""
		switch {
		case hasDefaultTarget:
			value := ""
			if defaultTarget == "@" || defaultTarget == "ARGUMENTS" {
				value = allArgs
			} else {
				index := 0
				fmt.Sscanf(defaultTarget, "%d", &index)
				if index-1 >= 0 && index-1 < len(args) {
					value = args[index-1]
				}
			}
			if value != "" {
				replacement = value
			} else if hasDefaultValue {
				replacement = defaultValue
			}

		case hasSliceStart:
			start := 0
			fmt.Sscanf(sliceStart, "%d", &start)
			start-- // Convert to 0-indexed (user provides 1-indexed)
			// Treat 0 as 1 (bash convention: args start at 1)
			if start < 0 {
				start = 0
			}
			if start > len(args) {
				start = len(args)
			}

			if hasSliceLength {
				length := 0
				fmt.Sscanf(sliceLength, "%d", &length)
				end := start + length
				if end > len(args) {
					end = len(args)
				}
				replacement = strings.Join(args[start:end], " ")
			} else {
				replacement = strings.Join(args[start:], " ")
			}

		case hasSimple && (simple == "ARGUMENTS" || simple == "@"):
			replacement = allArgs

		case hasSimple:
			index := 0
			fmt.Sscanf(simple, "%d", &index)
			if index-1 >= 0 && index-1 < len(args) {
				replacement = args[index-1]
			}
		}

		result.WriteString(content[lastIndex:loc[0]])
		result.WriteString(replacement)
		lastIndex = loc[1]
	}

	result.WriteString(content[lastIndex:])
	return result.String()
}

// loadTemplateFromFile loads one prompt template file (pi:
// loadTemplateFromFile); the template is nil on read/parse failure and the
// failure is reported as a warning diagnostic (pi #9830 / #9354).
func loadTemplateFromFile(filePath string, sourceInfo SourceInfo) (*PromptTemplate, []ResourceDiagnostic) {
	var diagnostics []ResourceDiagnostic
	rawContent, err := os.ReadFile(filePath)
	if err != nil {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: err.Error(), Path: filePath})
		return nil, diagnostics
	}
	frontmatter, body, err := utils.ParseFrontmatter(string(rawContent))
	if err != nil {
		diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: err.Error(), Path: filePath})
		return nil, diagnostics
	}

	name := strings.TrimSuffix(filepath.Base(filePath), ".md")

	// Get description from frontmatter or first non-empty line
	description := ""
	if value, ok := frontmatter["description"].(string); ok && value != "" {
		description = value
	}
	if description == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" {
				// Truncate if too long (rune counts; pi slices UTF-16 code
				// units)
				runes := []rune(line)
				description = string(runes[:min(60, len(runes))])
				if len(runes) > 60 {
					description += "..."
				}
				break
			}
		}
	}

	template := &PromptTemplate{
		Name:        name,
		Description: description,
		Content:     body,
		SourceInfo:  sourceInfo,
		FilePath:    filePath,
	}
	// pi: ...(frontmatter["argument-hint"] && { argumentHint }) — only
	// non-empty string hints are set.
	if hint, ok := frontmatter["argument-hint"].(string); ok && hint != "" {
		template.ArgumentHint = hint
	}
	return template, diagnostics
}

// loadTemplatesFromDir scans a directory for .md files (non-recursive) and
// loads them as prompt templates (pi: loadTemplatesFromDir).
func loadTemplatesFromDir(dir string, getSourceInfo func(filePath string) SourceInfo) ([]PromptTemplate, []ResourceDiagnostic) {
	var templates []PromptTemplate
	var diagnostics []ResourceDiagnostic

	if _, err := os.Stat(dir); err != nil {
		return templates, diagnostics
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return templates, diagnostics
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())

		// For symlinks, check if they point to a file
		isFile := entry.Type().IsRegular()
		if entry.Type()&os.ModeSymlink != 0 {
			stats, err := os.Stat(fullPath)
			if err != nil {
				// Broken symlink, skip it
				continue
			}
			isFile = stats.Mode().IsRegular()
		}

		if isFile && strings.HasSuffix(entry.Name(), ".md") {
			template, templateDiagnostics := loadTemplateFromFile(fullPath, getSourceInfo(fullPath))
			if template != nil {
				templates = append(templates, *template)
			}
			diagnostics = append(diagnostics, templateDiagnostics...)
		}
	}

	return templates, diagnostics
}

// LoadPromptTemplatesOptions carries the loadPromptTemplates options (pi:
// LoadPromptTemplatesOptions).
type LoadPromptTemplatesOptions struct {
	// Cwd is the working directory for project-local templates.
	Cwd string
	// AgentDir is the agent config directory for global templates.
	AgentDir string
	// PromptPaths are explicit prompt template paths (files or directories).
	PromptPaths []string
	// IncludeDefaults includes the default prompt directories.
	IncludeDefaults bool
}

// LoadPromptTemplatesResult bundles the loaded templates with diagnostics
// (pi: LoadPromptTemplatesResult, #9830).
type LoadPromptTemplatesResult struct {
	Templates   []PromptTemplate
	Diagnostics []ResourceDiagnostic
}

// LoadPromptTemplates loads all prompt templates from:
//  1. Global: agentDir/prompts/
//  2. Project: cwd/{CONFIG_DIR_NAME}/prompts/
//  3. Explicit prompt paths
//
// (pi: loadPromptTemplates.)
func LoadPromptTemplates(options LoadPromptTemplatesOptions) LoadPromptTemplatesResult {
	resolvedCwd := resolveAgainstProcessCwd(options.Cwd)
	resolvedAgentDir := resolveAgainstProcessCwd(options.AgentDir)
	promptPaths := options.PromptPaths
	includeDefaults := options.IncludeDefaults

	var templates []PromptTemplate
	var diagnostics []ResourceDiagnostic
	addResult := func(result LoadPromptTemplatesResult) {
		templates = append(templates, result.Templates...)
		diagnostics = append(diagnostics, result.Diagnostics...)
	}

	globalPromptsDir := filepath.Join(resolvedAgentDir, "prompts")
	projectPromptsDir := filepath.Join(resolvedCwd, codingagent.ConfigDirName, "prompts")

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

	getSourceInfo := func(resolvedPath string) SourceInfo {
		if isUnderPath(resolvedPath, globalPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, SyntheticSourceInfoOptions{
				Source:  "local",
				Scope:   SourceScopeUser,
				BaseDir: globalPromptsDir,
			})
		}
		if isUnderPath(resolvedPath, projectPromptsDir) {
			return CreateSyntheticSourceInfo(resolvedPath, SyntheticSourceInfoOptions{
				Source:  "local",
				Scope:   SourceScopeProject,
				BaseDir: projectPromptsDir,
			})
		}
		baseDir := filepath.Dir(resolvedPath)
		if stats, err := os.Stat(resolvedPath); err == nil && stats.IsDir() {
			baseDir = resolvedPath
		}
		return CreateSyntheticSourceInfo(resolvedPath, SyntheticSourceInfoOptions{
			Source:  "local",
			BaseDir: baseDir,
		})
	}

	if includeDefaults {
		gt, gd := loadTemplatesFromDir(globalPromptsDir, getSourceInfo)
		addResult(LoadPromptTemplatesResult{Templates: gt, Diagnostics: gd})
		pt, pd := loadTemplatesFromDir(projectPromptsDir, getSourceInfo)
		addResult(LoadPromptTemplatesResult{Templates: pt, Diagnostics: pd})
	}

	// 3. Load explicit prompt paths
	for _, rawPath := range promptPaths {
		resolvedPath := utils.ResolvePath(rawPath, resolvedCwd, &utils.PathInputOptions{Trim: lo.ToPtr(true)})
		stats, err := os.Stat(resolvedPath)
		if err != nil {
			// pi: statSync failures report a warning diagnostic (#9830);
			// they were silently ignored before.
			diagnostics = append(diagnostics, ResourceDiagnostic{Type: "warning", Message: err.Error(), Path: resolvedPath})
			continue
		}
		if stats.IsDir() {
			dt, dd := loadTemplatesFromDir(resolvedPath, getSourceInfo)
			addResult(LoadPromptTemplatesResult{Templates: dt, Diagnostics: dd})
		} else if stats.Mode().IsRegular() && strings.HasSuffix(resolvedPath, ".md") {
			template, templateDiagnostics := loadTemplateFromFile(resolvedPath, getSourceInfo(resolvedPath))
			if template != nil {
				templates = append(templates, *template)
			}
			diagnostics = append(diagnostics, templateDiagnostics...)
		}
	}

	return LoadPromptTemplatesResult{Templates: templates, Diagnostics: diagnostics}
}

// expandPromptTemplatePattern splits "/name args" (pi:
// /^\/([^\s]+)(?:\s+([\s\S]*))?$/).
var expandPromptTemplatePattern = regexp.MustCompile(`^/([^\s]+)(?:\s+([\s\S]*))?$`)

// ExpandPromptTemplate expands a prompt template if it matches a template
// name (pi: expandPromptTemplate). Returns the expanded content or the
// original text if not a template.
func ExpandPromptTemplate(text string, templates []PromptTemplate) string {
	if !strings.HasPrefix(text, "/") {
		return text
	}

	match := expandPromptTemplatePattern.FindStringSubmatch(text)
	if match == nil {
		return text
	}

	templateName := match[1]
	argsString := "" // pi: match[2] ?? ""
	if len(match) > 2 {
		argsString = match[2]
	}

	for _, template := range templates {
		if template.Name == templateName {
			args := ParseCommandArgs(argsString)
			return SubstituteArgs(template.Content, args)
		}
	}

	return text
}
