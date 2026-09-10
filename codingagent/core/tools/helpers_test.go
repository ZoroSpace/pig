// Test-only helpers for the coding-agent tools tests (pi: the vitest
// imports and small helpers at the top of tools.test.ts).
package tools

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"time"

	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// utilsGetShellConfig exposes the utils shell resolution to the tests (pi:
// the shellModule.getShellConfig spy target).
func utilsGetShellConfig(cwd string, shellPath string) (codingagentutils.ShellConfig, error) {
	return codingagentutils.GetShellConfig(cwd, shellPath)
}

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func timeMilli() time.Duration { return time.Millisecond }

func timeSleep(d time.Duration) { time.Sleep(d) }

var (
	_ = reflect.DeepEqual
	_ = regexp.MustCompile
	_ = strconv.Itoa
	_ = fmt.Sprintf
	_ = rand.Read
	_ = os.Getpid
)
