package ui

import (
	"strings"
	"testing"
)

func TestMarkdownRenderer_RenderDoesNotPanic(t *testing.T) {
	r := NewMarkdownRenderer(80)
	cases := []string{
		"",
		"hello world",
		"# Heading\n## Sub\n\nbody",
		"**bold** and _italic_",
		"- one\n- two\n- three",
		"```go\nfunc f() {}\n```",
		"```unterminated", // partial streaming
		"a]b[c[d",          // unmatched brackets (goldmark used to crash here)
		strings.Repeat("x ", 200),
	}
	for _, in := range cases {
		// Just exercising the renderer must not panic and must return
		// something walkable (i.e. a string).
		_ = r.Render(in)
	}
}

func TestMarkdownRenderer_ResizeIsIdempotent(t *testing.T) {
	r := NewMarkdownRenderer(60)
	r.Resize(60) // same width -> should not panic
	r.Resize(40) // different width -> should rebuild
	r.Resize(40)
	if r.width != 40 {
		t.Fatalf("expected width=40 after resize, got %d", r.width)
	}
}

func TestMarkdownRenderer_Strikethrough(t *testing.T) {
	r := NewMarkdownRenderer(80)
	got := r.Render("this is ~~done~~ task")
	// Strikethrough is rendered as ANSI CSI code 9 (CrossedOut).
	// Both DarkStyle and LightStyle have CrossedOut:true; NoTTYStyle
	// renders ~~ literally. We succeed if either form is present.
	if !strings.Contains(got, ";9m") && !strings.Contains(got, "~~done~~") {
		t.Fatalf("expected strikethrough escape (\";9m\") or literal ~~ in output, got %q", got)
	}
}

func TestMarkdownRenderer_PreservesContentWords(t *testing.T) {
	r := NewMarkdownRenderer(80)
	// glamour never drops words entirely; even if the style is notty the
	// body content should still be present in the output.
	in := "alpha beta gamma delta"
	got := r.Render(in)
	for _, w := range []string{"alpha", "beta", "gamma", "delta"} {
		if !strings.Contains(got, w) {
			t.Fatalf("expected output to contain %q, got %q", w, got)
		}
	}
}
