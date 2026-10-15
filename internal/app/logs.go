package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	applogs "argowt/internal/logs"
	"argowt/internal/ui"
)

func (model Model) openSelectedNodeLogs() (tea.Model, tea.Cmd) {
	if model.logService == nil || model.execution == nil {
		return model, nil
	}
	node, found := model.execution.Nodes[model.executionSelectedID]
	if !found {
		return model, nil
	}
	if model.logCancel != nil {
		model.logCancel()
	}
	model.logGeneration++
	ctx, cancel := context.WithCancel(model.rootContext)
	model.logCancel = cancel
	model.logVisible = true
	model.logNode = node
	model.logPod = applogs.Pod{}
	model.logContainer = 0
	model.logPrevious = false
	model.logFollow = true
	model.logBuffer = applogs.NewBuffer(applogs.DefaultMaxLines, applogs.DefaultMaxBytes)
	model.logState = "discovering pod"
	model.logError = nil
	model.logQuery = ""
	model.logSearching = false
	model.logAutoScroll = true
	model.logOffset = 0
	model.logSaving = false
	model.logSavePath = ""
	model.logNotice = ""
	model.logReconnectAttempt = 0
	return model, discoverNodeLog(ctx, model.logService, model.logGeneration, model.execution.Namespace, model.execution.Name, node.ID, node.Name)
}

func (model Model) startLogStream(clear bool) (tea.Model, tea.Cmd) {
	if model.logService == nil || len(model.logPod.Containers) == 0 {
		return model, nil
	}
	if model.logCancel != nil {
		model.logCancel()
	}
	ctx, cancel := context.WithCancel(model.rootContext)
	model.logCancel = cancel
	if clear || model.logBuffer == nil {
		model.logBuffer = applogs.NewBuffer(applogs.DefaultMaxLines, applogs.DefaultMaxBytes)
		model.logReconnectAttempt = 0
	}
	model.logGeneration++
	container := model.logPod.Containers[model.logContainer]
	if model.logPrevious && !container.HasPrevious {
		model.logPrevious = false
	}
	model.logState = "connecting"
	model.logError = nil
	model.logNotice = ""
	model.logAutoScroll = true
	model.logOffset = 0
	model.logUpdates = model.logService.Stream(ctx, applogs.Request{
		Namespace:  model.logPod.Namespace,
		Pod:        model.logPod.Name,
		Container:  container.Name,
		Previous:   model.logPrevious,
		Follow:     model.logFollow && !model.logPrevious,
		Timestamps: model.logTimestamps,
		TailLines:  applogs.DefaultMaxLines,
	})
	return model, waitForLogEvent(model.logGeneration, model.logUpdates)
}

func (model Model) updateLogKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model.logSaving {
		switch key.String() {
		case "esc":
			model.logSaving = false
			model.logSavePath = ""
		case "enter":
			if err := model.logBuffer.Save(model.logSavePath); err != nil {
				model.logError = err
			} else {
				model.logNotice = "Saved logs to " + model.logSavePath
				model.logError = nil
				model.logSaving = false
			}
		case "backspace":
			path := []rune(model.logSavePath)
			if len(path) > 0 {
				model.logSavePath = string(path[:len(path)-1])
			}
		default:
			if key.Type == tea.KeyRunes {
				model.logSavePath += string(key.Runes)
			}
		}
		return model, nil
	}
	if model.logSearching {
		switch key.String() {
		case "esc":
			model.logSearching = false
			model.logQuery = ""
		case "enter":
			model.logSearching = false
		case "backspace":
			query := []rune(model.logQuery)
			if len(query) > 0 {
				model.logQuery = string(query[:len(query)-1])
			}
		default:
			if key.Type == tea.KeyRunes {
				model.logQuery += string(key.Runes)
			}
		}
		model.logAutoScroll = true
		model.logOffset = 0
		return model, nil
	}

	switch key.String() {
	case "q", "ctrl+c":
		if model.cancel != nil {
			model.cancel()
		}
		return model, tea.Quit
	case "esc", "backspace":
		if model.logCancel != nil {
			model.logCancel()
		}
		model.logVisible = false
		model.logUpdates = nil
		model.logBuffer = nil
	case "j", "down":
		model.logAutoScroll = false
		model.logOffset = max(0, model.logOffset-1)
	case "k", "up":
		model.logAutoScroll = false
		model.logOffset++
	case "pgup":
		model.logAutoScroll = false
		model.logOffset += max(1, model.height-7)
	case "pgdown":
		model.logAutoScroll = false
		model.logOffset = max(0, model.logOffset-max(1, model.height-7))
	case "G", "end":
		model.logAutoScroll = true
		model.logOffset = 0
	case "c", "tab":
		if len(model.logPod.Containers) > 1 {
			model.logContainer = (model.logContainer + 1) % len(model.logPod.Containers)
			model.logPrevious = false
			return model.startLogStream(true)
		}
	case "p":
		if len(model.logPod.Containers) > 0 && model.logPod.Containers[model.logContainer].HasPrevious {
			model.logPrevious = !model.logPrevious
			return model.startLogStream(true)
		}
	case "f":
		model.logFollow = !model.logFollow
		return model.startLogStream(true)
	case "t":
		model.logTimestamps = !model.logTimestamps
		return model.startLogStream(true)
	case "w":
		model.logWrap = !model.logWrap
		model.logAutoScroll = true
		model.logOffset = 0
	case "r":
		return model.startLogStream(false)
	case "/":
		model.logSearching = true
	case "s":
		model.logSaving = true
		model.logSavePath = ""
	}
	return model, nil
}

func (model Model) logView(width int) string {
	var output strings.Builder
	containerName := "discovering"
	containerKind := ""
	if len(model.logPod.Containers) > 0 && model.logContainer < len(model.logPod.Containers) {
		container := model.logPod.Containers[model.logContainer]
		containerName = container.Name
		containerKind = container.Kind
	}
	attempt := "current"
	if model.logPrevious {
		attempt = "previous"
	}
	fmt.Fprintf(&output, "%s  %s: %s  %s: %s  %s: %s  %s\n",
		theme.Title.Render("argowt"),
		theme.Muted.Render("pod"), theme.Accent.Render(ui.Sanitize(model.logPod.Name)),
		theme.Muted.Render("container"), theme.Info.Render(ui.Sanitize(containerName)+" ("+containerKind+")"),
		theme.Muted.Render("attempt"), theme.Info.Render(attempt),
		theme.Status(model.logState, model.logState))
	effectiveFollow := model.logFollow && !model.logPrevious
	fmt.Fprintf(&output, "%s: %s  %s: %t  %s: %t  %s: %t\n",
		theme.Muted.Render("node"), theme.Accent.Render(ui.Sanitize(model.logNode.Label())),
		theme.Muted.Render("follow"), effectiveFollow,
		theme.Muted.Render("timestamps"), model.logTimestamps,
		theme.Muted.Render("wrap"), model.logWrap)
	output.WriteString(theme.Muted.Render("c/Tab container · p previous · f follow · t timestamps · w wrap · / search · s save · r reconnect · Esc back") + "\n")
	if model.logError != nil {
		output.WriteString(theme.Error.Render("! "+ui.Sanitize(model.logError.Error())) + "\n")
	}
	if model.logNotice != "" {
		output.WriteString(theme.Success.Render("✓ "+ui.Sanitize(model.logNotice)) + "\n")
	}
	if model.logBuffer != nil && model.logBuffer.Dropped() > 0 {
		output.WriteString(theme.Warning.Render(fmt.Sprintf("! %d old log lines dropped to keep memory bounded", model.logBuffer.Dropped())) + "\n")
	}
	if model.logSearching || model.logQuery != "" {
		output.WriteString(theme.ActiveInput.Render("search: /"+ui.Sanitize(model.logQuery)) + "\n")
	}
	if model.logSaving {
		output.WriteString(theme.ActiveInput.Render("save path: "+ui.Sanitize(model.logSavePath)) + "\n")
		output.WriteString(theme.Muted.Render("Enter save · Esc cancel") + "\n")
	}

	lines := model.visibleLogLines(width)
	used := strings.Count(output.String(), "\n")
	visible := model.height - used - 1
	if visible < 1 || model.height == 0 {
		visible = 20
	}
	maximumOffset := max(0, len(lines)-visible)
	offset := min(model.logOffset, maximumOffset)
	start := max(0, len(lines)-visible-offset)
	end := min(len(lines), start+visible)
	output.WriteByte('\n')
	for _, line := range lines[start:end] {
		rendered := ui.Truncate(line, width)
		if model.logQuery != "" && strings.Contains(strings.ToLower(line), strings.ToLower(model.logQuery)) {
			rendered = theme.Warning.Render(rendered)
		}
		output.WriteString(rendered)
		output.WriteByte('\n')
	}
	if len(lines) == 0 && model.logError == nil {
		output.WriteString(theme.Muted.Render("Waiting for log output…") + "\n")
	}
	return output.String()
}

func (model Model) visibleLogLines(width int) []string {
	if model.logBuffer == nil {
		return nil
	}
	raw := model.logBuffer.Filtered(model.logQuery)
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, ui.Sanitize(line))
	}
	if model.logWrap {
		return applogs.Wrap(lines, max(1, width))
	}
	return lines
}
