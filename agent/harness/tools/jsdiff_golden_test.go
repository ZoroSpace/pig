// Golden outputs captured from npm diff@8.0.4 (the version pinned by pi's
// packages/agent/package.json), run via node against the same inputs. Locks
// the jsdiff.go port byte-for-byte, including hunk headers, the
// "no newline at end of file" markers, and the 0-count start adjustment.
package tools

import "testing"

var createTwoFilesPatchGolden = []struct {
	name    string
	oldStr  string
	newStr  string
	context int
	want    string
}{
	{
		"disjoint single-line edits",
		"alpha\nbeta\ngamma\ndelta\n", "ALPHA\nbeta\nGAMMA\ndelta\n", 4,
		"--- f\n+++ f\n@@ -1,4 +1,4 @@\n-alpha\n+ALPHA\n beta\n-gamma\n+GAMMA\n delta\n",
	},
	{
		"pure addition at end",
		"one\n", "one\ntwo\n", 4,
		"--- f\n+++ f\n@@ -1,1 +1,2 @@\n one\n+two\n",
	},
	{
		"two hunks with context 2",
		"a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n", "a\nb\nc\nX\ne\nf\ng\nh\ni\nY\nk\nl\n", 2,
		"--- f\n+++ f\n@@ -2,5 +2,5 @@\n b\n c\n-d\n+X\n e\n f\n@@ -8,5 +8,5 @@\n h\n i\n-j\n+Y\n k\n l\n",
	},
	{
		"addition into empty file",
		"", "x\n", 4,
		"--- f\n+++ f\n@@ -0,0 +1,1 @@\n+x\n",
	},
	{
		"removal to empty file",
		"x\n", "", 4,
		"--- f\n+++ f\n@@ -1,1 +0,0 @@\n-x\n",
	},
	{
		"mid-file replacement without trailing newline",
		"a\nb\nc", "a\nd\nc", 4,
		"--- f\n+++ f\n@@ -1,3 +1,3 @@\n a\n-b\n+d\n c\n\\ No newline at end of file\n",
	},
	{
		"appended line after identical prefix",
		"same\n", "same\nmore\n", 4,
		"--- f\n+++ f\n@@ -1,1 +1,2 @@\n same\n+more\n",
	},
	{
		"single line change in ten-line file",
		"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n10\n", 4,
		"--- f\n+++ f\n@@ -1,9 +1,9 @@\n 1\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8\n 9\n",
	},
	{
		"full replacement",
		"a\n", "b\n", 4,
		"--- f\n+++ f\n@@ -1,1 +1,1 @@\n-a\n+b\n",
	},
}

func TestCreateTwoFilesPatchGolden(t *testing.T) {
	for _, tc := range createTwoFilesPatchGolden {
		t.Run(tc.name, func(t *testing.T) {
			got := CreateTwoFilesPatch("f", "f", tc.oldStr, tc.newStr, tc.context)
			if got != tc.want {
				t.Fatalf("patch mismatch:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

var diffLinesGolden = []struct {
	name string
	old  string
	new  string
	want []LineDiffChange
}{
	{
		"interleaved change",
		"a\nb\n", "a\nc\nb\n",
		[]LineDiffChange{
			{Count: 1, Value: "a\n"},
			{Count: 1, Added: true, Value: "c\n"},
			{Count: 1, Value: "b\n"},
		},
	},
	{
		"empty old file",
		"", "x\n",
		[]LineDiffChange{{Count: 1, Added: true, Value: "x\n"}},
	},
	{
		"crlf vs lf are different lines",
		"a\r\nb\n", "a\nb\n",
		[]LineDiffChange{
			{Count: 1, Removed: true, Value: "a\r\n"},
			{Count: 1, Added: true, Value: "a\n"},
			{Count: 1, Value: "b\n"},
		},
	},
}

func TestDiffLinesGolden(t *testing.T) {
	for _, tc := range diffLinesGolden {
		t.Run(tc.name, func(t *testing.T) {
			got := diffLines(tc.old, tc.new)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d parts, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("part %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
