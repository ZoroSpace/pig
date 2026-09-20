// pi: src/core/system-prompt.ts — system prompt construction and project
// context loading (rewritten in pi v0.86.0 to build ordered, independently
// replaceable prompt sections that ride the transcript's system messages).
//
// Segment deviations:
//   - sections maps: pi uses plain Records (insertion order); pig uses the
//     order-preserving ai/types.SystemSections ({Keys, Values}) so request
//     rendering and JSONL replay stay reproducible.
//   - selectedTools: pi's `selectedTools ?? [...]` vs `|| [...]` — after
//     normalization the collection-complete shape always materializes the
//     default tool list for a nil slice; an explicit empty slice stays
//     empty ("(none)").
//   - normalizeBuildSystemPromptOptions deep-copies slices/maps; pig's
//     copies are shallow where pi's spread is shallow ({...input.x}),
//     except toolGuidelines whose inner arrays are copied like pi's.
package core

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/codingagent"
)

// ContextFile is one pre-loaded context file (pi: inline
// { path, content } member of BuildSystemPromptOptions).
type ContextFile struct {
	Path    string
	Content string
}

// BuildSystemPromptOptions carries the buildSystemPrompt options (pi:
// BuildSystemPromptOptions).
type BuildSystemPromptOptions struct {
	// CustomPrompt replaces the default prefix ("" = absent).
	CustomPrompt string
	// ForceSystemPrompt is the exact full prompt replacement set by a
	// before_agent_start handler (pi: forceSystemPrompt; v0.86.0). nil =
	// unset.
	ForceSystemPrompt *string
	// SelectedTools lists the tools to include in the prompt; nil = absent
	// (default: [read, bash, edit, write]).
	SelectedTools []string
	// ToolSnippets are optional one-line tool snippets keyed by tool name.
	ToolSnippets map[string]string
	// ToolGuidelines are guideline bullets contributed by each tool, keyed
	// by tool name (pi: toolGuidelines; v0.86.0).
	ToolGuidelines map[string][]string
	// PromptGuidelines are additional guideline bullets appended to the
	// default system prompt rules.
	PromptGuidelines []string
	// AppendSystemPrompt is text appended from user configuration before
	// project context, skills, and cwd ("" = absent).
	AppendSystemPrompt string
	// Sections are additional XML-wrapped prompt sections keyed by tag name
	// (pi: sections; v0.86.0). pig keeps declaration order.
	Sections *types.SystemSections
	// Cwd is the working directory.
	Cwd string
	// ContextFiles are pre-loaded context files.
	ContextFiles []ContextFile
	// Skills are pre-loaded skills.
	Skills []Skill
}

// NormalizedBuildSystemPromptOptions is the mutable, collection-complete
// shape exposed to extensions (pi: NormalizedBuildSystemPromptOptions).
type NormalizedBuildSystemPromptOptions struct {
	CustomPrompt       string
	ForceSystemPrompt  string // "" = absent (pi: forceSystemPrompt?: string)
	SelectedTools      []string
	ToolSnippets       map[string]string
	ToolGuidelines     map[string][]string
	PromptGuidelines   []string
	AppendSystemPrompt string
	Sections           *types.SystemSections
	Cwd                string
	ContextFiles       []ContextFile
	Skills             []Skill
}

// systemPromptSectionName constrains custom section names (pi:
// SYSTEM_PROMPT_SECTION_NAME).
var systemPromptSectionName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// NormalizeBuildSystemPromptOptions normalizes prompt input into the
// mutable, collection-complete shape exposed to extensions (pi:
// normalizeBuildSystemPromptOptions).
func NormalizeBuildSystemPromptOptions(input BuildSystemPromptOptions) NormalizedBuildSystemPromptOptions {
	selectedTools := input.SelectedTools
	if selectedTools == nil {
		selectedTools = []string{"read", "bash", "edit", "write"}
	}
	toolSnippets := map[string]string{}
	for name, snippet := range input.ToolSnippets {
		toolSnippets[name] = snippet
	}
	toolGuidelines := map[string][]string{}
	for name, guidelines := range input.ToolGuidelines {
		toolGuidelines[name] = append([]string{}, guidelines...)
	}
	promptGuidelines := append([]string{}, input.PromptGuidelines...)
	appendSystemPrompt := input.AppendSystemPrompt
	sections := types.SystemSections{Keys: []string{}, Values: map[string]*string{}}
	if input.Sections != nil {
		for _, name := range input.Sections.Keys {
			sections.Keys = append(sections.Keys, name)
			sections.Values[name] = input.Sections.Values[name]
		}
	}
	contextFiles := make([]ContextFile, 0, len(input.ContextFiles))
	for _, file := range input.ContextFiles {
		contextFiles = append(contextFiles, ContextFile{Path: file.Path, Content: file.Content})
	}
	skills := make([]Skill, 0, len(input.Skills))
	for _, skill := range input.Skills {
		skills = append(skills, skill)
	}
	return NormalizedBuildSystemPromptOptions{
		CustomPrompt:       input.CustomPrompt,
		ForceSystemPrompt:  derefString(input.ForceSystemPrompt),
		SelectedTools:      append([]string{}, selectedTools...),
		ToolSnippets:       toolSnippets,
		ToolGuidelines:     toolGuidelines,
		PromptGuidelines:   promptGuidelines,
		AppendSystemPrompt: appendSystemPrompt,
		Sections:           &sections,
		Cwd:                input.Cwd,
		ContextFiles:       contextFiles,
		Skills:             skills,
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// buildSystemPromptOptions converts the normalized shape back to the
// optional-shape input (pig addition: BuildSystemPrompt(input) takes the
// optional shape; the normalized options must round-trip through it).
func (o NormalizedBuildSystemPromptOptions) buildSystemPromptOptions() BuildSystemPromptOptions {
	var force *string
	if o.ForceSystemPrompt != "" {
		force = lo.ToPtr(o.ForceSystemPrompt)
	}
	return BuildSystemPromptOptions{
		CustomPrompt:       o.CustomPrompt,
		ForceSystemPrompt:  force,
		SelectedTools:      o.SelectedTools,
		ToolSnippets:       o.ToolSnippets,
		ToolGuidelines:     o.ToolGuidelines,
		PromptGuidelines:   o.PromptGuidelines,
		AppendSystemPrompt: o.AppendSystemPrompt,
		Sections:           o.Sections,
		Cwd:                o.Cwd,
		ContextFiles:       o.ContextFiles,
		Skills:             o.Skills,
	}
}

// renderProjectContext renders the project_context section body (pi:
// renderProjectContext).
func renderProjectContext(contextFiles []ContextFile) string {
	parts := []string{"Project-specific instructions and guidelines:"}
	for _, file := range contextFiles {
		parts = append(parts, fmt.Sprintf("<project_instructions path=%q>\n%s\n</project_instructions>", file.Path, file.Content))
	}
	return strings.Join(parts, "\n\n")
}

// buildSystemPromptRules builds the rules section body (pi: buildRules).
func buildSystemPromptRules(selectedTools []string, toolGuidelines map[string][]string, promptGuidelines []string) string {
	var rules []string
	seen := map[string]struct{}{}
	addRule := func(rule string) {
		normalized := strings.TrimSpace(rule)
		if normalized == "" {
			return
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		rules = append(rules, normalized)
	}

	hasBash := stringSliceContains(selectedTools, "bash")
	hasPowerShell := stringSliceContains(selectedTools, "powershell")
	hasGrep := stringSliceContains(selectedTools, "grep")
	hasFind := stringSliceContains(selectedTools, "find")
	hasLs := stringSliceContains(selectedTools, "ls")

	if (hasBash || hasPowerShell) && !hasGrep && !hasFind && !hasLs {
		if hasBash && hasPowerShell {
			addRule("Use bash or PowerShell for file operations like listing, searching, and finding files")
		} else if hasPowerShell {
			addRule("Use PowerShell for file operations like listing, searching, and finding files")
		} else {
			addRule("Use bash for file operations like ls, rg, find")
		}
	}

	for _, name := range selectedTools {
		for _, rule := range toolGuidelines[name] {
			addRule(rule)
		}
	}
	for _, rule := range promptGuidelines {
		addRule(rule)
	}
	addRule("Be concise in your responses")
	addRule("Show file paths clearly when working with files")

	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		lines = append(lines, "- "+rule)
	}
	return strings.Join(lines, "\n")
}

// systemPromptSections is an ordered, order-preserving section collection
// used while assembling the prompt.
type systemPromptSections struct {
	keys   []string
	values map[string]string
}

func newSystemPromptSections() *systemPromptSections {
	return &systemPromptSections{values: map[string]string{}}
}

func (s *systemPromptSections) set(name string, content string) {
	if _, exists := s.values[name]; !exists {
		s.keys = append(s.keys, name)
	}
	s.values[name] = content
}

func (s *systemPromptSections) get(name string) string { return s.values[name] }

// BuildSystemPromptSections builds the ordered, independently replaceable
// sections of the structured system prompt (pi: buildSystemPromptSections).
// `preamble` carries untagged text; every other section value is wrapped in
// a tag of the same name so the model can match later updates to it. The
// returned SystemSections is normalized: preamble value untagged, others
// tagged, nil absent.
func BuildSystemPromptSections(input BuildSystemPromptOptions) *types.SystemSections {
	options := NormalizeBuildSystemPromptOptions(input)
	customPrompt := options.CustomPrompt
	selectedTools := options.SelectedTools
	toolSnippets := options.ToolSnippets
	toolGuidelines := options.ToolGuidelines
	promptGuidelines := options.PromptGuidelines
	appendSystemPrompt := options.AppendSystemPrompt
	customSections := options.Sections
	cwd := options.Cwd
	contextFiles := options.ContextFiles
	skills := options.Skills

	for _, name := range customSections.Keys {
		if !systemPromptSectionName.MatchString(name) || name == "preamble" {
			panic(fmt.Sprintf("Invalid system prompt section name: %s", name))
		}
	}

	promptSections := newSystemPromptSections()
	if customPrompt != "" {
		promptSections.set("preamble", customPrompt)
	} else {
		promptSections.set("preamble", "You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.")
		var visibleTools []string
		for _, name := range selectedTools {
			if toolSnippets[name] != "" {
				visibleTools = append(visibleTools, name)
			}
		}
		tools := "(none)"
		if len(visibleTools) > 0 {
			var lines []string
			for _, name := range visibleTools {
				lines = append(lines, fmt.Sprintf("- %s: %s", name, toolSnippets[name]))
			}
			tools = strings.Join(lines, "\n")
		}
		promptSections.set("tools", fmt.Sprintf("%s\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.", tools))
		promptSections.set("rules", buildSystemPromptRules(selectedTools, toolGuidelines, promptGuidelines))
		promptSections.set("docs", fmt.Sprintf(buildSystemPromptDocsSection, codingagent.GetReadmePath(), codingagent.GetDocsPath(), codingagent.GetExamplesPath()))
	}

	if appendSystemPrompt != "" {
		promptSections.set("addendum", appendSystemPrompt)
	}
	if len(contextFiles) > 0 {
		promptSections.set("project_context", renderProjectContext(contextFiles))
	}
	skillFileReadTool := ""
	for _, candidate := range []string{"read", "bash"} {
		if stringSliceContains(selectedTools, candidate) {
			skillFileReadTool = candidate
			break
		}
	}
	if skillFileReadTool != "" && len(skills) > 0 {
		skillsPrompt := strings.TrimSpace(FormatSkillsForPrompt(skills, skillFileReadTool))
		if skillsPrompt != "" {
			promptSections.set("skills", skillsPrompt)
		}
	}
	promptSections.set("cwd", strings.ReplaceAll(cwd, "\\", "/"))
	for _, name := range customSections.Keys {
		if content := derefString(customSections.Values[name]); content != "" {
			promptSections.set(name, content)
		}
	}

	sections := &types.SystemSections{Keys: []string{"preamble"}, Values: map[string]*string{
		"preamble": lo.ToPtr(promptSections.get("preamble")),
	}}
	for _, name := range promptSections.keys {
		if name == "preamble" {
			continue
		}
		sections.Keys = append(sections.Keys, name)
		sections.Values[name] = lo.ToPtr(fmt.Sprintf("<%s>\n%s\n</%s>", name, promptSections.get(name), name))
	}
	return sections
}

// buildSystemPromptStateOutput is the complete prompt state for an input
// (pi: buildSystemPromptState's inline object).
type buildSystemPromptStateOutput struct {
	content  string
	sections *types.SystemSections
}

// BuildSystemPromptState returns the complete prompt state for `input` (pi:
// buildSystemPromptState). A forced prompt is opaque and lives in `content`
// with no sections; otherwise `content` is empty and the structured
// sections carry the prompt.
func BuildSystemPromptState(input BuildSystemPromptOptions) buildSystemPromptStateOutput {
	if input.ForceSystemPrompt != nil {
		return buildSystemPromptStateOutput{content: *input.ForceSystemPrompt}
	}
	return buildSystemPromptStateOutput{sections: BuildSystemPromptSections(input)}
}

// BuildSystemPrompt builds the system prompt text, rendered exactly as the
// transcript's system message replays it (pi: buildSystemPrompt).
func BuildSystemPrompt(input BuildSystemPromptOptions) string {
	state := BuildSystemPromptState(input)
	message := &types.SystemMessage{
		Role:      types.RoleSystem,
		Content:   types.SystemContent{String: state.content},
		Sections:  state.sections,
		Timestamp: 0,
	}
	return aiutils.GetSystemMessageText(message)
}

// DiffSystemPromptSections diffs the sections the model currently has
// (replayed from the transcript, so never nil values) against the desired
// ones. Returns a SystemSections patch — non-nil values replace, nil values
// remove — or nil when nothing changed (pi: diffSystemPromptSections).
func DiffSystemPromptSections(previous *types.SystemSections, current *types.SystemSections) *types.SystemSections {
	patch := &types.SystemSections{Keys: []string{}, Values: map[string]*string{}}
	if current != nil {
		for _, name := range current.Keys {
			text := current.Values[name]
			if previous == nil || !sectionTextEqual(previous.Values[name], text) {
				patch.Keys = append(patch.Keys, name)
				patch.Values[name] = text
			}
		}
	}
	if previous != nil {
		for _, name := range previous.Keys {
			if current == nil || current.Values[name] == nil {
				// Removals and re-additions follow the TS Map order
				// semantics: a removed key that is re-added later in the
				// patch keeps its re-addition position; a pure removal
				// appends at the end.
				if _, exists := patch.Values[name]; !exists {
					patch.Keys = append(patch.Keys, name)
				}
				patch.Values[name] = nil
			}
		}
	}
	if len(patch.Keys) == 0 {
		return nil
	}
	return patch
}

// sectionTextEqual compares two optional section values by content (pi
// compares string values; Go pointers compare by identity).
func sectionTextEqual(a *string, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// buildSystemPromptDocsSection is the fixed docs section body (pi: the docs
// template literal).
const buildSystemPromptDocsSection = `Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: %s
- Additional docs: %s
- Examples: %s (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md), environment variables (docs/environment-variables.md)
- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing
- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)`

// stringSliceContains reports whether the slice holds the value (pi:
// Array.prototype.includes).
func stringSliceContains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
