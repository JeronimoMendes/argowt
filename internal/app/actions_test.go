package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"argowt/internal/workflows"
)

type fakeOperationService struct {
	operations []workflows.Operation
	targets    []workflows.OperationTarget
	err        error
}

// Run records the requested operation and target, then returns the configured error.
func (service *fakeOperationService) Run(_ context.Context, operation workflows.Operation, target workflows.OperationTarget) (workflows.OperationResult, error) {
	service.operations = append(service.operations, operation)
	service.targets = append(service.targets, target)
	return workflows.OperationResult{Operation: operation, Namespace: target.Namespace, Name: target.Name}, service.err
}

// key converts a test shortcut into a Bubble Tea key message.
func key(text string) tea.KeyMsg {
	switch text {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}
}

// press sends keys through the model and returns the final model and command.
func press(t *testing.T, model Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var command tea.Cmd
	for _, text := range keys {
		var updated tea.Model
		updated, command = model.Update(key(text))
		model = updated.(Model)
	}
	return model, command
}

// actionModel creates a fresh workflow list model with delete permission denied by its access checker.
func actionModel(service *fakeOperationService, items ...workflows.Summary) Model {
	model := NewModel(ModelOptions{
		Generation:       1,
		Context:          "kind-argowt-dev",
		RootContext:      context.Background(),
		OperationService: service,
		CheckAccess: func(context.Context, string) map[workflows.Operation]bool {
			return map[workflows.Operation]bool{workflows.OperationDelete: false}
		},
	})
	model.connectionState = workflows.StateFresh
	model.items = items
	model.width, model.height = 120, 30
	return model
}

// running returns a running workflow summary with a fixed identity and resource version.
func running() workflows.Summary {
	return workflows.Summary{Namespace: "argo", Name: "etl-abc", UID: "uid-1", ResourceVersion: "7", Phase: "Running"}
}

// TestActionMenuConfirmsWithContextAndRuns checks permission feedback, confirmation details, and the operation result notice.
func TestActionMenuConfirmsWithContextAndRuns(t *testing.T) {
	service := &fakeOperationService{}
	model, command := press(t, actionModel(service, running()), "a")
	if !model.actionVisible || command == nil {
		t.Fatal("action menu did not open and check permissions")
	}
	updated, _ := model.Update(command())
	model = updated.(Model)
	view := plain(model.View())
	if !strings.Contains(view, "Workflow actions") || !strings.Contains(view, "unavailable: only Failed or Error workflows") {
		t.Fatalf("menu view = %s", view)
	}
	if !strings.Contains(view, "unavailable: not permitted by RBAC") {
		t.Fatalf("RBAC denial not shown: %s", view)
	}

	model, _ = press(t, model, "x")
	view = plain(model.View())
	for _, want := range []string{"Confirm stop", "kind-argowt-dev", "argo", "etl-abc", "uid-1", "Running", "y confirm"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation lacks %q: %s", want, view)
		}
	}
	model, command = press(t, model, "y")
	if command == nil || model.actionStage != "running" {
		t.Fatal("confirmation did not run the operation")
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if len(service.operations) != 1 || service.operations[0] != workflows.OperationStop || service.targets[0].UID != "uid-1" || service.targets[0].ResourceVersion != "7" {
		t.Fatalf("operations = %v targets = %+v", service.operations, service.targets)
	}
	if model.actionVisible || !strings.Contains(plain(model.View()), "Stopped workflow argo/etl-abc") {
		t.Fatalf("result not shown: %s", plain(model.View()))
	}
}

// TestActionConfirmationCancelsAndIgnoresUnavailable checks that unavailable actions and cancellation never run operations.
func TestActionConfirmationCancelsAndIgnoresUnavailable(t *testing.T) {
	service := &fakeOperationService{}
	model, command := press(t, actionModel(service, running()), "a")
	updated, _ := model.Update(command())
	model = updated.(Model)
	model, _ = press(t, model, "r")
	if model.actionStage != "menu" {
		t.Fatalf("retry of a running workflow reached stage %q", model.actionStage)
	}
	model, _ = press(t, model, "D")
	if model.actionStage != "menu" {
		t.Fatalf("RBAC-denied delete reached stage %q", model.actionStage)
	}
	model, _ = press(t, model, "s", "n")
	if model.actionStage != "menu" {
		t.Fatalf("n did not cancel: stage %q", model.actionStage)
	}
	model, _ = press(t, model, "esc")
	if model.actionVisible || len(service.operations) != 0 {
		t.Fatalf("menu visible=%t operations=%v", model.actionVisible, service.operations)
	}
}

// TestActionMenuIgnoresPastedText checks that coalesced or pasted action keys leave the menu open.
func TestActionMenuIgnoresPastedText(t *testing.T) {
	service := &fakeOperationService{}
	model, _ := press(t, actionModel(service, running()), "a")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("xy")})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Paste: true})
	model = updated.(Model)
	if model.actionStage != "menu" {
		t.Fatalf("pasted text reached stage %q", model.actionStage)
	}
}

// TestActionsDisabledWhileStale checks that reconnecting workflow data prevents action confirmation.
func TestActionsDisabledWhileStale(t *testing.T) {
	model := actionModel(&fakeOperationService{}, running())
	model.connectionState = workflows.StateReconnecting
	model, _ = press(t, model, "a", "x")
	if model.actionStage != "menu" || !strings.Contains(plain(model.View()), "workflow list is reconnecting") {
		t.Fatalf("stale actions allowed: stage %q view %s", model.actionStage, plain(model.View()))
	}
}

// TestActionTargetIsCapturedAtConfirmation checks that later updates do not replace the confirmed workflow state.
func TestActionTargetIsCapturedAtConfirmation(t *testing.T) {
	service := &fakeOperationService{}
	model, _ := press(t, actionModel(service, running()), "a")
	progressed := running()
	progressed.ResourceVersion = "8"
	updated, _ := model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{Generation: 1, State: workflows.StateFresh, Workflows: []workflows.Summary{progressed}}})
	model = updated.(Model)
	model, _ = press(t, model, "x")
	finished := progressed
	finished.ResourceVersion = "9"
	finished.Phase = "Succeeded"
	updated, _ = model.Update(snapshotMessage{open: true, snapshot: workflows.Snapshot{Generation: 1, State: workflows.StateFresh, Workflows: []workflows.Summary{finished}}})
	model = updated.(Model)
	_, command := press(t, model, "y")
	command()
	if service.targets[0].ResourceVersion != "8" || service.targets[0].Phase != "Running" {
		t.Fatalf("target = %+v, want the state confirmed", service.targets[0])
	}
}

// TestOperationConflictAndForbiddenAreReported checks conflict feedback, no replay, and caching of runtime RBAC denials.
func TestOperationConflictAndForbiddenAreReported(t *testing.T) {
	service := &fakeOperationService{err: &workflows.ConflictError{Operation: workflows.OperationStop, Namespace: "argo", Name: "etl-abc", Reason: "changed from Running to Succeeded"}}
	model, _ := press(t, actionModel(service, running()), "a", "x")
	model, command := press(t, model, "y")
	updated, _ := model.Update(command())
	model = updated.(Model)
	if view := plain(model.View()); !strings.Contains(view, "conflict: stop not applied") {
		t.Fatalf("conflict not shown: %s", view)
	}
	if len(service.operations) != 1 {
		t.Fatalf("operation replayed: %v", service.operations)
	}

	service.err = apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "workflows"}, "etl-abc", errors.New("denied"))
	model, _ = press(t, model, "a", "s")
	model, command = press(t, model, "y")
	updated, _ = model.Update(command())
	model = updated.(Model)
	if !strings.Contains(plain(model.View()), "suspend argo/etl-abc is forbidden") {
		t.Fatalf("forbidden not shown: %s", plain(model.View()))
	}
	if allowed, known := model.permissions["argo"][workflows.OperationSuspend]; !known || allowed {
		t.Fatal("forbidden suspend was not remembered")
	}
}

// TestResubmitOpensSubmissionForm checks that resubmission loads the selected workflow with its UID.
func TestResubmitOpensSubmissionForm(t *testing.T) {
	submissions := &fakeSubmissionService{result: workflows.SubmissionResult{Namespace: "argo", Name: "etl-def"}}
	model := actionModel(&fakeOperationService{}, running())
	model.submissionService = submissions
	model, command := press(t, model, "a", "R")
	if !model.submissionVisible || model.actionVisible || command == nil {
		t.Fatal("resubmit did not open the submission form")
	}
	updated, _ := model.Update(command())
	model = updated.(Model)
	source := model.submissionForm.Source
	if source.Kind != workflows.SourceWorkflow || source.Name != "etl-abc" || source.UID != "uid-1" {
		t.Fatalf("source = %+v", source)
	}
}

// TestLocalManifestSubmissionAsksForPath checks path entry, empty path rejection, and parameter form loading.
func TestLocalManifestSubmissionAsksForPath(t *testing.T) {
	submissions := &fakeSubmissionService{}
	model := actionModel(&fakeOperationService{}, running())
	model.submissionService = submissions
	model.currentNamespace = "argo"
	model, _ = press(t, model, "S")
	if model.submissionStage != "path" || !strings.Contains(plain(model.View()), "manifest path") {
		t.Fatalf("path prompt not shown: %s", plain(model.View()))
	}
	model, _ = press(t, model, "enter")
	if model.submissionError == nil {
		t.Fatal("an empty path was accepted")
	}
	model, command := press(t, model, "./hello.yaml", "enter")
	if command == nil {
		t.Fatal("path did not load")
	}
	updated, _ := model.Update(command())
	model = updated.(Model)
	if model.submissionStage != "edit" || model.submissionForm.Source.Path != "./hello.yaml" || model.submissionForm.DestinationNamespace != "argo" {
		t.Fatalf("stage %q form %+v", model.submissionStage, model.submissionForm)
	}
}

// TestDeleteFromExecutionReturnsToList checks the deletion warning and return to the list after a successful delete.
func TestDeleteFromExecutionReturnsToList(t *testing.T) {
	service := &fakeOperationService{}
	model := actionModel(service)
	model.executionVisible = true
	model.execution = &workflows.Execution{Namespace: "argo", Name: "etl-abc", UID: "uid-1", ResourceVersion: "7", Phase: "Failed"}
	model.checkAccess = nil
	model, _ = press(t, model, "a", "D")
	if !strings.Contains(plain(model.View()), "This cannot be undone") {
		t.Fatalf("delete confirmation = %s", plain(model.View()))
	}
	model, command := press(t, model, "y")
	updated, _ := model.Update(command())
	model = updated.(Model)
	if model.executionVisible || !strings.Contains(model.actionNotice, "Deleted workflow argo/etl-abc") {
		t.Fatalf("execution visible=%t notice=%q", model.executionVisible, model.actionNotice)
	}
}

// plain removes ANSI escapes from a rendered view for assertions.
func plain(view string) string {
	return ansi.Strip(view)
}
