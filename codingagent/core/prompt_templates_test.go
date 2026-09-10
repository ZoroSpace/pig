// pi: test/prompt-templates.test.ts — SubstituteArgs, ParseCommandArgs,
// ExpandPromptTemplate, and LoadPromptTemplates behavior.
package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// SubstituteArgs case tables (pi: the substituteArgs describes).

func TestSubstituteArgs(t *testing.T) {
	cases := []struct {
		name     string
		template string
		args     []string
		want     string
	}{
		{"replaces $ARGUMENTS with all args joined", "Test: $ARGUMENTS", []string{"a", "b", "c"}, "Test: a b c"},
		{"replaces $@ with all args joined", "Test: $@", []string{"a", "b", "c"}, "Test: a b c"},
		{"does not recursively substitute in argument values ($ARGUMENTS)", "$ARGUMENTS", []string{"$1", "$ARGUMENTS"}, "$1 $ARGUMENTS"},
		{"does not recursively substitute in argument values ($@)", "$@", []string{"$100", "$1"}, "$100 $1"},
		{"does not recursively substitute in argument values (ARGUMENTS with $100)", "$ARGUMENTS", []string{"$100", "$1"}, "$100 $1"},
		{"supports mixed $1 and $ARGUMENTS", "$1: $ARGUMENTS", []string{"prefix", "a", "b"}, "prefix: prefix a b"},
		{"supports mixed $1 and $@", "$1: $@", []string{"prefix", "a", "b"}, "prefix: prefix a b"},
		{"empty arguments with $ARGUMENTS", "Test: $ARGUMENTS", nil, "Test: "},
		{"empty arguments with $@", "Test: $@", nil, "Test: "},
		{"empty arguments with $1", "Test: $1", nil, "Test: "},
		{"multiple occurrences of $ARGUMENTS", "$ARGUMENTS and $ARGUMENTS", []string{"a", "b"}, "a b and a b"},
		{"multiple occurrences of $@", "$@ and $@", []string{"a", "b"}, "a b and a b"},
		{"mixed occurrences of $@ and $ARGUMENTS", "$@ and $ARGUMENTS", []string{"a", "b"}, "a b and a b"},
		{"special characters in arguments", "$1 $2: $ARGUMENTS", []string{"arg100", "@user"}, "arg100 @user: arg100 @user"},
		{"out-of-range numbered placeholders", "$1 $2 $3 $4 $5", []string{"a", "b"}, "a b   "},
		{"unicode characters", "$ARGUMENTS", []string{"日本語", "🎉", "café"}, "日本語 🎉 café"},
		{"newlines and tabs in argument values", "$1 $2", []string{"line1\nline2", "tab\tthere"}, "line1\nline2 tab\tthere"},
		{"consecutive dollar patterns", "$1$2", []string{"a", "b"}, "ab"},
		{"quoted arguments with spaces", "$ARGUMENTS", []string{"first arg", "second arg"}, "first arg second arg"},
		{"single argument with $ARGUMENTS", "Test: $ARGUMENTS", []string{"only"}, "Test: only"},
		{"single argument with $@", "Test: $@", []string{"only"}, "Test: only"},
		{"$0 zero index", "$0", []string{"a", "b"}, ""},
		{"decimal number in pattern", "$1.5", []string{"a"}, "a.5"},
		{"$ARGUMENTS as part of word", "pre$ARGUMENTS", []string{"a", "b"}, "prea b"},
		{"$@ as part of word", "pre$@", []string{"a", "b"}, "prea b"},
		{"empty arguments in middle of list", "$ARGUMENTS", []string{"a", "", "c"}, "a  c"},
		{"trailing and leading spaces in arguments", "$ARGUMENTS", []string{"  leading  ", "trailing  "}, "  leading   trailing  "},
		{"argument containing pattern partially", "Prefix $ARGUMENTS suffix", []string{"ARGUMENTS"}, "Prefix ARGUMENTS suffix"},
		{"non-matching patterns", "$A $$ $ $ARGS", []string{"a"}, "$A $$ $ $ARGS"},
		{"case variations", "$arguments $Arguments $ARGUMENTS", []string{"a", "b"}, "$arguments $Arguments a b"},
		{"both syntaxes same result", "$@ and $ARGUMENTS", []string{"x", "y", "z"}, "x y z and x y z"},
		{"numbered placeholders single digit", "$1 $2 $3", []string{"a", "b", "c"}, "a b c"},
		{"numbered placeholders multiple digits", "$10 $12 $15", []string{"val0", "val1", "val2", "val3", "val4", "val5", "val6", "val7", "val8", "val9", "val10", "val11", "val12", "val13", "val14"}, "val9 val11 val14"},
		{"escaped dollar signs literal backslash", `Price: \$100`, nil, `Price: \`},
		{"mixed numbered and wildcard placeholders", "$1: $@ ($ARGUMENTS)", []string{"first", "second", "third"}, "first: first second third (first second third)"},
		{"command with no placeholders", "Just plain text", []string{"a", "b"}, "Just plain text"},
		{"command with only placeholders", "$1 $2 $@", []string{"a", "b", "c"}, "a b a b c"},
		// Positional defaults
		{"positional default when missing", `List exactly ${1:-7} next steps`, nil, "List exactly 7 next steps"},
		{"positional default used when present", `List exactly ${1:-7} next steps`, []string{"3"}, "List exactly 3 next steps"},
		{"positional default when empty", `Mode: ${1:-brief}`, []string{""}, "Mode: brief"},
		{"multiple positional defaults (none)", `${1:-7} ${2:-brief}`, nil, "7 brief"},
		{"multiple positional defaults (one)", `${1:-7} ${2:-brief}`, []string{"3"}, "3 brief"},
		{"multiple positional defaults (both)", `${1:-7} ${2:-brief}`, []string{"3", "verbose"}, "3 verbose"},
		{"defaults for all arguments (empty)", "${@:-default}\n${ARGUMENTS:-default}", nil, "default\ndefault"},
		{"defaults for all arguments (present)", "${@:-default}\n${ARGUMENTS:-default}", []string{"This", "would", "be", "the", "arguments"}, "This would be the arguments\nThis would be the arguments"},
		// Array slicing
		{"slice from index ${@:2}", "${@:2}", []string{"a", "b", "c", "d"}, "b c d"},
		{"slice from index ${@:1}", "${@:1}", []string{"a", "b", "c"}, "a b c"},
		{"slice from index ${@:3}", "${@:3}", []string{"a", "b", "c", "d"}, "c d"},
		{"slice with length ${@:2:2}", "${@:2:2}", []string{"a", "b", "c", "d"}, "b c"},
		{"slice with length ${@:1:1}", "${@:1:1}", []string{"a", "b", "c"}, "a"},
		{"slice with length ${@:3:1}", "${@:3:1}", []string{"a", "b", "c", "d"}, "c"},
		{"slice with length ${@:2:3}", "${@:2:3}", []string{"a", "b", "c", "d", "e"}, "b c d"},
		{"out of range slice ${@:99}", "${@:99}", []string{"a", "b"}, ""},
		{"out of range slice ${@:5}", "${@:5}", []string{"a", "b"}, ""},
		{"out of range slice ${@:10:5}", "${@:10:5}", []string{"a", "b"}, ""},
		{"zero-length slice ${@:2:0}", "${@:2:0}", []string{"a", "b", "c"}, ""},
		{"zero-length slice ${@:1:0}", "${@:1:0}", []string{"a", "b"}, ""},
		{"length exceeding array ${@:2:99}", "${@:2:99}", []string{"a", "b", "c"}, "b c"},
		{"length exceeding array ${@:1:10}", "${@:1:10}", []string{"a", "b"}, "a b"},
		{"slice before simple $@", "${@:2} vs $@", []string{"a", "b", "c"}, "b c vs a b c"},
		{"slice and all-args mix", "First: ${@:1:1}, All: $@", []string{"x", "y", "z"}, "First: x, All: x y z"},
		{"no recursive slice substitution", "${@:1}", []string{"${@:2}", "test"}, "${@:2} test"},
		{"no recursive slice substitution 2", "${@:2}", []string{"a", "${@:3}", "c"}, "${@:3} c"},
		{"slice mixed with positional", "$1: ${@:2}", []string{"cmd", "arg1", "arg2"}, "cmd: arg1 arg2"},
		{"positional then slice", "$1 $2 ${@:3}", []string{"a", "b", "c", "d"}, "a b c d"},
		{"slice ${@:0} is all args", "${@:0}", []string{"a", "b", "c"}, "a b c"},
		{"slice with empty args ${@:2}", "${@:2}", nil, ""},
		{"slice with empty args ${@:1}", "${@:1}", nil, ""},
		{"slice single arg ${@:1}", "${@:1}", []string{"only"}, "only"},
		{"slice single arg ${@:2}", "${@:2}", []string{"only"}, ""},
		{"slice in middle of text", "Process ${@:2} with $1", []string{"tool", "file1", "file2"}, "Process file1 file2 with tool"},
		{"multiple slices in one template", "${@:1:1} and ${@:2}", []string{"a", "b", "c"}, "a and b c"},
		{"multiple slices with lengths", "${@:1:2} vs ${@:3:2}", []string{"a", "b", "c", "d", "e"}, "a b vs c d"},
		{"quoted arguments in slices", "${@:2}", []string{"cmd", "first arg", "second arg"}, "first arg second arg"},
		{"special characters in sliced args", "${@:2}", []string{"cmd", "$100", "@user", "#tag"}, "$100 @user #tag"},
		{"unicode in sliced args", "${@:1}", []string{"日本語", "🎉", "café"}, "日本語 🎉 café"},
		{"positional slice and wildcard combined", "Run $1 on ${@:2:2}, then process $@", []string{"eslint", "file1.ts", "file2.ts", "file3.ts"}, "Run eslint on file1.ts file2.ts, then process eslint file1.ts file2.ts file3.ts"},
		{"slice with no spacing", "prefix${@:2}suffix", []string{"a", "b", "c"}, "prefixb csuffix"},
		{"large slice lengths", "${@:5:100}", []string{"arg1", "arg2", "arg3", "arg4", "arg5", "arg6", "arg7", "arg8", "arg9", "arg10"}, "arg5 arg6 arg7 arg8 arg9 arg10"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := SubstituteArgs(testCase.template, testCase.args)
			if got != testCase.want {
				t.Fatalf("SubstituteArgs(%q) = %q, want %q", testCase.template, got, testCase.want)
			}
		})
	}

	t.Run("replaces $@ and $ARGUMENTS identically", func(t *testing.T) {
		args := []string{"foo", "bar", "baz"}
		if SubstituteArgs("Test: $@", args) != SubstituteArgs("Test: $ARGUMENTS", args) {
			t.Fatal("$@ and $ARGUMENTS diverged")
		}
	})

	t.Run("very long argument lists", func(t *testing.T) {
		args := make([]string, 100)
		for i := range args {
			args[i] = "arg" + strconv.Itoa(i)
		}
		if got := SubstituteArgs("$ARGUMENTS", args); got != strings.Join(args, " ") {
			t.Fatalf("long list mismatch: %q", got)
		}
	})

	t.Run("defaults with spaces", func(t *testing.T) {
		// pi: "should support defaults with spaces"
		if got := SubstituteArgs(`${1:-two words}`, []string{}); got != "two words" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("out-of-range positional default", func(t *testing.T) {
		// pi: "should support out-of-range positional defaults"
		if got := SubstituteArgs(`${3:-fallback}`, []string{"a", "b"}); got != "fallback" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("mixes positional defaults with existing placeholders", func(t *testing.T) {
		// pi: "should mix positional defaults with existing placeholders"
		if got := SubstituteArgs(`$1 ${2:-x} $ARGUMENTS`, []string{"a"}); got != "a x a" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestParseCommandArgs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple space-separated arguments", "a b c", []string{"a", "b", "c"}},
		{"quoted arguments with spaces", `"first arg" second`, []string{"first arg", "second"}},
		{"single-quoted arguments", "'first arg' second", []string{"first arg", "second"}},
		{"mixed quote styles", `"double" 'single' "double again"`, []string{"double", "single", "double again"}},
		{"empty string", "", []string{}},
		{"extra spaces", "a  b   c", []string{"a", "b", "c"}},
		{"tabs as separators", "a\tb\tc", []string{"a", "b", "c"}},
		{"quoted empty string", `"" " "`, []string{" "}},
		{"arguments with special characters", "$100 @user #tag", []string{"$100", "@user", "#tag"}},
		{"unicode characters", "日本語 🎉 café", []string{"日本語", "🎉", "café"}},
		{"newlines in quoted arguments", "\"line1\nline2\" second", []string{"line1\nline2", "second"}},
		{"unquoted newlines as separators", "label-2\n\nHere is some description #2.", []string{"label-2", "Here", "is", "some", "description", "#2."}},
		{"collapsed mixed unquoted whitespace", "a\n\n\tb  c", []string{"a", "b", "c"}},
		{"escaped quotes inside quoted strings", `"quoted \"text\""`, []string{`quoted \text\`}},
		{"trailing spaces", "a b c   ", []string{"a", "b", "c"}},
		{"leading spaces", "   a b c", []string{"a", "b", "c"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ParseCommandArgs(testCase.input)
			if len(got) != len(testCase.want) {
				t.Fatalf("ParseCommandArgs(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("ParseCommandArgs(%q) = %q, want %q", testCase.input, got, testCase.want)
				}
			}
		})
	}
}

func TestExpandPromptTemplate(t *testing.T) {
	t.Run("should split template arguments on unquoted newlines", func(t *testing.T) {
		result := ExpandPromptTemplate("/arg-test label-2\n\nHere is some description #2.", []PromptTemplate{
			{
				Name:        "arg-test",
				Description: "test",
				Content:     "- arg1: $1\n- rest: ${@:2}",
				SourceInfo:  SourceInfo{Path: "/tmp/arg-test.md", Source: "local", Scope: SourceScopeTemporary, Origin: SourceOriginTopLevel},
				FilePath:    "/tmp/arg-test.md",
			},
		})

		want := "- arg1: label-2\n- rest: Here is some description #2."
		if result != want {
			t.Fatalf("got %q, want %q", result, want)
		}
	})

	t.Run("should support template command separated from args by newline", func(t *testing.T) {
		result := ExpandPromptTemplate("/arg-test\nlabel-2", []PromptTemplate{
			{
				Name:        "arg-test",
				Description: "test",
				Content:     "arg1: $1",
				SourceInfo:  SourceInfo{Path: "/tmp/arg-test.md", Source: "local", Scope: SourceScopeTemporary, Origin: SourceOriginTopLevel},
				FilePath:    "/tmp/arg-test.md",
			},
		})

		if result != "arg1: label-2" {
			t.Fatalf("got %q", result)
		}
	})
}

func TestParseCommandArgsAndSubstituteArgsIntegration(t *testing.T) {
	t.Run("should parse and substitute together correctly", func(t *testing.T) {
		input := `Button "onClick handler" "disabled support"`
		args := ParseCommandArgs(input)
		template := "Create component $1 with features: $ARGUMENTS"
		result := SubstituteArgs(template, args)
		want := "Create component Button with features: Button onClick handler disabled support"
		if result != want {
			t.Fatalf("got %q, want %q", result, want)
		}
	})

	t.Run("should handle the example from README", func(t *testing.T) {
		input := `Button "onClick handler" "disabled support"`
		args := ParseCommandArgs(input)
		template := "Create a React component named $1 with features: $ARGUMENTS"
		result := SubstituteArgs(template, args)
		want := "Create a React component named Button with features: Button onClick handler disabled support"
		if result != want {
			t.Fatalf("got %q, want %q", result, want)
		}
	})

	t.Run("should produce same result with $@ and $ARGUMENTS", func(t *testing.T) {
		args := ParseCommandArgs("feature1 feature2 feature3")
		if SubstituteArgs("Implement: $@", args) != SubstituteArgs("Implement: $ARGUMENTS", args) {
			t.Fatal("$@ and $ARGUMENTS diverged")
		}
	})
}

func TestLoadPromptTemplatesArgumentHint(t *testing.T) {
	writeTemplate := func(t *testing.T, dir string, name string, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	findTemplate := func(templates []PromptTemplate, name string) *PromptTemplate {
		for i := range templates {
			if templates[i].Name == name {
				return &templates[i]
			}
		}
		return nil
	}

	t.Run("should parse required argument-hint from frontmatter", func(t *testing.T) {
		testDir := t.TempDir()
		writeTemplate(t, testDir, "pr", `---
description: Review PRs from URLs with structured issue and code analysis
argument-hint: "<PR-URL>"
---
You are given one or more GitHub PR URLs: $@`)

		templates := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             mustGetwdForPrompt(),
			AgentDir:        "",
			PromptPaths:     []string{testDir},
			IncludeDefaults: false,
		})

		pr := findTemplate(templates, "pr")
		if pr == nil {
			t.Fatalf("templates = %v", templates)
		}
		if pr.ArgumentHint != "<PR-URL>" {
			t.Fatalf("argumentHint = %q", pr.ArgumentHint)
		}
		if pr.Description != "Review PRs from URLs with structured issue and code analysis" {
			t.Fatalf("description = %q", pr.Description)
		}
	})

	t.Run("should parse optional argument-hint from frontmatter", func(t *testing.T) {
		testDir := t.TempDir()
		writeTemplate(t, testDir, "wr", `---
description: Finish the current task end-to-end with changelog, commit, and push
argument-hint: "[instructions]"
---
Wrap it. Additional instructions: $ARGUMENTS`)

		templates := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             mustGetwdForPrompt(),
			AgentDir:        "",
			PromptPaths:     []string{testDir},
			IncludeDefaults: false,
		})

		wr := findTemplate(templates, "wr")
		if wr == nil {
			t.Fatalf("templates = %v", templates)
		}
		if wr.ArgumentHint != "[instructions]" {
			t.Fatalf("argumentHint = %q", wr.ArgumentHint)
		}
	})

	t.Run("should leave argumentHint undefined when not specified", func(t *testing.T) {
		testDir := t.TempDir()
		writeTemplate(t, testDir, "cl", `---
description: Audit changelog entries before release
---
Audit changelog entries for all commits since the last release.`)

		templates := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             mustGetwdForPrompt(),
			AgentDir:        "",
			PromptPaths:     []string{testDir},
			IncludeDefaults: false,
		})

		cl := findTemplate(templates, "cl")
		if cl == nil {
			t.Fatalf("templates = %v", templates)
		}
		if cl.ArgumentHint != "" {
			t.Fatalf("argumentHint = %q", cl.ArgumentHint)
		}
	})

	t.Run("should ignore empty argument-hint", func(t *testing.T) {
		testDir := t.TempDir()
		writeTemplate(t, testDir, "empty-hint", `---
description: A command with empty hint
argument-hint: ""
---
Do something`)

		templates := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             mustGetwdForPrompt(),
			AgentDir:        "",
			PromptPaths:     []string{testDir},
			IncludeDefaults: false,
		})

		tmpl := findTemplate(templates, "empty-hint")
		if tmpl == nil {
			t.Fatalf("templates = %v", templates)
		}
		if tmpl.ArgumentHint != "" {
			t.Fatalf("argumentHint = %q", tmpl.ArgumentHint)
		}
	})

	t.Run("should preserve argument-hint with special characters", func(t *testing.T) {
		testDir := t.TempDir()
		writeTemplate(t, testDir, "is", `---
description: Analyze GitHub issues (bugs or feature requests)
argument-hint: "<issue>"
---
Analyze GitHub issue(s): $ARGUMENTS`)

		templates := LoadPromptTemplates(LoadPromptTemplatesOptions{
			Cwd:             mustGetwdForPrompt(),
			AgentDir:        "",
			PromptPaths:     []string{testDir},
			IncludeDefaults: false,
		})

		is := findTemplate(templates, "is")
		if is == nil {
			t.Fatalf("templates = %v", templates)
		}
		if is.ArgumentHint != "<issue>" {
			t.Fatalf("argumentHint = %q", is.ArgumentHint)
		}
	})
}
