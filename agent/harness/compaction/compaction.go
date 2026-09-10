// pi: src/harness/compaction/compaction.ts — context compaction: threshold
// checks, cut-point selection, summarization prompts, and summary generation.
//
// Deviations:
//   - TS Result<T, CompactionError> collapses to (T, error) with
//     *harness.CompactionError as the concrete error type; prepareCompaction's
//     `Result<preparation | undefined>` collapses to (*CompactionPreparation,
//     error) with nil preparation = not applicable.
//   - pi's optional parameters: customInstructions/previousSummary are
//     strings whose TS falsy check (`if (customInstructions)`) makes ""
//     behave exactly like undefined, so they stay plain strings;
//     thinkingLevel likewise ("" = not provided, matching the TS falsy
//     guard); retry/callbacks are nilable pointers; signal travels as the
//     leading context.Context.
//   - Usage in AssistantMessage is a value type in pig (pi: required Usage);
//     CompactResult.Usage stays a pointer (pi: usage?).
//   - estimateTokens' toolCall char estimate stringifies arguments with
//     encoding/json (sorted keys, "null" for nil) where TS used insertion
//     order/undefined; the estimate is heuristic and unaffected structurally.
package compaction

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// CompactionDetails holds file-operation details stored on generated
// compaction entries (pi: CompactionDetails).
type CompactionDetails struct {
	// ReadFiles lists files read in the compacted history.
	ReadFiles []string `json:"readFiles"`
	// ModifiedFiles lists files modified in the compacted history.
	ModifiedFiles []string `json:"modifiedFiles"`
}

// extractFileOperations merges the previous compaction's recorded details
// with tool-call operations found in messages (pi: extractFileOperations).
func extractFileOperations(messages []agent.AgentMessage, entries []session.Entry, prevCompactionIndex int) *FileOperations {
	fileOps := CreateFileOps()
	if prevCompactionIndex >= 0 {
		if prevCompaction, ok := entries[prevCompactionIndex].(*session.CompactionEntry); ok {
			for _, f := range detailStrings(prevCompaction.Details, "readFiles") {
				fileOps.Read[f] = true
			}
			for _, f := range detailStrings(prevCompaction.Details, "modifiedFiles") {
				fileOps.Edited[f] = true
			}
		}
	}
	for _, msg := range messages {
		ExtractFileOpsFromMessage(msg, fileOps)
	}
	return fileOps
}

// detailStrings reads a string array off entry details, accepting both the
// typed CompactionDetails/branch-summary shapes and their decoded JSON
// map form (pi: `details as CompactionDetails` + Array.isArray guards).
func detailStrings(details any, key string) []string {
	switch d := details.(type) {
	case CompactionDetails:
		if key == "readFiles" {
			return d.ReadFiles
		}
		if key == "modifiedFiles" {
			return d.ModifiedFiles
		}
		return nil
	case BranchSummaryDetails:
		if key == "readFiles" {
			return d.ReadFiles
		}
		if key == "modifiedFiles" {
			return d.ModifiedFiles
		}
		return nil
	case map[string]any:
		raw, ok := d[key].([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// getMessageFromEntry projects an entry to its context message (pi:
// getMessageFromEntry).
func getMessageFromEntry(entry session.Entry) agent.AgentMessage {
	switch e := entry.(type) {
	case *session.MessageEntry:
		return e.Message
	case *session.BranchSummaryEntry:
		return harness.CreateBranchSummaryMessage(e.Summary, e.FromID, e.Timestamp)
	case *session.CompactionEntry:
		return harness.CreateCompactionSummaryMessage(e.Summary, e.TokensBefore, e.Timestamp)
	default:
		return nil
	}
}

// getMessageFromEntryForCompaction projects an entry for compaction input,
// skipping prior compaction summaries (pi: getMessageFromEntryForCompaction).
func getMessageFromEntryForCompaction(entry session.Entry) agent.AgentMessage {
	if _, ok := entry.(*session.CompactionEntry); ok {
		return nil
	}
	return getMessageFromEntry(entry)
}

// CompactResult is generated compaction data ready to be persisted as a
// compaction entry (pi: CompactResult<T>; the details generic collapses to
// any, filled with CompactionDetails by Compact).
type CompactResult struct {
	// Summary is the summary text that replaces compacted history in future
	// context.
	Summary string
	// TokensBefore is the estimated context tokens before compaction.
	TokensBefore int64
	// Usage is the usage from the LLM call(s) that generated this summary,
	// when available.
	Usage *types.Usage
	// RetainedTail holds retained recent messages stored directly on the
	// compaction entry.
	RetainedTail []agent.AgentMessage
	// Details holds optional implementation-specific details stored with the
	// compaction entry.
	Details any
}

// CompleteSimpleWithRetries runs one standalone completion with the retry
// policy applied (pi: completeSimpleWithRetries). Summaries are standalone
// requests, so routing is isolated and cache writes that cannot be reused
// are avoided.
func CompleteSimpleWithRetries(
	ctx context.Context,
	models ai.Models,
	model *ai.Model,
	llmContext *ai.Context,
	options *ai.ModelsSimpleStreamOptions,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*types.AssistantMessage, error) {
	// pi spreads options and overrides cacheRetention/sessionId; the value
	// copy keeps the caller's struct untouched. requestIdOptions.signal is
	// carried by ctx (see header note).
	requestOptions := ai.ModelsSimpleStreamOptions{}
	if options != nil {
		requestOptions = *options
	}
	requestOptions.CacheRetention = types.CacheRetentionNone
	requestOptions.SessionID = ai.UUIDv7()
	return ai.RetryAssistantCall(ctx, func() (*types.AssistantMessage, error) {
		return models.CompleteSimple(ctx, model, llmContext, &requestOptions)
	}, retry, callbacks)
}

// CombineUsage sums two usage records (pi: combineUsage).
func CombineUsage(first *types.Usage, second *types.Usage) *types.Usage {
	usage := types.Usage{
		Input:       first.Input + second.Input,
		Output:      first.Output + second.Output,
		CacheRead:   first.CacheRead + second.CacheRead,
		CacheWrite:  first.CacheWrite + second.CacheWrite,
		TotalTokens: first.TotalTokens + second.TotalTokens,
		Cost: types.Cost{
			Input:      first.Cost.Input + second.Cost.Input,
			Output:     first.Cost.Output + second.Cost.Output,
			CacheRead:  first.Cost.CacheRead + second.Cost.CacheRead,
			CacheWrite: first.Cost.CacheWrite + second.Cost.CacheWrite,
			Total:      first.Cost.Total + second.Cost.Total,
		},
	}
	if first.CacheWrite1h != nil || second.CacheWrite1h != nil {
		var sum int64
		if first.CacheWrite1h != nil {
			sum += *first.CacheWrite1h
		}
		if second.CacheWrite1h != nil {
			sum += *second.CacheWrite1h
		}
		usage.CacheWrite1h = &sum
	}
	if first.Reasoning != nil || second.Reasoning != nil {
		var sum int64
		if first.Reasoning != nil {
			sum += *first.Reasoning
		}
		if second.Reasoning != nil {
			sum += *second.Reasoning
		}
		usage.Reasoning = &sum
	}
	return &usage
}

// CompactionSettings holds compaction thresholds and retention settings (pi:
// CompactionSettings).
//
// Defined in package harness to break the import cycle (pi's
// agent-harness.ts — package harness — consumes CompactionSettings while the
// compaction package imports harness); aliased here to keep both pi import
// paths working.
type CompactionSettings = harness.CompactionSettings

// DefaultCompactionSettings are the default compaction settings used by the
// harness (pi: DEFAULT_COMPACTION_SETTINGS).
var DefaultCompactionSettings = harness.DefaultCompactionSettings

// CalculateContextTokens calculates total context tokens from provider usage
// (pi: calculateContextTokens).
func CalculateContextTokens(usage *types.Usage) int64 {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

// getAssistantUsage returns the usage of a completed, non-failed assistant
// message (pi: getAssistantUsage).
func getAssistantUsage(msg agent.AgentMessage) *types.Usage {
	assistant, ok := msg.(types.AssistantMessage)
	if !ok || assistant.MessageRole() != types.RoleAssistant {
		return nil
	}
	// pi guards `assistantMsg.usage &&` — pig's Usage is a value type, so the
	// nil check collapses away.
	if assistant.StopReason != types.StopReasonAborted &&
		assistant.StopReason != types.StopReasonError &&
		CalculateContextTokens(&assistant.Usage) > 0 {
		return &assistant.Usage
	}
	return nil
}

// GetLastAssistantUsage returns usage from the last valid assistant message
// in session entries (pi: getLastAssistantUsage).
func GetLastAssistantUsage(entries []session.Entry) *types.Usage {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if messageEntry, ok := entry.(*session.MessageEntry); ok {
			if usage := getAssistantUsage(messageEntry.Message); usage != nil {
				return usage
			}
		}
	}
	return nil
}

// ContextUsageEstimate is the estimated context-token usage for a message
// list (pi: ContextUsageEstimate).
type ContextUsageEstimate struct {
	// Tokens is the estimated total context tokens.
	Tokens int64
	// UsageTokens is the tokens reported by the most recent assistant usage
	// block.
	UsageTokens int64
	// TrailingTokens is the estimated tokens after the most recent assistant
	// usage block.
	TrailingTokens int64
	// LastUsageIndex is the index of the message that provided usage; nil
	// when none exists (pi: number | null).
	LastUsageIndex *int
}

// getLastAssistantUsageInfo finds the last message with valid usage (pi:
// getLastAssistantUsageInfo).
func getLastAssistantUsageInfo(messages []agent.AgentMessage) (*types.Usage, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		if usage := getAssistantUsage(messages[i]); usage != nil {
			return usage, i
		}
	}
	return nil, -1
}

// EstimateContextTokens estimates context tokens for messages using provider
// usage when available (pi: estimateContextTokens).
func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	usage, index := getLastAssistantUsageInfo(messages)

	if usage == nil {
		estimated := int64(0)
		for _, message := range messages {
			estimated += EstimateTokens(message)
		}
		return ContextUsageEstimate{
			Tokens:         estimated,
			UsageTokens:    0,
			TrailingTokens: estimated,
			LastUsageIndex: nil,
		}
	}

	usageTokens := CalculateContextTokens(usage)
	trailingTokens := int64(0)
	for i := index + 1; i < len(messages); i++ {
		trailingTokens += EstimateTokens(messages[i])
	}

	return ContextUsageEstimate{
		Tokens:         usageTokens + trailingTokens,
		UsageTokens:    usageTokens,
		TrailingTokens: trailingTokens,
		LastUsageIndex: &index,
	}
}

// ShouldCompact reports whether context usage exceeds the configured
// compaction threshold (pi: shouldCompact).
func ShouldCompact(contextTokens int64, contextWindow int64, settings CompactionSettings) bool {
	if !settings.Enabled {
		return false
	}
	return contextTokens > contextWindow-settings.ReserveTokens
}

// estimatedImageChars is the char budget charged per image block (pi:
// ESTIMATED_IMAGE_CHARS).
const estimatedImageChars = 4800

// estimateTextAndImageContentChars estimates characters for text/image block
// lists (pi: estimateTextAndImageContentChars).
func estimateTextAndImageContentChars(content []types.Content) int64 {
	chars := int64(0)
	for _, block := range content {
		switch b := block.(type) {
		case types.TextContent:
			chars += int64(len(b.Text))
		case types.ImageContent:
			chars += estimatedImageChars
		}
	}
	return chars
}

// EstimateTokens estimates the token count for one message using a
// conservative character heuristic (pi: estimateTokens).
func EstimateTokens(message agent.AgentMessage) int64 {
	chars := int64(0)

	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.Parts != nil {
			chars = estimateTextAndImageContentChars(m.Content.Parts)
		} else {
			chars = int64(len(m.Content.String))
		}
		return ceilDiv4(chars)
	case types.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case types.TextContent:
				chars += int64(len(b.Text))
			case types.ThinkingContent:
				chars += int64(len(b.Thinking))
			case types.ToolCall:
				chars += int64(len(b.Name)) + int64(len(safeJsonStringify(b.Arguments)))
			}
		}
		return ceilDiv4(chars)
	case harness.CustomMessage:
		switch c := m.Content.(type) {
		case string:
			chars = int64(len(c))
		case []types.Content:
			chars = estimateTextAndImageContentChars(c)
		}
		return ceilDiv4(chars)
	case types.ToolResultMessage:
		chars = estimateTextAndImageContentChars(m.Content)
		return ceilDiv4(chars)
	case harness.BashExecutionMessage:
		chars = int64(len(m.Command) + len(m.Output))
		return ceilDiv4(chars)
	case harness.BranchSummaryMessage:
		chars = int64(len(m.Summary))
		return ceilDiv4(chars)
	case harness.CompactionSummaryMessage:
		chars = int64(len(m.Summary))
		return ceilDiv4(chars)
	default:
		// pi's switch falls through to `return 0` for unknown roles.
		return 0
	}
}

// ceilDiv4 computes ceil(chars / 4).
func ceilDiv4(chars int64) int64 {
	return (chars + 3) / 4
}

// findValidCutPoints collects the entry indexes a compaction may cut at (pi:
// findValidCutPoints).
func findValidCutPoints(entries []session.Entry, startIndex int, endIndex int) []int {
	var cutPoints []int
	for i := startIndex; i < endIndex; i++ {
		entry := entries[i]
		switch e := entry.(type) {
		case *session.MessageEntry:
			switch e.Message.MessageRole() {
			case harness.RoleBashExecution, harness.RoleCustomMessage, harness.RoleBranchSummary, harness.RoleCompactionSummary,
				types.RoleUser, types.RoleAssistant:
				cutPoints = append(cutPoints, i)
			case types.RoleToolResult:
				// Not a cut point.
			}
		case *session.ThinkingLevelEntry, *session.ModelChangeEntry, *session.ActiveToolsEntry,
			*session.CompactionEntry, *session.CustomEntry:
			// Not a cut point.
		}
		if _, ok := entry.(*session.BranchSummaryEntry); ok {
			cutPoints = append(cutPoints, i)
		}
	}
	return cutPoints
}

// FindTurnStartIndex finds the user-visible message that starts the turn
// containing an entry (pi: findTurnStartIndex).
func FindTurnStartIndex(entries []session.Entry, entryIndex int, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		entry := entries[i]
		if _, ok := entry.(*session.BranchSummaryEntry); ok {
			return i
		}
		if messageEntry, ok := entry.(*session.MessageEntry); ok {
			role := messageEntry.Message.MessageRole()
			if role == types.RoleUser || role == harness.RoleBashExecution {
				return i
			}
		}
	}
	return -1
}

// CutPointResult is the cut point selected for compaction (pi:
// CutPointResult).
type CutPointResult struct {
	// FirstKeptEntryIndex is the index of the first entry retained after
	// compaction.
	FirstKeptEntryIndex int
	// TurnStartIndex is the index of the turn-start entry when the cut splits
	// a turn, otherwise -1.
	TurnStartIndex int
	// IsSplitTurn reports whether the selected cut point splits an
	// in-progress turn.
	IsSplitTurn bool
}

// FindCutPoint finds the compaction cut point that keeps approximately the
// requested recent-token budget (pi: findCutPoint).
func FindCutPoint(entries []session.Entry, startIndex int, endIndex int, keepRecentTokens int64) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)

	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1, IsSplitTurn: false}
	}
	accumulatedTokens := int64(0)
	cutIndex := cutPoints[0]

	for i := endIndex - 1; i >= startIndex; i-- {
		entry := entries[i]
		messageEntry, ok := entry.(*session.MessageEntry)
		if !ok {
			continue
		}
		messageTokens := EstimateTokens(messageEntry.Message)
		accumulatedTokens += messageTokens
		if accumulatedTokens >= keepRecentTokens {
			for _, c := range cutPoints {
				if c >= i {
					cutIndex = c
					break
				}
			}
			break
		}
	}
	for cutIndex > startIndex {
		prevEntry := entries[cutIndex-1]
		if _, ok := prevEntry.(*session.CompactionEntry); ok {
			break
		}
		if _, ok := prevEntry.(*session.MessageEntry); ok {
			break
		}
		cutIndex--
	}
	cutEntry := entries[cutIndex]
	isUserMessage := false
	if messageEntry, ok := cutEntry.(*session.MessageEntry); ok {
		isUserMessage = messageEntry.Message.MessageRole() == types.RoleUser
	}
	turnStartIndex := -1
	if !isUserMessage {
		turnStartIndex = FindTurnStartIndex(entries, cutIndex, startIndex)
	}

	return CutPointResult{
		FirstKeptEntryIndex: cutIndex,
		TurnStartIndex:      turnStartIndex,
		IsSplitTurn:         !isUserMessage && turnStartIndex != -1,
	}
}

// SummarizationSystemPrompt is the system prompt for all summarization calls
// (pi: SUMMARIZATION_SYSTEM_PROMPT).
const SummarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

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

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
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

// SummaryWithUsage pairs a generated summary text with its provider usage
// (pi: the `{ text, usage }` success payload).
type SummaryWithUsage struct {
	Text  string
	Usage *types.Usage
}

// GenerateSummary generates or updates a conversation summary for compaction
// (pi: generateSummary).
func GenerateSummary(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	models ai.Models,
	model *ai.Model,
	reserveTokens int64,
	customInstructions string,
	previousSummary string,
	thinkingLevel agent.ThinkingLevel,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (string, error) {
	result, err := GenerateSummaryWithUsage(ctx, currentMessages, models, model, reserveTokens, customInstructions, previousSummary, thinkingLevel, retry, callbacks)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// GenerateSummaryWithUsage generates or updates a conversation summary and
// returns its provider usage (pi: generateSummaryWithUsage).
func GenerateSummaryWithUsage(
	ctx context.Context,
	currentMessages []agent.AgentMessage,
	models ai.Models,
	model *ai.Model,
	reserveTokens int64,
	customInstructions string,
	previousSummary string,
	thinkingLevel agent.ThinkingLevel,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*SummaryWithUsage, error) {
	maxTokens := computeSummaryMaxTokens(0.8, reserveTokens, model)
	basePrompt := summarizationPrompt
	if previousSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	if customInstructions != "" {
		basePrompt = fmt.Sprintf("%s\n\nAdditional focus: %s", basePrompt, customInstructions)
	}
	llmMessages := harness.ConvertToLlm(currentMessages)
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n", conversationText)
	if previousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\n%s\n</previous-summary>\n\n", previousSummary)
	}
	promptText += basePrompt

	summarizationMessages := []types.Message{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(promptText)}},
			Timestamp: nowMillis(),
		},
	}

	completionOptions := summarizationCompletionOptions(maxTokens, model, thinkingLevel)

	response, err := CompleteSimpleWithRetries(
		ctx,
		models,
		model,
		&ai.Context{SystemPrompt: SummarizationSystemPrompt, Messages: summarizationMessages},
		completionOptions,
		retry,
		callbacks,
	)
	if err != nil {
		return nil, err
	}
	if response.StopReason == types.StopReasonAborted {
		message := response.ErrorMessage
		if message == "" {
			message = "Summarization aborted"
		}
		return nil, harness.NewCompactionError(harness.CompactionCodeAborted, message, nil)
	}
	if response.StopReason == types.StopReasonError {
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return nil, harness.NewCompactionError(harness.CompactionCodeSummarizationFailed, fmt.Sprintf("Summarization failed: %s", message), nil)
	}

	return &SummaryWithUsage{Text: response.Text(), Usage: &response.Usage}, nil
}

// computeSummaryMaxTokens clamps the summary output budget (pi: the
// maxTokens expressions; 0.8 for summaries, 0.5 for turn prefixes).
func computeSummaryMaxTokens(fraction float64, reserveTokens int64, model *ai.Model) int64 {
	limit := int64(math.Floor(fraction * float64(reserveTokens)))
	if model.MaxTokens > 0 && model.MaxTokens < limit {
		return model.MaxTokens
	}
	return limit
}

// summarizationCompletionOptions builds the request options (pi: the
// completionOptions ternaries). thinkingLevel "" or "off" (or a
// non-reasoning model) omits the reasoning option, matching the TS
// `model.reasoning && thinkingLevel && thinkingLevel !== "off"` guard.
func summarizationCompletionOptions(maxTokens int64, model *ai.Model, thinkingLevel agent.ThinkingLevel) *ai.ModelsSimpleStreamOptions {
	options := &ai.ModelsSimpleStreamOptions{}
	options.MaxTokens = &maxTokens
	if model.Reasoning && thinkingLevel != "" && thinkingLevel != "off" {
		options.Reasoning = types.ThinkingLevel(thinkingLevel)
	}
	return options
}

// nowMillis returns the current Unix time in milliseconds (pi: Date.now()).
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// agentMessageTimestamp reads one AgentMessage's timestamp (pi:
// `message.timestamp` on the union). The Go union is an interface, so the
// known message types are enumerated; unknown types fall back to now.
func agentMessageTimestamp(message agent.AgentMessage) int64 {
	switch m := message.(type) {
	case types.UserMessage:
		return m.Timestamp
	case types.AssistantMessage:
		return m.Timestamp
	case types.ToolResultMessage:
		return m.Timestamp
	case harness.BashExecutionMessage:
		return m.Timestamp
	case harness.CustomMessage:
		return m.Timestamp
	case harness.BranchSummaryMessage:
		return m.Timestamp
	case harness.CompactionSummaryMessage:
		return m.Timestamp
	default:
		return nowMillis()
	}
}

// CompactionPreparation holds the prepared inputs for a compaction run (pi:
// CompactionPreparation).
type CompactionPreparation struct {
	// MessagesToSummarize holds the messages summarized into the history
	// summary.
	MessagesToSummarize []agent.AgentMessage
	// TurnPrefixMessages holds the prefix messages summarized separately when
	// compaction splits a turn.
	TurnPrefixMessages []agent.AgentMessage
	// RetainedTail holds the recent messages retained after compaction and
	// stored on the compaction entry.
	RetainedTail []agent.AgentMessage
	// IsSplitTurn reports whether compaction splits a turn.
	IsSplitTurn bool
	// TokensBefore is the estimated context tokens before compaction.
	TokensBefore int64
	// PreviousSummary is the previous compaction summary used for iterative
	// updates; "" when none.
	PreviousSummary string
	// FileOps holds the file operations extracted from summarized history.
	FileOps *FileOperations
	// Settings holds the settings used to prepare compaction.
	Settings CompactionSettings
}

// PrepareCompaction prepares session entries for compaction, or returns nil
// when compaction is not applicable (pi: prepareCompaction).
func PrepareCompaction(pathEntries []session.Entry, settings CompactionSettings) (*CompactionPreparation, error) {
	if len(pathEntries) == 0 {
		return nil, nil
	}
	if _, ok := pathEntries[len(pathEntries)-1].(*session.CompactionEntry); ok {
		return nil, nil
	}

	prevCompactionIndex := -1
	for i := len(pathEntries) - 1; i >= 0; i-- {
		if _, ok := pathEntries[i].(*session.CompactionEntry); ok {
			prevCompactionIndex = i
			break
		}
	}

	var previousSummary string
	compactableEntries := pathEntries
	if prevCompactionIndex >= 0 {
		prevCompaction := pathEntries[prevCompactionIndex].(*session.CompactionEntry)
		previousSummary = prevCompaction.Summary
		// pi builds virtual message entries for the retained tail so the cut
		// scan sees them; the synthetic ids chain off the compaction entry.
		virtualRetainedEntries := make([]session.Entry, 0, len(prevCompaction.RetainedTail))
		prevID := prevCompaction.ID
		for index, message := range prevCompaction.RetainedTail {
			id := fmt.Sprintf("%s:retained:%d", prevID, index)
			entry := &session.MessageEntry{
				Message: message,
			}
			entry.ID = id
			entry.Seq = prevCompaction.Seq
			entry.Timestamp = agentMessageTimestamp(message)
			if index == 0 {
				parent := prevID
				entry.ParentID = &parent
			} else {
				parent := fmt.Sprintf("%s:retained:%d", prevID, index-1)
				entry.ParentID = &parent
			}
			virtualRetainedEntries = append(virtualRetainedEntries, entry)
		}
		compactableEntries = append(append([]session.Entry{}, virtualRetainedEntries...), pathEntries[prevCompactionIndex+1:]...)
	}
	boundaryEnd := len(compactableEntries)

	tokensBefore := EstimateContextTokens(session.BuildSessionContext(pathEntries, nil).Messages).Tokens

	cutPoint := FindCutPoint(compactableEntries, 0, boundaryEnd, settings.KeepRecentTokens)
	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}
	var messagesToSummarize []agent.AgentMessage
	for i := 0; i < historyEnd; i++ {
		if msg := getMessageFromEntryForCompaction(compactableEntries[i]); msg != nil {
			messagesToSummarize = append(messagesToSummarize, msg)
		}
	}
	var turnPrefixMessages []agent.AgentMessage
	if cutPoint.IsSplitTurn {
		for i := cutPoint.TurnStartIndex; i < cutPoint.FirstKeptEntryIndex; i++ {
			if msg := getMessageFromEntryForCompaction(compactableEntries[i]); msg != nil {
				turnPrefixMessages = append(turnPrefixMessages, msg)
			}
		}
	}
	var retainedTail []agent.AgentMessage
	for i := cutPoint.FirstKeptEntryIndex; i < boundaryEnd; i++ {
		if msg := getMessageFromEntryForCompaction(compactableEntries[i]); msg != nil {
			retainedTail = append(retainedTail, msg)
		}
	}
	fileOps := extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex)
	if cutPoint.IsSplitTurn {
		for _, msg := range turnPrefixMessages {
			ExtractFileOpsFromMessage(msg, fileOps)
		}
	}

	return &CompactionPreparation{
		MessagesToSummarize: messagesToSummarize,
		TurnPrefixMessages:  turnPrefixMessages,
		RetainedTail:        retainedTail,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            settings,
	}, nil
}

const turnPrefixSummarizationPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`

// Compact generates compaction summary data from prepared session history
// (pi: compact).
func Compact(
	ctx context.Context,
	preparation *CompactionPreparation,
	models ai.Models,
	model *ai.Model,
	customInstructions string,
	thinkingLevel agent.ThinkingLevel,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*CompactResult, error) {
	var summary string
	var summaryUsage *types.Usage

	if preparation.IsSplitTurn && len(preparation.TurnPrefixMessages) > 0 {
		historyText := "No prior history."
		var historyUsage *types.Usage
		if len(preparation.MessagesToSummarize) > 0 {
			historyResult, err := GenerateSummaryWithUsage(
				ctx,
				preparation.MessagesToSummarize,
				models,
				model,
				preparation.Settings.ReserveTokens,
				customInstructions,
				preparation.PreviousSummary,
				thinkingLevel,
				retry,
				callbacks,
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
			models,
			model,
			preparation.Settings.ReserveTokens,
			thinkingLevel,
			retry,
			callbacks,
		)
		if err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("%s\n\n---\n\n**Turn Context (split turn):**\n\n%s", historyText, turnPrefixResult.Text)
		if historyUsage != nil {
			summaryUsage = CombineUsage(historyUsage, turnPrefixResult.Usage)
		} else {
			summaryUsage = turnPrefixResult.Usage
		}
	} else {
		summaryResult, err := GenerateSummaryWithUsage(
			ctx,
			preparation.MessagesToSummarize,
			models,
			model,
			preparation.Settings.ReserveTokens,
			customInstructions,
			preparation.PreviousSummary,
			thinkingLevel,
			retry,
			callbacks,
		)
		if err != nil {
			return nil, err
		}
		summary = summaryResult.Text
		summaryUsage = summaryResult.Usage
	}

	readFiles, modifiedFiles := ComputeFileLists(preparation.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	return &CompactResult{
		Summary:      summary,
		TokensBefore: preparation.TokensBefore,
		Usage:        summaryUsage,
		RetainedTail: preparation.RetainedTail,
		Details: CompactionDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		},
	}, nil
}

// generateTurnPrefixSummary summarizes the prefix of a split turn (pi:
// generateTurnPrefixSummary).
func generateTurnPrefixSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	models ai.Models,
	model *ai.Model,
	reserveTokens int64,
	thinkingLevel agent.ThinkingLevel,
	retry *ai.RetryPolicy,
	callbacks *ai.RetryCallbacks,
) (*SummaryWithUsage, error) {
	maxTokens := computeSummaryMaxTokens(0.5, reserveTokens, model)
	llmMessages := harness.ConvertToLlm(messages)
	conversationText := SerializeConversation(llmMessages)
	promptText := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, turnPrefixSummarizationPrompt)
	summarizationMessages := []types.Message{
		types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(promptText)}},
			Timestamp: nowMillis(),
		},
	}

	completionOptions := summarizationCompletionOptions(maxTokens, model, thinkingLevel)
	response, err := CompleteSimpleWithRetries(
		ctx,
		models,
		model,
		&ai.Context{SystemPrompt: SummarizationSystemPrompt, Messages: summarizationMessages},
		completionOptions,
		retry,
		callbacks,
	)
	if err != nil {
		return nil, err
	}
	if response.StopReason == types.StopReasonAborted {
		message := response.ErrorMessage
		if message == "" {
			message = "Turn prefix summarization aborted"
		}
		return nil, harness.NewCompactionError(harness.CompactionCodeAborted, message, nil)
	}
	if response.StopReason == types.StopReasonError {
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return nil, harness.NewCompactionError(harness.CompactionCodeSummarizationFailed, fmt.Sprintf("Turn prefix summarization failed: %s", message), nil)
	}

	return &SummaryWithUsage{Text: response.Text(), Usage: &response.Usage}, nil
}
