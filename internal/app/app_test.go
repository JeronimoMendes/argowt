package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"argowt/internal/resources"
	"argowt/internal/workflows"
)

func TestModelIgnoresObsoleteGeneration(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 4, Context: "current"})
	updated, _ := model.Update(snapshotMessage{
		open: true,
		snapshot: workflows.Snapshot{
			Generation: 3,
			State:      workflows.StateFresh,
			Workflows:  []workflows.Summary{{Name: "obsolete"}},
		},
	})
	got := updated.(Model)
	if len(got.items) != 0 || got.connectionState != workflows.StateLoading {
		t.Fatalf("obsolete snapshot changed model: %+v", got)
	}
}

func TestMutationSafetyFollowsFreshness(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	if model.MutationAllowed() {
		t.Fatal("mutations must be disabled while loading")
	}

	updated, _ := model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{
		Generation: 1,
		State:      workflows.StateFresh,
	}})
	model = updated.(Model)
	if !model.MutationAllowed() {
		t.Fatal("mutations should be available for fresh data")
	}

	updated, _ = model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{
		Generation: 1,
		State:      workflows.StateStale,
	}})
	model = updated.(Model)
	if model.MutationAllowed() {
		t.Fatal("mutations must be disabled for stale data")
	}
}

func TestFilteringAndNavigation(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	model.items = []workflows.Summary{
		{Namespace: "argo", Name: "daily-payment", Phase: "Running"},
		{Namespace: "other", Name: "cleanup", Phase: "Pending"},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	for _, character := range "pay" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(Model)
	}
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "daily-payment" {
		t.Fatalf("filtered items = %+v", got)
	}
}

func TestInvalidFilterKeepsPreviousValidExpression(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	model.items = []workflows.Summary{
		{Name: "running", Phase: "Running"},
		{Name: "failed", Phase: "Failed"},
	}
	model.query = "status=Running"
	model.updateFilter()
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "running" {
		t.Fatalf("valid filter result = %+v", got)
	}

	model.query = "status="
	model.updateFilter()
	if model.filterError == nil {
		t.Fatal("invalid filter should report an error")
	}
	if got := model.filteredItems(); len(got) != 1 || got[0].Name != "running" {
		t.Fatalf("previous filter was not retained: %+v", got)
	}
}

type fakeResourceRepository struct {
	requests chan resources.Request
}

func (repository *fakeResourceRepository) Discover(context.Context) (map[resources.Kind]bool, error) {
	return map[resources.Kind]bool{resources.Workflows: true, resources.WorkflowTemplates: true}, nil
}

func (repository *fakeResourceRepository) Start(_ context.Context, request resources.Request) <-chan resources.Snapshot {
	repository.requests <- request
	return make(chan resources.Snapshot)
}

func (repository *fakeResourceRepository) Get(_ context.Context, descriptor resources.Descriptor, namespace, name, uid string) (resources.Detail, error) {
	return resources.Detail{
		Summary: resources.Summary{
			Kind:      descriptor.Kind,
			Namespace: namespace,
			Name:      name,
			UID:       uid,
			Status:    "Ready",
		},
		Labels:     map[string]string{"team": "platform"},
		Conditions: []resources.Condition{{Type: "Ready", Status: "True"}},
		YAML:       "apiVersion: argoproj.io/v1alpha1\nkind: Workflow\n",
	}, nil
}

func TestCommandPaletteSwitchesResource(t *testing.T) {
	repository := &fakeResourceRepository{requests: make(chan resources.Request, 1)}
	model := NewModel(ModelOptions{
		Generation:         1,
		RootContext:        context.Background(),
		ResourceRepository: repository,
		CurrentNamespace:   "argo",
		InstanceID:         "controller-a",
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	for _, character := range "wft" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.resourceDescriptor.Kind != resources.WorkflowTemplates {
		t.Fatalf("selected resource = %s", model.resourceDescriptor.Kind)
	}
	request := <-repository.requests
	if request.Descriptor.Kind != resources.WorkflowTemplates || request.CurrentNamespace != "argo" || request.InstanceID != "controller-a" {
		t.Fatalf("unexpected request: %+v", request)
	}
}

func TestEnterOpensMetadataAndYAMLDetail(t *testing.T) {
	repository := &fakeResourceRepository{requests: make(chan resources.Request, 1)}
	model := NewModel(ModelOptions{Generation: 1, RootContext: context.Background(), ResourceRepository: repository})
	model.connectionState = workflows.StateFresh
	model.items = []workflows.Summary{{Namespace: "argo", Name: "example", UID: "uid-1", Phase: "Running"}}

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.detailVisible || !model.detailLoading || command == nil {
		t.Fatalf("detail did not start loading: %+v", model)
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	metadata := model.View()
	if !strings.Contains(metadata, "team=platform") || !strings.Contains(metadata, "Ready=True") {
		t.Fatalf("metadata view = %q", metadata)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if yamlView := model.View(); !strings.Contains(yamlView, "apiVersion: argoproj.io/v1alpha1") {
		t.Fatalf("YAML view = %q", yamlView)
	}
}

func TestDiscoveryHidesUnavailableEventResources(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1})
	updated, _ := model.Update(discoveryMessage{available: map[resources.Kind]bool{
		resources.Workflows:         true,
		resources.WorkflowTemplates: true,
		resources.EventSources:      false,
	}})
	model = updated.(Model)
	for _, command := range resources.Commands(model.availableResources, "") {
		if command.Kind == resources.EventSources {
			t.Fatal("missing EventSource CRD should be hidden")
		}
	}
}

func TestViewSanitizesAPIValues(t *testing.T) {
	model := NewModel(ModelOptions{Generation: 1, Context: "safe\x1b[31m"})
	model.connectionState = workflows.StateFresh
	model.items = []workflows.Summary{{Namespace: "argo", Name: "bad\x1b[2Jname", Phase: "Running"}}
	view := model.View()
	if strings.ContainsRune(view, '\x1b') {
		t.Fatalf("view contains an escape character: %q", view)
	}
	if !strings.Contains(view, "▶ Running") {
		t.Fatalf("view does not include textual status: %q", view)
	}
}
