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

type Summary struct {
	Namespace         string
	Name              string
	UID               string
	ResourceVersion   string
	Phase             string
	Suspended         bool
	CreationTimestamp time.Time
	StartedAt         time.Time
	InstanceID        string
}

func Normalize(workflow *wfv1.Workflow) Summary {
	if workflow == nil {
		return Summary{}
	}
	return Summary{
		Namespace:         workflow.Namespace,
		Name:              workflow.Name,
		UID:               string(workflow.UID),
		ResourceVersion:   workflow.ResourceVersion,
		Phase:             string(workflow.Status.Phase),
		Suspended:         workflow.Spec.Suspend != nil && *workflow.Spec.Suspend,
		CreationTimestamp: workflow.CreationTimestamp.Time,
		StartedAt:         workflow.Status.StartedAt.Time,
		InstanceID:        workflow.Labels[ControllerInstanceIDLabel],
	}
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
