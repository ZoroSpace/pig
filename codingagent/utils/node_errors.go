// node-style errno code mapping (pi surfaces error.code strings like
// "ENOENT"/"EACCES" in tool error messages; pig derives them from syscall
// errno values).
package utils

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// NodeErrCode maps an os error to node's errno code string; other errors
// yield "" (pi: error.code is undefined for non-errno errors).
func NodeErrCode(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		switch pathErr.Err {
		case syscall.ENOENT:
			return "ENOENT"
		case syscall.EACCES:
			return "EACCES"
		case syscall.EPERM:
			return "EPERM"
		case syscall.ENOTDIR:
			return "ENOTDIR"
		case syscall.EISDIR:
			return "EISDIR"
		case syscall.EINVAL:
			return "EINVAL"
		}
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "ENOENT"
	case errors.Is(err, os.ErrPermission):
		return "EACCES"
	}
	return ""
}

// FSAccessMessage formats an access failure the way node's fs.access throws
// ("ENOENT: no such file or directory, stat '<path>'"). Non-errno errors
// pass through unwrapped.
func FSAccessMessage(err error, path string) error {
	code := NodeErrCode(err)
	if code == "" {
		return err
	}
	operation := "stat"
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Op != "" {
		operation = pathErr.Op
	}
	return &NodeErr{Code: code, Message: fmt.Sprintf("%s: %s, %s '%s'", code, syscallErrText(err), operation, path), Err: err}
}

// NodeErrCodeOf extracts a NodeErr code from an error chain; "" when absent.
func NodeErrCodeOf(err error) string {
	var nodeErr *NodeErr
	if errors.As(err, &nodeErr) {
		return nodeErr.Code
	}
	return NodeErrCode(err)
}

func syscallErrText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// NodeErr carries a node errno code with its formatted message (pig
// addition: the tools branch on error.code; Go errors lose the code unless
// carried explicitly).
type NodeErr struct {
	// Code is the node errno code ("ENOENT", "EACCES", ...).
	Code string
	// Message is the formatted node-style message.
	Message string
	// Err is the underlying error.
	Err error
}

func (e *NodeErr) Error() string { return e.Message }
func (e *NodeErr) Unwrap() error { return e.Err }

// ErrnoCode extracts a node-style errno code ("EIO", "EPIPE", ...) from an
// error (pig addition for isDeadTerminalError; pi reads error.code on
// NodeJS.ErrnoException).
func ErrnoCode(err error) string {
	if nodeErr, ok := err.(*NodeErr); ok {
		return nodeErr.Code
	}
	var syscallErr syscall.Errno
	if errors.As(err, &syscallErr) {
		return errnoNames[syscallErr]
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return ErrnoCode(pathErr.Err)
	}
	var opErr *os.SyscallError
	if errors.As(err, &opErr) {
		return ErrnoCode(opErr.Err)
	}
	return ""
}

// errnoNames maps the errno codes pi branches on.
var errnoNames = map[syscall.Errno]string{
	syscall.EIO:      "EIO",
	syscall.EPIPE:    "EPIPE",
	syscall.ENOTCONN: "ENOTCONN",
}
