package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestThemeKeepsTextualStatusAndSelection(t *testing.T) {
	theme := DefaultTheme()
	for _, test := range []struct {
		text   string
		status string
	}{
		{"▶ Running", "Running"},
		{"✓ Succeeded", "Succeeded"},
		{"✗ Failed", "Failed"},
		{"Ⅱ Suspended", "Suspended"},
	} {
		rendered := ansi.Strip(theme.Status(test.text, test.status))
		if rendered != test.text {
			t.Fatalf("Status(%q) stripped = %q", test.status, rendered)
		}
	}
	if rendered := ansi.Strip(theme.Row("> selected", true)); !strings.Contains(rendered, "selected") {
		t.Fatalf("selected row = %q", rendered)
	}
}

func TestKeysFitTheWidth(t *testing.T) {
	theme := DefaultTheme()
	pairs := []string{"j/k", "move", "Enter", "open", "q", "quit"}
	if got := ansi.Strip(theme.Keys(20, pairs...)); got != " j/k move" {
		t.Fatalf("Keys() = %q", got)
	}
	if got := ansi.Strip(theme.KeyLines(20, pairs...)); got != " j/k move\n Enter open   q quit" {
		t.Fatalf("KeyLines() = %q", got)
	}
}
