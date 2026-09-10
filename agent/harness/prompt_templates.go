// pi: src/harness/prompt-templates.ts — prompt template loading and
// argument formatting.
//
// Segment deviations:
//   - Result<T, E> collapses to (T, error) / extra return values; the load
//     functions report failures via diagnostics and never fail the call.
//   - pi duplicates parseFrontmatter and resolveKind verbatim in
//     prompt-templates.ts and skills.ts; in a single Go package the symbols
//     would collide, so both live here once and skills.go uses them.
//   - The npm `yaml` parse result's "properties of anything" access collapses
//     to a map[string]any: a non-mapping frontmatter (scalar/list top level)
//     contributes no keys, matching TS property access on a non-object.
//   - entries.sort((a, b) => a.name.localeCompare(b.name)) → byte-order
//     strings.Compare; all pi call sites sort ASCII filenames where both
//     orders agree.
//   - pi's async/await over the (synchronous) FileSystem interface collapses
//     to direct calls; the AbortSignal → context rule maps the env calls'
//     optional signals to a background context (the TS source passes none).
package harness

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PromptTemplateDiagnosticCode is the stable diagnostic vocabulary for prompt
// template loading (pi: PromptTemplateDiagnosticCode).
type PromptTemplateDiagnosticCode string

const (
	PromptTemplateDiagFileInfoFailed PromptTemplateDiagnosticCode = "file_info_failed"
	PromptTemplateDiagListFailed     PromptTemplateDiagnosticCode = "list_failed"
	PromptTemplateDiagReadFailed     PromptTemplateDiagnosticCode = "read_failed"
	PromptTemplateDiagParseFailed    PromptTemplateDiagnosticCode = "parse_failed"
)

// PromptTemplateDiagnostic is a warning produced while loading prompt
// templates (pi: PromptTemplateDiagnostic).
type PromptTemplateDiagnostic struct {
	// Type is the diagnostic severity. Currently only warnings are emitted
	// (pi's literal "warning").
	Type    string                       `json:"type"`
	Code    PromptTemplateDiagnosticCode `json:"code"`
	Message string                       `json:"message"`
	Path    string                       `json:"path"`
}

// mdSuffixRe strips a trailing ".md" case-insensitively (pi:
// fileName.replace(/\.md$/i, "")).
var mdSuffixRe = regexp.MustCompile(`(?i)\.md$`)

// LoadPromptTemplates loads prompt templates from one or more paths (pi:
// loadPromptTemplates).
//
// Directory inputs load direct `.md` children non-recursively. File inputs
// load explicit `.md` files. Missing paths and non-markdown files are
// skipped. Read and parse failures are returned as diagnostics.
func LoadPromptTemplates(env ExecutionEnv, paths []string) ([]PromptTemplate, []PromptTemplateDiagnostic) {
	var promptTemplates []PromptTemplate
	var diagnostics []PromptTemplateDiagnostic
	for _, path := range paths {
		info, err := env.FileInfo(context.Background(), path)
		if err != nil {
			if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
				diagnostics = append(diagnostics, PromptTemplateDiagnostic{
					Type:    "warning",
					Code:    PromptTemplateDiagFileInfoFailed,
					Message: err.Error(),
					Path:    path,
				})
			}
			continue
		}
		kind, _ := resolveKind(env, info, &diagnostics)
		if kind == FileKindDirectory {
			templates, diags := loadTemplatesFromDir(env, info.Path)
			promptTemplates = append(promptTemplates, templates...)
			diagnostics = append(diagnostics, diags...)
		} else if kind == FileKindFile && strings.HasSuffix(info.Name, ".md") {
			template, diags := loadTemplateFromFile(env, info.Path, info.Name)
			if template != nil {
				promptTemplates = append(promptTemplates, *template)
			}
			diagnostics = append(diagnostics, diags...)
		}
	}
	return promptTemplates, diagnostics
}

// SourcedInput is one source-tagged path (pi: the
// Array<{ path: string; source: TSource }> input shape).
type SourcedInput[TSource any] struct {
	Path   string
	Source TSource
}

// SourcedPromptTemplate pairs a loaded template with its provenance source
// (pi: the returned Array<{ promptTemplate: TPromptTemplate; source: TSource
// }> shape).
type SourcedPromptTemplate[TSource any] struct {
	PromptTemplate PromptTemplate
	Source         TSource
}

// SourcedPromptTemplateDiagnostic is a diagnostic tagged with its provenance
// source (pi: PromptTemplateDiagnostic & { source: TSource }).
type SourcedPromptTemplateDiagnostic[TSource any] struct {
	PromptTemplateDiagnostic
	Source TSource
}

// LoadSourcedPromptTemplates loads prompt templates from source-tagged paths
// (pi: loadSourcedPromptTemplates<TSource, TPromptTemplate>).
//
// Source values are preserved exactly and attached to every loaded prompt
// template and diagnostic. The agent package does not interpret source
// values; applications define their own provenance shape. pi's TPromptTemplate
// generic collapses to PromptTemplate: TS only widens the struct via a
// mapper that returns a compatible shape, which Go models by keeping the
// base struct and letting applications carry their own source-tagged view.
func LoadSourcedPromptTemplates[TSource any](
	env ExecutionEnv,
	inputs []SourcedInput[TSource],
	mapPromptTemplate func(PromptTemplate, TSource) PromptTemplate,
) ([]SourcedPromptTemplate[TSource], []SourcedPromptTemplateDiagnostic[TSource]) {
	var promptTemplates []SourcedPromptTemplate[TSource]
	var diagnostics []SourcedPromptTemplateDiagnostic[TSource]
	for _, input := range inputs {
		templates, diags := LoadPromptTemplates(env, []string{input.Path})
		for _, promptTemplate := range templates {
			if mapPromptTemplate != nil {
				promptTemplate = mapPromptTemplate(promptTemplate, input.Source)
			}
			promptTemplates = append(promptTemplates, SourcedPromptTemplate[TSource]{
				PromptTemplate: promptTemplate,
				Source:         input.Source,
			})
		}
		for _, diagnostic := range diags {
			diagnostics = append(diagnostics, SourcedPromptTemplateDiagnostic[TSource]{
				PromptTemplateDiagnostic: diagnostic,
				Source:                   input.Source,
			})
		}
	}
	return promptTemplates, diagnostics
}

// loadTemplatesFromDir loads direct `.md` children of one directory in name
// order (pi: loadTemplatesFromDir).
func loadTemplatesFromDir(env ExecutionEnv, dir string) ([]PromptTemplate, []PromptTemplateDiagnostic) {
	var promptTemplates []PromptTemplate
	var diagnostics []PromptTemplateDiagnostic
	entries, err := env.ListDir(context.Background(), dir)
	if err != nil {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{
			Type:    "warning",
			Code:    PromptTemplateDiagListFailed,
			Message: err.Error(),
			Path:    dir,
		})
		return promptTemplates, diagnostics
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.Compare(entries[i].Name, entries[j].Name) < 0
	})

	for _, entry := range entries {
		kind, _ := resolveKind(env, entry, &diagnostics)
		if kind != FileKindFile || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		template, diags := loadTemplateFromFile(env, entry.Path, entry.Name)
		if template != nil {
			promptTemplates = append(promptTemplates, *template)
		}
		diagnostics = append(diagnostics, diags...)
	}
	return promptTemplates, diagnostics
}

// loadTemplateFromFile parses one markdown template file (pi:
// loadTemplateFromFile). A nil template means the file was skipped or
// failed; diagnostics carry the reason.
func loadTemplateFromFile(env ExecutionEnv, filePath string, fileName string) (*PromptTemplate, []PromptTemplateDiagnostic) {
	var diagnostics []PromptTemplateDiagnostic
	rawContent, err := env.ReadTextFile(context.Background(), filePath)
	if err != nil {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{
			Type:    "warning",
			Code:    PromptTemplateDiagReadFailed,
			Message: err.Error(),
			Path:    filePath,
		})
		return nil, diagnostics
	}

	frontmatter, body, parseErr := parseFrontmatter(rawContent)
	if parseErr != nil {
		diagnostics = append(diagnostics, PromptTemplateDiagnostic{
			Type:    "warning",
			Code:    PromptTemplateDiagParseFailed,
			Message: parseErr.Error(),
			Path:    filePath,
		})
		return nil, diagnostics
	}

	firstLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) != "" {
			firstLine = line
			break
		}
	}
	description := ""
	if desc, ok := frontmatter["description"].(string); ok {
		description = desc
	}
	if description == "" && firstLine != "" {
		// pi slices by UTF-16 code unit (firstLine.slice(0, 60)); Go uses
		// rune counts, matching the truncate-module convention.
		runes := []rune(firstLine)
		description = firstLine
		if len(runes) > 60 {
			description = string(runes[:60]) + "..."
		}
	}
	return &PromptTemplate{
		Name:        mdSuffixRe.ReplaceAllString(fileName, ""),
		Description: &description,
		Content:     body,
	}, diagnostics
}

// resolveKind resolves the effective kind of a filesystem entry, following
// symlinks through CanonicalPath when the entry kind is neither file nor
// directory (pi: resolveKind). Missing-kind entries whose target cannot be
// resolved report a file_info_failed diagnostic unless the failure is
// "not_found", and resolve to ok=false (pi's undefined).
func resolveKind(env ExecutionEnv, info FileInfo, diagnostics *[]PromptTemplateDiagnostic) (FileKind, bool) {
	if info.Kind == FileKindFile || info.Kind == FileKindDirectory {
		return info.Kind, true
	}
	canonicalPath, err := env.CanonicalPath(context.Background(), info.Path)
	if err != nil {
		if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
			*diagnostics = append(*diagnostics, PromptTemplateDiagnostic{
				Type:    "warning",
				Code:    PromptTemplateDiagFileInfoFailed,
				Message: err.Error(),
				Path:    info.Path,
			})
		}
		return "", false
	}
	target, err := env.FileInfo(context.Background(), canonicalPath)
	if err != nil {
		if fileErr, _ := AsFileError(err); fileErr == nil || fileErr.Code != FileCodeNotFound {
			*diagnostics = append(*diagnostics, PromptTemplateDiagnostic{
				Type:    "warning",
				Code:    PromptTemplateDiagFileInfoFailed,
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

// parseFrontmatter splits a `---`-fenced YAML frontmatter block (pi:
// parseFrontmatter, duplicated verbatim in prompt-templates.ts and
// skills.ts; shared once here).
func parseFrontmatter(content string) (map[string]any, string, error) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if !strings.HasPrefix(normalized, "---") {
		return map[string]any{}, normalized, nil
	}
	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return map[string]any{}, normalized, nil
	}
	endIndex += 3
	yamlString := normalized[4:endIndex]
	body := strings.TrimSpace(normalized[endIndex+4:])

	// pi: parse(yamlString) ?? {} — parse errors flow to the caller as the
	// failure branch; a null parse result becomes an empty frontmatter. A
	// non-mapping top level yields a value whose properties are all
	// undefined, modeled here by an empty map.
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(yamlString), &node); err != nil {
		return nil, "", err
	}
	frontmatter := map[string]any{}
	if len(node.Content) > 0 && node.Content[0].Kind == yaml.MappingNode {
		var decoded map[string]any
		if err := node.Content[0].Decode(&decoded); err != nil {
			return nil, "", err
		}
		frontmatter = decoded
	}
	return frontmatter, body, nil
}

// ParseCommandArgs parses an argument string using simple shell-style single
// and double quotes (pi: parseCommandArgs).
func ParseCommandArgs(argsString string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune

	for _, char := range argsString {
		if inQuote != 0 {
			if char == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(char)
			}
		} else if char == '"' || char == '\'' {
			inQuote = char
		} else if char == ' ' || char == '\t' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(char)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// substituteArgs substitutes prompt template placeholders (`$1`, `$@`,
// `$ARGUMENTS`, `${@:N}`, `${@:N:L}`) with command arguments (pi:
// substituteArgs). Passes run in pi's order on the accumulated result.
func substituteArgs(content string, args []string) string {
	result := positionalArgsRe.ReplaceAllStringFunc(content, func(match string) string {
		num := parseIntSafe(match[1:])
		if num >= 1 && num <= len(args) {
			return args[num-1]
		}
		return ""
	})
	result = sliceArgsRe.ReplaceAllStringFunc(result, func(match string) string {
		groups := sliceArgsRe.FindStringSubmatch(match)
		start := parseIntSafe(groups[1]) - 1
		if start < 0 {
			start = 0
		}
		if start > len(args) {
			start = len(args)
		}
		if groups[2] != "" {
			end := start + parseIntSafe(groups[2])
			if end > len(args) {
				end = len(args)
			}
			return strings.Join(args[start:end], " ")
		}
		return strings.Join(args[start:], " ")
	})
	allArgs := strings.Join(args, " ")
	result = strings.ReplaceAll(result, "$ARGUMENTS", allArgs)
	result = strings.ReplaceAll(result, "$@", allArgs)
	return result
}

// parseIntSafe parses a non-negative decimal integer from a regex-matched
// digit run; malformed input yields 0 (cannot occur for \d+).
func parseIntSafe(digits string) int {
	n := 0
	for _, c := range digits {
		n = n*10 + int(c-'0')
	}
	return n
}

var (
	// positionalArgsRe matches $1, $2, ... (pi: /\$(\d+)/g).
	positionalArgsRe = regexp.MustCompile(`\$(\d+)`)
	// sliceArgsRe matches ${@:N} and ${@:N:L} (pi:
	// /\$\{@:(\d+)(?::(\d+))?\}/g).
	sliceArgsRe = regexp.MustCompile(`\$\{@:(\d+)(?::(\d+))?\}`)
)

// FormatPromptTemplateInvocation formats a prompt template invocation with
// positional arguments (pi: formatPromptTemplateInvocation; pi's default
// `args = []` becomes a nil-tolerant slice).
func FormatPromptTemplateInvocation(template PromptTemplate, args []string) string {
	return substituteArgs(template.Content, args)
}
