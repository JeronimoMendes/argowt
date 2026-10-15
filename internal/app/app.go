// Package app owns the top-level Bubble Tea model and process lifecycle.
package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"argowt/internal/config"
	"argowt/internal/filter"
	"argowt/internal/kube"
	"argowt/internal/ui"
	"argowt/internal/workflows"
)

// Options contains process-level settings established by the CLI. It contains
// no Kubernetes clients, credentials, or persisted resource data.
type Options struct {
	Kubeconfig    string
	Context       string
	Namespaces    []string
	AllNamespaces bool
	InstanceID    string
	Paths         config.Paths
	MouseEnabled  bool
}

// Run constructs native clients only after CLI parsing and starts the TUI.
func Run(ctx context.Context, options Options) error {
	connection, err := (kube.ClientBuilder{}).Build(kube.Options{
		Kubeconfig: options.Kubeconfig,
		Context:    options.Context,
	})
	if err != nil {
		return err
	}

	generation := uint64(1)
	repository := workflows.NewRepository(
		workflows.ArgoSource{Client: connection.Argo},
		kube.NamespaceLister{Client: connection.Kubernetes},
	)
	watchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	updates := repository.Start(watchContext, workflows.Request{
		Generation:       generation,
		AllNamespaces:    options.AllNamespaces,
		Namespaces:       options.Namespaces,
		CurrentNamespace: connection.DefaultNamespace,
		InstanceID:       options.InstanceID,
		ActiveOnly:       true,
	})
	model := NewModel(ModelOptions{
		Generation: generation,
		Context:    connection.Context,
		Namespace:  scopeLabel(options, connection.DefaultNamespace),
		InstanceID: options.InstanceID,
		Updates:    updates,
		Cancel:     cancel,
	})

	programOptions := []tea.ProgramOption{tea.WithContext(ctx), tea.WithAltScreen()}
	if options.MouseEnabled {
		programOptions = append(programOptions, tea.WithMouseCellMotion())
	}
	_, err = tea.NewProgram(model, programOptions...).Run()
	if err != nil {
		return fmt.Errorf("run terminal UI: %w", err)
	}
	return nil
}

func scopeLabel(options Options, defaultNamespace string) string {
	if options.AllNamespaces {
		return "all accessible"
	}
	if len(options.Namespaces) > 0 {
		return strings.Join(options.Namespaces, ",")
	}
	return defaultNamespace
}

type ModelOptions struct {
	Generation uint64
	Context    string
	Namespace  string
	InstanceID string
	Updates    <-chan workflows.Snapshot
	Cancel     context.CancelFunc
}

type Model struct {
	generation uint64
	context    string
	namespace  string
	instanceID string
	updates    <-chan workflows.Snapshot
	cancel     context.CancelFunc

	connectionState workflows.ConnectionState
	items           []workflows.Summary
	partialErrors   map[string]string
	fatalError      error
	cursor          int
	filtering       bool
	query           string
	showHelp        bool
	width           int
	height          int
}

type snapshotMessage struct {
	snapshot workflows.Snapshot
	open     bool
}

func NewModel(options ModelOptions) Model {
	return Model{
		generation:      options.Generation,
		context:         options.Context,
		namespace:       options.Namespace,
		instanceID:      options.InstanceID,
		updates:         options.Updates,
		cancel:          options.Cancel,
		connectionState: workflows.StateLoading,
	}
}

func (model Model) Init() tea.Cmd {
	return waitForSnapshot(model.updates)
}

func waitForSnapshot(updates <-chan workflows.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, open := <-updates
		return snapshotMessage{snapshot: snapshot, open: open}
	}
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case snapshotMessage:
		if !message.open {
			return model, nil
		}
		if message.snapshot.Generation == model.generation {
			model.connectionState = message.snapshot.State
			model.items = message.snapshot.Workflows
			model.partialErrors = message.snapshot.PartialErrors
			model.fatalError = message.snapshot.Err
			model.clampCursor()
		}
		return model, waitForSnapshot(model.updates)
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height
		return model, nil
	case tea.KeyMsg:
		return model.updateKey(message)
	case tea.MouseMsg:
		return model.updateMouse(message)
	}
	return model, nil
}

func (model Model) updateMouse(mouse tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch mouse.Button {
	case tea.MouseButtonWheelUp:
		model.moveCursor(-3)
	case tea.MouseButtonWheelDown:
		model.moveCursor(3)
	case tea.MouseButtonLeft:
		if mouse.Action != tea.MouseActionPress {
			break
		}
		row := mouse.Y - (4 + len(model.partialErrors))
		items := model.filteredItems()
		visibleRows := model.height - (4 + len(model.partialErrors)) - 3
		if visibleRows < 1 || model.height == 0 {
			visibleRows = len(items)
		}
		start := 0
		if model.cursor >= visibleRows {
			start = model.cursor - visibleRows + 1
		}
		index := start + row
		if row >= 0 && index >= 0 && index < len(items) {
			model.cursor = index
		}
	}
	return model, nil
}

func (model Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model.filtering {
		switch key.String() {
		case "esc":
			model.filtering = false
			model.query = ""
			model.cursor = 0
		case "enter":
			model.filtering = false
		case "backspace":
			query := []rune(model.query)
			if len(query) > 0 {
				model.query = string(query[:len(query)-1])
				model.cursor = 0
			}
		default:
			if key.Type == tea.KeyRunes {
				model.query += string(key.Runes)
				model.cursor = 0
			}
		}
		return model, nil
	}

	switch key.String() {
	case "q", "ctrl+c":
		if model.cancel != nil {
			model.cancel()
		}
		return model, tea.Quit
	case "j", "down":
		model.moveCursor(1)
	case "k", "up":
		model.moveCursor(-1)
	case "g", "home":
		model.cursor = 0
	case "G", "end":
		model.cursor = max(0, len(model.filteredItems())-1)
	case "/":
		model.filtering = true
	case "esc":
		model.query = ""
		model.cursor = 0
	case "?":
		model.showHelp = !model.showHelp
	}
	return model, nil
}

func (model *Model) moveCursor(delta int) {
	model.cursor += delta
	model.clampCursor()
}

func (model *Model) clampCursor() {
	last := len(model.filteredItems()) - 1
	if last < 0 {
		model.cursor = 0
		return
	}
	if model.cursor > last {
		model.cursor = last
	}
	if model.cursor < 0 {
		model.cursor = 0
	}
}

func (model Model) filteredItems() []workflows.Summary {
	if strings.TrimSpace(model.query) == "" {
		return model.items
	}
	items := make([]workflows.Summary, 0, len(model.items))
	for _, item := range model.items {
		if filter.Match(model.query, item.Name, item.Namespace, item.DisplayPhase(), item.InstanceID) {
			items = append(items, item)
		}
	}
	return items
}

func (model Model) View() string {
	width := model.width
	if width <= 0 {
		width = 100
	}
	var output strings.Builder
	fmt.Fprintf(&output, "argowt  context: %s  namespaces: %s  instance: %s\n",
		ui.Sanitize(model.context), ui.Sanitize(model.namespace), displayInstance(model.instanceID))
	fmt.Fprintf(&output, "status: %s", model.connectionState)
	if model.query != "" || model.filtering {
		fmt.Fprintf(&output, "  filter: /%s", ui.Sanitize(model.query))
	}
	output.WriteString("\n")

	if model.fatalError != nil {
		fmt.Fprintf(&output, "\n! %s\n", ui.Sanitize(model.fatalError.Error()))
		output.WriteString("\nq quit")
		return output.String()
	}
	if len(model.partialErrors) > 0 {
		keys := make([]string, 0, len(model.partialErrors))
		for key := range model.partialErrors {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&output, "! %s: %s\n", ui.Sanitize(key), ui.Sanitize(model.partialErrors[key]))
		}
	}

	output.WriteString("\n  PHASE         NAMESPACE           NAME                          AGE\n")
	items := model.filteredItems()
	visibleRows := model.height - strings.Count(output.String(), "\n") - 3
	if visibleRows < 1 || model.height == 0 {
		visibleRows = len(items)
	}
	start := 0
	if model.cursor >= visibleRows {
		start = model.cursor - visibleRows + 1
	}
	end := min(len(items), start+visibleRows)
	for index := start; index < end; index++ {
		item := items[index]
		cursor := " "
		if index == model.cursor {
			cursor = ">"
		}
		line := fmt.Sprintf("%s %-13s %-19s %-29s %s",
			cursor,
			statusText(item),
			ui.Truncate(ui.Sanitize(item.Namespace), 19),
			ui.Truncate(ui.Sanitize(item.Name), 29),
			formatAge(item.CreationTimestamp),
		)
		output.WriteString(ui.Truncate(line, width))
		output.WriteByte('\n')
	}
	if len(items) == 0 && model.connectionState != workflows.StateLoading {
		output.WriteString("  No active workflows match the current scope.\n")
	}

	if model.showHelp {
		output.WriteString("\nNavigation: j/k or arrows · g/G first/last · / filter · Esc clear · q quit · ? hide help\n")
	} else if model.filtering {
		output.WriteString("\nFilter: type to search · Enter apply · Esc cancel\n")
	} else {
		output.WriteString("\nj/k navigate · / filter · ? help · q quit\n")
	}
	return output.String()
}

func displayInstance(instanceID string) string {
	if instanceID == "" {
		return "all"
	}
	return ui.Sanitize(instanceID)
}

func statusText(summary workflows.Summary) string {
	switch strings.ToLower(summary.DisplayPhase()) {
	case "running":
		return "▶ Running"
	case "pending":
		return "… Pending"
	case "suspended":
		return "Ⅱ Suspended"
	case "failed":
		return "✗ Failed"
	case "error":
		return "! Error"
	case "succeeded":
		return "✓ Succeeded"
	default:
		return "? " + ui.Truncate(ui.Sanitize(summary.DisplayPhase()), 11)
	}
}

func formatAge(created time.Time) string {
	if created.IsZero() {
		return "unknown"
	}
	duration := time.Since(created)
	if duration < 0 {
		duration = 0
	}
	switch {
	case duration < time.Minute:
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	case duration < 24*time.Hour:
		return fmt.Sprintf("%dh", int(duration.Hours()))
	default:
		return fmt.Sprintf("%dd", int(duration.Hours()/24))
	}
}

// MutationAllowed centralizes the safety gate used by future operation views.
func (model Model) MutationAllowed() bool {
	return model.connectionState == workflows.StateFresh || model.connectionState == workflows.StatePartial
}

var _ tea.Model = Model{}
