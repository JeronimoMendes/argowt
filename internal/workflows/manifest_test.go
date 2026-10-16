package workflows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argofake "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"
)

const helloManifest = `# a comment before the document
---
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  generateName: hello-
  namespace: jobs
  uid: 1234
  resourceVersion: "99"
spec:
  entrypoint: main
  arguments:
    parameters:
    - name: message
      value: hi
  templates:
  - name: main
    container:
      image: busybox:1.36
status:
  phase: Succeeded
`

func TestParseWorkflowManifestAcceptsOneWorkflow(t *testing.T) {
	workflow, err := ParseWorkflowManifest([]byte(helloManifest))
	if err != nil {
		t.Fatalf("ParseWorkflowManifest() error = %v", err)
	}
	if workflow.GenerateName != "hello-" || workflow.Namespace != "jobs" || workflow.Spec.Entrypoint != "main" {
		t.Fatalf("workflow = %+v", workflow.ObjectMeta)
	}
	if workflow.UID != "" || workflow.ResourceVersion != "" || workflow.Status.Phase != "" {
		t.Fatalf("server fields were kept: %+v %+v", workflow.ObjectMeta, workflow.Status)
	}
}

func TestParseWorkflowManifestRejectsOtherKindsAndDocuments(t *testing.T) {
	cases := map[string]string{
		"empty":           "# nothing here\n---\n",
		"template":        "apiVersion: argoproj.io/v1alpha1\nkind: WorkflowTemplate\nmetadata:\n  name: t\n",
		"pod":             "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n",
		"other api group": "apiVersion: example.com/v1\nkind: Workflow\nmetadata:\n  name: w\n",
		"no kind":         "metadata:\n  name: w\n",
		"two documents":   helloManifest + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n",
		"no name":         "apiVersion: argoproj.io/v1alpha1\nkind: Workflow\nspec:\n  entrypoint: main\n",
		"not yaml":        "apiVersion: [unclosed\n",
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			if workflow, err := ParseWorkflowManifest([]byte(manifest)); err == nil {
				t.Fatalf("ParseWorkflowManifest() = %+v, want an error", workflow)
			}
		})
	}
}

func TestLoadWorkflowManifestReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hello.yaml")
	if err := os.WriteFile(path, []byte(helloManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkflowManifest(path); err != nil {
		t.Fatalf("LoadWorkflowManifest() error = %v", err)
	}
	if _, err := LoadWorkflowManifest(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("LoadWorkflowManifest() accepted a missing file")
	}
	large := filepath.Join(t.TempDir(), "large.yaml")
	if err := os.WriteFile(large, []byte(strings.Repeat("#", maxManifestBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkflowManifest(large); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("LoadWorkflowManifest() error = %v, want size error", err)
	}
}

func TestSubmitLocalManifestOverridesParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hello.yaml")
	if err := os.WriteFile(path, []byte(helloManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	client := argofake.NewSimpleClientset()
	service := NewSubmissionService(client)
	source := SubmissionSource{Kind: SourceFile, Path: path}
	form, err := service.Prepare(context.Background(), source, "argo")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if form.DestinationNamespace != "jobs" || len(form.Parameters) != 1 || form.Parameters[0].Default != "hi" {
		t.Fatalf("form = %+v", form)
	}
	_, err = service.Submit(context.Background(), SubmissionRequest{
		Source:               source,
		DestinationNamespace: "argo",
		Parameters:           map[string]string{"message": "hello"},
		Manifest:             form.Manifest,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	created := client.Actions()[0].(k8stesting.CreateAction).GetObject().(*wfv1.Workflow)
	if created.Namespace != "argo" || created.Spec.Arguments.Parameters[0].Value.String() != "hello" {
		t.Fatalf("created = %+v %+v", created.ObjectMeta, created.Spec.Arguments)
	}
	if form.Manifest.Spec.Arguments.Parameters[0].Value.String() != "hi" {
		t.Fatal("Submit() changed the prepared manifest")
	}
}

func TestResubmitFormKeepsWorkflowIdentity(t *testing.T) {
	workflow := &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{Namespace: "argo", Name: "etl-abc", UID: "uid-1"},
		Spec:       wfv1.WorkflowSpec{Arguments: wfv1.Arguments{Parameters: []wfv1.Parameter{{Name: "dataset", Value: wfv1.AnyStringPtr("orders")}}}},
	}
	client := argofake.NewSimpleClientset(workflow)
	service := NewSubmissionService(client)
	if _, err := service.Prepare(context.Background(), SubmissionSource{Kind: SourceWorkflow, Namespace: "argo", Name: "etl-abc", UID: "uid-2"}, ""); err == nil {
		t.Fatal("Prepare() accepted a recreated workflow")
	}
	form, err := service.Prepare(context.Background(), SubmissionSource{Kind: SourceWorkflow, Namespace: "argo", Name: "etl-abc", UID: "uid-1"}, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if form.DestinationNamespace != "argo" || form.Manifest.GenerateName != "etl-abc-" || form.Parameters[0].Default != "orders" {
		t.Fatalf("form = %+v", form)
	}
}
