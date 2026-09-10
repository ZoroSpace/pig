// tui/kill_ring.go — ported from pi: packages/tui/src/kill-ring.ts

package tui

// KillRing is a ring buffer for Emacs-style kill/yank operations.
//
// Tracks killed (deleted) text entries. Consecutive kills can accumulate
// into a single entry. Supports yank (paste most recent) and yank-pop
// (cycle through older entries).
type KillRing struct {
	ring []string
}

// KillRingPushOptions mirrors pi's push opts parameter.
type KillRingPushOptions struct {
	// Prepend: if accumulating, prepend (backward deletion) or append (forward deletion).
	Prepend bool
	// Accumulate merges with the most recent entry instead of creating a new one.
	Accumulate bool
}

// Push adds text to the kill ring.
func (k *KillRing) Push(text string, opts KillRingPushOptions) {
	if text == "" {
		return
	}
	if opts.Accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		if opts.Prepend {
			k.ring = append(k.ring, text+last)
		} else {
			k.ring = append(k.ring, last+text)
		}
	} else {
		k.ring = append(k.ring, text)
	}
}

// Peek gets the most recent entry without modifying the ring.
func (k *KillRing) Peek() (string, bool) {
	if len(k.ring) == 0 {
		return "", false
	}
	return k.ring[len(k.ring)-1], true
}

// Rotate moves the last entry to front (for yank-pop cycling).
func (k *KillRing) Rotate() {
	if len(k.ring) > 1 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		k.ring = append([]string{last}, k.ring...)
	}
}

// Len returns the number of entries in the ring.
func (k *KillRing) Len() int {
	return len(k.ring)
}
