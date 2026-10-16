package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argofake "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	argoclient "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	argov1 "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned/typed/workflow/v1alpha1"
)

// recordingArgo records the options of workflow writes, which the generated
// fake client drops.
type recordingArgo struct {
	*argofake.Clientset
	writes *[]string
}

// ArgoprojV1alpha1 wraps the fake Argo API group to record workflow writes.
func (client recordingArgo) ArgoprojV1alpha1() argov1.ArgoprojV1alpha1Interface {
	return recordingGroup{client.Clientset.ArgoprojV1alpha1(), client.writes}
}

type recordingGroup struct {
	argov1.ArgoprojV1alpha1Interface
	writes *[]string
}

// Workflows wraps the namespace client to record workflow updates and deletions.
func (group recordingGroup) Workflows(namespace string) argov1.WorkflowInterface {
	return recordingWorkflows{group.ArgoprojV1alpha1Interface.Workflows(namespace), group.writes}
}

type recordingWorkflows struct {
	argov1.WorkflowInterface
	writes *[]string
}

// Update records dry runs without persisting them and delegates real updates to the fake client.
func (client recordingWorkflows) Update(ctx context.Context, workflow *wfv1.Workflow, options metav1.UpdateOptions) (*wfv1.Workflow, error) {
	if len(options.DryRun) > 0 {
		*client.writes = append(*client.writes, "dry-run update")
		return workflow, nil
	}
	*client.writes = append(*client.writes, "update")
	return client.WorkflowInterface.Update(ctx, workflow, options)
}

// Delete records deletion preconditions before delegating to the fake client.
func (client recordingWorkflows) Delete(ctx context.Context, name string, options metav1.DeleteOptions) error {
	write := "delete"
	if options.Preconditions != nil && options.Preconditions.UID != nil && options.Preconditions.ResourceVersion != nil {
		write += " uid=" + string(*options.Preconditions.UID) + " version=" + *options.Preconditions.ResourceVersion
	}
	*client.writes = append(*client.writes, write)
	return client.WorkflowInterface.Delete(ctx, name, options)
}

var _ argoclient.Interface = recordingArgo{}

// runningWorkflow returns a running workflow fixture with a fixed UID and resource version.
func runningWorkflow() *wfv1.Workflow {
	return &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "etl", UID: "uid-1", ResourceVersion: "10"},
		Status:     wfv1.WorkflowStatus{Phase: wfv1.WorkflowRunning},
	}
}

// newOperationFixture creates an operation service with separate fake workflow and pod clients.
func newOperationFixture(objects ...runtime.Object) (*OperationService, *argofake.Clientset, *kubefake.Clientset) {
	var workflows, pods []runtime.Object
	for _, object := range objects {
		if _, ok := object.(*corev1.Pod); ok {
			pods = append(pods, object)
		} else {
			workflows = append(workflows, object)
		}
	}
	argo := argofake.NewSimpleClientset(workflows...)
	kubernetes := kubefake.NewSimpleClientset(pods...)
	return NewOperationService(argo, kubernetes), argo, kubernetes
}

// newRecordingFixture also returns the workflow writes and pod deletions in
// the order they were made.
func newRecordingFixture(objects ...runtime.Object) (*OperationService, *argofake.Clientset, *kubefake.Clientset, *[]string) {
	_, argo, kubernetes := newOperationFixture(objects...)
	writes := &[]string{}
	kubernetes.PrependReactor("delete", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		*writes = append(*writes, "delete pod "+action.(k8stesting.DeleteAction).GetName())
		return false, nil, nil
	})
	return NewOperationService(recordingArgo{argo, writes}, kubernetes), argo, kubernetes, writes
}

// storedWorkflow reads the fixture workflow from the fake client, failing the test on error.
func storedWorkflow(t *testing.T, client *argofake.Clientset) *wfv1.Workflow {
	t.Helper()
	workflow, err := client.ArgoprojV1alpha1().Workflows("argo").Get(context.Background(), "etl", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	return workflow
}

// mutations filters out read actions from the fake Argo client's recorded calls.
func mutations(client *argofake.Clientset) []k8stesting.Action {
	var result []k8stesting.Action
	for _, action := range client.Actions() {
		if action.GetVerb() != "get" && action.GetVerb() != "list" && action.GetVerb() != "watch" {
			result = append(result, action)
		}
	}
	return result
}

// TestAvailabilityFollowsWorkflowState checks available operations and denial reasons across workflow states.
func TestAvailabilityFollowsWorkflowState(t *testing.T) {
	cases := []struct {
		name      string
		target    OperationTarget
		available []Operation
	}{
		{"running", OperationTarget{Phase: "Running"}, []Operation{OperationResubmit, OperationSuspend, OperationStop, OperationTerminate, OperationDelete}},
		{"suspended", OperationTarget{Phase: "Running", Suspended: true}, []Operation{OperationResubmit, OperationResume, OperationStop, OperationTerminate, OperationDelete}},
		{"approval step", OperationTarget{Phase: "Running", SuspendNode: true}, []Operation{OperationResubmit, OperationSuspend, OperationResume, OperationStop, OperationTerminate, OperationDelete}},
		{"pending", OperationTarget{}, []Operation{OperationResubmit, OperationSuspend, OperationStop, OperationTerminate, OperationDelete}},
		{"failed", OperationTarget{Phase: "Failed"}, []Operation{OperationRetry, OperationResubmit, OperationDelete}},
		{"error", OperationTarget{Phase: "Error"}, []Operation{OperationRetry, OperationResubmit, OperationDelete}},
		{"succeeded", OperationTarget{Phase: "Succeeded"}, []Operation{OperationResubmit, OperationDelete}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var got []Operation
			for _, operation := range Operations() {
				if available, reason := test.target.Available(operation); available {
					got = append(got, operation)
				} else if reason == "" {
					t.Fatalf("%s is unavailable without a reason", operation)
				}
			}
			if len(got) != len(test.available) {
				t.Fatalf("available = %v, want %v", got, test.available)
			}
			for index := range got {
				if got[index] != test.available[index] {
					t.Fatalf("available = %v, want %v", got, test.available)
				}
			}
		})
	}
}

// TestSuspendStopAndTerminateUpdateSpec checks the spec changes and result identity for each operation.
func TestSuspendStopAndTerminateUpdateSpec(t *testing.T) {
	cases := []struct {
		operation Operation
		check     func(*wfv1.Workflow) bool
	}{
		{OperationSuspend, func(workflow *wfv1.Workflow) bool { return workflow.Spec.Suspend != nil && *workflow.Spec.Suspend }},
		{OperationStop, func(workflow *wfv1.Workflow) bool { return workflow.Spec.Shutdown == wfv1.ShutdownStrategyStop }},
		{OperationTerminate, func(workflow *wfv1.Workflow) bool { return workflow.Spec.Shutdown == wfv1.ShutdownStrategyTerminate }},
	}
	for _, test := range cases {
		t.Run(string(test.operation), func(t *testing.T) {
			workflow := runningWorkflow()
			service, client, _ := newOperationFixture(workflow)
			result, err := service.Run(context.Background(), test.operation, TargetFromSummary(Normalize(workflow)))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.Operation != test.operation || result.Name != "etl" {
				t.Fatalf("result = %+v", result)
			}
			if !test.check(storedWorkflow(t, client)) {
				t.Fatalf("workflow spec = %+v", storedWorkflow(t, client).Spec)
			}
		})
	}
}

// TestResumeClearsSuspendAndApprovesSuspendNodes checks that resume clears suspension and supplies default approval outputs.
func TestResumeClearsSuspendAndApprovesSuspendNodes(t *testing.T) {
	workflow := runningWorkflow()
	workflow.Spec.Suspend = ptr.To(true)
	workflow.Status.Nodes = wfv1.Nodes{
		"etl":      {ID: "etl", Name: "etl", Type: wfv1.NodeTypeSteps, Phase: wfv1.NodeRunning, Children: []string{"approve"}},
		"approve":  {ID: "approve", Name: "etl.approve", Type: wfv1.NodeTypeSuspend, Phase: wfv1.NodeRunning, Outputs: &wfv1.Outputs{Parameters: []wfv1.Parameter{{Name: "decision", ValueFrom: &wfv1.ValueFrom{Supplied: &wfv1.SuppliedValueFrom{}, Default: wfv1.AnyStringPtr("yes")}}}}},
		"finished": {ID: "finished", Name: "etl.finished", Type: wfv1.NodeTypeSuspend, Phase: wfv1.NodeSucceeded},
	}
	service, client, _ := newOperationFixture(workflow)
	if _, err := service.Run(context.Background(), OperationResume, TargetFromSummary(Normalize(workflow))); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	stored := storedWorkflow(t, client)
	if stored.Spec.Suspend != nil {
		t.Fatalf("suspend = %v", *stored.Spec.Suspend)
	}
	approve := stored.Status.Nodes["approve"]
	if approve.Phase != wfv1.NodeSucceeded || approve.FinishedAt.IsZero() {
		t.Fatalf("approve node = %+v", approve)
	}
	if output := approve.Outputs.Parameters[0]; output.Value == nil || output.Value.String() != "yes" || output.ValueFrom != nil {
		t.Fatalf("approve output = %+v", output)
	}
}

// TestResumeRefusesSuspendNodeWithoutOutputDefault checks that missing approval output defaults prevent any workflow write.
func TestResumeRefusesSuspendNodeWithoutOutputDefault(t *testing.T) {
	workflow := runningWorkflow()
	workflow.Status.Nodes = wfv1.Nodes{
		"approve": {ID: "approve", Name: "etl.approve", Type: wfv1.NodeTypeSuspend, Phase: wfv1.NodeRunning, Outputs: &wfv1.Outputs{Parameters: []wfv1.Parameter{{Name: "decision", ValueFrom: &wfv1.ValueFrom{Supplied: &wfv1.SuppliedValueFrom{}}}}}},
	}
	service, client, _ := newOperationFixture(workflow)
	if _, err := service.Run(context.Background(), OperationResume, TargetFromSummary(Normalize(workflow))); err == nil {
		t.Fatal("Run() expected an error")
	}
	if got := mutations(client); len(got) != 0 {
		t.Fatalf("mutations = %+v", got)
	}
}

// TestDeleteUsesIdentityAndVersionPreconditions checks that deletion carries the workflow UID and resource version.
func TestDeleteUsesIdentityAndVersionPreconditions(t *testing.T) {
	workflow := runningWorkflow()
	service, client, _, writes := newRecordingFixture(workflow)
	if _, err := service.Run(context.Background(), OperationDelete, TargetFromSummary(Normalize(workflow))); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(*writes) != 1 || (*writes)[0] != "delete uid=uid-1 version=10" {
		t.Fatalf("writes = %v", *writes)
	}
	if _, err := client.ArgoprojV1alpha1().Workflows("argo").Get(context.Background(), "etl", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("workflow still exists: %v", err)
	}
}

// TestRunRejectsRecreatedWorkflow checks that a changed UID produces a conflict without mutation.
func TestRunRejectsRecreatedWorkflow(t *testing.T) {
	workflow := runningWorkflow()
	service, client, _ := newOperationFixture(workflow)
	target := TargetFromSummary(Normalize(workflow))
	target.UID = "old-uid"
	_, err := service.Run(context.Background(), OperationDelete, target)
	if !IsConflict(err) {
		t.Fatalf("Run() error = %v, want conflict", err)
	}
	if got := mutations(client); len(got) != 0 {
		t.Fatalf("mutations = %+v", got)
	}
}

// TestRunRejectsMissingWorkflow checks that a missing workflow is reported as a conflict.
func TestRunRejectsMissingWorkflow(t *testing.T) {
	service, _, _ := newOperationFixture()
	_, err := service.Run(context.Background(), OperationStop, TargetFromSummary(Normalize(runningWorkflow())))
	if !IsConflict(err) {
		t.Fatalf("Run() error = %v, want conflict", err)
	}
}

// TestRunRejectsPhaseChangeSinceConfirmation checks that a changed phase prevents mutation of the confirmed target.
func TestRunRejectsPhaseChangeSinceConfirmation(t *testing.T) {
	workflow := runningWorkflow()
	target := TargetFromSummary(Normalize(workflow))
	workflow.ResourceVersion = "11"
	workflow.Status.Phase = wfv1.WorkflowSucceeded
	service, client, _ := newOperationFixture(workflow)
	_, err := service.Run(context.Background(), OperationDelete, target)
	if !IsConflict(err) {
		t.Fatalf("Run() error = %v, want conflict", err)
	}
	if got := mutations(client); len(got) != 0 {
		t.Fatalf("mutations = %+v", got)
	}
}

// TestRunAcceptsProgressWithoutStateChange checks that a newer version with the same operation state permits an update.
func TestRunAcceptsProgressWithoutStateChange(t *testing.T) {
	workflow := runningWorkflow()
	target := TargetFromSummary(Normalize(workflow))
	workflow.ResourceVersion = "42"
	service, client, _ := newOperationFixture(workflow)
	if _, err := service.Run(context.Background(), OperationStop, target); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if storedWorkflow(t, client).Spec.Shutdown != wfv1.ShutdownStrategyStop {
		t.Fatal("stop was not applied")
	}
}

// TestRunDoesNotReplayAfterAPIConflict checks that an API conflict produces exactly one update attempt.
func TestRunDoesNotReplayAfterAPIConflict(t *testing.T) {
	workflow := runningWorkflow()
	service, client, _ := newOperationFixture(workflow)
	updates := 0
	client.PrependReactor("update", "workflows", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "argoproj.io", Resource: "workflows"}, "etl", errors.New("changed"))
	})
	_, err := service.Run(context.Background(), OperationSuspend, TargetFromSummary(Normalize(workflow)))
	if !IsConflict(err) {
		t.Fatalf("Run() error = %v, want conflict", err)
	}
	if updates != 1 {
		t.Fatalf("updates = %d, want exactly one attempt", updates)
	}
}

// TestRunReportsForbidden checks that operation errors preserve Kubernetes forbidden status.
func TestRunReportsForbidden(t *testing.T) {
	workflow := runningWorkflow()
	service, client, _ := newOperationFixture(workflow)
	client.PrependReactor("update", "workflows", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "workflows"}, "etl", errors.New("no"))
	})
	_, err := service.Run(context.Background(), OperationStop, TargetFromSummary(Normalize(workflow)))
	if !apierrors.IsForbidden(err) {
		t.Fatalf("Run() error = %v, want forbidden", err)
	}
}

// failedStepsWorkflow returns a failed steps workflow with one successful pod and one failed pod.
func failedStepsWorkflow() *wfv1.Workflow {
	return &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "etl", UID: "uid-1", ResourceVersion: "10",
			Labels: map[string]string{CompletedLabel: "true", PhaseLabel: "Failed"}},
		Status: wfv1.WorkflowStatus{
			Phase:   wfv1.WorkflowFailed,
			Message: "child failed",
			Nodes: wfv1.Nodes{
				"etl":     {ID: "etl", Name: "etl", Type: wfv1.NodeTypeSteps, Phase: wfv1.NodeFailed, Children: []string{"group0"}},
				"group0":  {ID: "group0", Name: "etl[0]", Type: wfv1.NodeTypeStepGroup, Phase: wfv1.NodeFailed, BoundaryID: "etl", Children: []string{"extract", "load"}},
				"extract": {ID: "extract", Name: "etl[0].extract", Type: wfv1.NodeTypePod, Phase: wfv1.NodeSucceeded, BoundaryID: "etl"},
				"load":    {ID: "load", Name: "etl[0].load", Type: wfv1.NodeTypePod, Phase: wfv1.NodeFailed, BoundaryID: "etl"},
			},
		},
	}
}

// workflowPod creates a pod fixture labeled and owned by the fixture workflow with a node annotation.
func workflowPod(name, nodeID string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace:       "argo",
		Name:            name,
		UID:             types.UID("pod-uid-" + name),
		Labels:          map[string]string{workflowPodLabel: "etl"},
		Annotations:     map[string]string{nodeIDAnnotation: nodeID},
		OwnerReferences: []metav1.OwnerReference{{UID: "uid-1"}},
	}}
}

// TestRetryResetsFailedNodesAndDeletesTheirPods checks node resets, pod ownership filtering, and dry-run/delete/update ordering.
func TestRetryResetsFailedNodesAndDeletesTheirPods(t *testing.T) {
	workflow := failedStepsWorkflow()
	stranger := workflowPod("etl-load-other", "load")
	stranger.OwnerReferences = []metav1.OwnerReference{{UID: "someone-else"}}
	service, client, kubernetes, writes := newRecordingFixture(workflow, workflowPod("etl-load", "load"), workflowPod("etl-extract", "extract"), stranger)

	result, err := service.Run(context.Background(), OperationRetry, TargetFromSummary(Normalize(workflow)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.DeletedPods != 1 {
		t.Fatalf("deleted pods = %d", result.DeletedPods)
	}
	pods, _ := kubernetes.CoreV1().Pods("argo").List(context.Background(), metav1.ListOptions{})
	remaining := map[string]bool{}
	for _, pod := range pods.Items {
		remaining[pod.Name] = true
	}
	if remaining["etl-load"] || !remaining["etl-extract"] || !remaining["etl-load-other"] {
		t.Fatalf("remaining pods = %v", remaining)
	}

	stored := storedWorkflow(t, client)
	if stored.Status.Phase != wfv1.WorkflowRunning || stored.Status.Message != "" || stored.Labels[CompletedLabel] != "" {
		t.Fatalf("workflow status = %s %q labels=%v", stored.Status.Phase, stored.Status.Message, stored.Labels)
	}
	if _, found := stored.Status.Nodes["load"]; found {
		t.Fatal("failed node was kept")
	}
	if stored.Status.Nodes["extract"].Phase != wfv1.NodeSucceeded {
		t.Fatalf("succeeded node = %+v", stored.Status.Nodes["extract"])
	}
	for _, id := range []string{"etl", "group0"} {
		if stored.Status.Nodes[id].Phase != wfv1.NodeRunning {
			t.Fatalf("node %s = %s, want Running", id, stored.Status.Nodes[id].Phase)
		}
	}
	if children := stored.Status.Nodes["group0"].Children; len(children) != 1 || children[0] != "extract" {
		t.Fatalf("group children = %v", children)
	}

	// The dry run proves the update before any pod is deleted.
	if got := strings.Join(*writes, ", "); got != "dry-run update, delete pod etl-load, update" {
		t.Fatalf("writes = %s", got)
	}
}

// TestRetryRefusesRunningAndCompressedWorkflows checks rejection of active workflows and unavailable compressed node status.
func TestRetryRefusesRunningAndCompressedWorkflows(t *testing.T) {
	if _, _, err := formulateRetry(runningWorkflow()); err == nil {
		t.Fatal("formulateRetry() accepted a running workflow")
	}
	compressed := failedStepsWorkflow()
	compressed.Status.Nodes = nil
	compressed.Status.CompressedNodes = "H4sIAAAAAAAA"
	if _, _, err := formulateRetry(compressed); err == nil {
		t.Fatal("formulateRetry() accepted compressed nodes")
	}
}

// TestRetryRerunsExitHandler checks that retry removes exit handler status and includes its pod for deletion.
func TestRetryRerunsExitHandler(t *testing.T) {
	workflow := failedStepsWorkflow()
	workflow.Status.Nodes["exit"] = wfv1.NodeStatus{ID: "exit", Name: "etl.onExit", Type: wfv1.NodeTypePod, Phase: wfv1.NodeSucceeded}
	retried, podNodes, err := formulateRetry(workflow)
	if err != nil {
		t.Fatalf("formulateRetry() error = %v", err)
	}
	if retried.Status.Nodes.Has("exit") {
		t.Fatal("exit handler node was kept")
	}
	if len(podNodes) != 2 || podNodes[0] != "exit" || podNodes[1] != "load" {
		t.Fatalf("pod nodes = %v", podNodes)
	}
}

// TestResubmitCopiesSpecAndUserLabels checks that resubmission retains user data and clears completion and shutdown state.
func TestResubmitCopiesSpecAndUserLabels(t *testing.T) {
	workflow := failedStepsWorkflow()
	workflow.GenerateName = "etl-"
	workflow.Labels["team"] = "data"
	workflow.Labels[creatorLabel] = "someone"
	workflow.Spec.Shutdown = wfv1.ShutdownStrategyTerminate
	workflow.Spec.ActiveDeadlineSeconds = ptr.To[int64](0)
	resubmitted := formulateResubmit(workflow)
	if resubmitted.Name != "" || resubmitted.GenerateName != "etl-" || resubmitted.Namespace != "argo" {
		t.Fatalf("metadata = %+v", resubmitted.ObjectMeta)
	}
	if resubmitted.Labels["team"] != "data" || resubmitted.Labels[ResubmittedFromLabel] != "etl" {
		t.Fatalf("labels = %v", resubmitted.Labels)
	}
	for _, label := range []string{creatorLabel, CompletedLabel, PhaseLabel} {
		if _, found := resubmitted.Labels[label]; found {
			t.Fatalf("label %s was copied", label)
		}
	}
	if resubmitted.Spec.Shutdown != "" || resubmitted.Spec.ActiveDeadlineSeconds != nil {
		t.Fatalf("spec = %+v", resubmitted.Spec)
	}
	if len(resubmitted.Status.Nodes) != 0 {
		t.Fatal("status was copied")
	}
}

type fakeReviewer map[Access]bool

// Allowed returns a configured permission or an error when the permission has no configured answer.
func (reviewer fakeReviewer) Allowed(_ context.Context, _ string, access Access) (bool, error) {
	allowed, found := reviewer[access]
	if !found {
		return false, errors.New("review failed")
	}
	return allowed, nil
}

// TestCheckOperationsCombinesPermissions checks combined permission requirements and omission of unknown results.
func TestCheckOperationsCombinesPermissions(t *testing.T) {
	update := Access{Group: "argoproj.io", Resource: "workflows", Verb: "update"}
	reviewer := fakeReviewer{
		update: true,
		{Group: "argoproj.io", Resource: "workflows", Verb: "delete"}: false,
		{Resource: "pods", Verb: "list"}:                              true,
		{Resource: "pods", Verb: "delete"}:                            false,
	}
	got := CheckOperations(context.Background(), reviewer, "argo")
	if !got[OperationStop] || !got[OperationSuspend] {
		t.Fatalf("update operations = %v", got)
	}
	if allowed, known := got[OperationDelete]; !known || allowed {
		t.Fatalf("delete = %v %v", allowed, known)
	}
	if allowed, known := got[OperationRetry]; !known || allowed {
		t.Fatalf("retry needs pod delete: %v %v", allowed, known)
	}
	if _, known := got[OperationResubmit]; known {
		t.Fatal("an unchecked permission must stay unknown")
	}
}
