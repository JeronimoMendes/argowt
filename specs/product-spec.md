# Product specification

## 1. Product identity

- **Product and executable name:** `argowt`
- **Category:** terminal user interface for Argo Workflows
- **Primary jobs:**
  1. Monitor workflow execution and related Argo resources.
  2. Perform standard workflow operations safely.
- **Primary audience:** operators and engineers who already have Kubernetes access and want an interactive alternative to repeatedly invoking `argo` and `kubectl`.

## 2. Goals

`argowt` v1 must let a user:

- See active workflows immediately after startup.
- Browse relevant Argo resources across accessible namespaces.
- Inspect a workflow as a node tree, dependency graph, or table.
- Diagnose nodes using status, events, inputs, outputs, manifests, pods, and live logs.
- Submit and control workflows without leaving the terminal.
- Continue monitoring selected workflows after navigating away from them.
- Work under partial Kubernetes RBAC without repeated noisy failures.
- Switch Kubernetes contexts and Argo controller instance IDs in the application.

## 3. Non-goals for v1

The following are explicitly deferred:

- Argo Server as a connection backend.
- Archived workflows and archived logs.
- Direct archive-database access.
- Creating or editing WorkflowTemplates, ClusterWorkflowTemplates, CronWorkflows, Sensors, EventSources, or EventBuses.
- A generic YAML editor/apply facility.
- Full management of internal controller CRDs.
- Notifications while `argowt` is not running.
- A background daemon or OS service.
- Aggregating multiple Kubernetes clusters simultaneously.

## 4. Connection and compatibility

### 4.1 Backend

v1 connects directly to the Kubernetes API using native Go clients and kubeconfig credentials. It does not shell out to `argo` or `kubectl`.

The default behavior mirrors the normal Argo CLI setup:

- Use the current kubeconfig context unless overridden.
- Use the kubeconfig's current namespace unless another namespace scope is selected.
- Allow an explicit kubeconfig and context.
- Honor kubeconfig exec credential plugins.
- Never manage or store provider credentials itself.

### 4.2 Compatibility

- Argo Workflows `v3.6` and `v3.7` are supported.
- Argo Events resources are discovered dynamically and hidden when their CRDs are absent.
- v1 is designed for medium environments: hundreds of workflows across approximately ten namespaces.

## 5. User-facing resources

### 5.1 Main resource browser

The main resource browser exposes:

- Workflows
- WorkflowTemplates
- ClusterWorkflowTemplates
- CronWorkflows
- EventSources
- Sensors
- EventBuses

Internal resources such as WorkflowTaskSets and WorkflowArtifactGCTasks do not appear as primary resource types. They may be shown in diagnostics when relevant.

### 5.2 Argo Events boundary

EventSources, Sensors, and EventBuses are operational-diagnostics resources in v1. Users can:

- List and filter them.
- Inspect status, conditions, metadata, and YAML.
- Inspect related pods and Kubernetes events.
- Open relevant controller logs when permitted.
- Follow relationships from a Sensor-triggered workflow back to its source where metadata permits.

Users cannot create, edit, enable, disable, restart, or delete these resources in v1.

## 6. Navigation and startup

### 6.1 Navigation model

The interface uses a `k9s`-style resource-browser model:

- Switch resource types without leaving the main application.
- Filter and sort resource lists.
- Drill from a resource into details and related resources.
- Invoke actions using shortcuts or the command palette.
- Return predictably with Escape/back navigation.

### 6.2 Startup screen

The first screen shows active workflows across the effective namespace scope. Active workflows are nonterminal workflows, including pending, running, and suspended workflows. Press `c` to switch to completed workflows that are still in the cluster, newest finished first, and again to return. Completed workflows are listed only while that view is open, because listing them costs far more than listing active ones. Workflows already garbage-collected into the Argo archive are not shown; that needs the Argo Server.

### 6.3 Input model

The UI is keyboard-first with optional mouse support. Every operation must remain keyboard-accessible.

Default bindings combine:

- Conventional controls: arrows, Tab, Enter, Escape.
- Vim-style navigation: `j`, `k`, `g`, `G`.
- `/` for filtering.
- `:` for commands.
- A searchable command palette.

Bindings should be configurable. The UI must show contextual shortcut hints rather than requiring memorization.

Mouse support includes row selection, tabs, scrolling, and pane focus. It must not be required for any action.

## 7. Namespace, context, and instance scope

### 7.1 Namespace behavior

The user can view all accessible namespaces and switch scope quickly.

The application uses an adaptive strategy:

1. Attempt cluster-wide Workflow list/watch access.
2. If forbidden, use configured namespaces or namespaces discoverable to the user.
3. Probe and cache namespace permissions for the current process where necessary.
4. Do not repeatedly retry known-forbidden operations.
5. Make inaccessible or partially accessible scopes understandable in the UI.

The application must not claim that Kubernetes can always enumerate every namespace in which a user has Workflow permissions.

### 7.2 Context switching

The user can switch kubeconfig contexts without restarting. The active context and namespace scope remain visible at all times.

v1 does not aggregate resources from multiple contexts at once.

### 7.3 Controller instance IDs

The application:

- Honors `--instanceid` and `ARGO_INSTANCEID` as initial filters.
- Discovers visible controller instance IDs from resource metadata.
- Supports switching between a specific instance and an all-visible-instances view.
- Keeps the active instance filter visible.

## 8. Workflow views

Selecting a workflow opens a hybrid execution view with three modes:

1. **Tree:** compact hierarchy of steps, DAG tasks, pods, hooks, and children.
2. **DAG:** dependency-oriented representation with status-marked nodes and edges.
3. **Table:** sortable/filterable nodes with phase, duration, retries, pod, and template information.

The tree is the default. Selection should remain stable when switching modes when the corresponding node still exists.

Selecting a node exposes, subject to RBAC and availability:

- Phase, message, timestamps, duration, and progress.
- Template and boundary relationships.
- Inputs and outputs.
- Artifact references.
- Retry information.
- Related pod and container information.
- Kubernetes events.
- Node or pod manifest.
- Live logs.

Status must never be communicated by color alone; symbols and text are required.

## 9. Logs

v1 provides operational pod logs for workflow nodes:

- Select among init, main, wait, sidecar, and other containers.
- Follow live output.
- Toggle timestamps.
- Search within available output.
- Toggle line wrapping.
- Scroll without losing the ability to resume following.
- Copy selected content or save logs to a user-selected file.
- Clearly distinguish reconnecting, ended, unavailable, and forbidden states.

Aggregated archived logs and artifact-repository log retrieval are deferred with Argo Server support.

## 10. Search and filtering

The resource browser supports both:

- Fuzzy text search across useful visible fields such as name, namespace, template, phase, and instance ID.
- Structured expressions such as `status=Failed`, `namespace!=argo`, and time-oriented predicates.

Kubernetes label selectors are supported where the API permits server-side filtering. Active filters remain visible and individually removable.

## 11. Workflow operations

### 11.1 Supported operations

v1 supports:

- Submit a workflow.
- Retry a workflow.
- Resubmit a workflow.
- Suspend a workflow.
- Resume a workflow.
- Stop a workflow.
- Terminate a workflow.
- Delete a workflow.

It does not edit arbitrary workflow manifests after submission.

### 11.2 Submission sources

Users can submit from:

- An existing workflow.
- A WorkflowTemplate.
- A ClusterWorkflowTemplate.
- A local YAML workflow manifest.

Template submission uses a simple form with one text field per declared parameter. Defaults and descriptions may be shown, but v1 does not require a schema-driven form builder or raw YAML editor.

Before submission, the UI identifies at least the destination context, namespace, source/template, and entered parameters.

### 11.3 Confirmation and conflicts

Destructive actions use a standard yes/no confirmation that clearly displays the context, namespace, resource name, and operation.

Stale-resource policy:

- Prefer UID/resource-version preconditions where the Kubernetes API supports them.
- If the selected resource changed before an action, fail safely.
- Refresh the resource and explain the conflict.
- Do not automatically replay the operation against the updated resource.

Kubernetes RBAC remains authoritative. Forbidden actions are disabled when known and reported clearly when discovered at execution time.

## 12. Notifications

### 12.1 Workflow watches

A workflow can be marked for notifications independently of the currently open view. Navigating elsewhere does not stop its watch.

Each watch:

- Binds to Kubernetes context, namespace, workflow UID, and observed name.
- Cannot silently transfer to a deleted-and-recreated workflow with the same name.
- Persists across `argowt` restarts.
- Is active only while `argowt` is running.
- Reports the latest known state when the application starts if the workflow changed while it was closed.

### 12.2 Notification selection

Notification transitions are configurable per workflow. The default should emphasize terminal states, while users may opt into transitions including Running, Suspended, Succeeded, Failed, Error, and Terminated.

### 12.3 Delivery

- Important notifications appear inside the TUI.
- Desktop notifications are optional, disabled by default, and configurable by transition.
- Desktop delivery must fail gracefully when the OS notification mechanism is unavailable.

## 13. Disconnection behavior

When Kubernetes connectivity or authentication is lost:

- Keep the last in-memory view.
- Mark data visibly stale.
- Disable all write operations.
- Reconnect automatically with bounded exponential backoff and jitter.
- Re-enable mutations only after a fresh successful read.
- Do not persist stale resource bodies to disk.

Kubeconfig credential plugins handle token renewal. If renewal fails, show the provider error and offer retry; do not implement provider-specific login flows.

## 14. Local persistence

Persist:

- User configuration.
- Context policies and namespace allowlists.
- Keybindings and display preferences.
- UI state such as filters, column widths, and last-opened views.
- Workflow notification-watch metadata and preferences.

Do not persist:

- Workflow or CRD bodies as an offline cache.
- Logs.
- Submitted parameter values or form history.
- Authentication tokens or credentials.
- Kubernetes Secret values.

API-visible parameter and resource values are displayed as returned, without heuristic redaction. `argowt` must not fetch referenced Kubernetes Secret values merely to display them.

## 15. Distribution

v1 is distributed as prebuilt binaries for:

- macOS ARM64 and AMD64.
- Linux ARM64 and AMD64.

Homebrew, `go install` as the primary installation path, containers, and other package managers are not required for v1.
