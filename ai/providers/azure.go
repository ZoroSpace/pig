// pi: providers/azure.ts — the Azure provider (v1.0.3 #9645 renamed it from
// azure-openai-responses: it now serves Chat Completions deployments
// alongside the Responses API, so the api id "azure-openai-responses" stays
// while the provider id becomes "azure").
package providers

import (
	"context"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// azureProvider is the built-in Azure provider (pi: azureProvider).
func AzureProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "azure",
		Name:    "Azure",
		BaseURL: "",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Azure OpenAI API key", []string{"AZURE_OPENAI_API_KEY"}),
		},
		Models: GetBuiltinModels("azure"),
		APIByAPI: map[string]ai.ProviderStreams{
			// azureOpenAIResponsesApi() — the openai-responses event protocol
			// over Azure's deployment/api-version URL conventions.
			string(types.ApiAzureOpenAIResponses): api.AzureOpenAIResponses{},
			// azureStreams(openAICompletionsApi()) — the shared Chat
			// Completions implementation behind the endpoint/deployment
			// wrapper below.
			string(types.ApiOpenAICompletions): AzureStreams(api.OpenAICompletions{}),
		},
	})
}

// azureEndpointOptionsOf lifts the request options into the shared Azure
// endpoint-options shape (pi passes the same options object structurally;
// the Azure-specific fields read as absent when the caller did not set
// them). pig's concrete *types.StreamOptions dispatch cannot carry extended
// option fields, so at this layer the endpoint resolves from env and
// model.baseUrl; the option-level fields remain available on the adapters'
// own options types.
func azureEndpointOptionsOf(options *types.StreamOptions) *api.AzureEndpointOptions {
	endpoint := &api.AzureEndpointOptions{}
	if options != nil {
		endpoint.StreamOptions = *options
	}
	return endpoint
}

// resolveAzureModel resolves the Azure endpoint onto the model first — the
// shared openai-completions implementation reads it off the model (pi:
// resolveAzureModel).
func resolveAzureModel(model *types.Model, options *types.StreamOptions) (*types.Model, error) {
	baseURL, err := api.ResolveAzureBaseURL(model, azureEndpointOptionsOf(options))
	if err != nil {
		return nil, err
	}
	resolved := *model
	resolved.BaseURL = baseURL
	return &resolved, nil
}

// withDeploymentName sends the deployment name as the request's model,
// keeping model.ID as the catalog id (pi: withDeploymentName). The swap runs
// through onPayload before dispatch, then the caller's own onPayload runs on
// it.
func withDeploymentName(model *types.Model, options *types.StreamOptions) (*types.Model, *types.StreamOptions, error) {
	resolvedModel, err := resolveAzureModel(model, options)
	if err != nil {
		return nil, nil, err
	}
	deploymentName := api.ResolveDeploymentName(model, azureEndpointOptionsOf(options))
	if deploymentName == model.ID {
		return resolvedModel, options, nil
	}
	wrapped := types.StreamOptions{}
	if options != nil {
		wrapped = *options
	}
	callerOnPayload := wrapped.OnPayload
	wrapped.OnPayload = func(payload any, payloadModel *types.Model) any {
		if params, ok := payload.(map[string]any); ok {
			// pi spreads { ...payload, model: deploymentName }; the payload
			// map is adapter-built per request, so the in-place write is
			// equivalent.
			params["model"] = deploymentName
			payload = params
		}
		if callerOnPayload != nil {
			if next := callerOnPayload(payload, payloadModel); next != nil {
				return next
			}
		}
		return payload
	}
	return resolvedModel, &wrapped, nil
}

// azureStreams wraps an API implementation so the Azure endpoint resolves
// onto the model and the deployment name swaps into the payload before
// dispatch (pi: azureStreams). Resolution runs lazily inside the stream, so
// an unconfigured endpoint errors on the stream instead of throwing out of
// stream().
func AzureStreams(streams types.ProviderStreams) types.ProviderStreams {
	return &azureStreams{inner: streams}
}

type azureStreams struct {
	inner types.ProviderStreams
}

func (s *azureStreams) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	resolvedModel, resolvedOptions, err := withDeploymentName(model, options)
	if err != nil {
		return immediateErrorStream(model, err)
	}
	return s.inner.Stream(ctx, resolvedModel, llmContext, resolvedOptions)
}

func (s *azureStreams) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	resolvedModel, resolvedOptions, err := withDeploymentName(model, &options.StreamOptions)
	if err != nil {
		return immediateErrorStream(model, err)
	}
	simple := types.SimpleStreamOptions{}
	if resolvedOptions != nil {
		simple.StreamOptions = *resolvedOptions
	}
	simple.ToolChoice = options.ToolChoice
	simple.Reasoning = options.Reasoning
	simple.Deferred = options.Deferred
	simple.DeferredWindow = options.DeferredWindow
	simple.ThinkingBudgets = options.ThinkingBudgets
	return s.inner.StreamSimple(ctx, resolvedModel, llmContext, &simple)
}

// immediateErrorStream terminates a stream with a single error event (used
// where pi throws synchronously before returning a stream; same shape as the
// api-package helper — providers needs its own copy, pig layering).
func immediateErrorStream(model *types.Model, err error) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	message := &types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: message})
	stream.EndWith(*message)
	return stream
}
