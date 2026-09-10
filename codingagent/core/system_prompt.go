// pi: src/core/system-prompt.ts — system prompt construction and project
// context loading.
//
// Segment deviations:
//   - selectedTools: pi's `selectedTools || [...]` treats an empty array as
//     present (→ "(none)"); pig distinguishes a nil slice (absent → the
//     default tool list) from an empty non-nil slice.
//   - toolSnippets: pi's Record<string, string> → map[string]string (nil =
//     absent); empty-string snippets stay falsy like pi's !! check.
package core

import (
	"fmt"
	"strings"

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
	// CustomPrompt replaces the default prompt ("" = absent).
	CustomPrompt string
	// SelectedTools lists the tools to include in the prompt; nil = absent
	// (default: [read, bash, edit, write]).
	SelectedTools []string
	// ToolSnippets are optional one-line tool snippets keyed by tool name.
	ToolSnippets map[string]string
	// PromptGuidelines are additional guideline bullets appended to the
	// default system prompt guidelines.
	PromptGuidelines []string
	// AppendSystemPrompt is text to append to the system prompt ("" =
	// absent).
	AppendSystemPrompt string
	// Cwd is the working directory.
	Cwd string
	// ContextFiles are pre-loaded context files.
	ContextFiles []ContextFile
	// Skills are pre-loaded skills.
	Skills []Skill
}

// buildSystemPromptGuidelines is the fixed guideline tail (pi: literal).
const buildSystemPromptDocsSection = `Pi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):
- Main documentation: %s
- Additional docs: %s
- Examples: %s (extensions, custom tools, SDK)
- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory
- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md), environment variables (docs/environment-variables.md)
- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing
- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)`

// BuildSystemPrompt builds the system prompt with tools, guidelines, and
// context (pi: buildSystemPrompt).
func BuildSystemPrompt(options BuildSystemPromptOptions) string {
	customPrompt := options.CustomPrompt
	selectedTools := options.SelectedTools
	toolSnippets := options.ToolSnippets
	promptGuidelines := options.PromptGuidelines
	appendSystemPrompt := options.AppendSystemPrompt
	cwd := options.Cwd
	providedContextFiles := options.ContextFiles
	providedSkills := options.Skills

	promptCwd := strings.ReplaceAll(cwd, "\\", "/")

	appendSection := ""
	if appendSystemPrompt != "" {
		appendSection = "\n\n" + appendSystemPrompt
	}

	contextFiles := providedContextFiles
	skills := providedSkills

	if customPrompt != "" {
		prompt := customPrompt

		if appendSection != "" {
			prompt += appendSection
		}

		// Append project context files
		if len(contextFiles) > 0 {
			prompt += "\n\n<project_context>\n\n"
			prompt += "Project-specific instructions and guidelines:\n\n"
			for _, contextFile := range contextFiles {
				prompt += fmt.Sprintf("<project_instructions path=%q>\n%s\n</project_instructions>\n\n", contextFile.Path, contextFile.Content)
			}
			prompt += "</project_context>\n"
		}

		// Append skills section (only if read tool is available)
		customPromptHasRead := selectedTools == nil || stringSliceContains(selectedTools, "read")
		if customPromptHasRead && len(skills) > 0 {
			prompt += FormatSkillsForPrompt(skills)
		}

		prompt += fmt.Sprintf("\nCurrent working directory: %s\n", promptCwd)

		return prompt
	}

	// Get absolute paths to documentation and examples
	readmePath := codingagent.GetReadmePath()
	docsPath := codingagent.GetDocsPath()
	examplesPath := codingagent.GetExamplesPath()

	// Build tools list based on selected tools.
	// A tool appears in Available tools only when the caller provides a
	// one-line snippet.
	tools := selectedTools
	if tools == nil {
		tools = []string{"read", "bash", "edit", "write"}
	}
	var visibleTools []string
	for _, name := range tools {
		if snippet, ok := toolSnippets[name]; ok && snippet != "" {
			visibleTools = append(visibleTools, name)
		}
	}
	toolsList := "(none)"
	if len(visibleTools) > 0 {
		var lines []string
		for _, name := range visibleTools {
			lines = append(lines, fmt.Sprintf("- %s: %s", name, toolSnippets[name]))
		}
		toolsList = strings.Join(lines, "\n")
	}

	// Build guidelines based on which tools are actually available
	var guidelinesList []string
	guidelinesSet := map[string]struct{}{}
	addGuideline := func(guideline string) {
		if _, ok := guidelinesSet[guideline]; ok {
			return
		}
		guidelinesSet[guideline] = struct{}{}
		guidelinesList = append(guidelinesList, guideline)
	}

	hasBash := stringSliceContains(tools, "bash")
	hasPowerShell := stringSliceContains(tools, "powershell")
	hasGrep := stringSliceContains(tools, "grep")
	hasFind := stringSliceContains(tools, "find")
	hasLs := stringSliceContains(tools, "ls")
	hasRead := stringSliceContains(tools, "read")

	// File exploration guidelines
	if (hasBash || hasPowerShell) && !hasGrep && !hasFind && !hasLs {
		if hasBash && hasPowerShell {
			addGuideline("Use bash or PowerShell for file operations like listing, searching, and finding files")
		} else if hasPowerShell {
			addGuideline("Use PowerShell for file operations like listing, searching, and finding files")
		} else {
			addGuideline("Use bash for file operations like ls, rg, find")
		}
	}

	for _, guideline := range promptGuidelines {
		normalized := strings.TrimSpace(guideline)
		if len(normalized) > 0 {
			addGuideline(normalized)
		}
	}

	// Always include these
	addGuideline("Be concise in your responses")
	addGuideline("Show file paths clearly when working with files")

	var guidelineLines []string
	for _, guideline := range guidelinesList {
		guidelineLines = append(guidelineLines, "- "+guideline)
	}
	guidelines := strings.Join(guidelineLines, "\n")

	prompt := fmt.Sprintf(`You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.

Available tools:
%s

In addition to the tools above, you may have access to other custom tools depending on the project.

Guidelines:
%s

%s`, toolsList, guidelines, fmt.Sprintf(buildSystemPromptDocsSection, readmePath, docsPath, examplesPath))

	if appendSection != "" {
		prompt += appendSection
	}

	// Append project context files
	if len(contextFiles) > 0 {
		prompt += "\n\n<project_context>\n\n"
		prompt += "Project-specific instructions and guidelines:\n\n"
		for _, contextFile := range contextFiles {
			prompt += fmt.Sprintf("<project_instructions path=%q>\n%s\n</project_instructions>\n\n", contextFile.Path, contextFile.Content)
		}
		prompt += "</project_context>\n"
	}

	// Append skills section (only if read tool is available)
	if hasRead && len(skills) > 0 {
		prompt += FormatSkillsForPrompt(skills)
	}

	prompt += fmt.Sprintf("\nCurrent working directory: %s", promptCwd)

	return prompt
}

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
