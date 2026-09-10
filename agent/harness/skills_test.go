// pi: test/harness/skills.test.ts — skill loading over the node execution
// env.
package harness_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
)

func TestLoadsSkillMdFilesThroughTheExecutionEnvironment(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, ".agents/skills/example")
	mustWriteFile(t, e, ".agents/skills/example/SKILL.md", `---
name: example
description: Example skill
disable-model-invocation: true
---
Use this skill.
`)

	skills, diagnostics := harness.LoadSkills(e, []string{".agents/skills"})

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty", diagnostics)
	}
	want := harness.Skill{
		Name:                   "example",
		Description:            "Example skill",
		Content:                "Use this skill.",
		FilePath:               filepath.Join(root, ".agents/skills/example/SKILL.md"),
		DisableModelInvocation: lo.ToPtr(true),
	}
	if len(skills) != 1 || !skillEqual(skills[0], want) {
		t.Fatalf("skills = %+v, want [%+v]", skills, want)
	}
}

func TestLoadsSkillsThroughSymlinkedDirectories(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "actual/example")
	mustWriteFile(t, e, "actual/example/SKILL.md", "---\nname: example\ndescription: Example skill\n---\nUse this skill.")
	if err := os.Symlink(filepath.Join(root, "actual"), filepath.Join(root, "skills-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	skills, _ := harness.LoadSkills(e, []string{"skills-link"})

	if len(skills) != 1 || skills[0].Name != "example" {
		t.Fatalf("skill names = %v, want [example]", skillNames(skills))
	}
	if skills[0].FilePath != filepath.Join(root, "skills-link/example/SKILL.md") {
		t.Fatalf("filePath = %s, want the addressed path through the link", skills[0].FilePath)
	}
}

func TestPreservesSourceInfoForSourcedSkills(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "user/example")
	mustWriteFile(t, e, "user/example/SKILL.md", "---\nname: example\ndescription: Example skill\n---\nUse this skill.")

	skills, diagnostics := harness.LoadSourcedSkills(e, []harness.SourcedInput[testSource]{
		{Path: "user", Source: testSource{Type: "user"}},
	}, nil)

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty", diagnostics)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %v, want 1 entry", skills)
	}
	want := harness.Skill{
		Name:                   "example",
		Description:            "Example skill",
		Content:                "Use this skill.",
		FilePath:               filepath.Join(root, "user/example/SKILL.md"),
		DisableModelInvocation: lo.ToPtr(false),
	}
	if !skillEqual(skills[0].Skill, want) {
		t.Fatalf("skills[0].Skill = %+v, want %+v", skills[0].Skill, want)
	}
	if skills[0].Source != (testSource{Type: "user"}) {
		t.Fatalf("skills[0].Source = %+v, want user", skills[0].Source)
	}
}

func TestAttachesSourceInfoToSkillDiagnostics(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "user/broken")
	mustWriteFile(t, e, "user/broken/SKILL.md", "---\nname: broken\n---\nMissing description.")

	skills, diagnostics := harness.LoadSourcedSkills(e, []harness.SourcedInput[testSource]{
		{Path: "user", Source: testSource{Type: "user"}},
	}, nil)

	if len(skills) != 0 {
		t.Fatalf("skills = %v, want empty", skills)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want 1 entry", diagnostics)
	}
	diag := diagnostics[0]
	if diag.Type != "warning" || diag.Code != harness.SkillDiagInvalidMetadata {
		t.Fatalf("diagnostic type/code = %s/%s, want warning/invalid_metadata", diag.Type, diag.Code)
	}
	if diag.Message != "description is required" {
		t.Fatalf("diagnostic message = %q, want %q", diag.Message, "description is required")
	}
	if diag.Path != filepath.Join(root, "user/broken/SKILL.md") {
		t.Fatalf("diagnostic path = %s, want %s", diag.Path, filepath.Join(root, "user/broken/SKILL.md"))
	}
	if diag.Source != (testSource{Type: "user"}) {
		t.Fatalf("diagnostic source = %+v, want user", diag.Source)
	}
}

func TestLoadsDirectMarkdownChildrenOnlyFromTheRootDirectory(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "skills/nested")
	mustWriteFile(t, e, "skills/root.md", "---\ndescription: Root skill\n---\nRoot content")
	mustWriteFile(t, e, "skills/nested/ignored.md", "---\ndescription: Ignored\n---\nIgnored content")

	skills, _ := harness.LoadSkills(e, []string{"skills"})

	if len(skills) != 1 || skills[0].Name != "skills" {
		t.Fatalf("skill names = %v, want [skills]", skillNames(skills))
	}
	if skills[0].Content != "Root content" {
		t.Fatalf("content = %q, want %q", skills[0].Content, "Root content")
	}
}

func TestIgnoresRootMarkdownDocsThatDoNotDeclareSkills(t *testing.T) {
	root := t.TempDir()
	e := newTestEnv(root)
	mustCreateDir(t, e, "skills/nested-skill")
	mustWriteFile(t, e, "skills/README.md", "# Shared skills\n\nDocumentation.")
	mustWriteFile(t, e, "skills/AGENTS.md", "# Agent notes\n\nDocumentation.")
	mustWriteFile(t, e, "skills/CLAUDE.md", "---\ndescription: [invalid\n---\n\nDocumentation.")
	mustWriteFile(t, e, "skills/root.md", "---\ndescription: Root skill\n---\nRoot content")
	mustWriteFile(t, e, "skills/nested-skill/SKILL.md", "---\nname: nested-skill\ndescription: Nested skill\n---\nNested content")

	skills, diagnostics := harness.LoadSkills(e, []string{"skills"})

	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty", diagnostics)
	}
	names := skillNames(skills)
	sort.Strings(names)
	want := []string{"nested-skill", "skills"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("skill names = %v, want %v", names, want)
	}
}

func skillNames(skills []harness.Skill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

// implementation; tests drive it through harness functions only).
