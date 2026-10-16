package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	applogs "github.com/JeronimoMendes/argowt/internal/logs"
	"github.com/JeronimoMendes/argowt/internal/ui"
	"github.com/JeronimoMendes/argowt/internal/workflows"
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
			if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
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
			if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
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
		model.logOffset += model.logPage()
	case "pgdown":
		model.logAutoScroll = false
		model.logOffset = max(0, model.logOffset-model.logPage())
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
	top, footer := model.logChrome(width)
	return model.frame(top, footer, func(rows int) string {
		inner := panelInner(width)
		lines := model.visibleLogLines(inner)
		visible := 20
		if rows > 0 {
			visible = max(1, rows-2)
		}
		offset := min(model.logOffset, max(0, len(lines)-visible))
		start := max(0, len(lines)-visible-offset)
		end := min(len(lines), start+visible)
		body := make([]string, 0, end-start)
		for _, line := range lines[start:end] {
			if model.logQuery != "" && strings.Contains(strings.ToLower(line), strings.ToLower(model.logQuery)) {
				line = theme.Warning.Render(ui.Truncate(line, inner))
			}
			body = append(body, line)
		}
		if len(lines) == 0 && model.logError == nil {
			body = append(body, theme.Muted.Render("Waiting for log output…"))
		}
		title := theme.PanelTitle.Render("Logs")
		return theme.Panel(title, theme.Muted.Render(plural(len(lines), "line")), body, width, rows, true)
	})
}

// logChrome returns the lines above and below the log panel.
func (model Model) logChrome(width int) (string, string) {
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
	toggle := func(name string, on bool) string {
		if on {
			return theme.Success.Render("● " + name)
		}
		return theme.Muted.Render("○ " + name)
	}
	lines := []string{
		theme.Header(width, theme.Status("● "+model.logState, model.logState),
			label("pod", model.logPod.Name), label("container", containerName+" ("+containerKind+")"), label("attempt", attempt)),
		" " + label("node", model.logNode.Label()) + "   " + toggle("follow", model.logFollow && !model.logPrevious) +
			"  " + toggle("timestamps", model.logTimestamps) + "  " + toggle("wrap", model.logWrap),
	}
	if model.logError != nil {
		lines = append(lines, theme.Error.Render(" ! "+ui.Sanitize(model.logError.Error())))
	}
	if model.logNotice != "" {
		lines = append(lines, theme.Success.Render(" ✓ "+ui.Sanitize(model.logNotice)))
	}
	if model.logBuffer != nil && model.logBuffer.Dropped() > 0 {
		lines = append(lines, theme.Warning.Render(fmt.Sprintf(" ! %d old log lines dropped to keep memory bounded", model.logBuffer.Dropped())))
	}
	if model.logSearching || model.logQuery != "" {
		lines = append(lines, theme.ActiveInput.Render(" / "+ui.Sanitize(model.logQuery)))
	}
	for index := range lines {
		lines[index] = ui.TruncateStyled(lines[index], width)
	}

	footer := theme.Keys(width, "c/Tab", "container", "p", "previous", "f", "follow", "t", "timestamps", "w", "wrap", "/", "search", "s", "save", "r", "reconnect", ":", "commands", "Esc", "back")
	if model.logSaving {
		footer = theme.ActiveInput.Render(" save path: "+ui.Sanitize(model.logSavePath)+"▏") + "\n" + theme.Keys(width, "Enter", "save", "Esc", "cancel")
	}
	return strings.Join(lines, "\n"), footer
}

// logPage is how many log lines a page scroll moves.
func (model Model) logPage() int {
	top, footer := model.logChrome(model.viewWidth())
	return max(1, model.bodyRows(top, model.footer(footer))-2)
}

// nodeLogLines returns the last log lines of the node, or why there are none.
func (model Model) nodeLogLines(node workflows.Node, width, limit int) []string {
	switch {
	case !hasPod(node):
		return []string{theme.Warning.Render(fmt.Sprintf("! %s is a %s node; it has no logs.", ui.Sanitize(node.DisplayName), ui.Sanitize(node.Type)))}
	case model.nodePreviewError != nil:
		return []string{theme.Error.Render("! logs unavailable: " + ui.Sanitize(model.nodePreviewError.Error()))}
	case model.nodePreviewBuffer != nil && model.nodePreviewBuffer.Len() > 0:
		lines := model.nodePreviewBuffer.Lines()
		lines = lines[max(0, len(lines)-limit):]
		for index, line := range lines {
			lines[index] = ui.Truncate(ui.Sanitize(line), width)
		}
		return lines
	case model.nodePreviewState == "ended":
		return []string{theme.Muted.Render("Log stream ended with no output.")}
	case model.nodePreviewState == "waiting":
		return []string{theme.Muted.Render("Stay on this node to load its logs…")}
	default:
		return []string{theme.Muted.Render("Loading node logs…")}
	}
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
