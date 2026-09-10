// pi: src/harness/compaction/branch-summarization.ts — summarization of
// abandoned session branches before navigation.
//
// Deviations:
//   - TS Result<T, BranchSummaryError> collapses to (T, error) with
//     *harness.BranchSummaryError as the concrete error type.
//   - GenerateBranchSummaryOptions drops pi's signal member (cancellation
//     travels as the leading context.Context of GenerateBranchSummary, per
//     the AbortSignal → context rule).
//   - reserveTokens defaults via pointer (nil = pi's 16384 default) to keep
//     an explicit 0 distinguishable.
//   - pi's `export type { FileOperations }` re-export needs no counterpart
//     (same package).
package compaction

import (
	"context"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// BranchSummaryResult is generated branch summary data ready to be persisted
// as a branch-summary entry (pi: BranchSummaryResult).
type BranchSummaryResult struct {
	Summary       string
	Usage         *types.Usage
	ReadFiles     []string
	ModifiedFiles []string
}

// BranchSummaryDetails holds file-operation details stored on generated
// branch summary entries (pi: BranchSummaryDetails).
type BranchSummaryDetails struct {
	// ReadFiles lists files read while exploring the summarized branch.
	ReadFiles []string `json:"readFiles"`
	// ModifiedFiles lists files modified while exploring the summarized
	// branch.
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchPreparation holds the prepared branch content for summarization (pi:
// BranchPreparation).
type BranchPreparation struct {
	// Messages holds the messages selected for the branch summary.
	Messages []agent.AgentMessage
	// FileOps holds the file operations extracted from the branch.
	FileOps *FileOperations
	// TotalTokens is the estimated token count for selected messages.
	TotalTokens int64
}

// CollectEntriesResult holds the entries selected for branch summarization
// (pi: CollectEntriesResult).
type CollectEntriesResult struct {
	// Entries holds the entries to summarize in chronological order.
	Entries []session.Entry
	// CommonAncestorID is the deepest common ancestor between the previous
	// leaf and target entry; "" when none (pi: string | null).
	CommonAncestorID string
}

// GenerateBranchSummaryOptions are the options for generating a branch
// summary (pi: GenerateBranchSummaryOptions; pi's signal member is carried
// by the context argument).
type GenerateBranchSummaryOptions struct {
	// Models is the provider collection the summarization request goes
	// through; owns auth resolution.
	Models ai.Models
	// Model is the model used for summarization.
	Model *ai.Model
	// CustomInstructions are optional instructions appended to or replacing
	// the default prompt.
	CustomInstructions string
	// ReplaceInstructions replaces the default prompt with custom
	// instructions instead of appending them.
	ReplaceInstructions bool
	// ReserveTokens is the token budget reserved for prompt and model
	// output; nil = pi's 16384 default.
	ReserveTokens *int
	// Retry is the optional retry policy for transient summarization errors.
	Retry *ai.RetryPolicy
	// Callbacks are optional callbacks for retry reporting.
	Callbacks *ai.RetryCallbacks
}

// BranchEntryReader is pi's `Pick<Branch, "findEntries">` capability.
type BranchEntryReader interface {
	FindEntries(query *session.BranchScan, ctx context.Context) ([]session.Entry, error)
}

// SessionEntryReader is pi's `Pick<Session, "getEntry">` capability.
type SessionEntryReader interface {
	GetEntry(id string, ctx context.Context) (session.Entry, error)
}

// CollectEntriesForBranchSummary collects entries that should be summarized
// before navigating to a different session tree entry (pi:
// collectEntriesForBranchSummary; v0.85.1 takes the branch and session
// capabilities separately).
func CollectEntriesForBranchSummary(branch BranchEntryReader, sess SessionEntryReader, oldTipID *string, targetID string, ctx context.Context) (*CollectEntriesResult, error) {
	if oldTipID == nil {
		return &CollectEntriesResult{Entries: nil, CommonAncestorID: ""}, nil
	}
	oldEntries, err := branch.FindEntries(&session.BranchScan{Start: oldTipID}, ctx)
	if err != nil {
		return nil, err
	}
	oldPath := map[string]bool{}
	for _, entry := range oldEntries {
		oldPath[entry.Base().ID] = true
	}
	targetPath, err := branch.FindEntries(&session.BranchScan{Start: &targetID}, ctx)
	if err != nil {
		return nil, err
	}
	commonAncestorID := ""
	for _, entry := range targetPath {
		if oldPath[entry.Base().ID] {
			commonAncestorID = entry.Base().ID
			break
		}
	}

	var entries []session.Entry
	current := *oldTipID
	for current != "" && current != commonAncestorID {
		entry, err := sess.GetEntry(current, ctx)
		if err != nil {
			return nil, err
		}
		if entry == nil {
			return nil, fmt.Errorf("Corrupt session: entry %s not found", current)
		}
		entries = append(entries, entry)
		// pi: current = entry.parentId; "" ↔ null.
		if parent := entry.Base().ParentID; parent != nil {
			current = *parent
		} else {
			current = ""
		}
	}
	// pi pushes while walking to the root, then reverses.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return &CollectEntriesResult{Entries: entries, CommonAncestorID: commonAncestorID}, nil
}

// getBranchMessageFromEntry projects an entry to its context message,
// dropping tool results and non-message entries (pi: the local
// getMessageFromEntry).
func getBranchMessageFromEntry(entry session.Entry) agent.AgentMessage {
	switch e := entry.(type) {
	case *session.MessageEntry:
		if e.Message.MessageRole() == types.RoleToolResult {
			return nil
		}
		return e.Message
	case *session.BranchSummaryEntry:
		return harness.CreateBranchSummaryMessage(e.Summary, e.FromID, e.Timestamp)
	case *session.CompactionEntry:
		return harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp)
	default:
		// custom.
		return nil
	}
}

// PrepareBranchEntries prepares branch entries for summarization within an
// optional token budget (pi: prepareBranchEntries; tokenBudget 0 = pi's
// default of 0 = unlimited).
func PrepareBranchEntries(entries []session.Entry, tokenBudget int64) *BranchPreparation {
	var messages []agent.AgentMessage
	fileOps := CreateFileOps()
	totalTokens := int64(0)
	// Details from earlier branch summaries seed the file-operation
	// accumulator regardless of the budget scan.
	for _, entry := range entries {
		branchSummary, ok := entry.(*session.BranchSummaryEntry)
		if !ok {
			continue
		}
		for _, f := range detailStrings(branchSummary.Details, "readFiles") {
			fileOps.Read[f] = true
		}
		for _, f := range detailStrings(branchSummary.Details, "modifiedFiles") {
			fileOps.Edited[f] = true
		}
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		message := getBranchMessageFromEntry(entry)
		if message == nil {
			continue
		}
		ExtractFileOpsFromMessage(message, fileOps)

		tokens := EstimateTokens(message)
		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			if _, isCompaction := entry.(*session.CompactionEntry); isCompaction {
				if totalTokens < tokenBudget*9/10 {
					messages = append([]agent.AgentMessage{message}, messages...)
					totalTokens += tokens
				}
			} else if _, isBranchSummary := entry.(*session.BranchSummaryEntry); isBranchSummary {
				if totalTokens < tokenBudget*9/10 {
					messages = append([]agent.AgentMessage{message}, messages...)
					totalTokens += tokens
				}
			}
			break
		}

		messages = append([]agent.AgentMessage{message}, messages...)
		totalTokens += tokens
	}

	return &BranchPreparation{Messages: messages, FileOps: fileOps, TotalTokens: totalTokens}
}

const branchSummaryPreamble = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`

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

// GenerateBranchSummary generates a summary for abandoned branch entries
// (pi: generateBranchSummary). ctx carries the summarization request's
// cancellation (pi: options.signal).
func GenerateBranchSummary(ctx context.Context, entries []session.Entry, options *GenerateBranchSummaryOptions) (*BranchSummaryResult, error) {
	reserveTokens := 16384
	if options.ReserveTokens != nil {
		reserveTokens = *options.ReserveTokens
	}
	contextWindow := options.Model.ContextWindow
	if contextWindow == 0 {
		contextWindow = 128000
	}
	tokenBudget := contextWindow - int64(reserveTokens)

	preparation := PrepareBranchEntries(entries, tokenBudget)

	if len(preparation.Messages) == 0 {
		return &BranchSummaryResult{Summary: "No content to summarize", ReadFiles: []string{}, ModifiedFiles: []string{}}, nil
	}
	llmMessages := harness.ConvertToLlm(preparation.Messages)
	conversationText := SerializeConversation(llmMessages)
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
			Timestamp: nowMillis(),
		},
	}
	response, err := CompleteSimpleWithRetries(
		ctx,
		options.Models,
		options.Model,
		&ai.Context{SystemPrompt: SummarizationSystemPrompt, Messages: summarizationMessages},
		summarizationCompletionOptions(2048, options.Model, ""),
		options.Retry,
		options.Callbacks,
	)
	if err != nil {
		return nil, err
	}
	if response.StopReason == types.StopReasonAborted {
		message := response.ErrorMessage
		if message == "" {
			message = "Branch summary aborted"
		}
		return nil, harness.NewBranchSummaryError(harness.BranchSummaryCodeAborted, message, nil)
	}
	if response.StopReason == types.StopReasonError {
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return nil, harness.NewBranchSummaryError(harness.BranchSummaryCodeSummarizationFailed, fmt.Sprintf("Branch summary failed: %s", message), nil)
	}

	summary := response.Text()
	summary = branchSummaryPreamble + summary
	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	resultSummary := summary
	if resultSummary == "" {
		resultSummary = "No summary generated"
	}
	return &BranchSummaryResult{
		Summary:       resultSummary,
		Usage:         &response.Usage,
		ReadFiles:     readFiles,
		ModifiedFiles: modifiedFiles,
	}, nil
}
