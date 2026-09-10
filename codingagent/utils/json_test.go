// pig-side coverage for the stripJsonComments port (pi: utils/json.ts has no
// dedicated test).
package utils

import (
	"encoding/json"
	"testing"
)

func TestStripJsonComments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "line comments removed, strings kept",
			in:   "{\n  // a comment\n  \"url\": \"http://x//y\",\n  \"s\": \"// not a comment\"\n}",
			want: "{\n  \n  \"url\": \"http://x//y\",\n  \"s\": \"// not a comment\"\n}",
		},
		{
			name: "trailing commas removed",
			in:   `{"a": [1, 2,], "b": {"c": 1,},}`,
			want: `{"a": [1, 2], "b": {"c": 1}}`,
		},
		{
			name: "escaped quotes in strings kept",
			in:   `{"a": "x,\"y",}`,
			want: `{"a": "x,\"y"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripJsonComments(tc.in)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if err := json.Unmarshal([]byte(got), &map[string]any{}); err != nil {
				t.Fatalf("stripped output is not valid JSON: %v", err)
			}
		})
	}
}
