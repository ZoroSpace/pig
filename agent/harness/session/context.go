// pi: src/harness/session/context.ts — session context assembly: derive the
// LLM-facing message list plus live state (model/thinking/tools) from a
// branch path.
//
// Deviations:
//   - TS async-free helpers keep their signatures; pi's
//     `timestamp: string | number` parameters are int64 ms here (see
//     harness/messages.go note).
//   - SessionContextModel is the anonymous `{ provider, modelId }` shape.
//   - Entry transforms/projectors operate on []session.Entry (pi:
//     readonly Entry[]).
package session

import (
	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// SessionContextModel names the provider/model active when the context was
// built (pi: `{ provider, modelId }`).
type SessionContextModel struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// SessionContext is the assembled LLM context for one branch path (pi:
// SessionContext). ActiveToolNames is nil when no active_tools_change entry
// narrowed the toolset.
type SessionContext struct {
	Messages        []agent.AgentMessage `json:"messages"`
	ThinkingLevel   string               `json:"thinkingLevel"`
	Model           *SessionContextModel `json:"model"`
	ActiveToolNames []string             `json:"activeToolNames"`
}

// ContextEntryTransform rewrites the context entry list before projection
// (pi: ContextEntryTransform).
type ContextEntryTransform = func(entries []Entry) []Entry

// CustomEntryContextMessageProjector projects one custom entry into context
// messages (pi: CustomEntryContextMessageProjector). Returning nil drops the
// entry.
type CustomEntryContextMessageProjector = func(entry *CustomEntry, index int, entries []Entry) []agent.AgentMessage

// SessionContextBuildOptions parameterizes context assembly (pi:
// SessionContextBuildOptions).
type SessionContextBuildOptions struct {
	EntryTransforms []ContextEntryTransform
	EntryProjectors map[string]CustomEntryContextMessageProjector
}

// deriveSessionContextState walks the path for the latest model/thinking/
// tool state (pi: deriveSessionContextState).
func deriveSessionContextState(pathEntries []Entry) (thinkingLevel string, model *SessionContextModel, activeToolNames []string) {
	thinkingLevel = "off"
	for _, entry := range pathEntries {
		switch e := entry.(type) {
		case *ThinkingLevelEntry:
			thinkingLevel = e.ThinkingLevel
		case *ModelChangeEntry:
			model = &SessionContextModel{Provider: e.Provider, ModelID: e.ModelID}
		case *MessageEntry:
			if assistant, ok := e.Message.(types.AssistantMessage); ok {
				model = &SessionContextModel{Provider: string(assistant.Provider), ModelID: assistant.Model}
			}
		case *ActiveToolsEntry:
			activeToolNames = append([]string{}, e.ActiveToolNames...)
		}
	}
	return thinkingLevel, model, activeToolNames
}

// DefaultContextEntryTransform replaces the pre-compaction history with the
// compaction summary entry itself (pi: defaultContextEntryTransform).
func DefaultContextEntryTransform(pathEntries []Entry) []Entry {
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

// BuildContextEntries applies the default transform plus caller transforms
// (pi: buildContextEntries).
func BuildContextEntries(pathEntries []Entry, options *SessionContextBuildOptions) []Entry {
	entries := DefaultContextEntryTransform(pathEntries)
	if options != nil {
		for _, transform := range options.EntryTransforms {
			entries = transform(entries)
		}
	}
	return entries
}

// SessionEntryToContextMessages projects one entry into context messages
// (pi: sessionEntryToContextMessages).
func SessionEntryToContextMessages(entry Entry, index int, entries []Entry, options *SessionContextBuildOptions) []agent.AgentMessage {
	switch e := entry.(type) {
	case *MessageEntry:
		if assistant, ok := e.Message.(types.AssistantMessage); ok {
			if assistant.StopReason == types.StopReasonDeferred {
				return nil
			}
		}
		return []agent.AgentMessage{e.Message}
	case *CompactionEntry:
		messages := []agent.AgentMessage{
			harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp),
		}
		messages = append(messages, e.RetainedTail...)
		return messages
	case *BranchSummaryEntry:
		if e.Summary != "" {
			return []agent.AgentMessage{
				harness.CreateBranchSummaryMessage(e.Summary, e.FromID, e.Timestamp),
			}
		}
		return nil
	case *CustomEntry:
		if options != nil {
			if projector, ok := options.EntryProjectors[e.CustomType]; ok {
				return projector(e, index, entries)
			}
		}
		return nil
	default:
		return nil
	}
}

// BuildSessionContext assembles the LLM context for one branch path (pi:
// buildSessionContext).
func BuildSessionContext(pathEntries []Entry, options *SessionContextBuildOptions) SessionContext {
	thinkingLevel, model, activeToolNames := deriveSessionContextState(pathEntries)
	contextEntries := BuildContextEntries(pathEntries, options)
	messages := []agent.AgentMessage{}
	for index, entry := range contextEntries {
		messages = append(messages, SessionEntryToContextMessages(entry, index, contextEntries, options)...)
	}
	return SessionContext{
		Messages:        messages,
		ThinkingLevel:   thinkingLevel,
		Model:           model,
		ActiveToolNames: activeToolNames,
	}
}
