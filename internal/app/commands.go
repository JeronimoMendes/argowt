package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JeronimoMendes/argowt/internal/filter"
	"github.com/JeronimoMendes/argowt/internal/resources"
	"github.com/JeronimoMendes/argowt/internal/ui"
)

type paletteCommand struct {
	ID       string
	Title    string
	Shortcut string
}

func (model Model) paletteCommands() []paletteCommand {
	commands := make([]paletteCommand, 0, 24)
	for _, descriptor := range resources.Descriptors() {
		if model.availableResources != nil && !model.availableResources[descriptor.Kind] {
			continue
		}
		commands = append(commands, paletteCommand{
			ID:       "resource:" + string(descriptor.Kind),
			Title:    "Switch resource: " + descriptor.Title,
			Shortcut: ":" + descriptor.ShortName,
		})
	}

	switch {
	case model.logVisible:
		commands = append(commands,
			paletteCommand{ID: "logs.container", Title: "Logs: next container", Shortcut: "c / Tab"},
			paletteCommand{ID: "logs.previous", Title: "Logs: toggle previous attempt", Shortcut: "p"},
			paletteCommand{ID: "logs.follow", Title: "Logs: toggle follow", Shortcut: "f"},
			paletteCommand{ID: "logs.timestamps", Title: "Logs: toggle timestamps", Shortcut: "t"},
			paletteCommand{ID: "logs.wrap", Title: "Logs: toggle wrapping", Shortcut: "w"},
			paletteCommand{ID: "logs.search", Title: "Logs: search", Shortcut: "/"},
			paletteCommand{ID: "logs.save", Title: "Logs: save to file", Shortcut: "s"},
			paletteCommand{ID: "logs.reconnect", Title: "Logs: reconnect", Shortcut: "r"},
			paletteCommand{ID: "back", Title: "Back to workflow", Shortcut: "Esc"},
		)
	case model.executionVisible:
		commands = append(commands,
			paletteCommand{ID: "execution.logs", Title: "Open selected node logs", Shortcut: "Enter / o"},
			paletteCommand{ID: "execution.panel", Title: "Toggle node details/logs", Shortcut: "Tab"},
			paletteCommand{ID: "execution.tree", Title: "Workflow view: tree", Shortcut: "t"},
			paletteCommand{ID: "execution.dag", Title: "Workflow view: DAG", Shortcut: "d"},
			paletteCommand{ID: "execution.table", Title: "Workflow view: table", Shortcut: "l"},
			paletteCommand{ID: "notify.toggle", Title: "Toggle workflow notifications", Shortcut: "n"},
			paletteCommand{ID: "notify.configure", Title: "Configure workflow notifications", Shortcut: "N"},
			paletteCommand{ID: "back", Title: "Back to resources", Shortcut: "Esc"},
		)
	case model.detailVisible:
		commands = append(commands,
			paletteCommand{ID: "detail.yaml", Title: "Toggle metadata/YAML", Shortcut: "Tab / y"},
			paletteCommand{ID: "back", Title: "Back to resources", Shortcut: "Esc"},
		)
	default:
		commands = append(commands,
			paletteCommand{ID: "resource.next", Title: "Next resource", Shortcut: "Tab"},
			paletteCommand{ID: "resource.previous", Title: "Previous resource", Shortcut: "Shift+Tab"},
			paletteCommand{ID: "open", Title: "Open selected resource", Shortcut: "Enter"},
			paletteCommand{ID: "filter", Title: "Filter resources", Shortcut: "/"},
		)
		if model.resourceDescriptor.Kind == resources.Workflows {
			commands = append(commands,
				paletteCommand{ID: "notify.toggle", Title: "Toggle workflow notifications", Shortcut: "n"},
				paletteCommand{ID: "notify.configure", Title: "Configure workflow notifications", Shortcut: "N"},
			)
		}
		if model.resourceDescriptor.Kind == resources.WorkflowTemplates || model.resourceDescriptor.Kind == resources.ClusterWorkflowTemplates {
			commands = append(commands, paletteCommand{ID: "submit", Title: "Submit selected template", Shortcut: "s"})
		}
	}
	commands = append(commands,
		paletteCommand{ID: "help", Title: "Toggle contextual help", Shortcut: "?"},
		paletteCommand{ID: "quit", Title: "Quit argowt", Shortcut: "q"},
	)

	query := strings.TrimSpace(model.command)
	if query == "" {
		return commands
	}
	filtered := make([]paletteCommand, 0, len(commands))
	for _, command := range commands {
		if filter.Match(query, command.ID, command.Title, command.Shortcut) {
			filtered = append(filtered, command)
		}
	}
	return filtered
}

func (model Model) executePaletteCommand(command paletteCommand) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(command.ID, "resource:") {
		descriptor, found := resources.Lookup(strings.TrimPrefix(command.ID, "resource:"))
		if !found {
			return model, nil
		}
		return model.switchResource(descriptor)
	}
	switch command.ID {
	case "resource.next":
		return model.cycleResource(1)
	case "resource.previous":
		return model.cycleResource(-1)
	case "open":
		return model.openSelectedDetail()
	case "filter":
		model.filtering = true
	case "submit":
		return model.openTemplateSubmission()
	case "notify.toggle":
		model.toggleSelectedNotification()
	case "notify.configure":
		model.openNotificationConfig()
	case "execution.logs":
		return model.openSelectedNodeLogs()
	case "execution.panel":
		return model.toggleNodePanel()
	case "execution.tree", "execution.dag", "execution.table":
		model.executionMode = strings.TrimPrefix(command.ID, "execution.")
		model.reconcileExecutionSelection()
	case "logs.container":
		return model.executeLogShortcut("c")
	case "logs.previous":
		return model.executeLogShortcut("p")
	case "logs.follow":
		return model.executeLogShortcut("f")
	case "logs.timestamps":
		return model.executeLogShortcut("t")
	case "logs.wrap":
		return model.executeLogShortcut("w")
	case "logs.search":
		model.logSearching = true
	case "logs.save":
		model.logSaving = true
		model.logSavePath = ""
	case "logs.reconnect":
		return model.startLogStream(false)
	case "detail.yaml":
		model.detailYAML = !model.detailYAML
		model.detailScroll = 0
	case "back":
		return model.executeBack()
	case "help":
		model.showHelp = !model.showHelp
	case "quit":
		if model.cancel != nil {
			model.cancel()
		}
		return model, tea.Quit
	default:
		model.resourceNotice = fmt.Sprintf("Unknown command %s", command.ID)
	}
	return model, nil
}

func (model Model) executeLogShortcut(shortcut string) (tea.Model, tea.Cmd) {
	return model.updateLogKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shortcut)})
}

func (model Model) executeBack() (tea.Model, tea.Cmd) {
	switch {
	case model.logVisible:
		return model.updateLogKey(tea.KeyMsg{Type: tea.KeyEsc})
	case model.executionVisible:
		return model.updateExecutionKey(tea.KeyMsg{Type: tea.KeyEsc})
	case model.detailVisible:
		return model.updateDetailKey(tea.KeyMsg{Type: tea.KeyEsc})
	default:
		return model, nil
	}
}

func (model Model) cycleResource(delta int) (tea.Model, tea.Cmd) {
	available := make([]resources.Descriptor, 0)
	for _, descriptor := range resources.Descriptors() {
		if model.availableResources == nil || model.availableResources[descriptor.Kind] {
			available = append(available, descriptor)
		}
	}
	if len(available) == 0 {
		return model, nil
	}
	current := 0
	for index, descriptor := range available {
		if descriptor.Kind == model.resourceDescriptor.Kind {
			current = index
			break
		}
	}
	next := (current + delta) % len(available)
	if next < 0 {
		next += len(available)
	}
	return model.switchResource(available[next])
}

func (model Model) resourceTabs() string {
	parts := make([]string, 0)
	for _, descriptor := range resources.Descriptors() {
		if model.availableResources != nil && !model.availableResources[descriptor.Kind] {
			continue
		}
		label := descriptor.ShortName
		if descriptor.Kind == model.resourceDescriptor.Kind {
			label = theme.Selected.Render(" " + label + " ")
		} else {
			label = theme.Muted.Render(" " + label + " ")
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " ")
}

const paletteRows = 10

func (model Model) paletteView(width int) string {
	inner := panelInner(width)
	lines := []string{theme.ActiveInput.Render(":" + ui.Sanitize(model.command) + "▏")}
	commands := model.paletteCommands()
	start, end := window(len(commands), model.paletteCursor, paletteRows)
	for index := start; index < end; index++ {
		command := commands[index]
		selected := index == model.paletteCursor
		cursor := " "
		if selected {
			cursor = "›"
		}
		line := fmt.Sprintf("%s %-44s %s", cursor, command.Title, command.Shortcut)
		if selected {
			line = theme.Row(ui.Pad(ui.Truncate(line, inner), inner), true)
		}
		lines = append(lines, line)
	}
	if len(commands) == 0 {
		lines = append(lines, theme.Muted.Render("  No matching command."))
	}
	return theme.Panel(theme.PanelTitle.Render("Command palette"), theme.Muted.Render("Enter run · Esc close"), lines, width, 0, true)
}
