package core

import "testing"

// Ports pi's v1.0.4 tool-pattern tests (mcp-servers coverage in
// agent-session-mcp.test.ts + the 5109-exclude-tools regression).
func TestToolNameMatcher(t *testing.T) {
	t.Run("exact names only", func(t *testing.T) {
		match := toolNameMatcher([]string{"read", "bash"})
		if !match("read") || !match("bash") {
			t.Fatal("named tools must match")
		}
		if match("grep") {
			t.Fatal("unlisted tool must not match")
		}
	})

	t.Run("star pattern matches any characters", func(t *testing.T) {
		match := toolNameMatcher([]string{"mcp__radius__*"})
		if !match("mcp__radius__search") {
			t.Fatal("pattern must match server tool")
		}
		if match("mcp__other__search") {
			t.Fatal("pattern must not match other server")
		}
	})

	t.Run("leading and trailing patterns", func(t *testing.T) {
		match := toolNameMatcher([]string{"*search*", "edit"})
		if !match("mcp__radius__search") || !match("web_search") || !match("edit") {
			t.Fatal("substring pattern must match")
		}
		if match("read") {
			t.Fatal("pattern must not match unrelated tool")
		}
	})

	t.Run("regex metacharacters stay literal (pi #5109)", func(t *testing.T) {
		match := toolNameMatcher([]string{"a.b+c?"})
		if !match("a.b+c?") {
			t.Fatal("literal name must match itself")
		}
		if match("axb") || match("a.bcc") {
			t.Fatal("metacharacters must not act as wildcards")
		}
	})

	t.Run("empty entries match nothing", func(t *testing.T) {
		match := toolNameMatcher([]string{})
		if match("read") {
			t.Fatal("empty allowlist must match nothing")
		}
	})
}
