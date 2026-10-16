package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Reverse video marks a selection, so it reads in every theme.
var (
	selectedOn  = ansi.Style{}.Reverse().String()
	selectedOff = ansi.Style{}.NoReverse().String()
)

// Point is a screen cell: X column, Y row, both zero-based.
type Point struct {
	X int
	Y int
}

// Selection is a terminal-style stream selection between two cells,
// inclusive. Rows between the ends are selected from edge to edge.
//
//	anchor ─▶ ▓▓▓▓▓▓▓
//	        ▓▓▓▓▓▓▓▓▓▓
//	        ▓▓▓▓ ◀─ head
type Selection struct {
	Anchor Point
	Head   Point
}

// ordered returns the ends in reading order.
func (selection Selection) ordered() (Point, Point) {
	start, end := selection.Anchor, selection.Head
	if end.Y < start.Y || (end.Y == start.Y && end.X < start.X) {
		start, end = end, start
	}
	return start, end
}

// Highlight renders the selection in reverse video over a view.
// height is the screen height; a taller view shows its last rows, as the
// Bubble Tea renderer does.
func Highlight(view string, selection Selection, height int) string {
	lines := strings.Split(view, "\n")
	eachSelected(lines, selection, height, func(index, left, right int) {
		if left >= right {
			return
		}

		line := lines[index]
		selected := ansi.Strip(ansi.Cut(line, left, right))
		lines[index] = ansi.Cut(line, 0, left) + selectedOn + selected + selectedOff + ansi.Cut(line, right, ansi.StringWidth(line))
	})
	return strings.Join(lines, "\n")
}

// SelectedText returns the plain text under the selection.
// e.g. a drag over "argo  daily-payment" on one row returns "daily-payment".
func SelectedText(view string, selection Selection, height int) string {
	lines := strings.Split(ansi.Strip(view), "\n")
	selected := make([]string, 0)
	eachSelected(lines, selection, height, func(index, left, right int) {
		selected = append(selected, trimBorder(ansi.Cut(lines[index], left, right)))
	})
	return strings.Join(selected, "\n")
}

// trimBorder drops panel borders, so a copy is only the text inside.
// e.g. "│ argo  daily  │" returns "argo  daily".
func trimBorder(line string) string {
	line = strings.TrimRight(line, " ")
	line = strings.TrimRight(strings.TrimSuffix(line, "│"), " ")
	return strings.TrimPrefix(line, "│ ")
}

// eachSelected visits the view lines under the selection with their selected
// cells [left, right). Only selected rows are measured.
func eachSelected(lines []string, selection Selection, height int, visit func(index, left, right int)) {
	offset := screenOffset(len(lines), height)
	start, end := selection.ordered()
	for row := max(start.Y, 0); row <= end.Y && row+offset < len(lines); row++ {
		index := row + offset
		width := ansi.StringWidth(lines[index])

		left, right := 0, width
		if row == start.Y {
			left = min(start.X, width)
		}
		if row == end.Y {
			right = min(width, end.X+1)
		}
		visit(index, left, max(left, right))
	}
}

// screenOffset returns the first view line shown on screen.
func screenOffset(lines, height int) int {
	if height <= 0 || lines <= height {
		return 0
	}
	return lines - height
}
