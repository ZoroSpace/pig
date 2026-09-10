// pi: harness/session/jsonl/legacy-v3.ts — normalization of legacy v3 JSONL
// sessions into format-4 storage (retained-entry reminting, discarded-entry
// ancestor resolution, value reconstruction, usage aggregation).
//
// Deviations:
//   - The 10 legacy record types collapse into one LegacyV3Entry struct with
//     members-only fields (pi's TS interfaces are compile-time only).
//   - importedCustomMessage's `as unknown as AgentMessage` cast maps to
//     pig's harness.CustomMessage, which already rides the AgentMessage
//     channel.
//   - createBranchSummaryMessage's fromId: string | null maps to the plain
//     string field (harness/messages.go note); "root" sentinels resolve to
//     "" here.
package jsonl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// Legacy v3 entry type vocabulary (pi: RetainedLegacyV3Entry |
// DiscardedLegacyV3Entry).
const (
	legacyTypeMessage             = "message"
	legacyTypeCustom              = "custom"
	legacyTypeCustomMessage       = "custom_message"
	legacyTypeBranchSummary       = "branch_summary"
	legacyTypeCompaction          = "compaction"
	legacyTypeModelChange         = "model_change"
	legacyTypeThinkingLevelChange = "thinking_level_change"
	legacyTypeActiveToolsChange   = "active_tools_change"
	legacyTypeSessionInfo         = "session_info"
	legacyTypeLabel               = "label"
)

// LegacyV3Entry is one legacy v3 record (union collapse; see file header).
type LegacyV3Entry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"` // ISO string in legacy v3

	// message:
	Message agent.AgentMessage `json:"-"`

	// custom:
	CustomType string    `json:"customType,omitempty"`
	Data       JsonValue `json:"data,omitempty"`
	// LegacyDetails carries branch_summary/compaction `details`.
	LegacyDetails JsonValue `json:"legacyDetails,omitempty"`

	// custom_message:
	Content any  `json:"content,omitempty"`
	Display bool `json:"display,omitempty"`

	// branch_summary / compaction:
	Summary          string       `json:"summary,omitempty"`
	FromID           string       `json:"fromId,omitempty"`
	Usage            *types.Usage `json:"usage,omitempty"`
	FromHook         *bool        `json:"fromHook,omitempty"`
	FirstKeptEntryID string       `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int64        `json:"tokensBefore,omitempty"`

	// model_change:
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// thinking_level_change:
	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	// active_tools_change:
	ActiveToolNames []string `json:"activeToolNames,omitempty"`

	// session_info:
	Name *string `json:"name,omitempty"`

	// label:
	TargetID string  `json:"targetId,omitempty"`
	Label    *string `json:"label,omitempty"`
}

// JsonValue is the recursive JSON union alias (session.JsonValue).
type JsonValue = session.JsonValue

// NormalizedLegacyV3Records is the migration result (pi:
// NormalizedLegacyV3Records).
type NormalizedLegacyV3Records struct {
	Writes        []session.CommittedWrite
	ImportedUsage types.Usage
	NextSeq       int64
}

// parseLegacyV3Entry decodes and validates one legacy record (pi:
// parseLegacyV3Entry).
func parseLegacyV3Entry(line string, lineNumber int) (*LegacyV3Entry, error) {
	var entry LegacyV3Entry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return nil, fmt.Errorf("Invalid legacy v3 JSONL record at line %d: not valid JSON", lineNumber)
	}
	switch entry.Type {
	case legacyTypeMessage:
		// Decode the agent message eagerly (interface dispatch).
		var aux struct {
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &aux); err != nil {
			return nil, err
		}
		message, err := types.DecodeMessage(aux.Message)
		if err != nil {
			return nil, fmt.Errorf("Invalid legacy v3 JSONL record at line %d: %w", lineNumber, err)
		}
		entry.Message = message
	case legacyTypeCustom, legacyTypeCustomMessage, legacyTypeBranchSummary,
		legacyTypeCompaction, legacyTypeModelChange, legacyTypeThinkingLevelChange,
		legacyTypeActiveToolsChange, legacyTypeSessionInfo, legacyTypeLabel:
	default:
		return nil, fmt.Errorf("Unsupported legacy v3 record type at line %d: %s", lineNumber, entry.Type)
	}
	return &entry, nil
}

// importedCustomMessage converts a legacy custom_message entry into pig's
// custom message (pi: importedCustomMessage).
func importedCustomMessage(entry *LegacyV3Entry) agent.AgentMessage {
	timestamp := int64(0)
	if parsed, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
		timestamp = parsed.UnixMilli()
	} else if parsed, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
		timestamp = parsed.UnixMilli()
	}
	return harness.CustomMessage{
		Role:       harness.RoleCustomMessage,
		CustomType: entry.CustomType,
		Content:    entry.Content,
		Details:    entry.LegacyDetails,
		Display:    entry.Display,
		Timestamp:  timestamp,
	}
}

// isRetainedEntry reports whether the entry survives migration (pi:
// isRetainedEntry).
func isRetainedEntry(entry *LegacyV3Entry) bool {
	return entry.Type != legacyTypeModelChange &&
		entry.Type != legacyTypeThinkingLevelChange &&
		entry.Type != legacyTypeActiveToolsChange &&
		entry.Type != legacyTypeSessionInfo &&
		entry.Type != legacyTypeLabel
}

// legacyTimestampMs parses a legacy ISO timestamp to Unix ms.
func legacyTimestampMs(timestamp string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, timestamp)
		if err != nil {
			return 0
		}
	}
	return parsed.UnixMilli()
}

// retainedIDResolver resolves legacy nodes to their reminted ID, or to the
// nearest retained ancestor when discarded (pi: RetainedIdResolver).
type retainedIDResolver struct {
	resolvedIDs map[string]*string
	entriesByID map[string]*LegacyV3Entry
	remintedIDs map[string]string
}

func newRetainedIDResolver(entriesByID map[string]*LegacyV3Entry, remintedIDs map[string]string) *retainedIDResolver {
	return &retainedIDResolver{
		resolvedIDs: map[string]*string{},
		entriesByID: entriesByID,
		remintedIDs: remintedIDs,
	}
}

// Resolve resolves one legacy parent reference (pi: resolve).
func (r *retainedIDResolver) resolve(legacyID *string) *string {
	if legacyID == nil {
		return nil
	}
	var resolvedID *string
	var traversedIDs []string
	visitedIDs := map[string]struct{}{}
	currentID := legacyID

	for currentID != nil {
		if reminted, ok := r.remintedIDs[*currentID]; ok {
			resolvedID = &reminted
			break
		}
		if cached, ok := r.resolvedIDs[*currentID]; ok {
			resolvedID = cached
			break
		}
		if _, seen := visitedIDs[*currentID]; seen {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Cycle in legacy v3 parent chain at entry: %s", *currentID)})
		}
		visitedIDs[*currentID] = struct{}{}
		entry, ok := r.entriesByID[*currentID]
		if !ok {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Missing legacy v3 entry reference: %s", *currentID)})
		}
		traversedIDs = append(traversedIDs, *currentID)
		currentID = entry.ParentID
	}

	for _, traversedID := range traversedIDs {
		r.resolvedIDs[traversedID] = resolvedID
	}
	return resolvedID
}

// LegacyV3Error is a legacy-record normalization failure (pi: plain Error).
type LegacyV3Error struct{ Message string }

func (e *LegacyV3Error) Error() string { return e.Message }

// requireRetainedID resolves a reference that must have a retained ancestor
// (pi: requireRetainedId).
func requireRetainedID(resolver *retainedIDResolver, legacyID string) string {
	importedID := resolver.resolve(&legacyID)
	if importedID == nil {
		panic(&LegacyV3Error{Message: fmt.Sprintf("Legacy v3 entry reference has no retained ancestor: %s", legacyID)})
	}
	return *importedID
}

// resolveBranchSummaryFromID resolves a branch_summary fromId (pi:
// resolveBranchSummaryFromId); legacy branchWithSummary() encoded a root
// source as the "root" sentinel.
func resolveBranchSummaryFromID(resolver *retainedIDResolver, legacyFromID string) *string {
	if legacyFromID == "root" {
		return nil
	}
	return resolver.resolve(&legacyFromID)
}

// projectContextMessages projects one legacy entry into context messages
// (pi: projectContextMessages).
func projectContextMessages(entry *LegacyV3Entry, resolver *retainedIDResolver) []agent.AgentMessage {
	switch entry.Type {
	case legacyTypeMessage:
		return []agent.AgentMessage{entry.Message}
	case legacyTypeCustomMessage:
		return []agent.AgentMessage{importedCustomMessage(entry)}
	case legacyTypeBranchSummary:
		if entry.Summary != "" {
			return []agent.AgentMessage{
				harness.CreateBranchSummaryMessage(
					entry.Summary,
					resolveBranchSummaryFromID(resolver, entry.FromID),
					legacyTimestampMs(entry.Timestamp),
				),
			}
		}
		return nil
	case legacyTypeCompaction:
		return []agent.AgentMessage{
			harness.CreateCompactionSummaryMessage(entry.Summary, entry.TokensBefore, legacyTimestampMs(entry.Timestamp)),
		}
	default:
		// custom, model_change, thinking_level_change, active_tools_change,
		// session_info, label
		return nil
	}
}

// materializeRetainedTail walks a compaction's parent branch down to
// firstKeptEntryId and projects the tail into context messages (pi:
// materializeRetainedTail).
func materializeRetainedTail(
	compaction *LegacyV3Entry,
	entriesByID map[string]*LegacyV3Entry,
	resolver *retainedIDResolver,
) []agent.AgentMessage {
	var reversedTail []*LegacyV3Entry
	visited := map[string]struct{}{}
	currentID := compaction.ParentID
	for currentID != nil {
		if _, seen := visited[*currentID]; seen {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Cycle in legacy v3 parent chain at entry: %s", *currentID)})
		}
		visited[*currentID] = struct{}{}
		entry, ok := entriesByID[*currentID]
		if !ok {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Missing legacy v3 parent entry: %s", *currentID)})
		}
		reversedTail = append(reversedTail, entry)
		if *currentID == compaction.FirstKeptEntryID {
			// Reverse then project (pi: reversedTail.reverse().flatMap).
			var tail []agent.AgentMessage
			for i := len(reversedTail) - 1; i >= 0; i-- {
				tail = append(tail, projectContextMessages(reversedTail[i], resolver)...)
			}
			return tail
		}
		currentID = entry.ParentID
	}
	panic(&LegacyV3Error{Message: fmt.Sprintf(
		"Legacy v3 compaction %s firstKeptEntryId is not on its parent branch: %s",
		compaction.ID, compaction.FirstKeptEntryID)})
}

// normalizeRetainedEntry converts one retained legacy entry into a committed
// entry write (pi: normalizeRetainedEntry).
func normalizeRetainedEntry(
	entry *LegacyV3Entry,
	seq int64,
	entriesByID map[string]*LegacyV3Entry,
	resolver *retainedIDResolver,
) session.CommittedEntryWrite {
	timestamp := legacyTimestampMs(entry.Timestamp)
	parentID := resolver.resolve(entry.ParentID)
	id := requireRetainedID(resolver, entry.ID)
	switch entry.Type {
	case legacyTypeMessage:
		return session.CommittedEntryWrite{Entry: &session.MessageEntry{
			EntryBase: session.EntryBase{ID: id, ParentID: parentID, Seq: seq, Timestamp: timestamp, Type: session.EntryTypeMessage},
			Message:   entry.Message,
		}}
	case legacyTypeCustomMessage:
		return session.CommittedEntryWrite{Entry: &session.MessageEntry{
			EntryBase: session.EntryBase{ID: id, ParentID: parentID, Seq: seq, Timestamp: timestamp, Type: session.EntryTypeMessage},
			Message:   importedCustomMessage(entry),
		}}
	case legacyTypeBranchSummary:
		return session.CommittedEntryWrite{Entry: &session.BranchSummaryEntry{
			EntryBase: session.EntryBase{ID: id, ParentID: parentID, Seq: seq, Timestamp: timestamp, Type: session.EntryTypeBranchSummary},
			FromID:    resolveBranchSummaryFromID(resolver, entry.FromID),
			Summary:   entry.Summary,
			Details:   entry.LegacyDetails,
			Usage:     entry.Usage,
			FromHook:  entry.FromHook != nil && *entry.FromHook,
		}}
	case legacyTypeCompaction:
		return session.CommittedEntryWrite{Entry: &session.CompactionEntry{
			EntryBase:    session.EntryBase{ID: id, ParentID: parentID, Seq: seq, Timestamp: timestamp, Type: session.EntryTypeCompaction},
			Summary:      entry.Summary,
			RetainedTail: materializeRetainedTail(entry, entriesByID, resolver),
			TokensBefore: entry.TokensBefore,
			Details:      entry.LegacyDetails,
			Usage:        entry.Usage,
			FromHook:     entry.FromHook != nil && *entry.FromHook,
		}}
	default:
		// custom
		return session.CommittedEntryWrite{Entry: &session.CustomEntry{
			EntryBase: session.EntryBase{
				ID: id, ParentID: parentID, Seq: seq, Timestamp: timestamp,
				Type: session.EntryTypeCustom, CustomType: &entry.CustomType,
			},
			Data: entry.Data,
		}}
	}
}

// legacyThinkingLevels is the valid thinking-level vocabulary (pi:
// THINKING_LEVELS).
var legacyThinkingLevels = map[string]struct{}{
	"off": {}, "minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

// selectedConfiguration walks the final branch reconstructing the lane
// configuration from legacy change entries (pi: selectedConfiguration).
func selectedConfiguration(entriesByID map[string]*LegacyV3Entry, selectedID *string) *session.LaneConfiguration {
	var modelProvider, modelID string
	var sawModel bool
	var thinkingLevel string
	var sawThinkingLevel bool
	var sawValidThinkingLevel bool
	var activeToolNames []string
	var sawActiveToolNames bool
	visited := map[string]struct{}{}
	currentID := selectedID
	for currentID != nil && (!sawModel || !sawThinkingLevel || !sawActiveToolNames) {
		if _, seen := visited[*currentID]; seen {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Cycle in legacy v3 parent chain at entry: %s", *currentID)})
		}
		visited[*currentID] = struct{}{}
		entry, ok := entriesByID[*currentID]
		if !ok {
			panic(&LegacyV3Error{Message: fmt.Sprintf("Missing legacy v3 entry reference: %s", *currentID)})
		}
		switch {
		case entry.Type == legacyTypeModelChange && !sawModel:
			sawModel = true
			if entry.Provider != "" && entry.ModelID != "" {
				modelProvider = entry.Provider
				modelID = entry.ModelID
			}
		case entry.Type == legacyTypeThinkingLevelChange && !sawThinkingLevel:
			sawThinkingLevel = true
			if _, valid := legacyThinkingLevels[entry.ThinkingLevel]; valid {
				thinkingLevel = entry.ThinkingLevel
				sawValidThinkingLevel = true
			}
		case entry.Type == legacyTypeActiveToolsChange && !sawActiveToolNames:
			sawActiveToolNames = true
			activeToolNames = append([]string{}, entry.ActiveToolNames...)
		}
		if entry.ParentID == nil {
			break
		}
		currentID = entry.ParentID
	}
	if !sawModel || !sawValidThinkingLevel || modelProvider == "" || modelID == "" {
		return nil
	}
	configuration := &session.LaneConfiguration{
		ThinkingLevel:   thinkingLevel,
		ActiveToolNames: activeToolNames,
	}
	configuration.Model.Provider = modelProvider
	configuration.Model.ModelID = modelID
	return configuration
}

// normalizeLegacyV3Values reconstructs the value set from legacy entries
// (pi: normalizeLegacyV3Values).
func normalizeLegacyV3Values(
	entries []*LegacyV3Entry,
	entriesByID map[string]*LegacyV3Entry,
	resolver *retainedIDResolver,
	firstSeq int64,
) []session.CommittedValueSetWrite {
	var writes []session.CommittedValueSetWrite
	add := func(address session.Value, value any) {
		writes = append(writes, session.CommittedValueSetWrite{
			Kind:      "value",
			Op:        "set",
			Seq:       firstSeq + int64(len(writes)),
			Namespace: address.Namespace,
			Key:       address.Key,
			Value:     value,
		})
	}

	var latestSessionInfo *LegacyV3Entry
	for _, entry := range entries {
		if entry.Type == legacyTypeSessionInfo {
			latestSessionInfo = entry
		}
	}
	if latestSessionInfo != nil && latestSessionInfo.Name != nil && *latestSessionInfo.Name != "" {
		add(session.SessionNameAddress, *latestSessionInfo.Name)
	}

	labels := map[string]string{}
	labelOrder := []string{}
	for _, entry := range entries {
		if entry.Type != legacyTypeLabel {
			continue
		}
		targetID := resolver.resolve(&entry.TargetID)
		// Labels have no current address when their target has no retained
		// ancestor.
		if targetID == nil {
			continue
		}
		// Legacy v3 treated both undefined and the empty string as clearing
		// a label.
		if entry.Label != nil && *entry.Label != "" {
			if _, exists := labels[*targetID]; !exists {
				labelOrder = append(labelOrder, *targetID)
			}
			labels[*targetID] = *entry.Label
		} else {
			delete(labels, *targetID)
		}
	}
	for _, targetID := range labelOrder {
		if label, exists := labels[targetID]; exists {
			address := session.EntryLabel(targetID)
			add(address, label)
		}
	}

	tipAddress := session.BranchTip("main")
	var finalTip any
	if len(entries) > 0 {
		finalID := resolver.resolve(&entries[len(entries)-1].ID)
		if finalID != nil {
			finalTip = *finalID
		}
	}
	add(tipAddress, finalTip)
	var finalEntryID *string
	if len(entries) > 0 {
		finalEntryID = &entries[len(entries)-1].ID
	}
	if configuration := selectedConfiguration(entriesByID, finalEntryID); configuration != nil {
		add(session.LaneConfig("main"), configuration)
		add(session.LaneStateAddress("main"), session.LaneState{Inbox: []session.InboxItem{}})
	}
	return writes
}

// aggregateImportedUsage sums usage across legacy entries (pi:
// aggregateImportedUsage).
func aggregateImportedUsage(entries []*LegacyV3Entry) types.Usage {
	aggregate := utils.EmptyUsage()
	for _, entry := range entries {
		var usage *types.Usage
		switch entry.Type {
		case legacyTypeMessage:
			if assistant, ok := entry.Message.(types.AssistantMessage); ok {
				usage = &assistant.Usage
			} else if assistant, ok := entry.Message.(*types.AssistantMessage); ok {
				usage = &assistant.Usage
			} else if toolResult, ok := entry.Message.(types.ToolResultMessage); ok {
				usage = toolResult.Usage
			} else if toolResult, ok := entry.Message.(*types.ToolResultMessage); ok {
				usage = toolResult.Usage
			}
		case legacyTypeCompaction, legacyTypeBranchSummary:
			usage = entry.Usage
		}
		if usage != nil {
			aggregate = utils.AddUsage(aggregate, *usage)
		}
	}
	return aggregate
}

// NormalizeLegacyV3Records normalizes the currently supported v3 records
// without touching their source file (pi: normalizeLegacyV3Records).
func NormalizeLegacyV3Records(recordLines []string) (records NormalizedLegacyV3Records, err error) {
	defer recoverLegacyV3Panics(&err)
	entries := make([]*LegacyV3Entry, 0, len(recordLines))
	for index, line := range recordLines {
		entry, err := parseLegacyV3Entry(line, index+2)
		if err != nil {
			return NormalizedLegacyV3Records{}, err
		}
		entries = append(entries, entry)
	}
	entriesByID := map[string]*LegacyV3Entry{}
	for _, entry := range entries {
		if _, exists := entriesByID[entry.ID]; exists {
			return NormalizedLegacyV3Records{}, &LegacyV3Error{Message: fmt.Sprintf("Duplicate legacy v3 entry id: %s", entry.ID)}
		}
		entriesByID[entry.ID] = entry
	}
	var retainedEntries []*LegacyV3Entry
	for _, entry := range entries {
		if isRetainedEntry(entry) {
			retainedEntries = append(retainedEntries, entry)
		}
	}
	remintedIDs := map[string]string{}
	for _, entry := range retainedEntries {
		remintedIDs[entry.ID] = aiutils.UUIDv7WithTime(legacyTimestampMs(entry.Timestamp))
	}
	resolver := newRetainedIDResolver(entriesByID, remintedIDs)

	var writes []session.CommittedWrite
	for index, entry := range retainedEntries {
		writes = append(writes, normalizeRetainedEntry(entry, int64(index+1), entriesByID, resolver))
	}
	valueWrites := normalizeLegacyV3Values(entries, entriesByID, resolver, int64(len(retainedEntries)+1))
	for _, valueWrite := range valueWrites {
		writes = append(writes, valueWrite)
	}
	return NormalizedLegacyV3Records{
		Writes:        writes,
		ImportedUsage: aggregateImportedUsage(entries),
		NextSeq:       int64(len(writes) + 1),
	}, nil
}

// recoverLegacyV3Panics converts resolver panics (pi: thrown Errors) into
// error returns.
func recoverLegacyV3Panics(err *error) {
	if r := recover(); r != nil {
		if legacyErr, ok := r.(*LegacyV3Error); ok {
			*err = legacyErr
			return
		}
		panic(r)
	}
}

// MetadataFromLegacyV3Header resolves the legacy header into metadata (pi:
// metadataFromLegacyV3Header).
func MetadataFromLegacyV3Header(fileSystem harness.FileSystem, header *LegacyV3SessionHeader, ctx context.Context) (session.SessionMetadata, error) {
	metadata := session.SessionMetadata{
		ID:             header.ID,
		CreatedAt:      legacyTimestampMs(header.Timestamp),
		StorageVersion: JSONLStorageVersion,
	}
	cwd := header.Cwd
	metadata.Cwd = &cwd
	if header.ParentSession != nil {
		parentSessionID, err := resolveLegacyV3ParentSessionID(fileSystem, *header.ParentSession, ctx)
		if err != nil {
			return session.SessionMetadata{}, err
		}
		if parentSessionID != nil {
			metadata.ParentSessionID = parentSessionID
		} else {
			metadata.LegacyParentSessionPath = header.ParentSession
		}
	}
	return metadata, nil
}

// resolveLegacyV3ParentSessionID reads one header line of the parent file
// and extracts its id (pi: resolveLegacyV3ParentSessionId).
func resolveLegacyV3ParentSessionID(fileSystem harness.FileSystem, parentSessionPath string, ctx context.Context) (*string, error) {
	lines, err := fileSystem.ReadTextLines(ctx, parentSessionPath, lo.ToPtr(1))
	if err != nil {
		return nil, nil // pi: !lines.ok → undefined
	}
	if len(lines) == 0 {
		return nil, nil
	}
	parsed, err := ParseJsonlSessionHeader(lines[0])
	if err != nil {
		return nil, nil
	}
	if parsed.Format == "v4" {
		return &parsed.V4.ID, nil
	}
	return &parsed.V3Legacy.ID, nil
}

// NormalizeLegacyV3Header builds the v4 target header from a legacy header
// (pi: normalizeLegacyV3Header).
func NormalizeLegacyV3Header(fileSystem harness.FileSystem, header *LegacyV3SessionHeader, ctx context.Context) (JsonlStorageHeader, error) {
	metadata, err := MetadataFromLegacyV3Header(fileSystem, header, ctx)
	if err != nil {
		return JsonlStorageHeader{}, err
	}
	return JsonlStorageHeader{
		V:                       JSONLFormatVersion,
		Kind:                    "header",
		ID:                      metadata.ID,
		StorageVersion:          metadata.StorageVersion,
		CreatedAt:               metadata.CreatedAt,
		Cwd:                     metadataCwd(metadata),
		ParentSessionID:         metadata.ParentSessionID,
		LegacyParentSessionPath: metadata.LegacyParentSessionPath,
	}, nil
}

func metadataCwd(metadata session.SessionMetadata) string {
	if metadata.Cwd == nil {
		return ""
	}
	return *metadata.Cwd
}
