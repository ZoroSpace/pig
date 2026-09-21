// extensions_runner_9068_test.go (pi: test/suite/regressions/9068-user-bash-
// fail-closed.test.ts runner half, pi v0.86.0) — a throwing user_bash handler
// or an invalid result fails closed; only undefined falls back to local
// execution. (The RPC half of the pi suite is not portable: pig's rpc mode is
// deferred — Phase 5. The interactive half rides the same EmitUserBash
// contract.)
package core

import (
	"context"
	"strings"
	"testing"

	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
)

func new9068Runner(t *testing.T, handler ExtensionHandler) (*ExtensionRunner, *[]ExtensionError) {
	t.Helper()
	runner := NewExtensionRunner([]*Extension{{
		Path:     "<ext>",
		Handlers: map[string][]ExtensionHandler{"user_bash": {handler}},
	}}, CreateExtensionRuntime(), "/tmp/project", &SessionManager{}, &ModelRegistry{})
	t.Cleanup(func() { runner.Invalidate("test done") })
	errors := &[]ExtensionError{}
	runner.OnError(func(err ExtensionError) { *errors = append(*errors, err) })
	return runner, errors
}

func userBashEvent() *UserBashEvent {
	return &UserBashEvent{Type: "user_bash", Command: "pwd", ExcludeFromContext: false, Cwd: "/tmp/project"}
}

func Test9068RunnerUserBashFailClosed(t *testing.T) {
	t.Run("reports and rejects when a handler throws", func(t *testing.T) {
		runner, errors := new9068Runner(t, func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
			return nil, context.Canceled
		})
		_, err := runner.EmitUserBash(context.Background(), userBashEvent())
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("err = %v, want the handler error re-thrown", err)
		}
		if len(*errors) != 1 || (*errors)[0].Event != "user_bash" || !strings.Contains((*errors)[0].Error, "context canceled") {
			t.Fatalf("extension errors = %+v", *errors)
		}
	})

	t.Run("rejects invalid handler results", func(t *testing.T) {
		for name, result := range map[string]any{
			"empty object":       &UserBashEventResult{},
			"both halves set":    &UserBashEventResult{Operations: &coretools.BashOperations{}, Result: &BashResult{}},
			"operations no exec": &UserBashEventResult{Operations: &coretools.BashOperations{}},
		} {
			runner, errors := new9068Runner(t, func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
				return result, nil
			})
			_, err := runner.EmitUserBash(context.Background(), userBashEvent())
			if err == nil || !strings.Contains(err.Error(), "Invalid user_bash handler result") {
				t.Fatalf("%s: err = %v, want invalid-result failure", name, err)
			}
			if len(*errors) != 1 || (*errors)[0].Event != "user_bash" {
				t.Fatalf("%s: extension errors = %+v", name, *errors)
			}
		}
	})

	t.Run("returns undefined when a handler returns nil", func(t *testing.T) {
		runner, _ := new9068Runner(t, func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
			return nil, nil
		})
		result, err := runner.EmitUserBash(context.Background(), userBashEvent())
		if err != nil || result != nil {
			t.Fatalf("result = %v, err = %v, want nil/nil (local execution)", result, err)
		}
	})

	t.Run("returns a valid interception", func(t *testing.T) {
		operations := &coretools.BashOperations{Exec: func(context.Context, string, string, *coretools.BashExecOptions) (*int, error) {
			code := 0
			return &code, nil
		}}
		runner, _ := new9068Runner(t, func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
			return &UserBashEventResult{Operations: operations}, nil
		})
		result, err := runner.EmitUserBash(context.Background(), userBashEvent())
		if err != nil || result == nil || result.Operations != operations {
			t.Fatalf("result = %+v, err = %v, want the operations interception", result, err)
		}
	})
}
