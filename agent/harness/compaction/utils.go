// pi: src/harness/compaction/utils.ts — file-operation extraction and
// conversation serialization for summarization prompts.
//
// Deviations:
//   - TS Set<string> → map[string]bool accumulators.
//   - safeJsonStringify: JSON.stringify(undefined) yields "undefined" and
//     drops undefined object members; Go's JSON model has no undefined, so
//     nil marshals as "null" and NaN/function values (TS: "null"/no-throw)
//     surface as "[unserializable]".
//   - serializeConversation's tool-call argument entries follow Go map
//     iteration; keys are sorted for deterministic prompt text (TS relied on
//     object insertion order).
package compaction

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// FileOperations holds the file paths touched by a session branch or
// compaction range (pi: FileOperations).
type FileOperations struct {
	// Read holds files read but not necessarily modified.
	Read map[string]bool
	// Written holds files written by full-file write operations.
	Written map[string]bool
	// Edited holds files modified by edit operations.
	Edited map[string]bool
}

// CreateFileOps creates an empty file-operation accumulator (pi:
// createFileOps).
func CreateFileOps() *FileOperations {
	return &FileOperations{
		Read:    map[string]bool{},
		Written: map[string]bool{},
		Edited:  map[string]bool{},
	}
}

// ExtractFileOpsFromMessage adds file operations from assistant tool calls to
// an accumulator (pi: extractFileOpsFromMessage).
func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	assistant, ok := message.(types.AssistantMessage)
	if !ok || assistant.MessageRole() != types.RoleAssistant {
		return
	}

	for _, block := range assistant.Content {
		toolCall, ok := block.(types.ToolCall)
		if !ok {
			continue
		}
		args := toolCall.Arguments
		if args == nil {
			continue
		}
		path, ok := args["path"].(string)
		if !ok || path == "" {
			continue
		}

		switch toolCall.Name {
		case "read":
			fileOps.Read[path] = true
		case "write":
			fileOps.Written[path] = true
		case "edit":
			fileOps.Edited[path] = true
		}
	}
}

// ComputeFileLists computes sorted read-only and modified file lists from
// accumulated operations (pi: computeFileLists).
func ComputeFileLists(fileOps *FileOperations) (readFiles []string, modifiedFiles []string) {
	modified := map[string]bool{}
	for f := range fileOps.Edited {
		modified[f] = true
	}
	for f := range fileOps.Written {
		modified[f] = true
	}
	for f := range fileOps.Read {
		if !modified[f] {
			readFiles = append(readFiles, f)
		}
	}
	for f := range modified {
		modifiedFiles = append(modifiedFiles, f)
	}
	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)
	return readFiles, modifiedFiles
}

// FormatFileOperations formats file lists as summary metadata tags (pi:
// formatFileOperations).
func FormatFileOperations(readFiles []string, modifiedFiles []string) string {
	var sections []string
	if len(readFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<read-files>\n%s\n</read-files>", strings.Join(readFiles, "\n")))
	}
	if len(modifiedFiles) > 0 {
		sections = append(sections, fmt.Sprintf("<modified-files>\n%s\n</modified-files>", strings.Join(modifiedFiles, "\n")))
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

const toolResultMaxChars = 2000

// safeJsonStringify stringifies a value for prompt text (pi:
// safeJsonStringify).
func safeJsonStringify(value any) string {
	if value == nil {
		// JSON.stringify(undefined) === undefined → pi renders "undefined".
		return "undefined"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[unserializable]"
	}
	return string(encoded)
}

// truncateForSummary cuts long tool results for the summarization prompt
// (pi: truncateForSummary).
func truncateForSummary(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	truncatedChars := len(text) - maxChars
	return fmt.Sprintf("%s\n\n[... %d more characters truncated]", text[:maxChars], truncatedChars)
}

// SerializeConversation serializes LLM messages to plain text for
// summarization prompts (pi: serializeConversation).
func SerializeConversation(messages []types.Message) string {
	var parts []string

	for _, msg := range messages {
		switch m := msg.(type) {
		case types.UserMessage:
			content := userContentText(m.Content)
			if content != "" {
				parts = append(parts, fmt.Sprintf("[User]: %s", content))
			}
		case types.AssistantMessage:
			var thinkingParts []string
			var toolCalls []string

			hasText := false
			for _, block := range m.Content {
				switch b := block.(type) {
				case types.ThinkingContent:
					thinkingParts = append(thinkingParts, b.Thinking)
				case types.ToolCall:
					keys := make([]string, 0, len(b.Arguments))
					for k := range b.Arguments {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					entries := make([]string, 0, len(keys))
					for _, k := range keys {
						entries = append(entries, fmt.Sprintf("%s=%s", k, safeJsonStringify(b.Arguments[k])))
					}
					toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", b.Name, strings.Join(entries, ", ")))
				case types.TextContent:
					hasText = true
				}
			}

			if len(thinkingParts) > 0 {
				parts = append(parts, fmt.Sprintf("[Assistant thinking]: %s", strings.Join(thinkingParts, "\n")))
			}
			if hasText {
				parts = append(parts, fmt.Sprintf("[Assistant]: %s", ai.ContentText(m.Content, "\n")))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, fmt.Sprintf("[Assistant tool calls]: %s", strings.Join(toolCalls, "; ")))
			}
		case types.ToolResultMessage:
			content := ai.ContentText(m.Content, "")
			if content != "" {
				parts = append(parts, fmt.Sprintf("[Tool result]: %s", truncateForSummary(content, toolResultMaxChars)))
			}
		}
	}

	return strings.Join(parts, "\n\n")
}

// userContentText joins user message content (pi: contentText(msg.content,
// "") over a string-or-blocks union).
func userContentText(content types.UserContent) string {
	if content.Parts == nil {
		return content.String
	}
	return ai.ContentText(content.Parts, "")
}
