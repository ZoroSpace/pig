// pi: src/auth/oauth/trusted-url.ts.
package auth

import "net/url"

// trustedHTTPURL returns the normalized http(s) URL value names, or "" when
// it is not a trusted URL. Login flows open server-supplied URLs with the
// platform URL handler, which would also launch a file: URL or a custom
// protocol handler. Normalizing percent-encodes control characters, so the
// URL can also be embedded in terminal hyperlinks (pi: trustedHttpUrl).
func trustedHTTPURL(value any) string {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return ""
	}
	// pi's URL parser percent-encodes control characters and normalizes the
	// URI; Go's url.Parse rejects them outright, so escape them first to keep
	// the normalization semantics (github-copilot-oauth
	// "normalizes verification_uri" case).
	parsed, err := url.Parse(escapeControlBytes(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}
