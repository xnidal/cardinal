package tools

import "testing"

func TestFormatDiff(t *testing.T) {
	tests := []struct {
		name        string
		old, new    string
		mustContain []string
		mustNot     []string
		wantExact   string
	}{
		{
			name:      "no changes returns No changes",
			old:       "hello\nworld\n",
			new:       "hello\nworld\n",
			wantExact: "No changes",
		},
		{
			name:        "single-line replacement",
			old:         "alpha\nbeta\ngamma\n",
			new:         "alpha\nBETA\ngamma\n",
			mustContain: []string{" alpha", "-beta", "+BETA", " gamma"},
		},
		{
			name:        "line markers prefix every row",
			old:         "l1\nl2\nl3\nl4\n",
			new:         "l1\nl2\nCHANGED\nl4\n",
			mustContain: []string{" l1", " l2", "-l3", "+CHANGED", " l4"},
		},
		{
			name:        "append a single line shows only + marker",
			old:         "l1\nl2\n",
			new:         "l1\nl2\nNEW\n",
			mustContain: []string{" l2", "+NEW"},
			mustNot:     []string{"\n-"},
		},
		{
			name:        "delete a single line shows only - marker",
			old:         "l1\nl2\nl3\n",
			new:         "l1\nl3\n",
			mustContain: []string{"-l2"},
			mustNot:     []string{"\n+"},
		},
		{
			name:        "long unchanged tail collapses instead of repeating",
			old:         "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n",
			new:         "a\nb\nc\nd\ne\nf\nCHANGED\nh\ni\nj\nk\nl\nm\nn\n",
			mustContain: []string{" ... "},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := formatDiff(tc.old, tc.new)
			if tc.wantExact != "" {
				if out != tc.wantExact {
					t.Fatalf("formatDiff() =\n%q\nwant %q", out, tc.wantExact)
				}
				return
			}
			for _, s := range tc.mustContain {
				if !contains(out, s) {
					t.Errorf("formatDiff() output missing %q\nfull output:\n%s", s, out)
				}
			}
			for _, s := range tc.mustNot {
				if contains(out, s) {
					t.Errorf("formatDiff() output unexpectedly contains %q\nfull output:\n%s", s, out)
				}
			}
		})
	}
}

func TestFormatDiffMarkerAtColumnZero(t *testing.T) {
	// The renderer in tui_view.go colours rows by examining byte zero.
	// Each formatDiff row must therefore start with the marker byte.
	out := formatDiff("hello\n", "world\n")
	if out == "" || out == "No changes" {
		t.Fatalf("formatDiff() returned empty/no-change for changed input: %q", out)
	}
	for i, line := range splitTestLines(out) {
		if line == "" {
			continue
		}
		switch line[0] {
		case ' ', '+', '-':
			// ok
		default:
			t.Errorf("row %d does not start with marker character: %q", i, line)
		}
	}
}

func splitTestLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
