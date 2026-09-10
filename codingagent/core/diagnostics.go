// pi: src/core/diagnostics.ts — resource diagnostic shapes shared by the
// resource loaders.
package core

// ResourceCollision describes a name collision between two loaded resources
// (pi: ResourceCollision).
type ResourceCollision struct {
	// ResourceType is "extension" | "skill" | "prompt" | "theme"; the skill
	// name, command/tool/flag name, prompt name, or theme name.
	ResourceType string `json:"resourceType"`
	Name         string `json:"name"`
	WinnerPath   string `json:"winnerPath"`
	LoserPath    string `json:"loserPath"`
	WinnerSource string `json:"winnerSource,omitempty"` // e.g., "npm:foo", "git:...", "local"
	LoserSource  string `json:"loserSource,omitempty"`
}

// ResourceDiagnostic is one resource loading diagnostic (pi:
// ResourceDiagnostic).
type ResourceDiagnostic struct {
	Type      string             `json:"type"` // "warning" | "error" | "collision"
	Message   string             `json:"message"`
	Path      string             `json:"path,omitempty"`
	Collision *ResourceCollision `json:"collision,omitempty"`
}
