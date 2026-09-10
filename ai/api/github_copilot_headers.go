// pi: api/github-copilot-headers.ts.
package api

import (
	"github.com/ZoroSpace/pig/ai/types"
)

// InferCopilotInitiator reports whether the request is user-initiated or
// agent-initiated (pi: inferCopilotInitiator). Copilot expects X-Initiator to
// indicate it: a trailing non-user message marks an agent-initiated follow-up.
func InferCopilotInitiator(messages []types.Message) string {
	if len(messages) == 0 {
		return "user"
	}
	last := messages[len(messages)-1]
	if last.MessageRole() != types.RoleUser {
		return "agent"
	}
	return "user"
}

// HasCopilotVisionInput reports whether the messages include images (pi:
// hasCopilotVisionInput); Copilot requires the Copilot-Vision-Request header
// then.
func HasCopilotVisionInput(messages []types.Message) bool {
	for _, msg := range messages {
		switch m := msg.(type) {
		case types.UserMessage:
			if m.Content.Parts != nil && hasImageBlock(m.Content.Parts) {
				return true
			}
		case types.ToolResultMessage:
			if hasImageBlock(m.Content) {
				return true
			}
		}
	}
	return false
}

func hasImageBlock(content []types.Content) bool {
	for _, c := range content {
		if c.ContentKind() == types.KindImage {
			return true
		}
	}
	return false
}

// BuildCopilotDynamicHeaders builds the Copilot-specific request headers (pi:
// buildCopilotDynamicHeaders).
func BuildCopilotDynamicHeaders(messages []types.Message, hasImages bool) map[string]string {
	headers := map[string]string{
		"X-Initiator":   InferCopilotInitiator(messages),
		"Openai-Intent": "conversation-edits",
	}
	if hasImages {
		headers["Copilot-Vision-Request"] = "true"
	}
	return headers
}
