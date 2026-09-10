// pi: src/core/defaults.ts — default thinking level constants.
package core

import "github.com/ZoroSpace/pig/agent"

// DefaultThinkingLevel is the default thinking level (pi:
// DEFAULT_THINKING_LEVEL).
const DefaultThinkingLevel = agent.ThinkingLevel("medium")

// ThinkingLevelOptions lists all selectable thinking levels (pi:
// THINKING_LEVEL_OPTIONS, declaration order kept).
var ThinkingLevelOptions = []agent.ThinkingLevel{
	"off",
	"minimal",
	"low",
	"medium",
	"high",
	"xhigh",
	"max",
}
