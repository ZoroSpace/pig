// Regression for pi v1.0.3 4c6b724ea: ENOTTY (revoked tty on macOS) joins
// EIO/EPIPE/ENOTCONN as a dead-terminal errno, so a dead terminal is not
// reported as a crash.
package interactive

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

func TestIsDeadTerminalErrorRecognizesENOTTY(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"EIO", fmt.Errorf("read /dev/stdin: %w", os.NewSyscallError("read", syscall.EIO)), true},
		{"EPIPE", os.NewSyscallError("write", syscall.EPIPE), true},
		{"ENOTCONN plain", errors.New("transport disconnected"), false},
		{"ENOTTY", os.NewSyscallError("ioctl", syscall.ENOTTY), true},
		{"generic", errors.New("something else"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeadTerminalError(tc.err); got != tc.want {
				t.Fatalf("isDeadTerminalError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ENOTTY must be reachable through the wrapped-error unwalking in
// utils.ErrnoCode (PathError/SyscallError wrapping is how tty reads surface).
func TestErrnoCodeMapsENOTTY(t *testing.T) {
	wrapped := &os.PathError{Op: "read", Path: "/dev/tty", Err: syscall.ENOTTY}
	if got := utils.ErrnoCode(wrapped); got != "ENOTTY" {
		t.Fatalf("ErrnoCode = %q, want ENOTTY", got)
	}
}
