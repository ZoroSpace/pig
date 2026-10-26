// pi: packages/tui/src/fuzzy.ts — fuzzy matching utilities. Matches if all
// query characters appear in order (not necessarily consecutive). Lower
// score = better match.
//
// The first consumers (CLI list-models, later the session selector) need
// only this file of pi-tui; the rest of the package arrives with the TUI
// batch (Phase 6).
package tui

import (
	"math"
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

// scoreFuzzyPositions scores one alignment (pi: scorePositions).
func scoreFuzzyPositions(text []rune, positions []int) float64 {
	score := 0.0
	lastMatchIndex := -1
	consecutiveMatches := 0

	for _, i := range positions {
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
		if i == 0 || fuzzyWordBoundary.MatchString(string(text[i-1])) {
			score -= 10
		}

		// Slight penalty for later matches
		score += float64(i) * 0.1

		lastMatchIndex = i
	}

	return score
}

// matchFuzzyQuery is pi's matchQuery helper: greedy alignment plus every
// contiguous occurrence, keeping the best score.
func matchFuzzyQuery(normalizedQuery, textLower string) FuzzyMatch {
	if len(normalizedQuery) == 0 {
		return FuzzyMatch{Matches: true, Score: 0}
	}
	if len(normalizedQuery) > len(textLower) {
		return FuzzyMatch{Matches: false, Score: 0}
	}

	text := []rune(textLower)
	normalized := []rune(normalizedQuery)

	// Greedy alignment: each query character at its first occurrence after the previous one.
	positions := make([]int, 0, len(normalized))
	lastMatchIndex := -1
	for _, want := range normalized {
		i := -1
		for j := lastMatchIndex + 1; j < len(text); j++ {
			if text[j] == want {
				i = j
				break
			}
		}
		if i == -1 {
			return FuzzyMatch{Matches: false, Score: 0}
		}
		positions = append(positions, i)
		lastMatchIndex = i
	}

	score := scoreFuzzyPositions(text, positions)

	// The greedy alignment can scatter the query over an earlier, unrelated part of the text,
	// e.g. "opus" in "openrouter openrouter/anthropic/claude-opus" takes o, p, u from "openrouter".
	// Also score every contiguous occurrence and keep the best alignment.
	n := len(normalized)
	for start := 0; start+n <= len(text); start++ {
		matched := true
		for k := 0; k < n; k++ {
			if text[start+k] != normalized[k] {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		for k := 0; k < n; k++ {
			positions[k] = start + k
		}
		score = math.Min(score, scoreFuzzyPositions(text, positions))
	}

	if normalizedQuery == textLower {
		score -= 100
	}

	return FuzzyMatch{Matches: true, Score: score}
}

// FuzzyMatchScore computes the match of query against text (pi: fuzzyMatch).
func FuzzyMatchScore(query, text string) FuzzyMatch {
	queryLower := strings.ToLower(query)
	textLower := strings.ToLower(text)

	primaryMatch := matchFuzzyQuery(queryLower, textLower)
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

	swappedMatch := matchFuzzyQuery(swappedQuery, textLower)
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
