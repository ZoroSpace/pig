// pi: src/utils/frontmatter.ts — YAML frontmatter extraction and parsing
// for coding-agent resources.
//
// Segment deviations:
//   - npm `yaml` → gopkg.in/yaml.v3 (established precedent from the harness
//     resource-loading batch). Error message wording differs between the
//     libraries (pi's test asserts "at line 1, column 10"; yaml.v3 words the
//     same unterminated-flow error differently) — parse failures are what
//     matter, the exact message is library-specific (declared).
//   - pi's generic <T> frontmatter record → map[string]any.
package utils

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// normalizeNewlines converts CRLF/CR to LF (pi: normalizeNewlines).
func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

// extractFrontmatter splits a document into its frontmatter YAML string and
// body (pi: extractFrontmatter).
func extractFrontmatter(content string) (yamlString string, body string) {
	normalized := normalizeNewlines(StripBom(content))

	if !strings.HasPrefix(normalized, "---") {
		return "", normalized
	}

	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return "", normalized
	}
	endIndex += 3

	return normalized[4:endIndex], strings.TrimSpace(normalized[endIndex+4:])
}

// ParseFrontmatter parses YAML frontmatter and returns the remaining body
// (pi: parseFrontmatter). A missing or unterminated frontmatter block leaves
// the content as the body with an empty frontmatter map.
func ParseFrontmatter(content string) (frontmatter map[string]any, body string, err error) {
	yamlString, body := extractFrontmatter(content)
	frontmatter = map[string]any{}
	if yamlString == "" {
		return frontmatter, body, nil
	}
	var parsed any
	// npm yaml applies clip chomping to a literal block even when the
	// frontmatter block is not newline-terminated; yaml.v3 needs the trailing
	// newline to produce the same value.
	if !strings.HasSuffix(yamlString, "\n") {
		yamlString += "\n"
	}
	if err := yaml.Unmarshal([]byte(yamlString), &parsed); err != nil {
		return nil, body, err
	}
	if parsedMap, ok := parsed.(map[string]any); ok {
		frontmatter = parsedMap
	}
	return frontmatter, body, nil
}

// StripFrontmatter returns the body of a document (pi: stripFrontmatter;
// pi propagates parse errors, so does pig).
func StripFrontmatter(content string) (string, error) {
	_, body, err := ParseFrontmatter(content)
	if err != nil {
		return "", err
	}
	return body, nil
}
