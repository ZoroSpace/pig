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

// Match tiers, best first. Each token is scored by the best field it matches; tiers dominate
// fuzzy scores so an exact hit anywhere beats a scattered subsequence in a short field.
const (
	tierName   = 0 // query text appears in the session name
	tierText   = 1 // query text appears in the session id, messages, or cwd
	tierFuzzy  = 2 // query characters appear scattered in the session name or cwd
	tierWeight = 1_000_000
)

func tierScore(tier int, score float64) float64 {
	return float64(tier*tierWeight) + score
}

func sessionNameLower(session *core.SessionInfo) string {
	if session.Name == nil {
		return ""
	}
	return strings.ToLower(*session.Name)
}

func normalizeWhitespaceLowerText(text string) string {
	return normalizeWhitespaceLower(text)
}

// matchLiteralToken is an exact (case-insensitive) text match. Messages and
// ids are only matched exactly: scattered matches in them are noise.
func matchLiteralToken(session *core.SessionInfo, value string) (float64, bool) {
	name := sessionNameLower(session)
	if strings.Contains(name, value) {
		return tierScore(tierName, tui.FuzzyMatchScore(value, name).Score), true
	}
	if strings.Contains(strings.ToLower(session.ID), value) {
		return tierScore(tierText, 0), true
	}
	if strings.Contains(strings.ToLower(session.AllMessagesText), value) {
		return tierScore(tierText, 0), true
	}
	if strings.Contains(strings.ToLower(session.Cwd), value) {
		return tierScore(tierText, 0), true
	}
	return 0, false
}

func matchFuzzyToken(session *core.SessionInfo, value string) (float64, bool) {
	if literal, ok := matchLiteralToken(session, strings.ToLower(value)); ok {
		return literal, true
	}

	best := 0.0
	found := false
	for _, field := range []string{sessionNameLower(session), session.Cwd} {
		m := tui.FuzzyMatchScore(value, field)
		if m.Matches && (!found || m.Score < best) {
			best = m.Score
			found = true
		}
	}
	if !found {
		return 0, false
	}
	return tierScore(tierFuzzy, best), true
}

func matchPhraseToken(session *core.SessionInfo, phrase string) (float64, bool) {
	name := normalizeWhitespaceLowerText(sessionNameLower(session))
	if strings.Contains(name, phrase) {
		return tierScore(tierName, tui.FuzzyMatchScore(phrase, name).Score), true
	}
	if strings.Contains(strings.ToLower(session.ID), phrase) {
		return tierScore(tierText, 0), true
	}
	if strings.Contains(normalizeWhitespaceLowerText(strings.ToLower(session.AllMessagesText)), phrase) {
		return tierScore(tierText, 0), true
	}
	if strings.Contains(normalizeWhitespaceLowerText(strings.ToLower(session.Cwd)), phrase) {
		return tierScore(tierText, 0), true
	}
	return 0, false
}

func matchRegex(session *core.SessionInfo, regex *regexp.Regexp) (float64, bool) {
	if session.Name != nil && regex.MatchString(*session.Name) {
		return tierScore(tierName, 0), true
	}
	if regex.MatchString(session.ID) || regex.MatchString(session.AllMessagesText) || regex.MatchString(session.Cwd) {
		return tierScore(tierText, 0), true
	}
	return 0, false
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
// Each field is matched separately, so a token never matches across field
// boundaries; different tokens may match different fields.
func MatchSession(session *core.SessionInfo, parsed ParsedSearchQuery) MatchResult {
	if parsed.Mode == "regex" {
		if parsed.Regex == nil {
			return MatchResult{Matches: false, Score: 0}
		}
		score, ok := matchRegex(session, parsed.Regex)
		if !ok {
			return MatchResult{Matches: false, Score: 0}
		}
		return MatchResult{Matches: true, Score: score}
	}

	totalScore := 0.0

	for _, token := range parsed.Tokens {
		var score float64
		var ok bool
		if token.Kind == TokenKindPhrase {
			phrase := normalizeWhitespaceLower(token.Value)
			if phrase == "" {
				continue
			}
			score, ok = matchPhraseToken(session, phrase)
		} else {
			score, ok = matchFuzzyToken(session, token.Value)
		}
		if !ok {
			return MatchResult{Matches: false, Score: 0}
		}
		totalScore += score
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
