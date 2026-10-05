// pi: test/azure-reasoning-replay.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// newAzureReplayModel is the replay test's fixture model (pi: createModel).
func newAzureReplayModel() *types.Model {
	return &types.Model{
		ID: "gpt-5-mini", Name: "GPT-5 Mini", API: types.ApiAzureOpenAIResponses,
		Provider: types.ProviderID("azure"), BaseURL: "https://example.invalid",
		Reasoning: true, Input: []types.ModelInput{types.ModelInputText},
		Cost:          responsesModelCost(0, 0, 0, 0),
		ContextWindow: 400000, MaxTokens: 128000,
	}
}

// azureReplayEvents builds the added/done/completed sequence with the given
// output_item.done reasoning item and response.completed reasoning item (pi:
// createEvents).
func azureReplayEvents(doneItem, completedItem *responsesOutputItem) func(yield func(*responsesStreamEvent) bool) {
	return responsesEventSeq(
		&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
			Type: "reasoning", ID: doneItem.ID,
		}},
		&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: doneItem},
		&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{
			ID: "resp_test", Status: "completed", Output: []*responsesOutputItem{completedItem},
		}},
	)
}

// getAzureReplayedReasoning replays the assistant turn and returns the
// reasoning input item (pi: getReplayedReasoning).
func getAzureReplayedReasoning(t *testing.T, model *types.Model, assistant *types.AssistantMessage) map[string]any {
	t.Helper()
	llmContext := utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			responsesUserMessage("first"),
			*assistant,
			responsesUserMessage("follow-up"),
		},
	})
	input := ConvertResponsesMessages(model, llmContext, map[string]struct{}{"azure": {}}, nil)
	for _, item := range input {
		if m, ok := item.(map[string]any); ok && m["type"] == "reasoning" {
			return m
		}
	}
	return nil
}

func TestAzureOpenAIResponsesReasoningReplay(t *testing.T) {
	t.Run("preserves existing encrypted_content from output_item.done", func(t *testing.T) {
		model := newAzureReplayModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		doneItem := &responsesOutputItem{Type: "reasoning", ID: "rs_done", Raw: []byte(`{"type":"reasoning","id":"rs_done","summary":[],"encrypted_content":"from-output-item-done"}`)}
		doneItem.Summary = []responsesTextPart{}
		completedItem := &responsesOutputItem{Type: "reasoning", ID: "rs_done", EncryptedContent: "from-response-completed"}
		completedItem.Summary = []responsesTextPart{}

		if err := ProcessResponsesStream(azureReplayEvents(doneItem, completedItem), output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		replayed := getAzureReplayedReasoning(t, model, output)
		if replayed == nil {
			t.Fatal("expected reasoning input item")
		}
		if replayed["id"] != "rs_done" || replayed["encrypted_content"] != "from-output-item-done" {
			t.Fatalf("replayed = %v", replayed)
		}
	})

	t.Run("fills encrypted_content when output_item.done omitted it", func(t *testing.T) {
		model := newAzureReplayModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		doneItem := &responsesOutputItem{Type: "reasoning", ID: "rs_missing", Raw: []byte(`{"type":"reasoning","id":"rs_missing","summary":[]}`)}
		doneItem.Summary = []responsesTextPart{}
		completedItem := &responsesOutputItem{Type: "reasoning", ID: "rs_missing", EncryptedContent: "from-response-completed"}
		completedItem.Summary = []responsesTextPart{}

		if err := ProcessResponsesStream(azureReplayEvents(doneItem, completedItem), output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		replayed := getAzureReplayedReasoning(t, model, output)
		if replayed == nil {
			t.Fatal("expected reasoning input item")
		}
		if replayed["id"] != "rs_missing" || replayed["encrypted_content"] != "from-response-completed" {
			t.Fatalf("replayed = %v", replayed)
		}
	})
}
