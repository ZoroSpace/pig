// R2 tests: session/state command handlers (see docs/handoffs/RPC-MODE.md §4).
package rpc

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func openaiTestModel() *types.Model {
	return &types.Model{ID: "gpt-4o-mini", Provider: "openai", API: ai.ApiOpenAICompletions, BaseURL: "http://localhost:0"}
}

func (h *rpcTestHarness) response(id string) map[string]any {
	for _, record := range h.parsed() {
		if record["id"] == id && record["type"] == "response" {
			return record
		}
	}
	return nil
}

func (h *rpcTestHarness) responseData(id string) map[string]any {
	record := h.waitResponse(id)
	data, _ := record["data"].(map[string]any)
	return data
}

func (h *rpcTestHarness) waitResponse(id string) map[string]any {
	h.t.Helper()
	h.waitFor("response "+id, func() bool { return h.response(id) != nil })
	return h.response(id)
}

func TestRpcSetSessionNameAndGetState(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"n1","type":"set_session_name","name":"  My Session  "}`)
	if record := h.waitResponse("n1"); record["success"] != true {
		t.Fatalf("set_session_name failed: %v", record)
	}

	h.sendLine(`{"id":"s1","type":"get_state"}`)
	state := h.responseData("s1")
	if state["sessionName"] != "My Session" {
		t.Fatalf("sessionName = %v, want trimmed name", state["sessionName"])
	}

	h.sendLine(`{"id":"n2","type":"set_session_name","name":"   "}`)
	if record := h.waitResponse("n2"); record["success"] != false {
		t.Fatalf("empty session name should fail: %v", record)
	}
}

func TestRpcClearQueue(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"st1","type":"steer","message":"steering one"}`)
	if record := h.waitResponse("st1"); record["success"] != true {
		t.Fatalf("steer failed: %v", record)
	}
	h.sendLine(`{"id":"fu1","type":"follow_up","message":"follow up one"}`)
	h.waitResponse("fu1")

	h.sendLine(`{"id":"cq1","type":"clear_queue"}`)
	data := h.responseData("cq1")
	steering, _ := data["steering"].([]any)
	followUp, _ := data["followUp"].([]any)
	if len(steering) != 1 || steering[0] != "steering one" {
		t.Fatalf("steering = %v, want [steering one]", data["steering"])
	}
	if len(followUp) != 1 || followUp[0] != "follow up one" {
		t.Fatalf("followUp = %v, want [follow up one]", data["followUp"])
	}

	h.sendLine(`{"id":"cq2","type":"clear_queue"}`)
	data2 := h.responseData("cq2")
	steering2, _ := data2["steering"].([]any)
	followUp2, _ := data2["followUp"].([]any)
	if len(steering2) != 0 || len(followUp2) != 0 {
		t.Fatalf("second clearQueue not empty: %v", data2)
	}
}

func TestRpcGetMessagesAndLastAssistantText(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"p1","type":"prompt","message":"Hello"}`)
	h.waitFor("agent settled", func() bool {
		for _, record := range h.parsed() {
			if record["type"] == "agent_settled" {
				return true
			}
		}
		return false
	})

	h.sendLine(`{"id":"m1","type":"get_messages"}`)
	messages, _ := h.responseData("m1")["messages"].([]any)
	if len(messages) < 2 {
		t.Fatalf("messages = %v, want at least user + assistant", messages)
	}

	h.sendLine(`{"id":"t1","type":"get_last_assistant_text"}`)
	if text := h.responseData("t1")["text"]; text != "done" {
		t.Fatalf("last assistant text = %v, want done", text)
	}

	h.sendLine(`{"id":"e1","type":"get_entries"}`)
	entries, _ := h.responseData("e1")["entries"].([]any)
	if len(entries) == 0 {
		t.Fatalf("entries empty after a prompt")
	}
	if leaf := h.responseData("e1")["leafId"]; leaf == nil {
		t.Fatalf("leafId missing: %v", h.responseData("e1"))
	}
	first, _ := entries[0].(map[string]any)
	sinceID, _ := first["id"].(string)
	if sinceID == "" {
		t.Fatalf("first entry has no id: %v", first)
	}

	h.sendLine(`{"id":"e2","type":"get_entries","since":"` + sinceID + `"}`)
	if got := h.responseData("e2")["entries"].([]any); len(got) != len(entries)-1 {
		t.Fatalf("since cursor returned %d entries, want %d", len(got), len(entries)-1)
	}

	h.sendLine(`{"id":"e3","type":"get_entries","since":"does-not-exist"}`)
	if record := h.waitResponse("e3"); record["success"] != false {
		t.Fatalf("unknown since cursor should fail: %v", record)
	}

	h.sendLine(`{"id":"tr1","type":"get_tree"}`)
	if treeData := h.responseData("tr1"); treeData["leafId"] == nil {
		t.Fatalf("get_tree missing leafId: %v", treeData)
	}
}

func TestRpcGetCommandsEmptyShapes(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"c1","type":"get_commands"}`)
	data := h.responseData("c1")
	commands, ok := data["commands"].([]any)
	if !ok {
		t.Fatalf("commands is not an array (must be [] not null): %v", data["commands"])
	}
	if len(commands) != 0 {
		t.Fatalf("expected no extension/prompt/skill commands, got %v", commands)
	}
}

func TestRpcSetSteeringModeReflectedInState(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"sm1","type":"set_steering_mode","mode":"one-at-a-time"}`)
	if record := h.waitResponse("sm1"); record["success"] != true {
		t.Fatalf("set_steering_mode failed: %v", record)
	}
	h.sendLine(`{"id":"s1","type":"get_state"}`)
	if mode := h.responseData("s1")["steeringMode"]; mode != "one-at-a-time" {
		t.Fatalf("steeringMode = %v, want one-at-a-time", mode)
	}
}
