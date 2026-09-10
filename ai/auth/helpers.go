// pi: auth/helpers.ts.
package auth

import "context"

// EnvAPIKeyAuth is standard api-key auth: a stored credential key wins,
// otherwise the first set env var resolves. Includes a Login that prompts
// for the key (pi: envApiKeyAuth; pi's returned object carries resolve and
// the optional login member — pig expresses the optional member as the
// ApiKeyAuthLogin capability, satisfied by the same value).
//
// Providers with non-standard resolution (provider env, ambient files, IAM)
// write their own ApiKeyAuth.
func EnvAPIKeyAuth(name string, envVars []string) ApiKeyAuth {
	return envAPIKeyAuth{name: name, envVars: envVars}
}

type envAPIKeyAuth struct {
	name    string
	envVars []string
}

func (a envAPIKeyAuth) Name() string { return a.name }

// Resolve derives auth from the stored credential and/or env vars: the
// credential key wins, then the first set env var. A nil result means not
// configured.
func (a envAPIKeyAuth) Resolve(input ApiKeyAuthInput) (*AuthResult, error) {
	if err := input.Context.Err(); err != nil {
		return nil, err
	}
	if input.Credential != nil && input.Credential.Key != "" {
		return &AuthResult{
			Auth:   ModelAuth{APIKey: input.Credential.Key},
			Env:    input.Credential.Env,
			Source: "stored credential",
		}, nil
	}
	for _, envVar := range a.envVars {
		value, ok := input.Ctx.Env(envVar)
		if err := input.Context.Err(); err != nil {
			return nil, err
		}
		if ok {
			return &AuthResult{Auth: ModelAuth{APIKey: value}, Source: envVar}, nil
		}
	}
	return nil, nil
}

// Login prompts for the key (pi: login member).
func (a envAPIKeyAuth) Login(ctx context.Context, interaction AuthInteraction) (*ApiKeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := interaction.Prompt(AuthPrompt{Type: "secret", Message: "Enter " + a.name})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &ApiKeyCredential{Key: key}, nil
}

// lazyOAuth (pi helpers.ts) is missing here by design: it defers a dynamic
// import of the OAuth flow implementation purely for bundle splitting, which
// has no Go counterpart. Provider definitions advertise OAuth once the flows
// land (pi: loadAnthropicOAuth / loadOpenRouterOAuth via auth/oauth/load.ts,
// README Phase 2).
