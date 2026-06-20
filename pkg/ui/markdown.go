// Package ui provides shared terminal rendering helpers for Cardinal's TUI.
//
// The Markdown renderer is a thin wrapper around glamour that:
//   - picks a style by detected terminal profile (light/dark/no-tty),
//   - re-builds on width changes so word-wrapping tracks the viewport,
//   - tolerates broken/partial input during streaming (we fall back to the
//     raw text if glamour chokes on malformed markdown).
package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	styles "github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

// MarkdownRenderer caches a glamour renderer keyed by (width, profile).
// Re-rendering is cheap; rebuilding glamour on every frame is not.
type MarkdownRenderer struct {
	mu      sync.Mutex
	width   int
	profile termenv.Profile
	r       *glamour.TermRenderer
}

// NewMarkdownRenderer builds a renderer with sensible defaults for an
// interactive terminal. width <= 0 falls back to 80.
func NewMarkdownRenderer(width int) *MarkdownRenderer {
	m := &MarkdownRenderer{}
	m.rebuild(width)
	return m
}

// Resize rebuilds the renderer with a new word-wrap width. The caller should
// invoke this when the terminal/viewport width changes.
func (m *MarkdownRenderer) Resize(width int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if width == m.width {
		return
	}
	m.rebuild(width)
}

// tableBoxBorder hard-codes classic Unicode box-drawing characters for
// inner-table separators. Glamour's built-in styles (dark, light, dracula)
// intentionally leave RowSeparator/ColumnSeparator empty, which causes
// tables to render without visible inter-cell separators and look like a
// single ragged column. By providing these explicitly, every cell gets a
// visible left/right border and each row gets a horizontal rule beneath it,
// producing the "cell-in-a-box" look the TUI needs to be legible.
func tableBoxBorder() (center, column, row string) {
	return "┼", "│", "─"
}

// withTableBoxes copies a built-in glamour style and overrides only the
// Table block so the rest of the style (colours, headings, code blocks)
// stays intact while tables get a proper boxed layout.
func withTableBoxes(base *glamouransi.StyleConfig) *glamouransi.StyleConfig {
	if base == nil {
		return nil
	}
	cp := *base
	center, column, row := tableBoxBorder()
	cp.Table = glamouransi.StyleTable{
		StyleBlock: glamouransi.StyleBlock{
			StylePrimitive: glamouransi.StylePrimitive{},
		},
		CenterSeparator: &center,
		ColumnSeparator: &column,
		RowSeparator:    &row,
	}
	return &cp
}

func (m *MarkdownRenderer) rebuild(width int) {
	if width <= 0 {
		width = 80
	}
	profile := termenv.ColorProfile()
	m.width = width
	m.profile = profile
	// Build a renderer explicitly so we can inject the boxed-table style
	// instead of relying on WithStandardStyle (which would discard the
	// custom Table block).
	config := styleConfigFor(profile)
	if customized := withTableBoxes(config); customized != nil {
		config = customized
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(*config),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
	)
	if err != nil || r == nil {
		// Best-effort: keep the previous renderer if construction failed,
		// since glamour only really fails on bad custom styles. WithStandardStyle
		// shouldn't error in practice.
		return
	}
	m.r = r
}

// styleConfigFor resolves the glamour style config for the terminal palette.
// We prefer the "notty" profile for background pipes (logs/CI); inside an
// actual TTY we use dark/light depending on the background color.
func styleConfigFor(profile termenv.Profile) *glamouransi.StyleConfig {
	switch profile {
	case termenv.Ascii:
		if c, ok := styles.DefaultStyles[styles.NoTTYStyle]; ok {
			return c
		}
		return nil
	default:
		// Match the TUI's actual contrast: dark style on dark backgrounds,
		// light style on light backgrounds. We resolve explicitly because
		// termenv's "auto" detection inside glamour can be unreliable in
		// Bubble Tea hosts.
		if termenv.HasDarkBackground() {
			if c, ok := styles.DefaultStyles[styles.DarkStyle]; ok {
				return c
			}
		} else {
			if c, ok := styles.DefaultStyles[styles.LightStyle]; ok {
				return c
			}
		}
		// Fall back to whatever glamour picks if none of the above apply.
		return nil
	}
}

// Render formats markdown with the currently configured width. If glamour
// returns an error (typically only on partial/garbled streaming input) we
// fall back to the raw input so the chat stays legible. We then strip the
// glamour document margin (the blank first line it always emits) so the
// carded message layout in tui_view.go stays tight.
func (m *MarkdownRenderer) Render(input string) string {
	m.mu.Lock()
	r := m.r
	m.mu.Unlock()

	if r == nil {
		return input
	}
	out, err := r.Render(input)
	if err != nil || strings.TrimSpace(out) == "" {
		return input
	}
	return strings.TrimLeft(out, "\n")
}
