// Package app owns the top-level Bubble Tea model and process lifecycle.
package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JeronimoMendes/argowt/internal/argourl"
	"github.com/JeronimoMendes/argowt/internal/clipboard"
	"github.com/JeronimoMendes/argowt/internal/config"
	"github.com/JeronimoMendes/argowt/internal/filter"
	"github.com/JeronimoMendes/argowt/internal/kube"
	applogs "github.com/JeronimoMendes/argowt/internal/logs"
	"github.com/JeronimoMendes/argowt/internal/notify"
	"github.com/JeronimoMendes/argowt/internal/release"
	"github.com/JeronimoMendes/argowt/internal/resources"
	"github.com/JeronimoMendes/argowt/internal/ui"
	"github.com/JeronimoMendes/argowt/internal/version"
	"github.com/JeronimoMendes/argowt/internal/workflows"
)

var theme = ui.DefaultTheme()

// maxWarningScopes caps the scopes named in one grouped warning line.
const maxWarningScopes = 3

// updateCheckTimeout bounds the startup release check so offline runs stay quiet.
const updateCheckTimeout = 10 * time.Second

// Options contains process-level settings established by the CLI. It contains
// no Kubernetes clients, credentials, or persisted resource data.
type Options struct {
	Kubeconfig    string
	Context       string
	Namespaces    []string
	AllNamespaces bool
	InstanceID    string
	Paths         config.Paths
	Open          argourl.Target
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
	workflowSource := workflows.ArgoSource{Client: connection.Argo}
	repository := workflows.NewRepository(workflowSource, namespaceLister)
	resourceRepository := resources.NewRepository(connection.Dynamic, connection.Kubernetes.Discovery(), namespaceLister)
	submissionService := workflows.NewSubmissionService(connection.Argo)
	operationService := workflows.NewOperationService(connection.Argo, connection.Kubernetes)
	accessReviewer := accessReviewer{kube.AccessReviewer{Client: connection.Kubernetes}}
	logService := applogs.NewService(connection.Kubernetes)
	watchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	workflowContext, workflowCancel := context.WithCancel(watchContext)
	state, stateErr := config.LoadState(options.Paths.StateFile)
	if stateErr != nil {
		state = config.NewState()
	}
	notificationManager := notify.NewManager(workflowSource, connection.Context, options.Paths.StateFile, state, notify.NewDesktop())
	notificationManager.Start(watchContext)
	defer notificationManager.Close()
	updates := repository.Start(workflowContext, workflows.Request{
		Generation:       generation,
		AllNamespaces:    options.AllNamespaces,
		Namespaces:       options.Namespaces,
		CurrentNamespace: connection.DefaultNamespace,
		InstanceID:       options.InstanceID,
		ActiveOnly:       true,
	})
	model := NewModel(ModelOptions{
		Generation:          generation,
		Context:             connection.Context,
		Namespace:           scopeLabel(options, connection.DefaultNamespace),
		InstanceID:          options.InstanceID,
		Updates:             updates,
		Cancel:              cancel,
		WorkflowRepository:  repository,
		WorkflowCancel:      workflowCancel,
		RootContext:         watchContext,
		ResourceRepository:  resourceRepository,
		ExecutionRepository: repository,
		AllNamespaces:       options.AllNamespaces,
		Namespaces:          options.Namespaces,
		CurrentNamespace:    connection.DefaultNamespace,
		NotificationManager: notificationManager,
		SubmissionService:   submissionService,
		OperationService:    operationService,
		CheckAccess: func(ctx context.Context, namespace string) map[workflows.Operation]bool {
			return workflows.CheckOperations(ctx, accessReviewer, namespace)
		},
		LogService:     logService,
		StartupWarning: stateErr,
		Clipboard:      clipboard.Copy,
		Open:           options.Open,
		CheckUpdate:    checkUpdate,
	})

	_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	if err != nil {
		return fmt.Errorf("run terminal UI: %w", err)
	}
	return nil
}

// accessReviewer adapts SelfSubjectAccessReviews to workflow permissions.
type accessReviewer struct {
	reviewer kube.AccessReviewer
}

// Allowed adapts a workflow permission to the Kubernetes access reviewer.
func (adapter accessReviewer) Allowed(ctx context.Context, namespace string, access workflows.Access) (bool, error) {
	return adapter.reviewer.Allowed(ctx, namespace, access.Group, access.Resource, access.Verb)
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

// checkUpdate returns a newer release tag, or "" when up to date, offline,
// or running a local dev build.
func checkUpdate(ctx context.Context) string {
	if version.Version == version.Dev {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	tag, err := release.Latest(ctx)
	if err != nil || !release.Newer(version.Version, tag) {
		return ""
	}
	return tag
}

// WorkflowListRepository restarts the workflow list stream, e.g. to switch
// between active and completed workflows.
type WorkflowListRepository interface {
	Start(context.Context, workflows.Request) <-chan workflows.Snapshot
}

type WorkflowExecutionRepository interface {
	StartExecution(context.Context, uint64, string, string, string) <-chan workflows.ExecutionUpdate
}

type NotificationManager interface {
	Events() <-chan notify.Event
	Warnings() <-chan error
	IsWatched(string) bool
	Toggle(workflows.Summary) (bool, error)
	Settings(string) ([]string, bool, bool)
	SetTransitions(string, []string, bool) error
}

type LogService interface {
	Discover(context.Context, string, string, string, string) (applogs.Pod, error)
	Stream(context.Context, applogs.Request) <-chan applogs.Event
}

type TemplateSubmissionService interface {
	Prepare(context.Context, workflows.SubmissionSource, string) (workflows.SubmissionForm, error)
	Submit(context.Context, workflows.SubmissionRequest) (workflows.SubmissionResult, error)
}

type ResourceRepository interface {
	Discover(context.Context) (map[resources.Kind]bool, error)
	Start(context.Context, resources.Request) <-chan resources.Snapshot
	Get(context.Context, resources.Descriptor, string, string, string) (resources.Detail, error)
}

type ModelOptions struct {
	Generation          uint64
	Context             string
	Namespace           string
	InstanceID          string
	Updates             <-chan workflows.Snapshot
	Cancel              context.CancelFunc
	WorkflowRepository  WorkflowListRepository
	WorkflowCancel      context.CancelFunc
	RootContext         context.Context
	ResourceRepository  ResourceRepository
	ExecutionRepository WorkflowExecutionRepository
	NotificationManager NotificationManager
	SubmissionService   TemplateSubmissionService
	OperationService    WorkflowOperationService
	CheckAccess         func(context.Context, string) map[workflows.Operation]bool
	LogService          LogService
	Clipboard           func(string) error
	StartupWarning      error
	AllNamespaces       bool
	Namespaces          []string
	CurrentNamespace    string
	Open                argourl.Target
	CheckUpdate         func(context.Context) string
}

type Model struct {
	generation uint64
	context    string
	namespace  string
	instanceID string
	updates    <-chan workflows.Snapshot
	cancel     context.CancelFunc

	// showCompleted lists finished workflows still in the cluster instead of active ones.
	workflowRepository WorkflowListRepository
	workflowCancel     context.CancelFunc
	showCompleted      bool

	rootContext         context.Context
	resourceRepository  ResourceRepository
	executionRepository WorkflowExecutionRepository
	allNamespaces       bool
	namespaces          []string
	currentNamespace    string
	resourceGeneration  uint64
	resourceUpdates     <-chan resources.Snapshot
	resourceCancel      context.CancelFunc
	resourceDescriptor  resources.Descriptor
	resourceItems       []resources.Summary
	resourceState       string
	resourceErrors      map[string]string
	resourceError       error
	availableResources  map[resources.Kind]bool
	resourceNotice      string

	clipboard        func(string) error
	selection        ui.Selection
	selecting        bool
	selectionVisible bool
	detail           *resources.Detail
	detailError      error
	detailLoading    bool
	detailVisible    bool
	detailYAML       bool
	detailScroll     int
	detailUID        string

	executionGeneration uint64
	executionUpdates    <-chan workflows.ExecutionUpdate
	executionCancel     context.CancelFunc
	execution           *workflows.Execution
	executionError      error
	executionStale      bool
	executionVisible    bool
	executionMode       string
	executionCursor     int
	executionSelectedID string
	executionScroll     int
	open                argourl.Target
	checkUpdate         func(context.Context) string
	updateTag           string

	notificationManager  NotificationManager
	notificationEvents   <-chan notify.Event
	notificationWarnings <-chan error
	notifications        []notify.Event
	notificationWarning  error
	notificationConfig   bool
	notificationUID      string
	notificationName     string
	notificationChoices  map[string]bool
	notificationDesktop  bool

	submissionService        TemplateSubmissionService
	submissionVisible        bool
	submissionStage          string
	submissionForm           workflows.SubmissionForm
	submissionValues         map[string]string
	submissionFocus          int
	submissionError          error
	submissionResult         *workflows.SubmissionResult
	pendingWorkflowNamespace string
	pendingWorkflowName      string

	operationService   WorkflowOperationService
	checkAccess        func(context.Context, string) map[workflows.Operation]bool
	permissions        map[string]map[workflows.Operation]bool
	permissionsPending map[string]bool
	actionVisible      bool
	actionStage        string
	actionTarget       workflows.OperationTarget
	actionOperation    workflows.Operation
	actionCursor       int
	actionNotice       string
	actionError        error

	logService          LogService
	logVisible          bool
	logGeneration       uint64
	logNode             workflows.Node
	logPod              applogs.Pod
	logContainer        int
	logPrevious         bool
	logFollow           bool
	logTimestamps       bool
	logWrap             bool
	logBuffer           *applogs.Buffer
	logUpdates          <-chan applogs.Event
	logCancel           context.CancelFunc
	logState            string
	logError            error
	logQuery            string
	logSearching        bool
	logAutoScroll       bool
	logOffset           int
	logSaving           bool
	logSavePath         string
	logNotice           string
	logReconnectAttempt int

	executionLogPanel     bool
	nodePreviewGeneration uint64
	nodePreviewCancel     context.CancelFunc
	nodePreviewID         string
	nodePreviewPod        applogs.Pod
	nodePreviewBuffer     *applogs.Buffer
	nodePreviewUpdates    <-chan applogs.Event
	nodePreviewState      string
	nodePreviewError      error

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
	updates  <-chan workflows.Snapshot
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

type detailMessage struct {
	generation uint64
	descriptor resources.Descriptor
	uid        string
	detail     resources.Detail
	err        error
}

type executionMessage struct {
	update  workflows.ExecutionUpdate
	updates <-chan workflows.ExecutionUpdate
	open    bool
}

type notificationMessage struct {
	event  notify.Event
	events <-chan notify.Event
}

type notificationWarningMessage struct {
	err      error
	warnings <-chan error
}

type submissionFormMessage struct {
	form workflows.SubmissionForm
	err  error
}

type submissionResultMessage struct {
	result workflows.SubmissionResult
	err    error
}

type logDiscoveryMessage struct {
	generation uint64
	pod        applogs.Pod
	err        error
}

type logEventMessage struct {
	generation uint64
	event      applogs.Event
	updates    <-chan applogs.Event
	open       bool
}

type logReconnectMessage struct {
	generation uint64
}

// updateMessage carries a newer release tag found at startup.
type updateMessage struct {
	tag string
}

// openMessage opens the startup URL target once the program runs.
type openMessage struct{}

type nodePreviewDwellMessage struct {
	generation uint64
	nodeID     string
}

type nodePreviewDiscoveryMessage struct {
	generation uint64
	nodeID     string
	pod        applogs.Pod
	err        error
}

type nodePreviewEventMessage struct {
	generation uint64
	nodeID     string
	event      applogs.Event
	updates    <-chan applogs.Event
	open       bool
}

// NewModel initializes the terminal UI state and services from the supplied options.
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
	var notificationEvents <-chan notify.Event
	var notificationWarnings <-chan error
	if options.NotificationManager != nil {
		notificationEvents = options.NotificationManager.Events()
		notificationWarnings = options.NotificationManager.Warnings()
	}
	return Model{
		generation:           options.Generation,
		context:              options.Context,
		namespace:            options.Namespace,
		instanceID:           options.InstanceID,
		updates:              options.Updates,
		cancel:               options.Cancel,
		workflowRepository:   options.WorkflowRepository,
		workflowCancel:       options.WorkflowCancel,
		rootContext:          rootContext,
		resourceRepository:   options.ResourceRepository,
		executionRepository:  options.ExecutionRepository,
		notificationManager:  options.NotificationManager,
		notificationEvents:   notificationEvents,
		notificationWarnings: notificationWarnings,
		notificationWarning:  options.StartupWarning,
		submissionService:    options.SubmissionService,
		operationService:     options.OperationService,
		checkAccess:          options.CheckAccess,
		permissions:          make(map[string]map[workflows.Operation]bool),
		logService:           options.LogService,
		clipboard:            options.Clipboard,
		logFollow:            true,
		logWrap:              true,
		logAutoScroll:        true,
		allNamespaces:        options.AllNamespaces,
		namespaces:           append([]string(nil), options.Namespaces...),
		currentNamespace:     options.CurrentNamespace,
		resourceDescriptor:   workflowDescriptor,
		availableResources:   available,
		connectionState:      workflows.StateLoading,
		resourceState:        "loading",
		executionMode:        "tree",
		open:                 options.Open,
		checkUpdate:          options.CheckUpdate,
	}
}

func (model Model) Init() tea.Cmd {
	commands := []tea.Cmd{waitForSnapshot(model.updates)}
	if model.resourceRepository != nil {
		commands = append(commands, discoverResources(model.rootContext, model.resourceRepository))
	}
	if model.notificationEvents != nil {
		commands = append(commands, waitForNotification(model.notificationEvents))
	}
	if model.notificationWarnings != nil {
		commands = append(commands, waitForNotificationWarning(model.notificationWarnings))
	}
	if model.open.Kind != "" {
		commands = append(commands, func() tea.Msg { return openMessage{} })
	}
	if model.checkUpdate != nil {
		commands = append(commands, findUpdate(model.rootContext, model.checkUpdate))
	}
	return tea.Batch(commands...)
}

// findUpdate checks for a newer release off the UI thread.
func findUpdate(ctx context.Context, check func(context.Context) string) tea.Cmd {
	return func() tea.Msg {
		return updateMessage{tag: check(ctx)}
	}
}

func waitForSnapshot(updates <-chan workflows.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, open := <-updates
		return snapshotMessage{snapshot: snapshot, updates: updates, open: open}
	}
}

func waitForResourceSnapshot(updates <-chan resources.Snapshot) tea.Cmd {
	return func() tea.Msg {
		snapshot, open := <-updates
		return resourceSnapshotMessage{snapshot: snapshot, updates: updates, open: open}
	}
}

func waitForExecution(updates <-chan workflows.ExecutionUpdate) tea.Cmd {
	return func() tea.Msg {
		update, open := <-updates
		return executionMessage{update: update, updates: updates, open: open}
	}
}

func waitForNotification(events <-chan notify.Event) tea.Cmd {
	return func() tea.Msg {
		return notificationMessage{event: <-events, events: events}
	}
}

func waitForNotificationWarning(warnings <-chan error) tea.Cmd {
	return func() tea.Msg {
		return notificationWarningMessage{err: <-warnings, warnings: warnings}
	}
}

func prepareSubmission(ctx context.Context, service TemplateSubmissionService, source workflows.SubmissionSource, destinationNamespace string) tea.Cmd {
	return func() tea.Msg {
		form, err := service.Prepare(ctx, source, destinationNamespace)
		return submissionFormMessage{form: form, err: err}
	}
}

func submitTemplate(ctx context.Context, service TemplateSubmissionService, request workflows.SubmissionRequest) tea.Cmd {
	return func() tea.Msg {
		result, err := service.Submit(ctx, request)
		return submissionResultMessage{result: result, err: err}
	}
}

func discoverNodeLog(ctx context.Context, service LogService, generation uint64, namespace, workflowName, nodeID, nodeName string) tea.Cmd {
	return func() tea.Msg {
		pod, err := service.Discover(ctx, namespace, workflowName, nodeID, nodeName)
		return logDiscoveryMessage{generation: generation, pod: pod, err: err}
	}
}

func waitForLogEvent(generation uint64, updates <-chan applogs.Event) tea.Cmd {
	return func() tea.Msg {
		event, open := <-updates
		return logEventMessage{generation: generation, event: event, updates: updates, open: open}
	}
}

func reconnectLogAfter(generation uint64, attempt int) tea.Cmd {
	if attempt > 5 {
		attempt = 5
	}
	delay := time.Second * time.Duration(1<<attempt)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return logReconnectMessage{generation: generation} })
}

func discoverResources(ctx context.Context, repository ResourceRepository) tea.Cmd {
	return func() tea.Msg {
		available, err := repository.Discover(ctx)
		return discoveryMessage{available: available, err: err}
	}
}

func loadDetail(ctx context.Context, repository ResourceRepository, generation uint64, descriptor resources.Descriptor, namespace, name, uid string) tea.Cmd {
	return func() tea.Msg {
		detail, err := repository.Get(ctx, descriptor, namespace, name, uid)
		return detailMessage{generation: generation, descriptor: descriptor, uid: uid, detail: detail, err: err}
	}
}

// Update applies asynchronous results and user input to the model and schedules follow-up commands.
func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case snapshotMessage:
		if !message.open {
			return model, nil
		}
		// A stream replaced by toggleCompleted is cancelled and closes on its own.
		if message.updates != nil && message.updates != model.updates {
			return model, nil
		}
		if message.snapshot.Generation == model.generation {
			model.connectionState = message.snapshot.State
			model.items = message.snapshot.Workflows
			model.partialErrors = message.snapshot.PartialErrors
			model.fatalError = message.snapshot.Err
			model.clampCursor()
			if model.pendingWorkflowName != "" {
				for index, workflow := range model.filteredItems() {
					if workflow.Namespace == model.pendingWorkflowNamespace && workflow.Name == model.pendingWorkflowName {
						model.cursor = index
						model.pendingWorkflowName = ""
						model.pendingWorkflowNamespace = ""
						break
					}
				}
			}
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
	case detailMessage:
		if message.generation == model.currentResourceGeneration() && message.descriptor.Kind == model.resourceDescriptor.Kind && message.uid == model.detailUID {
			model.detailLoading = false
			model.detailError = message.err
			if message.err == nil {
				model.detail = &message.detail
			}
		}
		return model, nil
	case executionMessage:
		if !message.open {
			return model, nil
		}
		var previewCommand tea.Cmd
		if message.update.Generation == model.executionGeneration {
			model.executionStale = message.update.Stale
			model.executionError = message.update.Err
			if !message.update.Stale && message.update.Err == nil {
				selectedBefore := model.executionSelectedID
				execution := message.update.Execution
				model.execution = &execution
				model.reconcileExecutionSelection()
				if model.executionSelectedID != selectedBefore || model.nodePreviewID == "" {
					previewCommand = model.scheduleNodePreview()
				}
			}
		}
		return model, tea.Batch(waitForExecution(message.updates), previewCommand)
	case openMessage:
		return model.openTarget(model.open)
	case updateMessage:
		model.updateTag = message.tag
		return model, nil
	case notificationMessage:
		model.notifications = append(model.notifications, message.event)
		if len(model.notifications) > 5 {
			model.notifications = model.notifications[len(model.notifications)-5:]
		}
		return model, waitForNotification(message.events)
	case notificationWarningMessage:
		model.notificationWarning = message.err
		return model, waitForNotificationWarning(message.warnings)
	case permissionMessage:
		delete(model.permissionsPending, message.namespace)
		model.permissions[message.namespace] = message.allowed
		return model, nil
	case operationResultMessage:
		return model.handleOperationResult(message)
	case submissionFormMessage:
		if !model.submissionVisible {
			return model, nil
		}
		model.submissionError = message.err
		if message.err != nil && model.submissionForm.Source.Kind == workflows.SourceFile {
			// Let the user fix the path rather than start over.
			model.submissionStage = "path"
			return model, nil
		}
		if message.err == nil {
			model.submissionForm = message.form
			model.submissionValues = make(map[string]string, len(message.form.Parameters))
			for _, parameter := range message.form.Parameters {
				model.submissionValues[parameter.Name] = parameter.Default
			}
			model.submissionStage = "edit"
		} else {
			model.submissionStage = "error"
		}
		return model, nil
	case submissionResultMessage:
		model.submissionError = message.err
		if message.err == nil {
			result := message.result
			model.clearSubmission()
			if model.executionVisible {
				model.closeExecution()
			}
			model.pendingWorkflowNamespace = result.Namespace
			model.pendingWorkflowName = result.Name
			workflowDescriptor, _ := resources.Lookup("workflows")
			switched, command := model.switchResource(workflowDescriptor)
			model = switched.(Model)

			// The completed list would hide the new workflow.
			if model.showCompleted {
				toggled, toggleCommand := model.toggleCompleted()
				model = toggled.(Model)
				command = tea.Batch(command, toggleCommand)
			}

			// A template filter (e.g. name=train-hera) would hide the new workflow.
			model.query = ""
			model.updateFilter()
			model.resourceNotice = "Submitted workflow " + result.Namespace + "/" + result.Name
			return model, command
		}
		model.submissionStage = "confirm"
		return model, nil
	case logDiscoveryMessage:
		if message.generation != model.logGeneration || !model.logVisible {
			return model, nil
		}
		model.logError = message.err
		if message.err != nil {
			model.logState = "unavailable"
			return model, nil
		}
		model.logPod = message.pod
		if len(message.pod.Containers) == 0 {
			model.logState = "unavailable"
			model.logError = fmt.Errorf("pod %s has no loggable containers", message.pod.Name)
			return model, nil
		}
		model.logContainer = 0
		return model.startLogStream(true)
	case logEventMessage:
		if !message.open {
			return model, nil
		}
		if message.generation != model.logGeneration || !model.logVisible {
			return model, waitForLogEvent(message.generation, message.updates)
		}
		if len(message.event.Lines) > 0 {
			model.logBuffer.Append(message.event.Lines...)
			model.logError = nil
			model.logState = "streaming"
			model.logReconnectAttempt = 0
			if model.logAutoScroll {
				model.logOffset = 0
			}
		}
		if message.event.Err != nil {
			model.logError = message.event.Err
			model.logState = "reconnecting"
			attempt := model.logReconnectAttempt
			model.logReconnectAttempt++
			return model, reconnectLogAfter(model.logGeneration, attempt)
		}
		if message.event.Ended {
			model.logState = "ended"
			return model, nil
		}
		return model, waitForLogEvent(message.generation, message.updates)
	case logReconnectMessage:
		if message.generation != model.logGeneration || !model.logVisible {
			return model, nil
		}
		return model.startLogStream(false)
	case nodePreviewDwellMessage:
		if message.generation != model.nodePreviewGeneration || message.nodeID != model.executionSelectedID || !model.executionVisible || model.logVisible {
			return model, nil
		}
		return model, model.startNodePreviewDiscovery(message.generation, message.nodeID)
	case nodePreviewDiscoveryMessage:
		if message.generation != model.nodePreviewGeneration || message.nodeID != model.executionSelectedID || !model.executionVisible || model.logVisible {
			return model, nil
		}
		model.nodePreviewError = message.err
		if message.err != nil || len(message.pod.Containers) == 0 {
			model.nodePreviewState = "unavailable"
			return model, nil
		}
		model.nodePreviewPod = message.pod
		return model, model.startNodePreviewStream(message.generation, message.nodeID)
	case nodePreviewEventMessage:
		if !message.open {
			return model, nil
		}
		if message.generation != model.nodePreviewGeneration || message.nodeID != model.executionSelectedID || !model.executionVisible || model.logVisible {
			return model, waitForNodePreviewEvent(message.generation, message.nodeID, message.updates)
		}
		if len(message.event.Lines) > 0 {
			model.nodePreviewBuffer.Append(message.event.Lines...)
			model.nodePreviewState = "live"
		}
		if message.event.Err != nil {
			model.nodePreviewError = message.event.Err
			model.nodePreviewState = "unavailable"
			return model, nil
		}
		if message.event.Ended {
			model.nodePreviewState = "ended"
			return model, nil
		}
		return model, waitForNodePreviewEvent(message.generation, message.nodeID, message.updates)
	case tea.WindowSizeMsg:
		model.selectionVisible = false
		model.width = message.Width
		model.height = message.Height
		return model, nil
	case tea.KeyMsg:
		model.selectionVisible = false
		return model.updateKey(message)
	case tea.MouseMsg:
		selected, command, consumed := model.updateSelection(message)
		if consumed {
			return selected, command
		}
		return selected.updateMouse(message)
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
		top, footer := model.listChrome(model.viewWidth())
		row := mouse.Y - lineCount(top) - 1 - listHeaderRows
		itemCount := model.itemCount()
		start, _ := window(itemCount, model.cursor, listCapacity(model.bodyRows(top, model.footer(footer))))
		index := start + row
		if row >= 0 && index >= 0 && index < itemCount {
			model.cursor = index
		}
	}
	return model, nil
}

// updateKey routes keyboard input to the active dialog, view, or resource list.
func (model Model) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if model.palette {
		return model.updatePaletteKey(key)
	}
	if model.logVisible {
		return model.updateLogKey(key)
	}
	if model.submissionVisible {
		return model.updateSubmissionKey(key)
	}
	if model.actionVisible {
		return model.updateActionKey(key)
	}
	if model.notificationConfig {
		return model.updateNotificationConfigKey(key)
	}
	if model.executionVisible {
		return model.updateExecutionKey(key)
	}
	if model.detailVisible {
		return model.updateDetailKey(key)
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
			if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
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
	case "tab":
		return model.cycleResource(1)
	case "shift+tab":
		return model.cycleResource(-1)
	case "esc":
		model.query = ""
		model.updateFilter()
		model.cursor = 0
	case "?":
		model.showHelp = !model.showHelp
	case "enter":
		return model.openSelectedDetail()
	case "n":
		model.toggleSelectedNotification()
	case "N":
		model.openNotificationConfig()
	case "s":
		return model.openTemplateSubmission()
	case "S":
		return model.openLocalSubmission()
	case "a":
		return model.openActions()
	case "c":
		return model.toggleCompleted()
	}
	return model, nil
}

// toggleCompleted switches the workflow list between active workflows and
// completed workflows still in the cluster, restarting the list stream.
func (model Model) toggleCompleted() (tea.Model, tea.Cmd) {
	if model.resourceDescriptor.Kind != resources.Workflows || model.workflowRepository == nil {
		return model, nil
	}
	if model.workflowCancel != nil {
		model.workflowCancel()
	}
	model.showCompleted = !model.showCompleted
	model.generation++
	model.items = nil
	model.partialErrors = nil
	model.fatalError = nil
	model.connectionState = workflows.StateLoading
	model.cursor = 0
	workflowContext, cancel := context.WithCancel(model.rootContext)
	model.workflowCancel = cancel
	model.updates = model.workflowRepository.Start(workflowContext, workflows.Request{
		Generation:       model.generation,
		AllNamespaces:    model.allNamespaces,
		Namespaces:       model.namespaces,
		CurrentNamespace: model.currentNamespace,
		InstanceID:       model.instanceID,
		ActiveOnly:       !model.showCompleted,
		CompletedOnly:    model.showCompleted,
	})
	return model, waitForSnapshot(model.updates)
}

func (model Model) updateDetailKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "ctrl+c":
		if model.cancel != nil {
			model.cancel()
		}
		return model, tea.Quit
	case "esc", "backspace":
		model.detailVisible = false
		model.detail = nil
		model.detailError = nil
		model.detailLoading = false
		model.detailScroll = 0
		model.detailUID = ""
	case "tab", "y":
		model.detailYAML = !model.detailYAML
		model.detailScroll = 0
	case "j", "down":
		model.detailScroll++
	case "k", "up":
		model.detailScroll = max(0, model.detailScroll-1)
	case "g", "home":
		model.detailScroll = 0
	case ":":
		model.palette = true
		model.command = ""
		model.paletteCursor = 0
	}
	return model, nil
}

func (model Model) openSelectedDetail() (tea.Model, tea.Cmd) {
	if model.itemCount() == 0 {
		return model, nil
	}
	if model.resourceDescriptor.Kind == resources.Workflows {
		return model.openWorkflowExecution()
	}
	if model.resourceRepository == nil {
		return model, nil
	}
	var namespace, name, uid string
	{
		items := model.filteredResourceItems()
		if model.cursor >= len(items) {
			return model, nil
		}
		item := items[model.cursor]
		namespace, name, uid = item.Namespace, item.Name, item.UID
	}
	return model.openDetail(namespace, name, uid)
}

// openDetail loads one resource of the current kind. An empty uid skips the
// identity check.
func (model Model) openDetail(namespace, name, uid string) (tea.Model, tea.Cmd) {
	model.detailVisible = true
	model.detailLoading = true
	model.detailError = nil
	model.detail = nil
	model.detailYAML = false
	model.detailScroll = 0
	model.detailUID = uid
	return model, loadDetail(model.rootContext, model.resourceRepository, model.currentResourceGeneration(), model.resourceDescriptor, namespace, name, uid)
}

func (model Model) currentResourceGeneration() uint64 {
	if model.resourceDescriptor.Kind == resources.Workflows {
		return model.generation
	}
	return model.resourceGeneration
}

func (model *Model) selectedWorkflowSummary() (workflows.Summary, bool) {
	if model.notificationManager == nil || model.resourceDescriptor.Kind != resources.Workflows {
		return workflows.Summary{}, false
	}
	if model.executionVisible && model.execution != nil {
		return workflows.Summary{
			Namespace:       model.execution.Namespace,
			Name:            model.execution.Name,
			UID:             model.execution.UID,
			ResourceVersion: model.execution.ResourceVersion,
			Phase:           model.execution.Phase,
		}, true
	}
	items := model.filteredItems()
	if model.cursor >= len(items) {
		return workflows.Summary{}, false
	}
	return items[model.cursor], true
}

func (model *Model) toggleSelectedNotification() {
	summary, found := model.selectedWorkflowSummary()
	if !found {
		return
	}
	watched, err := model.notificationManager.Toggle(summary)
	if err != nil {
		model.notificationWarning = err
		return
	}
	if watched {
		model.resourceNotice = "Notifications enabled for " + summary.Namespace + "/" + summary.Name
	} else {
		model.resourceNotice = "Notifications disabled for " + summary.Namespace + "/" + summary.Name
	}
}

func (model *Model) openNotificationConfig() {
	summary, found := model.selectedWorkflowSummary()
	if !found {
		return
	}
	if !model.notificationManager.IsWatched(summary.UID) {
		watched, err := model.notificationManager.Toggle(summary)
		if err != nil || !watched {
			model.notificationWarning = err
			return
		}
	}
	transitions, desktop, found := model.notificationManager.Settings(summary.UID)
	if !found {
		return
	}
	model.notificationChoices = make(map[string]bool)
	for _, transition := range transitions {
		model.notificationChoices[transition] = true
	}
	model.notificationUID = summary.UID
	model.notificationName = summary.Namespace + "/" + summary.Name
	model.notificationDesktop = desktop
	model.notificationConfig = true
}

func (model Model) updateNotificationConfigKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	transitions := []string{"Running", "Suspended", "Succeeded", "Failed", "Error", "Terminated"}
	switch key.String() {
	case "esc":
		model.notificationConfig = false
	case "enter":
		selected := make([]string, 0, len(transitions))
		for _, transition := range transitions {
			if model.notificationChoices[transition] {
				selected = append(selected, transition)
			}
		}
		if err := model.notificationManager.SetTransitions(model.notificationUID, selected, model.notificationDesktop); err != nil {
			model.notificationWarning = err
		} else {
			model.notificationConfig = false
			model.resourceNotice = "Notification preferences saved for " + model.notificationName
		}
	case "d":
		model.notificationDesktop = !model.notificationDesktop
	default:
		if len(key.String()) == 1 && key.String()[0] >= '1' && key.String()[0] <= '6' {
			index := int(key.String()[0] - '1')
			transition := transitions[index]
			model.notificationChoices[transition] = !model.notificationChoices[transition]
		}
	}
	return model, nil
}

// openTemplateSubmission loads a submission form for the selected workflow template.
func (model Model) openTemplateSubmission() (tea.Model, tea.Cmd) {
	if model.submissionService == nil || (model.resourceDescriptor.Kind != resources.WorkflowTemplates && model.resourceDescriptor.Kind != resources.ClusterWorkflowTemplates) {
		return model, nil
	}
	items := model.filteredResourceItems()
	if model.cursor >= len(items) {
		return model, nil
	}
	item := items[model.cursor]
	source := workflows.SubmissionSource{
		Namespace: item.Namespace,
		Name:      item.Name,
		Kind:      workflows.SourceWorkflowTemplate,
	}
	if model.resourceDescriptor.Kind == resources.ClusterWorkflowTemplates {
		source.Kind = workflows.SourceClusterWorkflowTemplate
	}
	destination := item.Namespace
	if destination == "" {
		destination = model.currentNamespace
	}
	return model.startSubmission(source, destination)
}

// startSubmission loads the parameter form for a submission source.
func (model Model) startSubmission(source workflows.SubmissionSource, destination string) (tea.Model, tea.Cmd) {
	model.submissionVisible = true
	model.submissionStage = "loading"
	model.submissionForm = workflows.SubmissionForm{Source: source, DestinationNamespace: destination}
	model.submissionError = nil
	model.submissionResult = nil
	model.submissionFocus = 0
	return model, prepareSubmission(model.rootContext, model.submissionService, source, destination)
}

// updateSubmissionKey handles path entry, parameter editing, cancellation, and submission confirmation.
func (model Model) updateSubmissionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch model.submissionStage {
	case "path":
		return model.updateSubmissionPathKey(key)
	case "loading":
		if key.String() == "esc" {
			model.clearSubmission()
		}
		return model, nil
	case "error", "complete":
		if key.String() == "esc" || key.String() == "enter" {
			model.clearSubmission()
		}
		return model, nil
	case "confirm":
		switch key.String() {
		case "esc", "n":
			model.submissionStage = "edit"
			model.submissionError = nil
		case "y", "enter":
			model.submissionStage = "submitting"
			return model, submitTemplate(model.rootContext, model.submissionService, workflows.SubmissionRequest{
				Source:               model.submissionForm.Source,
				DestinationNamespace: model.submissionForm.DestinationNamespace,
				Parameters:           copyStringMap(model.submissionValues),
				Manifest:             model.submissionForm.Manifest,
			})
		}
		return model, nil
	case "submitting":
		return model, nil
	}

	fieldCount := len(model.submissionForm.Parameters) + 1
	switch key.String() {
	case "esc":
		model.clearSubmission()
	case "up", "shift+tab":
		model.submissionFocus = max(0, model.submissionFocus-1)
	case "down", "tab":
		model.submissionFocus = min(fieldCount-1, model.submissionFocus+1)
	case "enter", "ctrl+s":
		if model.submissionFocus < fieldCount-1 && key.String() == "enter" {
			model.submissionFocus++
			break
		}
		if err := model.validateSubmission(); err != nil {
			model.submissionError = err
		} else {
			model.submissionError = nil
			model.submissionStage = "confirm"
		}
	case "backspace":
		model.editSubmissionValue(func(value []rune) []rune {
			if len(value) == 0 {
				return value
			}
			return value[:len(value)-1]
		})
	default:
		if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
			addition := append([]rune(nil), key.Runes...)
			model.editSubmissionValue(func(value []rune) []rune { return append(value, addition...) })
		}
	}
	return model, nil
}

func (model *Model) editSubmissionValue(edit func([]rune) []rune) {
	if model.submissionFocus == 0 {
		model.submissionForm.DestinationNamespace = string(edit([]rune(model.submissionForm.DestinationNamespace)))
		return
	}
	parameter := model.submissionForm.Parameters[model.submissionFocus-1]
	model.submissionValues[parameter.Name] = string(edit([]rune(model.submissionValues[parameter.Name])))
}

func (model Model) validateSubmission() error {
	if strings.TrimSpace(model.submissionForm.DestinationNamespace) == "" {
		return fmt.Errorf("destination namespace is required")
	}
	for _, parameter := range model.submissionForm.Parameters {
		if parameter.Required && strings.TrimSpace(model.submissionValues[parameter.Name]) == "" {
			return fmt.Errorf("parameter %s is required", parameter.Name)
		}
	}
	return nil
}

func (model *Model) clearSubmission() {
	model.submissionVisible = false
	model.submissionStage = ""
	model.submissionForm = workflows.SubmissionForm{}
	model.submissionValues = nil
	model.submissionError = nil
	model.submissionResult = nil
	model.submissionFocus = 0
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (model Model) updatePaletteKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	commands := model.paletteCommands()
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
		return model.executePaletteCommand(commands[index])
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
		if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
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
	model.detailVisible = false
	model.detail = nil
	model.detailUID = ""
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

// screen renders the active view at the current terminal width.
func (model Model) screen() string {
	width := model.viewWidth()
	switch {
	case model.logVisible:
		return model.logView(width)
	case model.submissionVisible:
		return model.submissionView(width)
	case model.actionVisible:
		return model.actionView(width)
	case model.notificationConfig:
		return model.notificationConfigView(width)
	case model.executionVisible:
		return model.executionView(width)
	case model.detailVisible:
		return model.detailView(width)
	}
	top, footer := model.listChrome(width)
	return model.frame(top, footer, func(rows int) string {
		return model.listPanel(width, rows)
	})
}

// listChrome returns the lines above and below the resource list.
func (model Model) listChrome(width int) (string, string) {
	state := model.currentState()
	tabs := " " + model.resourceTabs()
	if model.query != "" && !model.filtering {
		tabs += "   " + theme.ActiveInput.Render("/"+ui.Sanitize(model.query))
	}
	lines := []string{
		theme.Header(width, theme.Status("● "+state, state),
			label("context", model.context), label("ns", model.namespace), label("instance", displayInstance(model.instanceID))),
		tabs,
	}
	if model.notificationWarning != nil {
		lines = append(lines, theme.Error.Render(" ! notification: "+ui.Sanitize(model.notificationWarning.Error())))
	}
	if model.updateTag != "" {
		lines = append(lines, theme.Info.Render(" ↑ argowt "+ui.Sanitize(model.updateTag)+" available · run: argowt update"))
	}
	if len(model.notifications) > 0 {
		latest := model.notifications[len(model.notifications)-1]
		lines = append(lines, theme.Warning.Render(fmt.Sprintf(" 🔔 %s/%s: %s → %s", ui.Sanitize(latest.Namespace), ui.Sanitize(latest.Name), ui.Sanitize(latest.From), ui.Sanitize(latest.To))))
	}
	if model.resourceNotice != "" {
		lines = append(lines, theme.Info.Render(" ◆ "+ui.Sanitize(model.resourceNotice)))
	}
	lines = append(lines, model.actionStatusLines()...)
	if model.filterError != nil {
		lines = append(lines, theme.Warning.Render(" ! filter: "+ui.Sanitize(model.filterError.Error())+" (previous valid filter remains active)"))
	}
	if currentError := model.currentError(); currentError != nil {
		lines = append(lines, theme.Error.Render(" ! "+ui.Sanitize(currentError.Error())))
	}
	for _, line := range scopeWarnings(model.currentErrors()) {
		lines = append(lines, theme.Warning.Render(" "+ui.Sanitize(line)))
	}
	for index := range lines {
		lines[index] = ui.TruncateStyled(lines[index], width)
	}

	var footer string
	switch {
	case model.filtering:
		footer = theme.ActiveInput.Render(" / "+ui.Sanitize(model.query)+"▏") + "\n" +
			theme.Keys(width, "Enter", "apply", "Esc", "cancel", "e.g.", "status=Running · namespace!=argo · age>10m · label:team=payments")
	case model.showHelp:
		footer = theme.KeyLines(width, "j/k ↑/↓", "move", "g/G", "first/last", "Enter", "open", "Tab/Shift+Tab", "resources",
			"/", "filter", "Esc", "clear filter", "c", "active/completed workflows", "a", "workflow actions", "s", "submit template", "S", "submit local YAML",
			"n", "toggle notify", "N", "configure notify",
			":", "commands", "?", "hide help", "q", "quit") + "\n" +
			theme.KeyLines(width, "filters", "fuzzy text · status=Running · namespace!=argo · age>10m · label:team=payments")
	default:
		completedKey := "completed"
		if model.showCompleted {
			completedKey = "active"
		}
		footer = theme.Keys(width, "j/k", "move", "Enter", "open", "/", "filter", "?", "help", "q", "quit", "Tab", "resources", "c", completedKey, "a", "actions", "s", "submit", "n", "notify", ":", "commands")
	}
	return strings.Join(lines, "\n"), footer
}

const listHeaderRows = 2

// listCapacity is how many items fit in a list panel of the given height.
func listCapacity(rows int) int {
	if rows <= 0 {
		return 0
	}
	return max(1, rows-2-listHeaderRows)
}

func (model Model) listPanel(width, rows int) string {
	inner := panelInner(width)
	nameWidth := min(max(29, inner-48), 60)
	ageHeader := "AGE"
	completed := model.showCompleted && model.resourceDescriptor.Kind == resources.Workflows
	if completed {
		ageHeader = "FINISHED"
	}
	lines := []string{
		theme.TableHeader.Render(ui.Truncate(fmt.Sprintf("  %-13s %-19s %-*s %s", "STATUS", "NAMESPACE", nameWidth, "NAME", ageHeader), inner)),
		theme.Border.Render(strings.Repeat("─", inner)),
	}
	var count int
	if model.resourceDescriptor.Kind == resources.Workflows {
		items := model.filteredItems()
		count = len(items)
		start, end := window(count, model.cursor, listCapacity(rows))
		for index := start; index < end; index++ {
			item := items[index]
			name := item.Name
			if model.notificationManager != nil && model.notificationManager.IsWatched(item.UID) {
				name = "🔔 " + name
			}
			age := item.CreationTimestamp
			if completed {
				age = item.FinishedOrCreated()
			}
			lines = append(lines, listRow(inner, nameWidth, index == model.cursor, statusText(item), item.Namespace, name, age))
		}
		if count == 0 && model.connectionState != workflows.StateLoading {
			if completed {
				lines = append(lines, theme.Muted.Render("  No completed workflows in the cluster match the current scope."))
			} else {
				lines = append(lines, theme.Muted.Render("  No active workflows match the current scope."))
			}
		}
	} else {
		items := model.filteredResourceItems()
		count = len(items)
		start, end := window(count, model.cursor, listCapacity(rows))
		for index := start; index < end; index++ {
			item := items[index]
			lines = append(lines, listRow(inner, nameWidth, index == model.cursor, genericStatusText(item.Status), item.Namespace, item.Name, item.CreationTimestamp))
		}
		if count == 0 && model.resourceState != "loading" && model.resourceError == nil {
			lines = append(lines, theme.Muted.Render("  No resources match the current scope."))
		}
	}
	title := theme.PanelTitle.Render(model.resourceDescriptor.Title)
	if model.resourceDescriptor.Kind == resources.Workflows {
		if completed {
			title += theme.Muted.Render(" · completed")
		} else {
			title += theme.Muted.Render(" · active")
		}
	}
	return theme.Panel(title, theme.Muted.Render(plural(count, "item")), lines, width, rows, true)
}

// submissionView renders manifest path entry, parameter editing, or submission confirmation and progress.
func (model Model) submissionView(width int) string {
	source := model.submissionForm.Source.Label()
	top := theme.Header(width, "", label("context", model.context), label("submit from", source))
	title := theme.PanelTitle.Render("Parameters")
	var lines []string
	var footer string
	switch model.submissionStage {
	case "path":
		title = theme.PanelTitle.Render("Submit a local Workflow manifest")
		inner := panelInner(width)
		lines = []string{
			theme.Row(ui.Pad(ui.Truncate("› manifest path: "+ui.Sanitize(model.submissionForm.Source.Path)+"▏", inner), inner), true),
			theme.Muted.Render("    One argoproj.io/v1alpha1 Workflow document; other kinds are rejected."),
		}
		if model.submissionError != nil {
			lines = append(lines, "", theme.Error.Render("! "+ui.Sanitize(model.submissionError.Error())))
		}
		footer = theme.Keys(width, "Enter", "load", "Esc", "cancel")
	case "loading":
		lines = []string{theme.Info.Render("◌ Loading parameters…")}
		footer = theme.Keys(width, "Esc", "cancel")
	case "error":
		lines = []string{theme.Error.Render("! " + ui.Sanitize(model.submissionError.Error()))}
		footer = theme.Keys(width, "Enter/Esc", "close")
	case "submitting":
		lines = []string{theme.Info.Render("◌ Submitting workflow…")}
	case "complete":
		lines = []string{theme.Success.Render("✓ Submitted workflow " + ui.Sanitize(model.submissionResult.Namespace) + "/" + ui.Sanitize(model.submissionResult.Name))}
		footer = theme.Keys(width, "Enter/Esc", "close")
	case "confirm":
		title = theme.PanelTitle.Render("Confirm submission")
		lines = []string{
			field("context", ui.Sanitize(model.context)),
			field("namespace", ui.Sanitize(model.submissionForm.DestinationNamespace)),
			field("source", ui.Sanitize(source)),
		}
		if manifest := model.submissionForm.Manifest; manifest != nil {
			name := manifest.Name
			if name == "" {
				name = manifest.GenerateName + "…"
			}
			lines = append(lines, field("workflow", ui.Sanitize(name)))
		}
		for _, parameter := range model.submissionForm.Parameters {
			lines = append(lines, field(ui.Sanitize(parameter.Name), ui.Sanitize(model.submissionValues[parameter.Name])))
		}
		if model.submissionError != nil {
			lines = append(lines, "", theme.Error.Render("! "+ui.Sanitize(model.submissionError.Error())))
		}
		footer = theme.Keys(width, "y/Enter", "submit", "n/Esc", "edit")
	default:
		type formField struct {
			name, value, description string
			required                 bool
		}
		fields := []formField{{name: "destination namespace", value: model.submissionForm.DestinationNamespace, required: true}}
		for _, parameter := range model.submissionForm.Parameters {
			fields = append(fields, formField{parameter.Name, model.submissionValues[parameter.Name], parameter.Description, parameter.Required})
		}
		inner := panelInner(width)
		for index, field := range fields {
			focused := index == model.submissionFocus
			cursor := " "
			if focused {
				cursor = "›"
			}
			required := ""
			if field.required {
				required = " *"
			}
			line := fmt.Sprintf("%s %s%s: %s", cursor, ui.Sanitize(field.name), required, ui.Sanitize(field.value))
			if focused {
				line = theme.Row(ui.Pad(ui.Truncate(line+"▏", inner), inner), true)
			}
			lines = append(lines, line)
			if field.description != "" {
				lines = append(lines, theme.Muted.Render("    "+ui.Sanitize(field.description)))
			}
		}
		if model.submissionError != nil {
			lines = append(lines, "", theme.Error.Render("! "+ui.Sanitize(model.submissionError.Error())))
		}
		footer = theme.Keys(width, "Tab/↑/↓", "fields", "Enter", "next/confirm", "Ctrl+S", "confirm", "Esc", "cancel")
	}
	return model.frame(top, footer, func(rows int) string {
		return theme.Panel(title, "", lines, width, rows, true)
	})
}

func (model Model) notificationConfigView(width int) string {
	transitions := []string{"Running", "Suspended", "Succeeded", "Failed", "Error", "Terminated"}
	top := theme.Header(width, "", label("context", model.context), label("notify", model.notificationName))
	lines := []string{theme.TableHeader.Render("Notify when the workflow becomes")}
	for index, transition := range transitions {
		if model.notificationChoices[transition] {
			lines = append(lines, theme.Success.Render(fmt.Sprintf("  %d ● %s", index+1, transition)))
		} else {
			lines = append(lines, theme.Muted.Render(fmt.Sprintf("  %d ○ %s", index+1, transition)))
		}
	}
	desktop := theme.Muted.Render("○ desktop notifications off")
	if model.notificationDesktop {
		desktop = theme.Success.Render("● desktop notifications on") + theme.Muted.Render(" (no-op when unavailable)")
	}
	lines = append(lines, "", "  d "+desktop)
	footer := theme.Keys(width, "1-6", "toggle", "d", "desktop", "Enter", "save", "Esc", "cancel")
	return model.frame(top, footer, func(rows int) string {
		return theme.Panel(theme.PanelTitle.Render("Notification preferences"), "", lines, width, rows, true)
	})
}

func (model Model) detailView(width int) string {
	name := model.detailUID
	if model.detail != nil {
		name = model.detail.Summary.Name
	}
	top := theme.Header(width, "", label("context", model.context), label(strings.ToLower(model.resourceDescriptor.Title), name))
	footer := theme.Keys(width, "Tab/y", "metadata/YAML", "j/k", "scroll", ":", "commands", "Esc", "back", "q", "quit")
	mode := 0
	if model.detailYAML {
		mode = 1
	}
	title := theme.Tabs([]string{"Metadata", "YAML"}, mode)
	return model.frame(top, footer, func(rows int) string {
		var lines []string
		switch {
		case model.detailLoading:
			lines = []string{theme.Info.Render("◌ Loading resource details…")}
		case model.detailError != nil:
			lines = []string{theme.Error.Render("! " + ui.Sanitize(model.detailError.Error()))}
		case model.detail == nil:
			lines = []string{"No detail is available."}
		default:
			all := model.detailLines()
			visible := len(all)
			if rows > 0 {
				visible = max(1, rows-2)
			}
			start := min(model.detailScroll, max(0, len(all)-1))
			for _, line := range all[start:min(len(all), start+visible)] {
				lines = append(lines, model.styleDetailLine(ui.Sanitize(line)))
			}
		}
		return theme.Panel(title, "", lines, width, rows, true)
	})
}

// styleDetailLine highlights section titles and the names in "Name: value".
func (model Model) styleDetailLine(line string) string {
	if model.detailYAML {
		return line
	}
	if strings.HasSuffix(line, ":") {
		return theme.PanelTitle.Render(line)
	}
	if name, value, found := strings.Cut(line, ": "); found && !strings.HasPrefix(line, " ") {
		return theme.Muted.Render(name+":") + " " + value
	}
	return line
}

func (model Model) detailLines() []string {
	if model.detailYAML {
		return strings.Split(strings.TrimSuffix(model.detail.YAML, "\n"), "\n")
	}
	detail := model.detail
	lines := []string{
		"Name: " + detail.Summary.Name,
		"Namespace: " + displayNamespace(detail.Summary.Namespace),
		"UID: " + detail.Summary.UID,
		"Resource version: " + detail.Summary.ResourceVersion,
		"Status: " + detail.Summary.Status,
		"Created: " + displayTime(detail.Summary.CreationTimestamp),
	}
	if detail.Summary.Template != "" {
		lines = append(lines, "Template: "+detail.Summary.Template)
	}
	lines = append(lines, "", "Labels:")
	lines = appendMap(lines, detail.Labels)
	lines = append(lines, "", "Annotations:")
	lines = appendMap(lines, detail.Annotations)
	lines = append(lines, "", "Owners:")
	if len(detail.Owners) == 0 {
		lines = append(lines, "  —")
	}
	for _, owner := range detail.Owners {
		lines = append(lines, fmt.Sprintf("  %s/%s (%s)", owner.Kind, owner.Name, owner.UID))
	}
	lines = append(lines, "", "Conditions:")
	if len(detail.Conditions) == 0 {
		lines = append(lines, "  —")
	}
	for _, condition := range detail.Conditions {
		line := fmt.Sprintf("  %s=%s", condition.Type, condition.Status)
		if condition.Reason != "" {
			line += " reason=" + condition.Reason
		}
		lines = append(lines, line)
		if condition.Message != "" {
			lines = append(lines, "    "+condition.Message)
		}
	}
	return lines
}

func appendMap(lines []string, values map[string]string) []string {
	if len(values) == 0 {
		return append(lines, "  —")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("  %s=%s", key, values[key]))
	}
	return lines
}

func displayNamespace(namespace string) string {
	if namespace == "" {
		return "cluster-scoped"
	}
	return namespace
}

func displayTime(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Format(time.RFC3339)
}

func listRow(width, nameWidth int, selected bool, status, namespace, name string, created time.Time) string {
	cursor := " "
	if selected {
		cursor = "›"
	}
	if namespace == "" {
		namespace = "—"
	}
	line := fmt.Sprintf("%s %-13s %-19s %-*s %s",
		cursor,
		ui.Truncate(ui.Sanitize(status), 13),
		ui.Truncate(ui.Sanitize(namespace), 19),
		nameWidth, ui.Truncate(ui.Sanitize(name), nameWidth),
		formatAge(created),
	)
	return styleRow(line, width, selected, status)
}

// scopeWarnings groups scopes by error, one line per error.
// e.g. "! forbidden: kyverno, mlflow, ray (+24 more)"
func scopeWarnings(errorsByScope map[string]string) []string {
	scopesByError := make(map[string][]string)
	for scope, message := range errorsByScope {
		scopesByError[message] = append(scopesByError[message], scope)
	}

	lines := make([]string, 0, len(scopesByError))
	for message, scopes := range scopesByError {
		sort.Strings(scopes)
		shown := scopes[:min(len(scopes), maxWarningScopes)]
		line := "! " + message + ": " + strings.Join(shown, ", ")
		if hidden := len(scopes) - len(shown); hidden > 0 {
			line += fmt.Sprintf(" (+%d more)", hidden)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
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
