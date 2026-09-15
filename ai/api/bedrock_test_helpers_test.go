// Shared bedrock test fixtures (pi: the vi.mock("@aws-sdk/client-bedrock-runtime")
// hoisted block shared by the bedrock-*.test.ts files).
package api

import (
	"context"
	"errors"
	"io"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/ZoroSpace/pig/ai/types"
)

// bedrockMockClient is the seam stub: it captures the client config and the
// ConverseStream input, and replays canned events (pi: BedrockRuntimeClient
// mock with constructorCalls + streamEvents).
type bedrockMockClient struct {
	configs []bedrockClientConfig
	inputs  []*bedrockConverseInput

	// events are the decoded stream events; err rejects the send itself; a
	// non-nil streamErr surfaces after the events are consumed (pi: throwing
	// from inside the async generator).
	events    []brtypes.ConverseStreamOutput
	err       error
	streamErr error
	// metadata is the $metadata slice of the send result.
	metadata bedrockResponseMetadata
}

func (m *bedrockMockClient) reset() {
	m.configs = nil
	m.inputs = nil
	m.events = nil
	m.err = nil
	m.streamErr = nil
	m.metadata = bedrockResponseMetadata{}
}

func (m *bedrockMockClient) lastConfig() bedrockClientConfig {
	if len(m.configs) == 0 {
		return bedrockClientConfig{}
	}
	return m.configs[len(m.configs)-1]
}

// install overrides the bedrockNewClient seam; cleanup restores it.
func (m *bedrockMockClient) install(t cleanupT) {
	prev := bedrockNewClient
	bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
		m.configs = append(m.configs, params.config)
		return &bedrockMockStreamClient{mock: m}, nil
	}
	t.Cleanup(func() { bedrockNewClient = prev })
}

type cleanupT interface {
	Cleanup(func())
}

// bedrockMockStreamClient implements the client half of the seam.
type bedrockMockStreamClient struct {
	mock *bedrockMockClient
}

func (c *bedrockMockStreamClient) ConverseStream(ctx context.Context, input *bedrockConverseInput) (*bedrockConverseStreamResult, error) {
	c.mock.inputs = append(c.mock.inputs, input)
	if c.mock.err != nil {
		return nil, c.mock.err
	}
	return &bedrockConverseStreamResult{
		Metadata: c.mock.metadata,
		Events:   &bedrockMockEventSource{mock: c.mock, ctx: ctx},
	}, nil
}

type bedrockMockEventSource struct {
	mock *bedrockMockClient
	ctx  context.Context
	pos  int
}

func (s *bedrockMockEventSource) Next(ctx context.Context) (brtypes.ConverseStreamOutput, error) {
	if s.pos < len(s.mock.events) {
		event := s.mock.events[s.pos]
		s.pos++
		return event, nil
	}
	if s.mock.streamErr != nil {
		return nil, s.mock.streamErr
	}
	return nil, io.EOF
}

func (s *bedrockMockEventSource) Close() {}

// bedrockBaseModel mirrors pi's baseModel fixture (a Claude Sonnet 4.5 US
// inference profile).
var bedrockBaseModel = &types.Model{
	ID:        "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
	Name:      "Claude Sonnet 4.5 (US)",
	API:       "bedrock-converse-stream",
	Provider:  "amazon-bedrock",
	BaseURL:   "https://bedrock-runtime.us-east-1.amazonaws.com",
	Reasoning: true,
	Input:     []types.ModelInput{types.ModelInputText, types.ModelInputImage},
	Cost: types.ModelCost{ModelCostRates: types.ModelCostRates{
		Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75,
	}},
	ContextWindow: 200000,
	MaxTokens:     64000,
	Compat:        &types.BedrockCompat{SupportsStrictMode: boolPtr(true)},
}

// bedrockNovaModel is a non-Claude, non-reasoning model without compat.
var bedrockNovaModel = func() *types.Model {
	m := *bedrockBaseModel
	m.ID = "amazon.nova-lite-v1:0"
	m.Name = "Nova Lite"
	m.Reasoning = false
	m.Compat = nil
	return &m
}()

// alreadyCancelledContext is pi's AbortSignal.abort(): a context that is
// already cancelled.
func alreadyCancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// captureBedrockPayload drives a stream whose onPayload captures the command
// input, with the request pre-aborted so nothing reaches the wire (pi:
// capturePayload with signal: AbortSignal.abort()).
func captureBedrockPayload(t cleanupT, context *types.Context, model *types.Model) *bedrockConverseInput {
	var captured *bedrockConverseInput
	streamOptions := types.StreamOptions{CacheRetention: types.CacheRetentionNone}
	streamOptions.OnPayload = func(payload any, _ *types.Model) any {
		captured = payload.(*bedrockConverseInput)
		return payload
	}
	s := streamBedrockConverse(alreadyCancelledContext(), model, context, &BedrockOptions{
		StreamOptions: streamOptions,
	})
	for event := range s.Events() {
		if event.Type == types.EventError {
			break
		}
	}
	if captured == nil {
		panic("Expected Bedrock payload to be captured before request abort")
	}
	return captured
}

// errorsNew is a tiny alias so test files need not import errors twice.
func errorsNew(msg string) error { return errors.New(msg) }

// strPtr2 avoids clashing with the strPtr helper above.
func strPtr2(v string) *string { return &v }
