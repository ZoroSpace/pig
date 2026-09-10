// pi: src/core/model-resolver.ts — model resolution, scoping, and initial
// selection.
//
// Segment deviations (declared):
//   - pi types the runtime parameter as the concrete ModelRuntime class; pig
//     narrows it to the ModelResolverRuntime interface (the members the
//     resolver actually consumes). ModelRuntime satisfies it once ported,
//     and tests stub it directly.
//   - pi's async functions only await ModelRuntime calls; they are
//     synchronous here apart from GetAvailable, which carries the Go
//     context.
//   - localeCompare → byte-order sort (model ids are ASCII; pi's sorts are
//     over these ids only).
//   - chalk coloring (red/yellow/dim console output) is dropped; messages
//     are verbatim. The warn/fatal sinks are package vars for tests.
//   - findInitialModel exits the process on a CLI resolution error (pi:
//     process.exit(1)); pig routes the exit through the modelResolverFatal
//     var so tests can intercept.
//   - defaultModelPerProvider's Object.keys iteration order is insertion
//     order in JS; pig keeps a parallel ordered key slice.
package core

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/constants"
	"github.com/samber/lo"
)

// modelResolverFatal terminates the process for CLI resolution failures (pi:
// console.error + process.exit(1)); var for tests.
var modelResolverFatal = func(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

// modelResolverWarn reports scope warnings; the message arrives pre-rendered
// with the "Warning: " prefix (pi: console.warn(chalk.yellow(...))); var for
// tests.
var modelResolverWarn = func(message string) {
	fmt.Fprintln(os.Stderr, message)
}

// ModelResolverRuntime is the ModelRuntime surface the resolver consumes
// (pi: the concrete ModelRuntime class).
type ModelResolverRuntime interface {
	// GetModels lists all models (pi: getModels() with no provider filter).
	GetModels() []ai.Model
	// HasConfiguredAuth reports whether provider auth is configured (pi:
	// hasConfiguredAuth).
	HasConfiguredAuth(providerID string) bool
	// GetAvailable lists models of providers with configured auth (pi:
	// getAvailable()); cancellation rides the context.
	GetAvailable(ctx context.Context) ([]ai.Model, error)
	// GetAvailableSnapshot returns the last-known available list (pi:
	// getAvailableSnapshot).
	GetAvailableSnapshot() []ai.Model
	// GetModel looks up one model (pi: getModel); ok is false when absent.
	GetModel(providerID, modelID string) (ai.Model, bool)
}

// DefaultModelPerProvider holds the default model id for each known provider
// (pi: defaultModelPerProvider). DefaultModelProviderKeys preserves pi's
// Object.keys insertion order for the first-match loops.
var DefaultModelPerProvider = map[string]string{
	"amazon-bedrock":             "us.anthropic.claude-opus-4-6-v1",
	"ant-ling":                   "Ring-2.6-1T",
	"anthropic":                  "claude-opus-4-8",
	"openai":                     "gpt-5.5",
	"azure-openai-responses":     "gpt-5.4",
	"openai-codex":               "gpt-5.5",
	"radius":                     "auto",
	"nvidia":                     "nvidia/nemotron-3-super-120b-a12b",
	"deepseek":                   "deepseek-v4-pro",
	"google":                     "gemini-3.1-pro-preview",
	"google-vertex":              "gemini-3.1-pro-preview",
	"github-copilot":             "gpt-5.4",
	"openrouter":                 "moonshotai/kimi-k2.6",
	"vercel-ai-gateway":          "zai/glm-5.1",
	"xai":                        "grok-4.6",
	"groq":                       "openai/gpt-oss-120b",
	"cerebras":                   "gpt-oss-120b",
	"zai":                        "glm-5.3",
	"zai-coding-cn":              "glm-5.3",
	"mistral":                    "devstral-medium-latest",
	"minimax":                    "MiniMax-M2.7",
	"minimax-cn":                 "MiniMax-M2.7",
	"moonshotai":                 "kimi-k2.6",
	"moonshotai-cn":              "kimi-k2.6",
	"huggingface":                "moonshotai/Kimi-K2.6",
	"fireworks":                  "accounts/fireworks/models/kimi-k2p6",
	"together":                   "moonshotai/Kimi-K2.6",
	"baseten":                    "zai-org/GLM-5.2",
	"opencode":                   "kimi-k2.6",
	"opencode-go":                "kimi-k2.6",
	"kimi-coding":                "kimi-for-coding",
	"cloudflare-workers-ai":      "@cf/moonshotai/kimi-k2.6",
	"cloudflare-ai-gateway":      "workers-ai/@cf/moonshotai/kimi-k2.6",
	"qwen-token-plan":            "qwen3.7-max",
	"qwen-token-plan-cn":         "qwen3.7-max",
	"qwen-token-plan-individual": "qwen3.8-max",
	"xiaomi":                     "mimo-v2.5-pro",
	"xiaomi-token-plan-cn":       "mimo-v2.5-pro",
	"xiaomi-token-plan-ams":      "mimo-v2.5-pro",
	"xiaomi-token-plan-sgp":      "mimo-v2.5-pro",
}

// DefaultModelProviderKeys preserves pi's Object.keys(defaultModelPerProvider)
// insertion order.
var DefaultModelProviderKeys = []string{
	"amazon-bedrock", "ant-ling", "anthropic", "openai", "azure-openai-responses",
	"openai-codex", "radius", "nvidia", "deepseek", "google", "google-vertex",
	"github-copilot", "openrouter", "vercel-ai-gateway", "xai", "groq",
	"cerebras", "zai", "zai-coding-cn", "mistral", "minimax", "minimax-cn",
	"moonshotai", "moonshotai-cn", "huggingface", "fireworks", "together",
	"baseten", "opencode", "opencode-go", "kimi-coding", "cloudflare-workers-ai",
	"cloudflare-ai-gateway", "qwen-token-plan", "qwen-token-plan-cn",
	"qwen-token-plan-individual", "xiaomi", "xiaomi-token-plan-cn",
	"xiaomi-token-plan-ams", "xiaomi-token-plan-sgp",
}

// ScopedModel is one pattern match with its optional explicit thinking level
// (pi: ScopedModel; empty ThinkingLevel = undefined).
type ScopedModel struct {
	Model ai.Model
	// ThinkingLevel is the level explicitly specified in the pattern (e.g.,
	// "model:high"); empty otherwise.
	ThinkingLevel agent.ThinkingLevel
}

// isAlias checks whether a model id looks like an alias (no date suffix;
// dates are typically -20241022 or -20250929; pi: isAlias).
func isAlias(id string) bool {
	// Check if the id ends with -latest.
	if strings.HasSuffix(id, "-latest") {
		return true
	}
	// Check if the id ends with a date pattern (-YYYYMMDD).
	if len(id) < 9 {
		return true
	}
	digits := id[len(id)-8:]
	if id[len(id)-9] != '-' {
		return true
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return true
		}
	}
	return false
}

// FindExactModelReferenceMatch finds an exact model reference match: a bare
// model id or a canonical provider/modelId reference. Bare-id matches that
// are ambiguous across providers are rejected (pi: findExactModelReferenceMatch).
func FindExactModelReferenceMatch(modelReference string, availableModels []ai.Model) (ai.Model, bool) {
	trimmedReference := strings.TrimSpace(modelReference)
	if trimmedReference == "" {
		return ai.Model{}, false
	}
	normalizedReference := strings.ToLower(trimmedReference)

	var canonicalMatches []ai.Model
	for _, model := range availableModels {
		if strings.ToLower(string(model.Provider)+"/"+model.ID) == normalizedReference {
			canonicalMatches = append(canonicalMatches, model)
		}
	}
	if len(canonicalMatches) == 1 {
		return canonicalMatches[0], true
	}
	if len(canonicalMatches) > 1 {
		return ai.Model{}, false
	}

	if slashIndex := strings.Index(trimmedReference, "/"); slashIndex != -1 {
		provider := strings.TrimSpace(trimmedReference[:slashIndex])
		modelID := strings.TrimSpace(trimmedReference[slashIndex+1:])
		if provider != "" && modelID != "" {
			var providerMatches []ai.Model
			for _, model := range availableModels {
				if strings.ToLower(string(model.Provider)) == strings.ToLower(provider) &&
					strings.ToLower(model.ID) == strings.ToLower(modelID) {
					providerMatches = append(providerMatches, model)
				}
			}
			if len(providerMatches) == 1 {
				return providerMatches[0], true
			}
			if len(providerMatches) > 1 {
				return ai.Model{}, false
			}
		}
	}

	var idMatches []ai.Model
	for _, model := range availableModels {
		if strings.ToLower(model.ID) == normalizedReference {
			idMatches = append(idMatches, model)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0], true
	}
	return ai.Model{}, false
}

// tryMatchModel matches a pattern against the available models, falling back
// to partial matching (pi: tryMatchModel). Alias ids are preferred over
// dated versions; within a group the highest-sorting id wins.
func tryMatchModel(modelPattern string, availableModels []ai.Model) (ai.Model, bool) {
	if exactMatch, ok := FindExactModelReferenceMatch(modelPattern, availableModels); ok {
		return exactMatch, true
	}

	// No exact match — fall back to partial matching.
	var matches []ai.Model
	for _, model := range availableModels {
		if strings.Contains(strings.ToLower(model.ID), strings.ToLower(modelPattern)) ||
			strings.Contains(strings.ToLower(model.Name), strings.ToLower(modelPattern)) {
			matches = append(matches, model)
		}
	}
	if len(matches) == 0 {
		return ai.Model{}, false
	}

	// Separate aliases from dated versions.
	var aliases, datedVersions []ai.Model
	for _, model := range matches {
		if isAlias(model.ID) {
			aliases = append(aliases, model)
		} else {
			datedVersions = append(datedVersions, model)
		}
	}

	if len(aliases) > 0 {
		// Prefer an alias; if multiple, pick the one that sorts highest.
		sort.Slice(aliases, func(i, j int) bool { return aliases[i].ID > aliases[j].ID })
		return aliases[0], true
	}
	// No alias found: pick the latest dated version.
	sort.Slice(datedVersions, func(i, j int) bool { return datedVersions[i].ID > datedVersions[j].ID })
	return datedVersions[0], true
}

// ParsedModelResult is one parseModelPattern outcome (pi: ParsedModelResult).
type ParsedModelResult struct {
	Model *ai.Model
	// ThinkingLevel is the explicit pattern suffix; empty = undefined.
	ThinkingLevel agent.ThinkingLevel
	Warning       string
}

// ParseModelPatternOptions parameterize ParseModelPattern (pi: the options
// argument; nil = default).
type ParseModelPatternOptions struct {
	// AllowInvalidThinkingLevelFallback defaults to true when the options
	// are omitted (pi: options?.allowInvalidThinkingLevelFallback ?? true).
	AllowInvalidThinkingLevelFallback *bool
}

func (o *ParseModelPatternOptions) allowFallback() bool {
	if o == nil || o.AllowInvalidThinkingLevelFallback == nil {
		return true
	}
	return *o.AllowInvalidThinkingLevelFallback
}

// buildFallbackModel builds a custom model id over a provider's default or
// first model (pi: buildFallbackModel).
func buildFallbackModel(provider, modelID string, availableModels []ai.Model) *ai.Model {
	var providerModels []ai.Model
	for _, model := range availableModels {
		if string(model.Provider) == provider {
			providerModels = append(providerModels, model)
		}
	}
	if len(providerModels) == 0 {
		return nil
	}
	defaultID, hasDefault := DefaultModelPerProvider[provider]
	var baseModel ai.Model
	if hasDefault {
		baseModel = providerModels[0]
		for _, model := range providerModels {
			if model.ID == defaultID {
				baseModel = model
				break
			}
		}
	} else {
		baseModel = providerModels[0]
	}

	baseModel.ID = modelID
	baseModel.Name = modelID
	return &baseModel
}

// ParseModelPattern parses a pattern to extract model and thinking level,
// handling models with colons in their ids (e.g. OpenRouter's :exacto
// suffix): try the full pattern first; otherwise split on the last colon and
// recurse when the suffix is a valid thinking level (pi: parseModelPattern).
func ParseModelPattern(pattern string, availableModels []ai.Model, options *ParseModelPatternOptions) ParsedModelResult {
	// Try exact match first.
	if exactMatch, ok := tryMatchModel(pattern, availableModels); ok {
		return ParsedModelResult{Model: &exactMatch, ThinkingLevel: "", Warning: ""}
	}

	// No match — try splitting on the last colon if present.
	lastColonIndex := strings.LastIndex(pattern, ":")
	if lastColonIndex == -1 {
		// No colons: the pattern simply doesn't match any model.
		return ParsedModelResult{}
	}

	prefix := pattern[:lastColonIndex]
	suffix := pattern[lastColonIndex+1:]

	if constants.IsValidThinkingLevel(suffix) {
		// Valid thinking level — recurse on the prefix and use this level.
		result := ParseModelPattern(prefix, availableModels, options)
		if result.Model != nil {
			// Only use this thinking level if the inner recursion warns nothing.
			level := agent.ThinkingLevel(suffix)
			if result.Warning != "" {
				level = ""
			}
			return ParsedModelResult{Model: result.Model, ThinkingLevel: level, Warning: result.Warning}
		}
		return result
	}

	// Invalid suffix.
	if !options.allowFallback() {
		// In strict mode (CLI --model parsing), treat it as part of the model
		// id and fail. This avoids accidentally resolving to a different model.
		return ParsedModelResult{}
	}

	// Scope mode: recurse on the prefix and warn.
	result := ParseModelPattern(prefix, availableModels, options)
	if result.Model != nil {
		return ParsedModelResult{
			Model:         result.Model,
			ThinkingLevel: "",
			Warning:       fmt.Sprintf("Invalid thinking level %q in pattern %q. Using default instead.", suffix, pattern),
		}
	}
	return result
}

// ModelScopeDiagnostic is one scope warning (pi: ModelScopeDiagnostic).
type ModelScopeDiagnostic struct {
	Type    string // "warning"
	Code    string // "no-match" | "invalid-thinking-level"
	Message string
	Pattern string
}

// ResolveModelScopeResult is the structured scope outcome (pi:
// ResolveModelScopeResult).
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel
	Diagnostics  []ModelScopeDiagnostic
}

// ResolveModelScopeFromModels resolves model patterns to actual models with
// optional thinking levels (pi: resolveModelScopeFromModels). Pattern format:
// "pattern:level"; glob patterns (* ? [) match against provider/modelId and
// bare model id.
func ResolveModelScopeFromModels(patterns []string, models []ai.Model) ResolveModelScopeResult {
	availableModels := append([]ai.Model(nil), models...)
	scopedModels := []ScopedModel{}
	diagnostics := []ModelScopeDiagnostic{}

	for _, pattern := range patterns {
		// Check if the pattern contains glob characters.
		if strings.ContainsAny(pattern, "*?[") {
			// Extract the optional thinking level suffix (e.g. "provider/*:high").
			globPattern := pattern
			var thinkingLevel agent.ThinkingLevel
			if colonIdx := strings.LastIndex(pattern, ":"); colonIdx != -1 {
				suffix := pattern[colonIdx+1:]
				if constants.IsValidThinkingLevel(suffix) {
					thinkingLevel = agent.ThinkingLevel(suffix)
					globPattern = pattern[:colonIdx]
				}
			}

			if exactMatch, ok := FindExactModelReferenceMatch(globPattern, availableModels); ok {
				if !scopedModelsContains(scopedModels, exactMatch) {
					scopedModels = append(scopedModels, ScopedModel{Model: exactMatch, ThinkingLevel: thinkingLevel})
				}
				continue
			}

			// Match against "provider/modelId" or just the model id, so
			// "*sonnet*" matches without requiring "anthropic/*sonnet*".
			var matchingModels []ai.Model
			for _, model := range availableModels {
				fullID := string(model.Provider) + "/" + model.ID
				if utils.Minimatch(fullID, globPattern) || utils.Minimatch(model.ID, globPattern) {
					matchingModels = append(matchingModels, model)
				}
			}
			if len(matchingModels) == 0 {
				diagnostics = append(diagnostics, ModelScopeDiagnostic{
					Type:    "warning",
					Code:    "no-match",
					Message: fmt.Sprintf("No models match pattern %q", pattern),
					Pattern: pattern,
				})
				continue
			}
			for _, model := range matchingModels {
				if !scopedModelsContains(scopedModels, model) {
					scopedModels = append(scopedModels, ScopedModel{Model: model, ThinkingLevel: thinkingLevel})
				}
			}
			continue
		}

		parsed := ParseModelPattern(pattern, availableModels, nil)
		if parsed.Warning != "" {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type:    "warning",
				Code:    "invalid-thinking-level",
				Message: parsed.Warning,
				Pattern: pattern,
			})
		}
		if parsed.Model == nil {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type:    "warning",
				Code:    "no-match",
				Message: fmt.Sprintf("No models match pattern %q", pattern),
				Pattern: pattern,
			})
			continue
		}
		// Avoid duplicates.
		if !scopedModelsContains(scopedModels, *parsed.Model) {
			scopedModels = append(scopedModels, ScopedModel{Model: *parsed.Model, ThinkingLevel: parsed.ThinkingLevel})
		}
	}

	return ResolveModelScopeResult{ScopedModels: scopedModels, Diagnostics: diagnostics}
}

func scopedModelsContains(scopedModels []ScopedModel, model ai.Model) bool {
	for _, scoped := range scopedModels {
		if ai.ModelsAreEqual(&scoped.Model, &model) {
			return true
		}
	}
	return false
}

// ResolveModelScopeWithDiagnostics resolves scope against the runtime's
// available models without writing warnings (pi:
// resolveModelScopeWithDiagnostics).
func ResolveModelScopeWithDiagnostics(ctx context.Context, patterns []string, modelRuntime ModelResolverRuntime) (ResolveModelScopeResult, error) {
	available, err := modelRuntime.GetAvailable(ctx)
	if err != nil {
		return ResolveModelScopeResult{}, err
	}
	return ResolveModelScopeFromModels(patterns, available), nil
}

// ResolveModelScope resolves model patterns and preserves CLI warning output
// (pi: resolveModelScope).
func ResolveModelScope(ctx context.Context, patterns []string, modelRuntime ModelResolverRuntime) ([]ScopedModel, error) {
	result, err := ResolveModelScopeWithDiagnostics(ctx, patterns, modelRuntime)
	if err != nil {
		return nil, err
	}
	for _, diagnostic := range result.Diagnostics {
		modelResolverWarn("Warning: " + diagnostic.Message)
	}
	return result.ScopedModels, nil
}

// ResolveCliModelResult is one CLI model resolution (pi:
// ResolveCliModelResult).
type ResolveCliModelResult struct {
	Model *ai.Model
	// ThinkingLevel is the parsed "<pattern>:<thinking>" suffix; empty =
	// undefined. Callers apply it.
	ThinkingLevel agent.ThinkingLevel
	Warning       string
	// Error is a message suitable for CLI display; when set, Model is nil.
	Error string
}

// ResolveCliModel resolves a single model from CLI flags: --provider
// <provider> --model <pattern>, --model <provider>/<pattern>, or fuzzy
// matching (pi: resolveCliModel).
func ResolveCliModel(options ResolveCliModelOptions) ResolveCliModelResult {
	cliProvider, cliModel, cliThinking := options.CliProvider, options.CliModel, options.CliThinking
	modelRuntime := options.ModelRuntime

	if cliModel == "" {
		return ResolveCliModelResult{}
	}

	// Important: use *all* models here, not just models with pre-configured
	// auth. This allows "--api-key" to be used for first-time setup.
	availableModels := append([]ai.Model(nil), modelRuntime.GetModels()...)
	if len(availableModels) == 0 {
		return ResolveCliModelResult{
			Error: "No models available. Check your installation or add models to models.json.",
		}
	}

	// Build the canonical provider lookup (case-insensitive).
	providerMap := map[string]string{}
	for _, model := range availableModels {
		providerMap[strings.ToLower(string(model.Provider))] = string(model.Provider)
	}

	var provider string
	if cliProvider != "" {
		provider = providerMap[strings.ToLower(cliProvider)]
		if provider == "" {
			return ResolveCliModelResult{
				Error: fmt.Sprintf("Unknown provider %q. Use --list-models to see available providers/models.", cliProvider),
			}
		}
	}

	// With no explicit --provider, try to interpret "provider/model" first.
	// When the prefix before the first slash matches a known provider, prefer
	// that interpretation over matching models whose ids literally contain
	// slashes (e.g. "zai/glm-5" should resolve to provider=zai, model=glm-5,
	// not to a vercel-ai-gateway model with id "zai/glm-5").
	pattern := cliModel
	inferredProvider := false

	if provider == "" {
		if slashIndex := strings.Index(cliModel, "/"); slashIndex != -1 {
			maybeProvider := cliModel[:slashIndex]
			canonical := providerMap[strings.ToLower(maybeProvider)]
			if canonical != "" {
				provider = canonical
				pattern = cliModel[slashIndex+1:]
				inferredProvider = true
			}
		}
	}

	// If no provider was inferred from the slash, try exact matches without
	// provider inference. This handles models whose ids naturally contain
	// slashes (e.g. OpenRouter-style ids). Bare exact ids can exist in
	// multiple providers, so do not choose by catalog order: prefer the sole
	// authenticated provider when there is one; otherwise require an explicit
	// provider to avoid silently selecting an unusable provider.
	if provider == "" {
		lower := strings.ToLower(cliModel)
		var exactMatches []ai.Model
		for _, model := range availableModels {
			if strings.ToLower(model.ID) == lower || strings.ToLower(string(model.Provider)+"/"+model.ID) == lower {
				exactMatches = append(exactMatches, model)
			}
		}
		if len(exactMatches) == 1 {
			return ResolveCliModelResult{Model: &exactMatches[0]}
		}
		if len(exactMatches) > 1 {
			var authenticatedExactMatches []ai.Model
			for _, model := range exactMatches {
				if modelRuntime.HasConfiguredAuth(string(model.Provider)) {
					authenticatedExactMatches = append(authenticatedExactMatches, model)
				}
			}
			if len(authenticatedExactMatches) == 1 {
				return ResolveCliModelResult{Model: &authenticatedExactMatches[0]}
			}

			canonical := make([]string, 0, len(exactMatches))
			for _, model := range exactMatches {
				canonical = append(canonical, string(model.Provider)+"/"+model.ID)
			}
			sort.Strings(canonical)
			var authHint string
			if len(authenticatedExactMatches) == 0 {
				authHint = "No matching provider is authenticated."
			} else {
				authHint = "More than one matching provider is authenticated."
			}
			return ResolveCliModelResult{
				Error: fmt.Sprintf("Model %q is ambiguous across providers: %s. %s Use --provider or provider/model.",
					cliModel, strings.Join(canonical, ", "), authHint),
			}
		}
	}

	if cliProvider != "" && provider != "" {
		// If both were provided, tolerate --model <provider>/<pattern> by
		// stripping the provider prefix.
		prefix := provider + "/"
		if strings.HasPrefix(strings.ToLower(cliModel), strings.ToLower(prefix)) {
			pattern = cliModel[len(prefix):]
		}
	}

	candidates := availableModels
	if provider != "" {
		candidates = nil
		for _, model := range availableModels {
			if string(model.Provider) == provider {
				candidates = append(candidates, model)
			}
		}
	}
	parsed := ParseModelPattern(pattern, candidates, &ParseModelPatternOptions{
		AllowInvalidThinkingLevelFallback: lo.ToPtr(false),
	})

	if parsed.Model != nil {
		// If provider inference matched an unauthenticated provider/model
		// pair, prefer one exact raw model-id match that is authenticated.
		// This keeps "provider/model" syntax preferred when usable, but
		// handles models whose literal id starts with a known provider name
		// (for example commandcode model id "xiaomi/mimo-v2.5-pro").
		if inferredProvider {
			var rawExactMatches []ai.Model
			for _, model := range availableModels {
				if strings.ToLower(model.ID) == strings.ToLower(cliModel) && !ai.ModelsAreEqual(&model, parsed.Model) {
					rawExactMatches = append(rawExactMatches, model)
				}
			}
			if len(rawExactMatches) > 0 && !modelRuntime.HasConfiguredAuth(string(parsed.Model.Provider)) {
				var authenticatedRawMatches []ai.Model
				for _, model := range rawExactMatches {
					if modelRuntime.HasConfiguredAuth(string(model.Provider)) {
						authenticatedRawMatches = append(authenticatedRawMatches, model)
					}
				}
				if len(authenticatedRawMatches) == 1 {
					return ResolveCliModelResult{Model: &authenticatedRawMatches[0]}
				}
			}
		}
		return ResolveCliModelResult{Model: parsed.Model, ThinkingLevel: parsed.ThinkingLevel, Warning: parsed.Warning}
	}

	// If we inferred a provider from the slash but found no match within that
	// provider, fall back to matching the full input as a raw model id across
	// all models. This handles OpenRouter-style ids like
	// "openai/gpt-4o:extended" where "openai" looks like a provider but the
	// full string is actually a model id on openrouter.
	if inferredProvider {
		lower := strings.ToLower(cliModel)
		for _, model := range availableModels {
			if strings.ToLower(model.ID) == lower || strings.ToLower(string(model.Provider)+"/"+model.ID) == lower {
				return ResolveCliModelResult{Model: &model}
			}
		}
		// Also try ParseModelPattern on the full input against all models.
		fallback := ParseModelPattern(cliModel, availableModels, &ParseModelPatternOptions{
			AllowInvalidThinkingLevelFallback: lo.ToPtr(false),
		})
		if fallback.Model != nil {
			return ResolveCliModelResult{Model: fallback.Model, ThinkingLevel: fallback.ThinkingLevel, Warning: fallback.Warning}
		}
	}

	if provider != "" {
		// Parse the thinking level suffix from the pattern before building
		// the fallback model, but only when --thinking is not explicitly
		// provided (e.g. "zai-org/GLM-5.1-FP8:high").
		fallbackPattern := pattern
		var fallbackThinking agent.ThinkingLevel
		if cliThinking == "" {
			if lastColon := strings.LastIndex(pattern, ":"); lastColon != -1 {
				suffix := pattern[lastColon+1:]
				if constants.IsValidThinkingLevel(suffix) {
					fallbackPattern = pattern[:lastColon]
					fallbackThinking = agent.ThinkingLevel(suffix)
				}
			}
		}

		fallbackModel := buildFallbackModel(provider, fallbackPattern, availableModels)
		if fallbackModel != nil {
			requestedThinking := fallbackThinking
			if cliThinking != "" {
				requestedThinking = cliThinking
			}
			if requestedThinking != "" && requestedThinking != "off" {
				fallbackModel.Reasoning = true
			}
			var fallbackWarning string
			if parsed.Warning != "" {
				fallbackWarning = fmt.Sprintf("%s Model %q not found for provider %q. Using custom model id.",
					parsed.Warning, fallbackPattern, provider)
			} else {
				fallbackWarning = fmt.Sprintf("Model %q not found for provider %q. Using custom model id.",
					fallbackPattern, provider)
			}
			return ResolveCliModelResult{Model: fallbackModel, ThinkingLevel: fallbackThinking, Warning: fallbackWarning}
		}
	}

	display := cliModel
	if provider != "" {
		display = provider + "/" + pattern
	}
	return ResolveCliModelResult{
		Warning: parsed.Warning,
		Error:   fmt.Sprintf("Model %q not found. Use --list-models to see available models.", display),
	}
}

// ResolveCliModelOptions are ResolveCliModel's inputs (pi: the options
// object).
type ResolveCliModelOptions struct {
	CliProvider  string
	CliModel     string
	CliThinking  agent.ThinkingLevel
	ModelRuntime ModelResolverRuntime
}

// InitialModelResult is findInitialModel's outcome (pi: InitialModelResult).
type InitialModelResult struct {
	Model           *ai.Model
	ThinkingLevel   agent.ThinkingLevel
	FallbackMessage string
}

// FindInitialModel finds the initial model by priority: CLI args, first
// scoped model, saved default, then first available (pi: findInitialModel).
func FindInitialModel(options FindInitialModelOptions) InitialModelResult {
	// 1. CLI args take priority.
	if options.CliProvider != "" && options.CliModel != "" {
		resolved := ResolveCliModel(ResolveCliModelOptions{
			CliProvider:  options.CliProvider,
			CliModel:     options.CliModel,
			ModelRuntime: options.ModelRuntime,
		})
		if resolved.Error != "" {
			modelResolverFatal(resolved.Error)
		}
		if resolved.Model != nil {
			return InitialModelResult{Model: resolved.Model, ThinkingLevel: DefaultThinkingLevel}
		}
	}

	// 2. Use the first model from scoped models (skip if continuing/resuming).
	if len(options.ScopedModels) > 0 && !options.IsContinuing {
		scopedModel := options.ScopedModels[0]
		perModel, hasPerModel := options.ModelThinkingLevels[string(scopedModel.Model.Provider)+"/"+scopedModel.Model.ID]
		if scopedModel.ThinkingLevel != "" {
			return InitialModelResult{Model: &scopedModel.Model, ThinkingLevel: scopedModel.ThinkingLevel}
		}
		if hasPerModel {
			return InitialModelResult{Model: &scopedModel.Model, ThinkingLevel: perModel}
		}
		if options.DefaultThinkingLevel != "" {
			return InitialModelResult{Model: &scopedModel.Model, ThinkingLevel: options.DefaultThinkingLevel}
		}
		return InitialModelResult{Model: &scopedModel.Model, ThinkingLevel: DefaultThinkingLevel}
	}

	// 3. Try the saved default from settings if auth is configured.
	if options.DefaultProvider != "" && options.DefaultModelID != "" {
		if found, ok := options.ModelRuntime.GetModel(options.DefaultProvider, options.DefaultModelID); ok {
			if options.ModelRuntime.HasConfiguredAuth(string(found.Provider)) {
				thinkingLevel := DefaultThinkingLevel
				perModel, hasPerModel := options.ModelThinkingLevels[options.DefaultProvider+"/"+options.DefaultModelID]
				if hasPerModel {
					thinkingLevel = perModel
				} else if options.DefaultThinkingLevel != "" {
					thinkingLevel = options.DefaultThinkingLevel
				}
				return InitialModelResult{Model: &found, ThinkingLevel: thinkingLevel}
			}
		}
	}

	// 4. Try the first available model with a valid API key.
	availableModels := append([]ai.Model(nil), options.ModelRuntime.GetAvailableSnapshot()...)

	if len(availableModels) > 0 {
		// Try to find a default model from known providers.
		for _, provider := range DefaultModelProviderKeys {
			defaultID := DefaultModelPerProvider[provider]
			for _, model := range availableModels {
				if string(model.Provider) == provider && model.ID == defaultID {
					return InitialModelResult{Model: &model, ThinkingLevel: DefaultThinkingLevel}
				}
			}
		}
		// If no default found, use the first available.
		return InitialModelResult{Model: &availableModels[0], ThinkingLevel: DefaultThinkingLevel}
	}

	// 5. No model found.
	return InitialModelResult{ThinkingLevel: DefaultThinkingLevel}
}

// FindInitialModelOptions are FindInitialModel's inputs (pi: the options
// object; empty strings = undefined).
type FindInitialModelOptions struct {
	CliProvider          string
	CliModel             string
	ScopedModels         []ScopedModel
	IsContinuing         bool
	DefaultProvider      string
	DefaultModelID       string
	DefaultThinkingLevel agent.ThinkingLevel
	// ModelThinkingLevels maps "provider/modelId" to a per-model thinking
	// level (pi: modelThinkingLevels).
	ModelThinkingLevels map[string]agent.ThinkingLevel
	ModelRuntime        ModelResolverRuntime
}

// RestoreModelFromSession restores the model from a session, with fallback
// to available models (pi: restoreModelFromSession). Empty strings in place
// of pi's undefined booleans: shouldPrintMessages is passed explicitly.
func RestoreModelFromSession(savedProvider, savedModelID string, currentModel *ai.Model, shouldPrintMessages bool, modelRuntime ModelResolverRuntime) (*ai.Model, string) {
	restoredModel, found := modelRuntime.GetModel(savedProvider, savedModelID)

	// Check if the restored model exists and still has auth configured.
	hasConfiguredAuth := found && modelRuntime.HasConfiguredAuth(string(restoredModel.Provider))

	if found && hasConfiguredAuth {
		if shouldPrintMessages {
			fmt.Printf("Restored model: %s/%s\n", savedProvider, savedModelID)
		}
		return &restoredModel, ""
	}

	// Model not found or no API key — fall back.
	reason := "no auth configured"
	if !found {
		reason = "model no longer exists"
	}

	if shouldPrintMessages {
		fmt.Fprintf(os.Stderr, "Warning: Could not restore model %s/%s (%s).\n", savedProvider, savedModelID, reason)
	}

	// If we already have a model, use it as fallback.
	if currentModel != nil {
		if shouldPrintMessages {
			fmt.Printf("Falling back to: %s/%s\n", currentModel.Provider, currentModel.ID)
		}
		message := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.",
			savedProvider, savedModelID, reason, currentModel.Provider, currentModel.ID)
		return currentModel, message
	}

	// Try to find any available model.
	availableModels := append([]ai.Model(nil), modelRuntime.GetAvailableSnapshot()...)

	if len(availableModels) > 0 {
		// Try to find a default model from known providers.
		var fallbackModel *ai.Model
		for _, provider := range DefaultModelProviderKeys {
			defaultID := DefaultModelPerProvider[provider]
			for _, model := range availableModels {
				if string(model.Provider) == provider && model.ID == defaultID {
					fallbackModel = &model
					break
				}
			}
			if fallbackModel != nil {
				break
			}
		}
		// If no default found, use the first available.
		if fallbackModel == nil {
			fallbackModel = &availableModels[0]
		}

		if shouldPrintMessages {
			fmt.Printf("Falling back to: %s/%s\n", fallbackModel.Provider, fallbackModel.ID)
		}
		message := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.",
			savedProvider, savedModelID, reason, fallbackModel.Provider, fallbackModel.ID)
		return fallbackModel, message
	}

	// No models available.
	return nil, ""
}
