// pi: src/harness/messages.ts — harness-level custom agent messages
// (bashExecution / custom / branchSummary / compactionSummary) and the
// AgentMessage → LLM message conversion.
//
// Deviations:
//   - pi declares these types via CustomAgentMessages declaration merging
//     (interface augmentation of AgentMessage). pig's AgentMessage is the
//     open types.Message interface: any type implementing MessageRole()
//     rides the same channel, so the four message structs here implement
//     it directly — no extension-point counterpart is needed (see
//     agent/types.go header note).
//   - create*Message take int64 millisecond timestamps only; pi also
//     accepts ISO date strings (callers convert before calling).
//   - CustomMessage.Content holds a string or a []types.Content (pi:
//     `string | (TextContent | ImageContent)[]`); values decoded from
//     generic JSON arrive as map/slice values and are not re-typed here
//     (the typed path is createCustomMessage).
//   - convertToLlm filters via a type switch and nil skips (pi: map +
//     filter over `Message | undefined`).
package harness

import (
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// Role literals for the harness custom messages (pi: the `role` string
// members of the custom message union).
const (
	RoleBashExecution     types.Role = "bashExecution"
	RoleCustomMessage     types.Role = "custom"
	RoleBranchSummary     types.Role = "branchSummary"
	RoleCompactionSummary types.Role = "compactionSummary"
)

// COMPACTION_SUMMARY_PREFIX wraps a compaction summary in the LLM context
// (pi: COMPACTION_SUMMARY_PREFIX).
const COMPACTION_SUMMARY_PREFIX = `The conversation history before this point was compacted into the following summary:

<summary>
`

// COMPACTION_SUMMARY_SUFFIX closes the compaction summary block (pi:
// COMPACTION_SUMMARY_SUFFIX).
const COMPACTION_SUMMARY_SUFFIX = `
</summary>`

// BRANCH_SUMMARY_PREFIX wraps a branch summary in the LLM context (pi:
// BRANCH_SUMMARY_PREFIX).
const BRANCH_SUMMARY_PREFIX = `The following is a summary of a branch that this conversation came back from:

<summary>
`

// BRANCH_SUMMARY_SUFFIX closes the branch summary block (pi:
// BRANCH_SUMMARY_SUFFIX).
const BRANCH_SUMMARY_SUFFIX = `</summary>`

// BashExecutionMessage reports one shell execution to the UI and context
// (pi: BashExecutionMessage).
type BashExecutionMessage struct {
	Role      types.Role `json:"role"` // "bashExecution"
	Command   string     `json:"command"`
	Output    string     `json:"output"`
	ExitCode  *int       `json:"exitCode"`
	Cancelled bool       `json:"cancelled"`
	Truncated bool       `json:"truncated"`
	// FullOutputPath points at the spilled full output when truncated.
	FullOutputPath string `json:"fullOutputPath,omitempty"`
	Timestamp      int64  `json:"timestamp"`
	// ExcludeFromContext drops the message from LLM conversion entirely.
	ExcludeFromContext *bool `json:"excludeFromContext,omitempty"`
}

func (m BashExecutionMessage) MessageRole() types.Role { return m.Role }

// CustomMessage carries an application-defined UI/context message (pi:
// CustomMessage<T>; details loses its generic).
type CustomMessage struct {
	Role       types.Role `json:"role"` // "custom"
	CustomType string     `json:"customType"`
	// Content is a string or a []types.Content of text/image parts.
	Content   any   `json:"content"`
	Display   bool  `json:"display"`
	Details   any   `json:"details,omitempty"`
	Timestamp int64 `json:"timestamp"`
}

func (m CustomMessage) MessageRole() types.Role { return m.Role }

// BranchSummaryMessage reports a branch summarization result (pi:
// BranchSummaryMessage; v0.85.1: fromId is string | null).
type BranchSummaryMessage struct {
	Role    types.Role `json:"role"` // "branchSummary"
	Summary string     `json:"summary"`
	// FromID is the source entry id; nil = pi's null (summary of a session
	// boundary with no single source entry).
	FromID    *string `json:"fromId"`
	Timestamp int64   `json:"timestamp"`
}

func (m BranchSummaryMessage) MessageRole() types.Role { return m.Role }

// CompactionSummaryMessage reports a compaction result (pi:
// CompactionSummaryMessage).
type CompactionSummaryMessage struct {
	Role         types.Role `json:"role"` // "compactionSummary"
	Summary      string     `json:"summary"`
	TokensBefore int64      `json:"tokensBefore"`
	Timestamp    int64      `json:"timestamp"`
}

func (m CompactionSummaryMessage) MessageRole() types.Role { return m.Role }

// BashExecutionToText renders one bash execution for the LLM context (pi:
// bashExecutionToText).
func BashExecutionToText(msg BashExecutionMessage) string {
	text := fmt.Sprintf("Ran `%s`\n", msg.Command)
	if msg.Output != "" {
		text += "```\n" + msg.Output + "\n```"
	} else {
		text += "(no output)"
	}
	if msg.Cancelled {
		text += "\n\n(command cancelled)"
	} else if msg.ExitCode != nil && *msg.ExitCode != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", *msg.ExitCode)
	}
	if msg.Truncated && msg.FullOutputPath != "" {
		text += fmt.Sprintf("\n\n[Output truncated. Full output: %s]", msg.FullOutputPath)
	}
	return text
}

// CreateBranchSummaryMessage builds a branch summary message (pi:
// createBranchSummaryMessage; v0.85.1: fromId is string | null; nil = null).
func CreateBranchSummaryMessage(summary string, fromID *string, timestamp int64) BranchSummaryMessage {
	return BranchSummaryMessage{
		Role:      RoleBranchSummary,
		Summary:   summary,
		FromID:    fromID,
		Timestamp: timestamp,
	}
}

// CreateCompactionSummaryMessage builds a compaction summary message (pi:
// createCompactionSummaryMessage).
func CreateCompactionSummaryMessage(summary string, tokensBefore int64, timestamp int64) CompactionSummaryMessage {
	return CompactionSummaryMessage{
		Role:         RoleCompactionSummary,
		Summary:      summary,
		TokensBefore: tokensBefore,
		Timestamp:    timestamp,
	}
}

// CreateCustomMessage builds a custom message (pi: createCustomMessage).
func CreateCustomMessage(
	customType string,
	content any,
	display bool,
	details any,
	timestamp int64,
) CustomMessage {
	return CustomMessage{
		Role:       RoleCustomMessage,
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
		Timestamp:  timestamp,
	}
}

// ConvertToLlm converts AgentMessages to LLM-compatible messages before the
// agent loop streams (pi: convertToLlm). Custom roles project to user
// messages; unknown message types are dropped.
//
// pig deviation (v0.86.1): tool-result images the model has already
// responded to are elided from the converted context (see image_elision.go);
// pi sends them on every request, which makes request bodies grow without
// bound over image-heavy sessions. Single choke point: all LLM callers
// (agent loop, compaction, branch summaries, bug reports) go through here.
func ConvertToLlm(messages []agent.AgentMessage) []types.Message {
	converted := make([]types.Message, 0, len(messages))
	for _, message := range messages {
		switch m := message.(type) {
		case types.SystemMessage:
			converted = append(converted, m)
		case *types.SystemMessage:
			converted = append(converted, m)
		case types.UserMessage:
			converted = append(converted, m)
		case types.AssistantMessage:
			converted = append(converted, m)
		case types.ToolResultMessage:
			converted = append(converted, m)
		case BashExecutionMessage:
			if m.ExcludeFromContext != nil && *m.ExcludeFromContext {
				continue
			}
			converted = append(converted, types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(BashExecutionToText(m))}},
				Timestamp: m.Timestamp,
			})
		case CustomMessage:
			content, ok := m.Content.([]types.Content)
			if !ok {
				if text, isString := m.Content.(string); isString {
					content = []types.Content{types.NewTextContent(text)}
				} else {
					content = nil
				}
			}
			converted = append(converted, types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{Parts: content},
				Timestamp: m.Timestamp,
			})
		case BranchSummaryMessage:
			converted = append(converted, types.UserMessage{
				Role: types.RoleUser,
				Content: types.UserContent{Parts: []types.Content{
					types.NewTextContent(BRANCH_SUMMARY_PREFIX + m.Summary + BRANCH_SUMMARY_SUFFIX),
				}},
				Timestamp: m.Timestamp,
			})
		case CompactionSummaryMessage:
			converted = append(converted, types.UserMessage{
				Role: types.RoleUser,
				Content: types.UserContent{Parts: []types.Content{
					types.NewTextContent(COMPACTION_SUMMARY_PREFIX + m.Summary + COMPACTION_SUMMARY_SUFFIX),
				}},
				Timestamp: m.Timestamp,
			})
		default:
			// pi's default arm returns undefined (message dropped).
		}
	}
	// pig deviation: elide tool-result images the model has already
	// responded to (see image_elision.go for the policy and rationale).
	return ElideRespondedToolResultImages(converted)
}
