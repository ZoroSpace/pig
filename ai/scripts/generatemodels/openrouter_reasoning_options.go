// openrouter_reasoning_options.go (pi: scripts/openrouter-reasoning-options.ts)
// — convert OpenRouter's reasoning metadata into pi model capabilities.
package main

import (
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
)

// OpenRouterReasoningMetadata is one model's reasoning metadata (pi:
// OpenRouterReasoningMetadata).
type OpenRouterReasoningMetadata struct {
	Mandatory        *bool     `json:"mandatory"`
	DefaultEnabled   *bool     `json:"default_enabled"`
	SupportedEfforts []*string `json:"supported_efforts"`
	DefaultEffort    *string   `json:"default_effort"`
}

// getOpenRouterThinkingLevelMap converts OpenRouter's reasoning metadata into
// pi's selectable thinking levels (pi: getOpenRouterThinkingLevelMap).
// OpenRouter's supported_efforts uses the same effort values as models.dev
// reasoning_options, so both sources can share the same pi thinking-level
// conversion.
func getOpenRouterThinkingLevelMap(reasoning *OpenRouterReasoningMetadata) *ai.ThinkingLevelMap {
	if reasoning == nil {
		return nil
	}
	if len(reasoning.SupportedEfforts) == 0 {
		if reasoning.Mandatory != nil && *reasoning.Mandatory {
			return &ai.ThinkingLevelMap{ai.ModelThinkingOff: nil}
		}
		return nil
	}
	mapValue := getEffortThinkingLevelMap([]ModelsDevReasoningOption{{Type: "effort", Values: reasoning.SupportedEfforts}})
	if mapValue == nil {
		if reasoning.Mandatory != nil && *reasoning.Mandatory {
			return &ai.ThinkingLevelMap{ai.ModelThinkingOff: nil}
		}
		return nil
	}
	// pi: return { ...map, off: mandatory ? null : "none" }.
	merged := *mapValue
	if reasoning.Mandatory != nil && *reasoning.Mandatory {
		merged[ai.ModelThinkingOff] = nil
	} else {
		merged[ai.ModelThinkingOff] = lo.ToPtr("none")
	}
	return &merged
}
