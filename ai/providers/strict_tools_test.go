// strict_tools_test.go — pi: test/openai-completions-tool-choice.test.ts
// v0.87.0 additions (#9816): unknown OpenAI-compatible endpoints default to
// non-strict tools, while capable built-in Chat Completions models keep
// strict tool sampling (the generated catalog marks them explicitly).
package providers

import (
	"encoding/json"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// strictPreferTool is pi's ping tool with prefer-level strict sampling.
func strictPreferTool() types.Tool {
	return types.Tool{
		Name:                "ping",
		Description:         "Ping tool",
		Parameters:          json.RawMessage(`{"type":"object","properties":{"required":{"type":"string"},"optional":{"type":"string"}},"required":["required"]}`),
		ConstrainedSampling: map[string]any{"type": "json_schema", "strict": "prefer"},
	}
}

// TestUnknownOpenAICompatDefaultsNonStrict pins #9816: a bare
// openai-completions model (no catalog compat) must not send the strict
// field, and the parameter schema passes through unchanged.
func TestUnknownOpenAICompatDefaultsNonStrict(t *testing.T) {
	model := bareModel("local", "http://localhost:11434/v1")
	captured := captureBodyWithTools(t, model, []types.Tool{strictPreferTool()})
	rawTools, ok := captured["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		t.Fatalf("tools = %v, want 1 function tool", captured["tools"])
	}
	function := rawTools[0].(map[string]any)["function"].(map[string]any)
	if _, present := function["strict"]; present {
		t.Fatalf("unknown endpoints must not send strict: %v", function)
	}
	parameters := function["parameters"].(map[string]any)
	required, _ := parameters["required"].([]any)
	if len(required) != 1 || required[0] != "required" {
		t.Fatalf("required = %v, want [required]", required)
	}
}

// TestBuiltinStrictToolPreserved pins the other half of #9816: a capable
// built-in model carries supportsStrictMode explicitly and keeps strict
// sampling (strict mode lifts optional properties into required).
func TestBuiltinStrictToolPreserved(t *testing.T) {
	model, ok := GetBuiltinModel("groq", "openai/gpt-oss-20b")
	if !ok {
		t.Fatal("groq/openai/gpt-oss-20b missing from catalog")
	}
	compat, cast := model.Compat.(*ai.OpenAICompletionsCompat)
	if !cast || compat == nil || compat.SupportsStrictMode == nil || !*compat.SupportsStrictMode {
		t.Fatalf("groq gpt-oss-20b supportsStrictMode should be true, got %+v", model.Compat)
	}
	captured := captureBodyWithTools(t, model, []types.Tool{strictPreferTool()})
	rawTools, ok := captured["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		t.Fatalf("tools = %v, want 1 function tool", captured["tools"])
	}
	function := rawTools[0].(map[string]any)["function"].(map[string]any)
	if strict, ok := function["strict"].(bool); !ok || !strict {
		t.Fatalf("strict = %v, want true", function["strict"])
	}
	parameters := function["parameters"].(map[string]any)
	required, _ := parameters["required"].([]any)
	if len(required) != 2 || required[0] != "required" || required[1] != "optional" {
		t.Fatalf("required = %v, want [required optional]", required)
	}
}
