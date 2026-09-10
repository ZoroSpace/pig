// pi: utils/headers.ts.
package utils

import (
	"net/http"

	"github.com/ZoroSpace/pig/ai/types"
)

// HeadersToRecord converts an http.Header to a string map (pi:
// headersToRecord).
func HeadersToRecord(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for key, values := range headers {
		for _, value := range values {
			result[key] = value
		}
	}
	return result
}

// ProviderHeadersToRecord converts ProviderHeaders to a string map, dropping
// nil entries (pi: providerHeadersToRecord). Returns nil when no non-nil entry
// remains.
func ProviderHeadersToRecord(headers types.ProviderHeaders) map[string]string {
	if headers == nil {
		return nil
	}
	result := map[string]string{}
	for key, value := range headers {
		if value != nil {
			result[key] = *value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
