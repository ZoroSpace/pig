// pi: test/skills.test.ts — coding-agent skill loading and prompt
// formatting. Fixtures live under testdata/ (pi: test/fixtures/).
package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturesDir mirrors pi's fixtures/skills.
var fixturesDir = mustJoin("testdata", "skills")

// collisionFixturesDir mirrors pi's fixtures/skills-collision.
var collisionFixturesDir = mustJoin("testdata", "skills-collision")

func mustJoin(elements ...string) string {
	joined, err := filepath.Abs(filepath.Join(elements...))
	if err != nil {
		panic(err)
	}
	return joined
}

func createTestSkill(options struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	DisableModelInvocation bool
	Source                 string
}) Skill {
	source := options.Source
	if source == "" {
		source = "test"
	}
	return Skill{
		Name:                   options.Name,
		Description:            options.Description,
		FilePath:               options.FilePath,
		BaseDir:                options.BaseDir,
		SourceInfo:             CreateSyntheticSourceInfo(options.FilePath, SyntheticSourceInfoOptions{Source: source}),
		DisableModelInvocation: options.DisableModelInvocation,
	}
}

func skillsFixtureDir(t *testing.T, elements ...string) string {
	t.Helper()
	return filepath.Join(append([]string{fixturesDir}, elements...)...)
}

func TestLoadSkillsFromDir(t *testing.T) {
	t.Run("should load a valid skill", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "valid-skill"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if result.Skills[0].Name != "valid-skill" {
			t.Fatalf("name = %q", result.Skills[0].Name)
		}
		if result.Skills[0].Description != "A valid skill for testing purposes." {
			t.Fatalf("description = %q", result.Skills[0].Description)
		}
		if result.Skills[0].SourceInfo.Source != "test" {
			t.Fatalf("source = %q", result.Skills[0].SourceInfo.Source)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should allow names that don't match parent directory", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "name-mismatch"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].Name != "different-name" {
			t.Fatalf("skills = %v", result.Skills)
		}
		for _, d := range result.Diagnostics {
			if strings.Contains(d.Message, "does not match parent directory") {
				t.Fatalf("unexpected diagnostic: %v", d.Message)
			}
		}
	})

	t.Run("should warn when name contains invalid characters", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "invalid-name-chars"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "invalid characters") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should warn when name exceeds 64 characters", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "long-name"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "exceeds 64 characters") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should warn and skip skill when description is missing", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "missing-description"), Source: "test"})

		if len(result.Skills) != 0 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "description is required") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should ignore unknown frontmatter fields", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "unknown-field"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should load nested skills recursively", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "nested"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].Name != "child-skill" {
			t.Fatalf("skills = %v", result.Skills)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should prefer a directory's root SKILL.md over nested SKILL.md files", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "root-skill-preferred"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].Name != "root-skill-preferred" {
			t.Fatalf("skills = %v", result.Skills)
		}
		if result.Skills[0].Description != "Root skill should win." {
			t.Fatalf("description = %q", result.Skills[0].Description)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should skip files without frontmatter", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "no-frontmatter"), Source: "test"})

		// no-frontmatter has no description, so it should be skipped
		if len(result.Skills) != 0 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "description is required") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should warn and skip skill when YAML frontmatter is invalid", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "invalid-yaml"), Source: "test"})

		if len(result.Skills) != 0 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "line") {
			t.Fatalf("diagnostics = %v (want a yaml line error)", result.Diagnostics)
		}
	})

	t.Run("should preserve multiline descriptions from YAML", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "multiline-description"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !strings.Contains(result.Skills[0].Description, "\n") {
			t.Fatalf("description = %q", result.Skills[0].Description)
		}
		if !strings.Contains(result.Skills[0].Description, "This is a multiline description.") {
			t.Fatalf("description = %q", result.Skills[0].Description)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should warn when name contains consecutive hyphens", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "consecutive-hyphens"), Source: "test"})

		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "consecutive hyphens") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should load all skills from fixture directory", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: fixturesDir, Source: "test"})

		// Should load all skills that have descriptions (even with warnings)
		// valid-skill, name-mismatch, invalid-name-chars, long-name,
		// unknown-field, nested/child-skill, consecutive-hyphens
		// NOT: missing-description, no-frontmatter (both missing descriptions)
		if len(result.Skills) < 6 {
			t.Fatalf("skills = %d (%v)", len(result.Skills), result.Skills)
		}
	})

	t.Run("should return empty for non-existent directory", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: "/non/existent/path", Source: "test"})

		if len(result.Skills) != 0 || len(result.Diagnostics) != 0 {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("should use parent directory name when name not in frontmatter", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "valid-skill"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].Name != "valid-skill" {
			t.Fatalf("skills = %v", result.Skills)
		}
	})

	t.Run("should parse disable-model-invocation frontmatter field", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "disable-model-invocation"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].Name != "disable-model-invocation" {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !result.Skills[0].DisableModelInvocation {
			t.Fatal("disableModelInvocation = false, want true")
		}
		for _, d := range result.Diagnostics {
			if strings.Contains(d.Message, "unknown frontmatter field") {
				t.Fatalf("unexpected diagnostic: %v", d.Message)
			}
		}
	})

	t.Run("should default disableModelInvocation to false when not specified", func(t *testing.T) {
		result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: skillsFixtureDir(t, "valid-skill"), Source: "test"})

		if len(result.Skills) != 1 || result.Skills[0].DisableModelInvocation {
			t.Fatalf("skills = %v", result.Skills)
		}
	})
}

func hasDiagnostic(diagnostics []ResourceDiagnostic, needle string) bool {
	for _, d := range diagnostics {
		if strings.Contains(d.Message, needle) {
			return true
		}
	}
	return false
}

func TestFormatSkillsForPrompt(t *testing.T) {
	t.Run("should return empty string for no skills", func(t *testing.T) {
		if got := FormatSkillsForPrompt(nil); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	testSkill := func() []Skill {
		return []Skill{createTestSkill(struct {
			Name                   string
			Description            string
			FilePath               string
			BaseDir                string
			DisableModelInvocation bool
			Source                 string
		}{
			Name:        "test-skill",
			Description: "A test skill.",
			FilePath:    "/path/to/skill/SKILL.md",
			BaseDir:     "/path/to/skill",
		})}
	}

	t.Run("should format skills as XML", func(t *testing.T) {
		result := FormatSkillsForPrompt(testSkill())

		for _, needle := range []string{
			"<available_skills>", "</available_skills>", "<skill>",
			"<name>test-skill</name>", "<description>A test skill.</description>",
			"<location>/path/to/skill/SKILL.md</location>",
		} {
			if !strings.Contains(result, needle) {
				t.Fatalf("missing %q in %q", needle, result)
			}
		}
	})

	t.Run("should include intro text before XML", func(t *testing.T) {
		result := FormatSkillsForPrompt(testSkill())
		xmlStart := strings.Index(result, "<available_skills>")
		introText := result[:xmlStart]

		if !strings.Contains(introText, "The following skills provide specialized instructions") {
			t.Fatalf("intro = %q", introText)
		}
		if !strings.Contains(introText, "Use the read tool to load a skill's file") {
			t.Fatalf("intro = %q", introText)
		}
	})

	t.Run("should escape XML special characters", func(t *testing.T) {
		skills := []Skill{createTestSkill(struct {
			Name                   string
			Description            string
			FilePath               string
			BaseDir                string
			DisableModelInvocation bool
			Source                 string
		}{
			Name:        "test-skill",
			Description: `A skill with <special> & "characters".`,
			FilePath:    "/path/to/skill/SKILL.md",
			BaseDir:     "/path/to/skill",
		})}

		result := FormatSkillsForPrompt(skills)

		if !strings.Contains(result, "&lt;special&gt;") || !strings.Contains(result, "&amp;") || !strings.Contains(result, "&quot;characters&quot;") {
			t.Fatalf("result = %q", result)
		}
	})

	t.Run("should format multiple skills", func(t *testing.T) {
		skills := []Skill{
			createTestSkill(struct {
				Name                   string
				Description            string
				FilePath               string
				BaseDir                string
				DisableModelInvocation bool
				Source                 string
			}{Name: "skill-one", Description: "First skill.", FilePath: "/path/one/SKILL.md", BaseDir: "/path/one"}),
			createTestSkill(struct {
				Name                   string
				Description            string
				FilePath               string
				BaseDir                string
				DisableModelInvocation bool
				Source                 string
			}{Name: "skill-two", Description: "Second skill.", FilePath: "/path/two/SKILL.md", BaseDir: "/path/two"}),
		}

		result := FormatSkillsForPrompt(skills)

		if !strings.Contains(result, "<name>skill-one</name>") || !strings.Contains(result, "<name>skill-two</name>") {
			t.Fatalf("result = %q", result)
		}
		if strings.Count(result, "<skill>") != 2 {
			t.Fatalf("skill count in %q", result)
		}
	})

	t.Run("should exclude skills with disableModelInvocation from prompt", func(t *testing.T) {
		skills := []Skill{
			createTestSkill(struct {
				Name                   string
				Description            string
				FilePath               string
				BaseDir                string
				DisableModelInvocation bool
				Source                 string
			}{Name: "visible-skill", Description: "A visible skill.", FilePath: "/path/visible/SKILL.md", BaseDir: "/path/visible"}),
			createTestSkill(struct {
				Name                   string
				Description            string
				FilePath               string
				BaseDir                string
				DisableModelInvocation bool
				Source                 string
			}{Name: "hidden-skill", Description: "A hidden skill.", FilePath: "/path/hidden/SKILL.md", BaseDir: "/path/hidden", DisableModelInvocation: true}),
		}

		result := FormatSkillsForPrompt(skills)

		if !strings.Contains(result, "<name>visible-skill</name>") {
			t.Fatalf("result = %q", result)
		}
		if strings.Contains(result, "<name>hidden-skill</name>") {
			t.Fatalf("hidden skill leaked: %q", result)
		}
		if strings.Count(result, "<skill>") != 1 {
			t.Fatalf("skill count in %q", result)
		}
	})

	t.Run("should return empty string when all skills have disableModelInvocation", func(t *testing.T) {
		skills := []Skill{createTestSkill(struct {
			Name                   string
			Description            string
			FilePath               string
			BaseDir                string
			DisableModelInvocation bool
			Source                 string
		}{Name: "hidden-skill", Description: "A hidden skill.", FilePath: "/path/hidden/SKILL.md", BaseDir: "/path/hidden", DisableModelInvocation: true})}

		if got := FormatSkillsForPrompt(skills); got != "" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLoadSkillsWithOptions(t *testing.T) {
	emptyAgentDir := mustJoin("testdata", "empty-agent")
	emptyCwd := mustJoin("testdata", "empty-cwd")

	t.Run("should load from explicit skillPaths", func(t *testing.T) {
		result := LoadSkills(LoadSkillsOptions{
			AgentDir:        emptyAgentDir,
			Cwd:             emptyCwd,
			SkillPaths:      []string{skillsFixtureDir(t, "valid-skill")},
			IncludeDefaults: true,
		})
		if len(result.Skills) != 1 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if result.Skills[0].SourceInfo.Scope != SourceScopeTemporary {
			t.Fatalf("scope = %q", result.Skills[0].SourceInfo.Scope)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should warn when skill path does not exist", func(t *testing.T) {
		result := LoadSkills(LoadSkillsOptions{
			AgentDir:        emptyAgentDir,
			Cwd:             emptyCwd,
			SkillPaths:      []string{"/non/existent/path"},
			IncludeDefaults: true,
		})
		if len(result.Skills) != 0 {
			t.Fatalf("skills = %v", result.Skills)
		}
		if !hasDiagnostic(result.Diagnostics, "does not exist") {
			t.Fatalf("diagnostics = %v", result.Diagnostics)
		}
	})

	t.Run("should expand ~ in skillPaths", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		homeSkillsDir := filepath.Join(home, ".pig", "agent", "skills")
		withTilde := LoadSkills(LoadSkillsOptions{
			AgentDir:        emptyAgentDir,
			Cwd:             emptyCwd,
			SkillPaths:      []string{filepath.Join(home, ".pig", "agent", "skills")},
			IncludeDefaults: true,
		})
		withoutTilde := LoadSkills(LoadSkillsOptions{
			AgentDir:        emptyAgentDir,
			Cwd:             emptyCwd,
			SkillPaths:      []string{homeSkillsDir},
			IncludeDefaults: true,
		})
		if len(withTilde.Skills) != len(withoutTilde.Skills) {
			t.Fatalf("tilde = %d, raw = %d", len(withTilde.Skills), len(withoutTilde.Skills))
		}
	})
}

func TestSkillCollisionHandling(t *testing.T) {
	t.Run("should detect name collisions and keep first skill", func(t *testing.T) {
		// Load from each directory
		first := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: filepath.Join(collisionFixturesDir, "first"), Source: "first"})
		second := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: filepath.Join(collisionFixturesDir, "second"), Source: "second"})

		// Simulate the collision behavior from LoadSkills()
		skillMap := map[string]Skill{}
		type collisionWarning struct {
			skillPath string
			message   string
		}
		var collisionWarnings []collisionWarning

		for _, skill := range first.Skills {
			skillMap[skill.Name] = skill
		}

		for _, skill := range second.Skills {
			if existing, ok := skillMap[skill.Name]; ok {
				collisionWarnings = append(collisionWarnings, collisionWarning{
					skillPath: skill.FilePath,
					message:   "name collision: \"" + skill.Name + "\" already loaded from " + existing.FilePath,
				})
			} else {
				skillMap[skill.Name] = skill
			}
		}

		if len(skillMap) != 1 {
			t.Fatalf("skillMap = %v", skillMap)
		}
		if skillMap["calendar"].SourceInfo.Source != "first" {
			t.Fatalf("winner = %+v", skillMap["calendar"])
		}
		if len(collisionWarnings) != 1 {
			t.Fatalf("warnings = %v", collisionWarnings)
		}
		if !strings.Contains(collisionWarnings[0].message, "name collision") {
			t.Fatalf("warning = %q", collisionWarnings[0].message)
		}
	})
}
