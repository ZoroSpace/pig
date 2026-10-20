package utils

import (
	"reflect"
	"testing"
)

// #9490: discover PowerShell outside PATH without assuming a drive or Windows
// directory name (pi: test/powershell-config.test.ts).
func TestGetPowerShellConfig(t *testing.T) {
	const (
		pwsh7             = `D:\Applications\PowerShell\7\pwsh.exe`
		windowsPowerShell = `E:\WinNT\System32\WindowsPowerShell\v1.0\powershell.exe`
	)

	env := map[string]string{
		"ProgramFiles": `D:\Applications`,
		"SystemRoot":   `E:\WinNT`,
	}
	newLookup := func(onPath map[string]string, existing map[string]bool) shellLookup {
		return shellLookup{
			goos:       "windows",
			getenv:     func(key string) string { return env[key] },
			pathExists: func(path string) bool { return existing[path] },
			findOnPath: func(exe string) string { return onPath[exe] },
		}
	}
	want := ShellConfig{Shell: "", Args: append([]string{}, PowerShellArgs...)}

	t.Run("prefers PowerShell 7 on PATH over known installation locations", func(t *testing.T) {
		shell := `E:\Tools\pwsh.exe`
		lookup := newLookup(map[string]string{"pwsh.exe": shell}, map[string]bool{pwsh7: true, windowsPowerShell: true})
		config, err := getPowerShellConfig(lookup)
		if err != nil {
			t.Fatal(err)
		}
		want.Shell = shell
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("finds PowerShell 7 under ProgramFiles before Windows PowerShell on PATH", func(t *testing.T) {
		lookup := newLookup(
			map[string]string{"powershell.exe": windowsPowerShell},
			map[string]bool{pwsh7: true, windowsPowerShell: true},
		)
		config, err := getPowerShellConfig(lookup)
		if err != nil {
			t.Fatal(err)
		}
		want.Shell = pwsh7
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("prefers Windows PowerShell on PATH over its system installation", func(t *testing.T) {
		shell := `E:\Tools\powershell.exe`
		lookup := newLookup(
			map[string]string{"powershell.exe": shell},
			map[string]bool{shell: true, windowsPowerShell: true},
		)
		config, err := getPowerShellConfig(lookup)
		if err != nil {
			t.Fatal(err)
		}
		want.Shell = shell
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("finds Windows PowerShell under SystemRoot when PATH lookup is unavailable", func(t *testing.T) {
		lookup := newLookup(nil, map[string]bool{windowsPowerShell: true})
		config, err := getPowerShellConfig(lookup)
		if err != nil {
			t.Fatal(err)
		}
		want.Shell = windowsPowerShell
		if !reflect.DeepEqual(config, want) {
			t.Fatalf("config = %+v, want %+v", config, want)
		}
	})

	t.Run("reports an error when no PowerShell installation exists", func(t *testing.T) {
		if _, err := getPowerShellConfig(newLookup(nil, nil)); err == nil ||
			err.Error() != "No PowerShell executable found. Install PowerShell or add powershell.exe/pwsh.exe to PATH." {
			t.Fatalf("expected no-PowerShell error, got %v", err)
		}
	})
}
