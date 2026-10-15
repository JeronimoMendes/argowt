package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
