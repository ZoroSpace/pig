// pi: src/core/experimental.ts — experimental feature gates.
package core

import (
	"os"

	"github.com/ZoroSpace/pig/ai/types"
)

// PreferStrictToolSampling is the strict-mode tool sampling request (pi:
// PREFER_STRICT_TOOL_SAMPLING = { type: "json_schema", strict: "prefer" }).
var PreferStrictToolSampling = types.ConstrainedSamplingConfig{
	Type:   "json_schema",
	Strict: "prefer",
}

// AreExperimentalFeaturesEnabled reports whether experimental features are
// on (pi: areExperimentalFeaturesEnabled — PI_EXPERIMENTAL=1).
func AreExperimentalFeaturesEnabled() bool {
	return os.Getenv("PI_EXPERIMENTAL") == "1"
}

// GetExperimentalToolSampling returns the strict tool sampling request when
// experimental features are enabled, nil otherwise (pi:
// getExperimentalToolSampling; consumers pass it as the tools'
// constrainedSampling option).
func GetExperimentalToolSampling() *types.ConstrainedSamplingConfig {
	if AreExperimentalFeaturesEnabled() {
		prefer := PreferStrictToolSampling
		return &prefer
	}
	return nil
}
