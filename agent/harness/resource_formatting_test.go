// pi: test/harness/resource-formatting.test.ts — skill invocation and prompt
// template invocation formatting.
package harness_test

import (
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
)

func TestFormatsSkillInvocationsWithAdditionalInstructions(t *testing.T) {
	skill := harness.Skill{
		Name:        "inspect",
		Description: "Inspect things",
		Content:     "Use inspection tools.",
		FilePath:    "/project/.pig/skills/inspect/SKILL.md",
	}

	got := harness.FormatSkillInvocation(skill, "Check errors.")
	want := "<skill name=\"inspect\" location=\"/project/.pig/skills/inspect/SKILL.md\">\n" +
		"References are relative to /project/.pig/skills/inspect.\n\n" +
		"Use inspection tools.\n</skill>\n\nCheck errors."
	if got != want {
		t.Fatalf("FormatSkillInvocation = %q, want %q", got, want)
	}
}

func TestFormatsPromptTemplateInvocationsWithPositionalArguments(t *testing.T) {
	got := harness.FormatPromptTemplateInvocation(
		harness.PromptTemplate{Name: "review", Content: "Review $1 with $ARGUMENTS"},
		[]string{"a.ts", "care"},
	)
	want := "Review a.ts with a.ts care"
	if got != want {
		t.Fatalf("FormatPromptTemplateInvocation = %q, want %q", got, want)
	}
}
