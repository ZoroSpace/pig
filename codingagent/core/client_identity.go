// pi: core/client-identity.ts.
package core

import (
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
)

// getClientIdentity is how pig's requests identify it to providers: a
// detailed User-Agent with pig's version, and attribution to pi as a CLI agent
// while install telemetry is enabled (pi: getClientIdentity).
func getClientIdentity(settingsManager *SettingsManager) *types.ClientIdentity {
	identity := &types.ClientIdentity{
		UserAgent: types.UserAgentDetailed(),
		Version:   codingagent.Version,
	}
	if IsInstallTelemetryEnabled(settingsManager, nil) {
		identity.Attribution = &types.AttributionSetting{Attribution: &types.PIAttribution}
	} else {
		// Explicit null: also turns off attribution inherited from the agent's
		// default identity.
		identity.Attribution = &types.AttributionSetting{}
	}
	return identity
}
