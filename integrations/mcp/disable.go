package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// setServerDisabledInFile toggles the `disabled` flag of one server inside a
// shared mcpServers config file by JSON surgery, never a struct round-trip:
// these files are ecosystem-standard (Claude Code / Cursor .mcp.json) and a
// full decode into ServerConfig + re-marshal would drop every field pig does
// not model. The raw tree is decoded into a generic map (json.Number keeps
// number literals verbatim), only mcpServers[name].disabled is touched — set
// to true, or deleted on enable so the file stays clean — and the result is
// written back 2-space indented with a trailing newline. Declared deviation:
// JSON normalization (indent/key order) of the whole file; JSON has no
// comments, so nothing else can be lost. The original file mode is preserved.
//
// With create (pi's updateMcpServerConfig override option), a missing file or
// a missing entry is created instead of erroring — project overrides are
// written to .pig/mcp.json before that file exists. The enable semantics
// follow pi's keepDefaults (1387af7b4): an entry without `command`/`url` keys
// is itself an override, so its explicit value replaces the overridden
// server's — enable writes `"disabled": false` instead of deleting the key.
func setServerDisabledInFile(path, name string, disabled bool, create bool) error {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && create {
			// The project override target may live in a .pig directory that
			// does not exist yet.
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create mcp config dir: %w", err)
			}
			content = []byte("{}")
		} else {
			return fmt.Errorf("read mcp config: %w", err)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("parse mcp config %s: %w", path, err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		if !create {
			return fmt.Errorf("mcp config %s has no mcpServers object", path)
		}
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	entry, ok := servers[name].(map[string]any)
	if !ok {
		if !create {
			return fmt.Errorf("mcp config %s has no server %q", path, name)
		}
		entry = map[string]any{}
		servers[name] = entry
	}
	keepDefaults := false
	if _, ok := entry["command"]; !ok {
		if _, ok := entry["url"]; !ok {
			keepDefaults = true
		}
	}
	if disabled || keepDefaults {
		entry["disabled"] = disabled
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
