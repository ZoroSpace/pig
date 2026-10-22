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
//   - pi v0.87.0 (466db0fec) added a system-message branch to THIS file's
//     estimateTokens only (replayed prompt content, sections, toolsAdded
//     JSON); the agent-harness twin stayed untouched, so the two TS copies
//     are no longer byte-identical. pig keeps the shared twin and adds the
//     branch in the core EstimateTokens wrapper below: the projection
//     cut-point walk and the EstimateProjectedContextTokens fallback see
//     system-message tokens, while the harness-delegated
//     EstimateContextTokens keeps counting trailing system messages as 0
//     (its callers are usage-anchored; agent-session swaps to the projected
//     estimator with the S8-4 batch).
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	compaction "github.com/ZoroSpace/pig/agent/harness/compaction"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
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

// getMessagesFromProjectedEntryForCompaction extracts the model-visible
// messages of one projected entry for summarization (pi:
// getMessagesFromProjectedEntryForCompaction; v0.87.0 replaced the
// single-message getMessageFromEntryForCompaction). Compaction entries
// contribute nothing, and system messages are prompt state, not
// conversation — the compaction entry carries their replay.
func getMessagesFromProjectedEntryForCompaction(entry ProjectedSessionEntry) []agent.AgentMessage {
	if _, ok := entry.SourceEntry.(*CompactionEntry); ok {
		return nil
	}
	messages := make([]agent.AgentMessage, 0, len(entry.Messages))
	for _, message := range entry.Messages {
		if message.MessageRole() != types.RoleSystem {
			messages = append(messages, message)
		}
	}
	return messages
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

// EstimateProjectedContextTokens estimates the projected context without
// trusting usage captured before a later edit or compaction (pi:
// estimateProjectedContextTokens; v0.87.0). When the message carrying the
// last assistant usage is followed — along the raw branch — by a
// context_edit or compaction entry, the usage no longer describes the
// projected context and every message falls back to the chars/4 estimate,
// with the replayed system message replacing the stored system entries.
func EstimateProjectedContextTokens(projection SessionProjection, branchEntries []SessionEntry) ContextUsageEstimate {
	estimate := EstimateContextTokens(projection.Messages)
	if estimate.LastUsageIndex != nil {
		projectedMessageIndex := 0
		var usageEntryID string
		for _, entry := range projection.Entries {
			nextMessageIndex := projectedMessageIndex + len(entry.Messages)
			if *estimate.LastUsageIndex < nextMessageIndex {
				usageEntryID = entry.SourceEntry.Base().ID
				break
			}
			projectedMessageIndex = nextMessageIndex
		}

		usageEntryIndex := -1
		if usageEntryID != "" {
			for i, entry := range branchEntries {
				if entry.Base().ID == usageEntryID {
					usageEntryIndex = i
					break
				}
			}
		}
		latestInvalidatingEntryIndex := -1
		for i := len(branchEntries) - 1; i >= 0; i-- {
			switch branchEntries[i].(type) {
			case *ContextEditEntry, *CompactionEntry:
				latestInvalidatingEntryIndex = i
			}
			if latestInvalidatingEntryIndex >= 0 {
				break
			}
		}
		if usageEntryIndex > latestInvalidatingEntryIndex {
			return estimate
		}
	}

	tokens := int64(0)
	if currentSystem := aiutils.GetCurrentSystemMessage(agentMessagesAsTypesMessages(projection.Messages)); currentSystem != nil {
		tokens += EstimateTokens(*currentSystem)
	}
	for _, message := range projection.Messages {
		if message.MessageRole() != types.RoleSystem {
			tokens += EstimateTokens(message)
		}
	}
	return ContextUsageEstimate{Tokens: tokens, UsageTokens: 0, TrailingTokens: tokens, LastUsageIndex: nil}
}

// agentMessagesAsTypesMessages adapts an agent message list to the
// transcript-helper input (pig-only: TS has one array type).
func agentMessagesAsTypesMessages(messages []agent.AgentMessage) []types.Message {
	out := make([]types.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, message)
	}
	return out
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

// estimatedImageChars is the char budget charged per image block (pi:
// ESTIMATED_IMAGE_CHARS; core copy for the v0.87.0 system branch).
const estimatedImageChars = 4800

// EstimateTokens estimates the token count for a message using the chars/4
// heuristic; conservative (overestimates tokens) (pi: estimateTokens).
//
// The v0.87.0 system-message branch lives in this core wrapper — see the
// package header for why the harness twin stays untouched.
func EstimateTokens(message agent.AgentMessage) int64 {
	switch system := message.(type) {
	case types.SystemMessage:
		return estimateSystemTokens(system)
	case *types.SystemMessage:
		if system != nil {
			return estimateSystemTokens(*system)
		}
	}
	return compaction.EstimateTokens(message)
}

// estimateSystemTokens implements the v0.87.0 system branch of pi's
// estimateTokens: content chars, one length per non-nil section value, and
// the JSON length of toolsAdded (pi: the `case "system"` block; Object.values
// skips holes, pig skips nil section values; JSON.stringify cannot fail for
// a []Tool, and a marshal error contributes 0 chars).
func estimateSystemTokens(system types.SystemMessage) int64 {
	chars := int64(0)
	if system.Content.Parts != nil {
		for _, block := range system.Content.Parts {
			switch b := block.(type) {
			case types.TextContent:
				chars += int64(len(b.Text))
			case types.ImageContent:
				chars += int64(estimatedImageChars)
			}
		}
	} else {
		chars = int64(len(system.Content.String))
	}
	if system.Sections != nil {
		for _, name := range system.Sections.Keys {
			if section := system.Sections.Values[name]; section != nil {
				chars += int64(len(*section))
			}
		}
	}
	if system.ToolsAdded != nil {
		if encoded, err := json.Marshal(system.ToolsAdded); err == nil {
			chars += int64(len(encoded))
		}
	}
	return (chars + 3) / 4
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
			// pi v0.86.0 (#9740): prefer the closest valid cut point at or
			// after this entry. If trailing tool results exceed the budget by
			// themselves, keep their preceding assistant tool call instead of
			// falling back to the first message.
			cutIndex = cutPoints[0]
			found := false
			for _, c := range cutPoints {
				if c >= i {
					cutIndex = c
					found = true
					break
				}
			}
			if !found {
				cutIndex = cutPoints[len(cutPoints)-1]
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
	apiKey string,
	headers map[string]string,
	env ai.ProviderEnv,
	thinkingLevel types.ThinkingLevel,
	sessionID string,
) *ai.SimpleStreamOptions {
	options := &ai.SimpleStreamOptions{}
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
	result := streams.StreamSimple(ctx, model, aiutils.NormalizeContext(llmContext), options).Result()
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
			result := streamFn(ctx, model, aiutils.NormalizeContext(llmContext), &requestOptions).Result()
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
	// Unused. Kept for existing callers; summaries use the model's output limit (#9512).
	_reserveTokens int64,
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
		_reserveTokens,
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
	// Unused. Kept for existing callers; summaries use the model's output limit (#9512).
	_reserveTokens int64,
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

// isProjectedTurnStart reports whether a projected entry starts a turn (pi:
// isProjectedTurnStart).
func isProjectedTurnStart(entry ProjectedSessionEntry) bool {
	if _, ok := entry.SourceEntry.(*CompactionEntry); ok {
		return false
	}
	for _, message := range entry.Messages {
		if isTurnStartMessage(message) {
			return true
		}
	}
	return false
}

// findProjectedTurnStartIndex finds the projected turn start at or before
// entryIndex, not older than startIndex; -1 when none (pi:
// findProjectedTurnStartIndex).
func findProjectedTurnStartIndex(entries []ProjectedSessionEntry, entryIndex int, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isProjectedTurnStart(entries[i]) {
			return i
		}
	}
	return -1
}

// findProjectedCutPoint is the projection-based cut-point search (pi:
// findProjectedCutPoint; v0.87.0). Beyond the raw-entry budget walk it
// carries the recovery-omission-suffix rule (pi docs/compaction.md
// "Overflow and Length Recovery Ordering"): preparation advances the kept
// boundary into a context-invisible suffix only when that suffix contains
// an omitted assistant attempt and no unomitted context-producing entries;
// metadata alone and newly appended custom messages do not move the cut,
// and a replacement edit affecting the candidate input blocks advancement
// because the omitted assistant answered the pre-edit input. The
// orchestration that appends those omission edits (persist → turn_end →
// agent_end → context_edit omissions → session_before_compact + compaction
// → fresh retry) lives in agent-session and lands with the S8-4 batch.
func findProjectedCutPoint(entries []ProjectedSessionEntry, startIndex int, endIndex int, keepRecentTokens int64) CutPointResult {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		if _, isCompaction := entry.SourceEntry.(*CompactionEntry); !isCompaction {
			for _, message := range entry.Messages {
				if isCutPointMessage(message) {
					cutPoints = append(cutPoints, i)
					break
				}
			}
		}
	}
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1, IsSplitTurn: false}
	}

	// Walk backwards from newest, accumulating estimated message sizes
	accumulatedTokens := int64(0)
	exceededBudget := false
	cutIndex := cutPoints[0] // Default: keep from first message (not header)

	for i := endIndex - 1; i >= startIndex; i-- {
		messageTokens := int64(0)
		for _, message := range entries[i].Messages {
			messageTokens += EstimateTokens(message)
		}
		if messageTokens == 0 {
			continue
		}
		accumulatedTokens += messageTokens

		// Check if we've exceeded the budget. Prefer the closest valid cut
		// point at or after this entry (pi #9740).
		if accumulatedTokens >= keepRecentTokens {
			exceededBudget = true
			cutIndex = cutPoints[0]
			found := false
			for _, candidate := range cutPoints {
				if candidate >= i {
					cutIndex = candidate
					found = true
					break
				}
			}
			if !found {
				cutIndex = cutPoints[len(cutPoints)-1]
			}
			break
		}
	}

	// A recovery attempt and its omission edits are context-invisible after
	// the last visible input. Advance only for a closed suffix containing an
	// omitted assistant attempt; arbitrary metadata must not move the cut
	// past unsent input.
	suffix := entries[cutIndex+1 : endIndex]
	isIntrinsicallyVisible := func(entry ProjectedSessionEntry) bool {
		if _, isEdit := entry.SourceEntry.(*ContextEditEntry); isEdit {
			return false
		}
		return len(SessionEntryToContextMessages(entry.SourceEntry)) > 0
	}
	isOmitted := func(entry ProjectedSessionEntry) bool {
		return isIntrinsicallyVisible(entry) && len(entry.Messages) == 0
	}
	omittedSuffixIDs := make(map[string]bool)
	for _, entry := range suffix {
		if isOmitted(entry) {
			omittedSuffixIDs[entry.SourceEntry.Base().ID] = true
		}
	}
	hasExternalReplacement := false
	for _, entry := range suffix {
		if edit, ok := entry.SourceEntry.(*ContextEditEntry); ok && edit.Replacement != nil && !omittedSuffixIDs[edit.TargetID] {
			hasExternalReplacement = true
			break
		}
	}
	if exceededBudget && !hasExternalReplacement {
		hasOmittedAssistant := false
		allClosed := true
		for _, entry := range suffix {
			if msg, ok := entry.SourceEntry.(*SessionMessageEntry); ok && msg.Message.MessageRole() == types.RoleAssistant && isOmitted(entry) {
				hasOmittedAssistant = true
			}
			_, isCompaction := entry.SourceEntry.(*CompactionEntry)
			if isCompaction || (isIntrinsicallyVisible(entry) && !isOmitted(entry)) {
				allClosed = false
				break
			}
		}
		if hasOmittedAssistant && allClosed {
			cutIndex++
		}
	}

	// Scan backwards from cutIndex to include adjacent metadata entries that
	// do not affect the projected context. Stop at compaction boundaries or
	// context-visible entries.
	for cutIndex > startIndex {
		previous := entries[cutIndex-1]
		if _, isCompaction := previous.SourceEntry.(*CompactionEntry); isCompaction || len(previous.Messages) > 0 {
			break
		}
		cutIndex--
	}

	// Determine if this is a split turn
	startsTurn := isProjectedTurnStart(entries[cutIndex])
	turnStartIndex := -1
	if !startsTurn {
		turnStartIndex = findProjectedTurnStartIndex(entries, cutIndex, startIndex)
	}

	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !startsTurn && turnStartIndex != -1,
	}
}

// PrepareCompaction computes the compaction preparation for a session branch;
// nil means nothing to compact (pi: prepareCompaction; v0.87.0 projection
// rewrite).
func PrepareCompaction(pathEntries []SessionEntry, settings CompactionSettingsValues) *CompactionPreparation {
	if len(pathEntries) > 0 {
		if _, ok := pathEntries[len(pathEntries)-1].(*CompactionEntry); ok {
			return nil
		}
	}

	projection := BuildSessionProjection(pathEntries, nil, nil)
	projectedEntries := projection.Entries
	sourceEntries := make([]SessionEntry, len(projectedEntries))
	for i, entry := range projectedEntries {
		sourceEntries[i] = entry.SourceEntry
	}
	// The newest compaction is projected first. Older compaction entries can
	// still occur in its retained raw range, but their projected contribution
	// is empty.
	prevCompactionIndex := -1
	for i, entry := range projectedEntries {
		if _, ok := entry.SourceEntry.(*CompactionEntry); ok && len(entry.Messages) > 0 {
			prevCompactionIndex = i
			break
		}
	}

	var previousSummary string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		previousSummary = projectedEntries[prevCompactionIndex].SourceEntry.(*CompactionEntry).Summary
		// The canonical projection has already selected the previous
		// compaction's retained tail.
		boundaryStart = prevCompactionIndex + 1
	}
	boundaryEnd := len(projectedEntries)

	tokensBefore := EstimateProjectedContextTokens(projection, pathEntries).Tokens

	cutPoint := findProjectedCutPoint(projectedEntries, boundaryStart, boundaryEnd, settings.KeepRecentTokens)

	// Get UUID of first kept entry
	var firstKeptEntry SessionEntry
	if cutPoint.FirstKeptEntryIndex < len(projectedEntries) {
		firstKeptEntry = projectedEntries[cutPoint.FirstKeptEntryIndex].SourceEntry
	}
	if firstKeptEntry == nil || firstKeptEntry.Base().ID == "" {
		return nil // Session needs migration
	}
	firstKeptEntryID := firstKeptEntry.Base().ID

	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}

	// Messages to summarize (will be discarded after summary)
	var messagesToSummarize []agent.AgentMessage
	for _, entry := range projectedEntries[boundaryStart:historyEnd] {
		messagesToSummarize = append(messagesToSummarize, getMessagesFromProjectedEntryForCompaction(entry)...)
	}

	// Messages for turn prefix summary (if splitting a turn)
	var turnPrefixMessages []agent.AgentMessage
	if cutPoint.IsSplitTurn {
		for _, entry := range projectedEntries[cutPoint.TurnStartIndex:cutPoint.FirstKeptEntryIndex] {
			turnPrefixMessages = append(turnPrefixMessages, getMessagesFromProjectedEntryForCompaction(entry)...)
		}
	}

	if len(messagesToSummarize) == 0 && len(turnPrefixMessages) == 0 {
		return nil
	}

	// Extract file operations from edited model-visible messages and the
	// previous compaction.
	fileOps := extractFileOperations(messagesToSummarize, sourceEntries, prevCompactionIndex)

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
// turn (pi: TURN_PREFIX_SUMMARIZATION_PROMPT; d192bd6dc reworded to
// continuation-oriented guidance to avoid Fable split-turn summary refusals,
// #9652/#9908).
const turnPrefixSummarizationPrompt = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`

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
		// pi v0.86.0: the prior compaction's summary seeds the history half,
		// so the merged summary carries the earlier summary forward even when
		// there are no new messages to summarize.
		historyText := preparation.PreviousSummary
		if historyText == "" {
			historyText = "No prior history."
		}
		var historyUsage *types.Usage
		if len(preparation.MessagesToSummarize) > 0 {
			historyResult, err := GenerateSummaryWithUsage(
				ctx,
				preparation.MessagesToSummarize,
				model,
				0,
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
			apiKey,
			headers,
			env,
			thinkingLevel,
			streamFn,
			retry,
			callbacks,
			sessionID,
			customInstructions,
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
			0,
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
	apiKey string,
	headers map[string]string,
	env ai.ProviderEnv,
	thinkingLevel types.ThinkingLevel,
	streamFn agent.StreamFn,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
	sessionID string,
	customInstructions string,
) (*SummaryWithUsage, error) {
	llmMessages := harness.ConvertToLlm(messages)
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("# Conversation\n%s\n\n# Instructions\n%s", conversationText, turnPrefixSummarizationPrompt)
	if customInstructions != "" {
		promptText = fmt.Sprintf("%s\n\nAdditional focus: %s", promptText, customInstructions)
	}

	response, err := CompleteSummarization(
		ctx,
		model,
		buildSummarizationContext(promptText),
		createSummarizationOptions(model, apiKey, headers, env, thinkingLevel, sessionID),
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
