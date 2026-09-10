// session_selector_search.go (pi: components/session-selector-search.ts) —
// session search query parsing (tokens / quoted phrases / re: regex),
// matching, and sort/filter helpers shared by the /resume session selector.
package components

import (
	"regexp"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// SortMode is the session list sort mode (pi: SortMode).
type SortMode string

const (
	SortModeThreaded  SortMode = "threaded"
	SortModeRecent    SortMode = "recent"
	SortModeRelevance SortMode = "relevance"
)

// NameFilter is the session name filter (pi: NameFilter).
type NameFilter string

const (
	NameFilterAll   NameFilter = "all"
	NameFilterNamed NameFilter = "named"
)

// SearchTokenKind discriminates fuzzy vs phrase tokens (pi: token.kind).
type SearchTokenKind string

const (
	// TokenKindFuzzy is a plain fuzzy token (pi: "fuzzy").
	TokenKindFuzzy SearchTokenKind = "fuzzy"
	// TokenKindPhrase is a quoted phrase token (pi: "phrase").
	TokenKindPhrase SearchTokenKind = "phrase"
)

// SearchToken is one parsed search token (pi: tokens entry).
type SearchToken struct {
	Kind  SearchTokenKind
	Value string
}

// ParsedSearchQuery is the parsed form of a search query (pi:
// ParsedSearchQuery). Mode is "tokens" or "regex". Error is set when parsing
// failed and the query should be treated as non-matching.
type ParsedSearchQuery struct {
	Mode   string
	Tokens []SearchToken
	Regex  *regexp.Regexp
	Error  string
}

// MatchResult is the outcome of matching one session against a parsed query
// (pi: MatchResult). Score is lower-is-better and only meaningful when
// Matches is true.
type MatchResult struct {
	Matches bool
	Score   float64
}

func normalizeWhitespaceLower(text string) string {
	// pi: text.toLowerCase().replace(/\s+/g, " ").trim().
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func getSessionSearchText(session *core.SessionInfo) string {
	name := ""
	if session.Name != nil {
		name = *session.Name
	}
	return session.ID + " " + name + " " + session.AllMessagesText + " " + session.Cwd
}

// HasSessionName reports whether the session has a non-blank display name
// (pi: hasSessionName).
func HasSessionName(session *core.SessionInfo) bool {
	return session.Name != nil && strings.TrimSpace(*session.Name) != ""
}

func matchesNameFilter(session *core.SessionInfo, filter NameFilter) bool {
	if filter == NameFilterAll {
		return true
	}
	return HasSessionName(session)
}

// ParseSearchQuery parses a search query into tokens or a regex (pi:
// parseSearchQuery). "re:<pattern>" switches to case-insensitive regex mode;
// quoted segments become phrase tokens; unbalanced quotes fall back to plain
// whitespace tokenization.
func ParseSearchQuery(query string) ParsedSearchQuery {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return ParsedSearchQuery{Mode: "tokens", Tokens: []SearchToken{}}
	}

	// Regex mode: re:<pattern>
	if strings.HasPrefix(trimmed, "re:") {
		pattern := strings.TrimSpace(trimmed[3:])
		if pattern == "" {
			return ParsedSearchQuery{Mode: "regex", Regex: nil, Error: "Empty regex"}
		}
		// pi: new RegExp(pattern, "i") — case-insensitive.
		regex, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return ParsedSearchQuery{Mode: "regex", Regex: nil, Error: err.Error()}
		}
		return ParsedSearchQuery{Mode: "regex", Regex: regex}
	}

	// Token mode with quote support.
	// Example: foo "node cve" bar
	tokens := []SearchToken{}
	var buf strings.Builder
	inQuote := false
	hadUnclosedQuote := false

	flush := func(kind SearchTokenKind) {
		v := strings.TrimSpace(buf.String())
		buf.Reset()
		if v == "" {
			return
		}
		tokens = append(tokens, SearchToken{Kind: kind, Value: v})
	}

	for _, ch := range trimmed {
		if ch == '"' {
			if inQuote {
				flush(TokenKindPhrase)
				inQuote = false
			} else {
				flush(TokenKindFuzzy)
				inQuote = true
			}
			continue
		}

		// pi: /\s/.test(ch) — JS \s is the Unicode whitespace set; IsSpace
		// covers it for practical inputs (declared deviation).
		if !inQuote && isJsWhitespace(ch) {
			flush(TokenKindFuzzy)
			continue
		}

		buf.WriteRune(ch)
	}

	if inQuote {
		hadUnclosedQuote = true
	}

	// If quotes were unbalanced, fall back to plain whitespace tokenization.
	if hadUnclosedQuote {
		fallback := []SearchToken{}
		for _, t := range strings.Fields(trimmed) {
			t = strings.TrimSpace(t)
			if t != "" {
				fallback = append(fallback, SearchToken{Kind: TokenKindFuzzy, Value: t})
			}
		}
		return ParsedSearchQuery{Mode: "tokens", Tokens: fallback}
	}

	if inQuote {
		flush(TokenKindPhrase)
	} else {
		flush(TokenKindFuzzy)
	}

	return ParsedSearchQuery{Mode: "tokens", Tokens: tokens}
}

// isJsWhitespace mirrors JS \s for the session query tokenizer.
func isJsWhitespace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// MatchSession matches one session against a parsed query (pi: matchSession).
func MatchSession(session *core.SessionInfo, parsed ParsedSearchQuery) MatchResult {
	text := getSessionSearchText(session)

	if parsed.Mode == "regex" {
		if parsed.Regex == nil {
			return MatchResult{Matches: false, Score: 0}
		}
		loc := parsed.Regex.FindStringIndex(text)
		if loc == nil {
			return MatchResult{Matches: false, Score: 0}
		}
		return MatchResult{Matches: true, Score: float64(loc[0]) * 0.1}
	}

	if len(parsed.Tokens) == 0 {
		return MatchResult{Matches: true, Score: 0}
	}

	totalScore := 0.0
	normalizedText := ""

	for _, token := range parsed.Tokens {
		if token.Kind == TokenKindPhrase {
			if normalizedText == "" {
				normalizedText = normalizeWhitespaceLower(text)
			}
			phrase := normalizeWhitespaceLower(token.Value)
			if phrase == "" {
				continue
			}
			idx := strings.Index(normalizedText, phrase)
			if idx < 0 {
				return MatchResult{Matches: false, Score: 0}
			}
			totalScore += float64(idx) * 0.1
			continue
		}

		m := tui.FuzzyMatchScore(token.Value, text)
		if !m.Matches {
			return MatchResult{Matches: false, Score: 0}
		}
		totalScore += m.Score
	}

	return MatchResult{Matches: true, Score: totalScore}
}

// FilterAndSortSessions filters and sorts sessions by query and sort mode
// (pi: filterAndSortSessions). nameFilter defaults to "all" (pi optional).
func FilterAndSortSessions(sessions []core.SessionInfo, query string, sortMode SortMode, nameFilter NameFilter) []core.SessionInfo {
	if nameFilter == "" {
		nameFilter = NameFilterAll
	}
	nameFiltered := make([]core.SessionInfo, 0, len(sessions))
	for i := range sessions {
		if matchesNameFilter(&sessions[i], nameFilter) {
			nameFiltered = append(nameFiltered, sessions[i])
		}
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nameFiltered
	}

	parsed := ParseSearchQuery(query)
	if parsed.Error != "" {
		return []core.SessionInfo{}
	}

	// Recent mode: filter only, keep incoming order.
	if sortMode == SortModeRecent {
		filtered := []core.SessionInfo{}
		for i := range nameFiltered {
			res := MatchSession(&nameFiltered[i], parsed)
			if res.Matches {
				filtered = append(filtered, nameFiltered[i])
			}
		}
		return filtered
	}

	// Relevance mode: sort by score, tie-break by modified desc.
	type scored struct {
		session core.SessionInfo
		score   float64
	}
	results := []scored{}
	for i := range nameFiltered {
		res := MatchSession(&nameFiltered[i], parsed)
		if !res.Matches {
			continue
		}
		results = append(results, scored{session: nameFiltered[i], score: res.Score})
	}

	sort.SliceStable(results, func(a, b int) bool {
		if results[a].score != results[b].score {
			return results[a].score < results[b].score
		}
		return results[a].session.Modified.After(results[b].session.Modified)
	})

	out := make([]core.SessionInfo, 0, len(results))
	for _, r := range results {
		out = append(out, r.session)
	}
	return out
}
