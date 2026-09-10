// pi: harness/session/context.ts — session context assembly (v0.85.1: the
// model/thinking/tools state extraction moved to lane configuration values;
// only message projection remains here).
//
// Deviations:
//   - pi's `timestamp: string | number` parameters are int64 ms here (see
//     harness/messages.go note).
//   - pi's async projector contract (EntryProjector returning
//     Promise<AgentMessage[] | undefined>) collapses to a synchronous
//     callback per §3.4; buildSessionContext stays synchronous.
package session

import (
	"context"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// SessionContextBuildOptions parameterizes context assembly (pi:
// SessionContextBuildOptions).
type SessionContextBuildOptions struct {
	EntryProjectors map[string]EntryProjector
}

// BuildContextEntries keeps everything after the last compaction entry,
// replacing the pre-compaction history with the compaction entry itself (pi:
// buildContextEntries).
func BuildContextEntries(pathEntries []Entry) []Entry {
	var compaction *CompactionEntry
	compactionIndex := -1
	for index := len(pathEntries) - 1; index >= 0; index-- {
		if entry, ok := pathEntries[index].(*CompactionEntry); ok {
			compaction = entry
			compactionIndex = index
			break
		}
	}
	if compaction == nil {
		return append([]Entry{}, pathEntries...)
	}
	result := []Entry{compaction}
	result = append(result, pathEntries[compactionIndex+1:]...)
	return result
}

// isContextMessage filters messages that do not belong in the model context
// (pi: isContextMessage).
func isContextMessage(message agent.AgentMessage) bool {
	assistant, ok := assistantMessageOf(message)
	if !ok {
		return true
	}
	return assistant.StopReason != types.StopReasonError &&
		assistant.StopReason != types.StopReasonAborted &&
		assistant.StopReason != types.StopReasonDeferred
}

// SessionEntryToContextMessages projects one entry into context messages
// (pi: sessionEntryToContextMessages).
func SessionEntryToContextMessages(entry Entry) []agent.AgentMessage {
	switch e := entry.(type) {
	case *MessageEntry:
		if isContextMessage(e.Message) {
			return []agent.AgentMessage{e.Message}
		}
		return nil
	case *CompactionEntry:
		messages := []agent.AgentMessage{
			harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp),
		}
		for _, message := range e.RetainedTail {
			if isContextMessage(message) {
				messages = append(messages, message)
			}
		}
		return messages
	case *BranchSummaryEntry:
		if e.Summary != "" {
			return []agent.AgentMessage{
				harness.CreateBranchSummaryMessage(e.Summary, e.FromID, e.Timestamp),
			}
		}
		return nil
	case *CustomEntry:
		return nil
	default:
		return nil
	}
}

// fromIDString dereferences an optional fromId; nil becomes the zero string
// (see harness/messages.go: BranchSummaryMessage.FromID is a plain string).
func fromIDString(fromID *string) string {
	if fromID == nil {
		return ""
	}
	return *fromID
}

// BuildSessionContext assembles the LLM context for one branch path (pi:
// buildSessionContext).
func BuildSessionContext(pathEntries []Entry, options *SessionContextBuildOptions, ctx context.Context) ([]agent.AgentMessage, error) {
	if options == nil {
		options = &SessionContextBuildOptions{}
	}
	entries := BuildContextEntries(pathEntries)
	messages := []agent.AgentMessage{}
	for _, entry := range entries {
		custom, ok := entry.(*CustomEntry)
		if !ok {
			messages = append(messages, SessionEntryToContextMessages(entry)...)
			continue
		}
		if projector, ok := options.EntryProjectors[custom.Base().CustomTypeOrValue()]; ok {
			projected, err := projector(*custom, ctx)
			if err != nil {
				return nil, err
			}
			messages = append(messages, projected...)
		}
	}
	return messages, nil
}
