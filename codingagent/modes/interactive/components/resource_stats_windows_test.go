//go:build windows

// resource_stats_windows_test.go — unit tests for the Windows sampler's
// pure conversion logic (WINDOWS-SUPPORT). The syscall-backed readMemBytes
// / readProcessCPUMicros need a live process on Windows; the FILETIME →
// microseconds conversion is factored out (filetimesToMicros) and locked
// here.
package components

import (
	"testing"

	"golang.org/x/sys/windows"
)

// filetimeFrom100ns builds a Filetime from raw 100ns units (FILETIMEs are
// 100ns intervals since 1601-01-01; CPU times are durations in the same
// units).
func filetimeFrom100ns(units uint64) windows.Filetime {
	return windows.Filetime{
		HighDateTime: uint32(units >> 32),
		LowDateTime:  uint32(units),
	}
}

func TestFiletimesToMicros(t *testing.T) {
	tests := []struct {
		name   string
		kernel uint64 // 100ns units
		user   uint64 // 100ns units
		want   int64  // microseconds
	}{
		{name: "zero", kernel: 0, user: 0, want: 0},
		{name: "one second each", kernel: 10_000_000, user: 10_000_000, want: 2_000_000},
		{name: "sub-microsecond truncates", kernel: 5, user: 4, want: 0}, // 0.9µs
		{name: "large values (hours of CPU)", kernel: 360_000_000_000, user: 360_000_000_000, want: 720_000_000_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filetimesToMicros(filetimeFrom100ns(tt.kernel), filetimeFrom100ns(tt.user))
			if got != tt.want {
				t.Fatalf("filetimesToMicros(%d, %d) = %d µs, want %d", tt.kernel, tt.user, got, tt.want)
			}
		})
	}
}
