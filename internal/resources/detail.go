package resources

import (
	"context"
	"fmt"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

type Condition struct {
	Type               string
	Status             string
	Reason             string
	Message            string
	LastTransitionTime string
}

type Owner struct {
	Kind string
	Name string
	UID  string
}

type Detail struct {
	Summary     Summary
	Labels      map[string]string
	Annotations map[string]string
	Owners      []Owner
	Conditions  []Condition
	YAML        string
}

// Get loads one full object for an explicitly opened detail view. The object is
// returned in-memory only and is never handed to state persistence.
func (repository *Repository) Get(ctx context.Context, descriptor Descriptor, namespace, name, expectedUID string) (Detail, error) {
	object, err := repository.interfaceFor(descriptor, namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Detail{}, fmt.Errorf("get %s %s: %w", descriptor.Title, name, err)
	}
	if expectedUID != "" && string(object.GetUID()) != expectedUID {
		return Detail{}, fmt.Errorf("resource identity changed for %s/%s", namespace, name)
	}
	serialized, err := yaml.Marshal(object.Object)
	if err != nil {
		return Detail{}, fmt.Errorf("encode resource YAML: %w", err)
	}
	detail := Detail{
		Summary:     normalize(descriptor, object),
		Labels:      copyMap(object.GetLabels()),
		Annotations: copyMap(object.GetAnnotations()),
		YAML:        string(serialized),
	}
	for _, reference := range object.GetOwnerReferences() {
		detail.Owners = append(detail.Owners, Owner{
			Kind: reference.Kind,
			Name: reference.Name,
			UID:  string(reference.UID),
		})
	}
	detail.Conditions = extractConditions(object)
	return detail, nil
}

func extractConditions(object *unstructured.Unstructured) []Condition {
	rawConditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	conditions := make([]Condition, 0, len(rawConditions))
	for _, raw := range rawConditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		conditions = append(conditions, Condition{
			Type:               stringValue(condition["type"]),
			Status:             stringValue(condition["status"]),
			Reason:             stringValue(condition["reason"]),
			Message:            stringValue(condition["message"]),
			LastTransitionTime: stringValue(condition["lastTransitionTime"]),
		})
	}
	sort.SliceStable(conditions, func(i, j int) bool { return conditions[i].Type < conditions[j].Type })
	return conditions
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
