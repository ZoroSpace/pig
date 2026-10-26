package main

import (
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
)

func TestIsKimiK3FamilyID(t *testing.T) {
	for _, id := range []string{"kimi-k3", "kimi-k3.1-pro", "kimi-k3.1-pro-highspeed", "kimi-k3-256k"} {
		if !isKimiK3FamilyID(id) {
			t.Errorf("%s should belong to the K3 family", id)
		}
	}
	for _, id := range []string{"kimi-k2.7-code", "kimi-k30", "moonshotai/kimi-k3", ""} {
		if isKimiK3FamilyID(id) {
			t.Errorf("%s should not belong to the K3 family", id)
		}
	}
}

func TestIsKimiCodingK3StyleUsesEffortOptions(t *testing.T) {
	effort := &ModelsDevModel{ReasoningOptions: []ModelsDevReasoningOption{{Type: "effort"}}}
	if !isKimiCodingK3Style(effort) {
		t.Error("effort models should be K3-style")
	}
	budget := &ModelsDevModel{ReasoningOptions: []ModelsDevReasoningOption{{Type: "budget_tokens"}}}
	if isKimiCodingK3Style(budget) {
		t.Error("budget-token models should not be K3-style")
	}
}

func TestProcessMoonshotModelsKimiK3FamilyMetadata(t *testing.T) {
	enabled := true
	data := ModelsDevCatalog{
		"moonshotai": {Models: map[string]*ModelsDevModel{
			"kimi-k3": {
				Name: "Kimi K3", ToolCall: &enabled, Reasoning: &enabled,
				Cost: &ModelsDevCost{
					Input: lo.ToPtr(9.0), Output: lo.ToPtr(9.0), CacheRead: lo.ToPtr(9.0), CacheWrite: lo.ToPtr(9.0),
				},
			},
			"kimi-k3.1-pro": {
				Name: "Kimi K3.1 Pro", ToolCall: &enabled, Reasoning: &enabled,
				Cost: &ModelsDevCost{
					Input: lo.ToPtr(2.0), Output: lo.ToPtr(10.0), CacheRead: lo.ToPtr(0.2), CacheWrite: lo.ToPtr(3.0),
				},
			},
			"kimi-k2.7-code": {
				Name: "Kimi K2.7 Code", ToolCall: &enabled, Reasoning: &enabled,
				Cost: &ModelsDevCost{
					Input: lo.ToPtr(0.6), Output: lo.ToPtr(2.5), CacheRead: lo.ToPtr(0.15), CacheWrite: lo.ToPtr(0.6),
				},
			},
		}},
	}
	byID := map[string]*ai.Model{}
	for _, model := range processMoonshotModels(data) {
		applyOpenAICompletionsTranscriptMetadata(model)
		byID[model.ID] = model
	}
	if byID["kimi-k3"] == nil || byID["kimi-k3"].Cost.Input != kimiK3Cost.Input ||
		byID["kimi-k3"].Cost.Output != kimiK3Cost.Output ||
		byID["kimi-k3"].Cost.CacheWrite != 0 {
		t.Errorf("kimi-k3 cost = %#v, want pinned %#v", byID["kimi-k3"], kimiK3Cost)
	}
	pointRelease := byID["kimi-k3.1-pro"]
	if pointRelease == nil || pointRelease.Cost.Input != 2 || pointRelease.Cost.Output != 10 ||
		pointRelease.Cost.CacheRead != 0.2 || pointRelease.Cost.CacheWrite != 0 {
		t.Errorf("kimi-k3.1-pro cost = %#v, want catalog rates with no cache write", pointRelease)
	}
	pointCompat, _ := pointRelease.Compat.(*ai.OpenAICompletionsCompat)
	if pointCompat == nil || pointCompat.SupportsMidConvoToolAdditions == nil ||
		!*pointCompat.SupportsMidConvoToolAdditions {
		t.Errorf("kimi-k3.1-pro should accept mid-conversation tool additions: %#v", pointRelease)
	}
	k2 := byID["kimi-k2.7-code"]
	if k2 == nil || k2.Cost.CacheWrite != 0.6 {
		t.Errorf("kimi-k2.7-code cost = %#v, want cache write retained", k2)
	}
	k2Compat, _ := k2.Compat.(*ai.OpenAICompletionsCompat)
	if k2Compat == nil || k2Compat.SupportsMidConvoToolAdditions != nil {
		t.Errorf("kimi-k2.7-code should not accept tool additions: %#v", k2)
	}
}

func TestApplyTranscriptMetadataKimiK3Family(t *testing.T) {
	model := &ai.Model{ID: "kimi-k3.1-pro", API: ai.ApiOpenAICompletions, Provider: ai.ProviderMoonshotAI}
	applyOpenAICompletionsTranscriptMetadata(model)
	compat, _ := model.Compat.(*ai.OpenAICompletionsCompat)
	if compat == nil || compat.SupportsMidConvoSystemMessages == nil ||
		!*compat.SupportsMidConvoSystemMessages {
		t.Errorf("system messages not enabled: %#v", model.Compat)
	}
	if compat.SupportsMidConvoToolAdditions == nil || !*compat.SupportsMidConvoToolAdditions {
		t.Errorf("tool additions not enabled: %#v", model.Compat)
	}
}
