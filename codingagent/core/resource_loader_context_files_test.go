// pi: test/resource-loader.test.ts — the "loadProjectContextFiles - nested
// worktree dedup" block. Builds a linked-worktree skeleton without a git
// binary: the main repo's `.git/worktrees/<name>/` holds `HEAD` plus a
// `commondir` pointing back at the main `.git`, and the worktree's working
// tree carries a `.git` file whose `gitdir:` resolves to it.
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func contextTestWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// contextTestLinkWorktree lays out <mainDir>/.git/worktrees/<name> plus the
// worktree's `.git` file (pi: linkWorktree).
func contextTestLinkWorktree(t *testing.T, mainDir string, worktreeDir string, name string) {
	t.Helper()
	gitDir := filepath.Join(mainDir, ".git", "worktrees", name)
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contextTestWriteFile(t, filepath.Join(mainDir, ".git", "HEAD"), "ref: refs/heads/main\n")
	contextTestWriteFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/feat\n")
	contextTestWriteFile(t, filepath.Join(gitDir, "commondir"), "../..")
	contextTestWriteFile(t, filepath.Join(worktreeDir, ".git"), "gitdir: "+gitDir+"\n")
}

type contextWorktreeFixture struct {
	outer       string
	main        string
	worktree    string
	worktreeSrc string
}

// contextTestSetupNestedWorktree creates <tempDir>/outer/main with a linked
// worktree at main/worktrees/feat (pi: setupNestedWorktree).
func contextTestSetupNestedWorktree(t *testing.T, tempDir string) contextWorktreeFixture {
	t.Helper()
	outer := filepath.Join(tempDir, "outer")
	main := filepath.Join(outer, "main")
	worktree := filepath.Join(main, "worktrees", "feat")
	worktreeSrc := filepath.Join(worktree, "src")
	if err := os.MkdirAll(worktreeSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	contextTestLinkWorktree(t, main, worktree, "feat")
	return contextWorktreeFixture{outer: outer, main: main, worktree: worktree, worktreeSrc: worktreeSrc}
}

func contextFileContents(files []AgentsFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Content)
	}
	return out
}

func contextFilePaths(files []AgentsFile) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	return out
}

func assertContextContents(t *testing.T, files []AgentsFile, want []string) {
	t.Helper()
	got := contextFileContents(files)
	if len(got) != len(want) {
		t.Fatalf("contents = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("contents = %v, want %v", got, want)
		}
	}
}

func TestLoadProjectContextFilesNestedWorktreeDedup(t *testing.T) {
	t.Run("should skip the main repo's duplicate when the worktree root has its own context", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		contextTestWriteFile(t, filepath.Join(fx.main, "AGENTS.md"), "main repo instructions")
		contextTestWriteFile(t, filepath.Join(fx.worktree, "AGENTS.md"), "worktree instructions")

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		assertContextContents(t, files, []string{"worktree instructions"})
	})

	// https://github.com/earendil-works/pi/issues/10681
	t.Run("should load the worktree's context once when it symlinks to the main repo's file", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		contextTestWriteFile(t, filepath.Join(fx.main, "AGENTS.md"), "main repo instructions")
		if err := os.Symlink(filepath.Join(fx.main, "AGENTS.md"), filepath.Join(fx.worktree, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		paths := contextFilePaths(files)
		if len(paths) != 1 || paths[0] != filepath.Join(fx.worktree, "AGENTS.md") {
			t.Fatalf("paths = %v, want [%s]", paths, filepath.Join(fx.worktree, "AGENTS.md"))
		}
	})

	t.Run("should skip the main repo's duplicate when the main repo's file is a symlink", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		shared := filepath.Join(fx.outer, "shared-agents.md")
		contextTestWriteFile(t, shared, "shared instructions")
		if err := os.Symlink(shared, filepath.Join(fx.main, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(shared, filepath.Join(fx.worktree, "AGENTS.md")); err != nil {
			t.Fatal(err)
		}

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		paths := contextFilePaths(files)
		if len(paths) != 1 || paths[0] != filepath.Join(fx.worktree, "AGENTS.md") {
			t.Fatalf("paths = %v, want [%s]", paths, filepath.Join(fx.worktree, "AGENTS.md"))
		}
	})

	t.Run("should still inherit the main repo's context when the worktree root has none", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		contextTestWriteFile(t, filepath.Join(fx.main, "AGENTS.md"), "main repo instructions")

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		assertContextContents(t, files, []string{"main repo instructions"})
	})

	t.Run("should only skip the same filename, not a differently named context file", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		contextTestWriteFile(t, filepath.Join(fx.main, "CLAUDE.md"), "main repo instructions")
		contextTestWriteFile(t, filepath.Join(fx.worktree, "AGENTS.md"), "worktree instructions")

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		assertContextContents(t, files, []string{"main repo instructions", "worktree instructions"})
	})

	t.Run("should NOT skip the container's context in a bare layout (proj/.bare + proj/main)", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		proj := filepath.Join(tempDir, "proj")
		bare := filepath.Join(proj, ".bare")
		worktree := filepath.Join(proj, "main")
		worktreeGitDir := filepath.Join(bare, "worktrees", "main")
		if err := os.MkdirAll(worktreeGitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(bare, "HEAD"), "ref: refs/heads/main\n")
		contextTestWriteFile(t, filepath.Join(worktreeGitDir, "HEAD"), "ref: refs/heads/main\n")
		contextTestWriteFile(t, filepath.Join(worktreeGitDir, "commondir"), "../..")
		contextTestWriteFile(t, filepath.Join(worktree, ".git"), "gitdir: "+worktreeGitDir+"\n")
		contextTestWriteFile(t, filepath.Join(proj, "AGENTS.md"), "container instructions")
		contextTestWriteFile(t, filepath.Join(worktree, "AGENTS.md"), "worktree instructions")

		files := LoadProjectContextFiles(worktree, agentDir)

		assertContextContents(t, files, []string{"container instructions", "worktree instructions"})
	})

	t.Run("should keep loading ancestors above the main repo", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		fx := contextTestSetupNestedWorktree(t, tempDir)
		contextTestWriteFile(t, filepath.Join(fx.outer, "AGENTS.md"), "outer instructions")
		contextTestWriteFile(t, filepath.Join(fx.main, "AGENTS.md"), "main repo instructions")
		contextTestWriteFile(t, filepath.Join(fx.worktree, "AGENTS.md"), "worktree instructions")

		files := LoadProjectContextFiles(fx.worktreeSrc, agentDir)

		assertContextContents(t, files, []string{"outer instructions", "worktree instructions"})
	})

	t.Run("should NOT skip anything for a sibling worktree (main repo is not an ancestor)", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		outer := filepath.Join(tempDir, "outer")
		main := filepath.Join(outer, "main")
		sib := filepath.Join(outer, "sib-feat")
		sibSrc := filepath.Join(sib, "src")
		if err := os.MkdirAll(sibSrc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(main, 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(outer, "AGENTS.md"), "outer instructions")
		contextTestWriteFile(t, filepath.Join(sib, "AGENTS.md"), "sibling worktree instructions")
		contextTestLinkWorktree(t, main, sib, "sib")

		files := LoadProjectContextFiles(sibSrc, agentDir)

		assertContextContents(t, files, []string{"outer instructions", "sibling worktree instructions"})
	})

	t.Run("should NOT skip the superproject's context from inside a submodule", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		sup := filepath.Join(tempDir, "super")
		sub := filepath.Join(sup, "vendor", "lib")
		subSrc := filepath.Join(sub, "src")
		if err := os.MkdirAll(subSrc, 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(sup, "AGENTS.md"), "superproject instructions")
		contextTestWriteFile(t, filepath.Join(sub, "AGENTS.md"), "submodule instructions")
		subGitDir := filepath.Join(sup, ".git", "modules", "vendor", "lib")
		if err := os.MkdirAll(subGitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(subGitDir, "HEAD"), "ref: refs/heads/main\n")
		contextTestWriteFile(t, filepath.Join(sub, ".git"), "gitdir: "+subGitDir+"\n")

		files := LoadProjectContextFiles(subSrc, agentDir)

		assertContextContents(t, files, []string{"superproject instructions", "submodule instructions"})
	})

	t.Run("should keep climbing past an ordinary repo root", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		outer := filepath.Join(tempDir, "outer")
		repo := filepath.Join(outer, "repo")
		leaf := filepath.Join(repo, "src")
		if err := os.MkdirAll(leaf, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
		contextTestWriteFile(t, filepath.Join(outer, "AGENTS.md"), "outer instructions")
		contextTestWriteFile(t, filepath.Join(repo, "AGENTS.md"), "repo instructions")
		contextTestWriteFile(t, filepath.Join(leaf, "AGENTS.md"), "leaf instructions")

		files := LoadProjectContextFiles(leaf, agentDir)

		assertContextContents(t, files, []string{"outer instructions", "repo instructions", "leaf instructions"})
	})

	t.Run("should climb normally when the gitdir: target does not exist", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := t.TempDir()
		repo := filepath.Join(tempDir, "corrupt")
		src := filepath.Join(repo, "src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		contextTestWriteFile(t, filepath.Join(repo, ".git"), "gitdir: /nonexistent/path/worktrees/feat\n")
		contextTestWriteFile(t, filepath.Join(repo, "AGENTS.md"), "repo instructions")
		contextTestWriteFile(t, filepath.Join(src, "AGENTS.md"), "src instructions")

		files := LoadProjectContextFiles(src, agentDir)

		assertContextContents(t, files, []string{"repo instructions", "src instructions"})
	})
}
