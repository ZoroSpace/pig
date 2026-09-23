// crash_log_test.go — pi: test/crash-log.test.ts (v0.87.0, extension crash
// attribution suite; findExtensionStackMatches).
package core

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

// packageStackExtension builds a package-origin extension (pi:
// packageExtension helper).
func packageStackExtension(source, baseDir, entry string) *Extension {
	if entry == "" {
		entry = "extensions/index.ts"
	}
	normalizedBaseDir := strings.ReplaceAll(baseDir, "\\", "/")
	resolvedPath := normalizedBaseDir + "/" + entry
	return &Extension{
		Path:         resolvedPath,
		ResolvedPath: resolvedPath,
		SourceInfo: utils.SourceInfo{
			Path:    resolvedPath,
			Source:  source,
			Scope:   utils.SourceScopeUser,
			Origin:  utils.SourceOriginPackage,
			BaseDir: baseDir,
		},
	}
}

func TestFindExtensionStackMatches(t *testing.T) {
	t.Run("matches stack frames beneath loaded package roots", func(t *testing.T) {
		memory := packageStackExtension(
			"npm:pi-observational-memory",
			"/home/fedora/.pi/agent/npm/node_modules/pi-observational-memory",
			"",
		)
		unrelated := packageStackExtension("npm:unrelated", "/home/fedora/.pi/agent/npm/node_modules/unrelated", "")
		stack := "TypeError: Cannot read properties of undefined (reading 'runtime')\n" +
			"    at streamSimple (file:///home/fedora/.local/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/chunks/chunk-CMRUVXTE.js:1093:16944)\n" +
			"    at /home/fedora/.pi/agent/npm/node_modules/pi-observational-memory/src/agents/worker-stream.ts:43:45"

		got := FindExtensionStackMatches(stack, []*Extension{memory, unrelated})
		if len(got) != 1 || got[0] != "npm:pi-observational-memory" {
			t.Fatalf("got %v, want [npm:pi-observational-memory]", got)
		}
	})

	t.Run("normalizes Windows paths and deduplicates package extensions", func(t *testing.T) {
		root := `C:\Users\reporter\.pi\agent\npm\node_modules\@scope\memory`
		first := packageStackExtension("npm:@scope/memory", root, "extensions/first.ts")
		second := packageStackExtension("npm:@scope/memory", root, "extensions/second.ts")
		stack := "Error: broken\n" +
			`    at run (c:\users\reporter\.pi\agent\npm\node_modules\@scope\memory\src\worker.ts:4:2)`

		got := FindExtensionStackMatches(stack, []*Extension{first, second})
		if len(got) != 1 || got[0] != "npm:@scope/memory" {
			t.Fatalf("got %v, want [npm:@scope/memory]", got)
		}
	})

	t.Run("does not attribute sibling single-file packages", func(t *testing.T) {
		extension := func(name string) *Extension {
			return &Extension{
				Path:         "/plugins/" + name + ".ts",
				ResolvedPath: "/plugins/" + name + ".ts",
				SourceInfo: utils.SourceInfo{
					Path:    "/plugins/" + name + ".ts",
					Source:  "/plugins/" + name + ".ts",
					Scope:   utils.SourceScopeUser,
					Origin:  utils.SourceOriginPackage,
					BaseDir: "/plugins",
				},
			}
		}
		stack := "Error: broken\n    at run (file:///plugins/b.ts:4:2)"

		got := FindExtensionStackMatches(stack, []*Extension{extension("a"), extension("b")})
		if len(got) != 1 || got[0] != "/plugins/b.ts" {
			t.Fatalf("got %v, want [/plugins/b.ts]", got)
		}
	})

	t.Run("ignores extension paths in the error message", func(t *testing.T) {
		extension := packageStackExtension("npm:memory", "/tmp/node_modules/memory", "")
		stack := "Error: Failed to read " + extension.ResolvedPath + "\n    at run (file:///opt/pi/dist/core.js:4:2)"

		got := FindExtensionStackMatches(stack, []*Extension{extension})
		if len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("decodes frame paths independently from malformed error text", func(t *testing.T) {
		extension := &Extension{
			Path:         "/Users/reporter/.pi/agent/extensions/local memory/index.ts",
			ResolvedPath: "/Users/reporter/.pi/agent/extensions/local memory/index.ts",
			SourceInfo: utils.SourceInfo{
				Path:    "/Users/reporter/.pi/agent/extensions/local memory/index.ts",
				Source:  "local",
				Scope:   utils.SourceScopeUser,
				Origin:  utils.SourceOriginTopLevel,
				BaseDir: "/Users/reporter/.pi/agent/extensions",
			},
		}
		stack := "Error: progress 100%\n    at run (file:///Users/reporter/.pi/agent/extensions/local%20memory/worker.ts:4:2)"

		got := FindExtensionStackMatches(stack, []*Extension{extension})
		if len(got) != 1 || got[0] != extension.Path {
			t.Fatalf("got %v, want [%s]", got, extension.Path)
		}
	})
}
