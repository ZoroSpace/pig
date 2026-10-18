// R2 tests (continued): bash / export / fork+clone handlers.
package rpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRpcBashCommand(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"b1","type":"bash","command":"echo hello-rpc"}`)
	data := h.responseData("b1")
	output, _ := data["output"].(string)
	if !strings.Contains(output, "hello-rpc") {
		t.Fatalf("bash output = %q, want it to contain hello-rpc", output)
	}

	h.sendLine(`{"id":"ab1","type":"abort_bash"}`)
	if record := h.waitResponse("ab1"); record["success"] != true {
		t.Fatalf("abort_bash failed: %v", record)
	}
}

func TestRpcExportHTMLPropagatesError(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	// The in-memory test session has no session file, so the export handler
	// must surface the core error (the success path is covered by core's
	// export_html tests).
	outputPath := filepath.Join(t.TempDir(), "session.html")
	h.sendLine(`{"id":"x1","type":"export_html","outputPath":"` + outputPath + `"}`)
	record := h.waitResponse("x1")
	if record["success"] != false {
		t.Fatalf("export of an in-memory session should fail: %v", record)
	}
	message, _ := record["error"].(string)
	if !strings.Contains(message, "Cannot export in-memory session") {
		t.Fatalf("error = %q", message)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("no file should be written on failure, stat err = %v", err)
	}
}

func TestRpcForkAndClone(t *testing.T) {
	session := rpcTestSession(t, openaiTestModel(), true, 0)
	h := startRpcTest(t, session)

	// No entries yet: clone must report the "no current entry" error.
	h.sendLine(`{"id":"c0","type":"clone"}`)
	if record := h.waitResponse("c0"); record["success"] != false {
		t.Fatalf("clone on empty session should fail: %v", record)
	}

	h.sendLine(`{"id":"p1","type":"prompt","message":"Hello"}`)
	h.waitFor("agent settled", func() bool {
		for _, record := range h.parsed() {
			if record["type"] == "agent_settled" {
				return true
			}
		}
		return false
	})

	// The fake host cancels forks; the handler still reports the shape.
	h.sendLine(`{"id":"c1","type":"clone"}`)
	if data := h.responseData("c1"); data["cancelled"] != true {
		t.Fatalf("clone data = %v, want cancelled true", data)
	}

	h.sendLine(`{"id":"f1","type":"get_fork_messages"}`)
	messages, ok := h.responseData("f1")["messages"].([]any)
	if !ok {
		t.Fatalf("get_fork_messages messages is not an array")
	}
	if len(messages) == 0 {
		t.Fatalf("expected at least one forkable user message")
	}

	h.sendLine(`{"id":"f2","type":"fork","entryId":"whatever"}`)
	if record := h.waitResponse("f2"); record["success"] != true {
		t.Fatalf("fork failed: %v", record)
	}
}
