// Package resources implements the read-only Argo resource browser without
// coupling Kubernetes objects to Bubble Tea.
package resources

import (
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Kind string

const (
	Workflows                Kind = "workflows"
	WorkflowTemplates        Kind = "workflowtemplates"
	ClusterWorkflowTemplates Kind = "clusterworkflowtemplates"
	CronWorkflows            Kind = "cronworkflows"
	EventSources             Kind = "eventsources"
	Sensors                  Kind = "sensors"
	EventBuses               Kind = "eventbuses"
)

type Descriptor struct {
	Kind       Kind
	Title      string
	ShortName  string
	Namespaced bool
	Optional   bool
	GVR        schema.GroupVersionResource
}

var descriptors = []Descriptor{
	{Kind: Workflows, Title: "Workflows", ShortName: "wf", Namespaced: true, GVR: gvr("workflows")},
	{Kind: WorkflowTemplates, Title: "WorkflowTemplates", ShortName: "wft", Namespaced: true, GVR: gvr("workflowtemplates")},
	{Kind: ClusterWorkflowTemplates, Title: "ClusterWorkflowTemplates", ShortName: "cwft", GVR: gvr("clusterworkflowtemplates")},
	{Kind: CronWorkflows, Title: "CronWorkflows", ShortName: "cron", Namespaced: true, GVR: gvr("cronworkflows")},
	{Kind: EventSources, Title: "EventSources", ShortName: "es", Namespaced: true, Optional: true, GVR: gvr("eventsources")},
	{Kind: Sensors, Title: "Sensors", ShortName: "sensor", Namespaced: true, Optional: true, GVR: gvr("sensors")},
	{Kind: EventBuses, Title: "EventBuses", ShortName: "eventbus", Namespaced: true, Optional: true, GVR: gvr("eventbus")},
}

func gvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: resource}
}

func Descriptors() []Descriptor {
	return append([]Descriptor(nil), descriptors...)
}

func Lookup(value string) (Descriptor, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, descriptor := range descriptors {
		if value == string(descriptor.Kind) || value == descriptor.ShortName || value == strings.ToLower(descriptor.Title) {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

func Commands(available map[Kind]bool, query string) []Descriptor {
	query = strings.ToLower(strings.TrimSpace(query))
	matches := make([]Descriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if available != nil && !available[descriptor.Kind] {
			continue
		}
		haystack := strings.ToLower(descriptor.ShortName + " " + descriptor.Title)
		if query == "" || fuzzy(query, haystack) {
			matches = append(matches, descriptor)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		leftExact := query != "" && (matches[i].ShortName == query || string(matches[i].Kind) == query)
		rightExact := query != "" && (matches[j].ShortName == query || string(matches[j].Kind) == query)
		if leftExact != rightExact {
			return leftExact
		}
		return matches[i].Title < matches[j].Title
	})
	return matches
}

func fuzzy(needle, haystack string) bool {
	index := 0
	runes := []rune(needle)
	for _, character := range haystack {
		if index < len(runes) && character == runes[index] {
			index++
		}
	}
	return index == len(runes)
}
