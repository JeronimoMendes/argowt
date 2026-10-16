package app

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"argowt/internal/resources"
	"argowt/internal/ui"
	"argowt/internal/workflows"
)

type WorkflowOperationService interface {
	Run(context.Context, workflows.Operation, workflows.OperationTarget) (workflows.OperationResult, error)
}

// actionKeys are the menu shortcuts, in menu order.
var actionKeys = map[workflows.Operation]string{
	workflows.OperationRetry:     "r",
	workflows.OperationResubmit:  "R",
	workflows.OperationSuspend:   "s",
	workflows.OperationResume:    "u",
	workflows.OperationStop:      "x",
	workflows.OperationTerminate: "X",
	workflows.OperationDelete:    "D",
}

type permissionMessage struct {
	namespace string
	allowed   map[workflows.Operation]bool
}

type operationResultMessage struct {
	target workflows.OperationTarget
	result workflows.OperationResult
	err    error
}

func checkPermissions(ctx context.Context, check func(context.Context, string) map[workflows.Operation]bool, namespace string) tea.Cmd {
	return func() tea.Msg {
		return permissionMessage{namespace: namespace, allowed: check(ctx, namespace)}
	}
}

func runOperation(ctx context.Context, service WorkflowOperationService, operation workflows.Operation, target workflows.OperationTarget) tea.Cmd {
	return func() tea.Msg {
		result, err := service.Run(ctx, operation, target)
		return operationResultMessage{target: target, result: result, err: err}
	}
}

// selectedOperationTarget is the workflow the actions apply to: the open
// execution, or the selected row of the workflow list.
func (model Model) selectedOperationTarget() (workflows.OperationTarget, bool) {
	if model.executionVisible {
		if model.execution == nil {
			return workflows.OperationTarget{}, false
		}
		return workflows.TargetFromExecution(*model.execution), true
	}
	if model.resourceDescriptor.Kind != resources.Workflows || model.detailVisible {
		return workflows.OperationTarget{}, false
	}
	items := model.filteredItems()
	if model.cursor >= len(items) {
		return workflows.OperationTarget{}, false
	}
	return workflows.TargetFromSummary(items[model.cursor]), true
}

// liveActionTarget is the latest known state of the action menu's workflow.
// It falls back to the state captured when the menu opened once the
// workflow leaves the live views, e.g. when it completes.
func (model Model) liveActionTarget() workflows.OperationTarget {
	target := model.actionTarget
	if model.executionVisible && model.execution != nil && model.execution.UID == target.UID {
		return workflows.TargetFromExecution(*model.execution)
	}
	for _, item := range model.items {
		if item.UID == target.UID {
			return workflows.TargetFromSummary(item)
		}
	}
	return target
}

func (model Model) openActions() (tea.Model, tea.Cmd) {
	if model.operationService == nil {
		return model, nil
	}
	target, found := model.selectedOperationTarget()
	if !found {
		return model, nil
	}
	model.actionVisible = true
	model.actionStage = "menu"
	model.actionTarget = target
	model.actionCursor = 0
	model.actionNotice = ""
	model.actionError = nil
	for index, operation := range workflows.Operations() {
		if available, _ := model.operationAvailable(operation); available {
			model.actionCursor = index
			break
		}
	}
	return model, model.ensurePermissions(target.Namespace)
}

// ensurePermissions checks RBAC once per namespace for the process lifetime.
func (model *Model) ensurePermissions(namespace string) tea.Cmd {
	if model.checkAccess == nil || model.permissionsPending[namespace] {
		return nil
	}
	if _, found := model.permissions[namespace]; found {
		return nil
	}
	if model.permissionsPending == nil {
		model.permissionsPending = make(map[string]bool)
	}
	model.permissionsPending[namespace] = true
	return checkPermissions(model.rootContext, model.checkAccess, namespace)
}

// mutationBlocked explains why the open view must not mutate, or is "".
func (model Model) mutationBlocked() string {
	if model.executionVisible {
		if model.executionStale || model.executionError != nil || model.execution == nil {
			return "workflow data is stale"
		}
		return ""
	}
	if !model.MutationAllowed() {
		return "workflow list is " + string(model.connectionState)
	}
	return ""
}

// operationAvailable reports whether the action menu may run an operation,
// with the reason when it may not.
func (model Model) operationAvailable(operation workflows.Operation) (bool, string) {
	if reason := model.mutationBlocked(); reason != "" {
		return false, reason
	}
	target := model.liveActionTarget()
	if allowed, known := model.permissions[target.Namespace][operation]; known && !allowed {
		return false, "not permitted by RBAC"
	}
	return target.Available(operation)
}

func (model Model) updateActionKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	operations := workflows.Operations()
	// Pasted or coalesced text must never choose or confirm an action.
	if key.Paste || (key.Type == tea.KeyRunes && len(key.Runes) > 1) {
		return model, nil
	}
	switch model.actionStage {
	case "running":
		return model, nil
	case "confirm":
		switch key.String() {
		case "y", "Y":
			model.actionStage = "running"
			return model, runOperation(model.rootContext, model.operationService, model.actionOperation, model.actionTarget)
		case "n", "N", "esc":
			model.actionStage = "menu"
		}
		return model, nil
	}

	switch key.String() {
	case "esc", "q":
		model.closeActions()
	case "j", "down":
		model.actionCursor = min(len(operations)-1, model.actionCursor+1)
	case "k", "up":
		model.actionCursor = max(0, model.actionCursor-1)
	case "enter":
		return model.chooseAction(operations[model.actionCursor])
	default:
		for index, operation := range operations {
			if key.String() == actionKeys[operation] {
				model.actionCursor = index
				return model.chooseAction(operation)
			}
		}
	}
	return model, nil
}

// chooseAction asks for confirmation, capturing the workflow state the user
// confirms against. Resubmit opens the submission form instead.
func (model Model) chooseAction(operation workflows.Operation) (tea.Model, tea.Cmd) {
	if available, _ := model.operationAvailable(operation); !available {
		return model, nil
	}
	target := model.liveActionTarget()
	if operation == workflows.OperationResubmit {
		model.closeActions()
		return model.openResubmission(target)
	}
	model.actionTarget = target
	model.actionOperation = operation
	model.actionStage = "confirm"
	return model, nil
}

func (model *Model) closeActions() {
	model.actionVisible = false
	model.actionStage = ""
	model.actionOperation = ""
}

func (model Model) handleOperationResult(message operationResultMessage) (tea.Model, tea.Cmd) {
	model.closeActions()
	target, result := message.target, message.result
	name := target.Namespace + "/" + target.Name
	if message.err != nil {
		model.actionError = message.err
		if apierrors.IsForbidden(message.err) {
			if model.permissions == nil {
				model.permissions = make(map[string]map[workflows.Operation]bool)
			}
			if model.permissions[target.Namespace] == nil {
				model.permissions[target.Namespace] = make(map[workflows.Operation]bool)
			}
			model.permissions[target.Namespace][result.Operation] = false
			model.actionError = fmt.Errorf("%s %s is forbidden: you lack the RBAC permission for it", result.Operation, name)
		}
		return model, nil
	}
	model.actionNotice = result.Operation.Past() + " workflow " + name
	if result.DeletedPods > 0 {
		model.actionNotice += fmt.Sprintf(" (deleted %s)", plural(result.DeletedPods, "pod"))
	}
	if result.Operation == workflows.OperationDelete && model.executionVisible && model.execution != nil && model.execution.UID == target.UID {
		model.closeExecution()
	}
	return model, nil
}

func (model Model) openResubmission(target workflows.OperationTarget) (tea.Model, tea.Cmd) {
	if model.submissionService == nil {
		return model, nil
	}
	source := workflows.SubmissionSource{Kind: workflows.SourceWorkflow, Namespace: target.Namespace, Name: target.Name, UID: target.UID}
	return model.startSubmission(source, target.Namespace)
}

func (model Model) actionView(width int) string {
	target := model.liveActionTarget()
	top := theme.Header(width, theme.Status(target.DisplayPhase(), target.DisplayPhase()),
		label("context", model.context), label("workflow", target.Namespace+"/"+target.Name))
	var lines []string
	var title, footer string
	switch model.actionStage {
	case "confirm", "running":
		operation := model.actionOperation
		confirmed := model.actionTarget
		title = theme.PanelTitle.Render("Confirm " + string(operation))
		lines = []string{
			field("context", ui.Sanitize(model.context)),
			field("namespace", ui.Sanitize(confirmed.Namespace)),
			field("workflow", ui.Sanitize(confirmed.Name)),
			field("uid", ui.Sanitize(confirmed.UID)),
			field("version", ui.Sanitize(confirmed.ResourceVersion)),
			field("phase", ui.Sanitize(confirmed.DisplayPhase())),
			field("action", theme.Warning.Render(operation.Title())),
			"",
		}
		description := theme.Muted.Render(operation.Description())
		if operation == workflows.OperationDelete {
			description = theme.Error.Render(operation.Description())
		}
		lines = append(lines, description)
		if model.actionStage == "running" {
			lines = append(lines, "", theme.Info.Render("◌ "+operation.Progressive()+" workflow…"))
			footer = theme.Keys(width, "", "waiting for the API server")
		} else {
			lines = append(lines, "", fmt.Sprintf("%s %s %s/%s in context %s?", operation.Title(), "workflow", ui.Sanitize(confirmed.Namespace), ui.Sanitize(confirmed.Name), ui.Sanitize(model.context)))
			footer = theme.Keys(width, "y", "confirm", "n/Esc", "cancel")
		}
	default:
		title = theme.PanelTitle.Render("Workflow actions")
		inner := panelInner(width)
		for index, operation := range workflows.Operations() {
			available, reason := model.operationAvailable(operation)
			cursor := " "
			if index == model.actionCursor {
				cursor = "›"
			}
			line := fmt.Sprintf("%s %-2s %-10s %s", cursor, actionKeys[operation], operation.Title(), operation.Description())
			if !available {
				line = fmt.Sprintf("%s %-2s %-10s unavailable: %s", cursor, actionKeys[operation], operation.Title(), reason)
			}
			switch {
			case index == model.actionCursor:
				line = theme.Row(ui.Pad(ui.Truncate(line, inner), inner), true)
			case !available:
				line = theme.Muted.Render(line)
			}
			lines = append(lines, line)
		}
		if model.permissionsPending[target.Namespace] {
			lines = append(lines, "", theme.Muted.Render("◌ checking RBAC permissions…"))
		}
		footer = theme.Keys(width, "j/k", "move", "Enter", "choose", "r/R/s/u/x/X/D", "shortcut", "Esc", "close")
	}
	return model.frame(top, footer, func(rows int) string {
		return theme.Panel(title, "", lines, width, rows, true)
	})
}

// actionStatusLines are the latest action outcome, for the list and
// execution views.
func (model Model) actionStatusLines() []string {
	var lines []string
	if model.actionNotice != "" {
		lines = append(lines, theme.Success.Render(" ✓ "+ui.Sanitize(model.actionNotice)))
	}
	if model.actionError != nil {
		message := ui.Sanitize(model.actionError.Error())
		if workflows.IsConflict(model.actionError) {
			message = "conflict: " + message
		}
		lines = append(lines, theme.Error.Render(" ! "+message))
	}
	return lines
}

// localManifestPrompt opens the submission flow at the manifest path field.
func (model Model) openLocalSubmission() (tea.Model, tea.Cmd) {
	if model.submissionService == nil {
		return model, nil
	}
	model.clearSubmission()
	model.submissionVisible = true
	model.submissionStage = "path"
	model.submissionForm = workflows.SubmissionForm{Source: workflows.SubmissionSource{Kind: workflows.SourceFile}}
	return model, nil
}

func (model Model) updateSubmissionPathKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	path := model.submissionForm.Source.Path
	switch key.String() {
	case "esc":
		model.clearSubmission()
	case "enter":
		if strings.TrimSpace(path) == "" {
			model.submissionError = fmt.Errorf("enter the path of a Workflow YAML file")
			return model, nil
		}
		return model.startSubmission(workflows.SubmissionSource{Kind: workflows.SourceFile, Path: strings.TrimSpace(path)}, model.currentNamespace)
	case "backspace":
		runes := []rune(path)
		if len(runes) > 0 {
			model.submissionForm.Source.Path = string(runes[:len(runes)-1])
		}
	default:
		if key.Type == tea.KeyRunes || key.Type == tea.KeySpace {
			model.submissionForm.Source.Path = path + string(key.Runes)
		}
	}
	return model, nil
}
