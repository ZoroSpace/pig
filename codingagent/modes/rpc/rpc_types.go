// pi: modes/rpc/rpc-types.ts — RPC protocol types for headless operation.
//
// Commands are sent as JSON lines on stdin; responses and events are emitted
// as JSON lines on stdout. The wire shape (camelCase keys, string literals)
// is a hard constraint (AGENTS.md 线格式): command `type` values, response
// `command` values, and field names must match pi exactly.
//
// Go adaptation (declared): pi models RpcCommand/RpcResponse as discriminated
// unions. Go collapses each union into one struct carrying every variant's
// members plus a `Type` (and, for responses, `Command`) discriminator — the
// same treatment as AgentSessionEvent. Inbound decoding ignores members that
// do not belong to the active variant; outbound responses are built by the
// success/error helpers in rpc_mode.go so only the active variant's keys
// marshal (via omitempty).
package rpc

import (
	"encoding/json"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// RpcCommand is a command received on stdin (pi: RpcCommand union). All
// variant members are optional except Type.
type RpcCommand struct {
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`

	// Prompting
	Message           string               `json:"message,omitempty"`
	Images            []types.ImageContent `json:"images,omitempty"`
	StreamingBehavior string               `json:"streamingBehavior,omitempty"`

	// new_session
	ParentSession string `json:"parentSession,omitempty"`

	// set_model
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// set_thinking_level
	Level types.ModelThinkingLevel `json:"level,omitempty"`

	// set_steering_mode / set_follow_up_mode
	Mode string `json:"mode,omitempty"`

	// compact
	CustomInstructions string `json:"customInstructions,omitempty"`

	// set_auto_compaction / set_auto_retry
	Enabled *bool `json:"enabled,omitempty"`

	// bash
	Command            string `json:"command,omitempty"`
	ExcludeFromContext *bool  `json:"excludeFromContext,omitempty"`

	// export_html
	OutputPath string `json:"outputPath,omitempty"`

	// switch_session
	SessionPath string `json:"sessionPath,omitempty"`

	// fork
	EntryID string `json:"entryId,omitempty"`

	// get_entries
	Since string `json:"since,omitempty"`

	// set_session_name
	Name string `json:"name,omitempty"`
}

// RpcSlashCommand is a command available for invocation via prompt (pi:
// RpcSlashCommand).
type RpcSlashCommand struct {
	// Name is the command name (without a leading slash).
	Name string `json:"name"`
	// Description is the human-readable description; "" = omitted.
	Description string `json:"description,omitempty"`
	// Source is "extension" | "prompt" | "skill".
	Source string `json:"source"`
	// SourceInfo is the source metadata for the owning resource.
	SourceInfo utils.SourceInfo `json:"sourceInfo"`
}

// RpcSessionState is the get_state response payload (pi: RpcSessionState).
type RpcSessionState struct {
	// Model is the current model; nil = omitted (pi: model?).
	Model *types.Model `json:"model,omitempty"`
	// ThinkingLevel is the current reasoning level.
	ThinkingLevel types.ModelThinkingLevel `json:"thinkingLevel"`
	// IsStreaming mirrors session.isStreaming.
	IsStreaming bool `json:"isStreaming"`
	// IsCompacting mirrors session.isCompacting.
	IsCompacting bool `json:"isCompacting"`
	// SteeringMode is "all" | "one-at-a-time".
	SteeringMode string `json:"steeringMode"`
	// FollowUpMode is "all" | "one-at-a-time".
	FollowUpMode string `json:"followUpMode"`
	// SessionFile is the session file path; "" = omitted (pi: sessionFile?).
	SessionFile string `json:"sessionFile,omitempty"`
	// SessionID is the session identifier.
	SessionID string `json:"sessionId"`
	// SessionName is the display name; "" = omitted (pi: sessionName?).
	SessionName string `json:"sessionName,omitempty"`
	// AutoCompactionEnabled reports whether auto-compaction is on.
	AutoCompactionEnabled bool `json:"autoCompactionEnabled"`
	// MessageCount is the transcript length.
	MessageCount int `json:"messageCount"`
	// PendingMessageCount is the number of queued messages.
	PendingMessageCount int `json:"pendingMessageCount"`
}

// RpcResponse is a response emitted on stdout (pi: RpcResponse union). The
// success/error helpers populate ID/Type/Command/Success plus either Data or
// Error.
type RpcResponse struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	// Data is the variant payload; nil = key omitted. A null payload (pi's
	// `data: null`) is carried as json.RawMessage("null") so the key is
	// emitted.
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// ============================================================================
// Extension UI (declared, not produced — Phase 7 is not being ported and the
// user has confirmed pig extensions are incompatible with pi's, so this
// channel has no consumer. The types are kept for wire parity and so the
// input loop can recognize and ignore extension_ui_response lines.)
// ============================================================================

// RpcExtensionUIRequest is a request to the host for user input (pi:
// RpcExtensionUIRequest). Never emitted by pig.
type RpcExtensionUIRequest struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Method string `json:"method"`
}

// RpcExtensionUIResponse is the host's answer (pi: RpcExtensionUIResponse).
// Recognized on stdin and discarded.
type RpcExtensionUIResponse struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Value     string `json:"value,omitempty"`
	Confirmed bool   `json:"confirmed,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

// marshalNull is the sentinel used to emit an explicit JSON null payload.
var marshalNull = json.RawMessage("null")

// Response payload shapes (pi: the data members of RpcResponse's success
// variants).

// rpcDispositionData is the prompt/steer/follow_up payload (pi:
// { disposition }).
type rpcDispositionData struct {
	Disposition core.PromptDisposition `json:"disposition"`
}

// rpcClearQueueData is the clear_queue payload (pi: { steering, followUp }).
type rpcClearQueueData struct {
	Steering []string `json:"steering"`
	FollowUp []string `json:"followUp"`
}

// rpcNewSessionData is the new_session payload (pi: { cancelled }).
type rpcNewSessionData struct {
	Cancelled bool `json:"cancelled"`
}

// rpcCycleModelData is the cycle_model payload (pi:
// { model, thinkingLevel, isScoped }).
type rpcCycleModelData struct {
	Model         *types.Model             `json:"model"`
	ThinkingLevel types.ModelThinkingLevel `json:"thinkingLevel"`
	IsScoped      bool                     `json:"isScoped"`
}

// rpcModelsData is the get_available_models payload (pi: { models }).
type rpcModelsData struct {
	Models []types.Model `json:"models"`
}

// rpcLevelData is the cycle_thinking_level payload (pi: { level }).
type rpcLevelData struct {
	Level types.ModelThinkingLevel `json:"level"`
}

// rpcLevelsData is the get_available_thinking_levels payload (pi: { levels }).
type rpcLevelsData struct {
	Levels []types.ModelThinkingLevel `json:"levels"`
}

// rpcExportHTMLData is the export_html payload (pi: { path }).
type rpcExportHTMLData struct {
	Path string `json:"path"`
}

// rpcForkData is the fork payload (pi: { text, cancelled }).
type rpcForkData struct {
	Text      string `json:"text"`
	Cancelled bool   `json:"cancelled"`
}

// rpcForkMessage is one forkable user message (pi: getUserMessagesForForking
// item { entryId, text }).
type rpcForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// rpcForkMessagesData is the get_fork_messages payload (pi: { messages }).
type rpcForkMessagesData struct {
	Messages []rpcForkMessage `json:"messages"`
}

// rpcEntriesData is the get_entries payload (pi:
// { entries, leafId: string | null }).
type rpcEntriesData struct {
	Entries []core.SessionEntry `json:"entries"`
	LeafID  *string             `json:"leafId"`
}

// rpcTreeData is the get_tree payload (pi: { tree, leafId: string | null }).
type rpcTreeData struct {
	Tree   []*core.SessionTreeNode `json:"tree"`
	LeafID *string                 `json:"leafId"`
}

// rpcTextData is the get_last_assistant_text payload (pi:
// { text: string | null }).
type rpcTextData struct {
	Text *string `json:"text"`
}

// rpcMessagesData is the get_messages payload (pi: { messages }).
type rpcMessagesData struct {
	Messages []agent.AgentMessage `json:"messages"`
}

// rpcCommandsData is the get_commands payload (pi: { commands }).
type rpcCommandsData struct {
	Commands []RpcSlashCommand `json:"commands"`
}
