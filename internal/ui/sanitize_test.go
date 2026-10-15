package ui

import "testing"

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	got := Sanitize("safe\x1b[31mred\nnext")
	if got != "safe�[31mred�next" {
		t.Fatalf("Sanitize() = %q", got)
	}
}

func TestTruncateStyledPreservesEscapeSequences(t *testing.T) {
	styled := "\x1b[31mabcdef\x1b[0m"
	got := TruncateStyled(styled, 4)
	if got == "" || got[len(got)-1] == '\x1b' {
		t.Fatalf("TruncateStyled() = %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("workflow", 5); got != "work…" {
		t.Fatalf("Truncate() = %q", got)
	}
	if got := Truncate("ok", 5); got != "ok" {
		t.Fatalf("Truncate() = %q", got)
	}
}
