// pi: test/bedrock-response-headers.test.ts. This suite exercises the REAL
// aws-sdk-go-v2 transport (no seam stub) against a local HTTP server, so the
// deserialize-step middleware is driven end-to-end.
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

const bedrockResponseHeaderModelID = "us.anthropic.claude-haiku-4-5-20251001-v1:0"

func TestBedrockResponseHeadersForwardsRawSmithyHeaders(t *testing.T) {
	resetBedrockEnv(t)
	var responses []types.ProviderResponse

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/vnd.amazon.eventstream")
		w.Header().Set("x-bifrost-provider", "bedrock")
		w.Header().Set("x-bifrost-resolved-model", bedrockResponseHeaderModelID)
		w.Header().Set("x-amzn-requestid", "req-123")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	baseModel, ok := ai.MODELS["amazon-bedrock"][bedrockResponseHeaderModelID]
	if !ok {
		t.Fatalf("expected %s in the builtin catalog", bedrockResponseHeaderModelID)
	}
	model := baseModel
	model.BaseURL = server.URL

	streamOptions := types.StreamOptions{
		ProviderRequestOptions: types.ProviderRequestOptions{
			Env: types.ProviderEnv{
				"AWS_BEDROCK_FORCE_HTTP1": "1",
				"AWS_BEDROCK_SKIP_AUTH":   "1",
			},
			OnResponse: func(response types.ProviderResponse, _ *types.Model) {
				responses = append(responses, response)
			},
		},
		CacheRetention: types.CacheRetentionNone,
	}
	s := streamBedrockConverse(context.Background(), &model, bedrockTestContext(), &BedrockOptions{StreamOptions: streamOptions})
	result := s.Result()

	// The fake server intentionally returns an empty event stream; this
	// assertion documents that the header callback still fires before stream
	// consumption.
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %q (%s), want error", result.StopReason, result.ErrorMessage)
	}
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if responses[0].Status != 200 {
		t.Fatalf("status = %d, want 200", responses[0].Status)
	}
	if responses[0].Headers["X-Amzn-Requestid"] != "req-123" {
		t.Fatalf("x-amzn-requestid = %q", responses[0].Headers["X-Amzn-Requestid"])
	}
	if responses[0].Headers["X-Bifrost-Provider"] != "bedrock" {
		t.Fatalf("x-bifrost-provider = %q", responses[0].Headers["X-Bifrost-Provider"])
	}
	if responses[0].Headers["X-Bifrost-Resolved-Model"] != bedrockResponseHeaderModelID {
		t.Fatalf("x-bifrost-resolved-model = %q", responses[0].Headers["X-Bifrost-Resolved-Model"])
	}
}
