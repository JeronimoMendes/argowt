package ui

import "testing"

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	got := Sanitize("safe\x1b[31mred\nnext")
	if got != "safe�[31mred�next" {
		t.Fatalf("Sanitize() = %q", got)
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
