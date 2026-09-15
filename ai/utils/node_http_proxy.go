// pi: utils/node-http-proxy.ts.
//
// Resolves the HTTP(S) proxy URL for an outbound provider request from proxy
// environment variables, honoring NO_PROXY exclusions. First consumers:
// bedrock-converse-stream (NodeHttpHandler proxy agents) and
// openai-codex-responses; pig re-uses it wherever a proxy-aware transport is
// assembled.
package utils

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

var defaultProxyPorts = map[string]int{
	"ftp":    21,
	"gopher": 70,
	"http":   80,
	"https":  443,
	"ws":     80,
	"wss":    443,
}

func getProxyEnv(key string, env types.ProviderEnv) string {
	lowercaseKey := strings.ToLower(key)
	uppercaseKey := strings.ToUpper(key)
	return firstNonEmptyEnv(env[lowercaseKey], env[uppercaseKey], GetProviderEnvValue(lowercaseKey, env), GetProviderEnvValue(uppercaseKey, env))
}

// firstNonEmptyEnv returns the first non-empty value (pi: env?.[k] || …
// short-circuiting over empty strings).
func firstNonEmptyEnv(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseProxyTargetURL(targetURL string) *url.URL {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return nil
	}
	return parsed
}

func stripBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

func parseNoProxyEntry(entry string) (host string, port int, ok bool) {
	trimmed := strings.TrimSpace(strings.ToLower(entry))
	if trimmed == "" {
		return "", 0, false
	}
	if strings.HasPrefix(trimmed, "[") {
		closingBracket := strings.Index(trimmed, "]")
		if closingBracket == -1 {
			return "", 0, false
		}
		host = trimmed[1:closingBracket]
		rest := trimmed[closingBracket+1:]
		if strings.HasPrefix(rest, ":") {
			port, err := strconv.Atoi(rest[1:])
			if err != nil {
				return host, 0, true
			}
			return host, port, true
		}
		return host, 0, true
	}
	if strings.Contains(trimmed, ":") && strings.Count(trimmed, ":") > 1 {
		// pi: bare (bracket-less) IPv6 entries parse as { host, port: 0 }.
		return trimmed, 0, true
	}
	colonIndex := strings.LastIndex(trimmed, ":")
	if colonIndex != -1 && colonIndex == strings.Index(trimmed, ":") {
		host = trimmed[:colonIndex]
		port, err := strconv.Atoi(trimmed[colonIndex+1:])
		if err == nil {
			return host, port, true
		}
		// pi: a non-numeric port falls through to the whole-entry host below.
		host = ""
	}
	return trimmed, 0, true
}

func shouldProxyHostname(hostname string, port int, env types.ProviderEnv) bool {
	noProxy := strings.ToLower(getProxyEnv("no_proxy", env))
	if noProxy == "" {
		return true
	}
	if noProxy == "*" {
		return false
	}

	normalizedTargetHost := stripBrackets(strings.ToLower(hostname))

	for _, entry := range splitNoProxy(noProxy) {
		host, entryPort, ok := parseNoProxyEntry(entry)
		if !ok {
			continue
		}
		if entryPort != 0 && entryPort != port {
			continue
		}
		domain := stripBrackets(host)
		if strings.HasPrefix(domain, "*.") {
			domain = domain[2:]
		} else if strings.HasPrefix(domain, ".") || strings.HasPrefix(domain, "*") {
			domain = domain[1:]
		}
		if domain == "" {
			continue
		}
		if normalizedTargetHost == domain {
			return false
		}
		if strings.HasSuffix(normalizedTargetHost, "."+domain) {
			return false
		}
	}
	return true
}

// splitNoProxy splits on commas and whitespace (pi: /[,\s]/).
func splitNoProxy(noProxy string) []string {
	return strings.FieldsFunc(noProxy, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func getProxyForURL(targetURL string, env types.ProviderEnv) string {
	parsedURL := parseProxyTargetURL(targetURL)
	if parsedURL == nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return ""
	}

	protocol := strings.SplitN(parsedURL.Scheme, ":", 2)[0]
	hostname := stripBrackets(parsedURL.Hostname())
	if hostname == "" {
		// pi: parsedURL.hostname || parsedURL.host.replace(/:\d*$/, "").
		host := parsedURL.Host
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}
		hostname = stripBrackets(host)
	}
	port, _ := strconv.Atoi(parsedURL.Port())
	if port == 0 {
		port = defaultProxyPorts[protocol]
	}
	if !shouldProxyHostname(hostname, port, env) {
		return ""
	}

	proxy := firstNonEmptyEnv(getProxyEnv(protocol+"_proxy", env), getProxyEnv("all_proxy", env))
	if proxy != "" && !strings.Contains(proxy, "://") {
		proxy = protocol + "://" + proxy
	}
	return proxy
}

// UnsupportedProxyProtocolMessage is pi's
// UNSUPPORTED_PROXY_PROTOCOL_MESSAGE.
const UnsupportedProxyProtocolMessage = "Unsupported proxy protocol. SOCKS and PAC proxy URLs are not supported; use an HTTP or HTTPS proxy URL."

// ResolveHTTPProxyURLForTarget resolves the proxy URL for a target request
// URL from proxy env vars, or nil when the target should go direct (pi:
// resolveHttpProxyUrlForTarget). SOCKS/PAC proxies are rejected explicitly.
func ResolveHTTPProxyURLForTarget(targetURL string, env types.ProviderEnv) (*url.URL, error) {
	proxy := getProxyForURL(targetURL, env)
	if proxy == "" {
		return nil, nil
	}

	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, fmt.Errorf("Invalid proxy URL %q: %v", proxy, err)
	}

	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		return nil, fmt.Errorf("%s Got %s:", UnsupportedProxyProtocolMessage, proxyURL.Scheme)
	}

	return proxyURL, nil
}
