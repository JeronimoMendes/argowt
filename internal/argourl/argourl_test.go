package argourl

import (
	"testing"

	"argowt/internal/resources"
)

func TestParse(t *testing.T) {
	cases := map[string]Target{
		"https://argo.example.com/workflows/argo/hello?uid=abc&nodeId=hello-123": {Kind: resources.Workflows, Namespace: "argo", Name: "hello", UID: "abc", NodeID: "hello-123"},
		"https://example.com/argo/workflows/argo/":                               {Kind: resources.Workflows, Namespace: "argo"},
		"https://argo.example.com/workflow-templates/argo/build?nodeId=ignored":  {Kind: resources.WorkflowTemplates, Namespace: "argo", Name: "build"},
		"https://argo.example.com/cluster-workflow-templates/shared":             {Kind: resources.ClusterWorkflowTemplates, Name: "shared"},
		"https://argo.example.com/cron-workflows/argo/nightly":                   {Kind: resources.CronWorkflows, Namespace: "argo", Name: "nightly"},
		"https://argo.example.com/sensors/events/webhook":                        {Kind: resources.Sensors, Namespace: "events", Name: "webhook"},
	}
	for raw, want := range cases {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", raw, err)
		}
		if got != want {
			t.Fatalf("Parse(%q) = %+v, want %+v", raw, got, want)
		}
	}

	for _, raw := range []string{
		"https://argo.example.com/archived-workflows/argo/uid",
		"https://argo.example.com/workflows/argo/hello/extra",
		"not a url",
	} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) succeeded", raw)
		}
	}
}
