//go:build !windows

package utils

import (
	"errors"
	"syscall"
	"testing"
)

type killCall struct {
	pid    int
	signal syscall.Signal
}

func recordKills(t *testing.T, fail func(killCall) error) *[]killCall {
	t.Helper()
	previousGOOS, previousKill := processGOOS, killProcessGroup
	t.Cleanup(func() {
		processGOOS, killProcessGroup = previousGOOS, previousKill
	})
	processGOOS = "darwin"
	var calls []killCall
	killProcessGroup = func(pid int, signal syscall.Signal) error {
		call := killCall{pid: pid, signal: signal}
		calls = append(calls, call)
		if fail != nil {
			return fail(call)
		}
		return nil
	}
	return &calls
}

func TestKillProcessTreeStopsDarwinGroupBeforeKilling(t *testing.T) {
	var calls []killCall
	previousGOOS, previousKill := processGOOS, killProcessGroup
	t.Cleanup(func() {
		processGOOS, killProcessGroup = previousGOOS, previousKill
	})
	processGOOS = "darwin"
	killProcessGroup = func(pid int, signal syscall.Signal) error {
		calls = append(calls, killCall{pid: pid, signal: signal})
		return nil
	}
	KillProcessTree(1234)
	want := []killCall{{-1234, syscall.SIGSTOP}, {-1234, syscall.SIGKILL}}
	if len(calls) != len(want) || calls[0] != want[0] || calls[1] != want[1] {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestKillProcessTreeKillsGroupWhenStopFails(t *testing.T) {
	calls := recordKills(t, func(call killCall) error {
		if call.signal == syscall.SIGSTOP {
			return errors.New("stop failed")
		}
		return nil
	})
	KillProcessTree(1234)
	if len(*calls) != 2 || (*calls)[1].pid != -1234 || (*calls)[1].signal != syscall.SIGKILL {
		t.Fatalf("calls = %#v", *calls)
	}
}

func TestKillProcessTreeFallsBackToShell(t *testing.T) {
	calls := recordKills(t, func(call killCall) error {
		if call.pid < 0 && call.signal == syscall.SIGKILL {
			return errors.New("group kill failed")
		}
		return nil
	})
	KillProcessTree(1234)
	want := []killCall{{-1234, syscall.SIGSTOP}, {-1234, syscall.SIGKILL}, {1234, syscall.SIGKILL}}
	if len(*calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", *calls, want)
	}
	for index := range want {
		if (*calls)[index] != want[index] {
			t.Fatalf("calls = %#v, want %#v", *calls, want)
		}
	}
}

func TestKillProcessTreeKeepsLinuxBehavior(t *testing.T) {
	calls := recordKills(t, nil)
	processGOOS = "linux"
	KillProcessTree(1234)
	if len(*calls) != 1 || (*calls)[0].pid != -1234 || (*calls)[0].signal != syscall.SIGKILL {
		t.Fatalf("calls = %#v, want only SIGKILL", *calls)
	}
}
