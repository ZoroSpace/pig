// pi: test/azure-openai-base-url.test.ts — endpoint resolution shared by both
// Azure dispatch paths (v1.0.3 #9645 moved it to azure-openai-config.ts).
package api

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func azureConfigModel() *types.Model {
	return &types.Model{ID: "gpt-5.4", API: types.ApiAzureOpenAIResponses, Provider: types.ProviderAzure}
}

func TestResolveAzureBaseURL(t *testing.T) {
	t.Run("appends the openai/v1 base path on Azure hosts", func(t *testing.T) {
		model := azureConfigModel()
		got, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_BASE_URL": "https://my-resource.openai.azure.com"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://my-resource.openai.azure.com/openai/v1" {
			t.Fatalf("baseURL = %q", got)
		}
	})

	t.Run("normalizes the services.ai.azure.com host shape", func(t *testing.T) {
		model := azureConfigModel()
		got, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_BASE_URL": "https://my-resource.services.ai.azure.com/"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://my-resource.services.ai.azure.com/openai/v1" {
			t.Fatalf("baseURL = %q", got)
		}
	})

	t.Run("leaves an explicit versioned path untouched on non-Azure hosts", func(t *testing.T) {
		model := azureConfigModel()
		got, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_BASE_URL": "https://proxy.example.com/v1"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://proxy.example.com/v1" {
			t.Fatalf("baseURL = %q", got)
		}
	})

	t.Run("builds the URL from AZURE_OPENAI_RESOURCE_NAME when no base URL is set", func(t *testing.T) {
		model := azureConfigModel()
		got, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_RESOURCE_NAME": "my-resource"},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://my-resource.openai.azure.com/openai/v1" {
			t.Fatalf("baseURL = %q", got)
		}
	})

	t.Run("falls back to the model base URL last", func(t *testing.T) {
		model := azureConfigModel()
		model.BaseURL = "https://model-fallback.example.com"
		got, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got != "https://model-fallback.example.com" {
			t.Fatalf("baseURL = %q", got)
		}
	})

	t.Run("errors when nothing is configured", func(t *testing.T) {
		model := azureConfigModel()
		_, err := ResolveAzureBaseURL(model, &AzureEndpointOptions{})
		if err == nil || !strings.Contains(err.Error(), "Azure OpenAI base URL is required") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestResolveDeploymentName(t *testing.T) {
	t.Run("uses the model id by default", func(t *testing.T) {
		model := azureConfigModel()
		if got := ResolveDeploymentName(model, &AzureEndpointOptions{}); got != "gpt-5.4" {
			t.Fatalf("deployment = %q", got)
		}
	})

	t.Run("maps the model id through AZURE_OPENAI_DEPLOYMENT_NAME_MAP", func(t *testing.T) {
		model := azureConfigModel()
		got := ResolveDeploymentName(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-5.4=my-deploy, other=x"},
			}},
		})
		if got != "my-deploy" {
			t.Fatalf("deployment = %q", got)
		}
	})

	t.Run("the option wins over the map", func(t *testing.T) {
		model := azureConfigModel()
		got := ResolveDeploymentName(model, &AzureEndpointOptions{
			StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-5.4=from-map"},
			}},
			AzureDeploymentName: "from-option",
		})
		if got != "from-option" {
			t.Fatalf("deployment = %q", got)
		}
	})
}
