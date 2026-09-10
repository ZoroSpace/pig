// pi: harness/session/fork-policy.ts — fork disposition of stored values.
package session

import "fmt"

// ForkDisposition is the final action for current state at one address (pi:
// ForkDisposition):
//   - "copy": emit the current value in the destination;
//   - "exclude": omit it from the destination;
//   - "reconstruct": do not copy the row because lane handling emits a
//     coherent replacement.
type ForkDisposition string

const (
	ForkDispositionCopy        ForkDisposition = "copy"
	ForkDispositionExclude     ForkDisposition = "exclude"
	ForkDispositionReconstruct ForkDisposition = "reconstruct"
)

// ClassifyForkAddress decides the final fork action for one current scalar
// address (pi: classifyForkAddress).
func ClassifyForkAddress(address Value, scope string, isEntryCopied func(entryID string) bool) (ForkDisposition, error) {
	switch address.Namespace {
	case namespaceSessionName:
		return ForkDispositionCopy, nil
	case namespaceEntryLabel:
		if isEntryCopied(address.Key) {
			return ForkDispositionCopy, nil
		}
		return ForkDispositionExclude, nil
	case namespaceBranchTip, namespaceLaneConfig, namespaceLaneState:
		return ForkDispositionReconstruct, nil
	case namespaceOperationResult:
		return ForkDispositionExclude, nil
	}
	if hasPrefix(address.Namespace, "pi.op.") || hasPrefix(address.Namespace, "pi.pending.") {
		return ForkDispositionExclude, nil
	}
	if address.Namespace == "pi" || hasPrefix(address.Namespace, "pi.") {
		return "", fmt.Errorf("Unknown reserved fork namespace: %s", address.Namespace)
	}
	if scope == ForkScopeTree {
		return ForkDispositionCopy, nil
	}
	return ForkDispositionExclude, nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
