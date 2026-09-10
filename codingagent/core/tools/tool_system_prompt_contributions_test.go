// pi: test/tool-system-prompt-contributions.test.ts — alignment between the
// built-in tools' definitions and their system prompt contributions.
package tools

import (
	"reflect"
	"testing"

	"github.com/samber/lo"
)

func TestBuiltInToolSystemPromptContributions(t *testing.T) {
	cases := []struct {
		name       string
		definition *ToolDefinition
		snippet    string
		guidelines []string
	}{
		{"read", CreateReadToolDefinition("/workspace", nil), ReadToolSystemPromptContribution.Snippet, ReadToolSystemPromptContribution.Guidelines},
		{"bash", CreateBashToolDefinition("/workspace", nil), BashToolSystemPromptContribution.Snippet, BashToolSystemPromptContribution.Guidelines},
		{"powershell", CreatePowerShellToolDefinition("/workspace", nil), PowerShellToolSystemPromptContribution.Snippet, PowerShellToolSystemPromptContribution.Guidelines},
		{"edit", CreateEditToolDefinition("/workspace", nil), EditToolSystemPromptContribution.Snippet, EditToolSystemPromptContribution.Guidelines},
		{"write", CreateWriteToolDefinition("/workspace", nil), WriteToolSystemPromptContribution.Snippet, WriteToolSystemPromptContribution.Guidelines},
		{"grep", CreateGrepToolDefinition("/workspace", nil), GrepToolSystemPromptContribution.Snippet, GrepToolSystemPromptContribution.Guidelines},
		{"find", CreateFindToolDefinition("/workspace", nil), FindToolSystemPromptContribution.Snippet, FindToolSystemPromptContribution.Guidelines},
		{"ls", CreateLsToolDefinition("/workspace", nil), LsToolSystemPromptContribution.Snippet, LsToolSystemPromptContribution.Guidelines},
	}

	for _, testCase := range cases {
		t.Run("keeps the "+testCase.name+" tool definition aligned with its contribution", func(t *testing.T) {
			if testCase.definition.PromptSnippet != testCase.snippet {
				t.Fatalf("promptSnippet = %q, want %q", testCase.definition.PromptSnippet, testCase.snippet)
			}
			// pi: (definition.promptGuidelines ?? []) toEqual
			// (contribution.guidelines ?? []) — normalize both nil sides.
			var got []string
			if testCase.definition.PromptGuidelines != nil {
				got = testCase.definition.PromptGuidelines
			}
			var want []string
			if testCase.guidelines != nil {
				want = testCase.guidelines
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("promptGuidelines = %v, want %v", got, want)
			}
		})
	}

	t.Run("keeps session-environment guidance conditional", func(t *testing.T) {
		bash := CreateBashToolDefinition("/workspace", &BashToolOptions{ExposeSessionEnvironment: lo.ToPtr(false)})
		if bash.PromptGuidelines != nil {
			t.Fatalf("bash promptGuidelines = %v, want undefined", bash.PromptGuidelines)
		}

		powershell := CreatePowerShellToolDefinition("/workspace", &PowerShellToolOptions{ExposeSessionEnvironment: lo.ToPtr(false)})
		if powershell.PromptGuidelines != nil {
			t.Fatalf("powershell promptGuidelines = %v, want undefined", powershell.PromptGuidelines)
		}
	})
}
