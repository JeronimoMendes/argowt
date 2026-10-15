package resources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

const ControllerInstanceIDLabel = "workflows.argoproj.io/controller-instanceid"

type Summary struct {
	Kind              Kind
	Namespace         string
	Name              string
	UID               string
	ResourceVersion   string
	Status            string
	Template          string
	InstanceID        string
	Labels            map[string]string
	CreationTimestamp time.Time
}

type Request struct {
	Generation       uint64
	Descriptor       Descriptor
	AllNamespaces    bool
	Namespaces       []string
	CurrentNamespace string
	InstanceID       string
	LabelSelector    string
}

type Snapshot struct {
	Generation    uint64
	Descriptor    Descriptor
	Items         []Summary
	State         string
	Scope         []string
	Fallback      bool
	Missing       bool
	PartialErrors map[string]string
	Err           error
}

type NamespaceSource interface {
	List(context.Context, metav1.ListOptions) ([]string, error)
}

type Repository struct {
	client     dynamic.Interface
	discovery  discovery.DiscoveryInterface
	namespaces NamespaceSource
	retryWait  func(context.Context, int) bool
}

func NewRepository(client dynamic.Interface, discovery discovery.DiscoveryInterface, namespaces NamespaceSource) *Repository {
	return &Repository{
		client:     client,
		discovery:  discovery,
		namespaces: namespaces,
		retryWait:  retryWait,
	}
}

func (repository *Repository) Discover(ctx context.Context) (map[Kind]bool, error) {
	resources, err := repository.discovery.ServerResourcesForGroupVersion("argoproj.io/v1alpha1")
	if err != nil {
		return nil, fmt.Errorf("discover Argo resources: %w", err)
	}
	apiResources := make(map[string]bool, len(resources.APIResources))
	for _, resource := range resources.APIResources {
		apiResources[resource.Name] = true
	}
	available := make(map[Kind]bool, len(descriptors))
	for _, descriptor := range descriptors {
		available[descriptor.Kind] = apiResources[descriptor.GVR.Resource]
	}
	return available, nil
}

func (repository *Repository) Start(ctx context.Context, request Request) <-chan Snapshot {
	output := make(chan Snapshot, 1)
	go func() {
		defer close(output)
		repository.run(ctx, request, output)
	}()
	return output
}

type scopeState struct {
	items           map[string]Summary
	resourceVersion string
	stale           bool
	err             error
}

type scopeUpdate struct {
	scope string
	state scopeState
}

func (repository *Repository) run(ctx context.Context, request Request, output chan<- Snapshot) {
	scopes, states, fallback, warnings, missing, err := repository.initial(ctx, request)
	if err != nil || missing {
		send(ctx, output, Snapshot{
			Generation: request.Generation,
			Descriptor: request.Descriptor,
			State:      "unavailable",
			Missing:    missing,
			Err:        err,
		})
		return
	}
	emit(ctx, output, request, scopes, states, fallback, warnings)

	updates := make(chan scopeUpdate)
	for scope, state := range states {
		go repository.watchScope(ctx, request, scope, state, updates)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
			states[update.scope] = update.state
			emit(ctx, output, request, scopes, states, fallback, warnings)
		}
	}
}

func (repository *Repository) initial(ctx context.Context, request Request) ([]string, map[string]scopeState, bool, map[string]string, bool, error) {
	if !request.Descriptor.Namespaced {
		list, err := repository.list(ctx, request, "")
		if err != nil {
			return nil, nil, false, nil, apierrors.IsNotFound(err), scopedError(request.Descriptor.Title, err)
		}
		return []string{"cluster"}, map[string]scopeState{"cluster": stateFromList(list, request.Descriptor)}, false, nil, false, nil
	}

	if request.AllNamespaces {
		list, err := repository.list(ctx, request, "")
		if err == nil {
			return []string{"*"}, map[string]scopeState{"*": stateFromList(list, request.Descriptor)}, false, nil, false, nil
		}
		if apierrors.IsNotFound(err) {
			return nil, nil, false, nil, true, nil
		}
		if !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err) {
			return nil, nil, false, nil, false, scopedError(request.Descriptor.Title, err)
		}
		warnings := map[string]string{"all namespaces": conciseError(err)}
		scopes, discoveryError := repository.fallbackScopes(ctx, request)
		if discoveryError != nil {
			warnings["namespace discovery"] = conciseError(discoveryError)
		}
		states, listWarnings, missing, listErr := repository.listScopes(ctx, request, scopes)
		for scope, warning := range listWarnings {
			warnings[scope] = warning
		}
		return scopes, states, true, warnings, missing, listErr
	}

	scopes := unique(request.Namespaces)
	if len(scopes) == 0 && request.CurrentNamespace != "" {
		scopes = []string{request.CurrentNamespace}
	}
	if len(scopes) == 0 {
		return nil, nil, false, nil, false, errors.New("no namespace is selected")
	}
	states, warnings, missing, err := repository.listScopes(ctx, request, scopes)
	return scopes, states, false, warnings, missing, err
}

func (repository *Repository) fallbackScopes(ctx context.Context, request Request) ([]string, error) {
	if configured := unique(request.Namespaces); len(configured) > 0 {
		return configured, nil
	}
	if repository.namespaces != nil {
		namespaces, err := repository.namespaces.List(ctx, metav1.ListOptions{})
		if err == nil && len(namespaces) > 0 {
			return unique(namespaces), nil
		}
		if request.CurrentNamespace != "" {
			return []string{request.CurrentNamespace}, err
		}
		return nil, err
	}
	if request.CurrentNamespace != "" {
		return []string{request.CurrentNamespace}, nil
	}
	return nil, errors.New("no fallback namespaces are available")
}

func (repository *Repository) listScopes(ctx context.Context, request Request, scopes []string) (map[string]scopeState, map[string]string, bool, error) {
	// List all scopes in parallel. Sequential lists add up to seconds on clusters with many namespaces.
	lists := make([]*unstructured.UnstructuredList, len(scopes))
	errs := make([]error, len(scopes))
	var group sync.WaitGroup
	for index, scope := range scopes {
		group.Add(1)
		go func() {
			defer group.Done()
			lists[index], errs[index] = repository.list(ctx, request, scope)
		}()
	}
	group.Wait()

	states := make(map[string]scopeState, len(scopes))
	warnings := make(map[string]string)
	var failures []error
	for index, scope := range scopes {
		list, err := lists[index], errs[index]
		if apierrors.IsNotFound(err) {
			return nil, nil, true, nil
		}
		if err != nil {
			warnings[scope] = conciseError(err)
			failures = append(failures, err)
			continue
		}
		states[scope] = stateFromList(list, request.Descriptor)
	}
	if len(states) == 0 {
		return nil, warnings, false, fmt.Errorf("list %s: %w", request.Descriptor.Title, errors.Join(failures...))
	}
	return states, warnings, false, nil
}

func (repository *Repository) list(ctx context.Context, request Request, namespace string) (*unstructured.UnstructuredList, error) {
	return repository.interfaceFor(request.Descriptor, namespace).List(ctx, metav1.ListOptions{
		LabelSelector: combinedSelector(request.InstanceID, request.LabelSelector),
	})
}

func (repository *Repository) watchScope(ctx context.Context, request Request, scope string, state scopeState, output chan<- scopeUpdate) {
	namespace := scope
	if scope == "*" || scope == "cluster" {
		namespace = ""
	}
	attempt := 0
	for ctx.Err() == nil {
		stream, err := repository.interfaceFor(request.Descriptor, namespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:       combinedSelector(request.InstanceID, request.LabelSelector),
			ResourceVersion:     state.resourceVersion,
			AllowWatchBookmarks: true,
		})
		if err == nil {
			err = consume(ctx, stream, request.Descriptor, &state, func() {
				sendScope(ctx, output, scopeUpdate{scope: scope, state: state})
			})
		}
		if ctx.Err() != nil {
			return
		}
		state.stale = true
		state.err = err
		sendScope(ctx, output, scopeUpdate{scope: scope, state: state})
		if !repository.retryWait(ctx, attempt) {
			return
		}
		attempt++
		list, listErr := repository.list(ctx, request, namespace)
		if listErr != nil {
			state.err = listErr
			sendScope(ctx, output, scopeUpdate{scope: scope, state: state})
			continue
		}
		state = stateFromList(list, request.Descriptor)
		attempt = 0
		sendScope(ctx, output, scopeUpdate{scope: scope, state: state})
	}
}

func consume(ctx context.Context, stream watch.Interface, descriptor Descriptor, state *scopeState, changed func()) error {
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-stream.ResultChan():
			if !open {
				return errors.New("resource watch closed")
			}
			if event.Type == watch.Error {
				return apierrors.FromObject(event.Object)
			}
			object, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}
			state.resourceVersion = object.GetResourceVersion()
			key := object.GetNamespace() + "/" + object.GetName()
			if event.Type == watch.Deleted {
				delete(state.items, key)
			} else {
				state.items[key] = normalize(descriptor, object)
			}
			state.stale = false
			state.err = nil
			changed()
		}
	}
}

func (repository *Repository) interfaceFor(descriptor Descriptor, namespace string) dynamic.ResourceInterface {
	resource := repository.client.Resource(descriptor.GVR)
	if descriptor.Namespaced {
		return resource.Namespace(namespace)
	}
	return resource
}

func stateFromList(list *unstructured.UnstructuredList, descriptor Descriptor) scopeState {
	state := scopeState{items: make(map[string]Summary, len(list.Items)), resourceVersion: list.GetResourceVersion()}
	for index := range list.Items {
		item := normalize(descriptor, &list.Items[index])
		state.items[item.Namespace+"/"+item.Name] = item
	}
	return state
}

func normalize(descriptor Descriptor, object *unstructured.Unstructured) Summary {
	status, _, _ := unstructured.NestedString(object.Object, "status", "phase")
	if status == "" {
		status = conditionStatus(object)
	}
	if descriptor.Kind == CronWorkflows {
		if suspended, _, _ := unstructured.NestedBool(object.Object, "spec", "suspend"); suspended {
			status = "Suspended"
		} else if active, _, _ := unstructured.NestedSlice(object.Object, "status", "active"); len(active) > 0 {
			status = "Active"
		}
	}
	if status == "" {
		status = "Ready"
	}
	template, _, _ := unstructured.NestedString(object.Object, "spec", "entrypoint")
	if reference, _, _ := unstructured.NestedString(object.Object, "spec", "workflowTemplateRef", "name"); reference != "" {
		template = reference
	}
	return Summary{
		Kind:              descriptor.Kind,
		Namespace:         object.GetNamespace(),
		Name:              object.GetName(),
		UID:               string(object.GetUID()),
		ResourceVersion:   object.GetResourceVersion(),
		Status:            status,
		Template:          template,
		InstanceID:        object.GetLabels()[ControllerInstanceIDLabel],
		Labels:            copyMap(object.GetLabels()),
		CreationTimestamp: object.GetCreationTimestamp().Time,
	}
}

func conditionStatus(object *unstructured.Unstructured) string {
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || !strings.EqualFold(fmt.Sprint(condition["status"]), "true") {
			continue
		}
		if conditionType := fmt.Sprint(condition["type"]); conditionType != "" {
			return conditionType
		}
	}
	return ""
}

func emit(ctx context.Context, output chan<- Snapshot, request Request, scopes []string, states map[string]scopeState, fallback bool, staticWarnings map[string]string) {
	byIdentity := make(map[string]Summary)
	warnings := make(map[string]string, len(staticWarnings)+len(states))
	for key, warning := range staticWarnings {
		warnings[key] = warning
	}
	fresh := 0
	for scope, state := range states {
		for key, item := range state.items {
			byIdentity[key] = item
		}
		if state.stale {
			warnings[scope] = conciseError(state.err)
		} else {
			fresh++
		}
	}
	items := make([]Summary, 0, len(byIdentity))
	for _, item := range byIdentity {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreationTimestamp.Equal(items[j].CreationTimestamp) {
			return items[i].CreationTimestamp.After(items[j].CreationTimestamp)
		}
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
	state := "fresh"
	if len(warnings) > 0 && fresh > 0 {
		state = "partially authorized"
	} else if len(warnings) > 0 {
		state = "reconnecting"
	}
	send(ctx, output, Snapshot{
		Generation:    request.Generation,
		Descriptor:    request.Descriptor,
		Items:         items,
		State:         state,
		Scope:         append([]string(nil), scopes...),
		Fallback:      fallback,
		PartialErrors: warnings,
	})
}

func combinedSelector(instanceID, selector string) string {
	parts := make([]string, 0, 2)
	if instanceID != "" {
		parts = append(parts, labels.Set{ControllerInstanceIDLabel: instanceID}.AsSelector().String())
	}
	if selector != "" {
		parts = append(parts, selector)
	}
	return strings.Join(parts, ",")
}

func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func copyMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func conciseError(err error) string {
	if err == nil {
		return "unknown error"
	}
	if apierrors.IsForbidden(err) {
		return "forbidden"
	}
	if apierrors.IsUnauthorized(err) {
		return "unauthorized"
	}
	return err.Error()
}

func scopedError(resource string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("list %s: %w", resource, err)
}

func retryWait(ctx context.Context, attempt int) bool {
	if attempt > 5 {
		attempt = 5
	}
	delay := time.Second * time.Duration(1<<attempt)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func send(ctx context.Context, output chan<- Snapshot, snapshot Snapshot) {
	select {
	case <-ctx.Done():
	case output <- snapshot:
	}
}

func sendScope(ctx context.Context, output chan<- scopeUpdate, update scopeUpdate) {
	select {
	case <-ctx.Done():
	case output <- update:
	}
}
