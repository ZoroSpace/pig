// pi: src/search/index.ts — the session search service contract (v0.85.1:
// the scanning implementation was removed upstream; pig's search package
// collapses to this interface, matching the v0.85.1 file 1:1).
//
// Deviations:
//   - pi's Promise-returning methods → (T, error).
package search

import "context"

// SearchQuery is one search request (pi: SearchQuery).
type SearchQuery struct {
	Text  string
	Limit *int
}

// SessionSearchHit is one matched session (pi: SessionSearchHit).
type SessionSearchHit struct {
	SessionID string            `json:"sessionId"`
	Score     *float64          `json:"score,omitempty"`
	Top       *SessionSearchTop `json:"top,omitempty"`
}

// SessionSearchTop is the top matching entry of a session hit (pi: the
// inline `top` shape).
type SessionSearchTop struct {
	EntryID   string  `json:"entryId"`
	Snippet   *string `json:"snippet,omitempty"`
	Timestamp int64   `json:"timestamp"`
}

// EntrySearchHit is one matched entry (pi: EntrySearchHit).
type EntrySearchHit struct {
	SessionID string   `json:"sessionId"`
	EntryID   string   `json:"entryId"`
	Timestamp int64    `json:"timestamp"`
	Snippet   *string  `json:"snippet,omitempty"`
	Score     *float64 `json:"score,omitempty"`
}

// SessionSearchService is the search contract consumed by the app layer
// (pi: SessionSearchService).
type SessionSearchService interface {
	SearchSessions(ctx context.Context, query SearchQuery) ([]SessionSearchHit, error)
	SearchEntries(ctx context.Context, query SearchQuery) ([]EntrySearchHit, error)
	Sync(ctx context.Context) error
	Notify(sessionID string)
	Remove(ctx context.Context, sessionID string) error
	Close(ctx context.Context) error
}
