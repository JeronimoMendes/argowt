package workflows

import (
	"testing"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalizeExecutionBuildsTreeDetailsAndDAGEdges(t *testing.T) {
	started := metav1.NewTime(time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC))
	finished := metav1.NewTime(started.Add(2 * time.Minute))
	result := "done"
	exitCode := "0"
	workflow := &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "example", UID: "workflow-uid"},
		Status: wfv1.WorkflowStatus{
			Phase: wfv1.WorkflowRunning,
			Nodes: wfv1.Nodes{
				"root": {
					ID: "root", Name: "example", DisplayName: "example", Type: wfv1.NodeTypeDAG,
					Phase: wfv1.NodeRunning, Children: []string{"task-a", "task-b"}, StartedAt: started,
				},
				"task-a": {
					ID: "task-a", Name: "example.task-a", DisplayName: "task-a", Type: wfv1.NodeTypePod,
					Phase: wfv1.NodeSucceeded, Children: []string{"shared"}, StartedAt: started, FinishedAt: finished,
					TemplateName: "worker",
					Inputs:       &wfv1.Inputs{Parameters: []wfv1.Parameter{{Name: "message", Value: wfv1.AnyStringPtr("hello")}}},
					Outputs:      &wfv1.Outputs{Result: &result, ExitCode: &exitCode},
				},
				"task-b": {
					ID: "task-b", Name: "example.task-b", DisplayName: "task-b", Type: wfv1.NodeTypePod,
					Phase: wfv1.NodeRunning, Children: []string{"shared", "missing"}, StartedAt: started,
				},
				"shared": {
					ID: "shared", Name: "example.shared", DisplayName: "shared", Type: wfv1.NodeTypePod,
					Phase: wfv1.NodePending,
				},
			},
		},
	}

	execution := NormalizeExecution(workflow)
	if len(execution.Nodes) != 5 {
		t.Fatalf("nodes = %d, want 5 including malformed reference", len(execution.Nodes))
	}
	if len(execution.Roots) != 1 || execution.Roots[0] != "root" {
		t.Fatalf("roots = %v", execution.Roots)
	}
	if got := execution.Nodes["shared"].Parents; len(got) != 2 || got[0] != "task-a" || got[1] != "task-b" {
		t.Fatalf("shared parents = %v", got)
	}
	if execution.Nodes["missing"].Phase != "Unknown" {
		t.Fatalf("missing node = %+v", execution.Nodes["missing"])
	}
	if execution.Nodes["task-a"].Inputs[0].Value != "hello" || execution.Nodes["task-a"].Result != "done" {
		t.Fatalf("node details = %+v", execution.Nodes["task-a"])
	}
	rows := execution.TreeRows()
	seen := map[string]int{}
	for _, row := range rows {
		seen[row.Node.ID]++
	}
	if seen["shared"] != 1 {
		t.Fatalf("shared DAG node rendered %d times", seen["shared"])
	}
	if len(execution.Edges()) != 5 {
		t.Fatalf("edges = %+v", execution.Edges())
	}
}

func TestTreeRowsToleratesCycle(t *testing.T) {
	execution := Execution{
		Nodes: map[string]Node{
			"a": {ID: "a", Children: []string{"b"}},
			"b": {ID: "b", Children: []string{"a"}},
		},
		Roots: []string{"a"},
	}
	rows := execution.TreeRows()
	if len(rows) != 3 || !rows[2].Cycle {
		t.Fatalf("cycle rows = %+v", rows)
	}
}

func TestNodeDurationHandlesRunningAndInvalidTimes(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	node := Node{StartedAt: now.Add(-5 * time.Minute)}
	if got := node.Duration(now); got != 5*time.Minute {
		t.Fatalf("running duration = %s", got)
	}
	node.FinishedAt = now.Add(-10 * time.Minute)
	if got := node.Duration(now); got != 0 {
		t.Fatalf("invalid duration = %s", got)
	}
}
