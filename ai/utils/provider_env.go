// pi: utils/provider-env.ts.
package utils

import (
	"os"

	"github.com/ZoroSpace/pig/ai/types"
)

// GetProviderEnvValue resolves a provider env value from scoped overrides,
// then normal process env (pi: getProviderEnvValue). pi's third fallback — a
// duplicate of process.env read from /proc/self/environ for Bun compiled
// binaries inside Linux sandboxes — has no Go counterpart: the Go runtime's
// os.Getenv already reads the real environment, so the workaround is
// unnecessary here.
func GetProviderEnvValue(name string, env types.ProviderEnv) string {
	if env != nil {
		if value, ok := env[name]; ok && value != "" {
			return value
		}
	}
	return os.Getenv(name)
}
