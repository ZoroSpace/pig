package mcp

import (
	"sort"
	"strings"
	"unicode"
)

// searchTools scores tools against the query and returns them best-first.
// An empty query lists all tools (stable order). Scoring is a pig-only,
// dependency-free approximation: exact/prefix/substring matches on the tool
// name dominate, whole-word matches in the description rank lower.
func searchTools(tools []ToolInfo, query string) []ToolMatch {
	query = strings.ToLower(strings.TrimSpace(query))
	matches := make([]ToolMatch, 0, len(tools))
	for _, tool := range tools {
		score := 0
		if query != "" {
			score = scoreTool(query, tool)
			if score <= 0 {
				continue
			}
		}
		matches = append(matches, ToolMatch{Tool: tool, Score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Tool.Server != matches[j].Tool.Server {
			return matches[i].Tool.Server < matches[j].Tool.Server
		}
		return matches[i].Tool.RawName < matches[j].Tool.RawName
	})
	return matches
}

const (
	scoreExact        = 1000
	scoreNamePrefix   = 700
	scoreWordBoundary = 600
	scoreNameContains = 500
	scoreAllWords     = 450
	scoreDescWords    = 200
	scoreDescContains = 120
)

func scoreTool(query string, tool ToolInfo) int {
	name := strings.ToLower(tool.Name)
	rawName := strings.ToLower(tool.RawName)
	description := strings.ToLower(tool.Description)

	switch {
	case name == query || rawName == query:
		return scoreExact
	case strings.HasPrefix(name, query):
		return scoreNamePrefix
	case containsAtWordBoundary(name, query):
		return scoreWordBoundary
	case strings.Contains(name, query):
		return scoreNameContains
	}

	words := splitWords(query)
	if len(words) > 1 && containsAllWords(name, words) {
		return scoreAllWords
	}
	if descScore := scoreDescription(words, description); descScore > 0 {
		return descScore
	}
	return 0
}

func scoreDescription(words []string, description string) int {
	if len(words) == 0 {
		return 0
	}
	if containsAllWords(description, words) {
		return scoreDescWords
	}
	for _, word := range words {
		if strings.Contains(description, word) {
			return scoreDescContains
		}
	}
	return 0
}

// containsAtWordBoundary reports whether s contains query starting at a
// word boundary (start of string, after space, -, _, . or /).
func containsAtWordBoundary(s, query string) bool {
	for i := 0; i+len(query) <= len(s); i++ {
		if i > 0 && !isWordSeparator(s[i-1]) {
			continue
		}
		if strings.HasPrefix(s[i:], query) {
			return true
		}
	}
	return false
}

func isWordSeparator(b byte) bool {
	switch b {
	case ' ', '-', '_', '.', '/', ':':
		return true
	}
	return false
}

func containsAllWords(s string, words []string) bool {
	for _, word := range words {
		if !strings.Contains(s, word) {
			return false
		}
	}
	return true
}

func splitWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		if unicode.IsSpace(r) {
			return true
		}
		return r < 128 && isWordSeparator(byte(r))
	})
}
