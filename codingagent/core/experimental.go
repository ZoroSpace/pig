// pi: src/core/experimental.ts — experimental feature gates.
//
// pi v0.86.0 removed getExperimentalToolSampling/PREFER_STRICT_TOOL_SAMPLING:
// the strict-mode tool sampling request is now hardcoded at each tool
// definition site (bash/read/write/edit), no longer gated on
// PI_EXPERIMENTAL. pig keeps only the feature gate.
package core

import "os"

// AreExperimentalFeaturesEnabled reports whether experimental features are
// on (pi: areExperimentalFeaturesEnabled — PI_EXPERIMENTAL=1).
func AreExperimentalFeaturesEnabled() bool {
	return os.Getenv("PI_EXPERIMENTAL") == "1"
}
