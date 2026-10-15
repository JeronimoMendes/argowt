package notify

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	"argowt/internal/config"
	"argowt/internal/workflows"
)

type fakeSource struct {
	client *fakeClient
}

func (source fakeSource) Workflows(string) workflows.WorkflowClient { return source.client }

type fakeClient struct {
	workflow     *wfv1.Workflow
	watcher      *watch.RaceFreeFakeWatcher
	watchOptions chan metav1.ListOptions
}

func (client *fakeClient) Get(context.Context, string, metav1.GetOptions) (*wfv1.Workflow, error) {
	if client.workflow == nil {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "workflows"}, "missing")
	}
	return client.workflow.DeepCopy(), nil
}

func (client *fakeClient) List(context.Context, metav1.ListOptions) (*wfv1.WorkflowList, error) {
	return &wfv1.WorkflowList{}, nil
}

func (client *fakeClient) Watch(_ context.Context, options metav1.ListOptions) (watch.Interface, error) {
	if client.watchOptions != nil {
		client.watchOptions <- options
	}
	return client.watcher, nil
}

func TestTogglePersistsAndNotifiesAfterNavigation(t *testing.T) {
	workflow := testWorkflow("uid-1", wfv1.WorkflowRunning, "1")
	client := &fakeClient{workflow: workflow, watcher: watch.NewRaceFreeFake()}
	path := filepath.Join(t.TempDir(), "state.json")
	manager := NewManager(fakeSource{client: client}, "development", path, config.NewState(), NoopDesktop{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	defer manager.Close()

	watched, err := manager.Toggle(workflows.Normalize(workflow))
	if err != nil || !watched {
		t.Fatalf("Toggle() = %t, %v", watched, err)
	}
	if !manager.IsWatched("uid-1") {
		t.Fatal("workflow should remain watched independently of the visible screen")
	}
	state, err := config.LoadState(path)
	if err != nil || len(state.Watches) != 1 || state.Watches[0].UID != "uid-1" {
		t.Fatalf("persisted state = %+v, %v", state, err)
	}

	completed := workflow.DeepCopy()
	completed.Status.Phase = wfv1.WorkflowSucceeded
	completed.ResourceVersion = "2"
	client.watcher.Modify(completed)
	event := receiveEvent(t, manager.Events())
	if event.From != "Running" || event.To != "Succeeded" || event.CatchUp {
		t.Fatalf("event = %+v", event)
	}
}

func TestObservationUpdatesPreserveTransitionPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := config.NewState()
	state.Watches = []config.WorkflowWatch{{
		Context: "development", Namespace: "argo", Name: "example", UID: "uid-1",
		LastPhase: "Running", Transitions: []string{"Succeeded"},
	}}
	manager := NewManager(fakeSource{}, "development", path, state, NoopDesktop{})
	if err := manager.SetTransitions("uid-1", []string{"Running", "Failed"}, true); err != nil {
		t.Fatal(err)
	}
	manager.updateRecord(config.WorkflowWatch{
		Context: "development", Namespace: "argo", Name: "example", UID: "uid-1",
		LastPhase: "Succeeded", ResourceVersion: "9", ObservedAt: time.Now(),
	})
	loaded, err := config.LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	watch := loaded.Watches[0]
	if !watch.Desktop || len(watch.Transitions) != 2 || watch.Transitions[0] != "Running" || watch.LastPhase != "Succeeded" {
		t.Fatalf("watch = %+v", watch)
	}
}

func TestStartupReconcilesMissedTransition(t *testing.T) {
	workflow := testWorkflow("uid-1", wfv1.WorkflowSucceeded, "4")
	state := config.NewState()
	state.Watches = []config.WorkflowWatch{{
		Context: "development", Namespace: "argo", Name: "example", UID: "uid-1",
		LastPhase: "Running", ResourceVersion: "2", Transitions: []string{"Succeeded"},
	}}
	client := &fakeClient{workflow: workflow, watcher: watch.NewRaceFreeFake(), watchOptions: make(chan metav1.ListOptions, 1)}
	manager := NewManager(fakeSource{client: client}, "development", "", state, NoopDesktop{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	defer manager.Close()

	event := receiveEvent(t, manager.Events())
	if !event.CatchUp || event.To != "Succeeded" {
		t.Fatalf("catch-up event = %+v", event)
	}

	// An unchanged workflow keeps an old resourceVersion; watching from it fails with 410 Expired.
	if options := <-client.watchOptions; options.ResourceVersion != "" {
		t.Fatalf("watch resourceVersion = %q, want current state", options.ResourceVersion)
	}
}

func TestRecreatedWorkflowDoesNotInheritWatch(t *testing.T) {
	workflow := testWorkflow("new-uid", wfv1.WorkflowRunning, "1")
	state := config.NewState()
	state.Watches = []config.WorkflowWatch{{
		Context: "development", Namespace: "argo", Name: "example", UID: "old-uid",
		LastPhase: "Running", Transitions: []string{"Succeeded"},
	}}
	manager := NewManager(fakeSource{client: &fakeClient{workflow: workflow, watcher: watch.NewRaceFreeFake()}}, "development", "", state, NoopDesktop{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	defer manager.Close()

	event := receiveEvent(t, manager.Events())
	if event.UID != "old-uid" || event.To != "Recreated" {
		t.Fatalf("recreation event = %+v", event)
	}
	if manager.IsWatched("new-uid") {
		t.Fatal("new workflow UID inherited the old watch")
	}
}

func TestWorkflowPhaseRecognizesSuspendedAndTerminated(t *testing.T) {
	suspended := true
	workflow := testWorkflow("uid-1", wfv1.WorkflowRunning, "1")
	workflow.Spec.Suspend = &suspended
	if got := workflowPhase(workflow); got != "Suspended" {
		t.Fatalf("suspended phase = %q", got)
	}
	workflow.Spec.Suspend = nil
	workflow.Status.Phase = wfv1.WorkflowFailed
	workflow.Status.Message = "workflow terminated by operator"
	if got := workflowPhase(workflow); got != "Terminated" {
		t.Fatalf("terminated phase = %q", got)
	}
}

func testWorkflow(uid string, phase wfv1.WorkflowPhase, resourceVersion string) *wfv1.Workflow {
	return &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "example", UID: types.UID(uid), ResourceVersion: resourceVersion},
		Status:     wfv1.WorkflowStatus{Phase: phase},
	}
}

func receiveEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
		return Event{}
	}
}
