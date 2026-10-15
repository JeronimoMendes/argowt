// Package argourl maps Argo Workflows web UI URLs to the resources they show.
package argourl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"argowt/internal/resources"
)

// Query parameters the Argo UI sets on workflow links.
const (
	nodeParam = "nodeId"
	uidParam  = "uid"
)

// Target is the resource a URL shows. An empty Name means the resource list.
type Target struct {
	Kind      resources.Kind
	Namespace string
	Name      string
	UID       string
	NodeID    string
}

// routes maps Argo UI path segments to resource kinds.
var routes = map[string]resources.Kind{
	"workflows":                  resources.Workflows,
	"workflow-templates":         resources.WorkflowTemplates,
	"cluster-workflow-templates": resources.ClusterWorkflowTemplates,
	"cron-workflows":             resources.CronWorkflows,
	"event-sources":              resources.EventSources,
	"sensors":                    resources.Sensors,
}

// Parse reads a URL such as
// https://argo.example.com/workflows/argo/hello?nodeId=hello-123.
// The UI can sit under a base path, so the first known route segment wins.
func Parse(raw string) (Target, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Target{}, fmt.Errorf("parse Argo URL: %w", err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	for index, segment := range segments {
		kind, found := routes[segment]
		if !found {
			continue
		}
		descriptor, _ := resources.Lookup(string(kind))
		return target(descriptor, segments[index+1:], parsed.Query())
	}
	return Target{}, fmt.Errorf("unsupported Argo URL: %s", raw)
}

// target assigns the path segments after the route to namespace and name.
func target(descriptor resources.Descriptor, segments []string, query url.Values) (Target, error) {
	result := Target{Kind: descriptor.Kind}
	if descriptor.Kind == resources.Workflows {
		result.UID = query.Get(uidParam)
		result.NodeID = query.Get(nodeParam)
	}
	if descriptor.Namespaced && len(segments) > 0 {
		result.Namespace, segments = segments[0], segments[1:]
	}
	if len(segments) > 0 {
		result.Name, segments = segments[0], segments[1:]
	}
	if len(segments) > 0 {
		return Target{}, errors.New("unsupported Argo URL path")
	}
	return result, nil
}
