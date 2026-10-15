package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"argowt/internal/ui"
)

// View renders the screen with the mouse selection on top.
func (model Model) View() string {
	view := model.screen()
	if !model.selectionVisible {
		return view
	}
	return ui.Highlight(view, model.selection, model.height)
}

// updateSelection tracks a left-button drag and copies the text on release.
// It reports whether the event is consumed; a plain click still falls
// through, so it keeps selecting rows.
func (model Model) updateSelection(mouse tea.MouseMsg) (Model, tea.Cmd, bool) {
	point := ui.Point{X: mouse.X, Y: mouse.Y}
	switch {
	case mouse.Action == tea.MouseActionPress && mouse.Button == tea.MouseButtonLeft:
		model.selection = ui.Selection{Anchor: point, Head: point}
		model.selecting = true
		model.selectionVisible = false
		return model, nil, false
	case mouse.Action == tea.MouseActionMotion && model.selecting:
		if model.selectionVisible && point == model.selection.Head {
			return model, nil, true
		}
		model.selection.Head = point
		model.selectionVisible = true
		return model, nil, true
	case mouse.Action == tea.MouseActionRelease && model.selecting:
		model.selecting = false
		if !model.selectionVisible {
			return model, nil, true
		}
		return model, model.copySelection(), true
	case tea.MouseEvent(mouse).IsWheel():
		// Scrolling moves the content under the highlight.
		model.selectionVisible = false
	}
	return model, nil, false
}

func (model Model) copySelection() tea.Cmd {
	text := ui.SelectedText(model.screen(), model.selection, model.height)
	if model.clipboard == nil || text == "" {
		return nil
	}

	copyText := model.clipboard
	// Copy silently: the highlight is the only feedback.
	return func() tea.Msg {
		_ = copyText(text)
		return nil
	}
}
