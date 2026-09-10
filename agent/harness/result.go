// pi: src/harness/result.ts — Result<T, E> and the TaggedError factory.
//
// Segment deviations:
//   - Result<TValue, TError> (the Result object with ok/err/isOk/isErr)
//     collapses to Go's (T, error) at every consumer; there is no runtime
//     counterpart (see types.go header note). pi's getOrThrow becomes plain
//     error propagation.
//   - matchError / ErrorMatchers are TS type-map machinery: Go consumers
//     dispatch with a type switch on the concrete tagged error structs. No
//     helper is ported.
//   - pi's TaggedError(tag) factory generates error classes at runtime; Go
//     declares one struct per tag, each embedding TaggedErrorBase (see
//     agent_harness.go for the instances). The embedded base reproduces the
//     toJSON shape exactly: {"_tag", "message", ...payload}.
package harness

import "errors"

// TaggedErrorBase carries the tag and message shared by every tagged error
// (pi: the TaggedErrorClass base). Embed by value in a concrete error struct
// and the promoted fields serialize as {"_tag", "message", ...payload},
// matching pi's toJSON.
type TaggedErrorBase struct {
	// Tag is the stable discriminant (pi: the _tag field / class name).
	Tag string `json:"_tag"`
	// Message is the error message (pi: the Error message). TS keeps message
	// on Error.prototype (not an own property) and re-adds it explicitly in
	// toJSON; the promoted field lands in the same JSON position.
	Message string `json:"message"`
}

func (e *TaggedErrorBase) Error() string    { return e.Message }
func (e *TaggedErrorBase) ErrorTag() string { return e.Tag }

// TaggedErrorValue is the runtime contract shared by tagged errors (pi:
// interface TaggedErrorValue<Tag>). pi's toJSON member is provided by the
// TaggedErrorBase embedding instead of a separate method.
type TaggedErrorValue interface {
	error
	ErrorTag() string
}

// AsTaggedError extracts a TaggedErrorValue from an error chain (pi:
// TaggedErrorClass.is(value) via instanceof; Go uses errors.As on the
// interface). Returns false for non-tagged errors.
func AsTaggedError(err error) (TaggedErrorValue, bool) {
	var tagged TaggedErrorValue
	if errors.As(err, &tagged) {
		return tagged, true
	}
	return nil, false
}
