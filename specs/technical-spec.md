# Technical specification

## 1. Technology choices

- **Language:** Go
- **TUI framework:** Bubble Tea
- **Rendering/components:** Bubbles and Lip Gloss where useful, without coupling domain logic to view components
- **Kubernetes integration:** `client-go` and Kubernetes API machinery
- **Argo Workflows integration:** typed Argo Workflows Go API/client packages compatible with v3.6 and v3.7
- **Argo Events integration:** typed clients where version compatibility is stable; discovery/unstructured adapters are acceptable at compatibility boundaries
- **Backend in v1:** direct Kubernetes API only

The production application must not execute `argo` or `kubectl` subprocesses.

## 2. Proposed package boundaries

The exact names may evolve, but the architecture should preserve these responsibilities:

```text
cmd/argowt/             CLI parsing, process lifecycle, dependency wiring
internal/app/           top-level Bubble Tea model and navigation
internal/config/        config/state schema, XDG paths, migration
internal/kube/          kubeconfig loading, contexts, discovery, RBAC helpers
internal/workflows/     Workflow repository, watches, operations, submissions
internal/events/        EventSource/Sensor/EventBus repositories and relations
internal/logs/          pod/container log streaming
internal/filter/        fuzzy and structured filtering
internal/notify/        persisted watches and TUI/desktop delivery
internal/ui/            reusable views, styles, keymaps, modal infrastructure
internal/testutil/      fake clients, fixtures, deterministic clocks/messages
```

Domain and repository code must not import Bubble Tea. UI models consume narrow interfaces so they can be tested without a cluster.

## 3. CLI contract

The initial CLI should support:

```text
argowt [flags]

--kubeconfig PATH
--context NAME
--namespace NAME        repeatable, or documented comma-separated equivalent
--all-namespaces
--instanceid ID
--config PATH
--version
```

Behavior:

- `--kubeconfig` takes precedence over `KUBECONFIG` and default loading rules.
- `--context` overrides the kubeconfig current context without rewriting kubeconfig.
- Namespace flags establish the initial scope.
- `--instanceid` takes precedence over `ARGO_INSTANCEID`.
- Flag parsing must not create a Kubernetes client merely to print help or version.

Additional flags may be added, but familiar Argo/Kubernetes flag meanings should not be redefined.

## 4. Configuration and state

Use conventional XDG locations with platform-appropriate fallbacks:

- Config: `$XDG_CONFIG_HOME/argowt/config.yaml`
- UI/watch state: `$XDG_STATE_HOME/argowt/state.json`

Configuration and state must be separate because configuration is user-authored while state is application-managed.

Expected configuration domains:

- Namespace allowlists per context.
- Keybindings.
- Desktop-notification enablement and transition defaults.
- Display preferences.
- Optional context labels/policies.

State may include:

- Last view and non-sensitive UI layout state.
- Filters and sorting preferences.
- Notification-watch records keyed by context/namespace/UID.

Writes must be atomic: write a temporary file in the destination directory, fsync where practical, and rename. Corrupt state should be quarantined or ignored with a visible warning rather than preventing startup.

## 5. Kubernetes client construction

### 5.1 Loading

Use standard client-go loading rules with explicit overrides. Honor exec credential plugins and provider-neutral kubeconfig behavior.

Client construction must be injectable. Tests must be able to supply fake repositories or fake Kubernetes/Argo interfaces without loading any kubeconfig file.

### 5.2 Context switching

A context switch creates a new client/repository generation. The application must:

1. Cancel watches and log streams from the previous generation.
2. Clear resource bodies held in memory.
3. Construct and validate the new client.
4. Establish fresh list/watch streams.
5. Only then enable mutations.

Messages from an old generation must carry a generation identifier and be ignored after a switch.

### 5.3 Namespace strategy

For an all-namespaces request:

1. Attempt an all-namespace list using the Workflow API.
2. If successful, establish an all-namespace watch.
3. On a forbidden response, resolve the fallback namespace set from explicit configuration and discoverable namespaces.
4. Probe access conservatively and cache results for the process lifetime.
5. Establish one list/watch stream per permitted namespace.

Do not retry forbidden scopes on every refresh. Provide a user action to re-check permissions.

A namespace fallback must tolerate partial list or watch failure without discarding healthy namespaces.

## 6. Repository and watch model

Repositories expose snapshots plus typed events rather than leaking raw informer mechanics into UI code.

A workflow event contains enough identity to reject stale delivery:

- Client/context generation.
- Namespace.
- Name.
- UID.
- Resource version.
- Event type.
- Minimal normalized summary or the typed resource as appropriate.

Initial list and watch must follow Kubernetes consistency rules. Handle expired resource versions by relisting. Coalesce rapid events before sending expensive redraws while preserving the latest state.

At the target scale, keep indexed in-memory summaries for list rendering. Load or retain full objects only when necessary for details and operations. Do not write resource objects to disk.

## 7. Workflow classification and ordering

The startup active-workflow query includes nonterminal phases. Phase handling must tolerate unknown values from newer versions.

Default ordering:

1. Workflows needing attention (Error/Failed only when terminal workflows are included by the active filter rules).
2. Suspended/Pending/Running as product behavior defines.
3. Newest creation timestamp first.
4. Namespace/name as deterministic tie-breakers.

The exact attention ordering should be covered by tests and shown in help. Unknown or missing timestamps must not panic.

## 8. Filtering

The filtering engine has two layers:

- Fuzzy plain-text matching over normalized summary fields.
- Parsed structured predicates and Kubernetes label selectors.

Parsing errors should leave the previous valid filter active and display an inline error. Filtering must be deterministic and independently unit-testable.

Server-side label selectors may reduce API traffic. Client-side filtering remains necessary for fields Kubernetes cannot select.

## 9. Bubble Tea architecture

The top-level model owns:

- Active client generation and connection status.
- Current context/namespace/instance scope.
- Navigation stack and modal state.
- Global command palette.
- Notification manager.

Feature screens own local selection, scrolling, filtering, and display state.

All asynchronous work returns typed Bubble Tea messages. Commands must accept contexts/cancellation or generation tokens so stale results cannot mutate a new screen or context.

Minimum global states:

- Starting/loading.
- Ready/fresh.
- Ready but partially authorized.
- Reconnecting/stale.
- Authentication failed.
- Fatal configuration error.

Terminal resizing must not panic. Narrow terminals should collapse secondary panes and preserve core list/action access.

## 10. Workflow operations

Operations are implemented through native Argo/Kubernetes clients behind a `WorkflowService` interface. Each operation must return a typed result suitable for user-facing errors.

Before a destructive mutation:

- Capture context, namespace, name, UID, and observed resource version.
- Present them in the confirmation.
- Re-read or use preconditions where applicable.
- Reject UID/resource-version conflicts.

Do not automatically retry a stale mutation. Network retries must be limited to requests known to be safe and idempotent.

Submission adapters cover:

- Existing Workflow resubmission.
- WorkflowTemplate references.
- ClusterWorkflowTemplate references.
- Local YAML decoding.

Local YAML loading must reject unsupported document kinds and multiple ambiguous documents. It must not silently apply non-Workflow resources.

## 11. Logs

The log service uses the Kubernetes pod log API directly. It must:

- Discover containers from the selected pod.
- Distinguish current and previous container attempts where available.
- Support follow and timestamps.
- Cancel streams on view exit, context switch, workflow/node change, or application shutdown.
- Bound in-memory log storage with a documented line/byte policy.
- Preserve a user's scroll position when new data arrives unless follow mode is active.

Saving logs writes only to an explicit user-selected path. Logs are never part of application state persistence.

## 12. Argo Events relations

Relations should use stable metadata first:

- Owner references.
- Labels and annotations defined by Argo Events/Workflows.
- Trigger/template metadata.

Heuristic name matching may be used only as a labeled best-effort fallback. The UI must distinguish verified and inferred relationships.

## 13. Notifications

Persisted workflow watches contain:

- Kubeconfig context identity.
- Namespace.
- Workflow name for display.
- Workflow UID for identity.
- Last observed phase/resource version/timestamp.
- Selected transitions.
- Desktop-delivery preference.

On startup, resolve each watch only in its recorded context and namespace. A name match with a different UID is treated as deletion/recreation, not the same workflow.

Desktop delivery is behind an interface with OS-specific implementations and a no-op fallback. Notification failure must not affect Kubernetes watches or the TUI.

## 14. Error handling and reconnects

Classify errors into at least:

- Forbidden/unauthorized.
- Authentication plugin failure.
- Not found/deleted.
- Conflict/stale resource.
- Timeout/temporary network failure.
- API discovery/version mismatch.
- Invalid local manifest/configuration.

On temporary connection loss:

- Retain the latest in-memory snapshot.
- Mark it stale.
- Disable mutations.
- Reconnect with capped exponential backoff and jitter.
- Reset backoff after a stable successful connection.

Do not hide partial failures. For example, successful Workflow watches should remain useful if one Argo Events CRD is forbidden.

## 15. Security and privacy

- Use kubeconfig and client-go authentication; never store credentials.
- Do not automatically resolve Kubernetes Secret references.
- Display values present in ordinary API responses as-is, per the product decision.
- Do not persist resource bodies, parameter values, or logs.
- Make context and namespace visible in every mutation confirmation.
- Avoid printing bearer tokens, exec-plugin output, or full kubeconfig contents in errors or diagnostics.
- Treat terminal escape sequences in resource names, events, and logs as untrusted; sanitize output before rendering.

## 16. Distribution

Build reproducible release binaries for macOS/Linux on ARM64/AMD64. Release automation should:

- Run unit and integration checks.
- Build with version/commit metadata.
- Produce checksums.
- Avoid embedding local paths or kubeconfig data.

The module path is not yet a product decision. Use the eventual canonical repository URL before the first public release; changing it later is possible but undesirable.
