// Package utils provides shared leaf-level helpers used across pig packages
// (pig-internal; no corresponding pi package, see AGENTS.md §3.1 exceptions
// like constants/).
package utils

import "reflect"

// SameFuncValue reports whether two func values refer to the same function.
// Go func values are not comparable; code-pointer equality stands in for the
// JS reference identity the pi ports rely on (e.g. removing a registered
// listener, `this.agent.streamFunction === streamSimple`). It is exact for
// the register-then-unregister-same-reference pattern, but cannot
// distinguish two distinct closure instances from the same literal —
// comparison then depends on compiler inlining, so freshly-built closures
// must not be matched with it. A nil or non-func value yields false.
func SameFuncValue[T any](a, b T) bool {
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if va.Kind() != reflect.Func || vb.Kind() != reflect.Func {
		return false
	}
	return !va.IsNil() && !vb.IsNil() && va.Pointer() == vb.Pointer()
}
