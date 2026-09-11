// pi: test/anthropic-long-cache-retention-e2e.test.ts. Real-API probe
// covering the generated anthropic-messages models; every live case skips
// without a provider API key. Case construction and the probe helper live in
// anthropic_eager_tool_input_e2e_test.go (the two pi files share the same
// structure).
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestAnthropicLongCacheRetentionE2EProbe(t *testing.T) {
	for _, c := range anthropicMessagesE2ECases(t) {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if c.apiKey == "" {
				t.Skip("no provider API key")
			}
			model := c.model
			model.Compat = mergeAnthropicCompatField(model.Compat, func(compat *types.AnthropicMessagesCompat) {
				compat.SupportsLongCacheRetention = boolPtr(true)
			})
			expectAnthropicToolRequestAccepted(t, &model, c.apiKey, types.CacheRetentionLong)
		})
	}
}
