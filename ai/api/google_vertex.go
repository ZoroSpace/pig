// pi: api/google-vertex.ts.
//
// The Google Vertex AI wire adapter. pi goes through the @google/genai SDK
// (client.models.generateContentStream with vertexai: true); the SDK has no
// Go counterpart, so the transport below is a native rewrite of the SDK
// surface pi exercises:
//
//   - URL: the SDK's ApiClient constructor + constructUrl + shouldPrependVertexProjectPath.
//     Default base URL by auth shape — express (apiKey) or location "global"
//     → https://aiplatform.googleapis.com/, multi-regional us/eu →
//     https://aiplatform.{location}.rep.googleapis.com/, otherwise
//     https://{location}-aiplatform.googleapis.com/; a model.baseUrl that does
//     not contain the {location} placeholder overrides it (pi:
//     ResourceScope.COLLECTION — the project/location path prefix is then
//     dropped). apiVersion is "v1" (pi: API_VERSION) unless the custom base
//     URL already carries a v<digits>[beta] segment, in which case the version
//     segment is skipped. The model path is the SDK's vertex tModel mapping:
//     publishers/google/models/{id} for catalog ids.
//   - headers: content-type: application/json, the httpOptions headers
//     (User-Agent + model headers + options headers) and the auth header —
//     x-goog-api-key for express API keys, Authorization: Bearer for ADC (pi:
//     google-auth-library GoogleAuth with the cloud-platform scope). The
//     SDK's x-goog-api-client telemetry header is not replicated (declared
//     deviation; endpoints do not require it).
//   - auth: ADC resolves GOOGLE_APPLICATION_CREDENTIALS (service account or
//     authorized_user), then the gcloud well-known file
//     ~/.config/gcloud/application_default_credentials.json, then the GCE
//     metadata server (pi: google-auth-library's chain). Tokens are cached by
//     credential until expiry (google-auth-library's client cache equivalent).
//   - body: the vertex mapping of GenerateContentParameters — identical to
//     the mldev mapping for the surface pi uses (contents pass through;
//     config's systemInstruction/tools/toolConfig hoist to the top level,
//     temperature/maxOutputTokens/thinkingConfig stay under generationConfig).
//   - response: SSE data lines, each a GenerateContentResponse JSON chunk.
//
// The SDK's internal pRetry fetch loop (5 attempts on 408/429/5xx) is not
// replicated: the pi-level retryGoogleRequest wrapping the request provides
// the equivalent policy at the adapter boundary (same declared deviation as
// the google-generative-ai transport).
package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
	"github.com/samber/lo"
)

// GoogleVertexOptions are the google-vertex request options (pi:
// GoogleVertexOptions extends StreamOptions).
type GoogleVertexOptions struct {
	types.StreamOptions
	// ToolChoice is "auto", "none", or "any".
	ToolChoice string
	// Thinking configures thinking; BudgetTokens is -1 for dynamic, 0 to
	// disable; Level mirrors Google's ThinkingLevel enum values.
	Thinking *GoogleThinkingOptions
	// Project is the GCP project ID; falls back to
	// GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT.
	Project string
	// Location is the GCP location; falls back to GOOGLE_CLOUD_LOCATION.
	Location string
}

const (
	vertexAPIVersion           = "v1"
	gcpVertexCredentialsMarker = "gcp-vertex-credentials"

	// vertexResourceScopeCollection is the SDK's ResourceScope.COLLECTION: a
	// custom baseUrl is a collection-scoped root, so the project/location
	// path prefix is not prepended.
	vertexResourceScopeCollection = "COLLECTION"

	// vertexDefaultAPIVersion is the SDK's VERTEX_AI_API_DEFAULT_VERSION,
	// used when the client carries no explicit version (pi always passes
	// API_VERSION = "v1").
	vertexDefaultAPIVersion = "v1beta1"

	vertexCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
)

// pi's THINKING_LEVEL_MAP maps GoogleApiThinkingLevel values to the SDK's
// ThinkingLevel enum — the identical string values — so in pig the level
// string passes through unchanged.

// googleVertexToolCallCounter counts generated tool call ids (pi: the vertex
// module-local toolCallCounter).
var googleVertexToolCallCounter atomic.Int64

// vertexMultiRegionalLocations is the SDK's MULTI_REGIONAL_LOCATIONS.
var vertexMultiRegionalLocations = map[string]bool{"us": true, "eu": true}

// googleVertexAuthOptions mirrors pi's googleAuthOptions — the keyFilename
// from GOOGLE_APPLICATION_CREDENTIALS handed to the SDK's GoogleAuth.
type googleVertexAuthOptions struct {
	keyFilename string
}

// googleVertexHTTPOptions is pi's buildHttpOptions output (the SDK
// HttpOptions patch): fields carry meaning only when non-zero; apiVersion ""
// means the custom base URL already includes a version segment.
type googleVertexHTTPOptions struct {
	baseURL              string
	baseURLResourceScope string
	apiVersion           string
	headers              map[string]string
}

// googleVertexClient mirrors the @google/genai constructor config pi passes
// (pi: new GoogleGenAI({ vertexai: true, ... }) — the object pi's tests
// capture). URL and auth normalization follow the SDK's ApiClient constructor
// and request path at transport time.
type googleVertexClient struct {
	vertexAI          bool
	project           string
	location          string
	apiKey            string
	apiVersion        string
	googleAuthOptions *googleVertexAuthOptions
	httpOptions       googleVertexHTTPOptions
}

// googleVertexGenerateContentStream is the transport seam for
// client.models.generateContentStream (pi: vi.mock("@google/genai") stubs the
// SDK; tests override this var). The default implementation performs the
// native HTTP request and returns a pull-based chunk stream.
var googleVertexGenerateContentStream = func(ctx context.Context, client *googleVertexClient, params *googleGenerateContentParams) (googleChunkSource, error) {
	return client.generateContentStream(ctx, params)
}

// generateContentStream performs the native request (pi:
// client.models.generateContentStream over the vertex endpoint).
func (c *googleVertexClient) generateContentStream(ctx context.Context, params *googleGenerateContentParams) (googleChunkSource, error) {
	requestURL, err := c.buildVertexRequestURL(params)
	if err != nil {
		return nil, err
	}
	body, err := marshalGoogleGenerateContentBody(params)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range c.httpOptions.headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	if err := c.addAuthHeaders(ctx, request.Header); err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Request was aborted")
		}
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		bodyText, _ := io.ReadAll(response.Body)
		return nil, &utils.ProviderHTTPError{
			Status:  response.StatusCode,
			Headers: response.Header,
			Body:    string(bodyText),
			Message: fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		}
	}
	return &googleChunkStream{resp: response, body: bufio.NewReader(response.Body)}, nil
}

// buildVertexRequestURL composes the URL (pi: the SDK's ApiClient constructor
// normalization + constructUrl + shouldPrependVertexProjectPath + the vertex
// tModel mapping).
func (c *googleVertexClient) buildVertexRequestURL(params *googleGenerateContentParams) (string, error) {
	// SDK constructor normalization: project+location beats apiKey;
	// otherwise apiKey clears project/location.
	project, location, apiKey := c.project, c.location, c.apiKey
	if project != "" && location != "" {
		apiKey = ""
	} else if apiKey != "" {
		project, location = "", ""
	}
	customBase := c.httpOptions.baseURL
	// vertexai && no location && no apiKey && no custom base → "global".
	if location == "" && apiKey == "" && customBase == "" {
		location = "global"
	}
	hasSufficientAuth := (project != "" && location != "") || apiKey != ""
	if !hasSufficientAuth && customBase == "" {
		return "", errors.New("Authentication is not set up. Please provide either a project and location, or an API key, or a custom base URL.")
	}

	baseURL := ""
	hasConstructorAuth := (c.project != "" && c.location != "") || c.apiKey != ""
	switch {
	case customBase != "" && !hasConstructorAuth:
		// The SDK clears project/location for unauthenticated custom bases.
		baseURL = customBase
		project, location = "", ""
	case apiKey != "" || location == "global":
		baseURL = "https://aiplatform.googleapis.com/"
	case project != "" && location != "" && vertexMultiRegionalLocations[location]:
		baseURL = "https://aiplatform." + location + ".rep.googleapis.com/"
	case project != "" && location != "":
		baseURL = "https://" + location + "-aiplatform.googleapis.com/"
	}
	// pi: buildHttpOptions sets baseUrl whenever resolveCustomBaseUrl yields
	// one; the SDK patch overwrites the init value either way.
	if customBase != "" {
		baseURL = customBase
	}

	// The version segment is skipped when the effective base URL already
	// includes one (pi: buildHttpOptions apiVersion "" + getRequestUrlInternal).
	apiVersion := c.apiVersion
	if apiVersion == "" {
		apiVersion = vertexDefaultAPIVersion
	}
	if baseURLIncludesAPIVersion(baseURL) {
		apiVersion = ""
	}

	urlElements := []string{strings.TrimSuffix(baseURL, "/")}
	if apiVersion != "" {
		urlElements = append(urlElements, apiVersion)
	}
	path := tGoogleVertexModel(params.Model)
	// pi: shouldPrependVertexProjectPath — COLLECTION-scoped custom bases,
	// express API keys, and paths that already start with projects/ skip the
	// prefix.
	prependProjectLocation := true
	if baseURL != "" && c.httpOptions.baseURLResourceScope == vertexResourceScopeCollection {
		prependProjectLocation = false
	} else if apiKey != "" {
		prependProjectLocation = false
	} else if strings.HasPrefix(path, "projects/") {
		prependProjectLocation = false
	}
	if prependProjectLocation {
		urlElements = append(urlElements, "projects/"+project+"/locations/"+location)
	}
	urlElements = append(urlElements, path+":streamGenerateContent")
	return strings.Join(urlElements, "/") + "?alt=sse", nil
}

// addAuthHeaders sets the auth header: x-goog-api-key for express API keys,
// Authorization: Bearer for ADC (pi: the SDK's NodeAuth.addAuthHeaders).
func (c *googleVertexClient) addAuthHeaders(ctx context.Context, header http.Header) error {
	// SDK constructor normalization again (see buildVertexRequestURL):
	// project+location beats apiKey.
	apiKey := c.apiKey
	if c.project != "" && c.location != "" {
		apiKey = ""
	}
	if apiKey != "" {
		header.Set("x-goog-api-key", apiKey)
		return nil
	}
	keyFilename := ""
	if c.googleAuthOptions != nil {
		keyFilename = c.googleAuthOptions.keyFilename
	}
	token, err := googleVertexADCAuthorization(ctx, keyFilename)
	if err != nil {
		return err
	}
	header.Set("Authorization", "Bearer "+token)
	return nil
}

// tGoogleVertexModel is the SDK's vertex tModel mapping: publishers/,
// projects/, models/ prefixed ids pass through; ids with a slash map to
// publishers/{prefix}/models/{name}; everything else gets the google
// publisher prefix. Mirrors the JS split('/', 2): segments after the second
// are dropped. Path-escaping ids ("..", "?", "&") are not rejected (pi: the
// SDK throws 'invalid model parameter'; pig lets the request fail on the URL
// instead — declared deviation, pi never hits this with catalog ids).
func tGoogleVertexModel(model string) string {
	if model == "" {
		return ""
	}
	if strings.HasPrefix(model, "publishers/") ||
		strings.HasPrefix(model, "projects/") ||
		strings.HasPrefix(model, "models/") {
		return model
	}
	if idx := strings.Index(model, "/"); idx >= 0 {
		parts := strings.SplitN(model, "/", 3)
		return "publishers/" + parts[0] + "/models/" + parts[1]
	}
	return "publishers/google/models/" + model
}

// marshalGoogleGenerateContentBody serializes the request body (pi: the SDK's
// generateContentParametersToVertex — identical to the mldev mapping for the
// surface pi uses). Shared with the google-generative-ai transport.
func marshalGoogleGenerateContentBody(params *googleGenerateContentParams) ([]byte, error) {
	body := map[string]any{}
	if len(params.Contents) > 0 {
		body["contents"] = params.Contents
	}
	config := params.Config
	if config != nil {
		generationConfig := map[string]any{}
		if config.Temperature != nil {
			generationConfig["temperature"] = *config.Temperature
		}
		if config.MaxOutputTokens != nil {
			generationConfig["maxOutputTokens"] = *config.MaxOutputTokens
		}
		if config.ThinkingConfig != nil {
			generationConfig["thinkingConfig"] = config.ThinkingConfig
		}
		if len(generationConfig) > 0 {
			body["generationConfig"] = generationConfig
		}
		if config.SystemInstruction != nil {
			body["systemInstruction"] = config.SystemInstruction
		}
		if config.Tools != nil {
			body["tools"] = config.Tools
		}
		if config.ToolConfig != nil {
			body["toolConfig"] = config.ToolConfig
		}
	}
	return json.Marshal(body)
}

// GoogleVertex implements ProviderStreams for the google-vertex API (pi: the
// { stream, streamSimple } module object).
type GoogleVertex struct{}

var _ types.ProviderStreams = GoogleVertex{}

// pi: compat.ts registers the builtin API implementations at module load;
// pig's layering rule (ai cannot import ai/api) puts the registration in the
// adapter package's init instead.
func init() {
	ai.RegisterBuiltInApiProvider(ai.ApiGoogleVertex, GoogleVertex{})
	ai.SetBuiltinApiRegistrar(func() {
		ai.RegisterBuiltInApiProvider(ai.ApiGoogleVertex, GoogleVertex{})
	})
}

// Stream dispatches one streaming request (pi: stream). ctx carries the
// caller's cancellation (pi: options.signal into the SDK request, buildParams
// pre-check and the retry helper).
func (GoogleVertex) Stream(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *types.AssistantMessageEventStream {
	opts := &GoogleVertexOptions{}
	if options != nil {
		opts.StreamOptions = *options
	}
	return streamGoogleVertex(ctx, model, llmContext, opts)
}

// StreamSimple dispatches one simple streaming request (pi: streamSimple).
// Unlike the google-generative-ai adapter there is no "no API key" throw:
// an absent key selects the ADC client.
func (GoogleVertex) StreamSimple(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
	// pi: { ...buildBaseOptions(model, context, options, undefined), ... } —
	// the undefined apiKey falls through to options.apiKey.
	base := BuildBaseOptions(model, llmContext, options, "")
	opts := &GoogleVertexOptions{StreamOptions: base, ToolChoice: string(options.ToolChoice)}
	if options.Reasoning == "" {
		opts.Thinking = &GoogleThinkingOptions{Enabled: false}
		return streamGoogleVertex(ctx, model, llmContext, opts)
	}

	clampedReasoning := ai.ClampThinkingLevel(model, types.ModelThinkingLevel(options.Reasoning))
	if clampedReasoning == types.ModelThinkingOff {
		opts.Thinking = &GoogleThinkingOptions{Enabled: false}
		return streamGoogleVertex(ctx, model, llmContext, opts)
	}
	resolvedLevel, err := ResolveGoogleThinkingLevel(model, clampedReasoning)
	if err != nil {
		return immediateErrorStream(model, err)
	}

	if UsesGoogleThinkingLevel(model) {
		opts.Thinking = &GoogleThinkingOptions{
			Enabled: true,
			Level:   ToGoogleThinkingLevel(resolvedLevel),
		}
		return streamGoogleVertex(ctx, model, llmContext, opts)
	}

	opts.Thinking = &GoogleThinkingOptions{
		Enabled:      true,
		BudgetTokens: lo.ToPtr(getGoogleVertexBudget(model, resolvedLevel, options.ThinkingBudgets)),
	}
	return streamGoogleVertex(ctx, model, llmContext, opts)
}

// streamGoogleVertex is the adapter's core stream implementation (pi: stream).
func streamGoogleVertex(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *GoogleVertexOptions) *types.AssistantMessageEventStream {
	stream := types.NewAssistantMessageEventStream()
	normalizedContext := utils.CollapseSystemMessages(llmContext)
	output := &types.AssistantMessage{
		Content:  []types.Content{},
		API:      ai.ApiGoogleVertex,
		Provider: model.Provider,
		Model:    model.ID,
		Usage: types.Usage{
			Cost: types.Cost{},
		},
		StopReason: types.StopReasonPending,
		Timestamp:  time.Now().UnixMilli(),
	}

	// push keeps the stream's partial pointer a snapshot of the mutable
	// output (pi pushes the same object; pig's event consumers treat Partial
	// as a value snapshot).
	push := func(event types.AssistantMessageEvent) {
		partial := *output
		event.Partial = &partial
		stream.Push(event)
	}

	go func() {
		defer stream.EndWith(*output)
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					failGoogleVertexStream(ctx, output, stream, err)
				} else {
					failGoogleVertexStream(ctx, output, stream, fmt.Errorf("%v", r))
				}
			}
		}()

		if err := runGoogleVertexStream(ctx, model, normalizedContext, options, output, push); err != nil {
			failGoogleVertexStream(ctx, output, stream, err)
		}
	}()

	return stream
}

// failGoogleVertexStream is pi's catch block: aborts normalize to the aborted
// stop reason, everything else to error, with the formatted provider message.
func failGoogleVertexStream(ctx context.Context, output *types.AssistantMessage, stream *types.AssistantMessageEventStream, err error) {
	if ctx.Err() != nil {
		output.StopReason = types.StopReasonAborted
	} else {
		output.StopReason = types.StopReasonError
	}
	output.ErrorMessage = utils.FormatProviderError(utils.NormalizeProviderError(err), "")
	stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: output.StopReason, Message: output})
}

// runGoogleVertexStream is pi's try block (the streaming loop).
func runGoogleVertexStream(ctx context.Context, model *types.Model, normalizedContext *types.TranscriptContext, options *GoogleVertexOptions, output *types.AssistantMessage, push func(types.AssistantMessageEvent)) error {
	if options.Fetch != nil {
		return errors.New("Custom fetch is not supported by the Google Vertex adapter")
	}
	var client *googleVertexClient
	if apiKey := resolveGoogleVertexAPIKey(options); apiKey != "" {
		// Vertex API key (express mode) client.
		client = createGoogleVertexAPIKeyClient(model, apiKey, options.Headers)
	} else {
		project, err := resolveGoogleVertexProject(options)
		if err != nil {
			return err
		}
		location, err := resolveGoogleVertexLocation(options)
		if err != nil {
			return err
		}
		client = createGoogleVertexClient(model, project, location, options.Headers, options.Env)
	}
	params, err := buildGoogleVertexParams(ctx, model, normalizedContext, options)
	if err != nil {
		return err
	}
	if options.OnPayload != nil {
		if next := options.OnPayload(params, model); next != nil {
			if nextParams, ok := next.(*googleGenerateContentParams); ok {
				params = nextParams
			}
		}
	}
	maxRetries := 0
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}
	chunkStream, err := RetryGoogleRequest(func() (googleChunkSource, error) {
		return googleVertexGenerateContentStream(ctx, client, params)
	}, GoogleRetryOptions{MaxRetries: maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, Ctx: ctx})
	if err != nil {
		return err
	}
	defer chunkStream.Close()

	push(types.AssistantMessageEvent{Type: types.EventStart, Partial: output})
	var currentBlock *googleStreamBlock
	blockIndex := func() int { return len(output.Content) - 1 }

	for {
		chunk, err := chunkStream.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		// Vertex uses the same @google/genai GenerateContentResponse type as
		// Gemini; responseId is documented as an output-only identifier for
		// each response. Keep the first non-empty one.
		if output.ResponseID == "" {
			output.ResponseID = chunk.ResponseID
		}
		if len(chunk.Candidates) > 0 {
			candidate := &chunk.Candidates[0]
			if candidate.Content != nil {
				for i := range candidate.Content.Parts {
					part := &candidate.Content.Parts[i]
					if part.Text != nil {
						isThinking := IsThinkingPart(part)
						if currentBlock == nil ||
							(isThinking && currentBlock.kind != "thinking") ||
							(!isThinking && currentBlock.kind != "text") {
							if currentBlock != nil {
								if currentBlock.kind == "text" {
									push(types.AssistantMessageEvent{
										Type: types.EventTextEnd, ContentIndex: blockIndex(), Content: currentBlock.text, Partial: output,
									})
								} else {
									push(types.AssistantMessageEvent{
										Type: types.EventThinkingEnd, ContentIndex: blockIndex(), Content: currentBlock.thinking, Partial: output,
									})
								}
							}
							if isThinking {
								currentBlock = &googleStreamBlock{kind: "thinking"}
								output.Content = append(output.Content, types.NewThinkingContent(""))
								push(types.AssistantMessageEvent{Type: types.EventThinkingStart, ContentIndex: blockIndex(), Partial: output})
							} else {
								currentBlock = &googleStreamBlock{kind: "text"}
								output.Content = append(output.Content, types.NewTextContent(""))
								push(types.AssistantMessageEvent{Type: types.EventTextStart, ContentIndex: blockIndex(), Partial: output})
							}
						}
						if currentBlock.kind == "thinking" {
							currentBlock.thinking += *part.Text
							currentBlock.signature = RetainThoughtSignature(currentBlock.signature, part.ThoughtSignature)
							entry := types.NewThinkingContent(currentBlock.thinking)
							entry.ThinkingSignature = currentBlock.signature
							output.Content[blockIndex()] = entry
							push(types.AssistantMessageEvent{
								Type: types.EventThinkingDelta, ContentIndex: blockIndex(), Delta: *part.Text, Partial: output,
							})
						} else {
							currentBlock.text += *part.Text
							currentBlock.signature = RetainThoughtSignature(currentBlock.signature, part.ThoughtSignature)
							entry := types.NewTextContent(currentBlock.text)
							entry.TextSignature = currentBlock.signature
							output.Content[blockIndex()] = entry
							push(types.AssistantMessageEvent{
								Type: types.EventTextDelta, ContentIndex: blockIndex(), Delta: *part.Text, Partial: output,
							})
						}
					}

					if part.FunctionCall != nil {
						if currentBlock != nil {
							if currentBlock.kind == "text" {
								push(types.AssistantMessageEvent{
									Type: types.EventTextEnd, ContentIndex: blockIndex(), Content: currentBlock.text, Partial: output,
								})
							} else {
								push(types.AssistantMessageEvent{
									Type: types.EventThinkingEnd, ContentIndex: blockIndex(), Content: currentBlock.thinking, Partial: output,
								})
							}
							currentBlock = nil
						}

						// Generate a unique ID if not provided or if it's a duplicate.
						providedID := part.FunctionCall.ID
						needsNewID := providedID == ""
						if !needsNewID {
							for _, block := range output.Content {
								if call, ok := block.(types.ToolCall); ok && call.ID == providedID {
									needsNewID = true
									break
								}
							}
						}
						toolCallID := providedID
						if needsNewID {
							toolCallID = fmt.Sprintf("%s_%d_%d", part.FunctionCall.Name, time.Now().UnixMilli(), googleVertexToolCallCounter.Add(1))
						}

						args := part.FunctionCall.Args
						if args == nil {
							args = map[string]any{}
						}
						toolCall := types.ToolCall{
							Kind:      types.KindToolCall,
							ID:        toolCallID,
							Name:      part.FunctionCall.Name,
							Arguments: args,
						}
						if part.ThoughtSignature != "" {
							toolCall.ThoughtSignature = part.ThoughtSignature
						}

						output.Content = append(output.Content, toolCall)
						push(types.AssistantMessageEvent{Type: types.EventToolCallStart, ContentIndex: blockIndex(), Partial: output})
						argumentsJSON, _ := json.Marshal(toolCall.Arguments)
						push(types.AssistantMessageEvent{
							Type: types.EventToolCallDelta, ContentIndex: blockIndex(), Delta: string(argumentsJSON), Partial: output,
						})
						push(types.AssistantMessageEvent{Type: types.EventToolCallEnd, ContentIndex: blockIndex(), ToolCall: &toolCall, Partial: output})
					}
				}
			}

			if candidate.FinishReason != "" {
				output.RawStopReason = candidate.FinishReason
				stopReason, mapErr := MapGoogleStopReason(candidate.FinishReason)
				if mapErr != nil {
					return mapErr
				}
				output.StopReason = stopReason
				hasToolCall := false
				for _, block := range output.Content {
					if _, ok := block.(types.ToolCall); ok {
						hasToolCall = true
						break
					}
				}
				if hasToolCall && output.StopReason == types.StopReasonStop {
					output.StopReason = types.StopReasonToolUse
				}
			}
		}

		if chunk.UsageMetadata != nil {
			usage := chunk.UsageMetadata
			output.Usage = types.Usage{
				Input:       usage.PromptTokenCount - usage.CachedContentTokenCount,
				Output:      usage.CandidatesTokenCount + usage.ThoughtsTokenCount,
				CacheRead:   usage.CachedContentTokenCount,
				CacheWrite:  0,
				Reasoning:   lo.ToPtr(usage.ThoughtsTokenCount),
				TotalTokens: usage.TotalTokenCount,
				Cost:        types.Cost{},
			}
			ai.CalculateCost(model, &output.Usage)
		}
	}

	if currentBlock != nil {
		if currentBlock.kind == "text" {
			push(types.AssistantMessageEvent{
				Type:         types.EventTextEnd,
				ContentIndex: blockIndex(),
				Content:      currentBlock.text,
				Partial:      output,
			})
		} else {
			push(types.AssistantMessageEvent{
				Type:         types.EventThinkingEnd,
				ContentIndex: blockIndex(),
				Content:      currentBlock.thinking,
				Partial:      output,
			})
		}
	}

	if ctx.Err() != nil {
		return errors.New("Request was aborted")
	}

	if output.StopReason == types.StopReasonPending {
		return errors.New("Google Vertex stream ended without a finish reason")
	}
	if output.StopReason == types.StopReasonAborted || output.StopReason == types.StopReasonError {
		if output.RawStopReason != "" {
			return fmt.Errorf("Provider stopped with: %s", output.RawStopReason)
		}
		return errors.New("An unknown error occurred")
	}

	push(types.AssistantMessageEvent{Type: types.EventDone, Reason: output.StopReason, Message: output})
	return nil
}

// createGoogleVertexClient assembles the ADC client (pi: createClient).
func createGoogleVertexClient(model *types.Model, project, location string, optionsHeaders types.ProviderHeaders, env types.ProviderEnv) *googleVertexClient {
	client := &googleVertexClient{
		vertexAI:   true,
		project:    project,
		location:   location,
		apiVersion: vertexAPIVersion,
	}
	if authOptions := buildGoogleVertexAuthOptions(env); authOptions != nil {
		client.googleAuthOptions = authOptions
	}
	client.httpOptions = buildGoogleVertexHTTPOptions(model, optionsHeaders)
	return client
}

// createGoogleVertexAPIKeyClient assembles the Vertex express API key client
// (pi: createClientWithApiKey).
func createGoogleVertexAPIKeyClient(model *types.Model, apiKey string, optionsHeaders types.ProviderHeaders) *googleVertexClient {
	return &googleVertexClient{
		vertexAI:    true,
		apiKey:      apiKey,
		apiVersion:  vertexAPIVersion,
		httpOptions: buildGoogleVertexHTTPOptions(model, optionsHeaders),
	}
}

// buildGoogleVertexHTTPOptions assembles the SDK httpOptions patch (pi:
// buildHttpOptions).
func buildGoogleVertexHTTPOptions(model *types.Model, optionsHeaders types.ProviderHeaders) googleVertexHTTPOptions {
	options := googleVertexHTTPOptions{}
	if baseURL := resolveGoogleVertexCustomBaseURL(model.BaseURL); baseURL != "" {
		options.baseURL = baseURL
		options.baseURLResourceScope = vertexResourceScopeCollection
		if baseURLIncludesAPIVersion(baseURL) {
			options.apiVersion = ""
		}
	}
	headers := map[string]string{"User-Agent": utils.GetPiUserAgent()}
	for key, value := range model.Headers {
		headers[key] = value
	}
	for key, value := range optionsHeaders {
		if value != nil {
			headers[key] = *value
		} else {
			// pi: a null header suppresses the default value.
			delete(headers, key)
		}
	}
	options.headers = headers
	return options
}

// resolveGoogleVertexCustomBaseURL resolves a model.baseUrl that may carry
// the {location} placeholder: empty or placeholder base URLs use the SDK
// default regional endpoint (pi: resolveCustomBaseUrl).
func resolveGoogleVertexCustomBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" || strings.Contains(trimmed, "{location}") {
		return ""
	}
	return trimmed
}

var googleVertexAPIVersionPartRe = regexp.MustCompile(`^v\d+(?:beta\d*)?$`)
var googleVertexAPIVersionFallbackRe = regexp.MustCompile(`(?:^|/)v\d+(?:beta\d*)?(?:/|$)`)

// baseURLIncludesAPIVersion reports whether the base URL path contains a
// version segment (pi: baseUrlIncludesApiVersion — /^v\d+(?:beta\d*)?$/ per
// path segment; on an unparseable URL a regex over the raw string).
func baseURLIncludesAPIVersion(baseURL string) bool {
	if parsed, err := url.Parse(baseURL); err == nil {
		for _, part := range strings.Split(parsed.Path, "/") {
			if googleVertexAPIVersionPartRe.MatchString(part) {
				return true
			}
		}
		return false
	}
	return googleVertexAPIVersionFallbackRe.MatchString(baseURL)
}

// buildGoogleVertexAuthOptions resolves the keyFilename from the provider env
// (pi: buildGoogleAuthOptions — GOOGLE_APPLICATION_CREDENTIALS).
func buildGoogleVertexAuthOptions(env types.ProviderEnv) *googleVertexAuthOptions {
	keyFilename := utils.GetProviderEnvValue("GOOGLE_APPLICATION_CREDENTIALS", env)
	if keyFilename == "" {
		return nil
	}
	return &googleVertexAuthOptions{keyFilename: keyFilename}
}

var googleVertexPlaceholderAPIKeyRe = regexp.MustCompile(`^<[^>]+>$`)

// resolveGoogleVertexAPIKey resolves the express API key from options: empty,
// the gcp-vertex-credentials marker, and <placeholder> keys all select the
// ADC client (pi: resolveApiKey + isPlaceholderApiKey).
func resolveGoogleVertexAPIKey(options *GoogleVertexOptions) string {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" || apiKey == gcpVertexCredentialsMarker || googleVertexPlaceholderAPIKeyRe.MatchString(apiKey) {
		return ""
	}
	return apiKey
}

// resolveGoogleVertexProject resolves the GCP project (pi: resolveProject).
func resolveGoogleVertexProject(options *GoogleVertexOptions) (string, error) {
	project := options.Project
	if project == "" {
		project = utils.GetProviderEnvValue("GOOGLE_CLOUD_PROJECT", options.Env)
	}
	if project == "" {
		project = utils.GetProviderEnvValue("GCLOUD_PROJECT", options.Env)
	}
	if project == "" {
		return "", errors.New("Vertex AI requires a project ID. Set GOOGLE_CLOUD_PROJECT/GCLOUD_PROJECT or pass project in options.")
	}
	return project, nil
}

// resolveGoogleVertexLocation resolves the GCP location (pi:
// resolveLocation).
func resolveGoogleVertexLocation(options *GoogleVertexOptions) (string, error) {
	location := options.Location
	if location == "" {
		location = utils.GetProviderEnvValue("GOOGLE_CLOUD_LOCATION", options.Env)
	}
	if location == "" {
		return "", errors.New("Vertex AI requires a location. Set GOOGLE_CLOUD_LOCATION or pass location in options.")
	}
	return location, nil
}

// buildGoogleVertexParams builds the request the SDK receives (pi:
// buildParams).
func buildGoogleVertexParams(ctx context.Context, model *types.Model, normalizedContext *types.TranscriptContext, options *GoogleVertexOptions) (*googleGenerateContentParams, error) {
	// pi: config.abortSignal — aborted before the request → "Request aborted".
	if ctx.Err() != nil {
		return nil, errors.New("Request aborted")
	}
	contents := ConvertGoogleMessages(model, normalizedContext)

	config := &googleGenerateContentConfig{}
	if options.Temperature != nil {
		config.Temperature = options.Temperature
	}
	if options.MaxTokens != nil {
		config.MaxOutputTokens = options.MaxTokens
	}

	supportsStrictMode := SupportsGoogleStrictToolSampling(model.ID)
	currentTools := utils.GetCurrentTools(normalizedContext.Messages)
	var functionCallingMode string
	if len(currentTools) > 0 {
		mode, err := ResolveGoogleFunctionCallingMode(currentTools, options.ToolChoice, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		functionCallingMode = mode
	}
	initialSystemMessage := utils.GetInitialSystemMessage(normalizedContext.Messages)
	if initialSystemMessage != nil {
		systemInstruction := utils.GetSystemMessageText(initialSystemMessage)
		if systemInstruction != "" {
			config.SystemInstruction = &googleContent{
				Parts: []googlePart{{Text: lo.ToPtr(utils.SanitizeSurrogates(systemInstruction))}},
			}
		}
	}
	if len(currentTools) > 0 {
		tools, err := ConvertGoogleTools(currentTools, false, supportsStrictMode)
		if err != nil {
			panic(err)
		}
		config.Tools = tools
	}
	if functionCallingMode != "" {
		config.ToolConfig = &googleToolConfig{
			FunctionCallingConfig: &googleFunctionCallingConfig{Mode: functionCallingMode},
		}
	}

	if options.Thinking != nil && options.Thinking.Enabled && model.Reasoning {
		thinkingConfig := &googleThinkingConfig{IncludeThoughts: true}
		if options.Thinking.Level != "" {
			// pi: THINKING_LEVEL_MAP[options.thinking.level] — the identity
			// map over the shared string values.
			thinkingConfig.ThinkingLevel = options.Thinking.Level
		} else if options.Thinking.BudgetTokens != nil {
			thinkingConfig.ThinkingBudget = options.Thinking.BudgetTokens
		}
		config.ThinkingConfig = thinkingConfig
	} else if model.Reasoning && options.Thinking != nil && !options.Thinking.Enabled {
		config.ThinkingConfig = GetDisabledGoogleThinkingConfig(model)
	}

	return &googleGenerateContentParams{
		Model:    model.ID,
		Contents: contents,
		Config:   config,
	}, nil
}

// getGoogleVertexBudget resolves the thinking budget for token-based models
// (pi: the vertex getGoogleBudget — no flash-lite branch; flash budgets apply
// to the whole 2.5-flash family).
func getGoogleVertexBudget(model *types.Model, level types.ModelThinkingLevel, customBudgets *types.ThinkingBudgets) int64 {
	if customBudgets != nil {
		var budget *int64
		switch level {
		case types.ModelThinkingMinimal:
			budget = customBudgets.Minimal
		case types.ModelThinkingLow:
			budget = customBudgets.Low
		case types.ModelThinkingMedium:
			budget = customBudgets.Medium
		case types.ModelThinkingHigh:
			budget = customBudgets.High
		}
		if budget != nil {
			return *budget
		}
	}

	if strings.Contains(model.ID, "2.5-pro") {
		switch level {
		case types.ModelThinkingMinimal:
			return 128
		case types.ModelThinkingLow:
			return 2048
		case types.ModelThinkingMedium:
			return 8192
		case types.ModelThinkingHigh:
			return 32768
		}
	}
	if strings.Contains(model.ID, "2.5-flash") {
		switch level {
		case types.ModelThinkingMinimal:
			return 128
		case types.ModelThinkingLow:
			return 2048
		case types.ModelThinkingMedium:
			return 8192
		case types.ModelThinkingHigh:
			return 24576
		}
	}
	return -1
}

// --- ADC (pi: google-auth-library GoogleAuth subset) ---

// adcTokenEntry is one cached access token.
type adcTokenEntry struct {
	token     string
	expiresAt time.Time
}

// googleVertexADCTokenCache caches access tokens by credential identity
// (google-auth-library's per-client token cache; pig keys the package-level
// cache by credential path).
var googleVertexADCTokenCache = struct {
	sync.Mutex
	entries map[string]adcTokenEntry
}{entries: map[string]adcTokenEntry{}}

// googleVertexADCAuthorization resolves Application Default Credentials to an
// access token (pi: google-auth-library GoogleAuth with the cloud-platform
// scope). Seam for tests; the default implementation walks the ADC chain.
var googleVertexADCAuthorization = func(ctx context.Context, keyFilename string) (string, error) {
	return googleVertexADCAuthorizationDefault(ctx, keyFilename)
}

// vertexADCWellKnownPath is the gcloud ADC file (pi: VERTEX_ADC_PATH in
// providers/google-vertex.ts; google-auth-library's well-known location).
const vertexADCWellKnownPath = "~/.config/gcloud/application_default_credentials.json"

func googleVertexADCAuthorizationDefault(ctx context.Context, keyFilename string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := keyFilename
	if path == "" {
		path = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}
	if path != "" {
		return googleVertexADCTokenFromFile(ctx, path)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		wellKnown := strings.TrimPrefix(vertexADCWellKnownPath, "~")
		path = filepath.Join(home, wellKnown)
		if _, statErr := os.Stat(path); statErr == nil {
			return googleVertexADCTokenFromFile(ctx, path)
		}
	}
	// No credential file: fall back to the GCE metadata server
	// (google-auth-library's compute credentials).
	return googleVertexMetadataToken(ctx)
}

func googleVertexADCTokenFromFile(ctx context.Context, path string) (string, error) {
	if entry, ok := googleVertexCachedADCToken(path); ok {
		return entry, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read Google Application Default Credentials from %s: %w", path, err)
	}
	var cred struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &cred); err != nil {
		return "", fmt.Errorf("invalid Google Application Default Credentials in %s: %w", path, err)
	}
	var token string
	switch cred.Type {
	case "authorized_user":
		token, err = googleVertexRefreshUserToken(ctx, path, data)
	case "service_account":
		token, err = googleVertexServiceAccountToken(ctx, path, data)
	default:
		return "", fmt.Errorf("unsupported Google Application Default Credentials type %q in %s", cred.Type, path)
	}
	if err != nil {
		return "", err
	}
	googleVertexADCTokenCache.Lock()
	googleVertexADCTokenCache.entries[path] = adcTokenEntry{token: token, expiresAt: time.Now().Add(55 * time.Minute)}
	googleVertexADCTokenCache.Unlock()
	return token, nil
}

func googleVertexCachedADCToken(path string) (string, bool) {
	googleVertexADCTokenCache.Lock()
	defer googleVertexADCTokenCache.Unlock()
	entry, ok := googleVertexADCTokenCache.entries[path]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.token, true
}

// googleVertexRefreshUserToken exchanges an authorized_user refresh token
// (pi: google-auth-library UserRefreshClient).
func googleVertexRefreshUserToken(ctx context.Context, path string, data []byte) (string, error) {
	var cred struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &cred); err != nil {
		return "", fmt.Errorf("invalid authorized_user credentials in %s: %w", path, err)
	}
	return googleVertexTokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cred.ClientID},
		"client_secret": {cred.ClientSecret},
		"refresh_token": {cred.RefreshToken},
	})
}

// googleVertexServiceAccountToken signs a JWT bearer assertion with the
// service account key (pi: google-auth-library JWT).
func googleVertexServiceAccountToken(ctx context.Context, path string, data []byte) (string, error) {
	var cred struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal(data, &cred); err != nil {
		return "", fmt.Errorf("invalid service_account credentials in %s: %w", path, err)
	}
	block, _ := pem.Decode([]byte(cred.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("invalid private key in service_account credentials in %s", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if rsaKey, rsaErr := x509.ParsePKCS1PrivateKey(block.Bytes); rsaErr == nil {
			key = rsaKey
			err = nil
		} else {
			return "", fmt.Errorf("invalid private key in service_account credentials in %s: %w", path, err)
		}
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("unsupported private key type in service_account credentials in %s", path)
	}
	now := time.Now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   cred.ClientEmail,
		"scope": vertexCloudPlatformScope,
		"aud":   "https://oauth2.googleapis.com/token",
		"exp":   now + 3600,
		"iat":   now,
	})
	assertion := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(assertion))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("could not sign service account JWT: %w", err)
	}
	assertion += "." + base64.RawURLEncoding.EncodeToString(signature)
	return googleVertexTokenRequest(ctx, url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
}

// googleVertexMetadataToken fetches a token from the GCE metadata server
// (google-auth-library's compute credentials fallback).
func googleVertexMetadataToken(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Metadata-Flavor", "Google")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("could not resolve Google Application Default Credentials: no credential file and the metadata server is unreachable: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("metadata server returned %d: %s", response.StatusCode, string(body))
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("metadata server returned an invalid token response")
	}
	return token.AccessToken, nil
}

func googleVertexTokenRequest(ctx context.Context, form url.Values) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("Request was aborted")
		}
		return "", fmt.Errorf("Google token request failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Google token request returned %d: %s", response.StatusCode, string(body))
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		return "", fmt.Errorf("Google token request returned an invalid response")
	}
	return token.AccessToken, nil
}
