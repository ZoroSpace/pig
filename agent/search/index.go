// pi: src/search/index.ts — the session search surface.
//
// Deviations:
//   - SessionSearch.search returns iter.Seq2[ScanningSessionSearchHit,
//     error]: pi's async iterable throws on abort/duplicate-id/fs errors;
//     Go sequences surface the terminating error as the second element.
//   - search takes a leading context.Context (pi: AbortSignal → context
//     rule); the signal field of SessionSearchOptions has no counterpart.
//   - pi's SessionSearch<T> hit generic collapses to the concrete
//     ScanningSessionSearchHit (per the TModel generic rule).
//   - pi's `readonly` option fields are plain struct fields.
package search

import (
	"context"
	"iter"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// SessionSearchOptions parameterizes a search call (pi:
// SessionSearchOptions).
type SessionSearchOptions struct {
	// EntryTypes restricts results to specific canonical entry types.
	EntryTypes []session.EntryType
	// Limit caps the number of hits returned.
	Limit *int
	// pi's `signal?: AbortSignal` maps to the Search ctx parameter.
}

// SessionSearchHit identifies one matching entry (pi: SessionSearchHit).
type SessionSearchHit struct {
	// SessionID is the logical identifier of the session that owns the
	// entry.
	SessionID string
	// EntryID is the logical identifier of the entry within that session.
	EntryID string
}

// SessionSearch searches session entries (pi: SessionSearch<T>; the hit
// generic collapses to ScanningSessionSearchHit).
type SessionSearch interface {
	// Search yields hits until the limit is reached; the second element
	// carries a terminating error (abort, duplicate session id, read
	// failure).
	Search(ctx context.Context, text string, options *SessionSearchOptions) iter.Seq2[ScanningSessionSearchHit, error]
}
