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

type SubmissionSource struct {
	Namespace string
	Name      string
	Cluster   bool
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
}

type SubmissionRequest struct {
	Source               SubmissionSource
	DestinationNamespace string
	Parameters           map[string]string
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

func (service *SubmissionService) Prepare(ctx context.Context, source SubmissionSource, destinationNamespace string) (SubmissionForm, error) {
	var parameters []wfv1.Parameter
	if source.Cluster {
		template, err := service.client.ArgoprojV1alpha1().ClusterWorkflowTemplates().Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return SubmissionForm{}, fmt.Errorf("get ClusterWorkflowTemplate %s: %w", source.Name, err)
		}
		parameters = template.Spec.Arguments.Parameters
	} else {
		template, err := service.client.ArgoprojV1alpha1().WorkflowTemplates(source.Namespace).Get(ctx, source.Name, metav1.GetOptions{})
		if err != nil {
			return SubmissionForm{}, fmt.Errorf("get WorkflowTemplate %s/%s: %w", source.Namespace, source.Name, err)
		}
		parameters = template.Spec.Arguments.Parameters
		if destinationNamespace == "" {
			destinationNamespace = source.Namespace
		}
	}
	if destinationNamespace == "" {
		return SubmissionForm{}, fmt.Errorf("destination namespace is required")
	}
	form := SubmissionForm{Source: source, DestinationNamespace: destinationNamespace}
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

func (service *SubmissionService) Submit(ctx context.Context, request SubmissionRequest) (SubmissionResult, error) {
	if strings.TrimSpace(request.DestinationNamespace) == "" {
		return SubmissionResult{}, fmt.Errorf("destination namespace is required")
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
				ClusterScope: request.Source.Cluster,
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
