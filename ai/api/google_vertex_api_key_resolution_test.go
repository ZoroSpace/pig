// pi: test/google-vertex-api-key-resolution.test.ts.
//
// pi mocks the @google/genai SDK constructor; pig overrides the
// googleVertexGenerateContentStream seam and asserts the client config the
// constructor would have received.
package api

import (
	"context"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

// vertexSeamState captures what the transport received (pi:
// googleGenAiMock.constructorCalls).
type vertexSeamState struct {
	constructorCalls []*googleVertexClient
	chunks           []*googleGenerateContentResponse
}

func stubVertexSeam(t *testing.T, state *vertexSeamState) {
	t.Helper()
	previous := googleVertexGenerateContentStream
	googleVertexGenerateContentStream = func(ctx context.Context, client *googleVertexClient, params *googleGenerateContentParams) (googleChunkSource, error) {
		state.constructorCalls = append(state.constructorCalls, client)
		return &googleChunkSlice{chunks: state.chunks}, nil
	}
	t.Cleanup(func() { googleVertexGenerateContentStream = previous })
}

// vertexCatalogModel resolves a google-vertex catalog model (pi: getModel).
func vertexCatalogModel(t *testing.T, id string) *types.Model {
	t.Helper()
	base, ok := ai.MODELS[ai.ProviderGoogleVertex][id]
	if !ok {
		t.Fatalf("%s missing from the catalog", id)
	}
	return &base
}

// vertexOKChunks is the pi mock's canned stream: one STOP chunk with "ok".
func vertexOKChunks() []*googleGenerateContentResponse {
	return []*googleGenerateContentResponse{{
		ResponseID: "vertex-response-id",
		Candidates: []googleCandidate{{
			Content:      &googleContent{Parts: []googlePart{{Text: lo.ToPtr("ok")}}},
			FinishReason: "STOP",
		}},
		UsageMetadata: &googleUsageMetadata{PromptTokenCount: 1, CandidatesTokenCount: 1, TotalTokenCount: 2},
	}}
}

// runVertexStream runs the vertex stream with typed options (pi: the typed
// streamGoogleVertex call) and drains it to the result.
func runVertexStream(t *testing.T, model *types.Model, options *GoogleVertexOptions) types.AssistantMessage {
	t.Helper()
	stream := streamGoogleVertex(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: time.Now().UnixMilli()},
		},
	}, options)
	_, result := collectResponsesEvents(t, stream)
	return result
}

// requireSingleConstructor asserts exactly one client construction.
func requireSingleConstructor(t *testing.T, state *vertexSeamState) *googleVertexClient {
	t.Helper()
	if len(state.constructorCalls) != 1 {
		t.Fatalf("constructor calls = %d, want 1", len(state.constructorCalls))
	}
	return state.constructorCalls[0]
}

func TestVertexAPIKeyPlaceholderFallsBackToADC(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")

	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	options.APIKey = "<authenticated>"
	result := runVertexStream(t, model, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.project != "test-project" || client.location != "us-central1" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.apiKey != "" {
		t.Fatalf("client.apiKey = %q, want absent", client.apiKey)
	}
}

func TestVertexAPIKeyMarkerFallsBackToADC(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")

	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	options.APIKey = "gcp-vertex-credentials"
	result := runVertexStream(t, model, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.project != "test-project" || client.location != "us-central1" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.apiKey != "" {
		t.Fatalf("client.apiKey = %q, want absent", client.apiKey)
	}
}

func TestVertexEnvAPIKeyPlaceholderFallsBackToADC(t *testing.T) {
	// pi sets process.env.GOOGLE_CLOUD_API_KEY = "<authenticated>"; the env
	// var belongs to the provider auth layer — the adapter never reads it and
	// still selects the ADC client.
	t.Setenv("GOOGLE_CLOUD_API_KEY", "<authenticated>")
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")

	result := runVertexStream(t, model, &GoogleVertexOptions{
		Project:  "test-project",
		Location: "us-central1",
	})
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.project != "test-project" || client.location != "us-central1" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.apiKey != "" {
		t.Fatalf("client.apiKey = %q, want absent", client.apiKey)
	}
}

func TestVertexRealAPIKeyUsesAPIKeyClient(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")

	options := &GoogleVertexOptions{}
	options.APIKey = "AIzaSyExampleRealisticLookingApiKey123456"
	result := runVertexStream(t, model, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.apiKey != "AIzaSyExampleRealisticLookingApiKey123456" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.project != "" || client.location != "" {
		t.Fatalf("client.project/location = %q/%q, want absent", client.project, client.location)
	}
}

func TestVertexDoesNotForwardGeneratedBaseURLPlaceholders(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview") // baseUrl carries the {location} placeholder

	result := runVertexStream(t, model, &GoogleVertexOptions{
		Project:  "test-project",
		Location: "us-central1",
	})
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	// pi: httpOptions toEqual({ headers: { "User-Agent": PI_USER_AGENT } }).
	if client.httpOptions.baseURL != "" || client.httpOptions.baseURLResourceScope != "" {
		t.Fatalf("httpOptions.baseUrl/resourceScope = %q/%q, want absent",
			client.httpOptions.baseURL, client.httpOptions.baseURLResourceScope)
	}
	if ua := client.httpOptions.headers["User-Agent"]; ua != utils.GetPiUserAgent() || len(client.httpOptions.headers) != 1 {
		t.Fatalf("httpOptions.headers = %v, want only the pi User-Agent", client.httpOptions.headers)
	}
}

func TestVertexExplicitHeadersOverrideUserAgent(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")

	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	options.Headers = types.ProviderHeaders{"User-Agent": lo.ToPtr("custom-agent")}
	result := runVertexStream(t, model, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if client.httpOptions.headers["User-Agent"] != "custom-agent" || len(client.httpOptions.headers) != 1 {
		t.Fatalf("httpOptions.headers = %v, want the overridden User-Agent", client.httpOptions.headers)
	}
}

func TestVertexCustomBaseURLForwardsToADCClient(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	model.BaseURL = "https://proxy.example.com"

	result := runVertexStream(t, model, &GoogleVertexOptions{
		Project:  "test-project",
		Location: "us-central1",
	})
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.project != "test-project" || client.location != "us-central1" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.httpOptions.baseURL != "https://proxy.example.com" ||
		client.httpOptions.baseURLResourceScope != vertexResourceScopeCollection {
		t.Fatalf("httpOptions = %+v, want the custom COLLECTION base URL", client.httpOptions)
	}
}

func TestVertexCustomBaseURLForwardsToAPIKeyClient(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	model.BaseURL = "https://proxy.example.com"

	options := &GoogleVertexOptions{}
	options.APIKey = "AIzaSyExampleRealisticLookingApiKey123456"
	result := runVertexStream(t, model, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if !client.vertexAI || client.apiKey != "AIzaSyExampleRealisticLookingApiKey123456" || client.apiVersion != vertexAPIVersion {
		t.Fatalf("client = %+v", client)
	}
	if client.httpOptions.baseURL != "https://proxy.example.com" ||
		client.httpOptions.baseURLResourceScope != vertexResourceScopeCollection {
		t.Fatalf("httpOptions = %+v, want the custom COLLECTION base URL", client.httpOptions)
	}
}

func TestVertexCustomBaseURLWithVersionOmitsAPIVersion(t *testing.T) {
	state := &vertexSeamState{chunks: vertexOKChunks()}
	stubVertexSeam(t, state)
	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	model.BaseURL = "https://proxy.example.com/v1/projects/test-project/locations/global"

	result := runVertexStream(t, model, &GoogleVertexOptions{
		Project:  "test-project",
		Location: "us-central1",
	})
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %s (%s)", result.StopReason, result.ErrorMessage)
	}
	client := requireSingleConstructor(t, state)
	if client.httpOptions.baseURL != "https://proxy.example.com/v1/projects/test-project/locations/global" ||
		client.httpOptions.baseURLResourceScope != vertexResourceScopeCollection || client.httpOptions.apiVersion != "" {
		t.Fatalf("httpOptions = %+v, want the custom base URL with an empty apiVersion", client.httpOptions)
	}
}

// TestVertexURLComposition pins the transport URL for the shapes above (pi
// asserts the constructor config; the wire URL is the SDK's composition —
// verified against @google/genai 1.52.0 with a node probe).
func TestVertexURLComposition(t *testing.T) {
	cases := []struct {
		name   string
		client *googleVertexClient
		want   string
	}{
		{
			"express",
			&googleVertexClient{vertexAI: true, apiKey: "AIzaTest", apiVersion: vertexAPIVersion},
			"https://aiplatform.googleapis.com/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"adc-regional",
			&googleVertexClient{vertexAI: true, project: "test-project", location: "us-central1", apiVersion: vertexAPIVersion},
			"https://us-central1-aiplatform.googleapis.com/v1/projects/test-project/locations/us-central1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"adc-global",
			&googleVertexClient{vertexAI: true, project: "test-project", location: "global", apiVersion: vertexAPIVersion},
			"https://aiplatform.googleapis.com/v1/projects/test-project/locations/global/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"adc-us-multiregion",
			&googleVertexClient{vertexAI: true, project: "test-project", location: "us", apiVersion: vertexAPIVersion},
			"https://aiplatform.us.rep.googleapis.com/v1/projects/test-project/locations/us/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"adc-custom-base",
			&googleVertexClient{vertexAI: true, project: "test-project", location: "us-central1", apiVersion: vertexAPIVersion,
				httpOptions: googleVertexHTTPOptions{baseURL: "https://proxy.example.com", baseURLResourceScope: vertexResourceScopeCollection}},
			"https://proxy.example.com/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"adc-custom-base-with-version",
			&googleVertexClient{vertexAI: true, project: "test-project", location: "us-central1", apiVersion: vertexAPIVersion,
				httpOptions: googleVertexHTTPOptions{baseURL: "https://proxy.example.com/v1/projects/test-project/locations/global", baseURLResourceScope: vertexResourceScopeCollection, apiVersion: ""}},
			"https://proxy.example.com/v1/projects/test-project/locations/global/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"express-custom-base",
			&googleVertexClient{vertexAI: true, apiKey: "AIzaTest", apiVersion: vertexAPIVersion,
				httpOptions: googleVertexHTTPOptions{baseURL: "https://proxy.example.com", baseURLResourceScope: vertexResourceScopeCollection}},
			"https://proxy.example.com/v1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
		{
			"default-sdk-version",
			&googleVertexClient{vertexAI: true, apiKey: "AIzaTest"},
			"https://aiplatform.googleapis.com/v1beta1/publishers/google/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		},
	}
	for _, c := range cases {
		got, err := c.client.buildVertexRequestURL(&googleGenerateContentParams{Model: "gemini-2.5-flash"})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: url = %s, want %s", c.name, got, c.want)
		}
	}
}

// TestVertexURLAuthenticationNotSetUp pins the SDK constructor error for a
// client without any auth shape.
func TestVertexURLAuthenticationNotSetUp(t *testing.T) {
	client := &googleVertexClient{vertexAI: true, apiVersion: vertexAPIVersion}
	if _, err := client.buildVertexRequestURL(&googleGenerateContentParams{Model: "gemini-2.5-flash"}); err == nil ||
		err.Error() != "Authentication is not set up. Please provide either a project and location, or an API key, or a custom base URL." {
		t.Fatalf("err = %v, want the authentication-not-set-up failure", err)
	}
}

// TestVertexTModel pins the SDK's vertex tModel mapping.
func TestVertexTModel(t *testing.T) {
	cases := map[string]string{
		"gemini-2.5-flash":        "publishers/google/models/gemini-2.5-flash",
		"publishers/meta/llama":   "publishers/meta/llama",
		"models/gemini-2.5-flash": "models/gemini-2.5-flash",
		"projects/p/models/m":     "projects/p/models/m",
		"foo/bar/baz":             "publishers/foo/models/bar",
		"":                        "",
	}
	for in, want := range cases {
		if got := tGoogleVertexModel(in); got != want {
			t.Errorf("tGoogleVertexModel(%q) = %q, want %q", in, got, want)
		}
	}
}
