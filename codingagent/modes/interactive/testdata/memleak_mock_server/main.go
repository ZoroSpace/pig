// Minimal OpenAI-completions SSE mock for pig memory probing
// (docs/fix/2026-09-14-tui-turn-memory-leak.md). Lives under testdata/ so the
// go tool ignores it; build with:
//
//	go build -o /tmp/mockserver ./codingagent/modes/interactive/testdata/memleak_mock_server/
//
// Env: SINGLE_CHUNK=1 emits the whole reply as one content chunk (no
// streaming frames) for the per-chunk control experiment.
//
// Serves POST /v1/chat/completions with a streamed assistant reply (~4KB).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
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
		if os.Getenv("SINGLE_CHUNK") == "1" {
			chunk(para, nil) // one shot, no streaming frames
		} else {
			for i := 0; i < 8; i++ { // ~4KB reply
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
	log.Println("listening on 127.0.0.1:18923")
	log.Fatal(http.ListenAndServe("127.0.0.1:18923", nil))
}
