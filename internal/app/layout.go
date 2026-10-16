package app

import (
	"fmt"
	"strings"

	"argowt/internal/ui"
)

func (model Model) viewWidth() int {
	if model.width <= 0 {
		return 100
	}
	return model.width
}

// frame stacks the top lines, the body and the footer. The body gets the rows
// left over, so the footer sits on the last screen row; with an unknown
// height, rows is 0 and the body takes its natural height.
func (model Model) frame(top, footer string, body func(rows int) string) string {
	footer = model.footer(footer)
	return top + "\n" + body(model.bodyRows(top, footer)) + "\n" + footer
}

// footer returns the command palette in place of the footer while it is open.
func (model Model) footer(footer string) string {
	if model.palette {
		return model.paletteView(model.viewWidth())
	}
	return footer
}

func (model Model) bodyRows(top, footer string) int {
	if model.height <= 0 {
		return 0
	}
	return max(3, model.height-lineCount(top)-lineCount(footer))
}

func lineCount(text string) int {
	return strings.Count(text, "\n") + 1
}

// panelInner is the text width inside a panel of the given outer width.
func panelInner(width int) int {
	return max(1, width-4)
}

// styleRow colors a list row by status, or highlights it across the panel
// when selected.
func styleRow(line string, width int, selected bool, status string) string {
	line = ui.Truncate(line, width)
	if selected {
		return theme.Row(ui.Pad(line, width), true)
	}
	return theme.Status(line, status)
}

// window returns the slice of length items that keeps cursor in view.
func window(length, cursor, rows int) (int, int) {
	if rows <= 0 {
		return 0, length
	}
	start := 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	return start, min(length, start+rows)
}

// label renders a muted name with a highlighted value, e.g. "ns argo".
func label(name, value string) string {
	return theme.Muted.Render(name) + " " + theme.Info.Render(ui.Sanitize(value))
}

// plural counts things, e.g. plural(1, "item") is "1 item".
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// field renders an aligned "name  value" row; value is already sanitized.
func field(name, value string) string {
	return theme.Muted.Render(fmt.Sprintf("%-12s", name)) + " " + value
}
