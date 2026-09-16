//go:build windows

// resource_stats_windows.go — Windows samplers for the footer resource
// segment (pig-only). See resource_stats.go for the shared poller.
//
// Cgo-free: GetProcessTimes comes from x/sys/windows; GetProcessMemoryInfo
// is not exported by x/sys/windows (v0.29.0) and is declared against
// psapi.dll via NewLazySystemDLL (the standard x/sys pattern). FD/handle
// count is deliberately unavailable: GetProcessHandleCount reports Win32
// handles — a different currency from open file descriptors (a typical
// Windows process holds hundreds), so fdThreshold (200) would misfire
// constantly; the footer simply hides the FD item.
package components

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var psapi = windows.NewLazySystemDLL("psapi.dll")

var procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")

// processMemoryCounters mirrors win32 PROCESS_MEMORY_COUNTERS (SIZE_T
// fields are uintptr-wide; Cb is set to unsafe.Sizeof at the call site, so
// the layout tracks GOARCH).
type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// readMemBytes reads the memory charged to the process via
// GetProcessMemoryInfo → WorkingSetSize. Windows monitors (Task Manager)
// report working set as "Memory", so this matches the sampler contract —
// the same number the platform's own monitor shows (linux sampler: RSS
// from statm; darwin sampler: phys_footprint).
func readMemBytes() (int64, bool) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var pmc processMemoryCounters
	pmc.Cb = uint32(unsafe.Sizeof(pmc))
	r1, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&pmc)),
		uintptr(pmc.Cb),
	)
	if r1 == 0 || pmc.WorkingSetSize == 0 {
		return 0, false
	}
	return int64(pmc.WorkingSetSize), true
}

// readFDCount is unavailable on Windows (decision recorded in the file
// header): Win32 handles are not file descriptors and the shared
// fdThreshold would misreport. The footer hides the FD item when this
// reports unavailable.
func readFDCount() (int64, bool) {
	return 0, false
}

// filetimesToMicros converts a kernel+user FILETIME pair (100ns units) to
// cumulative CPU microseconds (factored out as a pure function so the
// unit conversion is testable — see resource_stats_windows_test.go).
func filetimesToMicros(kernel, user windows.Filetime) int64 {
	return (kernel.Nanoseconds() + user.Nanoseconds()) / 1000
}

// readProcessCPUMicros reads the process's cumulative CPU time (kernel +
// user) in microseconds via GetProcessTimes — the Getrusage counterpart
// (resource_stats_unix.go shares this contract via
// resourceReadings.cpuMicros).
func readProcessCPUMicros() (int64, bool) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	return filetimesToMicros(kernel, user), true
}
