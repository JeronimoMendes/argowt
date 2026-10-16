package workflows

import (
	"fmt"
	"slices"
	"strings"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The retry and resubmit logic below follows FormulateRetryWorkflow and
// FormulateResubmitWorkflow in Argo Workflows v3.7 (workflow/util/util.go,
// Apache License 2.0). Importing that package would pull in the Argo server's
// database drivers and grow the binary by about a third, so the parts argowt
// needs are kept here instead.

const (
	PhaseLabel            = "workflows.argoproj.io/phase"
	ArchivingStatusLabel  = "workflows.argoproj.io/workflow-archiving-status"
	ResubmittedFromLabel  = "workflows.argoproj.io/resubmitted-from-workflow"
	creatorLabel          = "workflows.argoproj.io/creator"
	creatorEmailLabel     = "workflows.argoproj.io/creator-email"
	creatorPreferredLabel = "workflows.argoproj.io/creator-preferred-username"
	workflowPodLabel      = "workflows.argoproj.io/workflow"
	nodeIDAnnotation      = "workflows.argoproj.io/node-id"
)

// formulateRetry resets the failed nodes of a Failed or Error workflow so the
// controller runs them again. It returns the updated workflow and the IDs of
// the pod nodes whose pods must be deleted before the update is applied.
func formulateRetry(workflow *wfv1.Workflow) (*wfv1.Workflow, []string, error) {
	switch workflow.Status.Phase {
	case wfv1.WorkflowFailed, wfv1.WorkflowError:
	default:
		return nil, nil, fmt.Errorf("only Failed or Error workflows can be retried; this one is %s", displayWorkflowPhase(workflow))
	}
	if err := requireNodeStatus(workflow); err != nil {
		return nil, nil, err
	}

	onExitNodeName := workflow.Name + ".onExit"
	retried := newRetryWorkflow(workflow)

	deleteNodeIDs := make(map[string]bool)
	for nodeID, node := range workflow.Status.Nodes {
		if !node.FailedOrError() || !isExecutionNodeType(node.Type) {
			continue
		}
		// A retried attempt is judged by its Retry parent.
		if node.NodeFlag != nil && node.NodeFlag.Retried {
			parent := workflow.Status.Nodes.Find(func(candidate wfv1.NodeStatus) bool { return candidate.HasChild(node.ID) })
			if parent != nil {
				node = *parent
			}
		}
		if !descendantSucceeded(workflow, node, map[string]bool{}) {
			deleteNodeIDs[nodeID] = true
		}
	}

	nodes, err := newRetryDAG(workflow)
	if err != nil {
		return nil, nil, err
	}
	nodes, err = sortedRetryDAG(nodes, workflow.Name)
	if err != nil {
		return nil, nil, err
	}

	// Reset paths in topological order: once an ancestor is deleted, its
	// descendants are deleted with it and need no path of their own.
	toReset := make(map[string]bool)
	toDelete := make(map[string]bool)
	for _, node := range nodes {
		if !deleteNodeIDs[node.status.ID] || toDelete[node.status.ID] {
			continue
		}
		pathReset, pathDelete, err := resetPath(nodes, node.status.ID)
		if err != nil {
			return nil, nil, err
		}
		mergeSet(toReset, pathReset)
		mergeSet(toDelete, pathDelete)
	}

	for nodeID := range toReset {
		if toDelete[nodeID] {
			continue
		}
		node := workflow.Status.Nodes[nodeID]
		retried.Status.Nodes.Set(nodeID, resetNode(*node.DeepCopy()))
	}

	podNodes := make(map[string]bool)
	for nodeID := range toDelete {
		if workflow.Status.Nodes[nodeID].Type == wfv1.NodeTypePod {
			podNodes[nodeID] = true
		}
	}
	for id, node := range workflow.Status.Nodes {
		exitNode := strings.HasPrefix(node.Name, onExitNodeName)
		if !retried.Status.Nodes.Has(id) && !toDelete[id] && !exitNode {
			retried.Status.Nodes.Set(id, *node.DeepCopy())
		}
		if node.Name != onExitNodeName {
			continue
		}
		// The exit handler runs again, so its pods go too.
		queue := []wfv1.NodeStatus{node}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			podNodes[current.ID] = true
			for _, childID := range current.Children {
				child, err := workflow.Status.Nodes.Get(childID)
				if err != nil {
					return nil, nil, err
				}
				queue = append(queue, *child)
			}
		}
	}

	for id, previous := range workflow.Status.Nodes {
		if !retried.Status.Nodes.Has(id) {
			continue
		}
		node := retried.Status.Nodes[id]
		node.Children = slices.DeleteFunc(slices.Clone(previous.Children), func(childID string) bool { return toDelete[childID] })
		node.OutboundNodes = slices.DeleteFunc(slices.Clone(previous.OutboundNodes), func(nodeID string) bool { return toDelete[nodeID] })
		retried.Status.Nodes.Set(id, node)
	}

	podNodeIDs := make([]string, 0, len(podNodes))
	for nodeID := range podNodes {
		podNodeIDs = append(podNodeIDs, nodeID)
	}
	slices.Sort(podNodeIDs)
	return retried, podNodeIDs, nil
}

func newRetryWorkflow(workflow *wfv1.Workflow) *wfv1.Workflow {
	retried := workflow.DeepCopy()
	if retried.Labels == nil {
		retried.Labels = make(map[string]string)
	}
	delete(retried.Labels, CompletedLabel)
	delete(retried.Labels, ArchivingStatusLabel)
	retried.Labels[PhaseLabel] = string(wfv1.NodeRunning)
	retried.Status.Conditions.UpsertCondition(wfv1.Condition{Status: metav1.ConditionFalse, Type: wfv1.ConditionTypeCompleted})
	retried.Status.Phase = wfv1.WorkflowRunning
	retried.Status.Nodes = make(wfv1.Nodes)
	retried.Status.Message = ""
	retried.Status.StartedAt = metav1.Time{Time: time.Now().UTC()}
	retried.Status.FinishedAt = metav1.Time{}
	if retried.Status.StoredWorkflowSpec != nil {
		retried.Status.StoredWorkflowSpec.Shutdown = ""
	}
	retried.Spec.Shutdown = ""
	retried.Status.PersistentVolumeClaims = []corev1.Volume{}
	// A terminated workflow has a zero deadline; keep it from ending at once.
	if retried.Spec.ActiveDeadlineSeconds != nil && *retried.Spec.ActiveDeadlineSeconds == 0 {
		retried.Spec.ActiveDeadlineSeconds = nil
	}
	return retried
}

// formulateResubmit builds a new workflow that runs the spec of an existing
// one from the start.
func formulateResubmit(workflow *wfv1.Workflow) *wfv1.Workflow {
	resubmitted := &wfv1.Workflow{TypeMeta: workflow.TypeMeta}
	resubmitted.GenerateName = workflow.GenerateName
	if resubmitted.GenerateName == "" {
		resubmitted.GenerateName = workflow.Name + "-"
	}
	resubmitted.Namespace = workflow.Namespace
	resubmitted.Spec = *workflow.Spec.DeepCopy()
	if resubmitted.Spec.ActiveDeadlineSeconds != nil && *resubmitted.Spec.ActiveDeadlineSeconds == 0 {
		resubmitted.Spec.ActiveDeadlineSeconds = nil
	}
	resubmitted.Spec.Shutdown = ""

	resubmitted.Labels = make(map[string]string)
	for key, value := range workflow.Labels {
		switch key {
		case creatorLabel, creatorEmailLabel, creatorPreferredLabel, PhaseLabel, CompletedLabel, ArchivingStatusLabel:
		default:
			resubmitted.Labels[key] = value
		}
	}
	resubmitted.Labels[ResubmittedFromLabel] = workflow.Name
	resubmitted.Annotations = make(map[string]string, len(workflow.Annotations))
	for key, value := range workflow.Annotations {
		resubmitted.Annotations[key] = value
	}
	resubmitted.OwnerReferences = append(resubmitted.OwnerReferences, workflow.OwnerReferences...)
	return resubmitted
}

// requireNodeStatus rejects workflows whose nodes are compressed or offloaded
// to the Argo server database: argowt cannot read or rewrite those.
func requireNodeStatus(workflow *wfv1.Workflow) error {
	if len(workflow.Status.Nodes) > 0 {
		return nil
	}
	if workflow.Status.CompressedNodes != "" {
		return fmt.Errorf("node status of %s/%s is compressed; use the Argo CLI or UI for this action", workflow.Namespace, workflow.Name)
	}
	if workflow.Status.OffloadNodeStatusVersion != "" {
		return fmt.Errorf("node status of %s/%s is offloaded to the Argo database; use the Argo CLI or UI for this action", workflow.Namespace, workflow.Name)
	}
	return nil
}

func descendantSucceeded(workflow *wfv1.Workflow, node wfv1.NodeStatus, ignored map[string]bool) bool {
	for _, childID := range node.Children {
		child, err := workflow.Status.Nodes.Get(childID)
		if err != nil {
			continue
		}
		if (!ignored[childID] && child.Phase == wfv1.NodeSucceeded) || descendantSucceeded(workflow, *child, ignored) {
			return true
		}
	}
	return false
}

type retryNode struct {
	status   *wfv1.NodeStatus
	parent   *retryNode
	children []*retryNode
}

func newRetryDAG(workflow *wfv1.Workflow) ([]*retryNode, error) {
	nodes := make(map[string]*retryNode, len(workflow.Status.Nodes))
	parents := make(map[string]string)
	for id := range workflow.Status.Nodes {
		status := workflow.Status.Nodes[id]
		nodes[status.ID] = &retryNode{status: &status}
		for _, childID := range status.Children {
			parents[childID] = status.ID
		}
	}
	for _, node := range nodes {
		parentID, found := parents[node.status.ID]
		if !found && node.status.Name != workflow.Name && !strings.HasPrefix(node.status.Name, workflow.Name+".onExit") {
			return nil, fmt.Errorf("cannot retry: node %s has no parent", node.status.ID)
		}
		if found {
			node.parent = nodes[parentID]
		}
		for _, childID := range node.status.Children {
			child, found := nodes[childID]
			if !found {
				return nil, fmt.Errorf("cannot retry: child node %s is missing", childID)
			}
			node.children = append(node.children, child)
		}
	}
	values := make([]*retryNode, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node)
	}
	return values, nil
}

// sortedRetryDAG orders nodes breadth first from the root, leaving out the
// exit handler.
func sortedRetryDAG(nodes []*retryNode, rootName string) ([]*retryNode, error) {
	var queue []*retryNode
	for _, node := range nodes {
		if node.status.Name == rootName {
			queue = append(queue, node)
			break
		}
	}
	if len(queue) != 1 {
		return nil, fmt.Errorf("cannot retry: the root node is missing")
	}
	sorted := make([]*retryNode, 0, len(nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		sorted = append(sorted, current)
		queue = append(queue, current.children...)
	}
	return sorted, nil
}

func resetPath(nodes []*retryNode, startID string) (map[string]bool, map[string]bool, error) {
	var start *retryNode
	for _, node := range nodes {
		if node.status.ID == startID {
			start = node
			break
		}
	}
	if start == nil {
		return nil, nil, fmt.Errorf("cannot retry: node %s is missing", startID)
	}
	toDelete := descendants(start)
	toDelete[start.status.ID] = true
	toReset := make(map[string]bool)
	if start.parent == nil {
		return toReset, toDelete, nil
	}
	reset := func(id string) { toReset[id] = true }

	// Deletions win over resets, so resetting the start node is harmless.
	var err error
	current := start
	for current != nil {
		switch {
		case isGroupNodeType(current.status.Type):
			reset(current.status.ID)
			current, err = resetBoundaries(current, reset)
			if err != nil {
				return nil, nil, err
			}
			continue
		case current.status.Type == wfv1.NodeTypeRetry:
			reset(current.status.ID)
		case current.status.Type == wfv1.NodeTypeContainer:
			// A container node lives inside a pod node, which runs again whole.
			current, err = resetUntil(current, func(node *retryNode) bool { return node.status.Type == wfv1.NodeTypePod }, reset)
			if err != nil {
				return nil, nil, err
			}
			toDelete[current.status.ID] = true
			for id := range descendants(current) {
				toDelete[id] = true
			}
			continue
		}
		current = current.parent
	}
	return toReset, toDelete, nil
}

func resetBoundaries(node *retryNode, reset func(string)) (*retryNode, error) {
	current := node
	for current != nil {
		if current.parent != nil && current.parent.status.Type == wfv1.NodeTypeRetry {
			reset(current.parent.status.ID)
			current = current.parent
		}
		if current.parent != nil && current.parent.status.Type == wfv1.NodeTypeStepGroup {
			reset(current.parent.status.ID)
		}
		boundaryID := current.status.BoundaryID
		if boundaryID == "" {
			return current.parent, nil
		}
		var err error
		current, err = resetUntil(current, func(candidate *retryNode) bool { return candidate.status.ID == boundaryID }, reset)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func resetUntil(node *retryNode, match func(*retryNode) bool, reset func(string)) (*retryNode, error) {
	for current := node; current != nil; current = current.parent {
		if match(current) {
			reset(current.status.ID)
			return current, nil
		}
	}
	return nil, fmt.Errorf("cannot retry: no matching ancestor of node %s", node.status.ID)
}

func descendants(node *retryNode) map[string]bool {
	found := make(map[string]bool)
	queue := []*retryNode{node}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range current.children {
			if found[child.status.ID] {
				continue
			}
			found[child.status.ID] = true
			queue = append(queue, child)
		}
	}
	return found
}

func resetNode(node wfv1.NodeStatus) wfv1.NodeStatus {
	// A suspend node waits for its supplied outputs again.
	if node.Type == wfv1.NodeTypeSuspend && node.Outputs != nil {
		for index, parameter := range node.Outputs.Parameters {
			node.Outputs.Parameters[index] = wfv1.Parameter{
				Name:      parameter.Name,
				ValueFrom: &wfv1.ValueFrom{Supplied: &wfv1.SuppliedValueFrom{}},
			}
		}
	}
	// Skipped nodes stay skipped, or the workflow would wait on them forever.
	if node.Phase != wfv1.NodeSkipped {
		node.Phase = wfv1.NodeRunning
	}
	node.Message = ""
	node.StartedAt = metav1.Time{Time: time.Now().UTC()}
	node.FinishedAt = metav1.Time{}
	return node
}

func isGroupNodeType(nodeType wfv1.NodeType) bool {
	return nodeType == wfv1.NodeTypeDAG || nodeType == wfv1.NodeTypeTaskGroup || nodeType == wfv1.NodeTypeStepGroup || nodeType == wfv1.NodeTypeSteps
}

func isExecutionNodeType(nodeType wfv1.NodeType) bool {
	return nodeType == wfv1.NodeTypeContainer || nodeType == wfv1.NodeTypePod || nodeType == wfv1.NodeTypeHTTP || nodeType == wfv1.NodeTypePlugin
}

func mergeSet(target, source map[string]bool) {
	for key, value := range source {
		if _, found := target[key]; !found {
			target[key] = value
		}
	}
}
