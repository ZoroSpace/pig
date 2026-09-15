// pi: test/bedrock-custom-headers.test.ts. pi asserts on the registered
// Smithy middleware (registration shape + handler invocation); pig drives the
// seam's captured header map (end-to-end) and the injection helper directly
// (VC1/VC2), since the middleware object itself is inside the default
// transport.
package api

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestBedrockCustomHeaders(t *testing.T) {
	t.Run("VC1: injects the caller header end-to-end", func(t *testing.T) {
		var capturedHeaders map[string]string
		prev := bedrockNewClient
		bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
			capturedHeaders = params.customHeaders
			return nil, errorsNew("mock send")
		}
		t.Cleanup(func() { bedrockNewClient = prev })

		streamOptions := types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: map[string]*string{"x-custom": strPtr2("v")},
			},
			CacheRetention: types.CacheRetentionNone,
		}
		s := streamBedrockConverse(context.Background(), bedrockTestModel(), bedrockTestContext(),
			&BedrockOptions{StreamOptions: streamOptions})
		s.Result()

		if capturedHeaders == nil || capturedHeaders["x-custom"] != "v" {
			t.Fatalf("custom headers = %v, want {x-custom: v}", capturedHeaders)
		}
	})

	t.Run("VC2: skips reserved headers case-insensitively while applying allowed ones", func(t *testing.T) {
		header := http.Header{
			"Authorization": []string{"real-auth"},
			"X-Amz-Date":    []string{"real-date"},
			"Host":          []string{"real-host"},
		}
		applyBedrockCustomHeaders(header, map[string]string{
			"authorization": "evil",
			"x-amz-date":    "evil",
			"x-allowed":     "ok",
			"Authorization": "evil2",
			"X-Amz-Date":    "evil2",
			"HOST":          "evil3",
		})

		if header.Get("Authorization") != "real-auth" {
			t.Fatalf("authorization = %q", header.Get("Authorization"))
		}
		if header.Get("X-Amz-Date") != "real-date" {
			t.Fatalf("x-amz-date = %q", header.Get("X-Amz-Date"))
		}
		if header.Get("Host") != "real-host" {
			t.Fatalf("host = %q", header.Get("Host"))
		}
		if header.Get("X-Allowed") != "ok" {
			t.Fatalf("x-allowed = %q", header.Get("X-Allowed"))
		}
		// Mixed-case reserved keys must be skipped too: a case-sensitive guard
		// would add them back as distinct capitalised keys.
		if _, ok := header["Authorization-Evil"]; ok {
			t.Fatal("unexpected evil authorization header")
		}
		keys := make([]string, 0, len(header))
		for key := range header {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		want := []string{"Authorization", "Host", "X-Allowed", "X-Amz-Date"}
		if !slices.Equal(keys, want) {
			t.Fatalf("header keys = %v, want %v", keys, want)
		}
	})

	t.Run("VC3: passes no custom headers when headers is undefined or empty", func(t *testing.T) {
		var capturedHeaders map[string]string
		prev := bedrockNewClient
		bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
			capturedHeaders = params.customHeaders
			return nil, errorsNew("mock send")
		}
		t.Cleanup(func() { bedrockNewClient = prev })

		s := streamBedrockConverse(context.Background(), bedrockTestModel(), bedrockTestContext(),
			&BedrockOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}})
		s.Result()
		if len(capturedHeaders) != 0 {
			t.Fatalf("custom headers = %v, want empty for undefined headers", capturedHeaders)
		}

		streamOptions := types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: map[string]*string{},
			},
			CacheRetention: types.CacheRetentionNone,
		}
		s = streamBedrockConverse(context.Background(), bedrockTestModel(), bedrockTestContext(),
			&BedrockOptions{StreamOptions: streamOptions})
		s.Result()
		if len(capturedHeaders) != 0 {
			t.Fatalf("custom headers = %v, want empty for empty headers", capturedHeaders)
		}
	})

	t.Run("VC4: streamSimple forwards headers end-to-end (regression guard)", func(t *testing.T) {
		var capturedHeaders map[string]string
		prev := bedrockNewClient
		bedrockNewClient = func(ctx context.Context, params bedrockClientParams) (bedrockStreamClient, error) {
			capturedHeaders = params.customHeaders
			return nil, errorsNew("mock send")
		}
		t.Cleanup(func() { bedrockNewClient = prev })

		simple := types.SimpleStreamOptions{
			StreamOptions: types.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{
					Headers: map[string]*string{"x-custom": strPtr2("v")},
				},
			},
		}
		s := BedrockConverseStream{}.StreamSimple(context.Background(), bedrockTestModel(), bedrockTestContext(), &simple)
		s.Result()

		if capturedHeaders == nil || capturedHeaders["x-custom"] != "v" {
			t.Fatalf("custom headers = %v, want {x-custom: v}", capturedHeaders)
		}
	})
}
