// pi: core/compaction/utils.ts — shared utilities for compaction and branch
// summarization on the coding-agent side.
//
// This file is a near-byte-identical duplicate of the agent harness's
// compaction utils (agent/src/harness/compaction/utils.ts; pig:
// agent/harness/compaction/utils.go). The only behavioral difference is
// serializeConversation's tool-call argument formatting: the harness uses
// safeJsonStringify while core uses JSON.stringify — identical output for
// every JSON-decodable argument object, and tool-call arguments always come
// from JSON parsing, so per the edit-diff/truncate reuse precedent pig keeps
// the single harness implementation and re-exports it here under the core
// names. Go's map iteration order makes the serialized argument order
// key-sorted where TS preserves object insertion order (declared at the
// harness port and unchanged here).
package core

import (
	"github.com/ZoroSpace/pig/agent"
	compaction "github.com/ZoroSpace/pig/agent/harness/compaction"
	"github.com/ZoroSpace/pig/ai/types"
)

// FileOperations tracks the file paths touched by a session branch or
// compaction range (pi: FileOperations).
type FileOperations = compaction.FileOperations

// CreateFileOps creates an empty file-operation accumulator (pi:
// createFileOps).
func CreateFileOps() *FileOperations { return compaction.CreateFileOps() }

// ExtractFileOpsFromMessage adds the file operations of one assistant
// message's tool calls to the accumulator (pi: extractFileOpsFromMessage).
func ExtractFileOpsFromMessage(message agent.AgentMessage, fileOps *FileOperations) {
	compaction.ExtractFileOpsFromMessage(message, fileOps)
}

// ComputeFileLists computes the sorted read-only and modified file lists
// from accumulated operations (pi: computeFileLists).
func ComputeFileLists(fileOps *FileOperations) (readFiles []string, modifiedFiles []string) {
	return compaction.ComputeFileLists(fileOps)
}

// FormatFileOperations formats the file lists as summary metadata tags (pi:
// formatFileOperations).
func FormatFileOperations(readFiles []string, modifiedFiles []string) string {
	return compaction.FormatFileOperations(readFiles, modifiedFiles)
}

// SerializeConversation serializes LLM messages to text for summarization;
// call ConvertToLlm first to handle custom message types (pi:
// serializeConversation).
func SerializeConversation(messages []types.Message) string {
	return compaction.SerializeConversation(messages)
}

// SummarizationSystemPrompt is the system prompt for every summarization
// request (pi: SUMMARIZATION_SYSTEM_PROMPT in core/compaction/utils.ts; the
// harness twin lives in its compaction.ts).
const SummarizationSystemPrompt = compaction.SummarizationSystemPrompt
