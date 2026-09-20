// pi: src/utils/overflow.ts — context-overflow detection from assistant
// error messages and usage.
//
// Go's regexp is RE2: every pi pattern here is RE2-compatible (no
// backreferences or lookarounds), so the patterns port verbatim.
package utils

import (
	"regexp"

	"github.com/ZoroSpace/pig/ai/types"
)

// overflowPatterns detect context-overflow errors from different providers
// (pi: OVERFLOW_PATTERNS, same order; comments name the provider).
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),                                                                        // Anthropic token overflow
	regexp.MustCompile(`(?i)request_too_large`),                                                                         // Anthropic request byte-size overflow (HTTP 413)
	regexp.MustCompile(`(?i)input is too long for requested model`),                                                     // Amazon Bedrock
	regexp.MustCompile(`(?i)exceeds the context window`),                                                                // OpenAI (Completions & Responses API)
	regexp.MustCompile(`(?i)exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`), // OpenAI-compatible proxies (LiteLLM)
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),                                                    // Google (Gemini)
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                                                              // xAI (Grok)
	regexp.MustCompile(`(?i)reduce the length of the messages`),                                                         // Groq
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),                                                      // OpenRouter (most backends)
	regexp.MustCompile(`(?i)exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`),                           // OpenRouter/Poolside
	regexp.MustCompile(`(?i)input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`),            // Together AI
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),                                                                  // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                                                        // llama.cpp server
	regexp.MustCompile(`(?i)greater than the context length`),                                                           // LM Studio
	regexp.MustCompile(`(?i)context window exceeds limit`),                                                              // MiniMax
	regexp.MustCompile(`(?i)exceeded model token limit`),                                                                // Kimi For Coding
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`),                                       // Mistral
	regexp.MustCompile(`(?i)prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`),              // DS4 server
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                                                             // z.ai non-standard finish_reason surfaced as error text
	regexp.MustCompile(`(?i)prompt too long; exceeded (?:max )?context length`),                                         // Ollama explicit overflow error
	regexp.MustCompile(`(?i)range of input length should be`),                                                           // DashScope / Qwen Token Plan
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),                                                             // Generic fallback
	regexp.MustCompile(`(?i)too many tokens`),                                                                           // Generic fallback
	regexp.MustCompile(`(?i)token limit exceeded`),                                                                      // Generic fallback
	// pig-only additions (not in pi's OVERFLOW_PATTERNS):
	regexp.MustCompile(`(?i)OutofContextError`),         // Volcengine ark (Coding Plan) — pi-provider-volcengine-codingplan extension normalized these via a message_end hook; pig detects them here instead
	regexp.MustCompile(`(?i)exceed max message tokens`), // Volcengine ark (Coding Plan)
}

// nonOverflowPatterns indicate non-overflow errors (e.g. rate limiting,
// server errors). Error messages matching any of these are excluded from
// overflow detection even if they also match an overflowPattern (pi:
// NON_OVERFLOW_PATTERNS, same order; comments name the provider).
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(Throttling error|Service unavailable):`), // AWS Bedrock non-overflow errors (human-readable prefixes from formatBedrockError)
	regexp.MustCompile(`(?i)rate limit`),                               // Generic rate limiting
	regexp.MustCompile(`(?i)too many requests`),                        // Generic HTTP 429 style
}

// cerebrasBodylessOverflowPattern is scoped to the Cerebras provider
// upstream (pi: CEREBRAS_BODYLESS_OVERFLOW_PATTERN; the generic-pattern
// entry became provider-scoped in v0.86.0 — "scope bodyless overflow errors
// to Cerebras").
var cerebrasBodylessOverflowPattern = regexp.MustCompile(`(?i)^4(?:00|13)\s*(?:status code)?\s*\(no body\)`)

// IsContextOverflow reports whether an assistant message represents a
// context-overflow error (pi: isContextOverflow). Three cases:
//  1. Error-based overflow: most providers return stopReason "error" with a
//     specific error message pattern.
//  2. Silent overflow: some providers accept overflow requests and return
//     successfully; check whether usage input exceeds the context window.
//  3. Length-stop overflow: Xiaomi MiMo can return "length" with zero output
//     when the input fills the context window.
//
// contextWindow == 0 means pi's undefined (the silent-overflow checks are
// skipped).
func IsContextOverflow(message *types.AssistantMessage, contextWindow int64) bool {
	// Case 1: Check error message patterns
	if message.StopReason == types.StopReasonError && message.ErrorMessage != "" {
		// Skip messages matching known non-overflow patterns (e.g. throttling / rate-limit)
		isNonOverflow := false
		for _, pattern := range nonOverflowPatterns {
			if pattern.MatchString(message.ErrorMessage) {
				isNonOverflow = true
				break
			}
		}
		if !isNonOverflow {
			for _, pattern := range overflowPatterns {
				if pattern.MatchString(message.ErrorMessage) {
					return true
				}
			}
			if message.Provider == "cerebras" && cerebrasBodylessOverflowPattern.MatchString(message.ErrorMessage) {
				return true
			}
		}
	}

	// Case 2: Silent overflow (z.ai style) - successful but usage exceeds context
	if contextWindow != 0 && message.StopReason == types.StopReasonStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > contextWindow {
			return true
		}
	}

	// Case 3: Length-stop overflow (Xiaomi MiMo style) - server truncates oversized input
	// to fit the context window, leaving no room for output. Returns stopReason "length"
	// with output=0 and input+cacheRead filling the context window.
	if contextWindow != 0 && message.StopReason == types.StopReasonLength && message.Usage.Output == 0 {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		// pi: inputTokens >= contextWindow * 0.99 (float comparison kept)
		if float64(inputTokens) >= float64(contextWindow)*0.99 {
			return true
		}
	}

	return false
}

// IsRecoverableLength reports whether a length stop ended below the caller or
// model's intended output limit (pi: isRecoverableLength). Such responses may
// be caused by context pressure or provider-side truncation, so callers can
// make one bounded compact-and-retry attempt. desiredMaxOutput must be the
// original limit before any context-based clamping.
func IsRecoverableLength(message *types.AssistantMessage, desiredMaxOutput int64) bool {
	return message.StopReason == types.StopReasonLength && desiredMaxOutput > 0 && message.Usage.Output < desiredMaxOutput
}
