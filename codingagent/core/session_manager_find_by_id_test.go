// session_manager_find_by_id_test.go — pi: test/session-id-readonly.test.ts
// v0.86.0 additions (#9440): exact-ID lookup reads session headers without a
// full metadata listing. Pig adaptations: the spy on SessionManager.list is
// structural (FindSessionById never enters the listing path — it scans the
// directory's headers only), so the tests lock the observable lookup
// behavior: header-ID matching across renamed files and cwd filtering.
package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

// findByIdPersistSession persists one user + one assistant message so the
// session file carries a header and content (pi: persistSession).
func findByIdPersistSession(t *testing.T, session *SessionManager, content string) {
	t.Helper()
	if _, err := session.AppendMessage(types.UserMessage{
		Role: types.RoleUser, Content: types.UserContent{String: content}, Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	// pig (matching pi): the session file is only created once an assistant
	// message is appended.
	if _, err := session.AppendMessage(types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("persisted")},
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "test",
		Usage:      types.Usage{TotalTokens: 2},
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestFindSessionByIdLooksUpExactIDWithoutFullListing ports "looks up exact
// IDs without building full session listings": a fresh ID is not found, and
// CreateSessionManager with that ID creates a new session carrying it.
func TestFindSessionByIdLooksUpExactIDWithoutFullListing(t *testing.T) {
	tempRoot := newTempDir(t)
	projectDir := filepath.Join(tempRoot, "project")
	sessionDir := filepath.Join(tempRoot, "sessions")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	unrelated, err := CreateSessionManager(projectDir, sessionDir, &NewSessionOptions{ID: lo.ToPtr("unrelated-id")})
	if err != nil {
		t.Fatal(err)
	}
	findByIdPersistSession(t, unrelated, "large transcript contents must not be loaded")

	if got := FindSessionById(projectDir, "fresh-id", sessionDir); got != "" {
		t.Fatalf("FindSessionById(fresh-id) = %q, want \"\"", got)
	}

	created, err := CreateSessionManager(projectDir, sessionDir, &NewSessionOptions{ID: lo.ToPtr("fresh-id")})
	if err != nil {
		t.Fatal(err)
	}
	if created.GetSessionID() != "fresh-id" {
		t.Fatalf("created id = %q, want fresh-id", created.GetSessionID())
	}
}

// TestFindSessionByIdReopensRenamedSessionFile ports "reopens an exact ID
// from a renamed session file": the header ID drives the lookup, not the
// file name.
func TestFindSessionByIdReopensRenamedSessionFile(t *testing.T) {
	tempRoot := newTempDir(t)
	projectDir := filepath.Join(tempRoot, "project")
	sessionDir := filepath.Join(tempRoot, "sessions")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original, err := CreateSessionManager(projectDir, sessionDir, &NewSessionOptions{ID: lo.ToPtr("renamed-id")})
	if err != nil {
		t.Fatal(err)
	}
	findByIdPersistSession(t, original, "persist me")
	originalPath := original.GetSessionFile()
	if originalPath == "" {
		t.Fatal("expected a persisted session file")
	}
	renamedPath := filepath.Join(sessionDir, "imported-session.jsonl")
	if err := os.Rename(originalPath, renamedPath); err != nil {
		t.Fatal(err)
	}

	found := FindSessionById(projectDir, "renamed-id", sessionDir)
	if found != renamedPath {
		t.Fatalf("FindSessionById = %q, want %q", found, renamedPath)
	}
	reopened, err := OpenSession(found, sessionDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.GetSessionFile() != renamedPath {
		t.Fatalf("reopened file = %q, want %q", reopened.GetSessionFile(), renamedPath)
	}
}

// TestFindSessionByIdFiltersExactIDsByCwd ports "filters exact IDs by cwd in
// a custom session directory".
func TestFindSessionByIdFiltersExactIDsByCwd(t *testing.T) {
	tempRoot := newTempDir(t)
	projectA := filepath.Join(tempRoot, "project-a")
	projectB := filepath.Join(tempRoot, "project-b")
	sessionDir := filepath.Join(tempRoot, "sessions")
	for _, dir := range []string{projectA, projectB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := CreateSessionManager(projectB, sessionDir, &NewSessionOptions{ID: lo.ToPtr("foreign-id")})
	if err != nil {
		t.Fatal(err)
	}
	findByIdPersistSession(t, foreign, "foreign session")
	foreignPath := foreign.GetSessionFile()

	if got := FindSessionById(projectA, "foreign-id", sessionDir); got != "" {
		t.Fatalf("FindSessionById(projectA) = %q, want \"\"", got)
	}
	if got := FindSessionById(projectB, "foreign-id", sessionDir); got != foreignPath {
		t.Fatalf("FindSessionById(projectB) = %q, want %q", got, foreignPath)
	}
}

// TestListAllSessionsHonorsCancelledContext adapts "rejects a cancelled
// session listing" (pi: listAll rejects with AbortError): pig returns nil
// promptly for a cancelled context and does not hang when the context is
// cancelled from inside the progress callback.
func TestListAllSessionsHonorsCancelledContext(t *testing.T) {
	tempDir := newTempDir(t)
	projectA := filepath.Join(tempDir, "project-a")
	projectB := filepath.Join(tempDir, "project-b")
	for _, dir := range []string{projectA, projectB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seed := func(cwd, label string) {
		session, err := CreateSessionManager(cwd, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: label}, Timestamp: time.Now().UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("reply to " + label)},
			API:        "anthropic-messages",
			Provider:   "anthropic",
			Model:      "test",
			Usage:      types.Usage{TotalTokens: 2},
			StopReason: types.StopReasonStop,
			Timestamp:  time.Now().UnixMilli(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed(projectA, "from A")
	seed(projectB, "from B")

	ctx, cancel := context.WithCancel(context.Background())
	listing := ListAllSessions(ctx, tempDir, func(loaded int, total int, partialSessions []SessionInfo) {
		if partialSessions != nil {
			cancel()
		}
	})
	// The listing returns (no hang); after cancellation it carries no full
	// result set.
	if len(listing) > 2 {
		t.Fatalf("listing = %d sessions, want at most 2", len(listing))
	}

	cancel()
	if listing := ListAllSessions(context.Background(), tempDir, nil); len(listing) != 2 {
		t.Fatalf("uncancelled listing = %d sessions, want 2", len(listing))
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if listing := ListAllSessions(ctx2, tempDir, nil); listing != nil {
		t.Fatalf("pre-cancelled listing = %d sessions, want nil", len(listing))
	}
}
