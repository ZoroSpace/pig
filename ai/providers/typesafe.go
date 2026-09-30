// pi: providers/typesafe.ts (v0.99.1 #9948). The TypeSafe provider serves the
// direct Jev classifier through the System One protocol. pig ports the
// catalog entry but not the classify() runtime (S10-1 declared deviation, it
// arrives with codemode if ever): the provider has no stream adapter, so any
// request against it fails with the "no API implementation" stream error.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

// TypesafeProvider is the built-in TypeSafe provider (pi: typesafeProvider).
func TypesafeProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "typesafe",
		Name:    "TypeSafe",
		BaseURL: "https://api.typesafe.ai/v1/",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("TypeSafe API key", []string{"TYPESAFE_API_KEY"}),
		},
		// The classifier catalog is the provider's whole model list (pi:
		// typesafe.models.ts exports classifier models).
		Models: GetBuiltinClassifierModels("typesafe"),
	})
}
