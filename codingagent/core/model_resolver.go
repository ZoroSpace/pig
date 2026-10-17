// pi: src/core/model-resolver.ts — model resolution, scoping, and initial
// selection.
//
// Segment deviations (declared):
//   - pi types the runtime parameter as the concrete ModelRuntime class; pig
//     narrows it to the ModelResolverRuntime interface (the members the
//     resolver actually consumes). ModelRuntime satisfies it once ported,
//     and tests stub it directly.
//   - pi's `ModelReferenceResult` is a discriminated union; pig models it as
//     one struct with a Status discriminator (resolved | ambiguous |
//     not-found), matching the event-union convention used elsewhere.
//   - pi's async functions only await ModelRuntime calls; they are
//     synchronous here apart from GetAvailable, which carries the Go
//     context.
//   - localeCompare → byte-order sort (model ids are ASCII; pi's sorts are
//     over these ids only).
//   - chalk coloring (red/yellow/dim console output) is dropped; messages
//     are verbatim. The warn/fatal sinks are package vars for tests.
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
	"azure":                      "gpt-5.4",
	"openai-codex":               "gpt-6.1-sol",
	"radius":                     "balanced",
	"nvidia":                     "nvidia/nemotron-3-ultra-550b-a55b",
	"deepseek":                   "deepseek-v4-pro",
	"google":                     "gemini-3.1-pro-preview",
	"google-vertex":              "gemini-3.1-pro-preview",
	"github-copilot":             "gpt-5.4",
	"openrouter":                 "moonshotai/kimi-k2.6",
	"vercel-ai-gateway":          "zai/glm-5.1",
	"xai":                        "grok-4.7",
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
	"fireworks":                  "accounts/fireworks/models/kimi-k3",
	"together":                   "moonshotai/Kimi-K3",
	"baseten":                    "zai-org/GLM-5.2",
	"opencode":                   "kimi-k2.6",
	"opencode-go":                "kimi-k3",
	"kimi-coding":                "kimi-for-coding",
	"meta":                       "muse-spark-1.3",
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
	"amazon-bedrock", "ant-ling", "anthropic", "openai", "azure",
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

// ModelReferenceRequest parameterizes ResolveModelReference (pi:
// ModelReferenceRequest).
type ModelReferenceRequest struct {
	// Text is a bare model id or `provider/id`, optionally followed by
	// `:<thinking>`.
	Text string
	// Provider restricts the candidate pool to this provider; empty =
	// undefined.
	Provider string
	// ThinkingSuffix is "none" | "strict" | "lenient". A trailing
	// `:<suffix>` is only considered when the full text matches no model,
	// since ids such as OpenRouter's `qwen/qwen3-coder:exacto` contain
	// colons. "none" keeps the suffix part of the id; "strict" parses a
	// valid thinking level and keeps any other suffix in the id; "lenient"
	// is like strict but drops an invalid suffix with a warning.
	ThinkingSuffix string
	// Fuzzy matches id and name substrings when no exact match exists.
	Fuzzy bool
}

// ModelReferenceTarget is the not-found target used to build a custom model
// id (pi: ModelReferenceTarget).
type ModelReferenceTarget struct {
	Provider string
	// ProviderModels is non-empty (pi: [Model, ...Model[]]).
	ProviderModels []ai.Model
	// ModelText is the text after the provider prefix as typed, including
	// any `:<suffix>`.
	ModelText string
	// ModelID is ModelText without a valid thinking suffix (unless
	// ThinkingSuffix was "none").
	ModelID       string
	ThinkingLevel agent.ThinkingLevel
}

// ModelReferenceResult is one resolution outcome (pi:
// ModelReferenceResult). Status discriminates: "resolved", "ambiguous", or
// "not-found".
type ModelReferenceResult struct {
	Status string
	// Model is set when Status == "resolved".
	Model         *ai.Model
	ThinkingLevel agent.ThinkingLevel
	Warning       string
	// Candidates and UsableCount are set when Status == "ambiguous".
	Candidates  []ai.Model
	UsableCount int
	// Target is set when Status == "not-found".
	Target *ModelReferenceTarget
}

// alwaysUsable is the usable predicate that marks every provider usable.
func alwaysUsable(string) bool { return true }

// splitThinkingSuffix splits a trailing valid thinking level off text (pi:
// splitThinkingSuffix).
func splitThinkingSuffix(text string) (base string, level agent.ThinkingLevel, ok bool) {
	colonIndex := strings.LastIndex(text, ":")
	if colonIndex == -1 {
		return "", "", false
	}
	suffix := text[colonIndex+1:]
	if !constants.IsValidThinkingLevel(suffix) {
		return "", "", false
	}
	return text[:colonIndex], agent.ThinkingLevel(suffix), true
}

// providerPrefix is a `provider/rest` split (pi: the splitProviderPrefix
// return object).
type providerPrefix struct {
	provider  string
	modelText string
}

// splitProviderPrefix splits `provider/rest` when `provider` names a known
// provider in models, returning the canonical provider (pi:
// splitProviderPrefix).
func splitProviderPrefix(text string, models []ai.Model) (providerPrefix, bool) {
	slashIndex := strings.Index(text, "/")
	if slashIndex == -1 {
		return providerPrefix{}, false
	}
	prefix := strings.ToLower(strings.TrimSpace(text[:slashIndex]))
	modelText := strings.TrimSpace(text[slashIndex+1:])
	if prefix == "" || modelText == "" {
		return providerPrefix{}, false
	}
	for _, model := range models {
		if strings.ToLower(string(model.Provider)) == prefix {
			return providerPrefix{provider: string(model.Provider), modelText: modelText}, true
		}
	}
	return providerPrefix{}, false
}

// ResolveModelReference resolves a model reference against models, using
// usable only to break ties between providers (pi: resolveModelReference).
// Candidates are collected before a provider is chosen, so catalog order
// never decides the provider and an ambiguous match is reported instead of
// falling through to a weaker one.
func ResolveModelReference(request ModelReferenceRequest, models []ai.Model, usable func(provider string) bool) ModelReferenceResult {
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return ModelReferenceResult{Status: "not-found"}
	}
	constraint := strings.ToLower(request.Provider)
	pool := models
	if constraint != "" {
		pool = nil
		for _, model := range models {
			if strings.ToLower(string(model.Provider)) == constraint {
				pool = append(pool, model)
			}
		}
	}
	result := resolveText(text, request, pool, usable)
	if result.Status != "not-found" {
		return result
	}

	split, hasSplit := splitProviderPrefix(text, pool)
	anchorIdx := -1
	if constraint != "" {
		if len(pool) > 0 {
			anchorIdx = 0
		}
	} else if hasSplit {
		for i := range pool {
			if string(pool[i].Provider) == split.provider {
				anchorIdx = i
				break
			}
		}
	}
	if anchorIdx == -1 {
		return result
	}
	modelText := text
	if hasSplit {
		modelText = split.modelText
	}
	target := &ModelReferenceTarget{
		Provider:  string(pool[anchorIdx].Provider),
		ModelText: modelText,
		ModelID:   modelText,
	}
	target.ProviderModels = append(target.ProviderModels, pool[anchorIdx])
	for i := range pool {
		if i != anchorIdx && string(pool[i].Provider) == target.Provider {
			target.ProviderModels = append(target.ProviderModels, pool[i])
		}
	}
	if request.ThinkingSuffix != "none" {
		if base, level, ok := splitThinkingSuffix(modelText); ok {
			target.ModelID = base
			target.ThinkingLevel = level
		}
	}
	return ModelReferenceResult{Status: "not-found", Target: target}
}

// FindExactModelReferenceMatch finds an exact `provider/id` or bare id lookup
// without thinking suffixes or fuzzy matching (pi:
// findExactModelReferenceMatch).
func FindExactModelReferenceMatch(modelReference string, models []ai.Model) (ai.Model, bool) {
	result := ResolveModelReference(ModelReferenceRequest{
		Text:           modelReference,
		ThinkingSuffix: "none",
		Fuzzy:          false,
	}, models, alwaysUsable)
	if result.Status == "resolved" && result.Model != nil {
		return *result.Model, true
	}
	return ai.Model{}, false
}

// resolveText is the recursive core of ResolveModelReference (pi:
// resolveText).
func resolveText(text string, request ModelReferenceRequest, pool []ai.Model, usable func(provider string) bool) ModelReferenceResult {
	if reference, ok := selectReference(text, pool, request.Fuzzy, usable); ok {
		return reference
	}

	if request.Fuzzy {
		if fuzzy, ok := selectFuzzy(pool, fuzzyMatchIndices(pool, identityIndices(pool), text), usable); ok {
			return fuzzy
		}
	}

	if request.ThinkingSuffix == "none" {
		return ModelReferenceResult{Status: "not-found"}
	}
	if base, level, ok := splitThinkingSuffix(text); ok {
		inner := resolveText(base, request, pool, usable)
		if inner.Status == "not-found" {
			return inner
		}
		// Only use this thinking level if the inner recursion warned nothing.
		if inner.Warning != "" {
			inner.ThinkingLevel = ""
		} else {
			inner.ThinkingLevel = level
		}
		return inner
	}
	// Keep an invalid suffix as part of the id and fail, rather than resolve a
	// different model.
	if request.ThinkingSuffix == "strict" {
		return ModelReferenceResult{Status: "not-found"}
	}
	colonIndex := strings.LastIndex(text, ":")
	if colonIndex == -1 {
		return ModelReferenceResult{Status: "not-found"}
	}

	suffix := text[colonIndex+1:]
	inner := resolveText(text[:colonIndex], request, pool, usable)
	if inner.Status == "not-found" {
		return inner
	}
	inner.ThinkingLevel = ""
	inner.Warning = fmt.Sprintf("Invalid thinking level %q in pattern %q. Using default instead.", suffix, text)
	return inner
}

// selectReference resolves `provider/rest` as a hint: the whole text may also
// be a model id on another provider (pi: selectReference).
func selectReference(text string, pool []ai.Model, fuzzy bool, usable func(provider string) bool) (ModelReferenceResult, bool) {
	lowerText := strings.ToLower(text)
	var raw []int
	for i := range pool {
		if strings.ToLower(pool[i].ID) == lowerText {
			raw = append(raw, i)
		}
	}
	split, hasSplit := splitProviderPrefix(text, pool)
	namedIdx := -1
	if hasSplit {
		var providerIdxs []int
		for i := range pool {
			if string(pool[i].Provider) == split.provider {
				providerIdxs = append(providerIdxs, i)
			}
		}
		lowerModelText := strings.ToLower(split.modelText)
		// The provider's own literal id (OpenRouter's `openrouter/free`)
		// beats its fuzzy matches.
		for _, i := range providerIdxs {
			if strings.ToLower(pool[i].ID) == lowerModelText {
				namedIdx = i
				break
			}
		}
		if namedIdx == -1 {
			for _, i := range raw {
				if string(pool[i].Provider) == split.provider {
					namedIdx = i
					break
				}
			}
		}
		if namedIdx == -1 && fuzzy {
			namedIdx = bestFuzzyMatchIndex(pool, fuzzyMatchIndices(pool, providerIdxs, split.modelText))
		}
	}

	var others []int
	for _, i := range raw {
		if i != namedIdx {
			others = append(others, i)
		}
	}
	candidates := others
	if namedIdx != -1 {
		candidates = append([]int{namedIdx}, others...)
	}
	if len(candidates) == 0 {
		return ModelReferenceResult{}, false
	}
	if providerCount(pool, candidates) == 1 {
		model := pool[candidates[0]]
		return ModelReferenceResult{Status: "resolved", Model: &model}, true
	}
	if namedIdx != -1 && usable(string(pool[namedIdx].Provider)) {
		model := pool[namedIdx]
		return ModelReferenceResult{Status: "resolved", Model: &model}, true
	}
	var usableOthers []int
	for _, i := range others {
		if usable(string(pool[i].Provider)) {
			usableOthers = append(usableOthers, i)
		}
	}
	if len(usableOthers) == 1 {
		model := pool[usableOthers[0]]
		return ModelReferenceResult{Status: "resolved", Model: &model}, true
	}
	if len(usableOthers) == 0 && namedIdx != -1 {
		model := pool[namedIdx]
		return ModelReferenceResult{Status: "resolved", Model: &model}, true
	}
	return ModelReferenceResult{
		Status:      "ambiguous",
		Candidates:  modelsAt(pool, candidates),
		UsableCount: len(usableOthers),
	}, true
}

// selectFuzzy picks the best fuzzy match, preferring usable providers and
// reporting an ambiguous tie across providers (pi: selectFuzzy).
func selectFuzzy(pool []ai.Model, matchIdxs []int, usable func(provider string) bool) (ModelReferenceResult, bool) {
	if len(matchIdxs) == 0 {
		return ModelReferenceResult{}, false
	}
	multipleProviders := providerCount(pool, matchIdxs) > 1
	var usableIdxs []int
	if multipleProviders {
		for _, i := range matchIdxs {
			if usable(string(pool[i].Provider)) {
				usableIdxs = append(usableIdxs, i)
			}
		}
	}
	use := matchIdxs
	if len(usableIdxs) > 0 {
		use = usableIdxs
	}
	best := bestFuzzyMatchIndex(pool, use)
	if best == -1 {
		return ModelReferenceResult{}, false
	}
	// The same id on several providers would otherwise be decided by catalog
	// order.
	bestID := pool[best].ID
	var tied []int
	for _, i := range use {
		if pool[i].ID == bestID {
			tied = append(tied, i)
		}
	}
	if providerCount(pool, tied) == 1 {
		model := pool[best]
		return ModelReferenceResult{Status: "resolved", Model: &model}, true
	}
	usableCount := 0
	if len(usableIdxs) > 0 {
		usableCount = len(tied)
	}
	return ModelReferenceResult{
		Status:      "ambiguous",
		Candidates:  modelsAt(pool, tied),
		UsableCount: usableCount,
	}, true
}

// fuzzyMatchIndices filters indices to models whose id or name contains text
// (pi: fuzzyMatches).
func fuzzyMatchIndices(pool []ai.Model, indices []int, text string) []int {
	lowerText := strings.ToLower(text)
	var out []int
	for _, i := range indices {
		if strings.Contains(strings.ToLower(pool[i].ID), lowerText) ||
			strings.Contains(strings.ToLower(pool[i].Name), lowerText) {
			out = append(out, i)
		}
	}
	return out
}

// bestFuzzyMatchIndex prefers aliases (`claude-sonnet-4-5`) over dated
// versions (`claude-sonnet-4-5-20250929`), then the latest id (pi:
// bestFuzzyMatch).
func bestFuzzyMatchIndex(pool []ai.Model, indices []int) int {
	if len(indices) == 0 {
		return -1
	}
	var aliases []int
	for _, i := range indices {
		if isAlias(pool[i].ID) {
			aliases = append(aliases, i)
		}
	}
	list := indices
	if len(aliases) > 0 {
		list = aliases
	}
	best := list[0]
	for _, i := range list[1:] {
		if pool[i].ID > pool[best].ID {
			best = i
		}
	}
	return best
}

// providerCount counts distinct providers among the given pool indices.
func providerCount(pool []ai.Model, indices []int) int {
	seen := map[string]bool{}
	for _, i := range indices {
		seen[string(pool[i].Provider)] = true
	}
	return len(seen)
}

// identityIndices returns [0, len(pool)).
func identityIndices(pool []ai.Model) []int {
	out := make([]int, len(pool))
	for i := range pool {
		out[i] = i
	}
	return out
}

// modelsAt materializes the given pool indices.
func modelsAt(pool []ai.Model, indices []int) []ai.Model {
	out := make([]ai.Model, 0, len(indices))
	for _, i := range indices {
		out = append(out, pool[i])
	}
	return out
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

// buildFallbackModel builds a custom model id over a provider's default or
// first model (pi: buildFallbackModel).
func buildFallbackModel(providerModels []ai.Model, modelID string) ai.Model {
	baseModel := providerModels[0]
	if defaultID, ok := DefaultModelPerProvider[string(baseModel.Provider)]; ok && defaultID != "" {
		for _, model := range providerModels {
			if model.ID == defaultID {
				baseModel = model
				break
			}
		}
	}
	baseModel.ID = modelID
	baseModel.Name = modelID
	return baseModel
}

// ModelScopeDiagnostic is one scope warning (pi: ModelScopeDiagnostic).
type ModelScopeDiagnostic struct {
	Type string // "warning"
	// Code is "no-match" | "invalid-thinking-level" | "ambiguous".
	Code    string
	Message string
	Pattern string
}

// ResolveModelScopeResult is the structured scope outcome (pi:
// ResolveModelScopeResult).
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel
	Diagnostics  []ModelScopeDiagnostic
}

// ResolveModelScopeFromModels resolves scope patterns (`--models`,
// enabledModels) to models with optional thinking levels (pi:
// resolveModelScopeFromModels). Every model in models is usable, so
// credentials cannot choose between providers offering the same id; all of
// them are added, with a warning.
func ResolveModelScopeFromModels(patterns []string, models []ai.Model) ResolveModelScopeResult {
	scopedModels := []ScopedModel{}
	diagnostics := []ModelScopeDiagnostic{}
	add := func(model ai.Model, thinkingLevel agent.ThinkingLevel) {
		if !scopedModelsContains(scopedModels, model) {
			scopedModels = append(scopedModels, ScopedModel{Model: model, ThinkingLevel: thinkingLevel})
		}
	}
	noMatch := func(pattern string) {
		diagnostics = append(diagnostics, ModelScopeDiagnostic{
			Type:    "warning",
			Code:    "no-match",
			Message: fmt.Sprintf("No models match pattern %q", pattern),
			Pattern: pattern,
		})
	}
	ambiguous := func(pattern string, candidates []ai.Model) {
		seen := map[string]bool{}
		var providers []string
		for _, model := range candidates {
			if !seen[string(model.Provider)] {
				seen[string(model.Provider)] = true
				providers = append(providers, string(model.Provider))
			}
		}
		sort.Strings(providers)
		diagnostics = append(diagnostics, ModelScopeDiagnostic{
			Type:    "warning",
			Code:    "ambiguous",
			Message: fmt.Sprintf("Pattern %q matches several providers: %s. Use provider/id to choose one.", pattern, strings.Join(providers, ", ")),
			Pattern: pattern,
		})
	}

	for _, pattern := range patterns {
		// Check if the pattern contains glob characters.
		if strings.ContainsAny(pattern, "*?[") {
			// Extract the optional thinking level suffix (e.g. "provider/*:high").
			globPattern := pattern
			var thinkingLevel agent.ThinkingLevel
			if base, level, ok := splitThinkingSuffix(pattern); ok {
				globPattern = base
				thinkingLevel = level
			}

			// Ids such as `bracketed-model[1m]` are not globs.
			exact := ResolveModelReference(ModelReferenceRequest{
				Text:           globPattern,
				ThinkingSuffix: "none",
				Fuzzy:          false,
			}, models, alwaysUsable)
			var matches []ai.Model
			switch exact.Status {
			case "resolved":
				matches = []ai.Model{*exact.Model}
			case "ambiguous":
				matches = exact.Candidates
				ambiguous(pattern, matches)
			default:
				// Match against "provider/modelId" or just the model id, so
				// "*sonnet*" matches without requiring "anthropic/*sonnet*".
				for _, model := range models {
					fullID := string(model.Provider) + "/" + model.ID
					if utils.Minimatch(globPattern, fullID) || utils.Minimatch(globPattern, model.ID) {
						matches = append(matches, model)
					}
				}
			}
			if len(matches) == 0 {
				noMatch(pattern)
			}
			for _, model := range matches {
				add(model, thinkingLevel)
			}
			continue
		}

		result := ResolveModelReference(ModelReferenceRequest{
			Text:           pattern,
			ThinkingSuffix: "lenient",
			Fuzzy:          true,
		}, models, alwaysUsable)
		if result.Status == "not-found" {
			noMatch(pattern)
			continue
		}
		if result.Warning != "" {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type:    "warning",
				Code:    "invalid-thinking-level",
				Message: result.Warning,
				Pattern: pattern,
			})
		}
		if result.Status == "ambiguous" {
			ambiguous(pattern, result.Candidates)
		}
		var matches []ai.Model
		if result.Status == "resolved" {
			matches = []ai.Model{*result.Model}
		} else {
			matches = result.Candidates
		}
		for _, model := range matches {
			add(model, result.ThinkingLevel)
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

// ResolveCliModelOptions are ResolveCliModel's inputs (pi: the options
// object).
type ResolveCliModelOptions struct {
	CliProvider string
	CliModel    string
	CliThinking agent.ThinkingLevel
	// InlineApiKey is true when `--api-key` was supplied for the selected
	// provider (pi: inlineApiKey).
	InlineApiKey bool
	ModelRuntime ModelResolverRuntime
}

// ResolveCliModel resolves a single model from CLI flags (pi:
// resolveCliModel). Supports --provider <provider> --model <pattern>, --model
// <provider>/<pattern>, and fuzzy matching (exact id, then partial id/name).
func ResolveCliModel(options ResolveCliModelOptions) ResolveCliModelResult {
	cliModel := options.CliModel
	if cliModel == "" {
		return ResolveCliModelResult{}
	}

	// Important: use *all* models here, not just models with pre-configured
	// auth. This allows "--api-key" to be used for first-time setup.
	models := options.ModelRuntime.GetModels()
	if len(models) == 0 {
		return ResolveCliModelResult{
			Error: "No models available. Check your installation or add models to models.json.",
		}
	}

	provider := ""
	if options.CliProvider != "" {
		lower := strings.ToLower(options.CliProvider)
		for _, model := range models {
			if strings.ToLower(string(model.Provider)) == lower {
				provider = string(model.Provider)
				break
			}
		}
		if provider == "" {
			return ResolveCliModelResult{
				Error: fmt.Sprintf("Unknown provider %q. Use --list-models to see available providers/models.", options.CliProvider),
			}
		}
	}

	// An inline key goes to whichever provider is selected, so stored
	// credentials must not choose it.
	usable := func(providerID string) bool { return options.ModelRuntime.HasConfiguredAuth(providerID) }
	if options.InlineApiKey {
		usable = func(string) bool { return false }
	}

	result := ResolveModelReference(ModelReferenceRequest{
		Text:           cliModel,
		Provider:       provider,
		ThinkingSuffix: "strict",
		Fuzzy:          true,
	}, models, usable)
	if result.Status == "resolved" {
		return ResolveCliModelResult{Model: result.Model, ThinkingLevel: result.ThinkingLevel, Warning: result.Warning}
	}
	if result.Status == "ambiguous" {
		matches := make([]string, 0, len(result.Candidates))
		for _, model := range result.Candidates {
			matches = append(matches, string(model.Provider)+"/"+model.ID)
		}
		sort.Strings(matches)
		var authHint string
		switch {
		case options.InlineApiKey:
			authHint = "--api-key does not choose a provider."
		case result.UsableCount == 0:
			authHint = "No matching provider is authenticated."
		default:
			authHint = "More than one matching provider is authenticated."
		}
		return ResolveCliModelResult{
			Error: fmt.Sprintf("Model %q is ambiguous across providers: %s. %s Use --provider or provider/model.",
				cliModel, strings.Join(matches, ", "), authHint),
		}
	}

	target := result.Target
	if target == nil {
		return ResolveCliModelResult{
			Error: fmt.Sprintf("Model %q not found. Use --list-models to see available models.", cliModel),
		}
	}

	// Use the thinking level suffix only when --thinking is not explicitly
	// provided; otherwise it stays part of the custom model id (e.g.
	// "zai-org/GLM-5.1-FP8:high" → modelId="zai-org/GLM-5.1-FP8",
	// fallbackThinking="high").
	modelID := target.ModelID
	if options.CliThinking != "" {
		modelID = target.ModelText
	}
	fallbackThinking := target.ThinkingLevel
	if options.CliThinking != "" {
		fallbackThinking = ""
	}
	fallbackModel := buildFallbackModel(target.ProviderModels, modelID)
	requestedThinking := options.CliThinking
	if requestedThinking == "" {
		requestedThinking = fallbackThinking
	}
	if requestedThinking != "" && requestedThinking != "off" {
		fallbackModel.Reasoning = true
	}
	return ResolveCliModelResult{
		Model:         &fallbackModel,
		ThinkingLevel: fallbackThinking,
		Warning:       fmt.Sprintf("Model %q not found for provider %q. Using custom model id.", modelID, target.Provider),
	}
}

// InitialModelResult is findInitialModel's outcome (pi: InitialModelResult).
type InitialModelResult struct {
	Model           *ai.Model
	ThinkingLevel   agent.ThinkingLevel
	FallbackMessage string
}

// FindInitialModelOptions are FindInitialModel's inputs (pi: the options
// object; empty strings = undefined).
type FindInitialModelOptions struct {
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

// FindInitialModel finds the initial model by priority: first scoped model,
// saved default, then first available (pi: findInitialModel). CLI resolution
// happens in the caller (buildSessionOptions).
func FindInitialModel(options FindInitialModelOptions) InitialModelResult {
	// 1. Use the first model from scoped models (skip if continuing/resuming).
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

	// 2. Try the saved default from settings if auth is configured.
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

	// 3. Try the first available model with a valid API key.
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

	// 4. No model found.
	return InitialModelResult{ThinkingLevel: DefaultThinkingLevel}
}

// RestoreModelFromSession restores the model from a session, with fallback
// to available models (pi: restoreModelFromSession). shouldPrintMessages is
// passed explicitly in place of pi's default.
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
