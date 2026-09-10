// pig-side coverage for the minimatch subset (npm minimatch has no pig-side
// pins in pi's model-resolver tests).
package utils

import "testing"

func TestMinimatch(t *testing.T) {
	cases := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"anthropic/*", "anthropic/claude-4", true},
		{"anthropic/*", "anthropic/a/b", false},            // * does not cross /
		{"anthropic/**", "anthropic/a/b", true},            // ** crosses /
		{"*sonnet*", "anthropic/claude-sonnet-4-5", false}, // * does not cross /
		{"*/*sonnet*", "anthropic/claude-sonnet-4-5", true},
		{"*sonnet*", "claude-sonnet-4-5", true},
		{"anthropic/claude-*-4", "anthropic/claude-opus-4", true},
		{"anthropic/claude-?-5", "anthropic/claude-3-5", true},
		{"anthropic/claude-?-5", "anthropic/claude-34-5", false},
		{"openai/gpt-4o[ab]", "openai/gpt-4oa", true},
		{"*gpt-4o[ab]", "openai/gpt-4oa", false}, // * does not cross /
		{"openai/gpt-4o[!ab]", "openai/gpt-4oc", true},
		{"zai/glm-{5,6}", "zai/glm-5", true},
		{"zai/glm-{5,6}", "zai/glm-7", false},
		{"ANTHROPIC/*", "anthropic/x", true}, // nocase
	}
	for _, tc := range cases {
		if got := Minimatch(tc.pattern, tc.text); got != tc.want {
			t.Errorf("Minimatch(%q, %q) = %v, want %v", tc.pattern, tc.text, got, tc.want)
		}
	}
}
