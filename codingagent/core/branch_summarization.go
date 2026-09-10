// pi: core/compaction/branch-summarization.ts — branch summarization for
// tree navigation. When navigating to a different point in the session tree,
// this generates a summary of the branch being left so context isn't lost.
//
// This is the coding-agent-side implementation over ReadonlySessionManager /
// SessionEntry (pi keeps a separate, different implementation over the agent
// harness's session in agent/src/harness/compaction/branch-summarization.ts;
// pig ports both, this one included per the two-implementations precedent).
// It lives flat in package core for the dependency-direction reason declared
// in compaction.go's header.
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

// ============================================================================
// Types
// ============================================================================

// BranchSummaryResult is the outcome of GenerateBranchSummary; empty strings
// and false mean pi's undefined members (pi: BranchSummaryResult).
type BranchSummaryResult struct {
	// Summary is the generated summary text; "" = not generated.
	Summary string
	// Usage is the usage of the summarization call, if available.
	Usage *types.Usage
	// ReadFiles are files only read, not modified; nil = not reported (pi:
	// readFiles?).
	ReadFiles []string
	// ModifiedFiles are modified files; nil = not reported (pi:
	// modifiedFiles?).
	ModifiedFiles []string
	// Aborted is true when the summarization call was aborted (pi: aborted?).
	Aborted bool
	// Error is the error message when summarization failed; "" = success (pi:
	// error?).
	Error string
}

// BranchSummaryDetails holds the details stored in
// BranchSummaryEntry.Details for file tracking (pi: BranchSummaryDetails).
type BranchSummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchPreparation is the prepared summarization input with token budget
// (pi: BranchPreparation).
type BranchPreparation struct {
	// Messages are the extracted messages for summarization, in
	// chronological order.
	Messages []agent.AgentMessage
	// FileOps are the file operations extracted from tool calls.
	FileOps *FileOperations
	// TotalTokens is the total estimated tokens in messages.
	TotalTokens int64
}

// CollectEntriesResult holds the entries to summarize plus the common
// ancestor between old and new position, if any (pi: CollectEntriesResult;
// commonAncestorId string|null → *string).
type CollectEntriesResult struct {
	// Entries are the entries to summarize, in chronological order.
	Entries []SessionEntry
	// CommonAncestorID is the deepest node shared by both paths; nil = none
	// (pi: string | null).
	CommonAncestorID *string
}

// GenerateBranchSummaryOptions are the options for GenerateBranchSummary (pi:
// GenerateBranchSummaryOptions; the signal member is dropped — cancellation
// travels as the Go context).
type GenerateBranchSummaryOptions struct {
	// Model is the model to use for summarization.
	Model *ai.Model
	// APIKey is the API key for the model; "" = none (pi: apiKey?).
	APIKey string
	// Headers are request headers for the model; nil = none (pi: headers?).
	Headers map[string]string
	// Env holds provider-scoped environment values for the model; nil = none
	// (pi: env?).
	Env ai.ProviderEnv
	// CustomInstructions are optional custom instructions for summarization;
	// "" = none (pi: customInstructions?).
	CustomInstructions string
	// ReplaceInstructions makes customInstructions replace the default
	// prompt instead of being appended (pi: replaceInstructions?).
	ReplaceInstructions bool
	// ReserveTokens is the token budget reserved for prompt + LLM response;
	// nil = 16384 (pi: reserveTokens?).
	ReserveTokens *int
	// StreamFn is the optional session stream function, used to preserve SDK
	// request behavior without mutating agent state (pi: streamFn?).
	StreamFn agent.StreamFn
	// Retry is the retry policy for transient summarization errors; reuses
	// coding-agent's settings.retry (pi: retry?).
	Retry *ai.RetryPolicy
	// Callbacks are optional callbacks for retry reporting (e.g. TUI retry
	// indicators) (pi: callbacks?).
	Callbacks *ai.RetryCallbacks
}

// ============================================================================
// Entry Collection
// ============================================================================

// CollectEntriesForBranchSummary collects the entries that should be
// summarized when navigating from one position to another.
//
// Walks from oldLeafId back to the common ancestor with targetId, collecting
// entries along the way. Does NOT stop at compaction boundaries — those are
// included and their summaries become context.
//
// oldLeafID nil means no old position: nothing to summarize (pi:
// collectEntriesForBranchSummary).
func CollectEntriesForBranchSummary(
	session ReadonlySessionManager,
	oldLeafID *string,
	targetID string,
) *CollectEntriesResult {
	// If no old position, nothing to summarize
	if oldLeafID == nil {
		return &CollectEntriesResult{Entries: nil, CommonAncestorID: nil}
	}

	// Find common ancestor (deepest node that's on both paths)
	oldPath := map[string]bool{}
	for _, entry := range session.GetBranch(*oldLeafID) {
		oldPath[entry.Base().ID] = true
	}
	targetPath := session.GetBranch(targetID)

	// targetPath is root-first, so iterate backwards to find deepest common ancestor
	var commonAncestorID *string
	for i := len(targetPath) - 1; i >= 0; i-- {
		if oldPath[targetPath[i].Base().ID] {
			id := targetPath[i].Base().ID
			commonAncestorID = &id
			break
		}
	}

	// Collect entries from old leaf back to common ancestor
	var entries []SessionEntry
	current := *oldLeafID
	for current != "" && (commonAncestorID == nil || current != *commonAncestorID) {
		entry := session.GetEntry(current)
		if entry == nil {
			break
		}
		entries = append(entries, entry)
		parentID := entry.Base().ParentID
		if parentID == nil {
			break
		}
		current = *parentID
	}

	// Reverse to get chronological order
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return &CollectEntriesResult{Entries: entries, CommonAncestorID: commonAncestorID}
}

// ============================================================================
// Entry to Message Conversion
// ============================================================================

// getMessageFromEntry extracts the AgentMessage from a session entry; similar
// to getMessageFromEntry in compaction.ts but also handles compaction
// entries (pi: getMessageFromEntry).
func getMessageFromEntry(entry SessionEntry) agent.AgentMessage {
	switch e := entry.(type) {
	case *SessionMessageEntry:
		// Skip tool results — context is in assistant's tool call. A nil
		// message (corrupt input only) contributes nothing, mirroring
		// SessionEntryToContextMessages.
		if e.Message == nil || e.Message.MessageRole() == types.RoleToolResult {
			return nil
		}
		return e.Message

	case *CustomMessageEntry:
		return harness.CreateCustomMessage(e.CustomType, e.Content, e.Display, e.Details, isoToMillis(e.Timestamp))

	case *BranchSummaryEntry:
		return harness.CreateBranchSummaryMessage(e.Summary, e.FromID, isoToMillis(e.Timestamp))

	case *CompactionEntry:
		return harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, isoToMillis(e.Timestamp))

	// These don't contribute to conversation content
	default:
		// thinking_level_change, model_change, custom, label, session_info
		return nil
	}
}

// branchSummaryDetailsOf reads BranchSummaryEntry details in both in-memory
// and JSONL read-back forms (pi: `entry.details as BranchSummaryDetails`
// followed by Array.isArray member guards).
func branchSummaryDetailsOf(details any) BranchSummaryDetails {
	var out BranchSummaryDetails
	switch d := details.(type) {
	case BranchSummaryDetails:
		out = d
	case *BranchSummaryDetails:
		out = *d
	case map[string]any:
		if rf, ok := d["readFiles"].([]any); ok {
			for _, f := range rf {
				if s, ok := f.(string); ok {
					out.ReadFiles = append(out.ReadFiles, s)
				}
			}
		}
		if mf, ok := d["modifiedFiles"].([]any); ok {
			for _, f := range mf {
				if s, ok := f.(string); ok {
					out.ModifiedFiles = append(out.ModifiedFiles, s)
				}
			}
		}
	}
	return out
}

// PrepareBranchEntries prepares entries for summarization with a token
// budget.
//
// Walks entries from NEWEST to OLDEST, adding messages until we hit the
// token budget. This ensures we keep the most recent context when the branch
// is too long.
//
// Also collects file operations from:
//   - Tool calls in assistant messages
//   - Existing branch_summary entries' details (for cumulative tracking)
//
// tokenBudget is the maximum tokens to include (0 = no limit) (pi:
// prepareBranchEntries, default tokenBudget 0).
func PrepareBranchEntries(entries []SessionEntry, tokenBudget int64) *BranchPreparation {
	var messages []agent.AgentMessage
	fileOps := CreateFileOps()
	var totalTokens int64

	// First pass: collect file ops from ALL entries (even if they don't fit
	// in token budget). This ensures we capture cumulative file tracking from
	// nested branch summaries. Only extract from pi-generated summaries
	// (fromHook !== true), not extension-generated ones.
	for _, entry := range entries {
		if bs, ok := entry.(*BranchSummaryEntry); ok && (bs.FromHook == nil || !*bs.FromHook) && bs.Details != nil {
			details := branchSummaryDetailsOf(bs.Details)
			for _, f := range details.ReadFiles {
				fileOps.Read[f] = true
			}
			// Modified files go into both edited and written for proper
			// deduplication — pi adds to edited only here (written stays
			// empty for summary details; the comment follows pi).
			for _, f := range details.ModifiedFiles {
				fileOps.Edited[f] = true
			}
		}
	}

	// Second pass: walk from newest to oldest, adding messages until token budget
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		message := getMessageFromEntry(entry)
		if message == nil {
			continue
		}

		// Extract file ops from assistant messages (tool calls)
		ExtractFileOpsFromMessage(message, fileOps)

		tokens := EstimateTokens(message)

		// Check budget before adding
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			// If this is a summary entry, try to fit it anyway as it's
			// important context
			_, isCompaction := entry.(*CompactionEntry)
			_, isBranchSummary := entry.(*BranchSummaryEntry)
			if isCompaction || isBranchSummary {
				if float64(totalTokens) < float64(tokenBudget)*0.9 {
					messages = append([]agent.AgentMessage{message}, messages...)
					totalTokens += tokens
				}
			}
			// Stop - we've hit the budget
			break
		}

		messages = append([]agent.AgentMessage{message}, messages...)
		totalTokens += tokens
	}

	return &BranchPreparation{Messages: messages, FileOps: fileOps, TotalTokens: totalTokens}
}

// ============================================================================
// Summary Generation
// ============================================================================

// branchSummaryPreamble gives the model context about the branch summary (pi:
// BRANCH_SUMMARY_PREAMBLE).
const branchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

// branchSummaryPrompt is the branch summarization request prompt (pi:
// BRANCH_SUMMARY_PROMPT).
const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// GenerateBranchSummary generates a summary of abandoned branch entries (pi:
// generateBranchSummary).
func GenerateBranchSummary(
	ctx context.Context,
	entries []SessionEntry,
	options *GenerateBranchSummaryOptions,
) (*BranchSummaryResult, error) {
	reserveTokens := 16384
	if options.ReserveTokens != nil {
		reserveTokens = *options.ReserveTokens
	}

	// Token budget = context window minus reserved space for prompt + response
	contextWindow := options.Model.ContextWindow
	if contextWindow == 0 {
		contextWindow = 128000
	}
	tokenBudget := contextWindow - int64(reserveTokens)

	preparation := PrepareBranchEntries(entries, tokenBudget)

	if len(preparation.Messages) == 0 {
		return &BranchSummaryResult{Summary: "No content to summarize"}, nil
	}

	// Transform to LLM-compatible messages, then serialize to text.
	// Serialization prevents the model from treating it as a conversation to
	// continue.
	llmMessages := harness.ConvertToLlm(preparation.Messages)
	conversationText := SerializeConversation(llmMessages)

	// Build prompt
	var instructions string
	if options.ReplaceInstructions && options.CustomInstructions != "" {
		instructions = options.CustomInstructions
	} else if options.CustomInstructions != "" {
		instructions = fmt.Sprintf("%s\n\nAdditional focus: %s", branchSummaryPrompt, options.CustomInstructions)
	} else {
		instructions = branchSummaryPrompt
	}
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, instructions)

	summarizationMessages := []types.Message{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(promptText)}},
			Timestamp: time.Now().UnixMilli(),
		},
	}

	// Call LLM for summarization. Prefer the session stream function so SDK
	// request behavior (timeouts, retries, attribution headers) stays
	// consistent without running through agent state/events. Retried via
	// CompleteSummarization so transient stream drops reuse the configured
	// retry policy. pi builds requestOptions inline (no reasoning setup —
	// unlike compaction's createSummarizationOptions).
	requestOptions := &ai.SimpleStreamOptions{}
	requestOptions.MaxTokens = lo.ToPtr(int64(2048))
	requestOptions.APIKey = options.APIKey
	requestOptions.Env = options.Env
	if options.Headers != nil {
		requestOptions.Headers = ai.ProviderHeaders{}
		for k, v := range options.Headers {
			header := v
			requestOptions.Headers[k] = &header
		}
	}
	response, err := CompleteSummarization(
		ctx,
		options.Model,
		&ai.Context{SystemPrompt: SummarizationSystemPrompt, Messages: summarizationMessages},
		requestOptions,
		options.StreamFn,
		options.Retry,
		options.Callbacks,
	)
	if err != nil {
		return nil, err
	}

	// Check if aborted or errored
	if response.StopReason == types.StopReasonAborted {
		return &BranchSummaryResult{Aborted: true}, nil
	}
	if response.StopReason == types.StopReasonError {
		errorMessage := response.ErrorMessage
		if errorMessage == "" {
			errorMessage = "Summarization failed"
		}
		return &BranchSummaryResult{Error: errorMessage}, nil
	}
	for _, block := range response.Content {
		if block.ContentKind() == types.KindToolCall {
			return &BranchSummaryResult{Error: "Branch summarization attempted to call a tool"}, nil
		}
	}

	summary := branchSummaryPreamble + ai.ContentText(response.Content, "")

	// Compute file lists and append to summary
	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if summary == "" {
		summary = "No summary generated"
	}

	return &BranchSummaryResult{
		Summary:       summary,
		Usage:         &response.Usage,
		ReadFiles:     readFiles,
		ModifiedFiles: modifiedFiles,
	}, nil
}
