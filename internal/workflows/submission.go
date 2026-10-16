package workflows

import (
	"context"
	"fmt"
	"sort"
	"strings"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argoclient "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SourceKind string

const (
	SourceWorkflowTemplate        SourceKind = "WorkflowTemplate"
	SourceClusterWorkflowTemplate SourceKind = "ClusterWorkflowTemplate"
	SourceWorkflow                SourceKind = "Workflow"
	SourceFile                    SourceKind = "File"
)

// SubmissionSource is what a new workflow is created from: a template, an
// existing workflow (resubmit), or a local manifest file (Path).
type SubmissionSource struct {
	Kind      SourceKind
	Namespace string
	Name      string
	UID       string
	Path      string
}

// Label names the source for confirmations, e.g. "WorkflowTemplate/etl".
func (source SubmissionSource) Label() string {
	switch source.Kind {
	case SourceFile:
		return "file " + source.Path
	case SourceWorkflow:
		return "Workflow/" + source.Namespace + "/" + source.Name
	default:
		return string(source.Kind) + "/" + source.Name
	}
}

type SubmissionParameter struct {
	Name        string
	Default     string
	Description string
	Required    bool
}

type SubmissionForm struct {
	Source               SubmissionSource
	DestinationNamespace string
	Parameters           []SubmissionParameter
	// Manifest is the workflow to create for Workflow and File sources, as
	// read when the form was prepared.
	Manifest *wfv1.Workflow
}

type SubmissionRequest struct {
	Source               SubmissionSource
	DestinationNamespace string
	Parameters           map[string]string
	Manifest             *wfv1.Workflow
}

type SubmissionResult struct {
	Namespace string
	Name      string
	UID       string
}

type SubmissionService struct {
	client argoclient.Interface
}

func NewSubmissionService(client argoclient.Interface) *SubmissionService {
	return &SubmissionService{client: client}
}

// Prepare loads a template, existing workflow, or local manifest into a parameter form.
// It resolves the destination namespace and rejects resubmission when the source UID changed.
func (service *SubmissionService) Prepare(ctx context.Context, source SubmissionSource, destinationNamespace string) (SubmissionForm, error) {
	form := SubmissionForm{Source: source}
	var parameters []wfv1.Parameter
	switch source.Kind {
	case SourceClusterWorkflowTemplate:
		template, err := service.client.ArgoprojV1alpha1().ClusterWorkflowTemplates().Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return SubmissionForm{}, fmt.Errorf("get ClusterWorkflowTemplate %s: %w", source.Name, err)
		}
		parameters = template.Spec.Arguments.Parameters
	case SourceWorkflowTemplate:
		template, err := service.client.ArgoprojV1alpha1().WorkflowTemplates(source.Namespace).Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return SubmissionForm{}, fmt.Errorf("get WorkflowTemplate %s/%s: %w", source.Namespace, source.Name, err)
		}
		parameters = template.Spec.Arguments.Parameters
		if destinationNamespace == "" {
			destinationNamespace = source.Namespace
		}
	case SourceWorkflow:
		workflow, err := service.client.ArgoprojV1alpha1().Workflows(source.Namespace).Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return SubmissionForm{}, fmt.Errorf("get workflow %s/%s: %w", source.Namespace, source.Name, err)
		}
		if source.UID != "" && string(workflow.UID) != source.UID {
			return SubmissionForm{}, fmt.Errorf("workflow %s/%s was deleted and recreated; reopen it to resubmit", source.Namespace, source.Name)
		}
		form.Manifest = formulateResubmit(workflow)
		parameters = form.Manifest.Spec.Arguments.Parameters
		destinationNamespace = workflow.Namespace
	case SourceFile:
		workflow, err := LoadWorkflowManifest(source.Path)
		if err != nil {
			return SubmissionForm{}, err
		}
		form.Manifest = workflow
		parameters = workflow.Spec.Arguments.Parameters
		if workflow.Namespace != "" {
			destinationNamespace = workflow.Namespace
		}
	default:
		return SubmissionForm{}, fmt.Errorf("unsupported submission source %q", source.Kind)
	}
	if destinationNamespace == "" {
		return SubmissionForm{}, fmt.Errorf("destination namespace is required")
	}
	form.DestinationNamespace = destinationNamespace
	for _, parameter := range parameters {
		defaultValue := ""
		if parameter.Value != nil {
			defaultValue = parameter.Value.String()
		} else if parameter.Default != nil {
			defaultValue = parameter.Default.String()
		}
		description := ""
		if parameter.Description != nil {
			description = parameter.Description.String()
		}
		form.Parameters = append(form.Parameters, SubmissionParameter{
			Name:        parameter.Name,
			Default:     defaultValue,
			Description: description,
			Required:    parameter.Value == nil && parameter.Default == nil,
		})
	}
	return form, nil
}

// Submit creates a workflow from a template reference or prepared manifest with the requested parameters.
func (service *SubmissionService) Submit(ctx context.Context, request SubmissionRequest) (SubmissionResult, error) {
	if strings.TrimSpace(request.DestinationNamespace) == "" {
		return SubmissionResult{}, fmt.Errorf("destination namespace is required")
	}
	if request.Source.Kind == SourceWorkflow || request.Source.Kind == SourceFile {
		return service.submitManifest(ctx, request)
	}
	if strings.TrimSpace(request.Source.Name) == "" {
		return SubmissionResult{}, fmt.Errorf("template name is required")
	}
	names := make([]string, 0, len(request.Parameters))
	for name := range request.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	arguments := make([]wfv1.Parameter, 0, len(names))
	for _, name := range names {
		arguments = append(arguments, wfv1.Parameter{Name: name, Value: wfv1.AnyStringPtr(request.Parameters[name])})
	}
	workflow := &wfv1.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: request.Source.Name + "-",
			Namespace:    request.DestinationNamespace,
		},
		Spec: wfv1.WorkflowSpec{
			WorkflowTemplateRef: &wfv1.WorkflowTemplateRef{
				Name:         request.Source.Name,
				ClusterScope: request.Source.Kind == SourceClusterWorkflowTemplate,
			},
			Arguments: wfv1.Arguments{Parameters: arguments},
		},
	}
	created, err := service.client.ArgoprojV1alpha1().Workflows(request.DestinationNamespace).Create(ctx, workflow, metav1.CreateOptions{})
	if err != nil {
		return SubmissionResult{}, fmt.Errorf("submit workflow from template: %w", err)
	}
	return SubmissionResult{Namespace: created.Namespace, Name: created.Name, UID: string(created.UID)}, nil
}

// submitManifest creates a prepared workflow with the entered parameter
// values.
func (service *SubmissionService) submitManifest(ctx context.Context, request SubmissionRequest) (SubmissionResult, error) {
	if request.Manifest == nil {
		return SubmissionResult{}, fmt.Errorf("no workflow manifest is loaded")
	}
	workflow := request.Manifest.DeepCopy()
	workflow.Namespace = request.DestinationNamespace
	for index, parameter := range workflow.Spec.Arguments.Parameters {
		value, found := request.Parameters[parameter.Name]
		if !found {
			continue
		}
		workflow.Spec.Arguments.Parameters[index].Value = wfv1.AnyStringPtr(value)
		workflow.Spec.Arguments.Parameters[index].ValueFrom = nil
	}
	created, err := service.client.ArgoprojV1alpha1().Workflows(request.DestinationNamespace).Create(ctx, workflow, metav1.CreateOptions{})
	if err != nil {
		return SubmissionResult{}, fmt.Errorf("submit workflow from %s: %w", request.Source.Label(), err)
	}
	return SubmissionResult{Namespace: created.Namespace, Name: created.Name, UID: string(created.UID)}, nil
}
