// In-process mock OpenAI-completions SSE server for the memory-leak probe
// workflow (docs/fix/2026-09-14-tui-turn-memory-leak.md). Lives in a _test.go
// file on purpose: it never ships in the pig binary and needs no separate
// testdata main package. It is a probe HARNESS, not an assertion — it serves
// until the process is stopped, so it skips unless explicitly requested:
//
//	PIG_MEMLEAK_MOCK=1 go test ./codingagent/modes/interactive/ -run TestMemLeakMockServer -v -timeout 60m
//
// then point a real pig at it (models.json baseUrl
// http://127.0.0.1:18923/v1). Env: SINGLE_CHUNK=1 emits the whole reply as
// one content chunk (no streaming frames) for the per-chunk control
// experiment. Stop with Ctrl+C — the go test process owns the port.
package interactive

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const memLeakMockAddr = "127.0.0.1:18923"

// memLeakMockHandler serves POST /v1/chat/completions with a streamed
// assistant reply, mirroring the OpenAI-completions SSE framing.
func memLeakMockHandler(singleChunk bool) http.Handler {
	mux := http.NewServeMux()
	requests := 0
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		requests++
		log.Printf("REQ %d from %s", requests, r.RemoteAddr)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)

		send := func(payload string) {
			fmt.Fprintf(w, "data: %s\n\n", payload)
			fl.Flush()
		}
		chunk := func(delta string, finish *string) {
			d := map[string]any{}
			if delta != "" {
				d["role"] = "assistant"
				d["content"] = delta
			}
			c := map[string]any{"index": 0, "delta": d}
			if finish != nil {
				c["finish_reason"] = *finish
			}
			b, _ := json.Marshal(map[string]any{
				"id": "chatcmpl-mock", "object": "chat.completion.chunk",
				"created": time.Now().Unix(), "model": "mock-model", "choices": []any{c},
			})
			send(string(b))
		}

		para := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20) + "\n\n"
		stop := "stop"
		chunk("", nil)
		if singleChunk {
			chunk(para, nil) // one shot, no streaming frames
		} else {
			for i := 0; i < 8; i++ { // ~7.4KB reply
				chunk(para, nil)
			}
		}
		chunk("", &stop)
		b, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 500, "total_tokens": 600},
		})
		send(string(b))
		send("[DONE]")
	})
	return mux
}

// TestMemLeakMockServer serves the mock until the test process is stopped
// (pig-only diagnostic, not a pi test port).
func TestMemLeakMockServer(t *testing.T) {
	if testing.Short() {
		t.Skip("probe harness; skip in short mode")
	}
	if os.Getenv("PIG_MEMLEAK_MOCK") == "" {
		t.Skip("probe harness; set PIG_MEMLEAK_MOCK=1 to serve (see file header)")
	}
	listener, err := net.Listen("tcp", memLeakMockAddr)
	if err != nil {
		t.Fatalf("port %s busy — kill the stale mock first (ss -tlnp | grep 18923): %v", memLeakMockAddr, err)
	}
	log.Printf("memleak mock server listening on http://%s", memLeakMockAddr)
	singleChunk := os.Getenv("SINGLE_CHUNK") == "1"
	if singleChunk {
		log.Println("SINGLE_CHUNK=1 — one content chunk per reply")
	}
	server := &http.Server{Handler: memLeakMockHandler(singleChunk)}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		t.Fatalf("serve: %v", err)
	}
}
