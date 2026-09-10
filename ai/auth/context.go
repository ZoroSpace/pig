package auth

import (
	"os"
	"strings"
)

// pi: auth/context.ts.

// defaultAuthContext is the DefaultProviderAuthContext counterpart: env vars
// from the process environment, file existence via the filesystem.
type defaultAuthContext struct{}

func (defaultAuthContext) Env(name string) (string, bool) {
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func (defaultAuthContext) FileExists(path string) bool {
	resolved := path
	if strings.HasPrefix(resolved, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		resolved = home + resolved[len("~"):]
	}
	_, err := os.Stat(resolved)
	return err == nil
}

// DefaultProviderAuthContext returns the ambient AuthContext: env vars from
// the process environment, file existence via the filesystem (pi reads
// process.env and node:fs; pig uses os and always supports files).
func DefaultProviderAuthContext() AuthContext {
	return defaultAuthContext{}
}
