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
	"argowt/internal/resources"
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
	namespaceLister := kube.NamespaceLister{Client: connection.Kubernetes}
	repository := workflows.NewRepository(
		workflows.ArgoSource{Client: connection.Argo},
		namespaceLister,
	)
	resourceRepository := resources.NewRepository(connection.Dynamic, connection.Kubernetes.Discovery(), namespaceLister)
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
		Generation:         generation,
		Context:            connection.Context,
		Namespace:          scopeLabel(options, connection.DefaultNamespace),
		InstanceID:         options.InstanceID,
		Updates:            updates,
		Cancel:             cancel,
		RootContext:        watchContext,
		ResourceRepository: resourceRepository,
		AllNamespaces:      options.AllNamespaces,
		Namespaces:         options.Namespaces,
		CurrentNamespace:   connection.DefaultNamespace,
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

type ResourceRepository interface {
	Discover(context.Context) (map[resources.Kind]bool, error)
	Start(context.Context, resources.Request) <-chan resources.Snapshot
}

type ModelOptions struct {
	Generation         uint64
	Context            string
	Namespace          string
	InstanceID         string
	Updates            <-chan workflows.Snapshot
	Cancel             context.CancelFunc
	RootContext        context.Context
	ResourceRepository ResourceRepository
	AllNamespaces      bool
	Namespaces         []string
	CurrentNamespace   string
}

type Model struct {
	generation uint64
	context    string
	namespace  string
	instanceID string
	updates    <-chan workflows.Snapshot
	cancel     context.CancelFunc

	rootContext        context.Context
	resourceRepository ResourceRepository
	allNamespaces      bool
	namespaces         []string
	currentNamespace   string
	resourceGeneration uint64
	resourceUpdates    <-chan resources.Snapshot
	resourceCancel     context.CancelFunc
	resourceDescriptor resources.Descriptor
	resourceItems      []resources.Summary
	resourceState      string
	resourceErrors     map[string]string
	resourceError      error
	availableResources map[resources.Kind]bool
	resourceNotice     string

	connectionState workflows.ConnectionState
	items           []workflows.Summary
	partialErrors   map[string]string
	fatalError      error
	cursor          int
	filtering       bool
	query           string
	activeFilter    filter.Expression
	filterError     error
	showHelp        bool
	palette         bool
	command         string
	paletteCursor   int
	width           int
	height          int
}

type snapshotMessage struct {
	snapshot workflows.Snapshot
	open     bool
}

type resourceSnapshotMessage struct {
	snapshot resources.Snapshot
	updates  <-chan resources.Snapshot
	open     bool
}

type discoveryMessage struct {
	available map[resources.Kind]bool
	err       error
}

func NewModel(options ModelOptions) Model {
	workflowDescriptor, _ := resources.Lookup("workflows")
	available := make(map[resources.Kind]bool)
	for _, descriptor := range resources.Descriptors() {
		available[descriptor.Kind] = true
	}
	rootContext := options.RootContext
	if rootContext == nil {
		rootContext = context.Background()
	}
	return Model{
		generation:         options.Generation,
		context:            options.Context,
		namespace:          options.Namespace,
		instanceID:         options.InstanceID,
		updates:            options.Updates,
		cancel:             options.Cancel,
		rootContext:        rootContext,
		resourceRepository: options.ResourceRepository,
		allNamespaces:      options.AllNamespaces,
		namespaces:         append([]string(nil), options.Namespaces...),
		currentNamespace:   options.CurrentNamespace,
		resourceDescriptor: workflowDescriptor,
		availableResources: available,
		connectionState:    workflows.StateLoading,
		resourceState:      "loading",
	}
}

func (model Model) Init() tea.Cmd {
	commands := []tea.Cmd{waitForSnapshot(model.updates)}
	if model.resourceRepository != nil {
		commands = append(commands, discoverResources(model.rootContext, model.resourceRepository))
	}
	return tea.Batch(commands...)
}

func waitForSnapshot(updates <-chan workflows.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, open := <-updates
		return snapshotMessage{snapshot: snapshot, open: open}
	}
}

func waitForResourceSnapshot(updates <-chan resources.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, open := <-updates
		return resourceSnapshotMessage{snapshot: snapshot, updates: updates, open: open}
	}
}

func discoverResources(ctx context.Context, repository ResourceRepository) tea.Cmd {
	return func() tea.Msg {
		available, err := repository.Discover(ctx)
		return discoveryMessage{available: available, err: err}
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
	case resourceSnapshotMessage:
		if !message.open {
			return model, nil
		}
		if message.snapshot.Generation == model.resourceGeneration && message.snapshot.Descriptor.Kind == model.resourceDescriptor.Kind {
			if message.snapshot.Missing && message.snapshot.Descriptor.Optional {
				model.availableResources[message.snapshot.Descriptor.Kind] = false
				model.resourceNotice = message.snapshot.Descriptor.Title + " CRD is not installed"
				workflowDescriptor, _ := resources.Lookup("workflows")
				model.resourceDescriptor = workflowDescriptor
				model.cursor = 0
			} else {
				model.resourceItems = message.snapshot.Items
				model.resourceState = message.snapshot.State
				model.resourceErrors = message.snapshot.PartialErrors
				model.resourceError = message.snapshot.Err
				model.clampCursor()
			}
		}
		return model, waitForResourceSnapshot(message.updates)
	case discoveryMessage:
		if message.err != nil {
			model.resourceNotice = message.err.Error()
		} else {
			for kind, available := range message.available {
				model.availableResources[kind] = available
			}
			model.availableResources[resources.Workflows] = true
		}
		return model, nil
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
		noticeLines := len(model.currentErrors())
		if model.filterError != nil {
			noticeLines++
		}
		if model.resourceNotice != "" {
			noticeLines++
		}
		row := mouse.Y - (4 + noticeLines)
		itemCount := model.itemCount()
		visibleRows := model.height - (4 + noticeLines) - 3
		if visibleRows < 1 || model.height == 0 {
			visibleRows = itemCount
		}
		start := 0
		if model.cursor >= visibleRows {
			start = model.cursor - visibleRows + 1
		}
		index := start + row
		if row >= 0 && index >= 0 && index < itemCount {
			model.cursor = index
		}
	}
	return model, nil
}

func (model Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model.palette {
		return model.updatePaletteKey(key)
	}
	if model.filtering {
		switch key.String() {
		case "esc":
			model.filtering = false
			model.query = ""
			model.updateFilter()
			model.cursor = 0
		case "enter":
			model.filtering = false
		case "backspace":
			query := []rune(model.query)
			if len(query) > 0 {
				model.query = string(query[:len(query)-1])
				model.updateFilter()
				model.cursor = 0
			}
		default:
			if key.Type == tea.KeyRunes {
				model.query += string(key.Runes)
				model.updateFilter()
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
		model.cursor = max(0, model.itemCount()-1)
	case "/":
		model.filtering = true
	case ":":
		model.palette = true
		model.command = ""
		model.paletteCursor = 0
	case "esc":
		model.query = ""
		model.updateFilter()
		model.cursor = 0
	case "?":
		model.showHelp = !model.showHelp
	}
	return model, nil
}

func (model Model) updatePaletteKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	commands := resources.Commands(model.availableResources, model.command)
	switch key.String() {
	case "esc":
		model.palette = false
		model.command = ""
	case "enter":
		if len(commands) == 0 {
			return model, nil
		}
		index := min(model.paletteCursor, len(commands)-1)
		model.palette = false
		model.command = ""
		return model.switchResource(commands[index])
	case "up", "ctrl+k":
		model.paletteCursor = max(0, model.paletteCursor-1)
	case "down", "ctrl+j":
		model.paletteCursor = min(max(0, len(commands)-1), model.paletteCursor+1)
	case "backspace":
		command := []rune(model.command)
		if len(command) > 0 {
			model.command = string(command[:len(command)-1])
			model.paletteCursor = 0
		}
	default:
		if key.Type == tea.KeyRunes {
			model.command += string(key.Runes)
			model.paletteCursor = 0
		}
	}
	return model, nil
}

func (model Model) switchResource(descriptor resources.Descriptor) (tea.Model, tea.Cmd) {
	if model.resourceCancel != nil {
		model.resourceCancel()
		model.resourceCancel = nil
	}
	model.resourceDescriptor = descriptor
	model.resourceNotice = ""
	model.resourceError = nil
	model.resourceErrors = nil
	model.resourceItems = nil
	model.cursor = 0
	if descriptor.Kind == resources.Workflows || model.resourceRepository == nil {
		return model, nil
	}

	model.resourceGeneration++
	resourceContext, cancel := context.WithCancel(model.rootContext)
	model.resourceCancel = cancel
	model.resourceState = "loading"
	model.resourceUpdates = model.resourceRepository.Start(resourceContext, resources.Request{
		Generation:       model.resourceGeneration,
		Descriptor:       descriptor,
		AllNamespaces:    model.allNamespaces,
		Namespaces:       model.namespaces,
		CurrentNamespace: model.currentNamespace,
		InstanceID:       model.instanceID,
		LabelSelector:    model.activeFilter.LabelSelector(),
	})
	return model, waitForResourceSnapshot(model.resourceUpdates)
}

func (model *Model) moveCursor(delta int) {
	model.cursor += delta
	model.clampCursor()
}

func (model *Model) clampCursor() {
	last := model.itemCount() - 1
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

func (model Model) itemCount() int {
	if model.resourceDescriptor.Kind == resources.Workflows {
		return len(model.filteredItems())
	}
	return len(model.filteredResourceItems())
}

func (model *Model) updateFilter() {
	parsed, err := filter.Parse(model.query)
	model.filterError = err
	if err == nil {
		model.activeFilter = parsed
	}
}

func (model Model) filteredItems() []workflows.Summary {
	if model.activeFilter.Empty() {
		return model.items
	}
	items := make([]workflows.Summary, 0, len(model.items))
	now := time.Now()
	for _, item := range model.items {
		if model.activeFilter.Match(filter.Document{
			Name:              item.Name,
			Namespace:         item.Namespace,
			Status:            item.DisplayPhase(),
			Template:          item.Template,
			InstanceID:        item.InstanceID,
			Labels:            item.Labels,
			CreationTimestamp: item.CreationTimestamp,
		}, now) {
			items = append(items, item)
		}
	}
	return items
}

func (model Model) filteredResourceItems() []resources.Summary {
	if model.activeFilter.Empty() {
		return model.resourceItems
	}
	items := make([]resources.Summary, 0, len(model.resourceItems))
	now := time.Now()
	for _, item := range model.resourceItems {
		if model.activeFilter.Match(filter.Document{
			Name:              item.Name,
			Namespace:         item.Namespace,
			Status:            item.Status,
			Template:          item.Template,
			InstanceID:        item.InstanceID,
			Labels:            item.Labels,
			CreationTimestamp: item.CreationTimestamp,
		}, now) {
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
	fmt.Fprintf(&output, "argowt  view: %s  context: %s  namespaces: %s  instance: %s\n",
		ui.Sanitize(model.resourceDescriptor.Title), ui.Sanitize(model.context), ui.Sanitize(model.namespace), displayInstance(model.instanceID))
	fmt.Fprintf(&output, "status: %s", model.currentState())
	if model.query != "" || model.filtering {
		fmt.Fprintf(&output, "  filter: /%s", ui.Sanitize(model.query))
	}
	output.WriteString("\n")
	if model.resourceNotice != "" {
		fmt.Fprintf(&output, "! %s\n", ui.Sanitize(model.resourceNotice))
	}
	if model.filterError != nil {
		fmt.Fprintf(&output, "! filter: %s (previous valid filter remains active)\n", ui.Sanitize(model.filterError.Error()))
	}
	if currentError := model.currentError(); currentError != nil {
		fmt.Fprintf(&output, "\n! %s\n", ui.Sanitize(currentError.Error()))
	}
	if errorsByScope := model.currentErrors(); len(errorsByScope) > 0 {
		keys := make([]string, 0, len(errorsByScope))
		for key := range errorsByScope {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&output, "! %s: %s\n", ui.Sanitize(key), ui.Sanitize(errorsByScope[key]))
		}
	}

	output.WriteString("\n  STATUS        NAMESPACE           NAME                          AGE\n")
	if model.resourceDescriptor.Kind == resources.Workflows {
		model.renderWorkflows(&output, width)
	} else {
		model.renderResources(&output, width)
	}

	if model.palette {
		output.WriteString("\nCommand: :" + ui.Sanitize(model.command) + "\n")
		commands := resources.Commands(model.availableResources, model.command)
		for index, descriptor := range commands {
			cursor := " "
			if index == model.paletteCursor {
				cursor = ">"
			}
			fmt.Fprintf(&output, "%s :%-9s %s\n", cursor, descriptor.ShortName, descriptor.Title)
		}
		if len(commands) == 0 {
			output.WriteString("  No matching resource command.\n")
		}
		output.WriteString("Enter switch · Esc close\n")
	} else if model.showHelp {
		output.WriteString("\nNavigation: j/k or arrows · g/G first/last · : resources · / filter · Esc clear · q quit · ? hide help\n")
		output.WriteString("Filters: fuzzy text · status=Running · namespace!=argo · age>10m · label:team=payments\n")
	} else if model.filtering {
		output.WriteString("\nFilter: type to search · Enter apply · Esc cancel\n")
	} else {
		output.WriteString("\nj/k navigate · : resources · / filter · ? help · q quit\n")
	}
	return output.String()
}

func (model Model) renderWorkflows(output *strings.Builder, width int) {
	items := model.filteredItems()
	start, end := model.visibleRange(len(items), strings.Count(output.String(), "\n"))
	for index := start; index < end; index++ {
		item := items[index]
		renderRow(output, width, index == model.cursor, statusText(item), item.Namespace, item.Name, item.CreationTimestamp)
	}
	if len(items) == 0 && model.connectionState != workflows.StateLoading {
		output.WriteString("  No active workflows match the current scope.\n")
	}
}

func (model Model) renderResources(output *strings.Builder, width int) {
	items := model.filteredResourceItems()
	start, end := model.visibleRange(len(items), strings.Count(output.String(), "\n"))
	for index := start; index < end; index++ {
		item := items[index]
		renderRow(output, width, index == model.cursor, genericStatusText(item.Status), item.Namespace, item.Name, item.CreationTimestamp)
	}
	if len(items) == 0 && model.resourceState != "loading" && model.resourceError == nil {
		output.WriteString("  No resources match the current scope.\n")
	}
}

func (model Model) visibleRange(length, usedLines int) (int, int) {
	visibleRows := model.height - usedLines - 3
	if visibleRows < 1 || model.height == 0 {
		visibleRows = length
	}
	start := 0
	if model.cursor >= visibleRows {
		start = model.cursor - visibleRows + 1
	}
	return start, min(length, start+visibleRows)
}

func renderRow(output *strings.Builder, width int, selected bool, status, namespace, name string, created time.Time) {
	cursor := " "
	if selected {
		cursor = ">"
	}
	if namespace == "" {
		namespace = "—"
	}
	line := fmt.Sprintf("%s %-13s %-19s %-29s %s",
		cursor,
		ui.Truncate(ui.Sanitize(status), 13),
		ui.Truncate(ui.Sanitize(namespace), 19),
		ui.Truncate(ui.Sanitize(name), 29),
		formatAge(created),
	)
	output.WriteString(ui.Truncate(line, width))
	output.WriteByte('\n')
}

func (model Model) currentState() string {
	if model.resourceDescriptor.Kind == resources.Workflows {
		return string(model.connectionState)
	}
	return model.resourceState
}

func (model Model) currentErrors() map[string]string {
	if model.resourceDescriptor.Kind == resources.Workflows {
		return model.partialErrors
	}
	return model.resourceErrors
}

func (model Model) currentError() error {
	if model.resourceDescriptor.Kind == resources.Workflows {
		return model.fatalError
	}
	return model.resourceError
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

func genericStatusText(status string) string {
	switch strings.ToLower(status) {
	case "running", "active", "ready", "deployed":
		return "▶ " + status
	case "pending", "unknown":
		return "… " + status
	case "suspended":
		return "Ⅱ Suspended"
	case "failed", "error":
		return "! " + status
	case "succeeded":
		return "✓ Succeeded"
	default:
		return "? " + status
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
	if model.resourceDescriptor.Kind != resources.Workflows {
		return false
	}
	return model.connectionState == workflows.StateFresh || model.connectionState == workflows.StatePartial
}

var _ tea.Model = Model{}
