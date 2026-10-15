package workflows

import (
	"context"
	"testing"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argofake "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"
)

func TestPrepareWorkflowTemplateParameters(t *testing.T) {
	template := &wfv1.WorkflowTemplate{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "payments"},
		Spec: wfv1.WorkflowSpec{Arguments: wfv1.Arguments{Parameters: []wfv1.Parameter{
			{Name: "environment", Default: wfv1.AnyStringPtr("staging"), Description: wfv1.AnyStringPtr("deployment environment")},
			{Name: "required"},
		}}},
	}
	service := NewSubmissionService(argofake.NewSimpleClientset(template))
	form, err := service.Prepare(context.Background(), SubmissionSource{Namespace: "argo", Name: "payments"}, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if form.DestinationNamespace != "argo" || len(form.Parameters) != 2 {
		t.Fatalf("form = %+v", form)
	}
	if form.Parameters[0].Default != "staging" || form.Parameters[0].Description != "deployment environment" {
		t.Fatalf("default parameter = %+v", form.Parameters[0])
	}
	if !form.Parameters[1].Required {
		t.Fatalf("required parameter = %+v", form.Parameters[1])
	}
}

func TestSubmitCreatesWorkflowTemplateReference(t *testing.T) {
	client := argofake.NewSimpleClientset()
	service := NewSubmissionService(client)
	_, err := service.Submit(context.Background(), SubmissionRequest{
		Source:               SubmissionSource{Name: "global-template", Cluster: true},
		DestinationNamespace: "jobs",
		Parameters:           map[string]string{"message": "hello", "count": "3"},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	actions := client.Actions()
	if len(actions) != 1 {
		t.Fatalf("actions = %+v", actions)
	}
	create, ok := actions[0].(k8stesting.CreateAction)
	if !ok {
		t.Fatalf("action = %T", actions[0])
	}
	workflow := create.GetObject().(*wfv1.Workflow)
	if workflow.Namespace != "jobs" || workflow.GenerateName != "global-template-" {
		t.Fatalf("workflow metadata = %+v", workflow.ObjectMeta)
	}
	if workflow.Spec.WorkflowTemplateRef == nil || !workflow.Spec.WorkflowTemplateRef.ClusterScope || workflow.Spec.WorkflowTemplateRef.Name != "global-template" {
		t.Fatalf("template ref = %+v", workflow.Spec.WorkflowTemplateRef)
	}
	if len(workflow.Spec.Arguments.Parameters) != 2 || workflow.Spec.Arguments.Parameters[0].Name != "count" {
		t.Fatalf("arguments = %+v", workflow.Spec.Arguments.Parameters)
	}
}

func TestSubmitRejectsMissingDestination(t *testing.T) {
	service := NewSubmissionService(argofake.NewSimpleClientset())
	if _, err := service.Submit(context.Background(), SubmissionRequest{Source: SubmissionSource{Name: "template"}}); err == nil {
		t.Fatal("Submit() expected an error")
	}
}
