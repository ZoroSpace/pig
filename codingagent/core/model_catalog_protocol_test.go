// Ported (slimmed) from pi's test/model-catalog-protocol.test.ts (v0.99.1
// #9763, regression for #9099). pi drives the shared scripts/
// model-catalog-protocol.ts server semantics — index/revision selection and
// the redirect that appends pi-version. pig ports the client half: the
// request must carry types=chat,image,classifier, follow the server's
// pi-version redirect, and apply the returned mixed-type overlay. The
// pi.dev-side protocol script stays unported (server concern, declared).
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent"
)

// TestModelCatalogProtocolClient pins the client half of the pi.dev catalog
// protocol against a minimal stand-in for /api/models/providers/:provider.
func TestModelCatalogProtocolClient(t *testing.T) {
	modelID := "anthropic/claude-sonnet-5"
	// Newer than the bundled catalog, so the client applies the overlay.
	lastModified := "Mon, 02 Jan 2099 00:00:00 GMT"
	// The legacy (chat-only) shard shape: a keyed object without a type.
	legacyModel := map[string]any{
		"id": modelID, "name": "Claude Sonnet 5", "provider": "openrouter",
		"api": "openai-completions", "baseUrl": "https://openrouter.ai/api/v1",
		"reasoning": true, "input": []string{"text"},
		"cost":          map[string]any{"input": 3, "output": 15, "cacheRead": 0.3, "cacheWrite": 3.75},
		"contextWindow": 200000, "maxTokens": 64000,
	}
	body, err := json.Marshal(map[string]any{modelID: legacyModel})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/api/models/providers/openrouter") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Server-side negotiation (pi: parseModelCatalogRequest + redirect):
		// a request without pi-version is redirected onto itself with the
		// client's version appended.
		if r.URL.Query().Get("pi-version") == "" {
			location := *r.URL
			q := location.Query()
			q.Set("pi-version", codingagent.Version)
			location.RawQuery = q.Encode()
			w.Header().Set("location", location.String())
			w.Header().Set("cache-control", "no-store")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("last-modified", lastModified)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	modelsPath := t.TempDir() + "/models.json"
	refreshOnCreate := false
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:     &auth.InMemoryCredentialStore{},
		ModelsPath:      &modelsPath,
		ModelsStore:     &InMemoryCodingAgentModelsStore{},
		CatalogBaseURL:  server.URL,
		RefreshOnCreate: &refreshOnCreate,
	})
	if err := runtime.SetRuntimeApiKey(context.Background(), "openrouter", "test-key"); err != nil {
		t.Fatal(err)
	}
	result := runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{
		AllowNetwork: &allowNetworkTrue, Force: true, Providers: []string{"openrouter"},
	})
	if len(result.Errors) != 0 {
		t.Fatalf("refresh errors: %v", result.Errors)
	}

	mu.Lock()
	seen := append([]string(nil), requests...)
	mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("requests = %v, want two (types request + pi-version redirect follow)", seen)
	}
	if !strings.Contains(seen[0], "types=chat%2Cimage%2Cclassifier") && !strings.Contains(seen[0], "types=chat,image,classifier") {
		t.Fatalf("first request missing types parameter: %s", seen[0])
	}
	if !strings.Contains(seen[1], "pi-version=") {
		t.Fatalf("second request missing pi-version: %s", seen[1])
	}

	model, ok := runtime.GetModel("openrouter", modelID)
	if !ok {
		t.Fatalf("model %s missing after overlay", modelID)
	}
	if model.API != ai.ApiOpenAICompletions || model.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("overlay model = %s/%s", model.API, model.BaseURL)
	}
}

var allowNetworkTrue = true
