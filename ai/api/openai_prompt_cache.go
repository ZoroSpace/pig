// pi: api/openai-prompt-cache.ts.
package api

// OpenAIPromptCacheKeyMaxLength is the maximum prompt_cache_key length (pi:
// OPENAI_PROMPT_CACHE_KEY_MAX_LENGTH).
const OpenAIPromptCacheKeyMaxLength = 64

// ClampOpenAIPromptCacheKey truncates a prompt cache key to the maximum
// length, counting code points (pi: clampOpenAIPromptCacheKey).
func ClampOpenAIPromptCacheKey(key string) string {
	runes := []rune(key)
	if len(runes) <= OpenAIPromptCacheKeyMaxLength {
		return key
	}
	return string(runes[:OpenAIPromptCacheKeyMaxLength])
}
