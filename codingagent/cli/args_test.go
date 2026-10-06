// pi: test/args.test.ts — the "--mode flag" describe (e40126f57, #9045),
// first-ported with S9. The remaining parseArgs describes in args.test.ts are
// an existing gap (declared in the S9 handoff), not a regression.
package cli

import (
	"testing"
)

// TestParseArgsModeFlag ports pi's describe("--mode flag") block.
func TestParseArgsModeFlag(t *testing.T) {
	// test.each(["text", "json", "rpc"])("parses --mode %s")
	for _, mode := range []string{"text", "json", "rpc"} {
		result := ParseArgs([]string{"--mode", mode})
		if result.Mode == nil || *result.Mode != mode {
			t.Fatalf("--mode %s: mode = %v", mode, result.Mode)
		}
		if len(result.Diagnostics) != 0 {
			t.Fatalf("--mode %s: diagnostics = %v", mode, result.Diagnostics)
		}
	}

	// test.each(["yaml", ""])("rejects invalid --mode value %j")
	for _, mode := range []string{"yaml", ""} {
		result := ParseArgs([]string{"--mode", mode, "--version"})
		if result.Mode != nil {
			t.Fatalf("--mode %q: mode = %v, want unset", mode, *result.Mode)
		}
		if result.Version == nil || !*result.Version {
			t.Fatalf("--mode %q: version = %v, want true", mode, result.Version)
		}
		if len(result.Messages) != 0 {
			t.Fatalf("--mode %q: messages = %v, want empty", mode, result.Messages)
		}
		if len(result.UnknownFlags) != 0 {
			t.Fatalf("--mode %q: unknownFlags = %v, want empty", mode, result.UnknownFlags)
		}
		want := ArgDiagnostic{Type: ArgDiagnosticError, Message: "Invalid mode \"" + mode + "\". Valid values: text, json, rpc"}
		if len(result.Diagnostics) != 1 || result.Diagnostics[0] != want {
			t.Fatalf("--mode %q: diagnostics = %+v, want [%+v]", mode, result.Diagnostics, want)
		}
	}

	t.Run("reports a missing --mode value", func(t *testing.T) {
		result := ParseArgs([]string{"--mode"})
		if result.Mode != nil {
			t.Fatalf("mode = %v, want unset", *result.Mode)
		}
		if len(result.UnknownFlags) != 0 {
			t.Fatalf("unknownFlags = %v, want empty", result.UnknownFlags)
		}
		want := ArgDiagnostic{Type: ArgDiagnosticError, Message: "--mode requires text, json, or rpc"}
		if len(result.Diagnostics) != 1 || result.Diagnostics[0] != want {
			t.Fatalf("diagnostics = %+v, want [%+v]", result.Diagnostics, want)
		}
	})

	t.Run("does not consume another option as a --mode value", func(t *testing.T) {
		result := ParseArgs([]string{"--mode", "--version"})
		if result.Mode != nil {
			t.Fatalf("mode = %v, want unset", *result.Mode)
		}
		if result.Version == nil || !*result.Version {
			t.Fatalf("version = %v, want true", result.Version)
		}
		if len(result.UnknownFlags) != 0 {
			t.Fatalf("unknownFlags = %v, want empty", result.UnknownFlags)
		}
		want := ArgDiagnostic{Type: ArgDiagnosticError, Message: "--mode requires text, json, or rpc"}
		if len(result.Diagnostics) != 1 || result.Diagnostics[0] != want {
			t.Fatalf("diagnostics = %+v, want [%+v]", result.Diagnostics, want)
		}
	})

	t.Run("reports an invalid --mode value after a valid one", func(t *testing.T) {
		result := ParseArgs([]string{"--mode", "json", "--mode", "yaml"})
		want := ArgDiagnostic{Type: ArgDiagnosticError, Message: "Invalid mode \"yaml\". Valid values: text, json, rpc"}
		if len(result.Diagnostics) != 1 || result.Diagnostics[0] != want {
			t.Fatalf("diagnostics = %+v, want [%+v]", result.Diagnostics, want)
		}
	})
}

func TestParseArgsModelsIgnoresEmptyEntries(t *testing.T) {
	// pi #10334: a trailing comma produced an empty pattern that matched every
	// model by substring and added an extra model to the cycle.
	result := ParseArgs([]string{"--models", "kimi,gpt-5.5,"})
	if len(result.Models) != 2 || result.Models[0] != "kimi" || result.Models[1] != "gpt-5.5" {
		t.Fatalf("models = %v, want [kimi gpt-5.5]", result.Models)
	}
	result = ParseArgs([]string{"--models", ","})
	if len(result.Models) != 0 {
		t.Fatalf("models = %v, want empty", result.Models)
	}
}

func TestParseArgsNoMcp(t *testing.T) {
	// pi v1.0.4: --no-mcp disables the built-in MCP support for one run.
	result := ParseArgs([]string{"--no-mcp"})
	if result.NoMcp == nil || !*result.NoMcp {
		t.Fatal("--no-mcp must set NoMcp")
	}
	result = ParseArgs([]string{})
	if result.NoMcp != nil {
		t.Fatal("NoMcp must stay unset without the flag")
	}
}
