// session_selector_search_test.go (pi: test/session-selector-search.test.ts)
// — session search query parsing, matching, and sort/filter tests.
package components

import (
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
			s.ID = "late"
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "xxxx brave"
		}),
		makeSessionForSearch(func(s *core.SessionInfo) {
			s.ID = "early"
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			s.AllMessagesText = "brave xxxx"
		}),
	}

	result := FilterAndSortSessions(sessions, "\"brave\"", SortModeRelevance, NameFilterAll)
	assertIDs(t, sessionIDs(result), "early", "late")

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
