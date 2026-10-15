package resources

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestDiscoverHidesMissingOptionalCRDs(t *testing.T) {
	discovery := &fake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	discovery.Resources = []*metav1.APIResourceList{{
		GroupVersion: "argoproj.io/v1alpha1",
		APIResources: []metav1.APIResource{
			{Name: "workflows"},
			{Name: "workflowtemplates"},
			{Name: "clusterworkflowtemplates"},
			{Name: "cronworkflows"},
		},
	}}
	repository := NewRepository(nil, discovery, nil)
	available, err := repository.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !available[WorkflowTemplates] {
		t.Fatal("WorkflowTemplates should be available")
	}
	if available[EventSources] || available[Sensors] || available[EventBuses] {
		t.Fatalf("optional resources should be hidden: %+v", available)
	}
}

func TestListNamespacedResources(t *testing.T) {
	descriptor, _ := Lookup("wft")
	object := resourceObject(descriptor, "argo", "payments")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{descriptor.GVR: "WorkflowTemplateList"},
		object,
	)
	repository := NewRepository(client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	updates := repository.Start(ctx, Request{
		Generation:       3,
		Descriptor:       descriptor,
		CurrentNamespace: "argo",
	})
	snapshot := <-updates
	cancel()
	if snapshot.State != "fresh" || len(snapshot.Items) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Items[0].Name != "payments" || snapshot.Items[0].Template != "main" {
		t.Fatalf("unexpected item: %+v", snapshot.Items[0])
	}
}

func TestForbiddenNamespaceDoesNotHideHealthyNamespace(t *testing.T) {
	descriptor, _ := Lookup("cron")
	object := resourceObject(descriptor, "healthy", "nightly")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{descriptor.GVR: "CronWorkflowList"},
		object,
	)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: descriptor.GVR.Group, Resource: descriptor.GVR.Resource}, "", errors.New("denied"))
	client.PrependReactor("list", descriptor.GVR.Resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		namespace := action.GetNamespace()
		if namespace == "" || namespace == "denied" {
			return true, nil, forbidden
		}
		return false, nil, nil
	})
	repository := NewRepository(client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	updates := repository.Start(ctx, Request{
		Generation:    1,
		Descriptor:    descriptor,
		AllNamespaces: true,
		Namespaces:    []string{"healthy", "denied"},
	})
	snapshot := <-updates
	cancel()
	if snapshot.State != "partially authorized" || len(snapshot.Items) != 1 || !snapshot.Fallback {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.PartialErrors["denied"] != "forbidden" {
		t.Fatalf("partial errors = %+v", snapshot.PartialErrors)
	}
}

func TestMissingOptionalResourceIsScoped(t *testing.T) {
	descriptor, _ := Lookup("es")
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{descriptor.GVR: "EventSourceList"},
	)
	client.PrependReactor("list", descriptor.GVR.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: descriptor.GVR.Group, Resource: descriptor.GVR.Resource}, "")
	})
	repository := NewRepository(client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snapshot := <-repository.Start(ctx, Request{Generation: 1, Descriptor: descriptor, CurrentNamespace: "argo"})
	if !snapshot.Missing || snapshot.State != "unavailable" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestGetReturnsMetadataConditionsAndYAMLWithIdentityCheck(t *testing.T) {
	descriptor, _ := Lookup("sensor")
	object := resourceObject(descriptor, "argo", "listener")
	object.SetUID("uid-1")
	object.SetResourceVersion("9")
	object.SetLabels(map[string]string{"team": "events"})
	object.SetAnnotations(map[string]string{"note": "diagnostic"})
	object.SetOwnerReferences([]metav1.OwnerReference{{Kind: "EventBus", Name: "default", UID: "owner-1"}})
	if err := unstructured.SetNestedSlice(object.Object, []any{
		map[string]any{"type": "Deployed", "status": "True", "reason": "Healthy", "message": "sensor is ready"},
	}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{descriptor.GVR: "SensorList"},
		object,
	)
	repository := NewRepository(client, nil, nil)
	detail, err := repository.Get(context.Background(), descriptor, "argo", "listener", "uid-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Labels["team"] != "events" || len(detail.Conditions) != 1 || detail.Conditions[0].Reason != "Healthy" {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if detail.YAML == "" {
		t.Fatal("YAML should not be empty")
	}
	if _, err := repository.Get(context.Background(), descriptor, "argo", "listener", "different-uid"); err == nil {
		t.Fatal("Get() should reject a changed UID")
	}
}

func TestNormalizeConditionsAndCronSuspension(t *testing.T) {
	eventDescriptor, _ := Lookup("sensor")
	event := resourceObject(eventDescriptor, "argo", "listener")
	if err := unstructured.SetNestedSlice(event.Object, []any{
		map[string]any{"type": "Deployed", "status": "True"},
	}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if got := normalize(eventDescriptor, event).Status; got != "Deployed" {
		t.Fatalf("event status = %q", got)
	}

	cronDescriptor, _ := Lookup("cron")
	cron := resourceObject(cronDescriptor, "argo", "nightly")
	if err := unstructured.SetNestedField(cron.Object, true, "spec", "suspend"); err != nil {
		t.Fatal(err)
	}
	if got := normalize(cronDescriptor, cron).Status; got != "Suspended" {
		t.Fatalf("cron status = %q", got)
	}
}

func resourceObject(descriptor Descriptor, namespace, name string) *unstructured.Unstructured {
	kinds := map[Kind]string{
		Workflows:                "Workflow",
		WorkflowTemplates:        "WorkflowTemplate",
		ClusterWorkflowTemplates: "ClusterWorkflowTemplate",
		CronWorkflows:            "CronWorkflow",
		EventSources:             "EventSource",
		Sensors:                  "Sensor",
		EventBuses:               "EventBus",
	}
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": descriptor.GVR.Group + "/" + descriptor.GVR.Version,
		"kind":       kinds[descriptor.Kind],
		"metadata": map[string]any{
			"namespace": namespace,
			"name":      name,
		},
		"spec": map[string]any{"entrypoint": "main"},
	}}
	return object
}
