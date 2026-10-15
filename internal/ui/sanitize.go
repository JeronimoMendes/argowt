// Package ui contains reusable terminal rendering helpers.
package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Sanitize replaces terminal control characters so API-provided values cannot
// inject escape sequences into the terminal.
func Sanitize(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for _, character := range value {
		if unicode.IsControl(character) {
			output.WriteRune('�')
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

// TruncateStyled truncates text by terminal cell width while preserving ANSI
// styling sequences.
func TruncateStyled(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}

func Truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
