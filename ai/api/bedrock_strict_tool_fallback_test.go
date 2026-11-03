// pi: test/bedrock-strict-tool-fallback.test.ts (#11416).
package api

import (
	"context"
	"io"
	"testing"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

type bedrockStrictFallbackClient struct {
	inputs []*bedrockConverseInput
	calls  int
}

func (c *bedrockStrictFallbackClient) ConverseStream(ctx context.Context, input *bedrockConverseInput) (*bedrockConverseStreamResult, error) {
	c.inputs = append(c.inputs, input)
	c.calls++
	if c.calls == 1 {
		return nil, &bedrockTestException{code: "ValidationException", message: "Too many strict tools (20). Please reduce the number of strict tools."}
	}
	return &bedrockConverseStreamResult{Events: bedrockEmptyEventSource{}}, nil
}

type bedrockEmptyEventSource struct{}

func (bedrockEmptyEventSource) Next(ctx context.Context) (brtypes.ConverseStreamOutput, error) {
	return nil, io.EOF
}
func (bedrockEmptyEventSource) Close() {}

func TestBedrockStrictToolFallback(t *testing.T) {
	resetBedrockEnv(t)

	client := &bedrockStrictFallbackClient{}
	prev := bedrockNewClient
	bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
		return client, nil
	}
	t.Cleanup(func() { bedrockNewClient = prev })

	tool := types.Tool{
		Name:                "read",
		Description:         "Read a file",
		Parameters:          []byte(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		ConstrainedSampling: types.ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}
	llmContext := utils.NormalizeContext(&types.Context{Tools: []types.Tool{tool}})
	model := bedrockBaseModel

	s := streamBedrockConverse(context.Background(), model, llmContext,
		&BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
	message := s.Result()

	if len(client.inputs) != 2 {
		t.Fatalf("ConverseStream calls = %d, want 2", len(client.inputs))
	}
	first := client.inputs[0].ToolConfig
	if first == nil || len(first.Tools) != 1 || first.Tools[0].ToolSpec.Strict == nil || !*first.Tools[0].ToolSpec.Strict {
		t.Fatal("first request should send the preferred tool as strict")
	}
	second := client.inputs[1].ToolConfig
	if second == nil || len(second.Tools) != 1 || second.Tools[0].ToolSpec.Strict != nil {
		t.Fatal("retry should downgrade the preferred tool to a non-strict schema")
	}

	var diagnostic *types.AssistantMessageDiagnostic
	for i := range message.Diagnostics {
		if message.Diagnostics[i].Type == "bedrock_strict_tools_fallback" {
			diagnostic = &message.Diagnostics[i]
			break
		}
	}
	if diagnostic == nil {
		t.Fatal("expected a bedrock_strict_tools_fallback diagnostic")
	}
	if reason, _ := diagnostic.Details["reason"].(string); reason != "strict_tool_limits_exceeded" {
		t.Fatalf("diagnostic reason = %v", diagnostic.Details["reason"])
	}
}
