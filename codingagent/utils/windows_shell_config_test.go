package utils

import (
	"reflect"
	"testing"
)

// #9490: identify legacy WSL launchers under the actual Windows directory (pi:
// test/windows-shell-config.test.ts).
func TestLegacyWSLBashDetection(t *testing.T) {
	cases := []struct {
		name       string
		systemRoot string
		shell      string
		stdin      bool
	}{
		{"trailing slash SystemRoot", `D:\WinNT\`, "d:/WINNT/System32/BASH.EXE", true},
		{"Sysnative", `D:\WinNT`, `D:\WinNT\Sysnative\bash.exe`, true},
		{"unset SystemRoot falls back to conventional path", "", `E:\Windows\System32\bash.exe`, true},
		{"Git Bash", `D:\WinNT`, `D:\Git\bin\bash.exe`, false},
		{"other Windows directory", `D:\WinNT`, `E:\Windows\System32\bash.exe`, false},
		{"relative SystemRoot", "WinNT", `C:\Windows\System32\bash.exe`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SystemRoot", tc.systemRoot)
			want := ShellConfig{Shell: tc.shell, Args: []string{"-c"}}
			if tc.stdin {
				want.Args = []string{"-s"}
				want.CommandTransport = "stdin"
			}
			if got := getBashShellConfig(tc.shell); !reflect.DeepEqual(got, want) {
				t.Fatalf("config = %+v, want %+v", got, want)
			}
		})
	}
}
