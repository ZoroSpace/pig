// pi: src/utils/uuid.ts — time-ordered UUIDv7 generation.
//
// Deviations:
//   - TS keeps lastTimestamp/sequence as module-level mutable state relying on
//     the single-threaded event loop; Go guards them with a package-level
//     mutex (same monotonic-counter semantics: same-millisecond calls advance
//     the sequence, sequence wrap bumps the millisecond).
//   - TS falls back to Math.random when crypto.getRandomValues is missing;
//     Go always uses crypto/rand.
package utils

import (
	crand "crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

var (
	uuidMu          sync.Mutex
	uuidLastTS      int64 = -1 // last millisecond seen; -Inf in TS
	uuidSequence    uint32
	uuidSequenceSet bool
)

func fillRandomBytes(b []byte) {
	if _, err := crand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
}

// UUIDv7 generates a time-ordered UUIDv7 (pi: uuidv7).
func UUIDv7() string {
	uuidMu.Lock()
	defer uuidMu.Unlock()

	var random [16]byte
	fillRandomBytes(random[:])
	timestamp := time.Now().UnixMilli()

	if !uuidSequenceSet || timestamp > uuidLastTS {
		// random[6..9] as a 32-bit big-endian sequence seed.
		uuidSequence = binary.BigEndian.Uint32(random[6:10])
		uuidLastTS = timestamp
		uuidSequenceSet = true
	} else {
		uuidSequence++
		if uuidSequence == 0 {
			uuidLastTS++
		}
	}

	var b [16]byte
	ts := uint64(uuidLastTS)
	b[0] = byte(ts >> 40)
	b[1] = byte(ts >> 32)
	b[2] = byte(ts >> 24)
	b[3] = byte(ts >> 16)
	b[4] = byte(ts >> 8)
	b[5] = byte(ts)
	b[6] = 0x70 | byte((uuidSequence>>28)&0x0f)
	b[7] = byte(uuidSequence >> 20)
	b[8] = 0x80 | byte((uuidSequence>>14)&0x3f)
	b[9] = byte(uuidSequence >> 6)
	b[10] = byte(uuidSequence&0x3f)<<2 | (random[10] & 0x03)
	b[11] = random[11]
	b[12] = random[12]
	b[13] = random[13]
	b[14] = random[14]
	b[15] = random[15]

	const hexdigits = "0123456789abcdef"
	hex := make([]byte, 0, 36)
	for i, byteVal := range b {
		switch i {
		case 4, 6, 8, 10:
			hex = append(hex, '-')
		}
		hex = append(hex, hexdigits[byteVal>>4], hexdigits[byteVal&0x0f])
	}
	return string(hex)
}
