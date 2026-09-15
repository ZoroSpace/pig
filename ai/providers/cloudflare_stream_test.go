// pi: test/cloudflare-stream.test.ts — materializes the model endpoint before
// dispatch and keeps placeholders when the provider env does not resolve them.
package providers

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

const cloudflareTestModelBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"

type cloudflareCapturingStreams struct {
	captured []string
}

func (s *cloudflareCapturingStreams) Stream(_ context.Context, model *types.Model, _ *types.Context, _ *types.StreamOptions) *types.AssistantMessageEventStream {
	s.captured = append(s.captured, model.BaseURL)
	return types.NewAssistantMessageEventStream()
}

func (s *cloudflareCapturingStreams) StreamSimple(_ context.Context, model *types.Model, _ *types.Context, _ *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	s.captured = append(s.captured, model.BaseURL)
	return types.NewAssistantMessageEventStream()
}

func cloudflareTestModel() *types.Model {
	return &types.Model{
		ID:            "model",
		Name:          "model",
		API:           types.ApiOpenAICompletions,
		Provider:      types.ProviderID("cloudflare-ai-gateway"),
		BaseURL:       cloudflareTestModelBaseURL,
		Reasoning:     false,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 1000,
		MaxTokens:     100,
	}
}

func TestCloudflareStreamsMaterializesEndpointBeforeDispatch(t *testing.T) {
	captured := &cloudflareCapturingStreams{}
	streams := CloudflareStreams(captured)
	env := types.ProviderEnv{
		"CLOUDFLARE_ACCOUNT_ID": "account",
		"CLOUDFLARE_GATEWAY_ID": "gateway",
	}
	model := cloudflareTestModel()

	streams.Stream(context.Background(), model, &types.Context{}, &types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{Env: env}})
	streams.StreamSimple(context.Background(), model, &types.Context{}, &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{Env: env}}})

	want := []string{
		"https://gateway.ai.cloudflare.com/v1/account/gateway/openai",
		"https://gateway.ai.cloudflare.com/v1/account/gateway/openai",
	}
	if len(captured.captured) != 2 || captured.captured[0] != want[0] || captured.captured[1] != want[1] {
		t.Errorf("captured = %v, want %v", captured.captured, want)
	}
}

func TestCloudflareStreamsKeepsPlaceholdersWithoutEnv(t *testing.T) {
	captured := &cloudflareCapturingStreams{}
	streams := CloudflareStreams(captured)
	model := cloudflareTestModel()

	streams.StreamSimple(context.Background(), model, &types.Context{}, &types.SimpleStreamOptions{})

	if len(captured.captured) != 1 || captured.captured[0] != cloudflareTestModelBaseURL {
		t.Errorf("captured = %v, want [%s]", captured.captured, cloudflareTestModelBaseURL)
	}
}
