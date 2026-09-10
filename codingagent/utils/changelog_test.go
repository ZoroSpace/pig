package utils

import (
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent"
)

// pi: test/changelog.test.ts (normalizeChangelogLinks describe, both tests).
func TestNormalizeChangelogLinks(t *testing.T) {
	entry := ChangelogEntry{Major: 0, Minor: 79, Patch: 0}

	t.Run("rewrites package-relative changelog links to tag-pinned GitHub source links", func(t *testing.T) {
		markdown := strings.Join([]string{
			"[Project Trust](README.md#project-trust)",
			"[Extensions](docs/extensions.md#project_trust)",
			"[Examples](examples/extensions/)",
			"[Root README](../../README.md#supply-chain-hardening)",
		}, "\n")

		want := strings.Join([]string{
			"[Project Trust](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/README.md#project-trust)",
			"[Extensions](https://github.com/earendil-works/pi/blob/v0.79.0/packages/coding-agent/docs/extensions.md#project_trust)",
			"[Examples](https://github.com/earendil-works/pi/tree/v0.79.0/packages/coding-agent/examples/extensions/)",
			"[Root README](https://github.com/earendil-works/pi/blob/v0.79.0/README.md#supply-chain-hardening)",
		}, "\n")

		if got := NormalizeChangelogLinks(markdown, entry); got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("canonicalizes old repository URLs without changing external links", func(t *testing.T) {
		markdown := strings.Join([]string{
			"[#5167](https://github.com/earendil-works/pi-mono/pull/5167)",
			"[#4163](https://github.com/badlogic/pi-mono/issues/4163)",
			"[Agent README](https://github.com/badlogic/pi-mono/blob/main/packages/agent/README.md)",
			"[External](https://example.com/docs)",
			"[Local anchor](#settings)",
		}, "\n")

		want := strings.Join([]string{
			"[#5167](https://github.com/earendil-works/pi/pull/5167)",
			"[#4163](https://github.com/earendil-works/pi/issues/4163)",
			"[Agent README](https://github.com/earendil-works/pi/blob/v0.79.0/packages/agent/README.md)",
			"[External](https://example.com/docs)",
			"[Local anchor](#settings)",
		}, "\n")

		if got := NormalizeChangelogLinks(markdown, "0.79.0"); got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
}

// pig-side coverage: parseChangelog/compareVersions/getNewEntries (pi has no
// direct unit tests; behavior locked here).
func TestParseChangelogAndVersions(t *testing.T) {
	path := t.TempDir() + "/CHANGELOG.md"
	content := "# Changelog\n\n## [0.2.0]\n\n- two\n\n## [0.1.0]\n\n- one\n\n## Other section\nignored\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := ParseChangelog(path)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Major != 0 || entries[0].Minor != 2 || entries[0].Patch != 0 {
		t.Errorf("bad first version: %+v", entries[0])
	}
	if entries[0].Content != "## [0.2.0]\n\n- two" {
		t.Errorf("bad first content: %q", entries[0].Content)
	}

	if v := CompareVersions(entries[0], entries[1]); v <= 0 {
		t.Errorf("expected 0.2.0 > 0.1.0, got %d", v)
	}

	if missing := ParseChangelog(t.TempDir() + "/none.md"); len(missing) != 0 {
		t.Errorf("missing file should yield no entries")
	}

	newer := GetNewEntries(entries, "0.1.0")
	if len(newer) != 1 || newer[0].Minor != 2 {
		t.Errorf("expected only 0.2.0 newer than 0.1.0, got %+v", newer)
	}

	if got := codingagent.GetChangelogPath(); got == "" {
		t.Errorf("GetChangelogPath returned empty")
	}
}
