// Shared vocabulary constants (pi: module-level consts consumed by both the
// coding-agent CLI and core; pig relocates them here because cli imports
// core, and keeping them in cli would make core import cli and close a
// cycle).
package constants

// ValidThinkingLevels mirrors pi's VALID_THINKING_LEVELS (pi declares it in
// coding-agent src/cli/args.ts). Members are plain strings; consumers convert
// to agent.ThinkingLevel (= types.ModelThinkingLevel).
var ValidThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// IsValidThinkingLevel reports whether level is one of the valid thinking
// levels (pi: isValidThinkingLevel).
func IsValidThinkingLevel(level string) bool {
	for _, candidate := range ValidThinkingLevels {
		if level == candidate {
			return true
		}
	}
	return false
}
