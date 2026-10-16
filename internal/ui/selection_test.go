package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSelectionSpansRowsAndKeepsStyling(t *testing.T) {
	view := "\x1b[31mheader\x1b[0m\nargo  daily-payment\n\ncleanup job"

	// Dragged backwards from "clean" to "daily": the ends are reordered.
	selection := Selection{Anchor: Point{X: 4, Y: 3}, Head: Point{X: 6, Y: 1}}
	if got := SelectedText(view, selection, 0); got != "daily-payment\n\nclean" {
		t.Fatalf("SelectedText() = %q", got)
	}

	highlighted := Highlight(view, selection, 0)
	if !strings.HasPrefix(highlighted, "\x1b[31mheader") || !strings.Contains(highlighted, selectedOn+"daily-payment"+selectedOff) {
		t.Fatalf("Highlight() = %q", highlighted)
	}
	if ansi.Strip(highlighted) != ansi.Strip(view) {
		t.Fatalf("Highlight() changed text: %q", ansi.Strip(highlighted))
	}

	// A view taller than the screen shows its last rows, so row 0 is "argo…".
	if got := SelectedText(view, Selection{Head: Point{X: 3}}, 3); got != "argo" {
		t.Fatalf("SelectedText() on scrolled view = %q", got)
	}

	panel := "╭─ Logs ─────╮\n│   indented │\n│ last       │"
	if got := SelectedText(panel, Selection{Anchor: Point{Y: 1}, Head: Point{X: 13, Y: 2}}, 0); got != "  indented\nlast" {
		t.Fatalf("SelectedText() in panel = %q", got)
	}
}
