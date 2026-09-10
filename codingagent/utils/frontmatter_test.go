// pi: test/frontmatter.test.ts — parseFrontmatter / stripFrontmatter.
//
// Deviation: the "throws on invalid YAML frontmatter" case asserts pi's
// npm-yaml message shape ("at line 1, column 10"); yaml.v3 words the same
// unterminated-flow error differently, so the Go test locks only the parse
// failure (declared in frontmatter.go).
package utils

import (
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	t.Run("parses keys, strips quotes, and returns body", func(t *testing.T) {
		input := "---\nname: \"skill-name\"\ndescription: 'A desc'\nfoo-bar: value\n---\n\nBody text"
		frontmatter, body, err := ParseFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if frontmatter["name"] != "skill-name" {
			t.Fatalf("name = %v", frontmatter["name"])
		}
		if frontmatter["description"] != "A desc" {
			t.Fatalf("description = %v", frontmatter["description"])
		}
		if frontmatter["foo-bar"] != "value" {
			t.Fatalf("foo-bar = %v", frontmatter["foo-bar"])
		}
		if body != "Body text" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("normalizes newlines and handles CRLF", func(t *testing.T) {
		input := "---\r\nname: test\r\n---\r\nLine one\r\nLine two"
		_, body, err := ParseFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if body != "Line one\nLine two" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("throws on invalid YAML frontmatter", func(t *testing.T) {
		input := "---\nfoo: [bar\n---\nBody"
		_, _, err := ParseFrontmatter(input)
		if err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("parses | multiline yaml syntax", func(t *testing.T) {
		input := "---\ndescription: |\n  Line one\n  Line two\n---\n\nBody"
		frontmatter, body, err := ParseFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if frontmatter["description"] != "Line one\nLine two\n" {
			t.Fatalf("description = %q", frontmatter["description"])
		}
		if body != "Body" {
			t.Fatalf("body = %q", body)
		}
	})

	t.Run("returns original content when frontmatter is missing or unterminated", func(t *testing.T) {
		noFrontmatter := "Just text\nsecond line"
		missingEnd := "---\nname: test\nBody without terminator"
		_, bodyNoFrontmatter, err := ParseFrontmatter(noFrontmatter)
		if err != nil {
			t.Fatal(err)
		}
		if bodyNoFrontmatter != "Just text\nsecond line" {
			t.Fatalf("body = %q", bodyNoFrontmatter)
		}
		_, bodyMissingEnd, err := ParseFrontmatter(missingEnd)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(missingEnd)
		if bodyMissingEnd != want {
			t.Fatalf("body = %q (want %q)", bodyMissingEnd, want)
		}
	})

	t.Run("returns empty object for empty or comment-only frontmatter", func(t *testing.T) {
		input := "---\n# just a comment\n---\nBody"
		frontmatter, _, err := ParseFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(frontmatter) != 0 {
			t.Fatalf("frontmatter = %v", frontmatter)
		}
	})
}

func TestStripFrontmatter(t *testing.T) {
	t.Run("removes frontmatter and trims body", func(t *testing.T) {
		input := "---\nkey: value\n---\n\nBody\n"
		got, err := StripFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != "Body" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("returns body when no frontmatter present", func(t *testing.T) {
		input := "\n  No frontmatter body  \n"
		got, err := StripFrontmatter(input)
		if err != nil {
			t.Fatal(err)
		}
		if got != "\n  No frontmatter body  \n" {
			t.Fatalf("got %q", got)
		}
	})
}
