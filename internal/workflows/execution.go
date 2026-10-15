package workflows

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
)

type Execution struct {
	Namespace       string
	Name            string
	UID             string
	ResourceVersion string
	Phase           string
	Message         string
	Nodes           map[string]Node
	Roots           []string
}

type Node struct {
	ID           string
	Name         string
	DisplayName  string
	Type         string
	Phase        string
	Message      string
	Template     string
	TemplateRef  string
	BoundaryID   string
	Children     []string
	Parents      []string
	Outbound     []string
	StartedAt    time.Time
	FinishedAt   time.Time
	Progress     string
	HostNodeName string
	PodIP        string
	Retried      bool
	Hooked       bool
	Inputs       []Value
	Outputs      []Value
	Artifacts    []Artifact
	Result       string
	ExitCode     string
}

type Value struct {
	Name  string
	Value string
}

type Artifact struct {
	Direction string
	Name      string
	Path      string
	Location  string
}

type TreeRow struct {
	Node  Node
	Depth int
	Cycle bool
}

type Edge struct {
	From string
	To   string
}

type ExecutionUpdate struct {
	Generation uint64
	Execution  Execution
	Stale      bool
	Err        error
}

func (repository *Repository) GetExecution(ctx context.Context, namespace, name, expectedUID string) (Execution, error) {
	workflow, err := repository.workflows.Workflows(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Execution{}, fmt.Errorf("get workflow %s/%s: %w", namespace, name, err)
	}
	if expectedUID != "" && string(workflow.UID) != expectedUID {
		return Execution{}, fmt.Errorf("workflow identity changed for %s/%s", namespace, name)
	}
	return NormalizeExecution(workflow), nil
}

func (repository *Repository) StartExecution(ctx context.Context, generation uint64, namespace, name, expectedUID string) <-chan ExecutionUpdate {
	updates := make(chan ExecutionUpdate, 1)
	go func() {
		defer close(updates)
		client := repository.workflows.Workflows(namespace)
		workflow, err := client.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Err: err})
			return
		}
		if expectedUID != "" && string(workflow.UID) != expectedUID {
			sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Err: fmt.Errorf("workflow identity changed for %s/%s", namespace, name)})
			return
		}
		sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Execution: NormalizeExecution(workflow)})

		resourceVersion := workflow.ResourceVersion
		attempt := 0
		for ctx.Err() == nil {
			stream, watchErr := client.Watch(ctx, metav1.ListOptions{
				FieldSelector:   fields.OneTermEqualSelector("metadata.name", name).String(),
				ResourceVersion: resourceVersion,
			})
			if watchErr == nil {
				watchErr = consumeExecutionWatch(ctx, stream, generation, expectedUID, updates, &resourceVersion)
			}
			if ctx.Err() != nil {
				return
			}
			sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Stale: true, Err: watchErr})
			if !repository.retryWait(ctx, attempt) {
				return
			}
			attempt++
			workflow, err = client.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			if expectedUID != "" && string(workflow.UID) != expectedUID {
				sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Err: fmt.Errorf("workflow identity changed for %s/%s", namespace, name)})
				return
			}
			resourceVersion = workflow.ResourceVersion
			attempt = 0
			sendExecution(ctx, updates, ExecutionUpdate{Generation: generation, Execution: NormalizeExecution(workflow)})
		}
	}()
	return updates
}

func consumeExecutionWatch(ctx context.Context, stream watch.Interface, generation uint64, expectedUID string, output chan<- ExecutionUpdate, resourceVersion *string) error {
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-stream.ResultChan():
			if !open {
				return fmt.Errorf("workflow execution watch closed")
			}
			if event.Type == watch.Error {
				return fmt.Errorf("workflow execution watch failed")
			}
			workflow, ok := event.Object.(*wfv1.Workflow)
			if !ok {
				continue
			}
			if expectedUID != "" && string(workflow.UID) != expectedUID {
				return fmt.Errorf("workflow identity changed for %s/%s", workflow.Namespace, workflow.Name)
			}
			if event.Type == watch.Deleted {
				return fmt.Errorf("workflow %s/%s was deleted", workflow.Namespace, workflow.Name)
			}
			*resourceVersion = workflow.ResourceVersion
			sendExecution(ctx, output, ExecutionUpdate{Generation: generation, Execution: NormalizeExecution(workflow)})
		}
	}
}

func sendExecution(ctx context.Context, output chan<- ExecutionUpdate, update ExecutionUpdate) {
	select {
	case <-ctx.Done():
	case output <- update:
	}
}

func NormalizeExecution(workflow *wfv1.Workflow) Execution {
	if workflow == nil {
		return Execution{Nodes: map[string]Node{}}
	}
	execution := Execution{
		Namespace:       workflow.Namespace,
		Name:            workflow.Name,
		UID:             string(workflow.UID),
		ResourceVersion: workflow.ResourceVersion,
		Phase:           string(workflow.Status.Phase),
		Message:         workflow.Status.Message,
		Nodes:           make(map[string]Node, len(workflow.Status.Nodes)),
	}
	if len(workflow.Status.Nodes) == 0 && workflow.Status.OffloadNodeStatusVersion != "" {
		execution.Message = strings.TrimSpace(execution.Message + " Node status is offloaded and unavailable through the Kubernetes object.")
	}
	for id, status := range workflow.Status.Nodes {
		if status.ID == "" {
			status.ID = id
		}
		execution.Nodes[id] = normalizeNode(status)
	}

	// Preserve references to malformed or forward-version nodes visibly rather
	// than dropping edges or panicking during rendering.
	for _, node := range execution.Nodes {
		for _, childID := range node.Children {
			if _, found := execution.Nodes[childID]; !found {
				execution.Nodes[childID] = Node{ID: childID, Name: childID, DisplayName: childID, Type: "Unknown", Phase: "Unknown"}
			}
		}
	}
	for parentID, node := range execution.Nodes {
		for _, childID := range node.Children {
			child := execution.Nodes[childID]
			child.Parents = appendUnique(child.Parents, parentID)
			execution.Nodes[childID] = child
		}
	}
	for id, node := range execution.Nodes {
		sort.Strings(node.Parents)
		execution.Nodes[id] = node
		if len(node.Parents) == 0 {
			execution.Roots = append(execution.Roots, id)
		}
	}
	sort.SliceStable(execution.Roots, func(i, j int) bool {
		left, right := execution.Nodes[execution.Roots[i]], execution.Nodes[execution.Roots[j]]
		if !left.StartedAt.Equal(right.StartedAt) {
			if left.StartedAt.IsZero() {
				return false
			}
			if right.StartedAt.IsZero() {
				return true
			}
			return left.StartedAt.Before(right.StartedAt)
		}
		return nodeLabel(left) < nodeLabel(right)
	})
	if len(execution.Roots) == 0 && len(execution.Nodes) > 0 {
		for id := range execution.Nodes {
			execution.Roots = append(execution.Roots, id)
		}
		sort.Strings(execution.Roots)
	}
	return execution
}

func normalizeNode(status wfv1.NodeStatus) Node {
	node := Node{
		ID:           status.ID,
		Name:         status.Name,
		DisplayName:  status.DisplayName,
		Type:         string(status.Type),
		Phase:        string(status.Phase),
		Message:      status.Message,
		Template:     status.TemplateName,
		BoundaryID:   status.BoundaryID,
		Children:     append([]string(nil), status.Children...),
		Outbound:     append([]string(nil), status.OutboundNodes...),
		StartedAt:    status.StartedAt.Time,
		FinishedAt:   status.FinishedAt.Time,
		Progress:     string(status.Progress),
		HostNodeName: status.HostNodeName,
		PodIP:        status.PodIP,
	}
	if status.TemplateRef != nil {
		node.TemplateRef = status.TemplateRef.Name
	}
	if status.NodeFlag != nil {
		node.Retried = status.NodeFlag.Retried
		node.Hooked = status.NodeFlag.Hooked
	}
	if status.Inputs != nil {
		node.Inputs = parameterValues(status.Inputs.Parameters)
		node.Artifacts = append(node.Artifacts, artifactValues("input", status.Inputs.Artifacts)...)
	}
	if status.Outputs != nil {
		node.Outputs = parameterValues(status.Outputs.Parameters)
		node.Artifacts = append(node.Artifacts, artifactValues("output", status.Outputs.Artifacts)...)
		if status.Outputs.Result != nil {
			node.Result = *status.Outputs.Result
		}
		if status.Outputs.ExitCode != nil {
			node.ExitCode = *status.Outputs.ExitCode
		}
	}
	return node
}

func parameterValues(parameters []wfv1.Parameter) []Value {
	values := make([]Value, 0, len(parameters))
	for _, parameter := range parameters {
		value := ""
		switch {
		case parameter.Value != nil:
			value = parameter.Value.String()
		case parameter.Default != nil:
			value = parameter.Default.String()
		case parameter.ValueFrom != nil:
			value = "valueFrom"
		}
		values = append(values, Value{Name: parameter.Name, Value: value})
	}
	return values
}

func artifactValues(direction string, artifacts wfv1.Artifacts) []Artifact {
	values := make([]Artifact, 0, len(artifacts))
	for index := range artifacts {
		artifact := &artifacts[index]
		location, _ := artifact.ArtifactLocation.GetKey()
		if location == "" {
			location = artifact.From
		}
		values = append(values, Artifact{
			Direction: direction,
			Name:      artifact.Name,
			Path:      artifact.Path,
			Location:  location,
		})
	}
	return values
}

func (execution Execution) TreeRows() []TreeRow {
	rows := make([]TreeRow, 0, len(execution.Nodes))
	visited := make(map[string]bool, len(execution.Nodes))
	active := make(map[string]bool, len(execution.Nodes))
	var visit func(string, int)
	visit = func(id string, depth int) {
		node, found := execution.Nodes[id]
		if !found {
			return
		}
		if active[id] {
			rows = append(rows, TreeRow{Node: node, Depth: depth, Cycle: true})
			return
		}
		if visited[id] {
			return
		}
		visited[id] = true
		active[id] = true
		rows = append(rows, TreeRow{Node: node, Depth: depth})
		for _, childID := range node.Children {
			visit(childID, depth+1)
		}
		delete(active, id)
	}
	for _, rootID := range execution.Roots {
		visit(rootID, 0)
	}
	remaining := make([]string, 0)
	for id := range execution.Nodes {
		if !visited[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	for _, id := range remaining {
		visit(id, 0)
	}
	return rows
}

func (execution Execution) TableNodes() []Node {
	nodes := make([]Node, 0, len(execution.Nodes))
	for _, node := range execution.Nodes {
		nodes = append(nodes, node)
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Phase != nodes[j].Phase {
			return phaseRankForNode(nodes[i].Phase) < phaseRankForNode(nodes[j].Phase)
		}
		return nodeLabel(nodes[i]) < nodeLabel(nodes[j])
	})
	return nodes
}

func (execution Execution) Edges() []Edge {
	edges := make([]Edge, 0)
	for parentID, node := range execution.Nodes {
		for _, childID := range node.Children {
			edges = append(edges, Edge{From: parentID, To: childID})
		}
	}
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return edges
}

func (node Node) Duration(now time.Time) time.Duration {
	if node.StartedAt.IsZero() {
		return 0
	}
	end := node.FinishedAt
	if end.IsZero() {
		end = now
	}
	if end.Before(node.StartedAt) {
		return 0
	}
	return end.Sub(node.StartedAt)
}

func (node Node) Label() string {
	return nodeLabel(node)
}

func nodeLabel(node Node) string {
	if node.DisplayName != "" {
		return node.DisplayName
	}
	if node.Name != "" {
		parts := strings.Split(node.Name, ".")
		return parts[len(parts)-1]
	}
	return node.ID
}

func phaseRankForNode(phase string) int {
	switch strings.ToLower(phase) {
	case "error", "failed":
		return 0
	case "running":
		return 1
	case "pending":
		return 2
	case "succeeded":
		return 3
	case "skipped", "omitted":
		return 4
	default:
		return 5
	}
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
