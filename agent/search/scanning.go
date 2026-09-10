// pi: src/search/scanning.ts — linear-scan session search over
// SessionStorage reads.
//
// Deviations:
//   - Async generators → iter / iter.Seq closures. Fallible yields (fs
//     errors, aborts, duplicate session ids) surface through iter.Seq2's
//     error element; pi throws inside the generator.
//   - ScanningReadable (pi: Pick<SessionStorage, ...>) is a Go interface
//     satisfied structurally by session storage implementations.
//   - pi's generics (TMetadata, TSourceOptions, THit) collapse to concrete
//     types per the TModel/conditional-type rules: metadata is the base
//     session.SessionMetadata, source options are `any`, hits are the
//     concrete ScanningSessionSearchHit.
//   - AbortSignal probes → context.Context.Err() checks (pi:
//     throwIfAborted; the thrown error maps to the yielded error).
//   - JSON.stringify(entry) → json.Marshal (same canonical projection of
//     the entry wire shape).
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// ScanningReadable is the storage read surface the scanner consumes (pi:
// Pick<SessionStorage, "getMetadata" | "findEntries" | "getLabel">).
type ScanningReadable interface {
	GetMetadata() (session.SessionMetadata, error)
	FindEntries(query *session.EntryQuery) ([]session.Entry, error)
	GetLabel(id string) (*string, error)
}

// ScanningReadableSource yields readables to scan (pi:
// ScanningReadableSource<TMetadata, TOptions>; source options collapse to
// any).
type ScanningReadableSource = func(options any) iter.Seq[ScanningReadable]

// ScanningSearchTextProjector projects one entry into searchable text (pi:
// ScanningSearchTextProjector).
type ScanningSearchTextProjector = func(metadata session.SessionMetadata, entry session.Entry, label *string) string

// ScanningReadableOptions parameterizes one readable's scan (pi:
// ScanningReadableOptions).
type ScanningReadableOptions struct {
	// ProjectText overrides the default text projection.
	ProjectText ScanningSearchTextProjector
	// PageSize overrides the default scan page size.
	PageSize *int
}

// SessionSearchCandidate is one scannable entry projection (pi:
// SessionSearchCandidate).
type SessionSearchCandidate struct {
	EntryID   string
	Seq       int64
	Type      session.EntryType
	Timestamp int64
	Text      string
	// Fields carries extras such as the entry label; nil when empty.
	Fields map[string]any
}

// ScanningSessionSearchHit is one search hit with its snippet (pi:
// ScanningSessionSearchHit).
type ScanningSessionSearchHit struct {
	SessionID string
	EntryID   string
	Timestamp int64
	Snippet   string
}

// ScanningSessionSearchOptions parameterizes the scanning search (pi:
// ScanningSessionSearchOptions<...>; the source-options and hit generics
// collapse to any / the concrete hit type).
type ScanningSessionSearchOptions struct {
	ScanningReadableOptions
	// SourceOptions derives source options from the normalized query (pi:
	// sourceOptions). Returning nil is the TS `undefined`.
	SourceOptions func(text string, options SessionSearchOptions) any
	// Match overrides the default substring match.
	Match func(queryText string, candidate SessionSearchCandidate, metadata session.SessionMetadata) bool
	// CreateHit overrides the default hit shape.
	CreateHit func(metadata session.SessionMetadata, candidate SessionSearchCandidate) ScanningSessionSearchHit
}

// defaultSearchText projects the entry's JSON plus its label (pi:
// defaultSearchText).
func defaultSearchText(metadata session.SessionMetadata, entry session.Entry, label *string) string {
	encoded, err := json.Marshal(entry)
	if err != nil {
		// Typed entries always marshal; mirror pi's JSON.stringify leniency
		// with the error text rather than failing the scan.
		encoded = []byte(fmt.Sprintf("%v", entry))
	}
	if label == nil {
		return string(encoded)
	}
	return string(encoded) + " " + *label
}

// scanReadableEntries pages through one readable's entries, yielding
// candidates (pi: scanReadableEntries).
func scanReadableEntries(ctx context.Context, readable ScanningReadable, metadata session.SessionMetadata, options ScanningReadableOptions, query entryScanQuery) iter.Seq2[SessionSearchCandidate, error] {
	return func(yield func(SessionSearchCandidate, error) bool) {
		projectText := options.ProjectText
		if projectText == nil {
			projectText = defaultSearchText
		}
		pageSize := 100
		if query.Limit != nil {
			pageSize = *query.Limit
		} else if options.PageSize != nil {
			pageSize = *options.PageSize
		}
		afterSeq := int64(0)
		if query.AfterSeq != nil {
			afterSeq = *query.AfterSeq
		}
		var entryTypes map[session.EntryType]struct{}
		if query.EntryTypes != nil {
			entryTypes = map[session.EntryType]struct{}{}
			for _, entryType := range query.EntryTypes {
				entryTypes[entryType] = struct{}{}
			}
		}
		for {
			findQuery := &session.EntryQuery{
				Order:  session.OrderOldestFirst,
				Limit:  &pageSize,
				Cursor: &session.EntryCursor{AfterSeq: afterSeq},
			}
			// pi filters on `type` only when exactly one entry type is
			// requested.
			if len(query.EntryTypes) == 1 {
				findQuery.Type = &query.EntryTypes[0]
			}
			entries, err := readable.FindEntries(findQuery)
			if err != nil {
				yield(SessionSearchCandidate{}, err)
				return
			}
			if len(entries) == 0 {
				return
			}
			for _, entry := range entries {
				base := entry.Base()
				if entryTypes != nil {
					if _, ok := entryTypes[base.Type]; !ok {
						continue
					}
				}
				label, err := readable.GetLabel(base.ID)
				if err != nil {
					yield(SessionSearchCandidate{}, err)
					return
				}
				var fields map[string]any
				if label != nil {
					fields = map[string]any{"label": *label}
				}
				candidate := SessionSearchCandidate{
					EntryID:   base.ID,
					Seq:       base.Seq,
					Type:      base.Type,
					Timestamp: base.Timestamp,
					Text:      projectText(metadata, entry, label),
					Fields:    fields,
				}
				if !yield(candidate, nil) {
					return
				}
			}
			if last := entries[len(entries)-1].Base(); last != nil {
				afterSeq = last.Seq
			}
			if len(entries) < pageSize {
				return
			}
		}
	}
}

// entryScanQuery is the internal scan query (pi: the anonymous `{ afterSeq?,
// limit?, entryTypes? }` parameter).
type entryScanQuery struct {
	AfterSeq   *int64
	Limit      *int
	EntryTypes []session.EntryType
}

// ScanningEntries yields every candidate in one readable (pi:
// scanningEntries).
func ScanningEntries(ctx context.Context, readable ScanningReadable, options *ScanningReadableOptions) iter.Seq2[SessionSearchCandidate, error] {
	return func(yield func(SessionSearchCandidate, error) bool) {
		scanOptions := ScanningReadableOptions{}
		if options != nil {
			scanOptions = *options
		}
		metadata, err := readable.GetMetadata()
		if err != nil {
			yield(SessionSearchCandidate{}, err)
			return
		}
		for candidate, err := range scanReadableEntries(ctx, readable, metadata, scanOptions, entryScanQuery{}) {
			if !yield(candidate, err) {
				return
			}
		}
	}
}

// defaultMatch is a case-insensitive substring match (pi: defaultMatch).
func defaultMatch(queryText string, candidate SessionSearchCandidate) bool {
	return strings.Contains(strings.ToLower(candidate.Text), queryText)
}

// createDefaultScanningHit builds the default hit (pi:
// createDefaultScanningHit).
func createDefaultScanningHit(metadata session.SessionMetadata, candidate SessionSearchCandidate) ScanningSessionSearchHit {
	return ScanningSessionSearchHit{
		SessionID: metadata.ID,
		EntryID:   candidate.EntryID,
		Timestamp: candidate.Timestamp,
		Snippet:   candidate.Text,
	}
}

// readablesFor normalizes a slice-or-source into a sequence (pi:
// readablesFor + arraySource).
func readablesFor(source any, options any) iter.Seq[ScanningReadable] {
	switch s := source.(type) {
	case ScanningReadableSource:
		return s(options)
	case []ScanningReadable:
		return func(yield func(ScanningReadable) bool) {
			for _, readable := range s {
				if !yield(readable) {
					return
				}
			}
		}
	default:
		return func(yield func(ScanningReadable) bool) {}
	}
}

type scanningSessionSearch struct {
	source  any
	options ScanningSessionSearchOptions
}

// CreateScanningSessionSearch builds a linear-scan session search (pi:
// createScanningSessionSearch; the source accepts either a
// []ScanningReadable or a ScanningReadableSource).
func CreateScanningSessionSearch(source any, options *ScanningSessionSearchOptions) SessionSearch {
	searchOptions := ScanningSessionSearchOptions{}
	if options != nil {
		searchOptions = *options
	}
	return scanningSessionSearch{source: source, options: searchOptions}
}

// Search implements SessionSearch (pi: the search generator).
func (s scanningSessionSearch) Search(ctx context.Context, text string, searchOptions *SessionSearchOptions) iter.Seq2[ScanningSessionSearchHit, error] {
	return func(yield func(ScanningSessionSearchHit, error) bool) {
		opts := SessionSearchOptions{}
		if searchOptions != nil {
			opts = *searchOptions
		}
		normalizedText := strings.ToLower(strings.TrimSpace(text))
		if normalizedText == "" || (opts.Limit != nil && *opts.Limit <= 0) {
			return
		}
		if opts.EntryTypes != nil && len(opts.EntryTypes) == 0 {
			return
		}
		hitCount := 0
		seenSessionIDs := map[string]struct{}{}
		var entryTypes map[session.EntryType]struct{}
		if opts.EntryTypes != nil {
			entryTypes = map[session.EntryType]struct{}{}
			for _, entryType := range opts.EntryTypes {
				entryTypes[entryType] = struct{}{}
			}
		}
		var sourceOptions any
		if s.options.SourceOptions != nil {
			sourceOptions = s.options.SourceOptions(normalizedText, opts)
		}
		for readable := range readablesFor(s.source, sourceOptions) {
			if err := ctx.Err(); err != nil {
				yield(ScanningSessionSearchHit{}, err)
				return
			}
			metadata, err := readable.GetMetadata()
			if err != nil {
				yield(ScanningSessionSearchHit{}, err)
				return
			}
			if _, seen := seenSessionIDs[metadata.ID]; seen {
				yield(ScanningSessionSearchHit{}, fmt.Errorf("Duplicate sessionId: %s", metadata.ID))
				return
			}
			seenSessionIDs[metadata.ID] = struct{}{}
			for candidate, err := range scanReadableEntries(ctx, readable, metadata, s.options.ScanningReadableOptions, entryScanQuery{
				EntryTypes: opts.EntryTypes,
			}) {
				if err != nil {
					yield(ScanningSessionSearchHit{}, err)
					return
				}
				if err := ctx.Err(); err != nil {
					yield(ScanningSessionSearchHit{}, err)
					return
				}
				if entryTypes != nil {
					if _, ok := entryTypes[candidate.Type]; !ok {
						continue
					}
				}
				matches := defaultMatch(normalizedText, candidate)
				if s.options.Match != nil {
					matches = s.options.Match(normalizedText, candidate, metadata)
				}
				if !matches {
					continue
				}
				createHit := s.options.CreateHit
				if createHit == nil {
					createHit = createDefaultScanningHit
				}
				if !yield(createHit(metadata, candidate), nil) {
					return
				}
				hitCount++
				if opts.Limit != nil && hitCount >= *opts.Limit {
					return
				}
			}
		}
	}
}
