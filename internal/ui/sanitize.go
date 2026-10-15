// Package ui contains reusable terminal rendering helpers.
package ui

import (
	"strings"
	"unicode"
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
