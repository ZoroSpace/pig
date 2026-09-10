// pi: scripts/models-dev-reasoning-options.ts.
package main

import (
	"github.com/ZoroSpace/pig/ai"
)

// ModelsDevReasoningOption is one models.dev reasoning option (pi:
// ModelsDevReasoningOption): a toggle, an effort enumeration, or a token
// budget range. pi's discriminated union collapses into one struct; exactly
// the fields of Type are meaningful.
type ModelsDevReasoningOption struct {
	Type string `json:"type"`
	// Values belongs to type "effort": "none" | "minimal" | "low" |
	// "medium" | "high" | "xhigh" | "max" | "default" | null.
	Values []*string `json:"values,omitempty"`
	// Min/Max belong to type "budget_tokens".
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// thinkingLevels lists pi's selectable thinking levels (pi: THINKING_LEVELS).
var thinkingLevels = []ai.ThinkingLevel{
	ai.ThinkingLevelMinimal, ai.ThinkingLevelLow, ai.ThinkingLevelMedium,
	ai.ThinkingLevelHigh, ai.ThinkingLevelXHigh, ai.ThinkingLevelMax,
}

// getEffortThinkingLevelMap converts models.dev verified effort values into
// pi's selectable thinking levels (pi: getEffortThinkingLevelMap). Values
// without a pi equivalent ("default" and JSON null) are intentionally
// omitted. A nil result means undefined.
func getEffortThinkingLevelMap(options []ModelsDevReasoningOption) *ai.ThinkingLevelMap {
	var effortValues []*string
	for _, option := range options {
		if option.Type == "effort" {
			effortValues = append(effortValues, option.Values...)
		}
	}
	if len(effortValues) == 0 {
		return nil
	}

	supported := map[string]bool{}
	for _, value := range effortValues {
		if value != nil {
			supported[*value] = true
		}
	}
	hasLevel := false
	for _, level := range thinkingLevels {
		if supported[string(level)] {
			hasLevel = true
			break
		}
	}
	if !hasLevel && !supported["none"] {
		return nil
	}

	m := ai.ThinkingLevelMap{}
	if supported["none"] {
		none := "none"
		m[ai.ModelThinkingOff] = &none
	} else {
		m[ai.ModelThinkingOff] = nil
	}
	for _, level := range thinkingLevels {
		if supported[string(level)] {
			value := string(level)
			m[ai.ModelThinkingLevel(level)] = &value
		} else {
			m[ai.ModelThinkingLevel(level)] = nil
		}
	}
	return &m
}
