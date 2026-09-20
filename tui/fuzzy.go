// pi: packages/tui/src/fuzzy.ts — fuzzy matching utilities. Matches if all
// query characters appear in order (not necessarily consecutive). Lower
// score = better match.
//
// The first consumers (CLI list-models, later the session selector) need
// only this file of pi-tui; the rest of the package arrives with the TUI
// batch (Phase 6).
package tui

import (
	"regexp"
	"sort"
	"strings"
)

// FuzzyMatch is one fuzzy-match outcome (pi: FuzzyMatch).
type FuzzyMatch struct {
	Matches bool
	Score   float64
}

// fuzzyWordBoundary separates word-boundary positions (pi's
// /[\s\-_./:]/ test).
var fuzzyWordBoundary = regexp.MustCompile(`[\s\-_./:]`)

// fuzzyAlphaNumeric splits "letters+digits" queries for the swapped retry
// (pi: /^(?<letters>[a-z]+)(?<digits>[0-9]+)$/).
var fuzzyAlphaNumeric = regexp.MustCompile(`^([a-z]+)([0-9]+)$`)

// fuzzyNumericAlpha splits "digits+letters" queries for the swapped retry
// (pi: /^(?<digits>[0-9]+)(?<letters>[a-z]+)$/).
var fuzzyNumericAlpha = regexp.MustCompile(`^([0-9]+)([a-z]+)$`)

// FuzzyMatchScore computes the match of query against text (pi:
// fuzzyMatch's matchQuery helper folded into the exported function shape —
// pi's fuzzyMatch returns matchQuery's result or the swapped-retry result).
func FuzzyMatchScore(query, text string) FuzzyMatch {
	queryLower := strings.ToLower(query)
	textLower := strings.ToLower(text)

	matchQuery := func(normalizedQuery string) FuzzyMatch {
		if len(normalizedQuery) == 0 {
			return FuzzyMatch{Matches: true, Score: 0}
		}
		if len(normalizedQuery) > len(textLower) {
			return FuzzyMatch{Matches: false, Score: 0}
		}

		queryIndex := 0
		score := 0.0
		lastMatchIndex := -1
		consecutiveMatches := 0

		// pi iterates by UTF-16 code unit; byte iteration matches its
		// single-char indexing for ASCII queries and differs only for
		// multi-byte text, where both still compare full characters via
		// prefix equality. We iterate runes for correctness.
		runes := []rune(textLower)
		normalized := []rune(normalizedQuery)
		// pi v0.86.0 replaced the full scan with indexOf per query character
		// (skips non-matching characters in bulk); semantics are identical
		// greedy leftmost matching, so we mirror the new shape.
		for queryIndex < len(normalized) {
			i := -1
			for j := lastMatchIndex + 1; j < len(runes); j++ {
				if runes[j] == normalized[queryIndex] {
					i = j
					break
				}
			}
			if i == -1 {
				break
			}

			isWordBoundary := i == 0 || fuzzyWordBoundary.MatchString(string(runes[i-1]))

			// Reward consecutive matches
			if lastMatchIndex == i-1 {
				consecutiveMatches++
				score -= float64(consecutiveMatches) * 5
			} else {
				consecutiveMatches = 0
				// Penalize gaps
				if lastMatchIndex >= 0 {
					score += float64(i-lastMatchIndex-1) * 2
				}
			}

			// Reward word boundary matches
			if isWordBoundary {
				score -= 10
			}

			// Slight penalty for later matches
			score += float64(i) * 0.1

			lastMatchIndex = i
			queryIndex++
		}

		if queryIndex < len(normalized) {
			return FuzzyMatch{Matches: false, Score: 0}
		}

		if normalizedQuery == textLower {
			score -= 100
		}

		return FuzzyMatch{Matches: true, Score: score}
	}

	primaryMatch := matchQuery(queryLower)
	if primaryMatch.Matches {
		return primaryMatch
	}

	var swappedQuery string
	if m := fuzzyAlphaNumeric.FindStringSubmatch(queryLower); m != nil {
		swappedQuery = m[2] + m[1]
	} else if m := fuzzyNumericAlpha.FindStringSubmatch(queryLower); m != nil {
		swappedQuery = m[2] + m[1]
	}

	if swappedQuery == "" {
		return primaryMatch
	}

	swappedMatch := matchQuery(swappedQuery)
	if !swappedMatch.Matches {
		return primaryMatch
	}

	return FuzzyMatch{Matches: true, Score: swappedMatch.Score + 5}
}

// FuzzyFilter filters and sorts items by fuzzy match quality, best matches
// first. Supports whitespace- and slash-separated tokens: all tokens must
// match (pi: fuzzyFilter).
func FuzzyFilter[T any](items []T, query string, getText func(item T) string) []T {
	if strings.TrimSpace(query) == "" {
		return items
	}

	tokens := strings.FieldsFunc(strings.TrimSpace(query), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '/' || r == '\v' || r == '\f'
	})
	if len(tokens) == 0 {
		return items
	}

	type result struct {
		item       T
		totalScore float64
	}
	var results []result

	for _, item := range items {
		text := getText(item)
		totalScore := 0.0
		allMatch := true

		for _, token := range tokens {
			match := FuzzyMatchScore(token, text)
			if match.Matches {
				totalScore += match.Score
			} else {
				allMatch = false
				break
			}
		}

		if allMatch {
			results = append(results, result{item: item, totalScore: totalScore})
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].totalScore < results[j].totalScore })
	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}
