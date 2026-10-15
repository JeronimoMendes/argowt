package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	applogs "argowt/internal/logs"
	"argowt/internal/ui"
	"argowt/internal/workflows"
)

// Node log panel in the workflow view.
// Moving the cursor waits nodePreviewDwell before it streams, so fast scrolling opens no streams.
const (
	nodePreviewDwell      = 900 * time.Millisecond
	nodePreviewTailLines  = 50
	nodePreviewBufferSize = 64 * 1024
)

// toggleNodePanel switches the bottom panel between node details and live logs.
func (model Model) toggleNodePanel() (tea.Model, tea.Cmd) {
	model.executionLogPanel = !model.executionLogPanel
	if !model.resetNodePreview() {
		return model, nil
	}
	return model, model.startNodePreviewDiscovery(model.nodePreviewGeneration, model.executionSelectedID)
}

func (model *Model) scheduleNodePreview() tea.Cmd {
	if !model.resetNodePreview() {
		return nil
	}
	generation := model.nodePreviewGeneration
	nodeID := model.executionSelectedID
	return tea.Tick(nodePreviewDwell, func(time.Time) tea.Msg {
		return nodePreviewDwellMessage{generation: generation, nodeID: nodeID}
	})
}

// resetNodePreview stops the current stream and clears its state.
// It returns true when the log panel is open on a node that has a pod.
func (model *Model) resetNodePreview() bool {
	if model.nodePreviewCancel != nil {
		model.nodePreviewCancel()
		model.nodePreviewCancel = nil
	}
	model.nodePreviewGeneration++
	model.nodePreviewID = ""
	model.nodePreviewPod = applogs.Pod{}
	model.nodePreviewBuffer = nil
	model.nodePreviewUpdates = nil
	model.nodePreviewState = ""
	model.nodePreviewError = nil
	if !model.executionLogPanel || model.logService == nil || model.execution == nil {
		return false
	}

	node, found := model.execution.Nodes[model.executionSelectedID]
	if !found || !hasPod(node) {
		return false
	}
	model.nodePreviewID = model.executionSelectedID
	model.nodePreviewBuffer = applogs.NewBuffer(nodePreviewTailLines, nodePreviewBufferSize)
	model.nodePreviewState = "waiting"
	return true
}

func hasPod(node workflows.Node) bool {
	return node.Type == "Pod" || node.Type == "Container"
}

func (model *Model) startNodePreviewDiscovery(generation uint64, nodeID string) tea.Cmd {
	if model.nodePreviewCancel != nil {
		model.nodePreviewCancel()
	}
	ctx, cancel := context.WithCancel(model.rootContext)
	model.nodePreviewCancel = cancel
	model.nodePreviewState = "discovering"
	node := model.execution.Nodes[nodeID]
	return func() tea.Msg {
		pod, err := model.logService.Discover(ctx, model.execution.Namespace, model.execution.Name, node.ID, node.Name)
		return nodePreviewDiscoveryMessage{generation: generation, nodeID: nodeID, pod: pod, err: err}
	}
}

func (model *Model) startNodePreviewStream(generation uint64, nodeID string) tea.Cmd {
	if len(model.nodePreviewPod.Containers) == 0 {
		return nil
	}
	if model.nodePreviewCancel != nil {
		model.nodePreviewCancel()
	}
	ctx, cancel := context.WithCancel(model.rootContext)
	model.nodePreviewCancel = cancel
	container := model.nodePreviewPod.Containers[0]
	model.nodePreviewState = "connecting"
	model.nodePreviewUpdates = model.logService.Stream(ctx, applogs.Request{
		Namespace: model.nodePreviewPod.Namespace,
		Pod:       model.nodePreviewPod.Name,
		Container: container.Name,
		Follow:    true,
		TailLines: nodePreviewTailLines,
	})
	return waitForNodePreviewEvent(generation, nodeID, model.nodePreviewUpdates)
}

func waitForNodePreviewEvent(generation uint64, nodeID string, updates <-chan applogs.Event) tea.Cmd {
	return func() tea.Msg {
		event, open := <-updates
		return nodePreviewEventMessage{generation: generation, nodeID: nodeID, event: event, updates: updates, open: open}
	}
}

func (model Model) openSelectedNodeLogs() (tea.Model, tea.Cmd) {
	if model.logService == nil || model.execution == nil {
		return model, nil
	}
	node, found := model.execution.Nodes[model.executionSelectedID]
	if !found {
		return model, nil
	}
	if model.nodePreviewCancel != nil {
		model.nodePreviewCancel()
		model.nodePreviewCancel = nil
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
		return model, model.scheduleNodePreview()
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
	case ":":
		model.palette = true
		model.command = ""
		model.paletteCursor = 0
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
	output.WriteString(theme.Muted.Render("c/Tab container · p previous · f follow · t timestamps · w wrap · / search · s save · r reconnect · : commands · Esc back") + "\n")
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

// nodeLogPanel renders the last log lines of the node, or why there are none.
func (model Model) nodeLogPanel(node workflows.Node, width, limit int) string {
	var message string
	switch {
	case !hasPod(node):
		return theme.Warning.Render(ui.Truncate(fmt.Sprintf("! %s is a %s node; it has no logs.", ui.Sanitize(node.DisplayName), ui.Sanitize(node.Type)), width)) + "\n"
	case model.nodePreviewError != nil:
		return theme.Error.Render(ui.Truncate("! logs unavailable: "+ui.Sanitize(model.nodePreviewError.Error()), width)) + "\n"
	case model.nodePreviewBuffer != nil && model.nodePreviewBuffer.Len() > 0:
		lines := model.nodePreviewBuffer.Lines()
		lines = lines[max(0, len(lines)-limit):]
		var output strings.Builder
		for _, line := range lines {
			output.WriteString(ui.Truncate(ui.Sanitize(line), width) + "\n")
		}
		return output.String()
	case model.nodePreviewState == "ended":
		message = "Log stream ended with no output."
	case model.nodePreviewState == "waiting":
		message = "Stay on this node to load its logs…"
	default:
		message = "Loading node logs…"
	}
	return theme.Muted.Render(message) + "\n"
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
