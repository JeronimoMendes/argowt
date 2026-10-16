package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argoclient "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

type Operation string

const (
	OperationRetry     Operation = "retry"
	OperationResubmit  Operation = "resubmit"
	OperationSuspend   Operation = "suspend"
	OperationResume    Operation = "resume"
	OperationStop      Operation = "stop"
	OperationTerminate Operation = "terminate"
	OperationDelete    Operation = "delete"
)

// Operations lists every workflow operation in menu order.
func Operations() []Operation {
	return []Operation{OperationRetry, OperationResubmit, OperationSuspend, OperationResume, OperationStop, OperationTerminate, OperationDelete}
}

// Title is the operation name for menus, e.g. "Retry".
func (operation Operation) Title() string {
	if operation == "" {
		return ""
	}
	return strings.ToUpper(string(operation[:1])) + string(operation[1:])
}

// Progressive is the operation in progress, e.g. "Stopping".
func (operation Operation) Progressive() string {
	return operationWords[operation][0]
}

// Past is the completed operation, e.g. "Stopped".
func (operation Operation) Past() string {
	return operationWords[operation][1]
}

var operationWords = map[Operation][2]string{
	OperationRetry:     {"Retrying", "Retried"},
	OperationResubmit:  {"Resubmitting", "Resubmitted"},
	OperationSuspend:   {"Suspending", "Suspended"},
	OperationResume:    {"Resuming", "Resumed"},
	OperationStop:      {"Stopping", "Stopped"},
	OperationTerminate: {"Terminating", "Terminated"},
	OperationDelete:    {"Deleting", "Deleted"},
}

// Description says what the operation does, for confirmations.
func (operation Operation) Description() string {
	switch operation {
	case OperationRetry:
		return "Runs the failed nodes again and deletes their pods; succeeded nodes are kept."
	case OperationResubmit:
		return "Creates a new workflow from this workflow's spec."
	case OperationSuspend:
		return "Pauses the workflow; running pods finish, and no new steps start."
	case OperationResume:
		return "Continues a suspended workflow and approves any waiting suspend steps."
	case OperationStop:
		return "Stops the workflow; running pods are stopped and exit handlers run."
	case OperationTerminate:
		return "Terminates the workflow at once; exit handlers do not run."
	case OperationDelete:
		return "Deletes the workflow and its pods. This cannot be undone."
	default:
		return ""
	}
}

// Access is one Kubernetes permission an operation needs.
type Access struct {
	Group    string
	Resource string
	Verb     string
}

// RequiredAccess lists the permissions an operation needs in the workflow's
// namespace.
func RequiredAccess(operation Operation) []Access {
	workflows := func(verb string) Access { return Access{Group: "argoproj.io", Resource: "workflows", Verb: verb} }
	switch operation {
	case OperationRetry:
		return []Access{workflows("update"), {Resource: "pods", Verb: "list"}, {Resource: "pods", Verb: "delete"}}
	case OperationResubmit:
		return []Access{workflows("create")}
	case OperationDelete:
		return []Access{workflows("delete")}
	default:
		return []Access{workflows("update")}
	}
}

// AccessReviewer answers whether the current user holds a permission.
type AccessReviewer interface {
	Allowed(ctx context.Context, namespace string, access Access) (bool, error)
}

// CheckOperations reports which operations RBAC allows in a namespace. An
// operation is left out when its permission could not be checked.
func CheckOperations(ctx context.Context, reviewer AccessReviewer, namespace string) map[Operation]bool {
	answers := make(map[Access]bool)
	failed := make(map[Access]bool)
	result := make(map[Operation]bool)
	for _, operation := range Operations() {
		allowed, known := true, true
		for _, access := range RequiredAccess(operation) {
			if failed[access] {
				known = false
				continue
			}
			answer, found := answers[access]
			if !found {
				var err error
				answer, err = reviewer.Allowed(ctx, namespace, access)
				if err != nil {
					failed[access] = true
					known = false
					continue
				}
				answers[access] = answer
			}
			allowed = allowed && answer
		}
		if known || !allowed {
			result[operation] = allowed
		}
	}
	return result
}

// OperationTarget identifies the workflow a user confirmed an operation on,
// with the state they saw.
type OperationTarget struct {
	Namespace       string
	Name            string
	UID             string
	ResourceVersion string
	Phase           string
	Suspended       bool
	SuspendNode     bool
}

func TargetFromSummary(summary Summary) OperationTarget {
	return OperationTarget{
		Namespace:       summary.Namespace,
		Name:            summary.Name,
		UID:             summary.UID,
		ResourceVersion: summary.ResourceVersion,
		Phase:           summary.Phase,
		Suspended:       summary.Suspended,
		SuspendNode:     summary.SuspendNode,
	}
}

func TargetFromExecution(execution Execution) OperationTarget {
	return OperationTarget{
		Namespace:       execution.Namespace,
		Name:            execution.Name,
		UID:             execution.UID,
		ResourceVersion: execution.ResourceVersion,
		Phase:           execution.Phase,
		Suspended:       execution.Suspended,
		SuspendNode:     execution.SuspendNode,
	}
}

func targetFromWorkflow(workflow *wfv1.Workflow) OperationTarget {
	return TargetFromSummary(Normalize(workflow))
}

// DisplayPhase is the phase shown to users.
func (target OperationTarget) DisplayPhase() string {
	return Summary{Phase: target.Phase, Suspended: target.Suspended}.DisplayPhase()
}

func (target OperationTarget) completed() bool {
	return !(Summary{Phase: target.Phase}).Active()
}

// Available reports whether an operation applies to the workflow's state,
// with the reason when it does not.
func (target OperationTarget) Available(operation Operation) (bool, string) {
	switch operation {
	case OperationRetry:
		if target.Phase != string(wfv1.WorkflowFailed) && target.Phase != string(wfv1.WorkflowError) {
			return false, "only Failed or Error workflows"
		}
	case OperationSuspend:
		if target.completed() {
			return false, "workflow has completed"
		}
		if target.Suspended {
			return false, "already suspended"
		}
	case OperationResume:
		if target.completed() {
			return false, "workflow has completed"
		}
		if !target.Suspended && !target.SuspendNode {
			return false, "workflow is not suspended"
		}
	case OperationStop, OperationTerminate:
		if target.completed() {
			return false, "workflow has completed"
		}
	}
	return true, ""
}

// ConflictError reports that the workflow changed after the user confirmed
// an operation. The operation was not applied and is never replayed.
type ConflictError struct {
	Operation Operation
	Namespace string
	Name      string
	Reason    string
}

func (err *ConflictError) Error() string {
	return fmt.Sprintf("%s not applied: %s/%s %s; review its current state and try again", err.Operation, err.Namespace, err.Name, err.Reason)
}

func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}

type OperationResult struct {
	Operation   Operation
	Namespace   string
	Name        string
	DeletedPods int
}

// OperationService applies workflow operations through the Kubernetes API,
// as the Argo server would, without needing the Argo server.
type OperationService struct {
	argo       argoclient.Interface
	kubernetes kubernetes.Interface
	now        func() time.Time
}

func NewOperationService(argo argoclient.Interface, kubernetes kubernetes.Interface) *OperationService {
	return &OperationService{argo: argo, kubernetes: kubernetes, now: time.Now}
}

// Run applies an operation to the confirmed target. It re-reads the
// workflow first and refuses to act when its identity, or the state the
// operation depends on, changed. Mutations carry the re-read resource
// version, so a change in between fails instead of being overwritten.
func (service *OperationService) Run(ctx context.Context, operation Operation, target OperationTarget) (OperationResult, error) {
	result := OperationResult{Operation: operation, Namespace: target.Namespace, Name: target.Name}
	conflict := func(reason string) error {
		return &ConflictError{Operation: operation, Namespace: target.Namespace, Name: target.Name, Reason: reason}
	}
	client := service.argo.ArgoprojV1alpha1().Workflows(target.Namespace)
	workflow, err := client.Get(ctx, target.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return result, conflict("no longer exists")
	}
	if err != nil {
		return result, fmt.Errorf("get workflow %s/%s: %w", target.Namespace, target.Name, err)
	}
	if target.UID != "" && string(workflow.UID) != target.UID {
		return result, conflict("was deleted and recreated")
	}
	current := targetFromWorkflow(workflow)
	if workflow.ResourceVersion != target.ResourceVersion {
		// Running workflows change constantly; only a change the operation
		// depends on counts as a conflict.
		if current.Phase != target.Phase || current.Suspended != target.Suspended || current.SuspendNode != target.SuspendNode {
			return result, conflict(fmt.Sprintf("changed from %s to %s", describeState(target), describeState(current)))
		}
	}
	if available, reason := current.Available(operation); !available {
		return result, fmt.Errorf("cannot %s %s/%s: %s", operation, target.Namespace, target.Name, reason)
	}

	switch operation {
	case OperationSuspend:
		workflow.Spec.Suspend = ptr.To(true)
		err = service.update(ctx, workflow)
	case OperationResume:
		err = resumeWorkflow(workflow, service.now())
		if err == nil {
			err = service.update(ctx, workflow)
		}
	case OperationStop:
		workflow.Spec.Shutdown = wfv1.ShutdownStrategyStop
		err = service.update(ctx, workflow)
	case OperationTerminate:
		workflow.Spec.Shutdown = wfv1.ShutdownStrategyTerminate
		err = service.update(ctx, workflow)
	case OperationDelete:
		uid, resourceVersion := workflow.UID, workflow.ResourceVersion
		err = client.Delete(ctx, workflow.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}})
	case OperationRetry:
		result.DeletedPods, err = service.retry(ctx, workflow)
	default:
		return result, fmt.Errorf("unsupported operation %q", operation)
	}
	if apierrors.IsConflict(err) {
		return result, conflict("changed while the operation was applied")
	}
	if err != nil {
		return result, fmt.Errorf("%s workflow %s/%s: %w", operation, target.Namespace, target.Name, err)
	}
	return result, nil
}

func (service *OperationService) update(ctx context.Context, workflow *wfv1.Workflow) error {
	_, err := service.argo.ArgoprojV1alpha1().Workflows(workflow.Namespace).Update(ctx, workflow, metav1.UpdateOptions{})
	return err
}

// retry resets the workflow and deletes the pods of the nodes that run again,
// as the Argo server does. A dry run first proves the update would be
// accepted, so pods are not deleted for an update that then conflicts.
func (service *OperationService) retry(ctx context.Context, workflow *wfv1.Workflow) (int, error) {
	retried, podNodeIDs, err := formulateRetry(workflow)
	if err != nil {
		return 0, err
	}
	client := service.argo.ArgoprojV1alpha1().Workflows(workflow.Namespace)
	if _, err := client.Update(ctx, retried, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}}); err != nil {
		return 0, err
	}
	deleted, err := service.deleteNodePods(ctx, workflow, podNodeIDs)
	if err != nil {
		return deleted, err
	}
	_, err = client.Update(ctx, retried, metav1.UpdateOptions{})
	return deleted, err
}

// deleteNodePods deletes the pods of the given nodes, matching them by the
// node annotation and the workflow owner so a recreated workflow's pods are
// never touched.
func (service *OperationService) deleteNodePods(ctx context.Context, workflow *wfv1.Workflow, nodeIDs []string) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	wanted := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		wanted[nodeID] = true
	}
	pods := service.kubernetes.CoreV1().Pods(workflow.Namespace)
	list, err := pods.List(ctx, metav1.ListOptions{LabelSelector: labels.Set{workflowPodLabel: workflow.Name}.String()})
	if err != nil {
		return 0, fmt.Errorf("list workflow pods: %w", err)
	}
	deleted := 0
	for _, pod := range list.Items {
		if !wanted[pod.Annotations[nodeIDAnnotation]] || !ownedBy(pod.OwnerReferences, string(workflow.UID)) {
			continue
		}
		uid := pod.UID
		err := pods.Delete(ctx, pod.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return deleted, fmt.Errorf("delete pod %s: %w", pod.Name, err)
		}
		deleted++
	}
	return deleted, nil
}

func ownedBy(owners []metav1.OwnerReference, uid string) bool {
	for _, owner := range owners {
		if string(owner.UID) == uid {
			return true
		}
	}
	return false
}

// resumeWorkflow clears spec.suspend and approves waiting suspend nodes, as
// `argo resume` does.
func resumeWorkflow(workflow *wfv1.Workflow, now time.Time) error {
	workflow.Spec.Suspend = nil
	for nodeID, node := range workflow.Status.Nodes {
		if !node.IsActiveSuspendNode() {
			continue
		}
		if node.Outputs != nil {
			for index, parameter := range node.Outputs.Parameters {
				if parameter.ValueFrom == nil || parameter.ValueFrom.Supplied == nil {
					continue
				}
				if parameter.ValueFrom.Default == nil {
					return fmt.Errorf("suspend node %s needs a value for output parameter %s; use the Argo CLI or UI to supply it", node.DisplayName, parameter.Name)
				}
				node.Outputs.Parameters[index].Value = parameter.ValueFrom.Default
				node.Outputs.Parameters[index].ValueFrom = nil
			}
		}
		node.Phase = wfv1.NodeSucceeded
		node.FinishedAt = metav1.Time{Time: now.UTC()}
		workflow.Status.Nodes.Set(nodeID, node)
	}
	return nil
}

func describeState(target OperationTarget) string {
	state := target.DisplayPhase()
	if target.SuspendNode && !target.Suspended {
		state += " (waiting on a suspend step)"
	}
	return state
}

func displayWorkflowPhase(workflow *wfv1.Workflow) string {
	return Normalize(workflow).DisplayPhase()
}
