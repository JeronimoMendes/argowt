package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JeronimoMendes/argowt/internal/argourl"
	"github.com/JeronimoMendes/argowt/internal/resources"
	"github.com/JeronimoMendes/argowt/internal/ui"
	"github.com/JeronimoMendes/argowt/internal/workflows"
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
	return model.startExecution(workflow.Namespace, workflow.Name, workflow.UID)
}

// startExecution opens the live execution view. An empty uid skips the
// identity check.
func (model Model) startExecution(namespace, name, uid string) (tea.Model, tea.Cmd) {
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
		namespace,
		name,
		uid,
	)
	return model, waitForExecution(model.executionUpdates)
}

// openTarget shows a startup URL target: a workflow execution, a resource
// detail, or a resource list.
func (model Model) openTarget(target argourl.Target) (tea.Model, tea.Cmd) {
	descriptor, found := resources.Lookup(string(target.Kind))
	if !found {
		return model, nil
	}
	switched, command := model.switchResource(descriptor)
	model = switched.(Model)
	if target.Name == "" {
		return model, command
	}

	if target.Kind != resources.Workflows {
		if model.resourceRepository == nil {
			return model, command
		}
		opened, detailCommand := model.openDetail(target.Namespace, target.Name, "")
		return opened, tea.Batch(command, detailCommand)
	}
	if model.executionRepository == nil {
		return model, command
	}
	opened, executionCommand := model.startExecution(target.Namespace, target.Name, target.UID)
	model = opened.(Model)

	// The first execution update moves the cursor to this node.
	model.executionSelectedID = target.NodeID
	return model, tea.Batch(command, executionCommand)
}

func (model Model) updateExecutionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	selectedBefore := model.executionSelectedID
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
		if model.nodePreviewCancel != nil {
			model.nodePreviewCancel()
		}
		model.executionVisible = false
		model.execution = nil
		model.executionError = nil
		model.executionSelectedID = ""
		return model, nil
	case "tab":
		return model.toggleNodePanel()
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
	case "enter", "o":
		return model.openSelectedNodeLogs()
	case ":":
		model.palette = true
		model.command = ""
		model.paletteCursor = 0
	}
	if model.executionSelectedID != selectedBefore {
		return model, model.scheduleNodePreview()
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

var executionModes = []string{"tree", "dag", "table"}

func (model Model) executionView(width int) string {
	name := "workflow"
	status := theme.Info.Render("◌ loading")
	if model.execution != nil {
		name = model.execution.Namespace + "/" + model.execution.Name
		status = theme.Status(nodeStatus(model.execution.Phase), model.execution.Phase)
	}
	items := []string{label("context", model.context), label("workflow", name)}
	if model.execution != nil && model.notificationManager != nil && model.notificationManager.IsWatched(model.execution.UID) {
		items = append(items, theme.Warning.Render("🔔 notifying"))
	}
	lines := []string{theme.Header(width, status, items...)}
	if model.executionStale {
		lines = append(lines, theme.Warning.Render(" ! execution data is stale; reconnecting"))
	}
	if model.executionError != nil {
		lines = append(lines, theme.Error.Render(" ! "+ui.Sanitize(model.executionError.Error())))
	}
	if model.execution != nil && model.execution.Message != "" {
		lines = append(lines, theme.Muted.Render(" message ")+theme.Warning.Render(ui.Sanitize(model.execution.Message)))
	}
	for index := range lines {
		lines[index] = ui.TruncateStyled(lines[index], width)
	}
	footer := theme.Keys(width, "j/k", "move", "t/d/l", "tree/DAG/table", "Tab", "details/logs", "Enter", "full logs", "n", "notify", "N", "configure", ":", "commands", "Esc", "back", "q", "quit")
	return model.frame(strings.Join(lines, "\n"), footer, func(rows int) string {
		return model.executionPanels(width, rows)
	})
}

// executionPanels draws the node list over the selected node's details or logs.
func (model Model) executionPanels(width, rows int) string {
	inner := panelInner(width)
	modeTabs := theme.PanelTitle.Render("Nodes") + "  " + theme.Tabs(executionModes, slices.Index(executionModes, model.executionMode))
	if model.execution == nil {
		return theme.Panel(modeTabs, "", []string{theme.Info.Render("◌ Loading workflow execution…")}, width, rows, true)
	}
	if len(model.execution.Nodes) == 0 {
		return theme.Panel(modeTabs, "", []string{"No node status is available for this workflow."}, width, rows, true)
	}

	var header []string
	if model.executionMode == "table" {
		header = []string{theme.TableHeader.Render(fmt.Sprintf("  %-12s %-13s %-31s %s", "STATUS", "TYPE", "NODE", "DURATION"))}
	}
	nodeLines := model.executionLines(inner)
	listRows, detailRows := 0, 0
	if rows > 0 {
		listRows = min(len(header)+len(nodeLines)+2, max(3, rows/2))
		detailRows = max(3, rows-listRows)
	}
	start, end := window(len(nodeLines), model.executionCursor, max(0, listRows-2-len(header)))
	list := theme.Panel(modeTabs, theme.Muted.Render(plural(len(nodeLines), "node")), append(header, nodeLines[start:end]...), width, listRows, true)

	node, found := model.execution.Nodes[model.executionSelectedID]
	if !found {
		return list
	}
	panel := 0
	var body []string
	if model.executionLogPanel {
		panel = 1
		limit := nodePreviewTailLines
		if detailRows > 0 {
			limit = detailRows - 2
		}
		body = model.nodeLogLines(node, inner, limit)
	} else {
		body = nodeDetailLines(node)
	}
	title := theme.Tabs([]string{"Node details", "Live logs"}, panel)
	return list + "\n" + theme.Panel(title, theme.Muted.Render("Tab switch · Enter full logs"), body, width, detailRows, false)
}

func (model Model) executionLines(width int) []string {
	if model.execution == nil {
		return nil
	}
	now := time.Now()
	cursor := func(index int) string {
		if index == model.executionCursor {
			return "›"
		}
		return " "
	}
	if model.executionMode == "table" {
		nodes := model.execution.TableNodes()
		lines := make([]string, 0, len(nodes))
		for index, node := range nodes {
			line := fmt.Sprintf("%s %-12s %-13s %-31s %s",
				cursor(index), nodeStatus(node.Phase), node.Type, node.Label(), formatDuration(node.Duration(now)))
			lines = append(lines, styleRow(line, width, index == model.executionCursor, node.Phase))
		}
		return lines
	}

	rows := model.execution.TreeRows()
	lines := make([]string, 0, len(rows))
	for index, row := range rows {
		var line string
		if model.executionMode == "dag" {
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
			line = fmt.Sprintf("%s %-12s %s%s", cursor(index), nodeStatus(row.Node.Phase), row.Node.Label(), edges)
		} else {
			branch := strings.Repeat("  ", row.Depth)
			if row.Depth > 0 {
				branch += "└─"
			}
			cycle := ""
			if row.Cycle {
				cycle = " [cycle]"
			}
			line = fmt.Sprintf("%s %-12s %s%s  (%s)%s", cursor(index), nodeStatus(row.Node.Phase), branch, row.Node.Label(), row.Node.Type, cycle)
		}
		lines = append(lines, styleRow(line, width, index == model.executionCursor, row.Node.Phase))
	}
	return lines
}

// nodeDetailLines lists the node fields as aligned name/value rows.
func nodeDetailLines(node workflows.Node) []string {
	lines := []string{
		field("node", ui.Sanitize(node.Label())),
		field("phase", theme.Status(nodeStatus(node.Phase), node.Phase)) +
			"   " + theme.Muted.Render("type") + " " + ui.Sanitize(displayUnknown(node.Type)) +
			"   " + theme.Muted.Render("duration") + " " + formatDuration(node.Duration(time.Now())),
	}
	optional := []struct{ name, value string }{
		{"template", node.Template},
		{"templateRef", node.TemplateRef},
		{"progress", node.Progress},
		{"boundary", node.BoundaryID},
		{"parents", strings.Join(node.Parents, ", ")},
		{"children", strings.Join(node.Children, ", ")},
		{"host node", node.HostNodeName},
		{"pod IP", node.PodIP},
		{"exit code", node.ExitCode},
	}
	for _, item := range optional {
		if item.value != "" {
			lines = append(lines, field(item.name, ui.Sanitize(item.value)))
		}
	}
	if node.Retried {
		lines = append(lines, field("retried", "yes"))
	}
	if node.Hooked {
		lines = append(lines, field("hook", "yes"))
	}
	for _, input := range node.Inputs {
		lines = append(lines, field("input", ui.Sanitize(input.Name+" = "+input.Value)))
	}
	for _, output := range node.Outputs {
		lines = append(lines, field("output", ui.Sanitize(output.Name+" = "+output.Value)))
	}
	for _, artifact := range node.Artifacts {
		lines = append(lines, field(ui.Sanitize(artifact.Direction+" artifact"), ui.Sanitize(fmt.Sprintf("%s  path=%s  location=%s", artifact.Name, artifact.Path, artifact.Location))))
	}
	if node.Message != "" {
		lines = append(lines, field("message", theme.Warning.Render(ui.Sanitize(node.Message))))
	}
	if node.Result != "" {
		lines = append(lines, field("result", theme.Warning.Render(ui.Sanitize(node.Result))))
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
