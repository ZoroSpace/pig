// pi: src/utils/zip.ts — small classic ZIP archives used by bug reports
// (pi v0.86.0). pi hand-rolls the container (zlib deflateRaw + crc32); the Go
// port keeps the byte layout identical with compress/flate + hash/crc32.
// Declared deviation: zlib and Go flate produce different (both valid)
// deflate streams for the same input, so compressed bytes are not byte-identical.
package utils

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"os"
	"time"
)

// ZipEntry is one archived file (pi: ZipEntry). pi's Uint8Array branch has no
// consumer (all bug-report files are strings) — Data is a string.
type ZipEntry struct {
	Name string
	Data string
}

// dosDateTime packs a date into the DOS time/day fields (pi: dosDateTime).
func dosDateTime(date time.Time) (time int16, day int16) {
	t := uint32(date.Hour())<<11 | uint32(date.Minute())<<5 | uint32(date.Second())>>1
	d := uint32(maxInt32(1980, date.Year())-1980)<<9 | uint32(date.Month())<<5 | uint32(date.Day())
	return int16(t), int16(d)
}

func maxInt32(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// deflateRaw compresses with raw deflate at the default level (pi:
// zlib.deflateRawSync).
func deflateRaw(data []byte) []byte {
	var buf bytes.Buffer
	writer, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	_, _ = writer.Write(data)
	_ = writer.Close()
	return buf.Bytes()
}

// createZipArchive builds the classic ZIP container (pi: createZipArchive).
func createZipArchive(entries []ZipEntry) []byte {
	var files []byte
	var directory []byte
	t, d := dosDateTime(time.Now())
	var offset uint32

	for _, entry := range entries {
		name := []byte(entry.Name)
		data := []byte(entry.Data)
		compressed := deflateRaw(data)
		checksum := crc32.ChecksumIEEE(data)

		local := make([]byte, 30)
		binary.LittleEndian.PutUint32(local[0:], 0x04034b50)
		binary.LittleEndian.PutUint16(local[4:], 20)
		binary.LittleEndian.PutUint16(local[6:], 0x0800)
		binary.LittleEndian.PutUint16(local[8:], 8)
		binary.LittleEndian.PutUint16(local[10:], uint16(t))
		binary.LittleEndian.PutUint16(local[12:], uint16(d))
		binary.LittleEndian.PutUint32(local[14:], checksum)
		binary.LittleEndian.PutUint32(local[18:], uint32(len(compressed)))
		binary.LittleEndian.PutUint32(local[22:], uint32(len(data)))
		binary.LittleEndian.PutUint16(local[26:], uint16(len(name)))

		central := make([]byte, 46)
		binary.LittleEndian.PutUint32(central[0:], 0x02014b50)
		binary.LittleEndian.PutUint16(central[4:], 20)
		binary.LittleEndian.PutUint16(central[6:], 20)
		binary.LittleEndian.PutUint16(central[8:], 0x0800)
		binary.LittleEndian.PutUint16(central[10:], 8)
		binary.LittleEndian.PutUint16(central[12:], uint16(t))
		binary.LittleEndian.PutUint16(central[14:], uint16(d))
		binary.LittleEndian.PutUint32(central[16:], checksum)
		binary.LittleEndian.PutUint32(central[20:], uint32(len(compressed)))
		binary.LittleEndian.PutUint32(central[24:], uint32(len(data)))
		binary.LittleEndian.PutUint16(central[28:], uint16(len(name)))
		binary.LittleEndian.PutUint32(central[42:], offset)

		files = append(files, local...)
		files = append(files, name...)
		files = append(files, compressed...)
		directory = append(directory, central...)
		directory = append(directory, name...)
		offset += uint32(30 + len(name) + len(compressed))
	}

	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(entries)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(entries)))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(directory)))
	binary.LittleEndian.PutUint32(end[16:], offset)
	out := append(files, directory...)
	return append(out, end...)
}

// WriteZipArchive creates the archive at filePath (pi: writeZipArchive —
// async fs write; pig is synchronous).
func WriteZipArchive(filePath string, entries []ZipEntry) error {
	return os.WriteFile(filePath, createZipArchive(entries), 0o644)
}
