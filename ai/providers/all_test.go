package providers

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

type mapAuthContext map[string]string

func (m mapAuthContext) Env(name string) (string, bool) {
	value, ok := m[name]
	return value, ok
}

func (m mapAuthContext) FileExists(string) bool { return false }

type promptInteraction struct {
	response string
	prompt   auth.AuthPrompt
}

func (i *promptInteraction) Prompt(prompt auth.AuthPrompt) (string, error) {
	i.prompt = prompt
	return i.response, nil
}

func (i *promptInteraction) Notify(auth.AuthEvent) {}

func TestBuiltinProvidersOrder(t *testing.T) {
	providers := BuiltinProviders()
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID())
	}
	// pi's builtinProviders order, restricted to the ported protocols.
	// "volcengine-plan" is pig-only (extension made builtin), slotted
	// alphabetically. The A2 anthropic-messages batch adds fireworks,
	// kimi-coding, minimax, minimax-cn, and vercel-ai-gateway; the B2
	// openai-responses batch adds xai; the C2 codex batch adds openai-codex.
	want := []string{
		"ant-ling", "anthropic", "baseten", "cerebras", "deepseek",
		"fireworks", "google", "groq", "huggingface", "kimi-coding", "minimax",
		"minimax-cn", "moonshotai", "moonshotai-cn", "nvidia", "openai",
		"openai-codex", "openrouter", "together", "vercel-ai-gateway",
		"volcengine-plan", "xai", "zai", "zai-coding-cn",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("BuiltinProviders() = %v, want %v", ids, want)
	}
}

func TestBuiltinModelsRegistersAll(t *testing.T) {
	models := BuiltinModels(nil)
	for _, id := range []string{
		"ant-ling", "anthropic", "baseten", "cerebras", "deepseek",
		"fireworks", "google", "groq", "huggingface", "kimi-coding", "minimax",
		"minimax-cn", "moonshotai", "moonshotai-cn", "nvidia", "openai",
		"openai-codex", "openrouter", "together", "vercel-ai-gateway",
		"volcengine-plan", "xai", "zai", "zai-coding-cn",
	} {
		provider := models.GetProvider(id)
		if provider == nil {
			t.Fatalf("GetProvider(%q) = nil", id)
		}
		// openai-codex is OAuth-only (pi: auth oauth, no api key).
		if id == "openai-codex" {
			if provider.Auth().OAuth == nil {
				t.Fatalf("provider %q has no OAuth auth", id)
			}
			continue
		}
		if provider.Auth().APIKey == nil {
			t.Fatalf("provider %q has no APIKey auth", id)
		}
	}
	if got := len(models.GetProviders()); got != 24 {
		t.Fatalf("GetProviders() length = %d, want 24", got)
	}
}

func TestProviderMetadata(t *testing.T) {
	cases := []struct {
		provider ai.Provider
		name     string
		baseURL  string
	}{
		{AnthropicProvider(), "Anthropic", "https://api.anthropic.com"},
		{DeepSeekProvider(), "DeepSeek", "https://api.deepseek.com"},
		{OpenAIProvider(), "OpenAI", "https://api.openai.com/v1"},
		{OpenRouterProvider(), "OpenRouter", "https://openrouter.ai/api/v1"},
		{VolcenginePlanProvider(), "Volcengine Coding Plan", "https://ark.cn-beijing.volces.com/api/coding/v3"},
	}
	for _, c := range cases {
		if c.provider.Name() != c.name {
			t.Errorf("%s: Name() = %q, want %q", c.provider.ID(), c.provider.Name(), c.name)
		}
		if c.provider.BaseURL() != c.baseURL {
			t.Errorf("%s: BaseURL() = %q, want %q", c.provider.ID(), c.provider.BaseURL(), c.baseURL)
		}
	}
}

func TestCatalogAccessors(t *testing.T) {
	providers := GetBuiltinProviders()
	// The generated catalog covers the ported providers; providers whose wire
	// APIs are unported (mistral-conversations, ...) are absent. The B2
	// openai-responses batch adds openai, xai, and azure-openai-responses; the
	// C2 codex batch adds openai-codex; the C3 google batch adds google.
	for _, id := range []BuiltinProvider{"anthropic", "deepseek", "openrouter"} {
		if !slices.Contains(providers, id) {
			t.Fatalf("GetBuiltinProviders() = %v, missing %q", providers, id)
		}
	}
	for _, id := range []BuiltinProvider{"openai", "xai", "azure-openai-responses", "openai-codex", "google"} {
		if !slices.Contains(providers, id) {
			t.Fatalf("GetBuiltinProviders() = %v, missing B2 %q", providers, id)
		}
	}
	for _, id := range []BuiltinProvider{"mistral"} {
		if slices.Contains(providers, id) {
			t.Fatalf("GetBuiltinProviders() includes unported %q: %v", id, providers)
		}
	}
	if got := GetBuiltinModels("deepseek"); len(got) != 2 {
		t.Fatalf("GetBuiltinModels(deepseek) = %v, want the 2 hardcoded V4 models", got)
	}
	if got := GetBuiltinModels("google"); len(got) == 0 {
		// C3: the google provider is ported; its catalog comes from models.dev.
		t.Fatalf("GetBuiltinModels(google) = empty, want the models.dev catalog")
	}
	if got, ok := GetBuiltinModel("google", "gemini-2.5-flash"); !ok || got.API != ai.ApiGoogleGenerativeAI {
		t.Fatalf("GetBuiltinModel(google, gemini-2.5-flash) = %+v, %v", got, ok)
	}
	if got := GetBuiltinModelDataGeneratedAt(); got == 0 {
		t.Fatal("GetBuiltinModelDataGeneratedAt() = 0, want the real generation timestamp")
	}
}

func TestEnvAPIKeyAuthResolve(t *testing.T) {
	authMethod := auth.EnvAPIKeyAuth("DeepSeek API key", []string{"DEEPSEEK_API_KEY"})
	ctx := context.Background()

	stored, err := authMethod.Resolve(auth.ApiKeyAuthInput{
		Ctx:        mapAuthContext{},
		Credential: &auth.ApiKeyCredential{Key: "sk-stored"},
		Context:    ctx,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Auth.APIKey != "sk-stored" || stored.Source != "stored credential" {
		t.Fatalf("stored credential resolve = %+v", stored)
	}

	fromEnv, err := authMethod.Resolve(auth.ApiKeyAuthInput{
		Ctx:     mapAuthContext{"DEEPSEEK_API_KEY": "sk-env"},
		Context: ctx,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv.Auth.APIKey != "sk-env" || fromEnv.Source != "DEEPSEEK_API_KEY" {
		t.Fatalf("env resolve = %+v", fromEnv)
	}

	unset, err := authMethod.Resolve(auth.ApiKeyAuthInput{Ctx: mapAuthContext{}, Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if unset != nil {
		t.Fatalf("unset resolve = %+v, want nil", unset)
	}
}

func TestEnvAPIKeyAuthLogin(t *testing.T) {
	authMethod := auth.EnvAPIKeyAuth("DeepSeek API key", []string{"DEEPSEEK_API_KEY"})
	login, ok := authMethod.(auth.ApiKeyAuthLogin)
	if !ok {
		t.Fatal("EnvAPIKeyAuth does not implement ApiKeyAuthLogin")
	}
	interaction := &promptInteraction{response: "sk-prompted"}
	credential, err := login.Login(context.Background(), interaction)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Key != "sk-prompted" {
		t.Fatalf("Login credential = %+v", credential)
	}
	if interaction.prompt.Type != "secret" || interaction.prompt.Message != "Enter DeepSeek API key" {
		t.Fatalf("Login prompt = %+v", interaction.prompt)
	}
}

func TestAnthropicAPIKeyAuthChain(t *testing.T) {
	authMethod := anthropicAPIKeyAuth()
	ctx := context.Background()

	resolve := func(envs mapAuthContext, credential *auth.ApiKeyCredential) (*auth.AuthResult, error) {
		return authMethod.Resolve(auth.ApiKeyAuthInput{Ctx: envs, Credential: credential, Context: ctx})
	}

	// Stored credential wins over env.
	result, err := resolve(mapAuthContext{"ANTHROPIC_API_KEY": "sk-env"}, &auth.ApiKeyCredential{Key: "sk-stored"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey != "sk-stored" || result.Source != "stored credential" {
		t.Fatalf("stored credential resolve = %+v", result)
	}

	// ANTHROPIC_AUTH_TOKEN goes out as a Bearer header.
	result, err = resolve(mapAuthContext{"ANTHROPIC_AUTH_TOKEN": "tok", "ANTHROPIC_API_KEY": "sk-env"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer, ok := result.Auth.Headers["Authorization"]
	if !ok || bearer == nil || *bearer != "Bearer tok" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN resolve = %+v", result)
	}
	if result.Auth.APIKey != "" || result.Source != "ANTHROPIC_AUTH_TOKEN" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN resolve = %+v", result)
	}

	// Then OAuth token, then API key.
	result, err = resolve(mapAuthContext{"ANTHROPIC_OAUTH_TOKEN": "oat"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey != "oat" || result.Source != "ANTHROPIC_OAUTH_TOKEN" {
		t.Fatalf("ANTHROPIC_OAUTH_TOKEN resolve = %+v", result)
	}
	result, err = resolve(mapAuthContext{"ANTHROPIC_API_KEY": "sk-env"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey != "sk-env" || result.Source != "ANTHROPIC_API_KEY" {
		t.Fatalf("ANTHROPIC_API_KEY resolve = %+v", result)
	}

	// Nothing set: unconfigured.
	result, err = resolve(mapAuthContext{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("unset resolve = %+v, want nil", result)
	}
}

func TestAnthropicAPIKeyAuthLogin(t *testing.T) {
	login, ok := anthropicAPIKeyAuth().(auth.ApiKeyAuthLogin)
	if !ok {
		t.Fatal("anthropic api-key auth does not implement ApiKeyAuthLogin")
	}
	interaction := &promptInteraction{response: "sk-anthropic"}
	credential, err := login.Login(context.Background(), interaction)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Key != "sk-anthropic" {
		t.Fatalf("Login credential = %+v", credential)
	}
	if interaction.prompt.Type != "secret" || interaction.prompt.Message != "Enter Anthropic API key" {
		t.Fatalf("Login prompt = %+v", interaction.prompt)
	}
}
