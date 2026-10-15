// Package notify manages UID-bound workflow notifications independently of the
// currently visible TUI screen.
package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"

	"argowt/internal/config"
	"argowt/internal/workflows"
)

var (
	DefaultTransitions = []string{"Succeeded", "Failed", "Error"}
	errWorkflowDeleted = fmt.Errorf("workflow deleted")
)

// Pseudo-phases for watches whose workflow is gone.
const (
	phaseDeleted   = "Deleted"
	phaseRecreated = "Recreated"
)

type Event struct {
	Context   string
	Namespace string
	Name      string
	UID       string
	From      string
	To        string
	Message   string
	CatchUp   bool
	At        time.Time
}

type Desktop interface {
	Notify(Event) error
}

type NoopDesktop struct{}

func (NoopDesktop) Notify(Event) error { return nil }

type Manager struct {
	source    workflows.WorkflowSource
	context   string
	statePath string
	desktop   Desktop

	mu       sync.RWMutex
	state    config.State
	root     context.Context
	cancel   context.CancelFunc
	running  map[string]context.CancelFunc
	events   chan Event
	warnings chan error
}

func NewManager(source workflows.WorkflowSource, contextName, statePath string, state config.State, desktop Desktop) *Manager {
	if desktop == nil {
		desktop = NoopDesktop{}
	}
	return &Manager{
		source:    source,
		context:   contextName,
		statePath: statePath,
		desktop:   desktop,
		state:     state,
		running:   make(map[string]context.CancelFunc),
		events:    make(chan Event, 32),
		warnings:  make(chan error, 8),
	}
}

func (manager *Manager) Start(ctx context.Context) {
	manager.mu.Lock()
	if manager.cancel != nil {
		manager.mu.Unlock()
		return
	}
	manager.root, manager.cancel = context.WithCancel(ctx)
	records := append([]config.WorkflowWatch(nil), manager.state.Watches...)
	manager.mu.Unlock()
	for _, record := range records {
		if record.Context == manager.context {
			manager.startRecord(record)
		}
	}
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	if manager.cancel != nil {
		manager.cancel()
	}
	manager.cancel = nil
	manager.mu.Unlock()
}

func (manager *Manager) Events() <-chan Event { return manager.events }

func (manager *Manager) Warnings() <-chan error { return manager.warnings }

func (manager *Manager) IsWatched(uid string) bool {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	for _, record := range manager.state.Watches {
		if record.Context == manager.context && record.UID == uid {
			return true
		}
	}
	return false
}

func (manager *Manager) Toggle(summary workflows.Summary) (bool, error) {
	manager.mu.Lock()
	if _, removed := manager.removeLocked(summary.UID); removed {
		err := manager.saveLocked()
		manager.mu.Unlock()
		return false, err
	}
	record := config.WorkflowWatch{
		Context:         manager.context,
		Namespace:       summary.Namespace,
		Name:            summary.Name,
		UID:             summary.UID,
		LastPhase:       summary.DisplayPhase(),
		ResourceVersion: summary.ResourceVersion,
		ObservedAt:      time.Now().UTC(),
		Transitions:     append([]string(nil), DefaultTransitions...),
	}
	manager.state.Watches = append(manager.state.Watches, record)
	err := manager.saveLocked()
	manager.mu.Unlock()
	if err != nil {
		return false, err
	}
	manager.startRecord(record)
	return true, nil
}

func (manager *Manager) Settings(uid string) ([]string, bool, bool) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	for _, record := range manager.state.Watches {
		if record.Context == manager.context && record.UID == uid {
			return append([]string(nil), record.Transitions...), record.Desktop, true
		}
	}
	return nil, false, false
}

func (manager *Manager) SetTransitions(uid string, transitions []string, desktop bool) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for index := range manager.state.Watches {
		record := &manager.state.Watches[index]
		if record.Context == manager.context && record.UID == uid {
			record.Transitions = append([]string(nil), transitions...)
			record.Desktop = desktop
			return manager.saveLocked()
		}
	}
	return fmt.Errorf("notification watch %s was not found", uid)
}

func (manager *Manager) startRecord(record config.WorkflowWatch) {
	manager.mu.Lock()
	if manager.root == nil {
		manager.mu.Unlock()
		return
	}
	if previous := manager.running[record.UID]; previous != nil {
		previous()
	}
	ctx, cancel := context.WithCancel(manager.root)
	manager.running[record.UID] = cancel
	manager.mu.Unlock()
	go manager.runRecord(ctx, record)
}

func (manager *Manager) runRecord(ctx context.Context, record config.WorkflowWatch) {
	client := manager.source.Workflows(record.Namespace)
	attempt := 0
	for ctx.Err() == nil {
		workflow, err := client.Get(ctx, record.Name, metav1.GetOptions{})

		// Deleted while not watched (e.g. pruned by a cron history limit): drop the watch
		// instead of retrying forever.
		if apierrors.IsNotFound(err) {
			manager.dropWatch(ctx, record, phaseDeleted, "workflow was deleted")
			return
		}
		if err != nil {
			manager.warn(fmt.Errorf("reconcile notification for %s/%s: %w", record.Namespace, record.Name, err))
			if !wait(ctx, attempt) {
				return
			}
			attempt++
			continue
		}
		if string(workflow.UID) != record.UID {
			manager.dropWatch(ctx, record, phaseRecreated, "workflow was deleted and recreated; notification watch was not transferred")
			return
		}
		currentPhase := workflowPhase(workflow)
		if record.LastPhase != "" && currentPhase != record.LastPhase && selected(record.Transitions, currentPhase) {
			manager.emit(Event{
				Context: manager.context, Namespace: record.Namespace, Name: record.Name, UID: record.UID,
				From: record.LastPhase, To: currentPhase, Message: workflow.Status.Message, CatchUp: true, At: time.Now().UTC(),
			}, record.Desktop)
		}
		record.LastPhase = currentPhase
		record.ResourceVersion = workflow.ResourceVersion
		record.ObservedAt = time.Now().UTC()
		manager.updateRecord(record)
		attempt = 0

		// Watch from the current state, not from the Get resourceVersion: an unchanged
		// workflow keeps an old version, which the API server rejects with 410 Expired.
		stream, err := client.Watch(ctx, metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("metadata.name", record.Name).String(),
		})
		if err == nil {
			err = manager.consume(ctx, stream, &record)
		}
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errWorkflowDeleted) {
			manager.dropWatch(ctx, record, phaseDeleted, "workflow was deleted")
			return
		}
		manager.warn(fmt.Errorf("notification watch for %s/%s: %w", record.Namespace, record.Name, err))
		if !wait(ctx, attempt) {
			return
		}
		attempt++
	}
}

func (manager *Manager) consume(ctx context.Context, stream watch.Interface, record *config.WorkflowWatch) error {
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case watched, open := <-stream.ResultChan():
			if !open {
				return fmt.Errorf("watch closed")
			}
			if watched.Type == watch.Error {
				return fmt.Errorf("watch failed: %w", apierrors.FromObject(watched.Object))
			}
			workflow, ok := watched.Object.(*wfv1.Workflow)
			if !ok {
				continue
			}
			if string(workflow.UID) != record.UID {
				return fmt.Errorf("workflow identity changed")
			}
			if watched.Type == watch.Deleted {
				return errWorkflowDeleted
			}
			phase := workflowPhase(workflow)
			transitions, desktop, found := manager.Settings(record.UID)
			if !found {
				return context.Canceled
			}
			if phase != record.LastPhase && selected(transitions, phase) {
				manager.emit(Event{
					Context: manager.context, Namespace: record.Namespace, Name: record.Name, UID: record.UID,
					From: record.LastPhase, To: phase, Message: workflow.Status.Message, At: time.Now().UTC(),
				}, desktop)
			}
			record.LastPhase = phase
			record.ResourceVersion = workflow.ResourceVersion
			record.ObservedAt = time.Now().UTC()
			manager.updateRecord(*record)
		}
	}
}

func (manager *Manager) updateRecord(updated config.WorkflowWatch) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for index, record := range manager.state.Watches {
		if record.Context == updated.Context && record.UID == updated.UID {
			record.Name = updated.Name
			record.Namespace = updated.Namespace
			record.LastPhase = updated.LastPhase
			record.ResourceVersion = updated.ResourceVersion
			record.ObservedAt = updated.ObservedAt
			manager.state.Watches[index] = record
			if err := manager.saveLocked(); err != nil {
				manager.warnLocked(err)
			}
			return
		}
	}
}

// dropWatch removes a watch whose workflow is gone and reports why.
func (manager *Manager) dropWatch(ctx context.Context, record config.WorkflowWatch, to, message string) {
	manager.mu.Lock()

	// A cancelled context means the watch was toggled off, and maybe on again:
	// the stored record may already belong to a newer goroutine.
	if ctx.Err() != nil {
		manager.mu.Unlock()
		return
	}

	removed, _ := manager.removeLocked(record.UID)
	if err := manager.saveLocked(); err != nil {
		manager.warnLocked(err)
	}
	manager.mu.Unlock()

	manager.emit(Event{
		Context: manager.context, Namespace: record.Namespace, Name: record.Name, UID: record.UID,
		From: record.LastPhase, To: to, Message: message, At: time.Now().UTC(),
	}, removed.Desktop)
}

// removeLocked deletes a watch and stops its goroutine. The caller holds mu.
func (manager *Manager) removeLocked(uid string) (config.WorkflowWatch, bool) {
	for index, record := range manager.state.Watches {
		if record.Context != manager.context || record.UID != uid {
			continue
		}

		manager.state.Watches = append(manager.state.Watches[:index], manager.state.Watches[index+1:]...)
		if cancel := manager.running[uid]; cancel != nil {
			cancel()
			delete(manager.running, uid)
		}
		return record, true
	}
	return config.WorkflowWatch{}, false
}

func (manager *Manager) saveLocked() error {
	if manager.statePath == "" {
		return nil
	}
	return config.SaveState(manager.statePath, manager.state)
}

func (manager *Manager) emit(event Event, desktop bool) {
	select {
	case manager.events <- event:
	default:
	}
	if desktop {
		if err := manager.desktop.Notify(event); err != nil {
			manager.warn(err)
		}
	}
}

func (manager *Manager) warn(err error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.warnLocked(err)
}

func (manager *Manager) warnLocked(err error) {
	select {
	case manager.warnings <- err:
	default:
	}
}

func workflowPhase(workflow *wfv1.Workflow) string {
	if strings.Contains(strings.ToLower(workflow.Status.Message), "terminat") {
		return "Terminated"
	}
	if workflow.Spec.Suspend != nil && *workflow.Spec.Suspend {
		return "Suspended"
	}
	if workflow.Status.Phase == "" {
		return "Unknown"
	}
	return string(workflow.Status.Phase)
}

func selected(transitions []string, phase string) bool {
	for _, transition := range transitions {
		if transition == phase {
			return true
		}
	}
	return false
}

func wait(ctx context.Context, attempt int) bool {
	if attempt > 5 {
		attempt = 5
	}
	delay := time.Second * time.Duration(1<<attempt)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
