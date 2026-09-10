// pi: src/core/session-export.ts — write the current session branch and
// optional trailing export-only entries as JSONL.
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

// ExportSessionToJsonl writes the current session branch and optional
// trailing export-only entries as JSONL, returning the file path (pi:
// exportSessionToJsonl). The trailing callback receives the last branch
// entry's parent id (nil when the branch is empty) and the export timestamp.
// An empty outputPath defaults to "session-<timestamp>.jsonl" in the process
// cwd.
func ExportSessionToJsonl(sessionManager *SessionManager, outputPath string, createTrailingEntries func(parentID *string, timestamp string) []any) (string, error) {
	defaultName := "session-" + strings.ReplaceAll(strings.ReplaceAll(time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), ":", "-"), ".", "-") + ".jsonl"
	name := outputPath
	if name == "" {
		name = defaultName
	}
	filePath := utils.ResolvePath(name, "", nil)
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	version := CURRENT_SESSION_VERSION
	header := &SessionHeader{
		Type:      "session",
		Version:   &version,
		ID:        sessionManager.GetSessionID(),
		Timestamp: timestamp,
		Cwd:       sessionManager.GetCwd(),
	}
	headerLine, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	lines := []string{string(headerLine)}

	var parentID *string
	for _, entry := range sessionManager.GetBranch("") {
		line, err := entryLine(entry, parentID)
		if err != nil {
			return "", err
		}
		lines = append(lines, line)
		id := entry.Base().ID
		parentID = &id
	}
	if createTrailingEntries != nil {
		for _, entry := range createTrailingEntries(parentID, timestamp) {
			line, err := json.Marshal(entry)
			if err != nil {
				return "", err
			}
			lines = append(lines, string(line))
		}
	}

	if err := os.WriteFile(filePath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return filePath, nil
}

// entryLine renders one entry with its parent id override (pi: {...entry,
// parentId} — the spread overwrites whatever parentId the entry carried).
func entryLine(entry SessionEntry, parentID *string) (string, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return "", err
	}
	if parentID == nil {
		object["parentId"] = nil
	} else {
		object["parentId"] = *parentID
	}
	line, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	return string(line), nil
}
