package workflows

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type fakeWorkflowSource struct {
	clients map[string]*fakeWorkflowClient
}

func (source fakeWorkflowSource) Workflows(namespace string) WorkflowClient {
	return source.clients[namespace]
}

type fakeWorkflowClient struct {
	mu          sync.Mutex
	list        *wfv1.WorkflowList
	listErr     error
	watcher     *watch.RaceFreeFakeWatcher
	lastOptions metav1.ListOptions
}

func (client *fakeWorkflowClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*wfv1.Workflow, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.listErr != nil {
		return nil, client.listErr
	}
	for index := range client.list.Items {
		if client.list.Items[index].Name == name {
			return client.list.Items[index].DeepCopy(), nil
		}
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "workflows"}, name)
}

func (client *fakeWorkflowClient) List(_ context.Context, options metav1.ListOptions) (*wfv1.WorkflowList, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.lastOptions = options
	if client.listErr != nil {
		return nil, client.listErr
	}
	return client.list.DeepCopy(), nil
}

func (client *fakeWorkflowClient) Watch(context.Context, metav1.ListOptions) (watch.Interface, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.watcher == nil {
		client.watcher = watch.NewRaceFreeFake()
	}
	return client.watcher, nil
}

type fakeNamespaceClient struct {
	names []string
	err   error
}

func (client fakeNamespaceClient) List(context.Context, metav1.ListOptions) ([]string, error) {
	return client.names, client.err
}

func TestAllNamespaceForbiddenFallsBackAndKeepsHealthyNamespaces(t *testing.T) {
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "argoproj.io", Resource: "workflows"},
		"",
		errors.New("cluster-wide access denied"),
	)
	source := fakeWorkflowSource{clients: map[string]*fakeWorkflowClient{
		"":        {listErr: forbidden},
		"healthy": {list: workflowList("10", workflow("healthy", "running", "Running", ""))},
		"denied":  {listErr: forbidden},
	}}
	repository := NewRepository(source, nil)
	ctx, cancel := context.WithCancel(context.Background())
	updates := repository.Start(ctx, Request{
		Generation:    7,
		AllNamespaces: true,
		Namespaces:    []string{"healthy", "denied"},
		ActiveOnly:    true,
	})

	snapshot := <-updates
	cancel()
	if snapshot.Generation != 7 || snapshot.State != StatePartial || !snapshot.Fallback {
		t.Fatalf("unexpected snapshot state: %+v", snapshot)
	}
	if len(snapshot.Workflows) != 1 || snapshot.Workflows[0].Name != "running" {
		t.Fatalf("workflows = %+v", snapshot.Workflows)
	}
	if snapshot.PartialErrors["denied"] != "forbidden" {
		t.Fatalf("partial errors = %+v", snapshot.PartialErrors)
	}
}

func TestAllNamespaceSuccessUsesOneClusterWideWatch(t *testing.T) {
	clusterWide := &fakeWorkflowClient{list: workflowList("20",
		workflow("one", "first", "Running", "controller-a"),
		workflow("two", "other-instance", "Running", "controller-b"),
		workflow("two", "complete", "Succeeded", "controller-a"),
	)}
	repository := NewRepository(fakeWorkflowSource{clients: map[string]*fakeWorkflowClient{"": clusterWide}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	updates := repository.Start(ctx, Request{
		Generation:    2,
		AllNamespaces: true,
		InstanceID:    "controller-a",
		ActiveOnly:    true,
	})

	snapshot := <-updates
	cancel()
	if snapshot.State != StateFresh || snapshot.Fallback {
		t.Fatalf("unexpected snapshot state: %+v", snapshot)
	}
	if len(snapshot.Workflows) != 1 || snapshot.Workflows[0].Name != "first" {
		t.Fatalf("workflows = %+v", snapshot.Workflows)
	}
	clusterWide.mu.Lock()
	selector := clusterWide.lastOptions.LabelSelector
	clusterWide.mu.Unlock()
	if selector != ControllerInstanceIDLabel+"=controller-a" {
		t.Fatalf("label selector = %q", selector)
	}
}

func TestWatchUpdatesActiveSnapshot(t *testing.T) {
	watcher := watch.NewRaceFreeFake()
	client := &fakeWorkflowClient{
		list:    workflowList("1", workflow("argo", "first", "Running", "")),
		watcher: watcher,
	}
	repository := NewRepository(fakeWorkflowSource{clients: map[string]*fakeWorkflowClient{"argo": client}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := repository.Start(ctx, Request{
		Generation:       3,
		CurrentNamespace: "argo",
		ActiveOnly:       true,
	})
	<-updates

	added := workflow("argo", "second", "Pending", "")
	added.ResourceVersion = "2"
	watcher.Add(&added)
	updated := <-updates
	if len(updated.Workflows) != 2 {
		t.Fatalf("workflows after add = %+v", updated.Workflows)
	}

	completed := added.DeepCopy()
	completed.Status.Phase = wfv1.WorkflowSucceeded
	completed.ResourceVersion = "3"
	watcher.Modify(completed)
	updated = <-updates
	if len(updated.Workflows) != 1 || updated.Workflows[0].Name != "first" {
		t.Fatalf("workflows after completion = %+v", updated.Workflows)
	}
}

func TestExpiredWatchRelistsFromFreshResourceVersion(t *testing.T) {
	watcher := watch.NewRaceFreeFake()
	client := &fakeWorkflowClient{
		list:    workflowList("1", workflow("argo", "before", "Running", "")),
		watcher: watcher,
	}
	repository := NewRepository(fakeWorkflowSource{clients: map[string]*fakeWorkflowClient{"argo": client}}, nil)
	repository.retryWait = func(context.Context, int) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := repository.Start(ctx, Request{Generation: 1, CurrentNamespace: "argo", ActiveOnly: true})
	<-updates

	client.mu.Lock()
	client.list = workflowList("8", workflow("argo", "after-relist", "Pending", ""))
	client.mu.Unlock()
	watcher.Error(&metav1.Status{
		Status: metav1.StatusFailure,
		Reason: metav1.StatusReasonExpired,
		Code:   410,
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case snapshot := <-updates:
			if snapshot.State == StateFresh && len(snapshot.Workflows) == 1 && snapshot.Workflows[0].Name == "after-relist" {
				return
			}
		case <-deadline:
			t.Fatal("repository did not publish the relisted snapshot")
		}
	}
}

func TestNamespaceDiscoveryFallsBackToCurrentNamespace(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "workflows"}, "", errors.New("denied"))
	namespaceForbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", errors.New("denied"))
	source := fakeWorkflowSource{clients: map[string]*fakeWorkflowClient{
		"":        {listErr: forbidden},
		"default": {list: workflowList("1", workflow("default", "active", "Pending", ""))},
	}}
	repository := NewRepository(source, fakeNamespaceClient{err: namespaceForbidden})
	ctx, cancel := context.WithCancel(context.Background())
	updates := repository.Start(ctx, Request{
		Generation:       1,
		AllNamespaces:    true,
		CurrentNamespace: "default",
		ActiveOnly:       true,
	})

	snapshot := <-updates
	cancel()
	if len(snapshot.Workflows) != 1 || snapshot.Scope[0] != "default" {
		t.Fatalf("unexpected fallback snapshot: %+v", snapshot)
	}
	if snapshot.PartialErrors["namespace discovery"] != "forbidden" {
		t.Fatalf("partial errors = %+v", snapshot.PartialErrors)
	}
}

func workflow(namespace, name, phase, instanceID string) wfv1.Workflow {
	labels := map[string]string{}
	if instanceID != "" {
		labels[ControllerInstanceIDLabel] = instanceID
	}
	return wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Status:     wfv1.WorkflowStatus{Phase: wfv1.WorkflowPhase(phase)},
	}
}

func workflowList(resourceVersion string, workflows ...wfv1.Workflow) *wfv1.WorkflowList {
	return &wfv1.WorkflowList{
		ListMeta: metav1.ListMeta{ResourceVersion: resourceVersion},
		Items:    workflows,
	}
}
