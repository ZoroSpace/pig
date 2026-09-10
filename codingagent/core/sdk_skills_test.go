// Ported from pi's test/sdk-skills.test.ts, adapted to pig: stub resource
// loaders reuse the testResourceLoader helper (pi builds object literals).
package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAgentSessionSkills(t *testing.T) {
	tempDir := t.TempDir()
	skillsDir := filepath.Join(tempDir, "skills", "test-skill")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test skill in the pi skills directory
	skillContent := `---
name: test-skill
description: A test skill for SDK tests.
---

# Test Skill

This is a test skill.
`
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("should discover skills by default and expose them on session", func(t *testing.T) {
		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: sessionManager,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		// pi asserts only the skills surface here; user-level skill
		// diagnostics (auto-discovered dirs) are environment-dependent and
		// not part of the assertion.
		skills, _ := result.Session.GetResourceLoader().GetSkills()
		if len(skills) == 0 {
			t.Fatal("expected skills to be discovered")
		}
		found := false
		for _, skill := range skills {
			if skill.Name == "test-skill" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected test-skill in %v", skillNames(skills))
		}
	})

	t.Run("should have empty skills when resource loader returns none", func(t *testing.T) {
		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: sessionManager,
			ResourceLoader: &testResourceLoader{},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		skills, diagnostics := result.Session.GetResourceLoader().GetSkills()
		if len(skills) != 0 {
			t.Fatalf("expected no skills, got %v", skillNames(skills))
		}
		if len(diagnostics) != 0 {
			t.Fatalf("expected no diagnostics, got %v", diagnostics)
		}
	})

	t.Run("should use provided skills when resource loader supplies them", func(t *testing.T) {
		customSkill := Skill{
			Name:        "custom-skill",
			Description: "A custom skill",
			FilePath:    "/fake/path/SKILL.md",
			BaseDir:     "/fake/path",
			SourceInfo: CreateSyntheticSourceInfo("/fake/path/SKILL.md", SyntheticSourceInfoOptions{
				Source: "sdk",
			}),
			DisableModelInvocation: false,
		}
		loader := &testResourceLoader{}
		loader.skills = []Skill{customSkill}

		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:            tempDir,
			AgentDir:       tempDir,
			SessionManager: sessionManager,
			ResourceLoader: loader,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer result.Session.Dispose()

		skills, diagnostics := result.Session.GetResourceLoader().GetSkills()
		if len(skills) != 1 || skills[0] != customSkill {
			t.Fatalf("expected the custom skill, got %+v", skills)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("expected no diagnostics, got %v", diagnostics)
		}
	})
}

func skillNames(skills []Skill) []string {
	out := make([]string, 0, len(skills))
	for _, skill := range skills {
		out = append(out, skill.Name)
	}
	return out
}
