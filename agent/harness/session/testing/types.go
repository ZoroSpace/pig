// pi: harness/session/testing/types.ts — conformance infrastructure types.
//
// pi ships session/testing in src; pig keeps it as a regular package under
// agent/harness/session/testing, consumed by test files.
package testing

import (
	"testing"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// StorageFixture is a fresh backend storage instance owned by one test or
// benchmark case (pi: StorageFixture; the AsyncDisposable collapse is the
// case runner's cleanup).
type StorageFixture struct {
	Storage session.Storage
	Close   func()
}

// ConformanceCase is a runner-independent conformance case (pi:
// ConformanceCase); the async run collapses to a func taking *testing.T.
type ConformanceCase struct {
	Group string
	Name  string
	Run   func(t *testing.T)
}

// RunConformanceCases executes the cases as subtests, grouped by Group.
func RunConformanceCases(t *testing.T, cases []ConformanceCase) {
	for _, c := range cases {
		c := c
		t.Run(c.Group+"/"+c.Name, func(t *testing.T) {
			t.Parallel()
			c.Run(t)
		})
	}
}
