// Full-chain transport probe: a local server emitting hand-encoded
// vnd.amazon.eventstream frames (messageStart / contentBlockDelta /
// contentBlockStop / metadata / messageStop), driven through the real
// aws-sdk-go-v2 client with bearer-token auth. Locks the wire decode path the
// seam-stubbed suites cannot reach (pig analog of pi's response-headers e2e,
// extended to a complete stream).
package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// bedrockEventFrame encodes one event-stream message frame (AWS event stream
// encoding: prelude {total, headers-len, prelude-crc} + headers + payload +
// message-crc).
func bedrockEventFrame(t *testing.T, headers map[string]string, payload map[string]any) []byte {
	t.Helper()
	headerBuf := []byte{}
	for name, value := range headers {
		headerBuf = append(headerBuf, byte(len(name)))
		headerBuf = append(headerBuf, name...)
		headerBuf = append(headerBuf, 7) // string value type
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(value)))
		headerBuf = append(headerBuf, length[:]...)
		headerBuf = append(headerBuf, value...)
	}
	payloadBuf, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	total := 12 + len(headerBuf) + len(payloadBuf) + 4
	frame := make([]byte, 0, total)
	var prelude [12]byte
	binary.BigEndian.PutUint32(prelude[0:4], uint32(total))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(headerBuf)))
	binary.BigEndian.PutUint32(prelude[8:12], crc32.ChecksumIEEE(prelude[0:8]))
	frame = append(frame, prelude[:]...)
	frame = append(frame, headerBuf...)
	frame = append(frame, payloadBuf...)
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	return frame
}

func TestBedrockConverseStreamFullChainTransportE2E(t *testing.T) {
	resetBedrockEnv(t)

	events := []struct {
		eventType string
		payload   map[string]any
	}{
		{"messageStart", map[string]any{"role": "assistant"}},
		{"contentBlockDelta", map[string]any{
			"contentBlockIndex": 0, "delta": map[string]any{"text": "Hello from Bedrock"},
		}},
		{"contentBlockStop", map[string]any{"contentBlockIndex": 0}},
		{"messageStop", map[string]any{"stopReason": "end_turn"}},
		{"metadata", map[string]any{
			"usage": map[string]any{"inputTokens": 12, "outputTokens": 5, "totalTokens": 17},
		}},
	}

	var responses []types.ProviderResponse
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// AWS_BEDROCK_SKIP_AUTH = dummy SigV4 credentials (the bearer scheme
		// requires HTTPS, so the skip path signs over plain HTTP like pi's
		// response-headers e2e).
		if got := r.Header.Get("Authorization"); len(got) < len("AWS4-HMAC-SHA256") || got[:16] != "AWS4-HMAC-SHA256" {
			t.Errorf("missing SigV4 signature, got Authorization %q", got)
		}
		if got := r.URL.Path; got != "/model/us.anthropic.claude-haiku-4-5-20251001-v1:0/converse-stream" {
			t.Errorf("request path = %q", got)
		}
		w.Header().Set("content-type", "application/vnd.amazon.eventstream")
		w.Header().Set("x-amzn-requestid", "req-e2e")
		w.WriteHeader(http.StatusOK)
		for _, event := range events {
			frame := bedrockEventFrame(t, map[string]string{
				":message-type": "event",
				":event-type":   event.eventType,
			}, event.payload)
			if _, err := w.Write(frame); err != nil {
				t.Errorf("write frame: %v", err)
			}
		}
	}))
	defer server.Close()

	baseModel, ok := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-haiku-4-5-20251001-v1:0"]
	if !ok {
		t.Fatalf("expected %s in the builtin catalog", bedrockResponseHeaderModelID)
	}
	model := baseModel
	model.BaseURL = server.URL

	s := BedrockConverseStream{}.StreamSimple(context.Background(), &model, bedrockTestContext(), &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"},
			},
		},
	})
	var kinds []types.AssistantMessageEventType
	for event := range s.Events() {
		kinds = append(kinds, event.Type)
	}
	message := s.Result()

	if message.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", message.StopReason, message.ErrorMessage)
	}
	if text, ok := message.Content[0].(types.TextContent); !ok || text.Text != "Hello from Bedrock" {
		t.Fatalf("text = %+v", message.Content[0])
	}
	if message.Usage.Input != 12 || message.Usage.Output != 5 || message.Usage.TotalTokens != 17 {
		t.Fatalf("usage = %+v", message.Usage)
	}
	if len(responses) != 0 {
		t.Fatalf("onResponse fired without a callback: %v", responses)
	}
	want := []types.AssistantMessageEventType{
		types.EventStart, types.EventTextStart, types.EventTextDelta, types.EventTextEnd, types.EventDone,
	}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", kinds, want)
		}
	}
}

// TestBedrockConverseStreamCacheDetails1hCost prices the 1h cache details at
// 2x while preserving the total cache write (pi
// test/bedrock-cache-write-1h-cost.test.ts, regression for pi #9457; the TS
// mock of the AWS SDK becomes a local eventstream server here, mirroring the
// full-chain transport probe above).
func TestBedrockConverseStreamCacheDetails1hCost(t *testing.T) {
	resetBedrockEnv(t)

	events := []struct {
		eventType string
		payload   map[string]any
	}{
		{"messageStart", map[string]any{"role": "assistant"}},
		{"metadata", map[string]any{
			"usage": map[string]any{
				"inputTokens":           100,
				"outputTokens":          5,
				"totalTokens":           1_000_105,
				"cacheWriteInputTokens": 1_000_000,
				"cacheDetails": []any{
					map[string]any{"ttl": "1h", "inputTokens": 150_000},
					map[string]any{"ttl": "5m", "inputTokens": 600_000},
					map[string]any{"ttl": "1h", "inputTokens": 250_000},
				},
			},
		}},
		{"messageStop", map[string]any{"stopReason": "end_turn"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		for _, event := range events {
			frame := bedrockEventFrame(t, map[string]string{
				":message-type": "event",
				":event-type":   event.eventType,
			}, event.payload)
			if _, err := w.Write(frame); err != nil {
				t.Errorf("write frame: %v", err)
			}
		}
	}))
	defer server.Close()

	baseModel, ok := ai.MODELS["amazon-bedrock"]["us.anthropic.claude-opus-4-8"]
	if !ok {
		t.Skip("us.anthropic.claude-opus-4-8 not in the builtin catalog")
	}
	model := baseModel
	model.BaseURL = server.URL

	s := BedrockConverseStream{}.StreamSimple(context.Background(), &model, bedrockTestContext(), &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				Env: types.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"},
			},
		},
	})
	for range s.Events() {
	}
	message := s.Result()
	if message.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", message.StopReason, message.ErrorMessage)
	}

	if message.Usage.CacheWrite != 1_000_000 {
		t.Fatalf("cacheWrite = %+v, want 1000000", message.Usage.CacheWrite)
	}
	if message.Usage.CacheWrite1h == nil || *message.Usage.CacheWrite1h != 400_000 {
		t.Fatalf("cacheWrite1h = %+v, want 400000", message.Usage.CacheWrite1h)
	}
	expectedCacheWriteCost := (600_000*model.Cost.CacheWrite + 400_000*model.Cost.Input*2) / 1_000_000
	if diff := message.Usage.Cost.CacheWrite - expectedCacheWriteCost; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost.cacheWrite = %v, want ~%v", message.Usage.Cost.CacheWrite, expectedCacheWriteCost)
	}
}
