// pi: core/compaction/compaction.ts — context compaction for long sessions.
//
// Pure functions for compaction logic. The session manager handles I/O, and
// after compaction the session is reloaded.
//
// Placement deviation (reducer/agentharness precedent, inverse direction):
// pi keeps core/compaction/ in the same TS package as session-manager.ts,
// and AgentSession (also that package) consumes it. Moving these functions
// into a Go subpackage would make it import codingagent/core while package
// core imports it back — an import cycle. They therefore live flat in
// package core; the pi source path stays in this header.
//
// Segment deviations (declared):
//   - The token-estimation half (calculateContextTokens /
//     estimateContextTokens / estimateTokens / combineUsage) is byte-level
//     identical in pi between the agent harness's compaction.ts and this
//     file (only safeJsonStringify vs JSON.stringify for tool-call args,
//     behaviorally equivalent for JSON-decoded arguments). Per the
//     edit-diff/truncate reuse precedent pig keeps the single harness
//     implementation and delegates to it
//     (agent/harness/compaction/compaction.go).
//   - pi's options.signal members are dropped; cancellation travels as the
//     Go context (AbortSignal → context rule).
//   - The completeSimple fallback of completeSummarization goes through the
//     api-provider registry (ai.GetApiProvider) directly. pi's
//     compat.ts standalone stream family (getBuiltinProviderForModel with
//     the cloudflare-legacy branch) is not ported yet: pig's
//     builtin adapters self-register in the same registry. The withEnvApiKey
//     env-key fill landed in ai.StreamSimple on 2026-09-13
//     (docs/fix/2026-09-13-env-api-keys-fill.md). AgentSession always supplies the
//     agent's streamFn, so the fallback is only reachable for direct
//     library callers.
package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	compaction "github.com/ZoroSpace/pig/agent/harness/compaction"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// ============================================================================
// File Operation Tracking
// ============================================================================

// CompactionDetails holds the details stored in CompactionEntry.Details for
// file tracking (pi: CompactionDetails).
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// extractFileOperations extracts file operations from messages and previous
// compaction entries (pi: extractFileOperations).
func extractFileOperations(messages []agent.AgentMessage, entries []SessionEntry, prevCompactionIndex int) *FileOperations {
	fileOps := CreateFileOps()

	// Collect from previous compaction's details (if pi-generated)
	if prevCompactionIndex >= 0 {
		prevCompaction := entries[prevCompactionIndex].(*CompactionEntry)
		// fromHook field kept for session file compatibility
		if (prevCompaction.FromHook == nil || !*prevCompaction.FromHook) && prevCompaction.Details != nil {
			var details CompactionDetails
			switch d := prevCompaction.Details.(type) {
			case CompactionDetails:
				details = d
			case *CompactionDetails:
				details = *d
			case map[string]any:
				// JSONL read-back path: details decode as generic maps (pi's
				// `details as CompactionDetails` accepts any object shape and
				// guards each member with Array.isArray).
				if rf, ok := d["readFiles"].([]any); ok {
					for _, f := range rf {
						if s, ok := f.(string); ok {
							details.ReadFiles = append(details.ReadFiles, s)
						}
					}
				}
				if mf, ok := d["modifiedFiles"].([]any); ok {
					for _, f := range mf {
						if s, ok := f.(string); ok {
							details.ModifiedFiles = append(details.ModifiedFiles, s)
						}
					}
				}
			}
			for _, f := range details.ReadFiles {
				fileOps.Read[f] = true
			}
			for _, f := range details.ModifiedFiles {
				fileOps.Edited[f] = true
			}
		}
	}

	// Extract from tool calls in messages
	for _, msg := range messages {
		ExtractFileOpsFromMessage(msg, fileOps)
	}

	return fileOps
}

// ============================================================================
// Message Extraction
// ============================================================================

// getMessageFromEntryForCompaction extracts the AgentMessage from an entry if
// it produces one; entries that don't contribute to LLM context return nil
// (pi: getMessageFromEntryForCompaction).
func getMessageFromEntryForCompaction(entry SessionEntry) agent.AgentMessage {
	if _, ok := entry.(*CompactionEntry); ok {
		return nil
	}
	messages := SessionEntryToContextMessages(entry)
	if len(messages) > 0 {
		return messages[0]
	}
	return nil
}

// CompactionResult is the result from Compact — SessionManager adds
// uuid/parentUuid when saving (pi: CompactionResult<T>; the details generic
// collapses to any, filled with CompactionDetails by Compact).
type CompactionResult struct {
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	TokensBefore     int64  `json:"tokensBefore"`
	// EstimatedTokensAfter is filled by AgentSession after rebuilding the
	// context (pi: estimatedTokensAfter?).
	EstimatedTokensAfter *int64 `json:"estimatedTokensAfter,omitempty"`
	// Usage is the usage from the LLM call(s) that generated this summary,
	// if available.
	Usage *types.Usage `json:"usage,omitempty"`
	// Details is extension-specific data (e.g. ArtifactIndex, version
	// markers for structured compaction).
	Details any `json:"details,omitempty"`
}

// combineUsage sums two usage records (pi: combineUsage; byte-identical
// twin delegated to the harness implementation, see header).
var combineUsage = compaction.CombineUsage

// ============================================================================
// Types
// ============================================================================

// CompactionSettingsValues already names the resolved compaction settings
// shape (pi: CompactionSettings in core/compaction/compaction.ts; the
// settings-manager twin's optional shape claimed the plain
// CompactionSettings name in pig, so the resolved shape keeps its
// Values-suffixed name here).

// DefaultCompactionSettings are the default compaction settings (pi:
// DEFAULT_COMPACTION_SETTINGS).
var DefaultCompactionSettings = CompactionSettingsValues{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// ============================================================================
// Token calculation
// ============================================================================

// CalculateContextTokens calculates total context tokens from usage; uses
// the native totalTokens field when available, falls back to computing from
// components (pi: calculateContextTokens; identical twin delegated to the
// harness implementation, see header).
func CalculateContextTokens(usage *types.Usage) int64 {
	return compaction.CalculateContextTokens(usage)
}

// getAssistantUsage returns the usage from an assistant message if available.
// Skips aborted, error, and all-zero usage messages as they don't have valid
// usage data (pi: getAssistantUsage).
func getAssistantUsage(msg agent.AgentMessage) *types.Usage {
	if assistantMsg, ok := msg.(types.AssistantMessage); ok {
		if assistantMsg.StopReason != types.StopReasonAborted &&
			assistantMsg.StopReason != types.StopReasonError &&
			CalculateContextTokens(&assistantMsg.Usage) > 0 {
			return &assistantMsg.Usage
		}
	}
	return nil
}

// GetLastAssistantUsage finds the last valid assistant message usage from
// session entries (pi: getLastAssistantUsage).
func GetLastAssistantUsage(entries []SessionEntry) *types.Usage {
	for i := len(entries) - 1; i >= 0; i-- {
		if entry, ok := entries[i].(*SessionMessageEntry); ok {
			if usage := getAssistantUsage(entry.Message); usage != nil {
				return usage
			}
		}
	}
	return nil
}

// ContextUsageEstimate is the result of EstimateContextTokens (pi:
// ContextUsageEstimate; identical shape delegated to the harness type, see
// header).
type ContextUsageEstimate = compaction.ContextUsageEstimate

// EstimateContextTokens estimates context tokens from messages, using the
// last assistant usage when available; messages after the last usage are
// estimated with EstimateTokens (pi: estimateContextTokens; identical twin
// delegated to the harness implementation, see header).
func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	return compaction.EstimateContextTokens(messages)
}

// ShouldCompact checks if compaction should trigger based on context usage
// (pi: shouldCompact; identical twin reimplemented over the resolved
// settings shape).
func ShouldCompact(contextTokens int64, contextWindow int64, settings CompactionSettingsValues) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// ============================================================================
// Cut point detection
// ============================================================================

// EstimateTokens estimates the token count for a message using the chars/4
// heuristic; conservative (overestimates tokens) (pi: estimateTokens;
// identical twin delegated to the harness implementation, see header).
func EstimateTokens(message agent.AgentMessage) int64 {
	return compaction.EstimateTokens(message)
}

// isCutPointMessage reports whether a context message may host a cut (pi:
// isCutPointMessage). Never cut at tool results — they must follow their
// tool call.
func isCutPointMessage(message agent.AgentMessage) bool {
	switch message.MessageRole() {
	case types.RoleUser,
		types.RoleAssistant,
		harness.RoleBashExecution,
		harness.RoleCustomMessage,
		harness.RoleBranchSummary,
		harness.RoleCompactionSummary:
		return true
	case types.RoleToolResult:
		return false
	}
	return false
}

// isTurnStartMessage reports whether a context message starts a turn (pi:
// isTurnStartMessage).
func isTurnStartMessage(message agent.AgentMessage) bool {
	switch message.MessageRole() {
	case types.RoleUser,
		harness.RoleBashExecution,
		harness.RoleCustomMessage,
		harness.RoleBranchSummary,
		harness.RoleCompactionSummary:
		return true
	case types.RoleAssistant, types.RoleToolResult:
		return false
	}
	return false
}

// isTurnStartEntry reports whether any context message of the entry starts a
// turn (pi: isTurnStartEntry).
func isTurnStartEntry(entry SessionEntry) bool {
	if _, ok := entry.(*CompactionEntry); ok {
		return false
	}
	for _, message := range SessionEntryToContextMessages(entry) {
		if isTurnStartMessage(message) {
			return true
		}
	}
	return false
}

// findValidCutPoints collects the indices of context-visible user-like or
// assistant messages: never cut at tool results (they must follow their tool
// call). When we cut at an assistant message with tool calls, its tool
// results follow it and will be kept (pi: findValidCutPoints).
func findValidCutPoints(entries []SessionEntry, startIndex int, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if _, ok := entry.(*CompactionEntry); ok {
			continue
		}
		for _, message := range SessionEntryToContextMessages(entry) {
			if isCutPointMessage(message) {
				cutPoints = append(cutPoints, i)
				break
			}
		}
	}
	return cutPoints
}

// FindTurnStartIndex finds the context-visible user-role message that starts
// the turn containing the given entry index; returns -1 if no turn start
// found before the index (pi: findTurnStartIndex).
func FindTurnStartIndex(entries []SessionEntry, entryIndex int, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isTurnStartEntry(entries[i]) {
			return i
		}
	}
	return -1
}

// CutPointResult is the outcome of FindCutPoint (pi: CutPointResult).
type CutPointResult struct {
	// FirstKeptEntryIndex is the index of first entry to keep.
	FirstKeptEntryIndex int
	// TurnStartIndex is the index of the user message that starts the turn
	// being split, or -1 if not splitting.
	TurnStartIndex int
	// IsSplitTurn reports whether this cut splits a turn (cut point is not a
	// user message).
	IsSplitTurn bool
}

// FindCutPoint finds the cut point in session entries that keeps
// approximately keepRecentTokens.
//
// Algorithm: walk backwards from newest, accumulating estimated message
// sizes. Stop when we've accumulated >= keepRecentTokens. Cut at that point.
//
// Can cut at user OR assistant messages (never tool results). When cutting
// at an assistant message with tool calls, its tool results come after and
// will be kept.
//
// Only considers entries between startIndex and endIndex (exclusive) (pi:
// findCutPoint).
func FindCutPoint(entries []SessionEntry, startIndex int, endIndex int, keepRecentTokens int64) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)

	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1, IsSplitTurn: false}
	}

	// Walk backwards from newest, accumulating estimated message sizes
	accumulatedTokens := int64(0)
	cutIndex := cutPoints[0] // Default: keep from first message (not header)

	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		messageTokens := int64(0)
		for _, message := range SessionEntryToContextMessages(entry) {
			messageTokens += EstimateTokens(message)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens

		// Check if we've exceeded the budget
		if accumulatedTokens >= keepRecentTokens {
			// Find the closest valid cut point at or after this entry
			for _, c := range cutPoints {
				if c >= i {
					cutIndex = c
					break
				}
			}
			break
		}
	}

	// Scan backwards from cutIndex to include adjacent metadata entries that
	// do not affect context. Stop at compaction boundaries or context-visible
	// entries.
	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if _, ok := prevEntry.(*CompactionEntry); ok || len(SessionEntryToContextMessages(prevEntry)) > 0 {
			break
		}
		cutIndex--
	}

	// Determine if this is a split turn
	cutEntry := entries[cutIndex]
	startsTurn := isTurnStartEntry(cutEntry)
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}

	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

// ============================================================================
// Summarization
// ============================================================================

// summarizationPrompt is the initial summarization request prompt (pi:
// SUMMARIZATION_PROMPT).
const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// updateSummarizationInstructions are the merge rules for iterative summary
// updates (pi: UPDATE_SUMMARIZATION_INSTRUCTIONS).
const updateSummarizationInstructions = `Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// updateSummarizationPrompt wraps the merge rules for the update request (pi:
// UPDATE_SUMMARIZATION_PROMPT).
const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

` + updateSummarizationInstructions

// GetSummarizationFailure returns an error message when a summarization
// response cannot safely be persisted (pi: getSummarizationFailure,
// v0.85.1). A length stop contains partial text and must not become a
// session checkpoint; "" = no failure.
func GetSummarizationFailure(response *types.AssistantMessage, label string) string {
	if response.StopReason == types.StopReasonError {
		errorMessage := response.ErrorMessage
		if errorMessage == "" {
			errorMessage = "Unknown error"
		}
		return label + " failed: " + errorMessage
	}
	if response.StopReason == types.StopReasonLength {
		return label + " failed: generation hit the token cap and the summary is incomplete"
	}
	return ""
}

// createSummarizationOptions builds the stream options for one summarization
// request (pi: createSummarizationOptions; the signal member is dropped —
// cancellation travels as ctx).
func createSummarizationOptions(
	model *ai.Model,
	maxTokens int64,
	apiKey string,
	headers map[string]string,
	env ai.ProviderEnv,
	thinkingLevel types.ThinkingLevel,
	sessionID string,
) *ai.SimpleStreamOptions {
	options := &ai.SimpleStreamOptions{}
	options.MaxTokens = &maxTokens
	options.APIKey = apiKey
	options.Env = env
	options.SessionID = sessionID
	if headers != nil {
		options.Headers = ai.ProviderHeaders{}
		for k, v := range headers {
			header := v
			options.Headers[k] = &header
		}
	}
	if model.Reasoning && thinkingLevel != "" && thinkingLevel != types.ThinkingLevel("off") {
		options.Reasoning = thinkingLevel
	}
	return options
}

// completeSimpleFallback is the no-streamFn default of CompleteSummarization
// (pi: completeSimple from pi-ai/compat; mocked directly in tests via this
// seam, see header for the deviation scope).
var completeSimpleFallback = func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) (*types.AssistantMessage, error) {
	streams, ok := ai.GetApiProvider(model.API)
	if !ok {
		return nil, fmt.Errorf("No API provider registered for api: %s", model.API)
	}
	result := streams.StreamSimple(ctx, model, llmContext, options).Result()
	return &result, nil
}

// CompleteSummarization is the shared choke point for every
// compaction/branch-summary summarization call. It wraps the single LLM call
// in RetryAssistantCall so transient stream drops (e.g. `terminated`, socket
// close) honor the configured retry policy instead of failing the whole
// compaction on the first attempt. Deterministic errors and aborts return
// immediately (pi: completeSummarization).
func CompleteSummarization(
	ctx context.Context,
	model *ai.Model,
	llmContext *ai.Context,
	options *ai.SimpleStreamOptions,
	streamFn agent.StreamFn,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*types.AssistantMessage, error) {
	// Avoid cache writes for one-off summaries. Reuse caller-supplied routing
	// when available; callers without a session ID, including branch
	// summaries, receive a fresh routing ID.
	requestOptions := ai.SimpleStreamOptions{}
	if options != nil {
		requestOptions = *options
	}
	requestOptions.CacheRetention = types.CacheRetentionNone
	if requestOptions.SessionID == "" {
		requestOptions.SessionID = ai.UUIDv7()
	}
	// pi (v0.85.1): toolChoice:"none" dropped from the summarization request
	// options.
	produce := func() (*types.AssistantMessage, error) {
		if streamFn != nil {
			result := streamFn(ctx, model, llmContext, &requestOptions).Result()
			return &result, nil
		}
		return completeSimpleFallback(ctx, model, llmContext, &requestOptions)
	}
	return ai.RetryAssistantCall(ctx, produce, retry, callbacks)
}

// GenerateSummary generates a summary of the conversation using the LLM; if
// previousSummary is provided, uses the update prompt to merge (pi:
// generateSummary). Empty strings mean pi's undefined optional arguments.
func GenerateSummary(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int64,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel types.ThinkingLevel,
	streamFn agent.StreamFn,
	env ai.ProviderEnv,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
	sessionID string,
) (string, error) {
	result, err := GenerateSummaryWithUsage(
		ctx,
		currentMessages,
		model,
		reserveTokens,
		apiKey,
		headers,
		customInstructions,
		previousSummary,
		thinkingLevel,
		streamFn,
		env,
		retry,
		callbacks,
		sessionID,
	)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// buildSummarizationContext builds the provider context for a standalone
// summary request (pi: buildSummarizationContext).
func buildSummarizationContext(promptText string) *ai.Context {
	return &ai.Context{
		SystemPrompt: SummarizationSystemPrompt,
		Messages: []types.Message{
			types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(promptText)}},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}
}

// SummaryWithUsage is a generated summary plus its provider usage (the
// inline `{ text, usage }` return of pi's generateSummaryWithUsage).
type SummaryWithUsage struct {
	Text  string
	Usage *types.Usage
}

// GenerateSummaryWithUsage generates or updates a conversation summary and
// returns its provider usage (pi: generateSummaryWithUsage; parameter
// ordering follows pi with ctx first — pi's signal member is dropped).
func GenerateSummaryWithUsage(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int64,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel types.ThinkingLevel,
	streamFn agent.StreamFn,
	env ai.ProviderEnv,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
	sessionID string,
) (*SummaryWithUsage, error) {
	maxTokens := int64(math.Floor(0.8 * float64(reserveTokens)))
	if model.MaxTokens > 0 && model.MaxTokens < maxTokens {
		maxTokens = model.MaxTokens
	}

	// Use update prompt if we have a previous summary, otherwise initial prompt
	basePrompt := summarizationPrompt
	if previousSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	if customInstructions != "" {
		basePrompt = fmt.Sprintf("%s\n\nAdditional focus: %s", basePrompt, customInstructions)
	}

	// Serialize conversation to text so model doesn't try to continue it
	// Convert to LLM messages first (handles custom types like bashExecution,
	// custom, etc.)
	llmMessages := harness.ConvertToLlm(currentMessages)
	conversationText := SerializeConversation(llmMessages)

	// Build the prompt with conversation wrapped in tags
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n", conversationText)
	if previousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\n%s\n</previous-summary>\n\n", previousSummary)
	}
	promptText += basePrompt

	completionOptions := createSummarizationOptions(
		model,
		maxTokens,
		apiKey,
		headers,
		env,
		thinkingLevel,
		sessionID,
	)

	response, err := CompleteSummarization(
		ctx,
		model,
		buildSummarizationContext(promptText),
		completionOptions,
		streamFn,
		retry,
		callbacks,
	)
	if err != nil {
		return nil, err
	}

	// pi (v0.85.1): a length stop contains partial text and must not become
	// a session checkpoint, so it fails like an error.
	if failure := GetSummarizationFailure(response, "Summarization"); failure != "" {
		return nil, errors.New(failure)
	}
	for _, block := range response.Content {
		if block.ContentKind() == types.KindToolCall {
			return nil, errors.New("Summarization attempted to call a tool")
		}
	}

	return &SummaryWithUsage{Text: ai.ContentText(response.Content, ""), Usage: &response.Usage}, nil
}

// ============================================================================
// Compaction Preparation (for extensions)
// ============================================================================

// CompactionPreparation is the pre-calculated compaction input consumed by
// Compact and exposed to the session_before_compact extension event (pi:
// CompactionPreparation).
type CompactionPreparation struct {
	// FirstKeptEntryID is the UUID of first entry to keep.
	FirstKeptEntryID string
	// MessagesToSummarize are the messages that will be summarized and
	// discarded.
	MessagesToSummarize []agent.AgentMessage
	// TurnPrefixMessages are the messages that will be turned into turn
	// prefix summary (if splitting).
	TurnPrefixMessages []agent.AgentMessage
	// IsSplitTurn reports whether this is a split turn (cut point in middle
	// of turn).
	IsSplitTurn  bool
	TokensBefore int64
	// PreviousSummary is the summary from previous compaction, for iterative
	// update; "" = none (pi: previousSummary?).
	PreviousSummary string
	// FileOps are the file operations extracted from messagesToSummarize.
	FileOps *FileOperations
	// Settings are the compaction settings from settings.json (pi: settings;
	// comment's "settings.jsonl" typo follows pi).
	Settings CompactionSettingsValues
}

// PrepareCompaction computes the compaction preparation for a session branch;
// nil means nothing to compact (pi: prepareCompaction).
func PrepareCompaction(pathEntries []SessionEntry, settings CompactionSettingsValues) *CompactionPreparation {
	if len(pathEntries) > 0 {
		if _, ok := pathEntries[len(pathEntries)-1].(*CompactionEntry); ok {
			return nil
		}
	}

	prevCompactionIndex := -1
	for i := len(pathEntries) - 1; i >= 0; i-- {
		if _, ok := pathEntries[i].(*CompactionEntry); ok {
			prevCompactionIndex = i
			break
		}
	}

	var previousSummary string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		prevCompaction := pathEntries[prevCompactionIndex].(*CompactionEntry)
		previousSummary = prevCompaction.Summary
		firstKeptEntryIndex := -1
		for i, entry := range pathEntries {
			if entry.Base().ID == prevCompaction.FirstKeptEntryID {
				firstKeptEntryIndex = i
				break
			}
		}
		if firstKeptEntryIndex >= 0 {
			boundaryStart = firstKeptEntryIndex
		} else {
			boundaryStart = prevCompactionIndex + 1
		}
	}
	boundaryEnd := len(pathEntries)

	tokensBefore := EstimateContextTokens(BuildSessionContext(pathEntries, nil, nil).Messages).Tokens

	cutPoint := FindCutPoint(pathEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)

	// Get UUID of first kept entry
	firstKeptEntry := pathEntries[cutPoint.FirstKeptEntryIndex]
	if firstKeptEntry.Base().ID == "" {
		return nil // Session needs migration
	}
	firstKeptEntryID := firstKeptEntry.Base().ID

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}

	// Messages to summarize (will be discarded after summary)
	var messagesToSummarize []agent.AgentMessage
	for i := boundaryStart; i < historyEnd; i++ {
		if msg := getMessageFromEntryForCompaction(pathEntries[i]); msg != nil {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}

	// Messages for turn prefix summary (if splitting a turn)
	var turnPrefixMessages []agent.AgentMessage
	if cutPoint.IsSplitTurn {
		for i := cutPoint.TurnStartIndex; i < cutPoint.FirstKeptEntryIndex; i++ {
			if msg := getMessageFromEntryForCompaction(pathEntries[i]); msg != nil {
				turnPrefixMessages = append(turnPrefixMessages, msg)
			}
		}
	}

	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return nil
	}

	// Extract file operations from messages and previous compaction
	fileOps := extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)

	// Also extract file ops from turn prefix if splitting
	if cutPoint.IsSplitTurn {
		for _, msg := range turnPrefixMessages {
			ExtractFileOpsFromMessage(msg, fileOps)
		}
	}

	return &CompactionPreparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}
}

// ============================================================================
// Main compaction function
// ============================================================================

// turnPrefixSummarizationPrompt summarizes the kept-prefix of an oversized
// turn (pi: TURN_PREFIX_SUMMARIZATION_PROMPT).
const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

// Compact generates summaries for compaction using prepared data; the
// returned CompactionResult is persisted by SessionManager, which adds
// uuid/parentUuid when saving (pi: compact; pi's signal member is dropped —
// cancellation travels as ctx).
//
// sessionId is the optional routing session ID forwarded without enabling
// prompt caching.
func Compact(
	ctx context.Context,
	preparation *CompactionPreparation,
	model *ai.Model,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	thinkingLevel types.ThinkingLevel,
	streamFn agent.StreamFn,
	env ai.ProviderEnv,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
	sessionID string,
) (*CompactionResult, error) {
	// Generate summaries and merge into one
	var summary string
	var summaryUsage *types.Usage

	if preparation.IsSplitTurn && len(preparation.TurnPrefixMessages) > 0 {
		historyText := "No prior history."
		var historyUsage *types.Usage
		if len(preparation.MessagesToSummarize) > 0 {
			historyResult, err := GenerateSummaryWithUsage(
				ctx,
				preparation.MessagesToSummarize,
				model,
				preparation.Settings.ReserveTokens,
				apiKey,
				headers,
				customInstructions,
				preparation.PreviousSummary,
				thinkingLevel,
				streamFn,
				env,
				retry,
				callbacks,
				sessionID,
			)
			if err != nil {
				return nil, err
			}
			historyText = historyResult.Text
			historyUsage = historyResult.Usage
		}
		turnPrefixResult, err := generateTurnPrefixSummary(
			ctx,
			preparation.TurnPrefixMessages,
			model,
			preparation.Settings.ReserveTokens,
			apiKey,
			headers,
			env,
			thinkingLevel,
			streamFn,
			retry,
			callbacks,
			sessionID,
		)
		if err != nil {
			return nil, err
		}
		// Merge into single summary
		summary = fmt.Sprintf("%s\n\n---\n\n**Turn Context (split turn):**\n\n%s", historyText, turnPrefixResult.Text)
		if historyUsage != nil {
			summaryUsage = combineUsage(historyUsage, turnPrefixResult.Usage)
		} else {
			summaryUsage = turnPrefixResult.Usage
		}
	} else {
		// Just generate history summary
		result, err := GenerateSummaryWithUsage(
			ctx,
			preparation.MessagesToSummarize,
			model,
			preparation.Settings.ReserveTokens,
			apiKey,
			headers,
			customInstructions,
			preparation.PreviousSummary,
			thinkingLevel,
			streamFn,
			env,
			retry,
			callbacks,
			sessionID,
		)
		if err != nil {
			return nil, err
		}
		summary = result.Text
		summaryUsage = result.Usage
	}

	// Compute file lists and append to summary
	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if preparation.FirstKeptEntryID == "" {
		return nil, errors.New("First kept entry has no UUID - session may need migration")
	}

	return &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: preparation.FirstKeptEntryID,
		TokensBefore:     preparation.TokensBefore,
		Usage:            summaryUsage,
		Details: CompactionDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		},
	}, nil
}

// generateTurnPrefixSummary generates a summary for a turn prefix (when
// splitting a turn) (pi: generateTurnPrefixSummary).
func generateTurnPrefixSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int64,
	apiKey string,
	headers map[string]string,
	env ai.ProviderEnv,
	thinkingLevel types.ThinkingLevel,
	streamFn agent.StreamFn,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
	sessionID string,
) (*SummaryWithUsage, error) {
	maxTokens := int64(math.Floor(0.5 * float64(reserveTokens)))
	if model.MaxTokens > 0 && model.MaxTokens < maxTokens {
		maxTokens = model.MaxTokens
	} // Smaller budget for turn prefix
	llmMessages := harness.ConvertToLlm(messages)
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, turnPrefixSummarizationPrompt)

	response, err := CompleteSummarization(
		ctx,
		model,
		buildSummarizationContext(promptText),
		createSummarizationOptions(model, maxTokens, apiKey, headers, env, thinkingLevel, sessionID),
		streamFn,
		retry,
		callbacks,
	)
	if err != nil {
		return nil, err
	}

	if failure := GetSummarizationFailure(response, "Turn prefix summarization"); failure != "" {
		return nil, errors.New(failure)
	}
	for _, block := range response.Content {
		if block.ContentKind() == types.KindToolCall {
			return nil, errors.New("Turn prefix summarization attempted to call a tool")
		}
	}

	return &SummaryWithUsage{Text: ai.ContentText(response.Content, ""), Usage: &response.Usage}, nil
}
