package workflows

import (
	"testing"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSummaryActive(t *testing.T) {
	tests := []struct {
		phase  string
		active bool
	}{
		{phase: "", active: true},
		{phase: "Pending", active: true},
		{phase: "Running", active: true},
		{phase: "UnknownFromFuture", active: true},
		{phase: "Succeeded", active: false},
		{phase: "Failed", active: false},
		{phase: "Error", active: false},
	}
	for _, test := range tests {
		t.Run(test.phase, func(t *testing.T) {
			if got := (Summary{Phase: test.phase}).Active(); got != test.active {
				t.Fatalf("Active() = %t, want %t", got, test.active)
			}
		})
	}
}

func TestNormalizeSuspendedWorkflow(t *testing.T) {
	suspended := true
	workflow := &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:       "argo",
			Name:            "example",
			ResourceVersion: "12",
			Labels:          map[string]string{ControllerInstanceIDLabel: "controller-a"},
		},
		Spec:   wfv1.WorkflowSpec{Suspend: &suspended},
		Status: wfv1.WorkflowStatus{Phase: wfv1.WorkflowRunning},
	}
	got := Normalize(workflow)
	if !got.Suspended || got.DisplayPhase() != "Suspended" {
		t.Fatalf("Normalize() = %+v", got)
	}
	if got.InstanceID != "controller-a" {
		t.Fatalf("InstanceID = %q", got.InstanceID)
	}
}

func TestSortIsDeterministic(t *testing.T) {
	newer := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)
	items := []Summary{
		{Namespace: "b", Name: "running", Phase: "Running", CreationTimestamp: newer},
		{Namespace: "a", Name: "pending-old", Phase: "Pending", CreationTimestamp: older},
		{Namespace: "a", Name: "pending-new", Phase: "Pending", CreationTimestamp: newer},
		{Namespace: "a", Name: "failed", Phase: "Failed", CreationTimestamp: older},
		{Namespace: "a", Name: "suspended", Phase: "Running", Suspended: true, CreationTimestamp: older},
	}
	Sort(items)
	want := []string{"failed", "suspended", "pending-new", "pending-old", "running"}
	for index, name := range want {
		if items[index].Name != name {
			t.Fatalf("item %d = %q, want %q; all=%+v", index, items[index].Name, name, items)
		}
	}
}
