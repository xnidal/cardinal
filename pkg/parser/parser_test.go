package parser

import "testing"

func TestLooksLikeJSONToolArgs(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{`{"path":"pkg/api"}`, true},
		{`{"command":"go test ./..."}`, true},
		{`{"answer":"normal json"}`, false},
		{`Here is {"path":"x"}`, false},
		{`not json`, false},
	}
	for _, tc := range cases {
		if got := LooksLikeJSONToolArgs(tc.text); got != tc.want {
			t.Fatalf("LooksLikeJSONToolArgs(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
