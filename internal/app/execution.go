package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"argowt/internal/ui"
	"argowt/internal/workflows"
)

func (model Model) openWorkflowExecution() (tea.Model, tea.Cmd) {
	if model.executionRepository == nil {
		return model, nil
	}
	items := model.filteredItems()
	if model.cursor >= len(items) {
		return model, nil
	}
	workflow := items[model.cursor]
	if model.executionCancel != nil {
		model.executionCancel()
	}
	model.executionGeneration++
	executionContext, cancel := context.WithCancel(model.rootContext)
	model.executionCancel = cancel
	model.executionVisible = true
	model.execution = nil
	model.executionError = nil
	model.executionStale = false
	model.executionMode = "tree"
	model.executionCursor = 0
	model.executionScroll = 0
	model.executionSelectedID = ""
	model.executionUpdates = model.executionRepository.StartExecution(
		executionContext,
		model.executionGeneration,
		workflow.Namespace,
		workflow.Name,
		workflow.UID,
	)
	return model, waitForExecution(model.executionUpdates)
}

func (model Model) updateExecutionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		if model.cancel != nil {
			model.cancel()
		}
		return model, tea.Quit
	case "esc", "backspace":
		if model.executionCancel != nil {
			model.executionCancel()
		}
		model.executionVisible = false
		model.execution = nil
		model.executionError = nil
		model.executionSelectedID = ""
		return model, nil
	case "tab":
		switch model.executionMode {
		case "tree":
			model.executionMode = "dag"
		case "dag":
			model.executionMode = "table"
		default:
			model.executionMode = "tree"
		}
		model.reconcileExecutionSelection()
	case "t":
		model.executionMode = "tree"
		model.reconcileExecutionSelection()
	case "d":
		model.executionMode = "dag"
		model.reconcileExecutionSelection()
	case "l":
		model.executionMode = "table"
		model.reconcileExecutionSelection()
	case "j", "down":
		model.moveExecutionCursor(1)
	case "k", "up":
		model.moveExecutionCursor(-1)
	case "g", "home":
		model.executionCursor = 0
		model.syncExecutionSelection()
	case "G", "end":
		model.executionCursor = max(0, len(model.executionNodeIDs())-1)
		model.syncExecutionSelection()
	case "n":
		model.toggleSelectedNotification()
	case "N":
		model.openNotificationConfig()
	}
	return model, nil
}

func (model *Model) moveExecutionCursor(delta int) {
	ids := model.executionNodeIDs()
	if len(ids) == 0 {
		model.executionCursor = 0
		model.executionSelectedID = ""
		return
	}
	model.executionCursor = min(len(ids)-1, max(0, model.executionCursor+delta))
	model.executionSelectedID = ids[model.executionCursor]
}

func (model *Model) syncExecutionSelection() {
	ids := model.executionNodeIDs()
	if len(ids) == 0 {
		model.executionSelectedID = ""
		return
	}
	model.executionCursor = min(len(ids)-1, max(0, model.executionCursor))
	model.executionSelectedID = ids[model.executionCursor]
}

func (model *Model) reconcileExecutionSelection() {
	ids := model.executionNodeIDs()
	if len(ids) == 0 {
		model.executionCursor = 0
		model.executionSelectedID = ""
		return
	}
	if model.executionSelectedID != "" {
		for index, id := range ids {
			if id == model.executionSelectedID {
				model.executionCursor = index
				return
			}
		}
	}
	model.executionCursor = min(model.executionCursor, len(ids)-1)
	model.executionSelectedID = ids[model.executionCursor]
}

func (model Model) executionNodeIDs() []string {
	if model.execution == nil {
		return nil
	}
	if model.executionMode == "table" {
		nodes := model.execution.TableNodes()
		ids := make([]string, 0, len(nodes))
		for _, node := range nodes {
			ids = append(ids, node.ID)
		}
		return ids
	}
	rows := model.execution.TreeRows()
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.Node.ID)
	}
	return ids
}

func (model Model) executionView(width int) string {
	var output strings.Builder
	name := "workflow"
	phase := "loading"
	if model.execution != nil {
		name = model.execution.Namespace + "/" + model.execution.Name
		phase = model.execution.Phase
	}
	watchIndicator := ""
	if model.execution != nil && model.notificationManager != nil && model.notificationManager.IsWatched(model.execution.UID) {
		watchIndicator = "  🔔 notifications on"
	}
	fmt.Fprintf(&output, "%s  %s: %s  %s: %s  %s: %s%s\n",
		theme.Title.Render("argowt"), theme.Muted.Render("workflow"), theme.Accent.Render(ui.Sanitize(name)),
		theme.Muted.Render("mode"), theme.Info.Render(model.executionMode),
		theme.Muted.Render("phase"), theme.Status(ui.Sanitize(phase), phase), theme.Warning.Render(watchIndicator))
	output.WriteString(theme.Muted.Render("t tree · d DAG · l table · Tab next view · j/k navigate · n toggle notify · N configure · Esc back · q quit") + "\n")
	if model.executionStale {
		output.WriteString(theme.Warning.Render("! execution data is stale; reconnecting") + "\n")
	}
	if model.executionError != nil {
		fmt.Fprintf(&output, "%s\n", theme.Error.Render("! "+ui.Sanitize(model.executionError.Error())))
	}
	if model.execution == nil {
		output.WriteString("\n" + theme.Info.Render("◌ Loading workflow execution…") + "\n")
		return output.String()
	}
	if model.execution.Message != "" {
		fmt.Fprintf(&output, "%s %s\n", theme.Muted.Render("message:"), theme.Warning.Render(ui.Truncate(ui.Sanitize(model.execution.Message), max(1, width-9))))
	}
	if len(model.execution.Nodes) == 0 {
		output.WriteString("\nNo node status is available for this workflow.\n")
		return output.String()
	}

	listHeight := model.height * 3 / 5
	if listHeight < 5 || model.height == 0 {
		listHeight = 12
	}
	listHeight -= strings.Count(output.String(), "\n")
	if listHeight < 3 {
		listHeight = 3
	}
	lines := model.executionLines(width)
	start := 0
	if model.executionCursor >= listHeight {
		start = model.executionCursor - listHeight + 1
	}
	end := min(len(lines), start+listHeight)
	output.WriteByte('\n')
	if model.executionMode == "table" && width >= 65 {
		output.WriteString(theme.TableHeader.Render("  STATUS       TYPE          NODE                            DURATION") + "\n")
	}
	for _, line := range lines[start:end] {
		output.WriteString(line)
		output.WriteByte('\n')
	}

	if node, found := model.execution.Nodes[model.executionSelectedID]; found {
		output.WriteString("\n" + theme.PanelTitle.Render("── Node details ──") + "\n")
		detailLines := nodeDetailLines(node)
		remaining := model.height - strings.Count(output.String(), "\n") - 1
		if remaining < 1 || model.height == 0 {
			remaining = len(detailLines)
		}
		for index, line := range detailLines[:min(len(detailLines), remaining)] {
			rendered := ui.Truncate(ui.Sanitize(line), width)
			if index == 0 {
				rendered = theme.Status(rendered, node.Phase)
			} else if strings.HasPrefix(line, "message:") || strings.HasPrefix(line, "result:") {
				rendered = theme.Warning.Render(rendered)
			} else {
				rendered = theme.Muted.Render(rendered)
			}
			output.WriteString(rendered)
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func (model Model) executionLines(width int) []string {
	if model.execution == nil {
		return nil
	}
	now := time.Now()
	switch model.executionMode {
	case "table":
		nodes := model.execution.TableNodes()
		lines := []string{"  STATUS       TYPE          NODE                            DURATION"}
		// The header isn't selectable; keep row indexing aligned by returning it
		// separately would complicate viewport math, so omit it on narrow screens.
		if width < 65 {
			lines = lines[:0]
		}
		for index, node := range nodes {
			prefix := " "
			if index == model.executionCursor {
				prefix = ">"
			}
			line := fmt.Sprintf("%s %-12s %-13s %-31s %s",
				prefix, nodeStatus(node.Phase), node.Type, node.Label(), formatDuration(node.Duration(now)))
			line = ui.Truncate(line, width)
			if index == model.executionCursor {
				line = theme.Row(line, true)
			} else {
				line = theme.Status(line, node.Phase)
			}
			lines = append(lines, line)
		}
		if width >= 65 {
			// Remove the header to preserve cursor-to-line correspondence for the
			// scrolling calculation; the columns remain self-explanatory in help.
			return lines[1:]
		}
		return lines
	case "dag":
		rows := model.execution.TreeRows()
		lines := make([]string, 0, len(rows))
		for index, row := range rows {
			children := make([]string, 0, len(row.Node.Children))
			for _, childID := range row.Node.Children {
				if child, found := model.execution.Nodes[childID]; found {
					children = append(children, child.Label())
				}
			}
			edges := ""
			if len(children) > 0 {
				edges = " → " + strings.Join(children, ", ")
			}
			prefix := " "
			if index == model.executionCursor {
				prefix = ">"
			}
			line := ui.Truncate(fmt.Sprintf("%s %s %s%s", prefix, nodeStatus(row.Node.Phase), row.Node.Label(), edges), width)
			if index == model.executionCursor {
				line = theme.Row(line, true)
			} else {
				line = theme.Status(line, row.Node.Phase)
			}
			lines = append(lines, line)
		}
		return lines
	default:
		rows := model.execution.TreeRows()
		lines := make([]string, 0, len(rows))
		for index, row := range rows {
			prefix := " "
			if index == model.executionCursor {
				prefix = ">"
			}
			branch := strings.Repeat("  ", row.Depth)
			if row.Depth > 0 {
				branch += "└─"
			}
			cycle := ""
			if row.Cycle {
				cycle = " [cycle]"
			}
			line := ui.Truncate(fmt.Sprintf("%s %s %s%s  (%s)%s",
				prefix, nodeStatus(row.Node.Phase), branch, row.Node.Label(), row.Node.Type, cycle), width)
			if index == model.executionCursor {
				line = theme.Row(line, true)
			} else {
				line = theme.Status(line, row.Node.Phase)
			}
			lines = append(lines, line)
		}
		return lines
	}
}

func nodeDetailLines(node workflows.Node) []string {
	lines := []string{
		fmt.Sprintf("%s  phase=%s  type=%s  duration=%s", node.Label(), displayUnknown(node.Phase), displayUnknown(node.Type), formatDuration(node.Duration(time.Now()))),
	}
	if node.Template != "" || node.TemplateRef != "" {
		lines = append(lines, fmt.Sprintf("template=%s  templateRef=%s", node.Template, node.TemplateRef))
	}
	if node.Progress != "" {
		lines = append(lines, "progress="+node.Progress)
	}
	if node.Message != "" {
		lines = append(lines, "message: "+node.Message)
	}
	if node.Retried || node.Hooked {
		lines = append(lines, fmt.Sprintf("retried=%t  hook=%t", node.Retried, node.Hooked))
	}
	if node.BoundaryID != "" {
		lines = append(lines, "boundary="+node.BoundaryID)
	}
	if len(node.Parents) > 0 || len(node.Children) > 0 {
		lines = append(lines, fmt.Sprintf("parents=%s  children=%s", strings.Join(node.Parents, ","), strings.Join(node.Children, ",")))
	}
	if node.HostNodeName != "" || node.PodIP != "" {
		lines = append(lines, fmt.Sprintf("kubernetes node=%s  podIP=%s", node.HostNodeName, node.PodIP))
	}
	for _, input := range node.Inputs {
		lines = append(lines, fmt.Sprintf("input %s=%s", input.Name, input.Value))
	}
	for _, output := range node.Outputs {
		lines = append(lines, fmt.Sprintf("output %s=%s", output.Name, output.Value))
	}
	for _, artifact := range node.Artifacts {
		lines = append(lines, fmt.Sprintf("%s artifact %s  path=%s  location=%s", artifact.Direction, artifact.Name, artifact.Path, artifact.Location))
	}
	if node.ExitCode != "" {
		lines = append(lines, "exitCode="+node.ExitCode)
	}
	if node.Result != "" {
		lines = append(lines, "result: "+node.Result)
	}
	return lines
}

func nodeStatus(phase string) string {
	switch strings.ToLower(phase) {
	case "running":
		return "▶ Running"
	case "pending":
		return "… Pending"
	case "succeeded":
		return "✓ Succeeded"
	case "failed":
		return "✗ Failed"
	case "error":
		return "! Error"
	case "skipped":
		return "↷ Skipped"
	case "omitted":
		return "– Omitted"
	default:
		return "? " + displayUnknown(phase)
	}
}

func displayUnknown(value string) string {
	if value == "" {
		return "Unknown"
	}
	return value
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "—"
	}
	if duration < time.Minute {
		return duration.Round(time.Second).String()
	}
	return duration.Round(time.Minute).String()
}

func sortedNodeIDs(nodes map[string]workflows.Node) []string {
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
