// Package workflows contains workflow-domain behavior and Kubernetes-backed
// repositories. It deliberately does not import Bubble Tea.
package workflows

import (
	"sort"
	"strings"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
)

const ControllerInstanceIDLabel = "workflows.argoproj.io/controller-instanceid"

// CompletedLabel is set to "true" by the Argo controller when a workflow completes.
const CompletedLabel = "workflows.argoproj.io/completed"

type Summary struct {
	Namespace         string
	Name              string
	UID               string
	ResourceVersion   string
	Phase             string
	Suspended         bool
	SuspendNode       bool
	CreationTimestamp time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	InstanceID        string
	Template          string
	Labels            map[string]string
}

// Normalize copies workflow identity, phase, suspension state, template, and labels into a summary.
// A nil workflow produces an empty summary.
func Normalize(workflow *wfv1.Workflow) Summary {
	if workflow == nil {
		return Summary{}
	}
	template := workflow.Spec.Entrypoint
	if workflow.Spec.WorkflowTemplateRef != nil {
		template = workflow.Spec.WorkflowTemplateRef.Name
	}
	return Summary{
		Namespace:         workflow.Namespace,
		Name:              workflow.Name,
		UID:               string(workflow.UID),
		ResourceVersion:   workflow.ResourceVersion,
		Phase:             string(workflow.Status.Phase),
		Suspended:         workflow.Spec.Suspend != nil && *workflow.Spec.Suspend,
		SuspendNode:       waitingOnSuspendNode(workflow),
		CreationTimestamp: workflow.CreationTimestamp.Time,
		StartedAt:         workflow.Status.StartedAt.Time,
		FinishedAt:        workflow.Status.FinishedAt.Time,
		InstanceID:        workflow.Labels[ControllerInstanceIDLabel],
		Template:          template,
		Labels:            copyLabels(workflow.Labels),
	}
}

// waitingOnSuspendNode reports a running suspend step, such as an approval.
func waitingOnSuspendNode(workflow *wfv1.Workflow) bool {
	for _, node := range workflow.Status.Nodes {
		if node.IsActiveSuspendNode() {
			return true
		}
	}
	return false
}

func copyLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func (s Summary) DisplayPhase() string {
	if s.Suspended {
		return "Suspended"
	}
	if s.Phase == "" {
		return "Unknown"
	}
	return s.Phase
}

func (s Summary) Active() bool {
	switch strings.ToLower(s.Phase) {
	case "succeeded", "failed", "error":
		return false
	default:
		return true
	}
}

func Sort(summaries []Summary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		left, right := summaries[i], summaries[j]
		if leftRank, rightRank := phaseRank(left), phaseRank(right); leftRank != rightRank {
			return leftRank < rightRank
		}
		if !left.CreationTimestamp.Equal(right.CreationTimestamp) {
			return left.CreationTimestamp.After(right.CreationTimestamp)
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		return left.Name < right.Name
	})
}

func phaseRank(summary Summary) int {
	if summary.Suspended {
		return 2
	}
	switch strings.ToLower(summary.Phase) {
	case "error":
		return 0
	case "failed":
		return 1
	case "pending":
		return 3
	case "running":
		return 4
	case "succeeded":
		return 6
	default:
		return 5
	}
}

// SortCompleted orders finished workflows newest finished first. A workflow
// without a finish time falls back to its creation time.
func SortCompleted(summaries []Summary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		left, right := summaries[i], summaries[j]
		if leftTime, rightTime := left.FinishedOrCreated(), right.FinishedOrCreated(); !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if left.Namespace != right.Namespace {
			return left.Namespace < right.Namespace
		}
		return left.Name < right.Name
	})
}

// FinishedOrCreated is the finish time, or the creation time before Argo records one.
func (s Summary) FinishedOrCreated() time.Time {
	if !s.FinishedAt.IsZero() {
		return s.FinishedAt
	}
	return s.CreationTimestamp
}
