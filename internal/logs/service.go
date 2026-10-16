// Package logs discovers workflow pods and streams bounded operational logs
// directly from the Kubernetes pod log API.
package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

const (
	WorkflowLabel      = "workflows.argoproj.io/workflow"
	NodeIDAnnotation   = "workflows.argoproj.io/node-id"
	NodeNameAnnotation = "workflows.argoproj.io/node-name"

	streamBatchSize  = 32
	streamFlushDelay = 50 * time.Millisecond
)

type Container struct {
	Name         string
	Kind         string
	RestartCount int32
	HasPrevious  bool
}

type Pod struct {
	Namespace  string
	Name       string
	Phase      string
	Containers []Container
}

type Request struct {
	Namespace  string
	Pod        string
	Container  string
	Previous   bool
	Follow     bool
	Timestamps bool
	TailLines  int64
}

type Event struct {
	Lines []string
	Ended bool
	Err   error
}

type Service struct {
	client  kubernetes.Interface
	openLog func(context.Context, Request) (io.ReadCloser, error)
}

func NewService(client kubernetes.Interface) *Service {
	service := &Service{client: client}
	service.openLog = service.openPodLog
	return service
}

func (service *Service) Discover(ctx context.Context, namespace, workflowName, nodeID, nodeName string) (Pod, error) {
	selector := labels.Set{WorkflowLabel: workflowName}.AsSelector().String()
	pods, err := service.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return Pod{}, fmt.Errorf("list workflow pods: %w", err)
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		annotations := pod.GetAnnotations()
		annotatedNodeID := annotations[NodeIDAnnotation]
		annotatedNodeName := annotations[NodeNameAnnotation]
		if annotatedNodeID == nodeID || annotatedNodeName == nodeName || (annotatedNodeID == "" && pod.Name == nodeID) || pod.Name == nodeName {
			return normalizePod(pod), nil
		}
	}
	return Pod{}, fmt.Errorf("no pod found for workflow node %s", nodeID)
}

func (service *Service) Stream(ctx context.Context, request Request) <-chan Event {
	events := make(chan Event, 8)
	go func() {
		defer close(events)
		stream, err := service.openLog(ctx, request)
		if err != nil {
			send(ctx, events, Event{Err: fmt.Errorf("open pod logs: %w", err)})
			return
		}
		var closeOnce sync.Once
		closeStream := func() { closeOnce.Do(func() { _ = stream.Close() }) }
		defer closeStream()
		finished := make(chan struct{})
		defer close(finished)
		go func() {
			select {
			case <-ctx.Done():
				closeStream()
			case <-finished:
			}
		}()

		// The scanner runs apart so a quiet followed stream still flushes the
		// lines it has, instead of holding them until a batch fills.
		lines := make(chan string, streamBatchSize)
		var scanErr error
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(stream)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				select {
				case lines <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
			scanErr = scanner.Err()
		}()
		for {
			batch, open := collectBatch(lines)
			if len(batch) > 0 && !send(ctx, events, Event{Lines: batch}) {
				return
			}
			if !open {
				break
			}
		}
		if scanErr != nil && ctx.Err() == nil {
			send(ctx, events, Event{Err: fmt.Errorf("read pod logs: %w", scanErr)})
			return
		}
		if ctx.Err() == nil {
			send(ctx, events, Event{Ended: true})
		}
	}()
	return events
}

// collectBatch waits for a line, then takes the lines that follow within
// streamFlushDelay, up to streamBatchSize. It reports false once lines is closed.
func collectBatch(lines <-chan string) ([]string, bool) {
	line, open := <-lines
	if !open {
		return nil, false
	}
	batch := append(make([]string, 0, streamBatchSize), line)
	timer := time.NewTimer(streamFlushDelay)
	defer timer.Stop()
	for len(batch) < streamBatchSize {
		select {
		case line, open = <-lines:
			if !open {
				return batch, false
			}
			batch = append(batch, line)
		case <-timer.C:
			return batch, true
		}
	}
	return batch, true
}

func (service *Service) openPodLog(ctx context.Context, request Request) (io.ReadCloser, error) {
	options := &corev1.PodLogOptions{
		Container:  request.Container,
		Follow:     request.Follow && !request.Previous,
		Previous:   request.Previous,
		Timestamps: request.Timestamps,
	}
	if request.TailLines > 0 {
		options.TailLines = &request.TailLines
	}
	return service.client.CoreV1().Pods(request.Namespace).GetLogs(request.Pod, options).Stream(ctx)
}

func normalizePod(pod *corev1.Pod) Pod {
	restarts := make(map[string]int32)
	for _, status := range pod.Status.InitContainerStatuses {
		restarts[status.Name] = status.RestartCount
	}
	for _, status := range pod.Status.ContainerStatuses {
		restarts[status.Name] = status.RestartCount
	}
	containers := make([]Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	for _, container := range pod.Spec.InitContainers {
		restartCount := restarts[container.Name]
		containers = append(containers, Container{Name: container.Name, Kind: "init", RestartCount: restartCount, HasPrevious: restartCount > 0})
	}
	for _, container := range pod.Spec.Containers {
		restartCount := restarts[container.Name]
		kind := "sidecar"
		switch container.Name {
		case "main":
			kind = "main"
		case "wait":
			kind = "wait"
		}
		containers = append(containers, Container{Name: container.Name, Kind: kind, RestartCount: restartCount, HasPrevious: restartCount > 0})
	}
	sort.SliceStable(containers, func(i, j int) bool {
		left, right := containerRank(containers[i]), containerRank(containers[j])
		if left != right {
			return left < right
		}
		return containers[i].Name < containers[j].Name
	})
	return Pod{Namespace: pod.Namespace, Name: pod.Name, Phase: string(pod.Status.Phase), Containers: containers}
}

func containerRank(container Container) int {
	switch strings.ToLower(container.Kind) {
	case "main":
		return 0
	case "wait":
		return 1
	case "init":
		return 2
	default:
		return 3
	}
}

func send(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}
