// pi: test/system-prompt.test.ts — buildSystemPrompt behavior.
package core

import (
	"os"
	"strings"
	"testing"
)

func TestBuildSystemPrompt(t *testing.T) {
	t.Run("empty tools", func(t *testing.T) {
		t.Run("shows (none) for empty tools list", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{},
				ContextFiles:  []ContextFile{},
				Skills:        []Skill{},
				Cwd:           mustGetwdForPrompt(),
			})

			if !strings.Contains(prompt, "Available tools:\n(none)") {
				t.Fatalf("prompt = %q", prompt)
			}
		})

		t.Run("shows file paths guideline even with no tools", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{},
				ContextFiles:  []ContextFile{},
				Skills:        []Skill{},
				Cwd:           mustGetwdForPrompt(),
			})

			if !strings.Contains(prompt, "Show file paths clearly") {
				t.Fatalf("prompt = %q", prompt)
			}
		})
	})

	t.Run("default tools", func(t *testing.T) {
		t.Run("includes all default tools when snippets are provided", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				ToolSnippets: map[string]string{
					"read":  "Read file contents",
					"bash":  "Execute bash commands",
					"edit":  "Make surgical edits",
					"write": "Create or overwrite files",
				},
				ContextFiles: []ContextFile{},
				Skills:       []Skill{},
				Cwd:          mustGetwdForPrompt(),
			})

			for _, tool := range []string{"read", "bash", "edit", "write"} {
				if !strings.Contains(prompt, "- "+tool+":") {
					t.Fatalf("missing tool %s in %q", tool, prompt)
				}
			}
		})

		t.Run("uses shell-specific guidance", func(t *testing.T) {
			powershell := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{"powershell"},
				ContextFiles:  []ContextFile{},
				Skills:        []Skill{},
				Cwd:           mustGetwdForPrompt(),
			})
			if !strings.Contains(powershell, "Use PowerShell for file operations") {
				t.Fatalf("prompt = %q", powershell)
			}

			both := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{"bash", "powershell"},
				ContextFiles:  []ContextFile{},
				Skills:        []Skill{},
				Cwd:           mustGetwdForPrompt(),
			})
			if !strings.Contains(both, "Use bash or PowerShell for file operations") {
				t.Fatalf("prompt = %q", both)
			}
		})

		t.Run("instructs models to resolve pi docs and examples under absolute base paths", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				ContextFiles: []ContextFile{},
				Skills:       []Skill{},
				Cwd:          mustGetwdForPrompt(),
			})

			if !strings.Contains(prompt, "- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory") {
				t.Fatalf("prompt = %q", prompt)
			}
			if !strings.Contains(prompt, "environment variables (docs/environment-variables.md)") {
				t.Fatalf("prompt = %q", prompt)
			}
		})
	})

	t.Run("custom tool snippets", func(t *testing.T) {
		t.Run("includes custom tools in available tools section when promptSnippet is provided", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{"read", "dynamic_tool"},
				ToolSnippets: map[string]string{
					"dynamic_tool": "Run dynamic test behavior",
				},
				ContextFiles: []ContextFile{},
				Skills:       []Skill{},
				Cwd:          mustGetwdForPrompt(),
			})

			if !strings.Contains(prompt, "- dynamic_tool: Run dynamic test behavior") {
				t.Fatalf("prompt = %q", prompt)
			}
		})

		t.Run("omits custom tools from available tools section when promptSnippet is not provided", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{"read", "dynamic_tool"},
				ContextFiles:  []ContextFile{},
				Skills:        []Skill{},
				Cwd:           mustGetwdForPrompt(),
			})

			if strings.Contains(prompt, "dynamic_tool") {
				t.Fatalf("dynamic_tool leaked: %q", prompt)
			}
		})
	})

	t.Run("prompt guidelines", func(t *testing.T) {
		t.Run("appends promptGuidelines to default guidelines", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools:    []string{"read", "dynamic_tool"},
				PromptGuidelines: []string{"Use dynamic_tool for project summaries."},
				ContextFiles:     []ContextFile{},
				Skills:           []Skill{},
				Cwd:              mustGetwdForPrompt(),
			})

			if !strings.Contains(prompt, "- Use dynamic_tool for project summaries.") {
				t.Fatalf("prompt = %q", prompt)
			}
		})

		t.Run("deduplicates and trims promptGuidelines", func(t *testing.T) {
			prompt := BuildSystemPrompt(BuildSystemPromptOptions{
				SelectedTools: []string{"read", "dynamic_tool"},
				PromptGuidelines: []string{
					"Use dynamic_tool for summaries.",
					"  Use dynamic_tool for summaries.  ",
					"   ",
				},
				ContextFiles: []ContextFile{},
				Skills:       []Skill{},
				Cwd:          mustGetwdForPrompt(),
			})

			if strings.Count(prompt, "- Use dynamic_tool for summaries.") != 1 {
				t.Fatalf("guideline duplicated: %q", prompt)
			}
		})
	})
}

func mustGetwdForPrompt() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return cwd
}

func TestBuildSystemPromptSkillsToolFallback(t *testing.T) {
	// pi (v0.85.1): skills are included when read OR bash is available, and
	// the section names the tool that will read them.
	testSkill := Skill{
		Name:        "test-skill",
		Description: "A test skill.",
		FilePath:    "/skills/test-skill/SKILL.md",
		BaseDir:     "/skills/test-skill",
	}

	t.Run("includes skills with only bash in the default prompt", func(t *testing.T) {
		prompt := BuildSystemPrompt(BuildSystemPromptOptions{
			SelectedTools: []string{"bash"},
			ContextFiles:  []ContextFile{},
			Skills:        []Skill{testSkill},
			Cwd:           mustGetwdForPrompt(),
		})
		if !strings.Contains(prompt, "<available_skills>") {
			t.Fatalf("prompt missing skills section: %q", prompt)
		}
		if !strings.Contains(prompt, "<name>test-skill</name>") {
			t.Fatalf("prompt missing skill name: %q", prompt)
		}
		if !strings.Contains(prompt, "Use bash to load a skill's file") {
			t.Fatalf("prompt missing bash instruction: %q", prompt)
		}
	})

	t.Run("includes skills with only bash in the custom prompt", func(t *testing.T) {
		prompt := BuildSystemPrompt(BuildSystemPromptOptions{
			CustomPrompt:  "Custom system prompt",
			SelectedTools: []string{"bash"},
			ContextFiles:  []ContextFile{},
			Skills:        []Skill{testSkill},
			Cwd:           mustGetwdForPrompt(),
		})
		if !strings.Contains(prompt, "<available_skills>") {
			t.Fatalf("prompt missing skills section: %q", prompt)
		}
		if !strings.Contains(prompt, "Use bash to load a skill's file") {
			t.Fatalf("prompt missing bash instruction: %q", prompt)
		}
	})

	t.Run("omits skills without read or bash", func(t *testing.T) {
		prompt := BuildSystemPrompt(BuildSystemPromptOptions{
			SelectedTools: []string{"write"},
			ContextFiles:  []ContextFile{},
			Skills:        []Skill{testSkill},
			Cwd:           mustGetwdForPrompt(),
		})
		if strings.Contains(prompt, "<available_skills>") {
			t.Fatalf("prompt should omit skills: %q", prompt)
		}
	})
}
