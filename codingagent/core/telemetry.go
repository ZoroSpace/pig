// pi: core/telemetry.ts — install telemetry toggle (13 lines).
package core

import "os"

func isTruthyEnvFlag(value string) bool {
	if value == "" {
		return false
	}
	return value == "1" || value == "true" || value == "yes"
}

// IsInstallTelemetryEnabled reports whether install telemetry is enabled (pi:
// isInstallTelemetryEnabled). telemetryEnv nil = PI_TELEMETRY unset (pi:
// undefined, the process.env default); a non-nil empty string is a set-empty
// env value (falsy).
func IsInstallTelemetryEnabled(settingsManager *SettingsManager, telemetryEnv *string) bool {
	if telemetryEnv != nil {
		return isTruthyEnvFlag(*telemetryEnv)
	}
	if value, ok := os.LookupEnv("PI_TELEMETRY"); ok {
		return isTruthyEnvFlag(value)
	}
	return settingsManager.GetEnableInstallTelemetry()
}
