// pi: test/harness/session/memory.test.ts — conformance suite driven against
// InMemorySessionRepo, plus the injectable id-generator test.
package session_test

import (
	"strconv"
	"testing"

	session "github.com/ZoroSpace/pig/agent/harness/session"
	sessiontesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
)

// TestInMemorySessionRepoConformance ports the vitest conformance dispatch:
// every case runs against a fresh InMemorySessionRepo fixture.
func TestInMemorySessionRepoConformance(t *testing.T) {
	factory := func(t *testing.T) session.SessionRepo {
		return session.NewInMemorySessionRepo()
	}
	cases := sessiontesting.CreateSessionBackendConformance(factory)
	sessiontesting.RunConformance(t, cases, factory)
}

// TestSessionUsesOneInjectableIDGeneratorAcrossLaneViews ports memory.test.ts
// "uses one injectable id generator across lane views".
func TestSessionUsesOneInjectableIDGeneratorAcrossLaneViews(t *testing.T) {
	nextID := 0
	generator := injectedGenerator{next: func() string {
		nextID++
		return "generated-" + strconv.Itoa(nextID)
	}}
	s := session.NewSession(session.NewInMemorySessionStorage(session.SessionMetadata{ID: "session", CreatedAt: 1}), &session.SessionOptions{IDGenerator: generator})

	mainID, err := s.AppendCustomEntry("note", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLane("thread", &mainID); err != nil {
		t.Fatal(err)
	}
	threadID, err := s.View("thread").AppendCustomEntry("note", nil)
	if err != nil {
		t.Fatal(err)
	}

	if mainID != "generated-1" {
		t.Fatalf("mainId = %s, want generated-1", mainID)
	}
	if threadID != "generated-2" {
		t.Fatalf("threadId = %s, want generated-2", threadID)
	}
}

// injectedGenerator is a test IdGenerator (pi: { next: () => ... } object
// literal).
type injectedGenerator struct {
	next func() string
}

func (g injectedGenerator) Next() string { return g.next() }
