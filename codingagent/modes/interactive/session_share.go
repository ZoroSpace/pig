// pi: src/modes/interactive/session-share.ts — the trailing `pi.share` entry
// carrying the system prompt and tool schemas for the session viewer (pi
// v0.86.0 createShareTrailingEntries refactor). Deviation: the share command
// half (shareSession — Radius/gist upload with loader + HTML export) stays
// deferred with pig's other network-delivery commands; pig's /share is in the
// deferred-command list in interactive_mode.go.
package interactive

import (
	"github.com/ZoroSpace/pig/ai"
	core "github.com/ZoroSpace/pig/codingagent/core"
)

// createShareTrailingEntries builds the trailing pi.share entry (pi:
// createShareTrailingEntries). parentID is nil when the branch is empty; the
// entry maps 1:1 onto pi's object literal (camelCase wire keys).
func createShareTrailingEntries(session *core.AgentSession, parentID *string, timestamp string) []any {
	state := session.Agent.State()
	tools := make([]any, 0, len(state.Tools))
	for _, tool := range state.Tools {
		tools = append(tools, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Parameters,
		})
	}
	var id any = parentID // nil → JSON null
	if parentID != nil {
		id = *parentID
	}
	return []any{
		map[string]any{
			"type":       "custom",
			"customType": "pi.share",
			"id":         ai.UUIDv7()[:8],
			"parentId":   id,
			"timestamp":  timestamp,
			"data": map[string]any{
				"systemPrompt": state.SystemPrompt,
				"tools":        tools,
			},
		},
	}
}

// exportSessionForShare exports the current branch with presentation
// metadata for Radius (pi: exportSessionForShare).
func exportSessionForShare(filePath string, session *core.AgentSession) (string, error) {
	return core.ExportSessionToJsonl(session.SessionManager, filePath, func(parentID *string, timestamp string) []any {
		return createShareTrailingEntries(session, parentID, timestamp)
	})
}
