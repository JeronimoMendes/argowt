package logs

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDiscoverWorkflowPodAndContainers(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argo", Name: "example-task",
			Labels:      map[string]string{WorkflowLabel: "example"},
			Annotations: map[string]string{NodeIDAnnotation: "node-1", NodeNameAnnotation: "example.task"},
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init"}},
			Containers:     []corev1.Container{{Name: "wait"}, {Name: "sidecar"}, {Name: "main"}},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "main", RestartCount: 1}},
		},
	}
	service := NewService(fake.NewSimpleClientset(pod))
	got, err := service.Discover(context.Background(), "argo", "example", "node-1", "")
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got.Name != "example-task" || got.Phase != "Running" || len(got.Containers) != 4 {
		t.Fatalf("pod = %+v", got)
	}
	if got.Containers[0].Name != "main" || !got.Containers[0].HasPrevious || got.Containers[1].Name != "wait" {
		t.Fatalf("containers = %+v", got.Containers)
	}
}

func TestDiscoverSupportsHistoricalPodNameNodeID(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "argo", Name: "historical-node-id", Labels: map[string]string{WorkflowLabel: "example"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}},
	}
	service := NewService(fake.NewSimpleClientset(pod))
	got, err := service.Discover(context.Background(), "argo", "example", "historical-node-id", "example.task")
	if err != nil || got.Name != "historical-node-id" {
		t.Fatalf("Discover() = %+v, %v", got, err)
	}
}

func TestDiscoverDoesNotSelectUnrelatedWorkflowPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		listAction := action.(k8stesting.ListAction)
		if got := listAction.GetListRestrictions().Labels.String(); got != WorkflowLabel+"=example" {
			t.Fatalf("selector = %q", got)
		}
		return true, &corev1.PodList{}, nil
	})
	service := NewService(client)
	if _, err := service.Discover(context.Background(), "argo", "example", "node-1", ""); err == nil {
		t.Fatal("Discover() expected an error")
	}
}

func TestStreamCancellationClosesBlockedReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	service := &Service{openLog: func(context.Context, Request) (io.ReadCloser, error) { return reader, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	events := service.Stream(ctx, Request{})
	cancel()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("canceled stream emitted an event")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled stream did not close")
	}
}

func TestStreamBatchesLinesAndEnds(t *testing.T) {
	service := &Service{openLog: func(context.Context, Request) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("one\ntwo\n")), nil
	}}
	events := service.Stream(context.Background(), Request{})
	first := <-events
	if strings.Join(first.Lines, ",") != "one,two" {
		t.Fatalf("lines = %v", first.Lines)
	}
	ended := <-events
	if !ended.Ended || ended.Err != nil {
		t.Fatalf("ended event = %+v", ended)
	}
}

func TestStreamFlushesLinesWhileFollowedStreamIsQuiet(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	service := &Service{openLog: func(context.Context, Request) (io.ReadCloser, error) { return reader, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := service.Stream(ctx, Request{Follow: true})
	go func() { _, _ = writer.Write([]byte("starting\n")) }()
	select {
	case event := <-events:
		if strings.Join(event.Lines, ",") != "starting" {
			t.Fatalf("lines = %v", event.Lines)
		}
	case <-time.After(time.Second):
		t.Fatal("a line was held back while the stream stayed open")
	}
}
