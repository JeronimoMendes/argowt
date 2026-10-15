package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"argowt/internal/argourl"
	applogs "argowt/internal/logs"
	"argowt/internal/notify"
	"argowt/internal/resources"
	"argowt/internal/workflows"
)

func TestModelIgnoresObsoleteGeneration(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 4, Context: "current"})
	updated, _ := model.Update(snapshotMessage{
		open: true,
		snapshot: workflows.Snapshot{
			Generation: 3,
			State:      workflows.StateFresh,
			Workflows:  []workflows.Summary{{Name: "obsolete"}},
		},
	})
	got := updated.(Model)
	if len(got.items) != 0 || got.connectionState != workflows.StateLoading {
		t.Fatalf("obsolete snapshot changed model: %+v", got)
	}
}

func TestMutationSafetyFollowsFreshness(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	if model.MutationAllowed() {
		t.Fatal("mutations must be disabled while loading")
	}

	updated, _ := model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{
		Generation: 1,
		State:      workflows.StateFresh,
	}})
	model = updated.(Model)
	if !model.MutationAllowed() {
		t.Fatal("mutations should be available for fresh data")
	}

	updated, _ = model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{
		Generation: 1,
		State:      workflows.StateStale,
	}})
	model = updated.(Model)
	if model.MutationAllowed() {
		t.Fatal("mutations must be disabled for stale data")
	}
}

func TestFilteringAndNavigation(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	model.items = []workflows.Summary{
		{Namespace: "argo", Name: "daily-payment", Phase: "Running"},
		{Namespace: "other", Name: "cleanup", Phase: "Pending"},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	for _, character := range "pay" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(Model)
	}
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "daily-payment" {
		t.Fatalf("filtered items = %+v", got)
	}
}

func TestInvalidFilterKeepsPreviousValidExpression(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	model.items = []workflows.Summary{
		{Name: "running", Phase: "Running"},
		{Name: "failed", Phase: "Failed"},
	}
	model.query = "status=Running"
	model.updateFilter()
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "running" {
		t.Fatalf("valid filter result = %+v", got)
	}

	model.query = "status="
	model.updateFilter()
	if model.filterError == nil {
		t.Fatal("invalid filter should report an error")
	}
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "running" {
		t.Fatalf("previous filter was not retained: %+v", got)
	}
}

type fakeResourceRepository struct {
	requests chan resources.Request
}

type fakeNotificationManager struct {
	watched     map[string]bool
	transitions map[string][]string
	desktop     map[string]bool
	events      chan notify.Event
	warnings    chan error
}

func newFakeNotificationManager() *fakeNotificationManager {
	return &fakeNotificationManager{
		watched: make(map[string]bool), transitions: make(map[string][]string), desktop: make(map[string]bool),
		events: make(chan notify.Event, 1), warnings: make(chan error, 1),
	}
}

func (manager *fakeNotificationManager) Events() <-chan notify.Event { return manager.events }
func (manager *fakeNotificationManager) Warnings() <-chan error      { return manager.warnings }
func (manager *fakeNotificationManager) IsWatched(uid string) bool   { return manager.watched[uid] }
func (manager *fakeNotificationManager) Toggle(summary workflows.Summary) (bool, error) {
	manager.watched[summary.UID] = !manager.watched[summary.UID]
	if manager.watched[summary.UID] {
		manager.transitions[summary.UID] = append([]string(nil), notify.DefaultTransitions...)
	}
	return manager.watched[summary.UID], nil
}
func (manager *fakeNotificationManager) Settings(uid string) ([]string, bool, bool) {
	if !manager.watched[uid] {
		return nil, false, false
	}
	return append([]string(nil), manager.transitions[uid]...), manager.desktop[uid], true
}
func (manager *fakeNotificationManager) SetTransitions(uid string, transitions []string, desktop bool) error {
	manager.transitions[uid] = append([]string(nil), transitions...)
	manager.desktop[uid] = desktop
	return nil
}

type fakeLogService struct {
	pod      applogs.Pod
	events   chan applogs.Event
	requests chan applogs.Request
}

func (service *fakeLogService) Discover(context.Context, string, string, string, string) (applogs.Pod, error) {
	return service.pod, nil
}

func (service *fakeLogService) Stream(_ context.Context, request applogs.Request) <-chan applogs.Event {
	service.requests <- request
	return service.events
}

type fakeSubmissionService struct {
	form    workflows.SubmissionForm
	request workflows.SubmissionRequest
	result  workflows.SubmissionResult
}

func (service *fakeSubmissionService) Prepare(_ context.Context, source workflows.SubmissionSource, destination string) (workflows.SubmissionForm, error) {
	form := service.form
	form.Source = source
	form.DestinationNamespace = destination
	return form, nil
}

func (service *fakeSubmissionService) Submit(_ context.Context, request workflows.SubmissionRequest) (workflows.SubmissionResult, error) {
	service.request = request
	return service.result, nil
}

type fakeExecutionRepository struct {
	updates   chan workflows.ExecutionUpdate
	namespace string
	name      string
	uid       string
}

func (repository *fakeExecutionRepository) StartExecution(_ context.Context, generation uint64, namespace, name, uid string) <-chan workflows.ExecutionUpdate {
	repository.namespace = namespace
	repository.name = name
	repository.uid = uid
	return repository.updates
}

func (repository *fakeResourceRepository) Discover(context.Context) (map[resources.Kind]bool, error) {
	return map[resources.Kind]bool{resources.Workflows: true, resources.WorkflowTemplates: true}, nil
}

func (repository *fakeResourceRepository) Start(_ context.Context, request resources.Request) <-chan resources.Snapshot {
	repository.requests <- request
	return make(chan resources.Snapshot)
}

func (repository *fakeResourceRepository) Get(_ context.Context, descriptor resources.Descriptor, namespace, name, uid string) (resources.Detail, error) {
	return resources.Detail{
		Summary: resources.Summary{
			Kind:      descriptor.Kind,
			Namespace: namespace,
			Name:      name,
			UID:       uid,
			Status:    "Ready",
		},
		Labels:     map[string]string{"team": "platform"},
		Conditions: []resources.Condition{{Type: "Ready", Status: "True"}},
		YAML:       "apiVersion: argoproj.io/v1alpha1\nkind: Workflow\n",
	}, nil
}

func TestCommandPaletteSwitchesResource(t *testing.T) {
	repository := &fakeResourceRepository{requests: make(chan resources.Request, 1)}
	model := NewModel(ModelOptions{
		Generation:         1,
		RootContext:        context.Background(),
		ResourceRepository: repository,
		CurrentNamespace:   "argo",
		InstanceID:         "controller-a",
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	for _, character := range "wft" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.resourceDescriptor.Kind != resources.WorkflowTemplates {
		t.Fatalf("selected resource = %s", model.resourceDescriptor.Kind)
	}
	request := <-repository.requests
	if request.Descriptor.Kind != resources.WorkflowTemplates || request.CurrentNamespace != "argo" || request.InstanceID != "controller-a" {
		t.Fatalf("unexpected request: %+v", request)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	<-repository.requests
	if model.resourceDescriptor.Kind != resources.ClusterWorkflowTemplates {
		t.Fatalf("Tab resource = %s", model.resourceDescriptor.Kind)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	model = updated.(Model)
	if model.resourceDescriptor.Kind != resources.WorkflowTemplates {
		t.Fatalf("Shift+Tab resource = %s", model.resourceDescriptor.Kind)
	}
}

func TestWorkflowNotificationToggleAndConfiguration(t *testing.T) {
	notifications := newFakeNotificationManager()
	model := NewModel(ModelOptions{Generation: 1, NotificationManager: notifications})
	model.connectionState = workflows.StateFresh
	model.items = []workflows.Summary{{Namespace: "argo", Name: "example", UID: "uid-1", Phase: "Running"}}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if !notifications.IsWatched("uid-1") {
		t.Fatal("n should enable a UID-bound notification watch")
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	model = updated.(Model)
	if !model.notificationConfig {
		t.Fatal("N should open notification preferences")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.notificationConfig || !notifications.desktop["uid-1"] {
		t.Fatalf("notification preferences were not saved: %+v", model)
	}
	foundRunning := false
	for _, transition := range notifications.transitions["uid-1"] {
		foundRunning = foundRunning || transition == "Running"
	}
	if !foundRunning {
		t.Fatalf("transitions = %v", notifications.transitions["uid-1"])
	}
}

func TestEnterOpensLiveWorkflowExecutionAndPreservesSelection(t *testing.T) {
	executionRepository := &fakeExecutionRepository{updates: make(chan workflows.ExecutionUpdate, 2)}
	model := NewModel(ModelOptions{
		Generation:          1,
		RootContext:         context.Background(),
		ExecutionRepository: executionRepository,
	})
	model.connectionState = workflows.StateFresh
	model.items = []workflows.Summary{{Namespace: "argo", Name: "example", UID: "uid-1", Phase: "Running"}}

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.executionVisible || command == nil {
		t.Fatalf("execution did not open: %+v", model)
	}
	executionRepository.updates <- workflows.ExecutionUpdate{
		Generation: model.executionGeneration,
		Execution: workflows.Execution{
			Namespace: "argo",
			Name:      "example",
			Phase:     "Running",
			Roots:     []string{"root"},
			Nodes: map[string]workflows.Node{
				"root":  {ID: "root", DisplayName: "root", Phase: "Running", Children: []string{"child"}},
				"child": {ID: "child", DisplayName: "child", Phase: "Pending", Parents: []string{"root"}},
			},
		},
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.executionSelectedID != "root" || !strings.Contains(model.View(), "child") {
		t.Fatalf("execution view not rendered: selected=%q view=%q", model.executionSelectedID, model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.executionSelectedID != "child" {
		t.Fatalf("selected node = %q", model.executionSelectedID)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if model.executionMode != "dag" || model.executionSelectedID != "child" {
		t.Fatalf("selection was not preserved across mode switch: mode=%s selected=%s", model.executionMode, model.executionSelectedID)
	}
	if executionRepository.namespace != "argo" || executionRepository.name != "example" || executionRepository.uid != "uid-1" {
		t.Fatalf("unexpected execution identity: %+v", executionRepository)
	}
}

func TestOpenTargetSelectsURLNode(t *testing.T) {
	executionRepository := &fakeExecutionRepository{updates: make(chan workflows.ExecutionUpdate, 1)}
	model := NewModel(ModelOptions{
		Generation:          1,
		RootContext:         context.Background(),
		ExecutionRepository: executionRepository,
		Open:                argourl.Target{Kind: resources.Workflows, Namespace: "argo", Name: "example", NodeID: "child"},
	})

	updated, command := model.Update(openMessage{})
	model = updated.(Model)
	if !model.executionVisible || executionRepository.name != "example" || executionRepository.uid != "" {
		t.Fatalf("execution did not open: %+v", executionRepository)
	}
	executionRepository.updates <- workflows.ExecutionUpdate{
		Generation: model.executionGeneration,
		Execution: workflows.Execution{
			Roots: []string{"root"},
			Nodes: map[string]workflows.Node{
				"root":  {ID: "root", Children: []string{"child"}},
				"child": {ID: "child", Parents: []string{"root"}},
			},
		},
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.executionSelectedID != "child" || model.executionCursor != 1 {
		t.Fatalf("selected node = %q cursor = %d", model.executionSelectedID, model.executionCursor)
	}
}

func TestOpenLiveNodeLogsAndSwitchOptions(t *testing.T) {
	logService := &fakeLogService{
		pod: applogs.Pod{
			Namespace: "argo", Name: "example-task", Phase: "Running",
			Containers: []applogs.Container{{Name: "main", Kind: "main", HasPrevious: true}, {Name: "wait", Kind: "wait"}},
		},
		events: make(chan applogs.Event, 2), requests: make(chan applogs.Request, 3),
	}
	model := NewModel(ModelOptions{Generation: 1, RootContext: context.Background(), LogService: logService})
	model.executionVisible = true
	model.executionGeneration = 1
	model.executionSelectedID = "node-1"
	model.execution = &workflows.Execution{
		Namespace: "argo", Name: "example", UID: "uid-1", Phase: "Running",
		Nodes: map[string]workflows.Node{"node-1": {ID: "node-1", Name: "example.task", DisplayName: "task", Type: "Pod", Phase: "Running"}},
		Roots: []string{"node-1"},
	}

	updated, discover := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.logVisible || discover == nil {
		t.Fatal("log view did not open")
	}
	updated, stream := model.Update(discover())
	model = updated.(Model)
	if stream == nil {
		t.Fatal("log stream did not start")
	}
	request := <-logService.requests
	if request.Pod != "example-task" || request.Container != "main" || !request.Follow {
		t.Fatalf("request = %+v", request)
	}
	logService.events <- applogs.Event{Lines: []string{"starting task", "task complete"}}
	updated, _ = model.Update(stream())
	model = updated.(Model)
	if view := model.View(); !strings.Contains(view, "task complete") || !strings.Contains(view, "example-task") {
		t.Fatalf("log view = %q", view)
	}

	updated, restart := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)
	if restart == nil {
		t.Fatal("previous-log toggle did not restart the stream")
	}
	request = <-logService.requests
	if !request.Previous || request.Follow {
		t.Fatalf("previous request = %+v", request)
	}

	// Back in the workflow view, Tab switches the node panel to live logs.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	updated, discover = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	updated, stream = model.Update(discover())
	model = updated.(Model)
	<-logService.requests
	logService.events <- applogs.Event{Lines: []string{"preview line"}}
	updated, _ = model.Update(stream())
	model = updated.(Model)
	if view := model.View(); !strings.Contains(view, "Live logs") || !strings.Contains(view, "preview line") {
		t.Fatalf("node preview not rendered: %q", view)
	}
	model.execution.Nodes["node-1"] = workflows.Node{ID: "node-1", DisplayName: "steps", Type: "Steps", Phase: "Running"}
	if view := model.View(); !strings.Contains(view, "steps is a Steps node; it has no logs") {
		t.Fatalf("no-logs warning not rendered: %q", view)
	}

	// The palette opens over the workflow view with its commands.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	if view := model.View(); !strings.Contains(view, "Command palette") || !strings.Contains(view, "Open selected node logs") {
		t.Fatalf("palette not rendered in workflow view: %q", view)
	}
}

func TestSubmitWorkflowFromTemplateFormAndConfirmation(t *testing.T) {
	service := &fakeSubmissionService{
		form:   workflows.SubmissionForm{Parameters: []workflows.SubmissionParameter{{Name: "message", Required: true, Description: "message to process"}}},
		result: workflows.SubmissionResult{Namespace: "argo", Name: "payments-abc"},
	}
	model := NewModel(ModelOptions{Generation: 1, RootContext: context.Background(), SubmissionService: service, CurrentNamespace: "argo"})
	descriptor, _ := resources.Lookup("wft")
	model.resourceDescriptor = descriptor
	model.resourceItems = []resources.Summary{{Kind: resources.WorkflowTemplates, Namespace: "argo", Name: "payments"}}
	model.query = "name=payments"
	model.updateFilter()

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if !model.submissionVisible || command == nil {
		t.Fatal("submission form did not start loading")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	for _, character := range "hello" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = updated.(Model)
	if model.submissionStage != "confirm" || !strings.Contains(model.View(), "context:") {
		t.Fatalf("confirmation view = %q", model.View())
	}
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("confirmation did not submit")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.submissionVisible || model.resourceDescriptor.Kind != resources.Workflows || service.request.Parameters["message"] != "hello" {
		t.Fatalf("submission did not jump to workflows: visible=%t resource=%s request=%+v", model.submissionVisible, model.resourceDescriptor.Kind, service.request)
	}
	if model.pendingWorkflowName != "payments-abc" || !strings.Contains(model.resourceNotice, "payments-abc") {
		t.Fatalf("submitted workflow focus/notice = %q, %q", model.pendingWorkflowName, model.resourceNotice)
	}

	updated, _ = model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{
		Generation: 1, State: workflows.StateFresh,
		Workflows: []workflows.Summary{{Namespace: "argo", Name: "other", Phase: "Running"}, {Namespace: "argo", Name: "payments-abc", Phase: "Pending"}},
	}})
	model = updated.(Model)
	if items := model.filteredItems(); model.cursor >= len(items) || items[model.cursor].Name != "payments-abc" {
		t.Fatalf("submitted workflow not selected; template filter %q still active?", model.query)
	}
}

func TestEnterOpensMetadataAndYAMLDetail(t *testing.T) {
	repository := &fakeResourceRepository{requests: make(chan resources.Request, 1)}
	model := NewModel(ModelOptions{Generation: 1, RootContext: context.Background(), ResourceRepository: repository})
	descriptor, _ := resources.Lookup("wft")
	model.resourceDescriptor = descriptor
	model.resourceState = "fresh"
	model.resourceItems = []resources.Summary{{Kind: resources.WorkflowTemplates, Namespace: "argo", Name: "example", UID: "uid-1", Status: "Ready"}}

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.detailVisible || !model.detailLoading || command == nil {
		t.Fatalf("detail did not start loading: %+v", model)
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	metadata := model.View()
	if !strings.Contains(metadata, "team=platform") || !strings.Contains(metadata, "Ready=True") {
		t.Fatalf("metadata view = %q", metadata)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if yamlView := model.View(); !strings.Contains(yamlView, "apiVersion: argoproj.io/v1alpha1") {
		t.Fatalf("YAML view = %q", yamlView)
	}
}

func TestDiscoveryHidesUnavailableEventResources(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	updated, _ := model.Update(discoveryMessage{available: map[resources.Kind]bool{
		resources.Workflows:         true,
		resources.WorkflowTemplates: true,
		resources.EventSources:      false,
	}})
	model = updated.(Model)
	for _, command := range resources.Commands(model.availableResources, "") {
		if command.Kind == resources.EventSources {
			t.Fatal("missing EventSource CRD should be hidden")
		}
	}
}

func TestViewSanitizesAPIValues(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1, Context: "safe\x1b[31m"})
	model.connectionState = workflows.StateFresh
	model.items = []workflows.Summary{{Namespace: "argo", Name: "bad\x1b[2Jname", Phase: "Running"}}
	view := model.View()
	if strings.ContainsRune(view, '\x1b') {
		t.Fatalf("view contains an escape character: %q", view)
	}
	if !strings.Contains(view, "▶ Running") {
		t.Fatalf("view does not include textual status: %q", view)
	}

	updated, _ := model.Update(updateMessage{tag: "v1.2.0"})
	if view := updated.View(); !strings.Contains(view, "argowt v1.2.0 available") {
		t.Fatalf("view does not include update notice: %q", view)
	}
}

func TestMouseDragCopiesSelectedText(t *testing.T) {
	copied := make(chan string, 1)
	model := NewModel(ModelOptions{Generation: 1, Clipboard: func(text string) error {
		copied <- text
		return nil
	}})
	model.width, model.height = 100, 30

	updated, _ := model.Update(tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	updated, _ = updated.Update(tea.MouseMsg{X: 5, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	if view := updated.View(); !strings.Contains(view, "\x1b[7margowt\x1b[27m") {
		t.Fatalf("selection not highlighted: %q", view)
	}

	updated, command := updated.Update(tea.MouseMsg{X: 5, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if command == nil {
		t.Fatal("release should copy the selection")
	}
	command()
	if text := <-copied; text != "argowt" {
		t.Fatalf("copied = %q", text)
	}
}
