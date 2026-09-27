package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// setServerDisabledInFile toggles the `disabled` flag of one server inside a
// shared mcpServers config file by JSON surgery, never a struct round-trip:
// these files are ecosystem-standard (Claude Code / Cursor .mcp.json) and a
// full decode into ServerConfig + re-marshal would drop every field pig does
// not model. The raw tree is decoded into a generic map (json.Number keeps
// number literals verbatim), only mcpServers[name].disabled is touched —
// set to true, or deleted on enable so the file stays clean — and the result
// is written back 2-space indented with a trailing newline. Declared
// deviation: JSON normalization (indent/key order) of the whole file; JSON
// has no comments, so nothing else can be lost. The original file mode is
// preserved.
func setServerDisabledInFile(path, name string, disabled bool) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read mcp config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return fmt.Errorf("mcp config %s has no mcpServers object", path)
	}
	entry, ok := servers[name].(map[string]any)
	if !ok {
		return fmt.Errorf("mcp config %s has no server %q", path, name)
	}
	if disabled {
		entry["disabled"] = true
	} else {
		delete(entry, "disabled")
	}

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return fmt.Errorf("encode mcp config %s: %w", path, err)
	}
	// Encode already appends the trailing newline.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, out.Bytes(), mode)
}
