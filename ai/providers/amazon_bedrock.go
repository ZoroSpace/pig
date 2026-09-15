// pi: providers/amazon-bedrock.ts.
package providers

import (
	"context"
	"fmt"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// bedrockAuth is Bedrock auth (pi: bedrockAuth, an ApiKeyAuth with a login
// member — pig expresses the optional member as the ApiKeyAuthLogin
// capability).
//
// Bedrock accepts a bearer token or the AWS SDK's default credential chain.
// The login flow can store a token/profile choice; resolve also detects
// ambient AWS credentials without copying them into pig's credential store.
type bedrockAuth struct{}

func (bedrockAuth) Name() string { return "AWS credentials or bearer token" }

// Login runs the interactive setup (pi: bedrockAuth.login): bearer token,
// AWS profile, or existing credential chain.
func (bedrockAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	method, err := interaction.Prompt(auth.AuthPrompt{
		Type:    "select",
		Message: "Select Amazon Bedrock authentication method:",
		Options: []auth.AuthPromptOption{
			{ID: "bearer-token", Label: "Bearer token"},
			{ID: "aws-profile", Label: "AWS profile"},
			{ID: "credential-chain", Label: "Existing AWS credential chain"},
		},
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if method == "bearer-token" {
		key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter Amazon Bedrock bearer token"})
		if err != nil {
			return nil, err
		}
		return &auth.ApiKeyCredential{Key: key}, nil
	}
	interaction.Notify(auth.AuthEvent{
		Type:    "info",
		Message: "Amazon Bedrock supports AWS profiles, IAM credentials, and role-based credentials.",
		Links: []auth.AuthInfoLink{{
			Label: "AWS credential provider chain",
			URL:   "https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html",
		}},
	})
	if method == "aws-profile" {
		profile, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter AWS profile name"})
		if err != nil {
			return nil, err
		}
		return &auth.ApiKeyCredential{Env: types.ProviderEnv{"AWS_PROFILE": profile}}, nil
	}
	if method != "credential-chain" {
		return nil, fmt.Errorf("Unknown Amazon Bedrock auth method: %s", method)
	}
	if _, err := interaction.Prompt(auth.AuthPrompt{
		Type:    "text",
		Message: "Configure AWS credentials, then press Enter to continue",
	}); err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{}, nil
}

// Resolve derives auth from the stored credential and/or ambient AWS
// configuration (pi: bedrockAuth.resolve). Ambient sources yield an empty
// ModelAuth: the adapter (and the AWS SDK) resolve the ambient credentials
// themselves.
func (bedrockAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	env := func(name string) (string, bool) {
		if err := input.Context.Err(); err != nil {
			return "", false
		}
		return input.Ctx.Env(name)
	}
	if input.Credential != nil && input.Credential.Key != "" {
		return &auth.AuthResult{
			Auth:   auth.ModelAuth{APIKey: input.Credential.Key},
			Env:    input.Credential.Env,
			Source: "stored credential",
		}, nil
	}
	if value, _ := env("AWS_BEARER_TOKEN_BEDROCK"); value != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "AWS_BEARER_TOKEN_BEDROCK"}, nil
	}
	scopedProfile := ""
	if input.Credential != nil {
		scopedProfile = input.Credential.Env["AWS_PROFILE"]
	}
	ambientProfile, _ := env("AWS_PROFILE")
	if scopedProfile != "" || ambientProfile != "" {
		result := &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "AWS_PROFILE"}
		if scopedProfile != "" {
			result.Source = "stored credential"
		}
		if input.Credential != nil {
			result.Env = input.Credential.Env
		}
		return result, nil
	}
	accessKeyID, _ := env("AWS_ACCESS_KEY_ID")
	secretAccessKey, _ := env("AWS_SECRET_ACCESS_KEY")
	if accessKeyID != "" && secretAccessKey != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "AWS access keys"}, nil
	}
	if value, _ := env("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI"); value != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "ECS task role"}, nil
	}
	if value, _ := env("AWS_CONTAINER_CREDENTIALS_FULL_URI"); value != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "ECS task role"}, nil
	}
	if value, _ := env("AWS_WEB_IDENTITY_TOKEN_FILE"); value != "" {
		return &auth.AuthResult{Auth: auth.ModelAuth{}, Source: "web identity token"}, nil
	}
	return nil, nil
}

// AmazonBedrockProvider is the built-in Amazon Bedrock provider (pi:
// amazonBedrockProvider).
func AmazonBedrockProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "amazon-bedrock",
		Name:   "Amazon Bedrock",
		Auth:   auth.ProviderAuth{APIKey: bedrockAuth{}},
		Models: GetBuiltinModels(types.ProviderAmazonBedrock),
		API:    api.BedrockConverseStream{},
	})
}
