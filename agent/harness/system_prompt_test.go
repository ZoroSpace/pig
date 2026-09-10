// pi: test/harness/system-prompt.test.ts — model-visible skills block
// formatting.
package harness_test

import (
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
)

func TestFormatsVisibleSkillsInOrderAndSkipsModelDisabledSkills(t *testing.T) {
	visibleSkill := harness.Skill{
		Name:        "visible",
		Description: "Use <this> & that",
		Content:     "visible content",
		FilePath:    "/skills/visible/SKILL.md",
	}
	secondSkill := harness.Skill{
		Name:        "second",
		Description: "Second skill",
		Content:     "second content",
		FilePath:    "/skills/second/SKILL.md",
	}
	disabledSkill := harness.Skill{
		Name:                   "hidden",
		Description:            "Hidden",
		Content:                "hidden content",
		FilePath:               "/skills/hidden/SKILL.md",
		DisableModelInvocation: lo.ToPtr(true),
	}

	got := harness.FormatSkillsForSystemPrompt([]harness.Skill{visibleSkill, disabledSkill, secondSkill})
	want := `The following skills provide specialized instructions for specific tasks.
Read the full skill file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>visible</name>
    <description>Use &lt;this&gt; &amp; that</description>
    <location>/skills/visible/SKILL.md</location>
  </skill>
  <skill>
    <name>second</name>
    <description>Second skill</description>
    <location>/skills/second/SKILL.md</location>
  </skill>
</available_skills>`
	if got != want {
		t.Fatalf("FormatSkillsForSystemPrompt =\n%q\nwant\n%q", got, want)
	}
}

func TestReturnsEmptyStringWhenNoSkillsAreModelVisible(t *testing.T) {
	disabledSkill := harness.Skill{
		Name:                   "hidden",
		Description:            "Hidden",
		Content:                "hidden content",
		FilePath:               "/skills/hidden/SKILL.md",
		DisableModelInvocation: lo.ToPtr(true),
	}
	if got := harness.FormatSkillsForSystemPrompt([]harness.Skill{disabledSkill}); got != "" {
		t.Fatalf("FormatSkillsForSystemPrompt = %q, want empty", got)
	}
}

func TestEscapesXmlInAllModelVisibleSkillFields(t *testing.T) {
	got := harness.FormatSkillsForSystemPrompt([]harness.Skill{
		{
			Name:        "a&b",
			Description: `Quote "double" and 'single'`,
			Content:     "content",
			FilePath:    `/skills/<bad>&"quote"/SKILL.md`,
		},
	})
	want := "<name>a&amp;b</name>\n    <description>Quote &quot;double&quot; and &apos;single&apos;</description>\n    <location>/skills/&lt;bad&gt;&amp;&quot;quote&quot;/SKILL.md</location>"
	if !strings.Contains(got, want) {
		t.Fatalf("FormatSkillsForSystemPrompt =\n%q\nwant to contain\n%q", got, want)
	}
}
