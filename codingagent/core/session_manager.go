// pi: src/core/session-manager.ts — append-only JSONL session trees with
// compaction-aware context building, branching, labels, migrations, and
// session discovery/listing.
//
// Deviations:
//   - Entry/FileEntry discriminated unions → interface + pointer concrete
//     types + DecodeFileEntry dispatch (per §3.4). rawFileEntry preserves
//     entries whose shape pig's typed decode rejects (pi's JSON.parse keeps
//     arbitrary objects verbatim); they round-trip byte-for-byte but are
//     skipped by migration and the id index (pi assigns ids to every parsed
//     object; raw shapes cannot occur in files pi itself writes).
//   - AgentMessage is the open agent.AgentMessage interface. Message payloads
//     with an unrecognized role decode into untypedMessage (raw-preserving);
//     pi's v2→v3 hookMessage→custom role flip happens on that wrapper.
//   - Unknown/extra fields on typed entries are dropped on re-serialization
//     (TS parsed objects keep them); only compaction's legacy
//     firstKeptEntryIndex is retained, as FirstKeptEntryIndex, for the v1→v2
//     migration to consume and zero.
//   - pi throws synchronously (invalid session ids, missing entries, mkdir /
//     write failures, non-session files) → error returns on the same methods.
//   - leafId: string | null on the manager maps to *string (nil = null). The
//     package-level BuildContextEntries/BuildSessionProjection/
//     BuildSessionContext take *string with nil = TS undefined (fall back to
//     the last entry); the null semantics (empty path) is internalized by the
//     manager methods.
//   - SessionManager.list/listAll/buildSessionInfo are async in pi purely for
//     fs concurrency; pig runs them synchronously with the same bounded
//     concurrency (10) and progress callbacks. No ctx: pi has no AbortSignal.
//   - buildSessionInfosWithConcurrency invokes onProgress from worker
//     goroutines; pig serializes the calls behind a mutex (pi's microtask
//     queue serialized them implicitly). onProgress must not re-enter the
//     SessionManager.
//   - Date handling: entry timestamps are ISO strings (wire format); message
//     timestamps stay int64 milliseconds. Invalid date strings → zero
//     time.Time (pi: Invalid Date / NaN).
//   - getChildren order: pi iterates Map insertion order (= file order); pig
//     iterates fileEntries directly (same order).
//   - createBranchedSession's labelsToWrite order: pi iterates the
//     insertion-ordered labelsById Map; pig derives target order from
//     first-appearance in fileEntries (same content, stable order).
//   - messages.ts: the four custom message types and convertToLlm already
//     live in agent/harness (pi re-declares them in core/messages.ts via
//     declaration merging; pig imports the single implementation).
package core

import (
	bufio "bufio"
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// CURRENT_SESSION_VERSION is the session file format version pig writes
// (pi: CURRENT_SESSION_VERSION).
const CURRENT_SESSION_VERSION = 3

// SessionHeader is the first line of a session file (pi: SessionHeader).
type SessionHeader struct {
	Type string `json:"type"` // "session"
	// Version is absent in v1 sessions.
	Version   *int   `json:"version,omitempty"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	// ParentSession is the path to the parent session (if this session was
	// forked).
	ParentSession *string `json:"parentSession,omitempty"`
}

func (h *SessionHeader) EntryType() string { return "session" }

// NewSessionOptions parameterize new-session creation (pi: NewSessionOptions).
type NewSessionOptions struct {
	ID            *string `json:"id,omitempty"`
	ParentSession *string `json:"parentSession,omitempty"`
}

// EntryBase carries the fields shared by every session-tree entry (pi:
// SessionEntryBase). Timestamp is an ISO date string (wire format).
type EntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

func (b *EntryBase) Base() *EntryBase  { return b }
func (b *EntryBase) EntryType() string { return b.Type }

// SessionEntry is the session-tree entry union (pi: SessionEntry).
type SessionEntry interface {
	FileEntry
	// Base exposes the shared id/parentId/timestamp fields.
	Base() *EntryBase
}

// FileEntry is a raw file entry: the session header or one session-tree
// entry (pi: FileEntry = SessionHeader | SessionEntry).
type FileEntry interface {
	// EntryType returns the JSON "type" discriminant ("session" for headers).
	EntryType() string
}

// SessionMessageEntry holds one AgentMessage (pi: SessionMessageEntry).
type SessionMessageEntry struct {
	EntryBase
	Message agent.AgentMessage `json:"message"`
}

// ThinkingLevelChangeEntry records a thinking level change (pi:
// ThinkingLevelChangeEntry).
type ThinkingLevelChangeEntry struct {
	EntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// ModelChangeEntry records a model switch (pi: ModelChangeEntry).
type ModelChangeEntry struct {
	EntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// UsageEntry records model-attributed usage that does not participate in
// LLM context (pi: UsageEntry; v0.86.0, e.g. cache warming refreshes).
type UsageEntry struct {
	EntryBase
	// Kind is an arbitrary usage category, such as "cache_warm".
	Kind     string       `json:"kind"`
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	Usage    *types.Usage `json:"usage"`
	// Note is an optional human-readable qualifier for usage notices.
	Note string `json:"note,omitempty"`
}

// CompactionEntry records a compaction summary (pi: CompactionEntry).
type CompactionEntry struct {
	EntryBase
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	TokensBefore     int64  `json:"tokensBefore"`
	// Details is extension-specific data (e.g. ArtifactIndex, version
	// markers for structured compaction).
	Details any `json:"details,omitempty"`
	// Usage comes from the LLM call(s) that generated this summary, if
	// available.
	Usage *types.Usage `json:"usage,omitempty"`
	// FromHook is true when generated by an extension, nil/false when
	// pi-generated (backward compatible).
	FromHook *bool `json:"fromHook,omitempty"`
	// SystemMessage is the complete prompt state at this compaction
	// boundary (pi: systemMessage; v0.86.0). Replayed ahead of the summary
	// so mid-conversation system messages survive compaction.
	SystemMessage *types.SystemMessage `json:"systemMessage,omitempty"`
	// FirstKeptEntryIndex is the v1 legacy field (pi: firstKeptEntryIndex,
	// deleted during v1→v2 migration; pig keeps it as a pointer the
	// migration zeroes so it never re-serializes).
	FirstKeptEntryIndex *int64 `json:"firstKeptEntryIndex,omitempty"`
}

// BranchSummaryEntry records a branch summarization (pi: BranchSummaryEntry).
type BranchSummaryEntry struct {
	EntryBase
	FromID  string `json:"fromId"`
	Summary string `json:"summary"`
	// Details is extension-specific data (not sent to LLM).
	Details any `json:"details,omitempty"`
	// Usage comes from the LLM call that generated this summary, if
	// available.
	Usage *types.Usage `json:"usage,omitempty"`
	// FromHook is true when generated by an extension, false when
	// pi-generated.
	FromHook *bool `json:"fromHook,omitempty"`
}

// CustomEntry stores extension-specific data in the session (pi:
// CustomEntry). It does NOT participate in LLM context (ignored by
// buildSessionContext); for injecting content into context see
// CustomMessageEntry.
type CustomEntry struct {
	EntryBase
	CustomType string `json:"customType"`
	// Data is extension-specific payload (pi: data?: T).
	Data any `json:"data,omitempty"`
}

// LabelEntry is a user-defined bookmark/marker on an entry (pi: LabelEntry).
type LabelEntry struct {
	EntryBase
	TargetID string `json:"targetId"`
	// Label nil = clear the label.
	Label *string `json:"label,omitempty"`
}

// SessionInfoEntry carries session metadata, e.g. a user-defined display
// name (pi: SessionInfoEntry).
type SessionInfoEntry struct {
	EntryBase
	Name *string `json:"name,omitempty"`
}

// CustomMessageEntry lets extensions inject messages into LLM context (pi:
// CustomMessageEntry). Unlike CustomEntry this DOES participate in LLM
// context: the content is converted to a user message in
// buildSessionContext. Details is extension-specific metadata (not sent to
// LLM). Display controls TUI rendering: false = hidden entirely, true =
// rendered with distinct styling.
type CustomMessageEntry struct {
	EntryBase
	CustomType string `json:"customType"`
	// Content is a string or a []types.Content of text/image parts (pi:
	// `string | (TextContent | ImageContent)[]`); values decoded from
	// generic JSON arrive as map/slice values.
	Content any  `json:"content"`
	Details any  `json:"details,omitempty"`
	Display bool `json:"display"`
}

// ContextEditableContent is the content shape an append-only context edit
// may replace without changing message metadata (pi: ContextEditableContent
// = UserMessage.content | AssistantMessage.content | ToolResultMessage.content
// | CustomMessage.content). Decoded values are string or []types.Content
// (see ContextEditReplacement.UnmarshalJSON).
type ContextEditableContent = any

// ContextEditEntry is an append-only edit of one earlier context-producing
// entry (pi: ContextEditEntry; v0.87.0). It changes only future model
// context; the target entry and its metadata remain unchanged in raw
// history, UI, exports, and session accounting.
type ContextEditEntry struct {
	EntryBase
	TargetID string `json:"targetId"`
	// Replacement nil (pi: null) omits the target from model context; a
	// value replaces only its content.
	Replacement *ContextEditReplacement `json:"replacement"`
}

// ContextEditReplacement is the non-null replacement payload of a
// ContextEditEntry (pi: `{ content: ContextEditableContent }`).
type ContextEditReplacement struct {
	Content ContextEditableContent `json:"content"`
}

// UnmarshalJSON decodes replacement content as string or []types.Content
// (pi: ContextEditableContent; pig decodes content-block arrays into typed
// Content at the boundary so projections see one shape). JSON null content
// normalizes to an empty array (pi's own writer never produces it;
// appendContextEdit rejects it).
func (r *ContextEditReplacement) UnmarshalJSON(data []byte) error {
	var probe struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	var s string
	if len(probe.Content) > 0 && json.Unmarshal(probe.Content, &s) == nil {
		r.Content = s
		return nil
	}
	parts, err := types.DecodeContentList(probe.Content)
	if err != nil {
		return err
	}
	r.Content = parts
	return nil
}

// rawFileEntry preserves an entry line whose shape pig's typed decode
// rejects; it round-trips byte-for-byte (see package header).
type rawFileEntry struct {
	entryType string
	raw       json.RawMessage
}

func (e *rawFileEntry) EntryType() string            { return e.entryType }
func (e *rawFileEntry) MarshalJSON() ([]byte, error) { return e.raw, nil }

// untypedMessage preserves a message payload whose role pig's typed decode
// doesn't recognize (pi's JSON.parse keeps arbitrary shapes verbatim; they
// flow through context building unchanged and round-trip on rewrite).
// hookMessage entries are rewritten by migrateV2ToV3 on this wrapper.
type untypedMessage struct {
	Role types.Role
	Raw  json.RawMessage
}

func (m *untypedMessage) MessageRole() types.Role      { return m.Role }
func (m *untypedMessage) MarshalJSON() ([]byte, error) { return m.Raw, nil }

// SessionTreeNode is a getTree node: a defensive copy of the session
// structure (pi: SessionTreeNode).
type SessionTreeNode struct {
	Entry    SessionEntry
	Children []*SessionTreeNode
	// Resolved label for this entry, if any.
	Label *string
	// Timestamp of the latest label change for this entry, if any.
	LabelTimestamp *string
}

// ProjectedSessionEntry is one selected entry with its model-visible
// messages after context edits (pi: ProjectedSessionEntry).
type ProjectedSessionEntry struct {
	// SourceEntry is the raw append-only entry that owns this projected
	// contribution.
	SourceEntry SessionEntry
	// Messages are the model-visible messages after context edits; empty
	// for state-only entries and omissions.
	Messages []agent.AgentMessage
}

// SessionProjection is the provenance-preserving, compaction-aware model
// context (pi: SessionProjection; v0.87.0).
type SessionProjection struct {
	Entries       []ProjectedSessionEntry
	Messages      []agent.AgentMessage
	ThinkingLevel string
	Model         *SessionModelRef
}

// SessionModelRef is the model reference carried by SessionContext (pi:
// SessionContext.model: { provider, modelId } | null).
type SessionModelRef struct {
	Provider string
	ModelID  string
}

// SessionContext is the resolved LLM context of a session (pi:
// SessionContext).
type SessionContext struct {
	Messages      []agent.AgentMessage
	ThinkingLevel string
	Model         *SessionModelRef
}

// SessionInfo describes one discovered session file (pi: SessionInfo).
type SessionInfo struct {
	Path string
	ID   string
	// Cwd is the working directory where the session was started; empty for
	// old sessions.
	Cwd string
	// Name is the user-defined display name from session_info entries.
	Name *string
	// ParentSessionPath points at the parent session (if this session was
	// forked).
	ParentSessionPath *string
	Created           time.Time
	Modified          time.Time
	MessageCount      int
	FirstMessage      string
	AllMessagesText   string
}

// ReadonlySessionManager is the read-only SessionManager surface (pi:
// ReadonlySessionManager = Pick<SessionManager, ...>).
type ReadonlySessionManager interface {
	GetCwd() string
	GetSessionDir() string
	GetSessionID() string
	GetSessionFile() string
	GetLeafID() *string
	GetLeafEntry() SessionEntry
	GetEntry(id string) SessionEntry
	GetLabel(id string) (string, bool)
	GetBranch(fromID string) []SessionEntry
	BuildContextEntries() []SessionEntry
	BuildSessionProjection() SessionProjection
	GetHeader() *SessionHeader
	GetEntries() []SessionEntry
	GetTree() []*SessionTreeNode
	GetSessionName() (string, bool)
}

func createSessionID() string {
	return aiutils.UUIDv7()
}

// AssertValidSessionID validates a caller-provided session id (pi:
// assertValidSessionId; pi throws → error return).
func AssertValidSessionID(id string) error {
	isAlnum := func(r rune) bool {
		return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}
	isInner := func(r rune) bool {
		return isAlnum(r) || r == '-' || r == '_' || r == '.'
	}
	runes := []rune(id)
	if len(runes) == 0 || !isAlnum(runes[0]) || !isAlnum(runes[len(runes)-1]) {
		return fmt.Errorf("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
	}
	for _, r := range runes {
		if !isInner(r) {
			return fmt.Errorf("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
		}
	}
	return nil
}

// randomUUIDString renders a random RFC 4122 v4 UUID (pi: randomUUID from
// node:crypto).
func randomUUIDString() string {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const hexdigits = "0123456789abcdef"
	hex := make([]byte, 0, 36)
	for i, byteVal := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			hex = append(hex, '-')
		}
		hex = append(hex, hexdigits[byteVal>>4], hexdigits[byteVal&0x0f])
	}
	return string(hex)
}

// generateID produces a unique short ID: 8 hex chars, collision-checked
// against exists (pi: generateId(byId)). Falls back to a full UUID after 100
// collisions.
func generateID(exists func(string) bool) string {
	for i := 0; i < 100; i++ {
		id := randomUUIDString()[:8]
		if !exists(id) {
			return id
		}
	}
	return randomUUIDString()
}

// migrateV1ToV2 adds id/parentId tree structure, in place (pi:
// migrateV1ToV2).
func migrateV1ToV2(entries []FileEntry) {
	ids := map[string]bool{}
	var prevID *string

	for _, entry := range entries {
		if header, ok := entry.(*SessionHeader); ok {
			v := 2
			header.Version = &v
			continue
		}
		se, ok := entry.(SessionEntry)
		if !ok {
			// rawFileEntry: pi assigns ids to raw objects too; pig cannot
			// reach into the preserved bytes (see package header).
			continue
		}
		base := se.Base()
		id := generateID(func(id string) bool { return ids[id] })
		ids[id] = true
		base.ID = id
		base.ParentID = prevID
		prevID = &id

		// Convert firstKeptEntryIndex to firstKeptEntryId for compaction.
		if comp, ok := entry.(*CompactionEntry); ok && comp.FirstKeptEntryIndex != nil {
			idx := *comp.FirstKeptEntryIndex
			if idx >= 0 && int(idx) < len(entries) {
				if target, ok := entries[idx].(SessionEntry); ok {
					comp.FirstKeptEntryID = target.Base().ID
				}
			}
			comp.FirstKeptEntryIndex = nil
		}
	}
}

// migrateV2ToV3 renames the hookMessage role to custom, in place (pi:
// migrateV2ToV3).
func migrateV2ToV3(entries []FileEntry) {
	for _, entry := range entries {
		if header, ok := entry.(*SessionHeader); ok {
			v := 3
			header.Version = &v
			continue
		}

		// Update message entries with hookMessage role.
		if msgEntry, ok := entry.(*SessionMessageEntry); ok {
			if um, ok := msgEntry.Message.(*untypedMessage); ok && um.Role == "hookMessage" {
				msgEntry.Message = rewriteHookMessage(um.Raw)
			}
		}
	}
}

// rewriteHookMessage re-types a hookMessage payload as a custom message (pi
// flips the role string on the parsed object; pig re-decodes the raw JSON
// and keeps the wrapper when the payload doesn't fit).
func rewriteHookMessage(raw json.RawMessage) agent.AgentMessage {
	var m harness.CustomMessage
	if err := json.Unmarshal(raw, &m); err == nil {
		m.Role = harness.RoleCustomMessage
		return m
	}
	return &untypedMessage{Role: harness.RoleCustomMessage, Raw: raw}
}

// migrateToCurrentVersion runs all necessary migrations, in place, and
// reports whether any was applied (pi: migrateToCurrentVersion).
func migrateToCurrentVersion(entries []FileEntry) bool {
	version := 1
	for _, entry := range entries {
		if header, ok := entry.(*SessionHeader); ok {
			if header.Version != nil {
				version = *header.Version
			}
			break
		}
	}

	if version >= CURRENT_SESSION_VERSION {
		return false
	}

	if version < 2 {
		migrateV1ToV2(entries)
	}
	if version < 3 {
		migrateV2ToV3(entries)
	}

	return true
}

// MigrateSessionEntries brings entries to the current version, in place (pi:
// migrateSessionEntries; exported for testing).
func MigrateSessionEntries(entries []FileEntry) {
	migrateToCurrentVersion(entries)
}

// ParseSessionEntries parses JSONL content into file entries, skipping
// malformed lines (pi: parseSessionEntries; exported for the compaction
// tests).
func ParseSessionEntries(content string) []FileEntry {
	var entries []FileEntry
	for _, line := range strings.Split(strings.Trim(content, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if entry := decodeFileEntryLine(line); entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// GetLatestCompactionEntry returns the most recent compaction entry, or nil
// (pi: getLatestCompactionEntry).
func GetLatestCompactionEntry(entries []SessionEntry) *CompactionEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if comp, ok := entries[i].(*CompactionEntry); ok {
			return comp
		}
	}
	return nil
}

// buildEntryIndex returns the given index or builds one from entries (pi:
// buildEntryIndex). byID nil = build fresh.
func buildEntryIndex(entries []SessionEntry, byID map[string]SessionEntry) map[string]SessionEntry {
	if byID != nil {
		return byID
	}
	index := make(map[string]SessionEntry, len(entries))
	for _, entry := range entries {
		index[entry.Base().ID] = entry
	}
	return index
}

// buildSessionPath resolves the path from root to leafID (pi:
// buildSessionPath). leafID nil = TS undefined → fall back to the last
// entry; an unknown id behaves the same way.
func buildSessionPath(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) []SessionEntry {
	index := buildEntryIndex(entries, byID)
	var leaf SessionEntry
	if leafID != nil {
		leaf = index[*leafID]
	}
	if leaf == nil {
		if len(entries) == 0 {
			return nil
		}
		leaf = entries[len(entries)-1]
	}

	var path []SessionEntry
	current := leaf
	for current != nil {
		path = append(path, current)
		if parentID := current.Base().ParentID; parentID != nil {
			current = index[*parentID]
		} else {
			current = nil
		}
	}
	// reverse in place
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// getSessionContextSettings reduces thinkingLevel/model from a path (pi:
// getSessionContextSettings).
func getSessionContextSettings(path []SessionEntry) (string, *SessionModelRef) {
	thinkingLevel := "off"
	var model *SessionModelRef

	for _, entry := range path {
		switch e := entry.(type) {
		case *ThinkingLevelChangeEntry:
			thinkingLevel = e.ThinkingLevel
		case *ModelChangeEntry:
			model = &SessionModelRef{Provider: e.Provider, ModelID: e.ModelID}
		case *SessionMessageEntry:
			if msg, ok := e.Message.(types.AssistantMessage); ok {
				model = &SessionModelRef{Provider: string(msg.Provider), ModelID: msg.Model}
			}
		}
	}

	return thinkingLevel, model
}

// SessionEntryToContextMessages projects one selected session entry into
// LLM/runtime messages. Plain custom entries are display/state entries and
// do not participate in context (pi: sessionEntryToContextMessages).
func SessionEntryToContextMessages(entry SessionEntry) []agent.AgentMessage {
	switch e := entry.(type) {
	case *SessionMessageEntry:
		message := e.Message
		if message == nil {
			// pi throws TypeError on message entries without a message; pig
			// skips them to keep the reader total (corrupt input only).
			return nil
		}
		// Session files are parsed without validation; old versions, forks,
		// or hand-edited files can contain messages with null/missing
		// content.
		switch m := message.(type) {
		case types.SystemMessage:
			// pi: a system message with null content normalizes to "" (the
			// structured sections carry the prompt).
			if m.Content.String == "" && m.Content.Parts == nil {
				message = m
			}
		case types.UserMessage:
			if m.Content.String == "" && m.Content.Parts == nil {
				// Deviation: pi's `content == null` cannot distinguish an
				// empty-string content after typed decode; both normalize to
				// empty content.
				m.Content.Parts = []types.Content{}
			}
			message = m
		case types.AssistantMessage:
			if m.Content == nil {
				m.Content = []types.Content{}
			}
			message = m
		case types.ToolResultMessage:
			if m.Content == nil {
				m.Content = []types.Content{}
			}
			message = m
		}
		return []agent.AgentMessage{message}
	case *CustomMessageEntry:
		content := e.Content
		if content == nil {
			content = []types.Content{}
		}
		return []agent.AgentMessage{
			harness.CreateCustomMessage(e.CustomType, content, e.Display, e.Details, isoToMillis(e.Timestamp)),
		}
	case *BranchSummaryEntry:
		if e.Summary != "" {
			return []agent.AgentMessage{
				harness.CreateBranchSummaryMessage(e.Summary, lo.ToPtr(e.FromID), isoToMillis(e.Timestamp)),
			}
		}
		return nil
	case *CompactionEntry:
		// pi v0.86.0: the compaction entry replays the complete prompt state
		// (system message) ahead of the summary so mid-conversation system
		// messages survive compaction.
		summary := harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, isoToMillis(e.Timestamp))
		if e.SystemMessage != nil {
			return []agent.AgentMessage{*e.SystemMessage, summary}
		}
		return []agent.AgentMessage{summary}
	}
	return nil
}

// BuildContextEntries builds the active, compaction-aware session entry
// list. This follows the leaf path; if the path contains compaction entries,
// the latest compaction is represented by the compaction entry itself,
// followed by the kept entries starting at firstKeptEntryId and all entries
// after the compaction entry. Older summarized entries are omitted (pi:
// buildContextEntries; leafID nil = TS undefined → last entry).
func BuildContextEntries(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) []SessionEntry {
	path := buildSessionPath(entries, leafID, byID)
	var compaction *CompactionEntry

	for _, entry := range path {
		if comp, ok := entry.(*CompactionEntry); ok {
			compaction = comp
		}
	}

	if compaction == nil {
		return path
	}

	compactionIdx := -1
	for i, entry := range path {
		if entry.Base().ID == compaction.Base().ID {
			compactionIdx = i
			break
		}
	}
	if compactionIdx < 0 {
		return path
	}

	contextEntries := []SessionEntry{compaction}
	foundFirstKept := false
	for i := 0; i < compactionIdx; i++ {
		entry := path[i]
		if entry.Base().ID == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		// pi v0.86.0: system messages are prompt state, not conversation —
		// the compaction entry carries their replay.
		if foundFirstKept {
			if messageEntry, ok := entry.(*SessionMessageEntry); ok && messageEntry.Message != nil && messageEntry.Message.MessageRole() == types.RoleSystem {
				continue
			}
			contextEntries = append(contextEntries, entry)
		}
	}
	contextEntries = append(contextEntries, path[compactionIdx+1:]...)
	return contextEntries
}

// projectContextEntry projects one selected entry, applying the latest
// branch-relative context edit targeting it (pi: projectContextEntry; edit
// nil = TS undefined).
func projectContextEntry(entry SessionEntry, edit *ContextEditEntry) []agent.AgentMessage {
	messages := SessionEntryToContextMessages(entry)
	if edit == nil {
		return messages
	}
	if edit.Replacement == nil {
		// Null replacement omits the target from model context.
		return []agent.AgentMessage{}
	}
	content := edit.Replacement.Content
	projected := make([]agent.AgentMessage, 0, len(messages))
	for _, message := range messages {
		projected = append(projected, replaceMessageContent(message, content))
	}
	return projected
}

// replaceMessageContent clones a message with the replacement content,
// leaving roles without editable content unchanged (pi: projectContextEntry's
// map callback; assistant/toolResult string replacements normalize to one
// text block because those roles require content arrays).
func replaceMessageContent(message agent.AgentMessage, content ContextEditableContent) agent.AgentMessage {
	switch m := message.(type) {
	case types.UserMessage:
		m.Content = userContentFromReplacement(content)
		return m
	case types.AssistantMessage:
		m.Content = contentBlocksFromReplacement(content)
		return m
	case types.ToolResultMessage:
		m.Content = contentBlocksFromReplacement(content)
		return m
	case harness.CustomMessage:
		m.Content = content
		return m
	}
	return message
}

// userContentFromReplacement maps replacement content onto the user-message
// content union (string stays string; arrays become typed parts).
func userContentFromReplacement(content ContextEditableContent) types.UserContent {
	switch c := content.(type) {
	case string:
		return types.UserContent{String: c}
	case []types.Content:
		return types.UserContent{Parts: c}
	}
	// pi carries arbitrary shapes through; the typed world normalizes
	// unknown shapes to empty content.
	return types.UserContent{Parts: []types.Content{}}
}

// contentBlocksFromReplacement maps replacement content onto the
// array-only assistant/toolResult content shape.
func contentBlocksFromReplacement(content ContextEditableContent) []types.Content {
	switch c := content.(type) {
	case string:
		return []types.Content{types.NewTextContent(c)}
	case []types.Content:
		return c
	}
	// pi carries arbitrary shapes through; the typed world normalizes
	// unknown shapes to an empty array.
	return []types.Content{}
}

// BuildSessionProjection builds the provenance-preserving, compaction-aware
// model context from entries using tree traversal. If leafID is provided,
// walks from that entry to root. Handles compaction, branch summaries, and
// context edits along the path (pi: buildSessionProjection; leafID nil = TS
// undefined → last entry).
func BuildSessionProjection(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) SessionProjection {
	path := buildSessionPath(entries, leafID, byID)
	thinkingLevel, model := getSessionContextSettings(path)
	contextEntries := BuildContextEntries(entries, leafID, byID)
	edits := map[string]*ContextEditEntry{}
	for _, entry := range contextEntries {
		if edit, ok := entry.(*ContextEditEntry); ok {
			edits[edit.TargetID] = edit
		}
	}
	projectedEntries := make([]ProjectedSessionEntry, 0, len(contextEntries))
	for index, sourceEntry := range contextEntries {
		var messages []agent.AgentMessage
		// buildContextEntries() may retain an older compaction entry because
		// its raw ID lies inside the newest retained range. Only the newest
		// compaction at index zero contributes a checkpoint and summary.
		_, isCompaction := sourceEntry.(*CompactionEntry)
		if !isCompaction || index == 0 {
			messages = projectContextEntry(sourceEntry, edits[sourceEntry.Base().ID])
		} else {
			messages = []agent.AgentMessage{}
		}
		projectedEntries = append(projectedEntries, ProjectedSessionEntry{SourceEntry: sourceEntry, Messages: messages})
	}
	var messages []agent.AgentMessage
	for _, entry := range projectedEntries {
		messages = append(messages, entry.Messages...)
	}
	return SessionProjection{Entries: projectedEntries, Messages: messages, ThinkingLevel: thinkingLevel, Model: model}
}

// BuildSessionContext builds the finalized model context from the canonical
// session projection (pi: buildSessionContext; leafID nil = TS undefined →
// last entry).
func BuildSessionContext(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) SessionContext {
	projection := BuildSessionProjection(entries, leafID, byID)
	return SessionContext{Messages: projection.Messages, ThinkingLevel: projection.ThinkingLevel, Model: projection.Model}
}

// getDefaultSessionDirPath computes the default session directory for a cwd,
// encoding cwd into a safe directory name under the agent sessions dir (pi:
// getDefaultSessionDirPath).
func getDefaultSessionDirPath(cwd string, agentDir string) string {
	resolvedCwd := utils.ResolvePath(cwd, "", nil)
	resolvedAgentDir := utils.ResolvePath(agentDir, "", nil)
	// pi: replace(/^[/\\]/, "").replace(/[/\\:]/g, "-"); TrimLeft covers the
	// single-leading-separator case (Clean never leaves several).
	safe := strings.TrimLeft(resolvedCwd, "/\\")
	safe = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(safe)
	safe = "--" + safe + "--"
	return filepath.Join(resolvedAgentDir, "sessions", safe)
}

// GetDefaultSessionDir returns (creating if needed) the default session
// directory for a cwd (pi: getDefaultSessionDir; pi's mkdirSync throws →
// error return). agentDir "" = the default agent dir.
func GetDefaultSessionDir(cwd string, agentDir string) (string, error) {
	if agentDir == "" {
		agentDir = codingagent.GetAgentDir()
	}
	sessionDir := getDefaultSessionDirPath(cwd, agentDir)
	if !existsPath(sessionDir) {
		if err := os.MkdirAll(sessionDir, 0o755); err != nil {
			return "", err
		}
	}
	return sessionDir, nil
}

const (
	SESSION_READ_BUFFER_SIZE        = 1024 * 1024
	SESSION_HEADER_READ_BUFFER_SIZE = 4096
	// MAX_SESSION_HEADER_SCAN_BYTES bounds synchronous header discovery
	// while allowing large cwd and custom metadata fields.
	MAX_SESSION_HEADER_SCAN_BYTES = 1024 * 1024
)

// SessionHeaderScanLimitError reports a header beyond the bounded scan (pi:
// SessionHeaderScanLimitError).
type SessionHeaderScanLimitError struct {
	FilePath string
}

func (e *SessionHeaderScanLimitError) Error() string {
	return fmt.Sprintf("Session header exceeds %d-byte scan limit: %s", MAX_SESSION_HEADER_SCAN_BYTES, e.FilePath)
}

// decodeTypedFileEntry decodes one line into a typed entry, dispatching on
// the "type" field (pi: JSON.parse + cast; pig unions per §3.4).
func decodeTypedFileEntry(data []byte) (FileEntry, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case "session":
		var h SessionHeader
		return &h, json.Unmarshal(data, &h)
	case "message":
		var e SessionMessageEntry
		var aux map[string]json.RawMessage
		if err := json.Unmarshal(data, &aux); err != nil {
			return nil, err
		}
		raw, ok := aux["message"]
		delete(aux, "message")
		stripped, err := json.Marshal(aux)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(stripped, &e); err != nil {
			return nil, err
		}
		if ok {
			msg, err := decodeAgentMessage(raw)
			if err != nil {
				return nil, err
			}
			e.Message = msg
		}
		return &e, nil
	case "thinking_level_change":
		var e ThinkingLevelChangeEntry
		return &e, json.Unmarshal(data, &e)
	case "model_change":
		var e ModelChangeEntry
		return &e, json.Unmarshal(data, &e)
	case "usage":
		var e UsageEntry
		return &e, json.Unmarshal(data, &e)
	case "compaction":
		var e CompactionEntry
		return &e, json.Unmarshal(data, &e)
	case "branch_summary":
		var e BranchSummaryEntry
		return &e, json.Unmarshal(data, &e)
	case "custom":
		var e CustomEntry
		return &e, json.Unmarshal(data, &e)
	case "custom_message":
		var e CustomMessageEntry
		return &e, json.Unmarshal(data, &e)
	case "context_edit":
		var e ContextEditEntry
		return &e, json.Unmarshal(data, &e)
	case "label":
		var e LabelEntry
		return &e, json.Unmarshal(data, &e)
	case "session_info":
		var e SessionInfoEntry
		return &e, json.Unmarshal(data, &e)
	default:
		return nil, fmt.Errorf("unknown session entry type %q", probe.Type)
	}
}

// decodeFileEntryLine decodes one session-file line, preserving unknown
// shapes as rawFileEntry and returning nil for blank/malformed lines (pi:
// parseSessionEntryLine; JSON.parse keeps any object, pig falls back).
func decodeFileEntryLine(line string) FileEntry {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if entry, err := decodeTypedFileEntry([]byte(line)); err == nil {
		return entry
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		// Skip malformed lines.
		return nil
	}
	var probe struct {
		Type *string `json:"type"`
	}
	_ = json.Unmarshal(raw, &probe)
	entryType := ""
	if probe.Type != nil {
		entryType = *probe.Type
	}
	return &rawFileEntry{entryType: entryType, raw: raw}
}

// decodeAgentMessage decodes a session message payload, permissively: the
// three LLM roles via types.DecodeMessage, the four harness custom roles as
// typed structs, anything else as untypedMessage (pi: JSON.parse with no
// validation).
func decodeAgentMessage(raw json.RawMessage) (agent.AgentMessage, error) {
	var probe struct {
		Role types.Role `json:"role"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.Role {
	case types.RoleSystem, types.RoleUser, types.RoleAssistant, types.RoleToolResult:
		return types.DecodeMessage(raw)
	case harness.RoleBashExecution:
		var m harness.BashExecutionMessage
		return m, json.Unmarshal(raw, &m)
	case harness.RoleCustomMessage:
		var m harness.CustomMessage
		return m, json.Unmarshal(raw, &m)
	case harness.RoleBranchSummary:
		var m harness.BranchSummaryMessage
		return m, json.Unmarshal(raw, &m)
	case harness.RoleCompactionSummary:
		var m harness.CompactionSummaryMessage
		return m, json.Unmarshal(raw, &m)
	default:
		return &untypedMessage{Role: probe.Role, Raw: raw}, nil
	}
}

// LoadEntriesFromFile loads and decodes a session file, skipping malformed
// lines and validating the header (pi: loadEntriesFromFile; exported for
// testing). Returns nil for missing/invalid files.
func LoadEntriesFromFile(filePath string) []FileEntry {
	resolvedFilePath := utils.NormalizePath(filePath, nil)
	if !existsPath(resolvedFilePath) {
		return nil
	}

	f, err := os.Open(resolvedFilePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []FileEntry
	reader := newSessionLineReader(f, SESSION_READ_BUFFER_SIZE)
	for {
		line, ok := reader.next()
		if !ok {
			break
		}
		if entry := decodeFileEntryLine(line); entry != nil {
			entries = append(entries, entry)
		}
	}

	// Validate session header before repairing the file.
	if len(entries) == 0 {
		return entries
	}
	header, ok := entries[0].(*SessionHeader)
	if !ok || header.ID == "" {
		return nil
	}

	// pi (v0.85.1): a trailing partial line means the file was cut off
	// mid-write; terminate it with a newline so the repair is durable.
	if reader.hadTrailing {
		f, err := os.OpenFile(resolvedFilePath, os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.WriteString("\n")
			_ = f.Close()
		}
	}
	return entries
}

// sessionLineReader splits a stream into \n-terminated lines with bounded
// memory, mirroring pi's chunked readSync + StringDecoder loop (arbitrary
// line lengths are fine; UTF-8 multibyte sequences cannot split mid-rune in
// the byte domain).
type sessionLineReader struct {
	reader  *os.File
	buf     []byte
	pending []byte
	// hadTrailing reports whether the stream ended with an unterminated
	// line (pi's pending StringDecoder residue after the read loop).
	hadTrailing bool
}

func newSessionLineReader(f *os.File, bufSize int) *sessionLineReader {
	return &sessionLineReader{reader: f, buf: make([]byte, bufSize)}
}

// next returns the next line (without the newline); ok=false at EOF.
func (r *sessionLineReader) next() (string, bool) {
	for {
		// bytes.IndexByte (memchr) instead of a per-byte loop: the buffer is
		// re-scanned from the start on every read, so a hand-rolled scan made
		// long lines quadratic and cost ~35s on the 512MB fixture under -race.
		if idx := bytes.IndexByte(r.pending, '\n'); idx >= 0 {
			line := string(r.pending[:idx])
			r.pending = r.pending[idx+1:]
			return line, true
		}
		n, err := r.reader.Read(r.buf)
		if n > 0 {
			r.pending = append(r.pending, r.buf[:n]...)
		}
		if err != nil || n == 0 {
			if len(r.pending) > 0 {
				line := string(r.pending)
				r.pending = nil
				// pi (v0.85.1): remember the unterminated tail so the caller
				// can repair the file by appending a newline.
				r.hadTrailing = true
				return line, true
			}
			return "", false
		}
	}
}

// parseSessionHeaderCandidate inspects one line while searching for the
// first parsed session entry. Returns (nil, 0) to keep scanning, (nil, -1)
// for a parsed non-header entry, or the header (pi: parseSessionHeaderCandidate's
// undefined/null/header triple).
func parseSessionHeaderCandidate(line string) (*SessionHeader, int) {
	if strings.TrimSpace(line) == "" {
		return nil, 0
	}
	entry := decodeFileEntryLine(line)
	if entry == nil {
		return nil, 0
	}
	header, ok := entry.(*SessionHeader)
	if !ok || header.ID == "" {
		return nil, -1
	}
	return header, 1
}

// readSessionHeader reads just the session header with a bounded scan (pi:
// readSessionHeader). Reads never exceed MAX_SESSION_HEADER_SCAN_BYTES;
// partial trailing lines are only accepted when the file ends exactly at the
// limit.
func readSessionHeader(filePath string) (*SessionHeader, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, SESSION_HEADER_READ_BUFFER_SIZE)
	buf := make([]byte, SESSION_HEADER_READ_BUFFER_SIZE)
	var lineChunks []byte
	scannedBytes := 0

	for scannedBytes < MAX_SESSION_HEADER_SCAN_BYTES {
		readLength := len(buf)
		if remaining := MAX_SESSION_HEADER_SCAN_BYTES - scannedBytes; remaining < readLength {
			readLength = remaining
		}
		bytesRead, readErr := reader.Read(buf[:readLength])
		if bytesRead == 0 {
			if readErr != nil && readErr != io.EOF {
				return nil, readErr
			}
			header, _ := parseSessionHeaderCandidate(string(lineChunks))
			return header, nil
		}
		scannedBytes += bytesRead

		chunk := buf[:bytesRead]
		lineStart := 0
		for i := 0; i < len(chunk); i++ {
			if chunk[i] != '\n' {
				continue
			}
			lineChunks = append(lineChunks, chunk[lineStart:i]...)
			header, state := parseSessionHeaderCandidate(string(lineChunks))
			if state == 1 {
				return header, nil
			}
			if state == -1 {
				return nil, nil
			}
			lineChunks = lineChunks[:0]
			lineStart = i + 1
		}
		lineChunks = append(lineChunks, chunk[lineStart:]...)
		if readErr == io.EOF {
			header, _ := parseSessionHeaderCandidate(string(lineChunks))
			return header, nil
		}
	}

	// Probe for EOF so a final header without a newline is allowed when it
	// ends exactly at the scan limit. Any additional byte exceeds the
	// bounded scan.
	if _, err := reader.ReadByte(); err == io.EOF {
		header, _ := parseSessionHeaderCandidate(string(lineChunks))
		return header, nil
	}
	return nil, &SessionHeaderScanLimitError{FilePath: filePath}
}

// readSessionHeaderForDiscovery is readSessionHeader with all failures
// swallowed: discovery is best-effort, and one corrupt file must not prevent
// other sessions from being found (pi: readSessionHeaderForDiscovery).
func readSessionHeaderForDiscovery(filePath string) *SessionHeader {
	header, err := readSessionHeader(filePath)
	if err != nil {
		return nil
	}
	return header
}

// sessionCwdMatches reports whether a session header cwd resolves to
// resolvedCwd (pi: sessionCwdMatches). Empty/absent cwd never matches.
func sessionCwdMatches(cwd string, resolvedCwd string) bool {
	return cwd != "" && utils.ResolvePath(cwd, "", nil) == resolvedCwd
}

// FindMostRecentSession returns the most recently modified session file in
// sessionDir, optionally filtered by cwd (pi: findMostRecentSession; cwd ""
// = no filter). Returns nil when unavailable. pi v0.86.0 sorts by mtime
// first and then finds the first header match, so a corrupt header no
// longer hides a newer matching session behind it.
func FindMostRecentSession(sessionDir string, cwd string) *string {
	resolvedSessionDir := utils.NormalizePath(sessionDir, nil)
	resolvedCwd := ""
	if cwd != "" {
		resolvedCwd = utils.ResolvePath(cwd, "", nil)
	}
	dirEntries, err := os.ReadDir(resolvedSessionDir)
	if err != nil {
		// Directory access and stat races make recent-session discovery
		// unavailable.
		return nil
	}
	type recentFile struct {
		path  string
		mtime int64
	}
	var files []recentFile
	for _, de := range dirEntries {
		name := de.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		path := filepath.Join(resolvedSessionDir, name)
		info, err := os.Stat(path)
		if err != nil {
			// pi: a stat race aborts the whole discovery via try/catch.
			return nil
		}
		files = append(files, recentFile{path: path, mtime: info.ModTime().UnixMilli()})
	}
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].mtime > files[j].mtime
	})
	for _, file := range files {
		header := readSessionHeaderForDiscovery(file.path)
		if header == nil {
			continue
		}
		if resolvedCwd != "" && !sessionCwdMatches(header.Cwd, resolvedCwd) {
			continue
		}
		path := file.path
		return &path
	}
	return nil
}

// isoToMillis parses an ISO timestamp to Unix milliseconds (pi:
// new Date(str).getTime(); invalid → 0 where pi would produce NaN).
func isoToMillis(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

// IsoToMillis is the exported form of isoToMillis for consumers outside
// core (pig-only export; pi inlines new Date(str).getTime() at call sites).
func IsoToMillis(s string) int64 { return isoToMillis(s) }

// parseTimestamp parses an ISO timestamp (pi: new Date(str).getTime(); ok
// =false = NaN).
func parseTimestamp(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// sessionTimestamp renders the current time as a JS-style ISO string (pi:
// new Date().toISOString()).
func sessionTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// sessionFileTimestamp sanitizes an ISO timestamp for file names (pi:
// timestamp.replace(/[:.]/g, "-")).
func sessionFileTimestamp(timestamp string) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(timestamp)
}

func existsPath(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// SessionManager manages conversation sessions as append-only trees stored
// in JSONL files (pi: SessionManager).
//
// Each session entry has an id and parentId forming a tree structure. The
// "leaf" pointer tracks the current position. Appending creates a child of
// the current leaf. Branching moves the leaf to an earlier entry, allowing
// new branches without modifying history.
//
// Use BuildSessionContext to get the resolved message list for the LLM,
// which handles compaction summaries and follows the path from root to
// current leaf.
//
// pig: a single mutex guards the mutable state (pi relies on the
// single-threaded event loop; same convention as agent.Agent).
type SessionManager struct {
	mu sync.Mutex

	sessionID   string
	sessionFile string // "" = unset (pi: string | undefined)
	sessionDir  string
	cwd         string
	persist     bool
	flushed     bool
	fileEntries []FileEntry
	byID        map[string]SessionEntry
	labelsByID  map[string]string
	labelTsByID map[string]string
	leafID      *string
}

// newSessionManager mirrors pi's private constructor. sessionFile "" =
// start a new session.
func newSessionManager(cwd string, sessionDir string, sessionFile string, persist bool, options *NewSessionOptions, preloadedFileEntries []FileEntry) (*SessionManager, error) {
	m := &SessionManager{
		cwd:         utils.ResolvePath(cwd, "", nil),
		sessionDir:  utils.NormalizePath(sessionDir, nil),
		persist:     persist,
		byID:        map[string]SessionEntry{},
		labelsByID:  map[string]string{},
		labelTsByID: map[string]string{},
	}
	if persist && m.sessionDir != "" && !existsPath(m.sessionDir) {
		if err := os.MkdirAll(m.sessionDir, 0o755); err != nil {
			return nil, err
		}
	}

	if sessionFile != "" {
		if err := m.setSessionFilePreloaded(sessionFile, preloadedFileEntries); err != nil {
			return nil, err
		}
	} else if len(preloadedFileEntries) > 0 {
		// pi (v0.85.1): entries held outside the filesystem load directly
		// instead of starting a fresh session.
		if err := m.loadEntriesLocked(preloadedFileEntries, options); err != nil {
			return nil, err
		}
	} else {
		if err := m.newSessionLocked(options); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// SetSessionFile switches to a different session file (used for resume and
// branching; pi: setSessionFile; throws → error return).
func (m *SessionManager) SetSessionFile(sessionFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setSessionFilePreloaded(sessionFile, nil)
}

func (m *SessionManager) setSessionFilePreloaded(sessionFile string, preloadedFileEntries []FileEntry) error {
	m.sessionFile = utils.ResolvePath(sessionFile, "", nil)
	if existsPath(m.sessionFile) {
		entries := preloadedFileEntries
		if entries == nil {
			entries = LoadEntriesFromFile(m.sessionFile)
		}

		// If file was empty, initialize it with a valid session header. If
		// it was non-empty but did not parse as a pi session, fail without
		// modifying it.
		if len(entries) == 0 {
			explicitPath := m.sessionFile
			info, err := os.Stat(explicitPath)
			if err != nil {
				return err
			}
			if info.Size() > 0 {
				return fmt.Errorf("Session file is not a valid %s session: %s", codingagent.AppName, explicitPath)
			}
			if err := m.newSessionLocked(nil); err != nil {
				return err
			}
			m.sessionFile = explicitPath
			if err := m.rewriteFileLocked(); err != nil {
				return err
			}
			m.flushed = true
			return nil
		}

		if err := m.loadEntriesLocked(entries, nil); err != nil {
			return err
		}
		m.flushed = true
	} else {
		explicitPath := m.sessionFile
		if err := m.newSessionLocked(nil); err != nil {
			return err
		}
		m.sessionFile = explicitPath // preserve explicit path from --session flag
	}
	return nil
}

// NewSession starts a new session and returns the session file path (empty
// when not persisting; pi: newSession; invalid ids throw → error return).
func (m *SessionManager) NewSession(options *NewSessionOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.newSessionLocked(options); err != nil {
		return "", err
	}
	return m.sessionFile, nil
}

func (m *SessionManager) newSessionLocked(options *NewSessionOptions) error {
	if options != nil && options.ID != nil {
		if err := AssertValidSessionID(*options.ID); err != nil {
			return err
		}
	}
	if options != nil && options.ID != nil {
		m.sessionID = *options.ID
	} else {
		m.sessionID = createSessionID()
	}
	timestamp := sessionTimestamp()
	var version = CURRENT_SESSION_VERSION
	header := &SessionHeader{
		Type:          "session",
		Version:       &version,
		ID:            m.sessionID,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: optionsPtrParentSession(options),
	}
	m.fileEntries = []FileEntry{header}
	m.byID = map[string]SessionEntry{}
	m.labelsByID = map[string]string{}
	m.labelTsByID = map[string]string{}
	m.leafID = nil
	m.flushed = false

	if m.persist {
		fileTimestamp := sessionFileTimestamp(timestamp)
		m.sessionFile = filepath.Join(m.GetSessionDir(), fmt.Sprintf("%s_%s.jsonl", fileTimestamp, m.sessionID))
	}
	return nil
}

func optionsPtrParentSession(options *NewSessionOptions) *string {
	if options == nil {
		return nil
	}
	return options.ParentSession
}

// loadEntriesLocked installs entries into the manager (pi: _loadEntries).
// With a session header the entries become the session; without one a fresh
// session is created and the entries are appended after it. Callers hold
// m.mu (construction / setSessionFilePreloaded paths).
func (m *SessionManager) loadEntriesLocked(entries []FileEntry, options *NewSessionOptions) error {
	var header *SessionHeader
	for _, entry := range entries {
		if h, ok := entry.(*SessionHeader); ok {
			header = h
			break
		}
	}

	if header != nil {
		m.fileEntries = entries
		m.sessionID = header.ID

		if migrateToCurrentVersion(m.fileEntries) {
			if err := m.rewriteFileLocked(); err != nil {
				return err
			}
		}
	} else {
		if err := m.newSessionLocked(options); err != nil {
			return err
		}
		m.fileEntries = append(m.fileEntries, entries...)
	}

	m.buildIndexLocked()
	return nil
}

func (m *SessionManager) buildIndexLocked() {
	m.byID = map[string]SessionEntry{}
	m.labelsByID = map[string]string{}
	m.labelTsByID = map[string]string{}
	m.leafID = nil
	for _, entry := range m.fileEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		se, ok := entry.(SessionEntry)
		if !ok {
			// rawFileEntry: pi indexes parsed objects by their id too; raw
			// shapes have no reachable id (see package header).
			continue
		}
		m.byID[se.Base().ID] = se
		leafID := se.Base().ID
		m.leafID = &leafID
		if labelEntry, ok := se.(*LabelEntry); ok {
			if labelEntry.Label != nil && *labelEntry.Label != "" {
				m.labelsByID[labelEntry.TargetID] = *labelEntry.Label
				m.labelTsByID[labelEntry.TargetID] = labelEntry.Base().Timestamp
			} else {
				delete(m.labelsByID, labelEntry.TargetID)
				delete(m.labelTsByID, labelEntry.TargetID)
			}
		}
	}
}

func (m *SessionManager) rewriteFileLocked() error {
	if !m.persist || m.sessionFile == "" {
		return nil
	}
	f, err := os.OpenFile(m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, entry := range m.fileEntries {
		if err := writeJSONLine(f, entry); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONLine(f *os.File, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}

// IsPersisted reports whether the session is file-backed (pi: isPersisted).
func (m *SessionManager) IsPersisted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persist
}

// GetCwd returns the session working directory (pi: getCwd).
func (m *SessionManager) GetCwd() string {
	return m.cwd
}

// GetSessionDir returns the session directory (pi: getSessionDir).
func (m *SessionManager) GetSessionDir() string {
	return m.sessionDir
}

// UsesDefaultSessionDir reports whether the session lives in the default
// per-cwd directory (pi: usesDefaultSessionDir).
func (m *SessionManager) UsesDefaultSessionDir() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionDir == getDefaultSessionDirPath(m.cwd, codingagent.GetAgentDir())
}

// GetSessionID returns the session id (pi: getSessionId).
func (m *SessionManager) GetSessionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionID
}

// GetSessionFile returns the session file path; "" = unset (pi:
// getSessionFile: string | undefined).
func (m *SessionManager) GetSessionFile() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionFile
}

// persist appends an entry to the session file. Writing is deferred until
// the first assistant message so that abandoned short turns never hit disk
// (pi: _persist; pig keeps it unexported — reach for it again if the
// extensions batch needs it).
func (m *SessionManager) persistLocked(entry SessionEntry) error {
	if !m.persist || m.sessionFile == "" {
		return nil
	}

	hasAssistant := false
	for _, e := range m.fileEntries {
		if me, ok := e.(*SessionMessageEntry); ok {
			if _, ok := me.Message.(types.AssistantMessage); ok {
				hasAssistant = true
				break
			}
		}
	}
	if !hasAssistant {
		if m.flushed {
			return appendJSONLLine(m.sessionFile, entry)
		}
		// Mark as not flushed so when assistant arrives, all entries get
		// written.
		m.flushed = false
		return nil
	}

	if !m.flushed {
		f, err := os.OpenFile(m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		for _, e := range m.fileEntries {
			if err := writeJSONLine(f, e); err != nil {
				return err
			}
		}
		m.flushed = true
		return nil
	}
	return appendJSONLLine(m.sessionFile, entry)
}

// appendJSONLLine appends one JSON-encoded value as a line (pi:
// appendFileSync(file, `${JSON.stringify(entry)}\n`), which creates the
// file when missing).
func appendJSONLLine(path string, v any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return writeJSONLine(f, v)
}

func (m *SessionManager) appendEntryLocked(entry SessionEntry) error {
	m.fileEntries = append(m.fileEntries, entry)
	m.byID[entry.Base().ID] = entry
	leafID := entry.Base().ID
	m.leafID = &leafID
	return m.persistLocked(entry)
}

// AppendMessage appends a message as child of current leaf, then advances
// the leaf. Returns the entry id. Does not allow writing compaction summary
// and branch summary messages directly: these are top-level entries in the
// session, so it is easier to find them; they need to be appended via
// AppendCompaction and BranchWithSummary (pi: appendMessage; the TS
// parameter is `Message | CustomMessage | BashExecutionMessage`, pig's open
// AgentMessage channel).
func (m *SessionManager) AppendMessage(message agent.AgentMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &SessionMessageEntry{Message: message}
	entry.Type = "message"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendThinkingLevelChange appends a thinking level change as child of
// current leaf, then advances leaf. Returns the entry id (pi:
// appendThinkingLevelChange).
func (m *SessionManager) AppendThinkingLevelChange(thinkingLevel string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &ThinkingLevelChangeEntry{ThinkingLevel: thinkingLevel}
	entry.Type = "thinking_level_change"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendModelChange appends a model change as child of current leaf, then
// advances leaf. Returns the entry id (pi: appendModelChange).
func (m *SessionManager) AppendModelChange(provider string, modelID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &ModelChangeEntry{Provider: provider, ModelID: modelID}
	entry.Type = "model_change"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendCompaction appends a compaction summary as child of current leaf,
// then advances leaf. Returns the entry id (pi: appendCompaction; fromHook
// nil = TS undefined).
// AppendUsage appends model-attributed usage that does not participate in
// LLM context. Returns the appended entry (pi: appendUsage; v0.86.0).
func (m *SessionManager) AppendUsage(kind string, provider string, model string, usage *types.Usage, note string) (*UsageEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &UsageEntry{
		Kind:     kind,
		Provider: provider,
		Model:    model,
		Usage:    usage,
		Note:     note,
	}
	entry.Type = "usage"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// AppendCompaction appends a compaction summary as child of current leaf,
// then advances leaf. Returns the entry id (pi: appendCompaction). The
// complete prompt state at this boundary is captured from the current
// session context (pi v0.86.0).
func (m *SessionManager) AppendCompaction(summary string, firstKeptEntryID *string, tokensBefore int64, details any, fromHook *bool, usage *types.Usage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	timestamp := sessionTimestamp()
	systemMessage := currentSystemMessageFromEntries(m.getEntriesLocked(), m.leafID, m.byID)
	id := m.generateIDLocked()
	// pi v0.87.0: firstKeptEntryId ?? id — a null firstKeptEntryId makes the
	// compaction self-referencing (retain none of the preceding entries).
	keptID := id
	if firstKeptEntryID != nil {
		keptID = *firstKeptEntryID
	}
	entry := &CompactionEntry{
		Summary:          summary,
		FirstKeptEntryID: keptID,
		TokensBefore:     tokensBefore,
		Details:          details,
		Usage:            usage,
		FromHook:         fromHook,
	}
	if systemMessage != nil {
		captured := *systemMessage
		captured.Timestamp = isoToMillis(timestamp)
		entry.SystemMessage = &captured
	}
	entry.Type = "compaction"
	entry.ID = id
	entry.ParentID = m.leafID
	entry.Timestamp = timestamp
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// currentSystemMessageFromEntries replays the transcript's system messages
// for the current branch (pi: getCurrentSystemMessage(
// this.buildSessionProjection().messages); pig works from the entry list
// with the package-level projection to avoid re-locking the manager under
// the held mutex).
func currentSystemMessageFromEntries(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) *types.SystemMessage {
	projection := BuildSessionProjection(entries, leafID, byID)
	var transcript aiutils.TranscriptMessages
	for _, message := range projection.Messages {
		if typed, ok := message.(types.Message); ok {
			transcript = append(transcript, typed)
		}
	}
	return aiutils.GetCurrentSystemMessage(transcript)
}

// AppendCustomEntry appends a custom entry (for extensions) as child of
// current leaf, then advances leaf. Returns the entry id (pi:
// appendCustomEntry).
func (m *SessionManager) AppendCustomEntry(customType string, data any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &CustomEntry{CustomType: customType, Data: data}
	entry.Type = "custom"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendSessionInfo appends a session info entry (e.g. display name).
// Returns the entry id (pi: appendSessionInfo).
func (m *SessionManager) AppendSessionInfo(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	crlf := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")
	sanitizedName := strings.TrimSpace(crlf.Replace(name))
	entry := &SessionInfoEntry{Name: &sanitizedName}
	entry.Type = "session_info"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// GetSessionName returns the current session name from the latest
// session_info entry, if any (pi: getSessionName). Empty names explicitly
// clear the session title.
func (m *SessionManager) GetSessionName() (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Walk entries in reverse to find the latest session_info entry.
	entries := m.getEntriesLocked()
	for i := len(entries) - 1; i >= 0; i-- {
		if entry, ok := entries[i].(*SessionInfoEntry); ok {
			if entry.Name != nil && strings.TrimSpace(*entry.Name) != "" {
				return strings.TrimSpace(*entry.Name), true
			}
			return "", false
		}
	}
	return "", false
}

// AppendCustomMessageEntry appends a custom message entry (for extensions)
// that participates in LLM context. customType is the extension identifier
// for filtering on reload; content is a string or text/image parts; display
// controls TUI rendering; details is optional extension-specific metadata
// (not sent to LLM). Returns the entry id (pi: appendCustomMessageEntry).
func (m *SessionManager) AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := &CustomMessageEntry{
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	entry.Type = "custom_message"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// AppendContextEdit appends a branch-local edit to an earlier
// model-visible entry. Returns the entry id (pi: appendContextEdit; throws
// → error return).
func (m *SessionManager) AppendContextEdit(targetID string, replacement *ContextEditReplacement) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if replacement != nil {
		switch replacement.Content.(type) {
		case string, []types.Content:
		default:
			return "", fmt.Errorf("Context edit replacement must be null or contain string/array content")
		}
	}
	target, ok := m.byID[targetID]
	if !ok {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	onBranch := false
	for _, entry := range m.getBranchLocked("") {
		if entry.Base().ID == targetID {
			onBranch = true
			break
		}
	}
	if !onBranch {
		return "", fmt.Errorf("Entry %s is not on the active branch", targetID)
	}
	editable := false
	targetRole := "custom"
	if messageEntry, ok := target.(*SessionMessageEntry); ok {
		switch messageEntry.Message.MessageRole() {
		case types.RoleUser, types.RoleAssistant, types.RoleToolResult:
			editable = true
			targetRole = string(messageEntry.Message.MessageRole())
		}
	} else if _, ok := target.(*CustomMessageEntry); ok {
		editable = true
	}
	if !editable {
		return "", fmt.Errorf("Entry %s does not contribute editable model content", targetID)
	}
	normalized := replacement
	if replacement != nil && (targetRole == string(types.RoleAssistant) || targetRole == string(types.RoleToolResult)) {
		if text, ok := replacement.Content.(string); ok {
			normalized = &ContextEditReplacement{Content: []types.Content{types.NewTextContent(text)}}
		}
	}
	entry := &ContextEditEntry{
		TargetID:    targetID,
		Replacement: normalized,
	}
	entry.Type = "context_edit"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// generateIDLocked produces an id unique within the current index (pi:
// generateId(this.byId)).
func (m *SessionManager) generateIDLocked() string {
	return generateID(func(id string) bool {
		_, taken := m.byID[id]
		return taken
	})
}

// GetLeafID returns the current leaf entry id; nil = null (pi: getLeafId).
func (m *SessionManager) GetLeafID() *string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leafID
}

// GetLeafEntry returns the current leaf entry, or nil (pi: getLeafEntry).
func (m *SessionManager) GetLeafEntry() SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leafID == nil {
		return nil
	}
	return m.byID[*m.leafID]
}

// GetEntry returns the entry with the given id, or nil (pi: getEntry).
func (m *SessionManager) GetEntry(id string) SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[id]
}

// GetChildren returns all direct children of an entry (pi: getChildren).
func (m *SessionManager) GetChildren(parentID string) []SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var children []SessionEntry
	for _, entry := range m.fileEntries {
		se, ok := entry.(SessionEntry)
		if !ok {
			continue
		}
		if se.Base().ParentID != nil && *se.Base().ParentID == parentID {
			children = append(children, se)
		}
	}
	return children
}

// GetLabel returns the label for an entry, if any (pi: getLabel).
func (m *SessionManager) GetLabel(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	label, ok := m.labelsByID[id]
	return label, ok
}

// AppendLabelChange sets or clears a label on an entry. Labels are
// user-defined markers for bookmarking/navigation. Pass nil or an empty
// string to clear the label. Returns the entry id (pi: appendLabelChange;
// throws for unknown targets → error return).
func (m *SessionManager) AppendLabelChange(targetID string, label *string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[targetID]; !ok {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	entry := &LabelEntry{TargetID: targetID, Label: label}
	entry.Type = "label"
	entry.ID = m.generateIDLocked()
	entry.ParentID = m.leafID
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	if label != nil && *label != "" {
		m.labelsByID[targetID] = *label
		m.labelTsByID[targetID] = entry.Base().Timestamp
	} else {
		delete(m.labelsByID, targetID)
		delete(m.labelTsByID, targetID)
	}
	return entry.ID, nil
}

// GetBranch walks from entry to root, returning all entries in path order.
// Includes all entry types (messages, compaction, model changes, etc.).
// fromID "" = the current leaf. Use BuildSessionContext to get the resolved
// messages for the LLM (pi: getBranch).
func (m *SessionManager) GetBranch(fromID string) []SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getBranchLocked(fromID)
}

func (m *SessionManager) getBranchLocked(fromID string) []SessionEntry {
	var path []SessionEntry
	startID := fromID
	if startID == "" && m.leafID != nil {
		startID = *m.leafID
	}
	var current SessionEntry
	if startID != "" {
		current = m.byID[startID]
	}
	for current != nil {
		path = append(path, current)
		if parentID := current.Base().ParentID; parentID != nil {
			current = m.byID[*parentID]
		} else {
			current = nil
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// BuildContextEntries builds the active, compaction-aware entry list for
// context/rendering, using tree traversal from current leaf (pi:
// buildContextEntries). A null leaf (before any entries) yields an empty
// list.
func (m *SessionManager) BuildContextEntries() []SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leafID == nil {
		// pi passes leafId: null → buildSessionPath returns [].
		return []SessionEntry{}
	}
	return BuildContextEntries(m.getEntriesLocked(), m.leafID, m.byID)
}

// BuildSessionProjection builds the provenance-preserving, compaction-aware
// model context (what gets sent to the LLM), using tree traversal from
// current leaf (pi: buildSessionProjection). A null leaf yields the default
// projection.
func (m *SessionManager) BuildSessionProjection() SessionProjection {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leafID == nil {
		// pi passes leafId: null → empty path → default settings.
		return SessionProjection{Entries: []ProjectedSessionEntry{}, Messages: []agent.AgentMessage{}, ThinkingLevel: "off"}
	}
	return BuildSessionProjection(m.getEntriesLocked(), m.leafID, m.byID)
}

// BuildSessionContext builds the finalized model context from the canonical
// session projection, using tree traversal from current leaf (pi:
// buildSessionContext). A null leaf yields the default context.
func (m *SessionManager) BuildSessionContext() SessionContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leafID == nil {
		// pi passes leafId: null → empty path → default settings.
		return SessionContext{Messages: []agent.AgentMessage{}, ThinkingLevel: "off"}
	}
	projection := BuildSessionProjection(m.getEntriesLocked(), m.leafID, m.byID)
	return SessionContext{Messages: projection.Messages, ThinkingLevel: projection.ThinkingLevel, Model: projection.Model}
}

// GetHeader returns the session header, or nil (pi: getHeader).
func (m *SessionManager) GetHeader() *SessionHeader {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.fileEntries {
		if h, ok := entry.(*SessionHeader); ok {
			return h
		}
	}
	return nil
}

// GetEntries returns all session entries (excludes header) as a shallow
// copy. The session is append-only: use Append* to add entries and Branch
// to change the leaf pointer. Entries cannot be modified or deleted (pi:
// getEntries).
func (m *SessionManager) GetEntries() []SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getEntriesLocked()
}

func (m *SessionManager) getEntriesLocked() []SessionEntry {
	out := make([]SessionEntry, 0, len(m.fileEntries))
	for _, entry := range m.fileEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		if se, ok := entry.(SessionEntry); ok {
			out = append(out, se)
		}
	}
	return out
}

// GetTree returns the session as a tree structure: a shallow defensive copy
// of all entries. A well-formed session has exactly one root (first entry
// with parentId === null). Orphaned entries (broken parent chain) are also
// returned as roots (pi: getTree).
func (m *SessionManager) GetTree() []*SessionTreeNode {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries := m.getEntriesLocked()
	nodeMap := make(map[string]*SessionTreeNode, len(entries))
	var roots []*SessionTreeNode

	// Create nodes with resolved labels.
	for _, entry := range entries {
		node := &SessionTreeNode{Entry: entry}
		if label, ok := m.labelsByID[entry.Base().ID]; ok {
			node.Label = lo.ToPtr(label)
			if ts, ok := m.labelTsByID[entry.Base().ID]; ok {
				node.LabelTimestamp = lo.ToPtr(ts)
			}
		}
		nodeMap[entry.Base().ID] = node
	}

	// Build tree.
	for _, entry := range entries {
		node := nodeMap[entry.Base().ID]
		parentID := entry.Base().ParentID
		if parentID == nil || *parentID == entry.Base().ID {
			roots = append(roots, node)
		} else if parent, ok := nodeMap[*parentID]; ok {
			parent.Children = append(parent.Children, node)
		} else {
			// Orphan - treat as root.
			roots = append(roots, node)
		}
	}

	// Sort children by timestamp (oldest first, newest at bottom). Use
	// iterative approach to avoid stack overflow on deep trees. Deviation:
	// unparseable timestamps sort as zero time (pi: NaN comparisons keep
	// order).
	var stack []*SessionTreeNode
	stack = append(stack, roots...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		children := node.Children
		sort.SliceStable(children, func(i, j int) bool {
			ti, iok := parseTimestamp(children[i].Entry.Base().Timestamp)
			tj, jok := parseTimestamp(children[j].Entry.Base().Timestamp)
			if !iok || !jok {
				return iok && !jok
			}
			return ti.Before(tj)
		})
		stack = append(stack, children...)
	}

	return roots
}

// Branch starts a new branch from an earlier entry. Moves the leaf pointer
// to the specified entry. The next Append* call will create a child of that
// entry, forming a new branch. Existing entries are not modified or deleted
// (pi: branch; throws for unknown targets → error return).
func (m *SessionManager) Branch(branchFromID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[branchFromID]; !ok {
		return fmt.Errorf("Entry %s not found", branchFromID)
	}
	leafID := branchFromID
	m.leafID = &leafID
	return nil
}

// ResetLeaf resets the leaf pointer to null (before any entries). The next
// Append* call will create a new root entry (parentId = null). Use this
// when navigating to re-edit the first user message (pi: resetLeaf).
func (m *SessionManager) ResetLeaf() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leafID = nil
}

// BranchWithSummary starts a new branch with a summary of the abandoned
// path. Same as Branch, but also appends a branch_summary entry that
// captures context from the abandoned conversation path. branchFromID ""
// = pi's null (new root). Returns the entry id (pi: branchWithSummary;
// throws for unknown targets → error return).
func (m *SessionManager) BranchWithSummary(branchFromID string, summary string, details any, fromHook *bool, usage *types.Usage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if branchFromID != "" {
		if _, ok := m.byID[branchFromID]; !ok {
			return "", fmt.Errorf("Entry %s not found", branchFromID)
		}
	}
	fromID := "root"
	if m.leafID != nil {
		fromID = *m.leafID
	}
	if branchFromID == "" {
		m.leafID = nil
	} else {
		leafID := branchFromID
		m.leafID = &leafID
	}
	entry := &BranchSummaryEntry{
		FromID:   fromID,
		Summary:  summary,
		Details:  details,
		Usage:    usage,
		FromHook: fromHook,
	}
	entry.Type = "branch_summary"
	entry.ID = m.generateIDLocked()
	entry.ParentID = nil
	if branchFromID != "" {
		entry.ParentID = &branchFromID
	}
	entry.Timestamp = sessionTimestamp()
	if err := m.appendEntryLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// cloneEntryWithParent shallow-copies an entry with a new parentId (pi:
// {...entry, parentId: pathParentId} — pi never mutates the original).
func cloneEntryWithParent(entry SessionEntry, parentID *string) SessionEntry {
	switch e := entry.(type) {
	case *SessionMessageEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *ThinkingLevelChangeEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *ModelChangeEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *CompactionEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *BranchSummaryEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *CustomEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *CustomMessageEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *ContextEditEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *LabelEntry:
		c := *e
		c.ParentID = parentID
		return &c
	case *SessionInfoEntry:
		c := *e
		c.ParentID = parentID
		return &c
	default:
		// rawFileEntry cannot occur in typed indexes (see package header).
		return entry
	}
}

// CreateBranchedSession creates a new session file containing only the path
// from root to the specified leaf. Useful for extracting a single
// conversation path from a branched session. Returns the new session file
// path, or "" when not persisting (pi: createBranchedSession; throws for
// unknown targets → error return).
func (m *SessionManager) CreateBranchedSession(leafID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	previousSessionFile := m.sessionFile
	path := m.getBranchLocked(leafID)
	if len(path) == 0 {
		return "", fmt.Errorf("Entry %s not found", leafID)
	}

	// Filter out LabelEntry from path - we'll recreate them from the
	// resolved map. Because labels are real tree entries, later entries can
	// be children of labels; removing labels requires re-chaining the
	// retained path to avoid orphaned subtrees. v0.85.1: if a compaction's
	// firstKeptEntryId pointed at a removed label, remap it to the first
	// real entry after that label.
	var pathWithoutLabels []SessionEntry
	replacementByLabelID := map[string]string{}
	var pendingLabelIDs []string
	var pathParentID *string
	for _, entry := range path {
		if label, ok := entry.(*LabelEntry); ok {
			pendingLabelIDs = append(pendingLabelIDs, label.Base().ID)
			continue
		}
		for _, labelID := range pendingLabelIDs {
			replacementByLabelID[labelID] = entry.Base().ID
		}
		pendingLabelIDs = nil
		if compaction, ok := entry.(*CompactionEntry); ok {
			clone := cloneEntryWithParent(entry, pathParentID).(*CompactionEntry)
			// pi: firstKeptEntryId: replacementByLabelId.get(entry.firstKeptEntryId) ?? entry.firstKeptEntryId.
			// pi v0.87.0: a retain-none compaction keeps its self-referencing
			// firstKeptEntryId even when a removed label precedes it.
			if compaction.FirstKeptEntryID != compaction.Base().ID {
				if replacement, remapped := replacementByLabelID[compaction.FirstKeptEntryID]; remapped {
					clone.FirstKeptEntryID = replacement
				}
			}
			pathWithoutLabels = append(pathWithoutLabels, clone)
		} else {
			pathWithoutLabels = append(pathWithoutLabels, cloneEntryWithParent(entry, pathParentID))
		}
		id := pathWithoutLabels[len(pathWithoutLabels)-1].Base().ID
		pathParentID = &id
	}

	newSessionID := createSessionID()
	timestamp := sessionTimestamp()
	fileTimestamp := sessionFileTimestamp(timestamp)
	newSessionFile := filepath.Join(m.GetSessionDir(), fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newSessionID))

	version := CURRENT_SESSION_VERSION
	header := &SessionHeader{
		Type:          "session",
		Version:       &version,
		ID:            newSessionID,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: nil,
	}
	if m.persist && previousSessionFile != "" {
		header.ParentSession = lo.ToPtr(previousSessionFile)
	}

	// Collect labels for entries in the path. Order: first appearance in
	// fileEntries (pi iterates the insertion-ordered labelsById Map; see
	// package header).
	pathEntryIDs := map[string]bool{}
	for _, entry := range pathWithoutLabels {
		pathEntryIDs[entry.Base().ID] = true
	}
	var orderedTargets []string
	seenTargets := map[string]bool{}
	for _, entry := range m.fileEntries {
		if labelEntry, ok := entry.(*LabelEntry); ok && !seenTargets[labelEntry.TargetID] {
			seenTargets[labelEntry.TargetID] = true
			orderedTargets = append(orderedTargets, labelEntry.TargetID)
		}
	}
	type labelRef struct {
		targetID  string
		label     string
		timestamp string
	}
	var labelsToWrite []labelRef
	for _, targetID := range orderedTargets {
		label, ok := m.labelsByID[targetID]
		if ok && pathEntryIDs[targetID] {
			labelsToWrite = append(labelsToWrite, labelRef{targetID: targetID, label: label, timestamp: m.labelTsByID[targetID]})
		}
	}

	if m.persist {
		// Build label entries.
		lastEntryID := ""
		if len(pathWithoutLabels) > 0 {
			lastEntryID = pathWithoutLabels[len(pathWithoutLabels)-1].Base().ID
		}
		parentID := lastEntryID
		var labelEntries []FileEntry
		for _, ref := range labelsToWrite {
			labelEntry := &LabelEntry{
				TargetID: ref.targetID,
				Label:    lo.ToPtr(ref.label),
			}
			labelEntry.Type = "label"
			labelEntry.ID = generateID(func(id string) bool { return pathEntryIDs[id] })
			pid := parentID
			if pid == "" {
				labelEntry.ParentID = nil
			} else {
				labelEntry.ParentID = &pid
			}
			labelEntry.Timestamp = ref.timestamp
			pathEntryIDs[labelEntry.ID] = true
			labelEntries = append(labelEntries, labelEntry)
			parentID = labelEntry.ID
		}

		m.fileEntries = []FileEntry{header}
		for _, entry := range pathWithoutLabels {
			m.fileEntries = append(m.fileEntries, entry)
		}
		for _, entry := range labelEntries {
			m.fileEntries = append(m.fileEntries, entry)
		}
		m.sessionID = newSessionID
		m.sessionFile = newSessionFile
		m.buildIndexLocked()

		// Only write the file now if it contains an assistant message.
		// Otherwise defer to persistLocked, which creates the file on the
		// first assistant response, matching the NewSession contract and
		// avoiding the duplicate-header bug when the no-assistant guard
		// later resets flushed to false.
		hasAssistant := false
		for _, e := range m.fileEntries {
			if me, ok := e.(*SessionMessageEntry); ok {
				if _, ok := me.Message.(types.AssistantMessage); ok {
					hasAssistant = true
					break
				}
			}
		}
		if hasAssistant {
			if err := m.rewriteFileLocked(); err != nil {
				return "", err
			}
			m.flushed = true
		} else {
			m.flushed = false
		}

		return newSessionFile, nil
	}

	// In-memory mode: replace current session with the path + labels.
	parentID := ""
	if len(pathWithoutLabels) > 0 {
		parentID = pathWithoutLabels[len(pathWithoutLabels)-1].Base().ID
	}
	var labelEntries []FileEntry
	for _, ref := range labelsToWrite {
		labelEntry := &LabelEntry{
			TargetID: ref.targetID,
			Label:    lo.ToPtr(ref.label),
		}
		labelEntry.Type = "label"
		labelEntry.ID = generateID(func(id string) bool { return pathEntryIDs[id] })
		if parentID == "" {
			labelEntry.ParentID = nil
		} else {
			labelEntry.ParentID = lo.ToPtr(parentID)
		}
		labelEntry.Timestamp = ref.timestamp
		pathEntryIDs[labelEntry.ID] = true
		labelEntries = append(labelEntries, labelEntry)
		parentID = labelEntry.ID
	}
	m.fileEntries = []FileEntry{header}
	for _, entry := range pathWithoutLabels {
		m.fileEntries = append(m.fileEntries, entry)
	}
	for _, entry := range labelEntries {
		m.fileEntries = append(m.fileEntries, entry)
	}
	m.sessionID = newSessionID
	m.buildIndexLocked()
	return "", nil
}

// CreateSessionManager creates a new persisted session (pi:
// SessionManager.create). sessionDir "" = the default per-cwd directory.
func CreateSessionManager(cwd string, sessionDir string, options *NewSessionOptions) (*SessionManager, error) {
	dir := sessionDir
	if dir != "" {
		dir = utils.NormalizePath(dir, nil)
	} else {
		var err error
		dir, err = GetDefaultSessionDir(cwd, "")
		if err != nil {
			return nil, err
		}
	}
	return newSessionManager(cwd, dir, "", true, options, nil)
}

// OpenSession opens a specific session file (pi: SessionManager.open).
// sessionDir "" = derive from the file's parent. cwdOverride nil = use the
// session header cwd.
func OpenSession(path string, sessionDir string, cwdOverride *string) (*SessionManager, error) {
	resolvedPath := utils.ResolvePath(path, "", nil)
	var header *SessionHeader
	var preloadedFileEntries []FileEntry
	if cwdOverride == nil && existsPath(resolvedPath) {
		var err error
		header, err = readSessionHeader(resolvedPath)
		if err != nil {
			var limitErr *SessionHeaderScanLimitError
			if !errorsAs(err, &limitErr) {
				return nil, err
			}
			// The bounded scan is only a discovery optimization. A full load
			// remains authoritative for legacy files with very large headers
			// or prefixes.
			preloadedFileEntries = LoadEntriesFromFile(resolvedPath)
			if len(preloadedFileEntries) > 0 {
				header, _ = preloadedFileEntries[0].(*SessionHeader)
			}
		}
	}
	cwd := ""
	if cwdOverride != nil {
		cwd = *cwdOverride
	} else if header != nil && header.Cwd != "" {
		// Deviation: pi falls back to process.cwd() only when header.cwd is
		// absent; an explicit empty header cwd also yields "" there. pig
		// cannot distinguish absent from empty after typed decode.
		cwd = header.Cwd
	} else {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	// If no sessionDir provided, derive from file's parent directory.
	dir := sessionDir
	if dir != "" {
		dir = utils.NormalizePath(dir, nil)
	} else {
		dir = filepath.Dir(resolvedPath)
	}
	return newSessionManager(cwd, dir, resolvedPath, true, nil, preloadedFileEntries)
}

// errorsAs wraps errors.As (kept local to mirror pi's instanceof check).
func errorsAs(err error, target **SessionHeaderScanLimitError) bool {
	return errors.As(err, target)
}

// ContinueRecentSession continues the most recent session, or creates new
// if none (pi: SessionManager.continueRecent). sessionDir "" = the default
// per-cwd directory.
func ContinueRecentSession(cwd string, sessionDir string) (*SessionManager, error) {
	dir := sessionDir
	if dir != "" {
		dir = utils.NormalizePath(dir, nil)
	} else {
		var err error
		dir, err = GetDefaultSessionDir(cwd, "")
		if err != nil {
			return nil, err
		}
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, codingagent.GetAgentDir())
	filterCwdArg := ""
	if filterCwd {
		filterCwdArg = cwd
	}
	if mostRecent := FindMostRecentSession(dir, filterCwdArg); mostRecent != nil {
		return newSessionManager(cwd, dir, *mostRecent, true, nil, nil)
	}
	return newSessionManager(cwd, dir, "", true, nil, nil)
}

// InMemorySession creates an in-memory session (no file persistence),
// optionally from entries held outside the filesystem (pi:
// SessionManager.inMemory). cwd "" = the process working directory.
func InMemorySession(cwd string, options *NewSessionOptions, entries ...[]FileEntry) (*SessionManager, error) {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	var preloaded []FileEntry
	if len(entries) > 0 {
		preloaded = entries[0]
	}
	return newSessionManager(cwd, "", "", false, options, preloaded)
}

// ForkSession forks a session from another project directory into the
// current project: creates a new session in the target cwd with the full
// history from the source session (pi: SessionManager.forkFrom).
// sessionDir "" = the default for targetCwd.
func ForkSession(sourcePath string, targetCwd string, sessionDir string, options *NewSessionOptions) (*SessionManager, error) {
	resolvedSourcePath := utils.ResolvePath(sourcePath, "", nil)
	resolvedTargetCwd := utils.ResolvePath(targetCwd, "", nil)
	sourceEntries := LoadEntriesFromFile(resolvedSourcePath)
	if len(sourceEntries) == 0 {
		return nil, fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", resolvedSourcePath)
	}

	var sourceHeader *SessionHeader
	for _, entry := range sourceEntries {
		if h, ok := entry.(*SessionHeader); ok {
			sourceHeader = h
			break
		}
	}
	if sourceHeader == nil {
		return nil, fmt.Errorf("Cannot fork: source session has no header: %s", resolvedSourcePath)
	}

	dir := sessionDir
	if dir != "" {
		dir = utils.NormalizePath(dir, nil)
	} else {
		var err error
		dir, err = GetDefaultSessionDir(resolvedTargetCwd, "")
		if err != nil {
			return nil, err
		}
	}
	if !existsPath(dir) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	// Create new session file with new ID but forked content.
	if options != nil && options.ID != nil {
		if err := AssertValidSessionID(*options.ID); err != nil {
			return nil, err
		}
	}
	newSessionID := ""
	if options != nil && options.ID != nil {
		newSessionID = *options.ID
	} else {
		newSessionID = createSessionID()
	}
	timestamp := sessionTimestamp()
	fileTimestamp := sessionFileTimestamp(timestamp)
	newSessionFile := filepath.Join(dir, fmt.Sprintf("%s_%s.jsonl", fileTimestamp, newSessionID))

	// Write new header pointing to source as parent, with updated cwd.
	version := CURRENT_SESSION_VERSION
	newHeader := &SessionHeader{
		Type:          "session",
		Version:       &version,
		ID:            newSessionID,
		Timestamp:     timestamp,
		Cwd:           resolvedTargetCwd,
		ParentSession: lo.ToPtr(resolvedSourcePath),
	}
	f, err := os.OpenFile(newSessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	if err := writeJSONLine(f, newHeader); err != nil {
		f.Close()
		return nil, err
	}

	// Copy all non-header entries from source.
	for _, entry := range sourceEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		if err := writeJSONLine(f, entry); err != nil {
			f.Close()
			return nil, err
		}
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	return newSessionManager(resolvedTargetCwd, dir, newSessionFile, true, nil, nil)
}

// messageWithContent reports whether the message carries role and content
// (pi: isMessageWithContent; typed messages always do, untyped wrappers
// never re-type).
func messageWithContent(message agent.AgentMessage) bool {
	switch message.(type) {
	case types.UserMessage, types.AssistantMessage, types.ToolResultMessage:
		return true
	}
	return false
}

// extractTextContent concatenates the text parts of a message (pi:
// extractTextContent; string contents join with " " between blocks).
func extractTextContent(message agent.AgentMessage) string {
	var parts []string
	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.Parts == nil {
			return m.Content.String
		}
		for _, block := range m.Content.Parts {
			if text, ok := block.(types.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
	case types.AssistantMessage:
		for _, block := range m.Content {
			if text, ok := block.(types.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
	}
	return strings.Join(parts, " ")
}

// getMessageActivityTime returns the activity timestamp of a message entry
// (pi: getMessageActivityTime). Only user/assistant messages carry one;
// ok=false when unavailable.
func getMessageActivityTime(entry *SessionMessageEntry) (int64, bool) {
	message := entry.Message
	if !messageWithContent(message) {
		return 0, false
	}
	var role types.Role
	var timestamp int64
	switch m := message.(type) {
	case types.UserMessage:
		role, timestamp = m.Role, m.Timestamp
	case types.AssistantMessage:
		role, timestamp = m.Role, m.Timestamp
	default:
		return 0, false
	}
	if role != types.RoleUser && role != types.RoleAssistant {
		return 0, false
	}
	// Deviation: pi only trusts a numeric message timestamp and falls back
	// to the entry timestamp otherwise; pig's typed messages always carry
	// the field, so 0 falls back too.
	if timestamp != 0 {
		return timestamp, true
	}
	t, ok := parseTimestamp(entry.Base().Timestamp)
	if !ok {
		return 0, false
	}
	return t.UnixMilli(), true
}

// buildSessionInfo loads one session file into a SessionInfo; nil on any
// failure (pi: buildSessionInfo). fileStats carries a pre-read stat from
// discovery (pi: fileStats); ctx cancels the scan (pi: signal).
func buildSessionInfo(ctx context.Context, filePath string, fileStats os.FileInfo) *SessionInfo {
	var stats os.FileInfo
	if fileStats != nil {
		stats = fileStats
	} else {
		var err error
		stats, err = os.Stat(filePath)
		if err != nil {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var header *SessionHeader
	messageCount := 0
	firstMessage := ""
	var allMessages []string
	var name *string
	var lastActivityTime int64
	hasActivity := false

	reader := newSessionLineReader(f, SESSION_READ_BUFFER_SIZE)
	for {
		line, ok := reader.next()
		if !ok {
			break
		}
		entry := decodeFileEntryLine(line)
		if entry == nil {
			continue
		}

		if header == nil {
			h, ok := entry.(*SessionHeader)
			if !ok {
				return nil
			}
			header = h
			continue
		}

		// Extract session name (use latest, including explicit clears).
		if infoEntry, ok := entry.(*SessionInfoEntry); ok {
			if infoEntry.Name != nil && strings.TrimSpace(*infoEntry.Name) != "" {
				name = lo.ToPtr(strings.TrimSpace(*infoEntry.Name))
			} else {
				name = nil
			}
		}

		msgEntry, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		messageCount++

		if activityTime, ok := getMessageActivityTime(msgEntry); ok {
			if !hasActivity || activityTime > lastActivityTime {
				lastActivityTime = activityTime
				hasActivity = true
			}
		}

		message := msgEntry.Message
		if !messageWithContent(message) {
			continue
		}
		role := message.MessageRole()
		if role != types.RoleUser && role != types.RoleAssistant {
			continue
		}

		textContent := extractTextContent(message)
		if textContent == "" {
			continue
		}

		allMessages = append(allMessages, textContent)
		if firstMessage == "" && role == types.RoleUser {
			firstMessage = textContent
		}
	}

	if header == nil {
		return nil
	}

	// pi: signal?.throwIfAborted() in the catch — a cancelled scan reports
	// nothing.
	if err := ctx.Err(); err != nil {
		return nil
	}

	cwd := header.Cwd
	var parentSessionPath *string
	if header.ParentSession != nil {
		parentSessionPath = lo.ToPtr(*header.ParentSession)
	}
	created, createdOK := parseTimestamp(header.Timestamp)
	modified := stats.ModTime()
	if hasActivity && lastActivityTime > 0 {
		modified = time.UnixMilli(lastActivityTime)
	} else if createdOK {
		modified = created
	}
	first := firstMessage
	if first == "" {
		first = "(no messages)"
	}

	return &SessionInfo{
		Path:              filePath,
		ID:                header.ID,
		Cwd:               cwd,
		Name:              name,
		ParentSessionPath: parentSessionPath,
		Created:           created,
		Modified:          modified,
		MessageCount:      messageCount,
		FirstMessage:      first,
		AllMessagesText:   strings.Join(allMessages, " "),
	}
}

// SessionListProgress reports (loaded, total) during session listing (pi:
// SessionListProgress; v0.86.0 adds partialSessions — sessions loaded so
// far sorted by activity, present on periodic updates, nil otherwise).
// pig: invoked from worker goroutines, serialized behind a mutex; must not
// re-enter the SessionManager.
type SessionListProgress func(loaded int, total int, partialSessions []SessionInfo)

const (
	MAX_CONCURRENT_SESSION_INFO_LOADS      = 10
	MAX_CONCURRENT_SESSION_DISCOVERY_LOADS = 64
	CURRENT_SESSION_LIST_PUBLISH_INTERVAL  = 10
	ALL_SESSION_LIST_PUBLISH_INTERVAL      = 100
)

// sessionFileCandidate is one discovery candidate with its pre-read stat
// (pi: SessionFileCandidate).
type sessionFileCandidate struct {
	path  string
	stats os.FileInfo
}

// mapWithConcurrency maps items with a bounded worker pool preserving
// result order, aborting on ctx cancellation (pi: mapWithConcurrency).
func mapWithConcurrency[T any, R any](items []T, limit int, mapFn func(item T, index int) R, ctx context.Context) []R {
	results := make([]R, len(items))
	var nextIndex atomic.Int64
	var wg sync.WaitGroup
	workers := limit
	if workers > len(items) {
		workers = len(items)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if ctx.Err() != nil {
					return
				}
				index := int(nextIndex.Add(1)) - 1
				if index >= len(items) {
					return
				}
				results[index] = mapFn(items[index], index)
			}
		}()
	}
	wg.Wait()
	return results
}

// sortSessionInfos sorts sessions by modified time, newest first (pi:
// sortSessionInfos).
func sortSessionInfos(sessions []SessionInfo) []SessionInfo {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].Modified.After(sessions[j].Modified)
	})
	return sessions
}

// buildSessionInfosWithConcurrency loads session infos for files with a
// bounded worker pool, preserving result order (pi:
// buildSessionInfosWithConcurrency).
func buildSessionInfosWithConcurrency(files []sessionFileCandidate, onLoaded func(info *SessionInfo, index int), ctx context.Context) []*SessionInfo {
	return mapWithConcurrency(files, MAX_CONCURRENT_SESSION_INFO_LOADS, func(file sessionFileCandidate, index int) *SessionInfo {
		info := buildSessionInfo(ctx, file.path, file.stats)
		onLoaded(info, index)
		return info
	}, ctx)
}

// listSessionsFromDir lists session infos from one directory (pi:
// listSessionsFromDir; v0.86.0 reworks progress publishing and abort).
func listSessionsFromDir(ctx context.Context, dir string, onProgress SessionListProgress) []SessionInfo {
	if err := ctx.Err(); err != nil {
		return nil
	}
	if !existsPath(dir) {
		return nil
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		// Return empty list on error.
		return nil
	}
	var names []string
	for _, de := range dirEntries {
		if strings.HasSuffix(de.Name(), ".jsonl") {
			names = append(names, de.Name())
		}
	}
	// pi: files sorted descending by name so ties in mtime resolve
	// deterministically newest-first.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	files := make([]sessionFileCandidate, 0, len(names))
	for _, name := range names {
		files = append(files, sessionFileCandidate{path: filepath.Join(dir, name)})
	}
	total := len(files)

	var partialSessions []SessionInfo
	var progressMu sync.Mutex
	loaded := 0
	results := buildSessionInfosWithConcurrency(files, func(info *SessionInfo, index int) {
		progressMu.Lock()
		defer progressMu.Unlock()
		loaded++
		if info != nil {
			partialSessions = append(partialSessions, *info)
		}
		if onProgress != nil {
			publishPartial := loaded == 1 || loaded%CURRENT_SESSION_LIST_PUBLISH_INTERVAL == 0 || loaded == total
			var partial []SessionInfo
			if publishPartial {
				partial = sortSessionInfos(append([]SessionInfo{}, partialSessions...))
			}
			onProgress(loaded, total, partial)
		}
	}, ctx)
	sessions := make([]SessionInfo, 0, len(results))
	for _, info := range results {
		if info != nil {
			sessions = append(sessions, *info)
		}
	}
	return sessions
}

// FindSessionById finds an exact session ID without loading transcript
// bodies. sessionDir "" = the default per-cwd directory (pi:
// SessionManager.findById; v0.86.0). Returns "" when not found.
func FindSessionById(cwd string, id string, sessionDir string) string {
	var dir string
	if sessionDir != "" {
		dir = utils.NormalizePath(sessionDir, nil)
	} else {
		var err error
		dir, err = GetDefaultSessionDir(cwd, "")
		if err != nil {
			return ""
		}
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, codingagent.GetAgentDir())
	resolvedCwd := utils.ResolvePath(cwd, "", nil)

	files, err := os.ReadDir(dir)
	if err != nil {
		// Exact session discovery is best-effort, matching ListSessions.
		return ""
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		header := readSessionHeaderForDiscovery(path)
		if header == nil || header.ID != id {
			continue
		}
		if filterCwd && !sessionCwdMatches(header.Cwd, resolvedCwd) {
			continue
		}
		return path
	}
	return ""
}

// ListSessions lists all sessions for a directory, most recently modified
// first (pi: SessionManager.list; v0.86.0 adds partialSessions progress and
// ctx abort). sessionDir "" = the default per-cwd directory; onProgress may
// be nil.
func ListSessions(ctx context.Context, cwd string, sessionDir string, onProgress SessionListProgress) []SessionInfo {
	dir := sessionDir
	if dir != "" {
		dir = utils.NormalizePath(dir, nil)
	} else {
		var err error
		dir, err = GetDefaultSessionDir(cwd, "")
		if err != nil {
			return nil
		}
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, codingagent.GetAgentDir())
	resolvedCwd := utils.ResolvePath(cwd, "", nil)
	includeSession := func(session SessionInfo) bool {
		return !filterCwd || sessionCwdMatches(session.Cwd, resolvedCwd)
	}
	var progress SessionListProgress
	if onProgress != nil {
		progress = func(loaded, total int, partialSessions []SessionInfo) {
			var filtered []SessionInfo
			for _, session := range partialSessions {
				if includeSession(session) {
					filtered = append(filtered, session)
				}
			}
			onProgress(loaded, total, filtered)
		}
	}
	sessions := listSessionsFromDir(ctx, dir, progress)
	filtered := make([]SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		if includeSession(session) {
			filtered = append(filtered, session)
		}
	}
	return sortSessionInfos(filtered)
}

// ListAllSessions lists all sessions across all project directories, most
// recently modified first (pi: SessionManager.listAll; v0.86.0 reworks
// discovery ordering, partial progress, and ctx abort). sessionDir "" = the
// agent sessions root; onProgress may be nil.
func ListAllSessions(ctx context.Context, sessionDir string, onProgress SessionListProgress) []SessionInfo {
	customSessionDir := ""
	if sessionDir != "" {
		customSessionDir = utils.NormalizePath(sessionDir, nil)
	}
	if customSessionDir != "" {
		return sortSessionInfos(listSessionsFromDir(ctx, customSessionDir, onProgress))
	}

	sessionsDir := codingagent.GetSessionsDir()
	if err := ctx.Err(); err != nil {
		return nil
	}
	if !existsPath(sessionsDir) {
		return nil
	}
	dirEntries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, de := range dirEntries {
		// pi includes directories and symlinks (readdir withFileTypes).
		if de.IsDir() || de.Type()&os.ModeSymlink != 0 {
			dirs = append(dirs, filepath.Join(sessionsDir, de.Name()))
		}
	}

	// pi: directory listing and stat probing run through
	// mapWithConcurrency at discovery concurrency, and candidates are
	// sorted by mtime descending (name ascending on ties) before the
	// bounded info load.
	dirFileLists := mapWithConcurrency(dirs, MAX_CONCURRENT_SESSION_DISCOVERY_LOADS, func(dir string, _ int) []string {
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var names []string
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".jsonl") {
				names = append(names, filepath.Join(dir, file.Name()))
			}
		}
		return names
	}, ctx)
	var allFiles []string
	for _, files := range dirFileLists {
		allFiles = append(allFiles, files...)
	}
	candidates := mapWithConcurrency(allFiles, MAX_CONCURRENT_SESSION_DISCOVERY_LOADS, func(path string, _ int) sessionFileCandidate {
		info, err := os.Stat(path)
		if err != nil {
			return sessionFileCandidate{path: path}
		}
		return sessionFileCandidate{path: path, stats: info}
	}, ctx)
	sort.SliceStable(candidates, func(i, j int) bool {
		// pi: (b.stats?.mtimeMs ?? -Infinity) - (a.stats?.mtimeMs ?? -Infinity)
		// — descending mtime, missing stats last.
		iKey, jKey := int64(-1)<<62, int64(-1)<<62
		if candidates[i].stats != nil {
			iKey = candidates[i].stats.ModTime().UnixMilli()
		}
		if candidates[j].stats != nil {
			jKey = candidates[j].stats.ModTime().UnixMilli()
		}
		if iKey != jKey {
			return iKey > jKey
		}
		// pi: basename(b.path).localeCompare(basename(a.path)) — descending name.
		return filepath.Base(candidates[i].path) > filepath.Base(candidates[j].path)
	})

	totalFiles := len(candidates)
	var partialSessions []SessionInfo
	var progressMu sync.Mutex
	loaded := 0
	firstCandidateLoaded := false
	results := buildSessionInfosWithConcurrency(candidates, func(info *SessionInfo, index int) {
		progressMu.Lock()
		defer progressMu.Unlock()
		loaded++
		if index == 0 {
			firstCandidateLoaded = true
		}
		if info != nil {
			partialSessions = append(partialSessions, *info)
		}
		if onProgress != nil {
			publishPartial := firstCandidateLoaded && (index == 0 || loaded%ALL_SESSION_LIST_PUBLISH_INTERVAL == 0 || loaded == totalFiles)
			var partial []SessionInfo
			if publishPartial {
				partial = sortSessionInfos(append([]SessionInfo{}, partialSessions...))
			}
			onProgress(loaded, totalFiles, partial)
		}
	}, ctx)
	var sessions []SessionInfo
	for _, info := range results {
		if info != nil {
			sessions = append(sessions, *info)
		}
	}
	return sortSessionInfos(sessions)
}
