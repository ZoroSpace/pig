// pi: utils/hash.ts.
package utils

// ShortHash is the fast deterministic hash used to shorten long strings (pi:
// shortHash, a 64-bit splitmix-style two-lane hash rendered in base-36).
func ShortHash(str string) string {
	h1 := uint32(0xdeadbeef)
	h2 := uint32(0x41c6ce57)
	for i := 0; i < len(str); i++ {
		ch := uint32(str[i])
		h1 = (h1 ^ ch) * 2654435761
		h2 = (h2 ^ ch) * 1597334677
	}
	h1 = ((h1 ^ (h1 >> 16)) * 2246822507) ^ ((h2 ^ (h2 >> 13)) * 3266489909)
	h2 = ((h2 ^ (h2 >> 16)) * 2246822507) ^ ((h1 ^ (h1 >> 13)) * 3266489909)
	return itoa36(h2) + itoa36(h1)
}

func itoa36(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		d := v % 36
		if d < 10 {
			buf[i] = byte('0' + d)
		} else {
			buf[i] = byte('a' + d - 10)
		}
		v /= 36
	}
	return string(buf[i:])
}
