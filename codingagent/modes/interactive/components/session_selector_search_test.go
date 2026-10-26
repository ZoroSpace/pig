// session_selector_search_test.go (pi: test/session-selector-search.test.ts)
// — session search query parsing, matching, and sort/filter tests.
package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
)

func makeSessionForSearch(overrides func(*core.SessionInfo)) core.SessionInfo {
	s := core.SessionInfo{
		MessageCount: 1,
		FirstMessage: "(no messages)",
		Created:      time.Unix(0, 0),
		Modified:     time.Unix(0, 0),
	}
	if overrides != nil {
		overrides(&s)
	}
	return s
}

func sessionIDs(sessions []core.SessionInfo) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}

func assertIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func TestSessionSelectorSearchQuotedPhrase(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "a"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "node\n\n   cve was discussed"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "b"
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "node something else"
		}),
	}

	result := FilterAndSortSessions(sessions, "\"node cve\"", SortModeRecent, NameFilterAll)
	assertIDs(t, sessionIDs(result), "a")
}

func TestSessionSelectorSearchRegex(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "a"
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "Brave is great"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "b"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "bravery is not the same"
		}),
	}

	result := FilterAndSortSessions(sessions, "re:\\bbrave\\b", SortModeRecent, NameFilterAll)
	assertIDs(t, sessionIDs(result), "a")
}

func TestSessionSelectorSearchRecentSortPreservesInputOrder(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "newer"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "older"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "nomatch"
			s.Modified = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "something else"
		}),
	}

	result := FilterAndSortSessions(sessions, "\"brave\"", SortModeRecent, NameFilterAll)
	assertIDs(t, sessionIDs(result), "newer", "older")
}

func TestSessionSelectorSearchRelevanceSort(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "message"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "name"
			name := "Brave work"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "xxxx"
		}),
	}

	result := FilterAndSortSessions(sessions, "\"brave\"", SortModeRelevance, NameFilterAll)
	assertIDs(t, sessionIDs(result), "name", "message")

	tieSessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "newer"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "older"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
	}

	result2 := FilterAndSortSessions(tieSessions, "\"brave\"", SortModeRelevance, NameFilterAll)
	assertIDs(t, sessionIDs(result2), "newer", "older")
}

func TestSessionSelectorSearchInvalidRegex(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "a"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
	}

	result := FilterAndSortSessions(sessions, "re:(", SortModeRecent, NameFilterAll)
	if len(result) != 0 {
		t.Fatalf("expected empty result for invalid regex, got %v", sessionIDs(result))
	}
}

func TestSessionSelectorSearchNameFilter(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "named1"
			s.Name = strPtr("My Project")
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "blueberry"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "named2"
			s.Name = strPtr("Another Named")
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "blueberry"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "other1"
			s.Modified = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "blueberry"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "other2"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "blueberry"
		}),
	}

	t.Run("returns all sessions when nameFilter is 'all'", func(t *testing.T) {
		result := FilterAndSortSessions(sessions, "", SortModeRecent, NameFilterAll)
		assertIDs(t, sessionIDs(result), "named1", "named2", "other1", "other2")
	})

	t.Run("returns only named sessions when nameFilter is 'named'", func(t *testing.T) {
		result := FilterAndSortSessions(sessions, "", SortModeRecent, NameFilterNamed)
		assertIDs(t, sessionIDs(result), "named1", "named2")
	})

	t.Run("applies name filter before search query", func(t *testing.T) {
		result := FilterAndSortSessions(sessions, "blueberry", SortModeRecent, NameFilterNamed)
		assertIDs(t, sessionIDs(result), "named1", "named2")
	})

	t.Run("excludes whitespace-only names from named filter", func(t *testing.T) {
		sessionsWithWhitespace := []core.SessionInfo{
			makeSessionForSearch(func(s *core.SessionInfo) {
				s.ID = "whitespace"
				s.Name = strPtr("   ")
				s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				s.AllMessagesText = "test"
			}),
			makeSessionForSearch(func(s *core.SessionInfo) {
				s.ID = "empty"
				s.Name = strPtr("")
				s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
				s.AllMessagesText = "test"
			}),
			makeSessionForSearch(func(s *core.SessionInfo) {
				s.ID = "named"
				s.Name = strPtr("Real Name")
				s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
				s.AllMessagesText = "test"
			}),
		}

		result := FilterAndSortSessions(sessionsWithWhitespace, "", SortModeRecent, NameFilterNamed)
		assertIDs(t, sessionIDs(result), "named")
	})
}

// TestSessionSelectorSearchIgnoresMessagePosition guards pi 8c03fef4d:
// exact matches in message text rank by tier without a position penalty.
func TestSessionSelectorSearchIgnoresMessagePosition(t *testing.T) {
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 400)
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "early"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "opus " + filler
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "deep"
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = filler + " we discussed the opus model"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "scattered"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = filler
		}),
	}

	result := FilterAndSortSessions(sessions, "opus", SortModeRelevance, NameFilterAll)
	assertIDs(t, sessionIDs(result), "deep", "early")
}

// TestSessionSelectorSearchDoesNotMatchScatteredIDs guards pi 8c03fef4d:
// session ids and message text match exactly only.
func TestSessionSelectorSearchDoesNotMatchScatteredIDs(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "019e9024-aaaa-7bbb-8ccc-dddddddddddd"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "unrelated"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "01a1ffff-aaaa-7bbb-8ccc-dddddddddddd"
			name := "#1024 TDD bare env"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "unrelated"
		}),
	}

	byName := FilterAndSortSessions(sessions, "1024", SortModeRelevance, NameFilterAll)
	if len(byName) != 1 || byName[0].Name == nil || *byName[0].Name != "#1024 TDD bare env" {
		t.Fatalf("1024 result = %#v", byName)
	}
	assertIDs(t, sessionIDs(FilterAndSortSessions(sessions, "9024-aa", SortModeRelevance, NameFilterAll)),
		"019e9024-aaaa-7bbb-8ccc-dddddddddddd")
}

// TestSessionSelectorSearchTierRanking guards pi 8c03fef4d: name text,
// then id/message/cwd text, then scattered name/cwd matches.
func TestSessionSelectorSearchTierRanking(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "scattered-name"
			name := "c-a-c-h-e notes"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "unrelated"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "message"
			name := "Other work"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "A discussion mentioning the cache."
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "name"
			name := "Cache work"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "Summary"
		}),
	}

	assertIDs(t, sessionIDs(FilterAndSortSessions(sessions, "cache", SortModeRelevance, NameFilterAll)),
		"name", "message", "scattered-name")
}

// TestSessionSelectorSearchMatchesEachTokenWithinOneField guards pi
// 8c03fef4d: tokens never match across field boundaries.
func TestSessionSelectorSearchMatchesEachTokenWithinOneField(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "split"
			name := "Fuzzy"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "search ranking"
		}),
	}

	assertIDs(t, sessionIDs(FilterAndSortSessions(sessions, "fuzzy ranking", SortModeRelevance, NameFilterAll)), "split")
	if got := FilterAndSortSessions(sessions, "zzysea", SortModeRelevance, NameFilterAll); len(got) != 0 {
		t.Fatalf("zzysea = %v, want empty", sessionIDs(got))
	}
	if got := FilterAndSortSessions(sessions, "re:fuzzy search", SortModeRelevance, NameFilterAll); len(got) != 0 {
		t.Fatalf("re:fuzzy search = %v, want empty", sessionIDs(got))
	}
}

// TestSessionSelectorSearchRanksRegexNameMatchesFirst guards pi 8c03fef4d.
func TestSessionSelectorSearchRanksRegexNameMatchesFirst(t *testing.T) {
	sessions := []core.SessionInfo{
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "message"
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "name"
			name := "Brave"
			s.Name = &name
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "xxxx"
		}),
	}

	assertIDs(t, sessionIDs(FilterAndSortSessions(sessions, "re:\\bbrave\\b", SortModeRelevance, NameFilterAll)),
		"name", "message")
}
