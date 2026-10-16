package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Panel draws lines inside a rounded border, with the title and hint set in
// the top edge. height is the outer height, borders included; 0 fits the lines.
//
//	╭─ Workflows ─────── 2 items ─╮
//	│ body                        │
//	╰─────────────────────────────╯
func (theme Theme) Panel(title, hint string, lines []string, width, height int, focused bool) string {
	border := theme.Border
	if focused {
		border = theme.BorderFocused
	}
	inner := max(1, width-4)
	rows := len(lines)
	if height > 0 {
		rows = max(0, height-2)
	}

	var output strings.Builder
	output.WriteString(border.Render("╭─") + edge(" "+title+" ", " "+hint+" ", inner, border) + border.Render("─╮") + "\n")
	for index := range rows {
		line := ""
		if index < len(lines) {
			line = lines[index]
		}
		output.WriteString(border.Render("│") + " " + Pad(TruncateStyled(line, inner), inner) + " " + border.Render("│") + "\n")
	}
	output.WriteString(border.Render("╰" + strings.Repeat("─", inner+2) + "╯"))
	return output.String()
}

// edge fills the top border between the title and the hint.
func edge(title, hint string, width int, border lipgloss.Style) string {
	if strings.TrimSpace(hint) == "" {
		hint = ""
	}
	if ansi.StringWidth(title)+ansi.StringWidth(hint) > width {
		hint = ""
	}
	title = TruncateStyled(title, width)
	fill := width - ansi.StringWidth(title) - ansi.StringWidth(hint)
	return title + border.Render(strings.Repeat("─", max(0, fill))) + hint
}

// Header is the top bar: the logo and items, with status at the right edge.
func (theme Theme) Header(width int, status string, items ...string) string {
	left := theme.Logo.Render(" argowt ") + "  " + strings.Join(items, theme.Muted.Render("  │  "))
	return Spread(width, left, status)
}

// Tabs renders labels as tabs with the active one highlighted.
func (theme Theme) Tabs(labels []string, active int) string {
	parts := make([]string, len(labels))
	for index, label := range labels {
		if index == active {
			parts[index] = theme.TabActive.Render(" " + label + " ")
		} else {
			parts[index] = theme.Tab.Render(" " + label + " ")
		}
	}
	return strings.Join(parts, " ")
}

// Keys renders shortcut hints on one line, e.g.
// Keys(80, "j/k", "navigate", "q", "quit") draws "j/k navigate   q quit".
// Hints that do not fit are left out.
func (theme Theme) Keys(width int, pairs ...string) string {
	line, _, _ := strings.Cut(theme.KeyLines(width, pairs...), "\n")
	return line
}

// KeyLines renders shortcut hints, wrapping them onto as many lines as needed.
func (theme Theme) KeyLines(width int, pairs ...string) string {
	var lines []string
	line := ""
	for index := 0; index+1 < len(pairs); index += 2 {
		hint := theme.Key.Render(pairs[index]) + " " + theme.Muted.Render(pairs[index+1])
		if line != "" && ansi.StringWidth(line+"   "+hint)+1 > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += "   "
		}
		line += hint
	}
	lines = append(lines, line)
	for index := range lines {
		lines[index] = TruncateStyled(" "+lines[index], width)
	}
	return strings.Join(lines, "\n")
}

// Spread places left and right at the edges of width; left is cut to fit.
func Spread(width int, left, right string) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return TruncateStyled(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// Pad fills styled text with spaces up to width cells.
func Pad(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
}
