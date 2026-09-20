// pi: src/utils/text.ts — extract and join text from message content.
package utils

import "github.com/ZoroSpace/pig/ai/types"

// ContentText extracts and joins text from message content (pi:
// contentText). Non-text blocks are skipped; separator defaults to "\n" at
// call sites in pi (pass it explicitly here).
func ContentText(content []types.Content, separator string) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		if text, ok := block.(types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += separator
		}
		result += part
	}
	return result
}

// systemMessageContentText joins a system message body (pi: contentText over
// string | TextContent[]; the default separator there is "\n\n").
func systemMessageContentText(content types.SystemContent) string {
	if content.Parts == nil {
		return content.String
	}
	return ContentText(content.Parts, "\n\n")
}

// GetSystemMessageText renders a system message as a complete prompt: its
// content followed by its sections (pi: getSystemMessageText).
func GetSystemMessageText(message *types.SystemMessage) string {
	parts := []string{systemMessageContentText(message.Content)}
	if message.Sections != nil {
		for _, name := range message.Sections.Keys {
			if text := message.Sections.Values[name]; text != nil {
				parts = append(parts, *text)
			}
		}
	}
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			kept = append(kept, part)
		}
	}
	return joinWith(kept, "\n\n")
}

// RenderSystemMessageUpdate renders a later system message for APIs that
// accept system messages mid-conversation. Section changes are framed by
// name so the model can relate them to the leading prompt. This framing is
// request-time only and may change between versions (pi:
// renderSystemMessageUpdate).
func RenderSystemMessageUpdate(message *types.SystemMessage) string {
	var parts []string
	text := systemMessageContentText(message.Content)
	if len(text) > 0 {
		parts = append(parts, text)
	}
	if message.Sections != nil {
		for _, name := range message.Sections.Keys {
			if value := message.Sections.Values[name]; value == nil {
				parts = append(parts, "Removed system prompt section \""+name+"\".")
			} else {
				parts = append(parts, "Updated system prompt section \""+name+"\":\n\n"+*value)
			}
		}
	}
	return joinWith(parts, "\n\n")
}

func joinWith(parts []string, separator string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += separator
		}
		result += part
	}
	return result
}
