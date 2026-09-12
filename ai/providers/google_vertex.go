// pi: providers/google-vertex.ts.
package providers

import (
	"context"
	"fmt"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// vertexADCPath is the gcloud ADC file (pi: VERTEX_ADC_PATH).
const vertexADCPath = "~/.config/gcloud/application_default_credentials.json"

// googleVertexAuth is Vertex auth (pi: vertexAuth, an ApiKeyAuth with a login
// member — pig expresses the optional member as the ApiKeyAuthLogin
// capability).
//
// Vertex accepts an explicit API key or Application Default Credentials
// (`gcloud auth application-default login`). ADC additionally requires
// project and location env vars, which the implementation reads itself.
type googleVertexAuth struct{}

func (googleVertexAuth) Name() string { return "Google Cloud credentials" }

// Resolve derives auth: an explicit key (stored credential or
// GOOGLE_CLOUD_API_KEY) wins; otherwise ADC credentials plus project/location
// env configure the provider (pi: vertexAuth.resolve).
func (googleVertexAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	env := func(name string) (string, bool) {
		if err := input.Context.Err(); err != nil {
			return "", false
		}
		return input.Ctx.Env(name)
	}

	key := ""
	if input.Credential != nil {
		key = input.Credential.Key
	}
	if key == "" {
		value, ok := env("GOOGLE_CLOUD_API_KEY")
		if ok {
			key = value
		}
	}
	if key != "" {
		source := "GOOGLE_CLOUD_API_KEY"
		if input.Credential != nil && input.Credential.Key != "" {
			source = "stored credential"
		}
		return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: key}, Source: source}, nil
	}

	// pi: credential?.env?.GOOGLE_APPLICATION_CREDENTIALS ?? env(...) — fall
	// back to the gcloud well-known path when neither is set.
	adcPath := vertexADCPath
	if input.Credential != nil {
		if value, ok := input.Credential.Env["GOOGLE_APPLICATION_CREDENTIALS"]; ok {
			adcPath = value
		}
	}
	if adcPath == vertexADCPath {
		if value, ok := env("GOOGLE_APPLICATION_CREDENTIALS"); ok {
			adcPath = value
		}
	}
	if err := input.Context.Err(); err != nil {
		return nil, err
	}
	hasCredentials := input.Ctx.FileExists(adcPath)

	project := ""
	location := ""
	if input.Credential != nil {
		if value, ok := input.Credential.Env["GOOGLE_CLOUD_PROJECT"]; ok {
			project = value
		}
		if value, ok := input.Credential.Env["GOOGLE_CLOUD_LOCATION"]; ok {
			location = value
		}
	}
	if project == "" {
		if value, ok := env("GOOGLE_CLOUD_PROJECT"); ok {
			project = value
		}
	}
	if project == "" {
		if value, ok := env("GCLOUD_PROJECT"); ok {
			project = value
		}
	}
	if location == "" {
		if value, ok := env("GOOGLE_CLOUD_LOCATION"); ok {
			location = value
		}
	}
	if err := input.Context.Err(); err != nil {
		return nil, err
	}
	if hasCredentials && project != "" && location != "" {
		result := &auth.AuthResult{
			Auth:   auth.ModelAuth{},
			Source: "gcloud application default credentials",
		}
		if input.Credential != nil {
			result.Env = input.Credential.Env
			result.Source = "stored credential"
		}
		return result, nil
	}
	return nil, nil
}

// Login runs the interactive setup (pi: vertexAuth.login): API key,
// Application Default Credentials, or a service account credentials file.
func (googleVertexAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	method, err := interaction.Prompt(auth.AuthPrompt{
		Type:    "select",
		Message: "Select Google Vertex AI authentication method:",
		Options: []auth.AuthPromptOption{
			{ID: "api-key", Label: "Google Cloud API key"},
			{ID: "adc", Label: "Application Default Credentials"},
			{ID: "service-account", Label: "Service account credentials file"},
		},
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "api-key" {
		key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter Google Cloud API key"})
		if err != nil {
			return nil, err
		}
		return &auth.ApiKeyCredential{Key: key}, nil
	}
	if method != "adc" && method != "service-account" {
		return nil, fmt.Errorf("Unknown Google Vertex AI auth method: %s", method)
	}
	if method == "adc" {
		interaction.Notify(auth.AuthEvent{
			Type:    "info",
			Message: "Run `gcloud auth application-default login`, then provide the project and location.",
			Links: []auth.AuthInfoLink{{
				Label: "Application Default Credentials",
				URL:   "https://cloud.google.com/docs/authentication/provide-credentials-adc",
			}},
		})
	} else {
		interaction.Notify(auth.AuthEvent{
			Type:    "info",
			Message: "Provide a service account credentials file, project, and location.",
			Links: []auth.AuthInfoLink{{
				Label: "Application Default Credentials",
				URL:   "https://cloud.google.com/docs/authentication/provide-credentials-adc",
			}},
		})
	}
	project, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter Google Cloud project ID"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	location, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter Google Cloud location"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env := types.ProviderEnv{
		"GOOGLE_CLOUD_PROJECT":  project,
		"GOOGLE_CLOUD_LOCATION": location,
	}
	if method == "service-account" {
		credentialsPath, err := interaction.Prompt(auth.AuthPrompt{
			Type: "text", Message: "Enter service account credentials file path",
		})
		if err != nil {
			return nil, err
		}
		env["GOOGLE_APPLICATION_CREDENTIALS"] = credentialsPath
	}
	return &auth.ApiKeyCredential{Env: env}, nil
}

// GoogleVertexProvider is the built-in Google Vertex AI provider (pi:
// googleVertexProvider).
func GoogleVertexProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "google-vertex",
		Name:   "Google Vertex AI",
		Auth:   auth.ProviderAuth{APIKey: googleVertexAuth{}},
		Models: GetBuiltinModels(types.ProviderGoogleVertex),
		API:    api.GoogleVertex{},
	})
}
