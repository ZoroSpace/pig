package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/cli"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/samber/lo"
)

func emptyModelRuntime(t *testing.T) *core.ModelRuntime {
	t.Helper()
	return core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials:       core.CreateAuthStorage(filepath.Join(t.TempDir(), "auth.json")),
		ModelsStore:       &core.InMemoryCodingAgentModelsStore{},
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
}

// pi: main.ts buildSessionOptions --provider fail-closed check (#10236,
// 0c453048b). --provider without --model used to be silently ignored and the
// default model from another provider ran instead. The accepted-path cases
// pass a nil ModelRuntime: model resolution then emits unrelated lookup
// diagnostics, so they assert only that the requires-model error is absent.
func TestBuildSessionOptionsProviderRequiresModel(t *testing.T) {
	provider := "anthropic"
	model := "claude"

	t.Run("provider without model is an error diagnostic", func(t *testing.T) {
		result := buildSessionOptions(&cli.Args{Provider: &provider}, nil, false, nil, nil)
		found := false
		for _, diagnostic := range result.diagnostics {
			if diagnostic.Type == "error" && diagnostic.Message == "--provider requires --model (for example: --provider anthropic --model <pattern>)" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected --provider requires --model error, got %+v", result.diagnostics)
		}
		if result.options.Model != nil {
			t.Fatalf("expected no model to be resolved, got %+v", result.options.Model)
		}
	})

	t.Run("provider with model does not require-model", func(t *testing.T) {
		result := buildSessionOptions(&cli.Args{Provider: &provider, Model: &model}, nil, false, emptyModelRuntime(t), nil)
		for _, diagnostic := range result.diagnostics {
			if diagnostic.Message == "--provider requires --model (for example: --provider anthropic --model <pattern>)" {
				t.Fatalf("unexpected requires-model diagnostic: %+v", result.diagnostics)
			}
		}
	})

	t.Run("model alone does not require-model", func(t *testing.T) {
		result := buildSessionOptions(&cli.Args{Model: &model}, nil, false, emptyModelRuntime(t), nil)
		for _, diagnostic := range result.diagnostics {
			if diagnostic.Message == "--provider requires --model (for example: --provider anthropic --model <pattern>)" {
				t.Fatalf("unexpected requires-model diagnostic: %+v", result.diagnostics)
			}
		}
	})
}
