package workflows

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	wfv1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	argoclient "github.com/argoproj/argo-workflows/v3/pkg/client/clientset/versioned"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
)

type ConnectionState string

const (
	StateLoading      ConnectionState = "loading"
	StateFresh        ConnectionState = "fresh"
	StatePartial      ConnectionState = "partially authorized"
	StateStale        ConnectionState = "stale"
	StateReconnecting ConnectionState = "reconnecting"
	StateFatal        ConnectionState = "fatal error"
)

type Request struct {
	Generation       uint64
	AllNamespaces    bool
	Namespaces       []string
	CurrentNamespace string
	InstanceID       string
	ActiveOnly       bool
}

type Snapshot struct {
	Generation    uint64
	Workflows     []Summary
	State         ConnectionState
	Fallback      bool
	Scope         []string
	PartialErrors map[string]string
	Err           error
}

type WorkflowClient interface {
	Get(context.Context, string, metav1.GetOptions) (*wfv1.Workflow, error)
	List(context.Context, metav1.ListOptions) (*wfv1.WorkflowList, error)
	Watch(context.Context, metav1.ListOptions) (watch.Interface, error)
}

type WorkflowSource interface {
	Workflows(namespace string) WorkflowClient
}

// ArgoSource adapts the generated Argo client to the repository's narrow
// interface.
type ArgoSource struct {
	Client argoclient.Interface
}

func (source ArgoSource) Workflows(namespace string) WorkflowClient {
	return source.Client.ArgoprojV1alpha1().Workflows(namespace)
}

// NamespaceClient is the small portion of the core Kubernetes client needed
// for adaptive namespace discovery.
type NamespaceClient interface {
	List(context.Context, metav1.ListOptions) ([]string, error)
}

type Repository struct {
	workflows  WorkflowSource
	namespaces NamespaceClient
	retryWait  func(context.Context, int) bool
}

func NewRepository(workflows WorkflowSource, namespaces NamespaceClient) *Repository {
	return &Repository{workflows: workflows, namespaces: namespaces, retryWait: waitForRetry}
}

func (repository *Repository) Start(ctx context.Context, request Request) <-chan Snapshot {
	updates := make(chan Snapshot, 1)
	go func() {
		defer close(updates)
		repository.run(ctx, request, updates)
	}()
	return updates
}

type scopedState struct {
	workflows       map[string]Summary
	resourceVersion string
	stale           bool
	reconnecting    bool
	err             error
}

type scopedUpdate struct {
	scope string
	state scopedState
}

func (repository *Repository) run(ctx context.Context, request Request, output chan<- Snapshot) {
	scopes, initial, fallback, warnings, err := repository.initial(ctx, request)
	if err != nil {
		sendSnapshot(ctx, output, Snapshot{
			Generation: request.Generation,
			State:      StateFatal,
			Err:        err,
		})
		return
	}

	states := make(map[string]scopedState, len(initial))
	for scope, state := range initial {
		states[scope] = state
	}
	emitCombined(ctx, output, request.Generation, scopes, states, fallback, warnings)

	workerUpdates := make(chan scopedUpdate)
	for scope, state := range states {
		go repository.watchScope(ctx, request, scope, state, workerUpdates)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-workerUpdates:
			states[update.scope] = update.state
			emitCombined(ctx, output, request.Generation, scopes, states, fallback, warnings)
		}
	}
}

func (repository *Repository) initial(ctx context.Context, request Request) ([]string, map[string]scopedState, bool, map[string]string, error) {
	options := listOptions(request)
	if request.AllNamespaces {
		list, err := repository.workflows.Workflows("").List(ctx, options)
		if err == nil {
			return []string{"*"}, map[string]scopedState{"*": stateFromList(list, request)}, false, nil, nil
		}
		if !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err) {
			return nil, nil, false, nil, fmt.Errorf("list workflows across all namespaces: %w", err)
		}
		warnings := map[string]string{"all namespaces": conciseError(err)}
		scopes, discoveryWarning, resolveErr := repository.fallbackNamespaces(ctx, request)
		if resolveErr != nil {
			return nil, nil, true, warnings, resolveErr
		}
		if discoveryWarning != nil {
			warnings["namespace discovery"] = conciseError(discoveryWarning)
		}
		initial, listWarnings, listErr := repository.listScopes(ctx, request, scopes)
		for scope, warning := range listWarnings {
			warnings[scope] = warning
		}
		return scopes, initial, true, warnings, listErr
	}

	scopes := uniqueNonEmpty(request.Namespaces)
	if len(scopes) == 0 && request.CurrentNamespace != "" {
		scopes = []string{request.CurrentNamespace}
	}
	if len(scopes) == 0 {
		return nil, nil, false, nil, errors.New("no namespace is selected")
	}
	initial, warnings, err := repository.listScopes(ctx, request, scopes)
	return scopes, initial, false, warnings, err
}

func (repository *Repository) fallbackNamespaces(ctx context.Context, request Request) ([]string, error, error) {
	if configured := uniqueNonEmpty(request.Namespaces); len(configured) > 0 {
		return configured, nil, nil
	}
	if repository.namespaces != nil {
		namespaces, err := repository.namespaces.List(ctx, metav1.ListOptions{})
		if err == nil && len(namespaces) > 0 {
			return uniqueNonEmpty(namespaces), nil, nil
		}
		if request.CurrentNamespace != "" {
			return []string{request.CurrentNamespace}, err, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("discover fallback namespaces: %w", err)
		}
	}
	if request.CurrentNamespace != "" {
		return []string{request.CurrentNamespace}, nil, nil
	}
	return nil, nil, errors.New("all-namespace access is forbidden and no fallback namespaces are available")
}

func (repository *Repository) listScopes(ctx context.Context, request Request, scopes []string) (map[string]scopedState, map[string]string, error) {
	// List all scopes in parallel. Sequential lists add up to seconds on clusters with many namespaces.
	lists := make([]*wfv1.WorkflowList, len(scopes))
	errs := make([]error, len(scopes))
	var group sync.WaitGroup
	for index, scope := range scopes {
		group.Add(1)
		go func() {
			defer group.Done()
			lists[index], errs[index] = repository.workflows.Workflows(scope).List(ctx, listOptions(request))
		}()
	}
	group.Wait()

	states := make(map[string]scopedState, len(scopes))
	warnings := make(map[string]string)
	var failures []error
	for index, scope := range scopes {
		if err := errs[index]; err != nil {
			warnings[scope] = conciseError(err)
			failures = append(failures, fmt.Errorf("namespace %s: %w", scope, err))
			continue
		}
		states[scope] = stateFromList(lists[index], request)
	}
	if len(states) == 0 {
		return nil, warnings, fmt.Errorf("list workflows: %w", errors.Join(failures...))
	}
	return states, warnings, nil
}

func (repository *Repository) watchScope(ctx context.Context, request Request, scope string, state scopedState, output chan<- scopedUpdate) {
	attempt := 0
	for ctx.Err() == nil {
		stream, err := repository.workflows.Workflows(namespaceForScope(scope)).Watch(ctx, metav1.ListOptions{
			LabelSelector:       listOptions(request).LabelSelector,
			ResourceVersion:     state.resourceVersion,
			AllowWatchBookmarks: true,
		})
		if err == nil {
			err = consumeWatch(ctx, stream, request, &state, func() {
				sendScopedUpdate(ctx, output, scopedUpdate{scope: scope, state: state})
			})
		}
		if ctx.Err() != nil {
			return
		}

		state.stale = true
		state.reconnecting = true
		state.err = err
		sendScopedUpdate(ctx, output, scopedUpdate{scope: scope, state: state})
		if !repository.retryWait(ctx, attempt) {
			return
		}
		attempt++

		list, listErr := repository.workflows.Workflows(namespaceForScope(scope)).List(ctx, listOptions(request))
		if listErr != nil {
			state.err = listErr
			sendScopedUpdate(ctx, output, scopedUpdate{scope: scope, state: state})
			continue
		}
		state = stateFromList(list, request)
		attempt = 0
		sendScopedUpdate(ctx, output, scopedUpdate{scope: scope, state: state})
	}
}

func consumeWatch(ctx context.Context, stream watch.Interface, request Request, state *scopedState, changed func()) error {
	defer stream.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-stream.ResultChan():
			if !open {
				return errors.New("workflow watch closed")
			}
			if event.Type == watch.Error {
				return apierrors.FromObject(event.Object)
			}
			workflow, ok := event.Object.(*wfv1.Workflow)
			if !ok {
				continue
			}
			state.resourceVersion = workflow.ResourceVersion

			// A bookmark only carries a resourceVersion, not a workflow.
			if event.Type == watch.Bookmark {
				continue
			}

			key := workflow.Namespace + "/" + workflow.Name
			if event.Type == watch.Deleted || (request.ActiveOnly && !Normalize(workflow).Active()) {
				delete(state.workflows, key)
			} else if request.InstanceID == "" || workflow.Labels[ControllerInstanceIDLabel] == request.InstanceID {
				state.workflows[key] = Normalize(workflow)
			}
			state.stale = false
			state.reconnecting = false
			state.err = nil
			changed()
		}
	}
}

func stateFromList(list *wfv1.WorkflowList, request Request) scopedState {
	state := scopedState{workflows: make(map[string]Summary), resourceVersion: list.ResourceVersion}
	for index := range list.Items {
		summary := Normalize(&list.Items[index])
		if request.ActiveOnly && !summary.Active() {
			continue
		}
		if request.InstanceID != "" && summary.InstanceID != request.InstanceID {
			continue
		}
		state.workflows[summary.Namespace+"/"+summary.Name] = summary
	}
	return state
}

func emitCombined(ctx context.Context, output chan<- Snapshot, generation uint64, scopes []string, states map[string]scopedState, fallback bool, staticWarnings map[string]string) {
	byIdentity := make(map[string]Summary)
	warnings := make(map[string]string, len(staticWarnings)+len(states))
	for scope, warning := range staticWarnings {
		warnings[scope] = warning
	}
	fresh, reconnecting := 0, 0
	for scope, state := range states {
		for key, workflow := range state.workflows {
			byIdentity[key] = workflow
		}
		if state.err != nil {
			warnings[scope] = conciseError(state.err)
		}
		if state.stale {
			if state.reconnecting {
				reconnecting++
			}
		} else {
			fresh++
		}
	}

	items := make([]Summary, 0, len(byIdentity))
	for _, workflow := range byIdentity {
		items = append(items, workflow)
	}
	Sort(items)
	state := StateFresh
	switch {
	case len(warnings) > 0 && fresh > 0:
		state = StatePartial
	case reconnecting > 0:
		state = StateReconnecting
	case len(warnings) > 0:
		state = StateStale
	}
	sendSnapshot(ctx, output, Snapshot{
		Generation:    generation,
		Workflows:     items,
		State:         state,
		Fallback:      fallback,
		Scope:         append([]string(nil), scopes...),
		PartialErrors: warnings,
	})
}

// listOptions filters workflows on the server.
// ActiveOnly skips completed workflows: in a namespace with ~1000 completed
// workflows, this cuts the list from ~80MB to ~2MB.
func listOptions(request Request) metav1.ListOptions {
	selector := labels.NewSelector()
	if request.InstanceID != "" {
		requirement, _ := labels.NewRequirement(ControllerInstanceIDLabel, selection.Equals, []string{request.InstanceID})
		selector = selector.Add(*requirement)
	}
	if request.ActiveOnly {
		requirement, _ := labels.NewRequirement(CompletedLabel, selection.NotEquals, []string{"true"})
		selector = selector.Add(*requirement)
	}
	return metav1.ListOptions{LabelSelector: selector.String()}
}

func namespaceForScope(scope string) string {
	if scope == "*" {
		return ""
	}
	return scope
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func conciseError(err error) string {
	if err == nil {
		return ""
	}
	if apierrors.IsForbidden(err) {
		return "forbidden"
	}
	if apierrors.IsUnauthorized(err) {
		return "unauthorized"
	}
	if apierrors.IsResourceExpired(err) {
		return "resource version expired"
	}
	return err.Error()
}

func waitForRetry(ctx context.Context, attempt int) bool {
	if attempt > 5 {
		attempt = 5
	}
	maximum := time.Second * time.Duration(1<<attempt)
	if maximum > 30*time.Second {
		maximum = 30 * time.Second
	}
	delay := maximum/2 + time.Duration(rand.Int63n(maximum.Nanoseconds()/2+1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sendSnapshot(ctx context.Context, output chan<- Snapshot, snapshot Snapshot) {
	select {
	case <-ctx.Done():
	case output <- snapshot:
	}
}

func sendScopedUpdate(ctx context.Context, output chan<- scopedUpdate, update scopedUpdate) {
	select {
	case <-ctx.Done():
	case output <- update:
	}
}
