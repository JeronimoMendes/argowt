# Implementation plan

Implementation is divided into vertical milestones. Each milestone must keep `go test ./...` cluster-free and passing.

## Milestone 0: repository foundation

Deliver:

- Go module and `cmd/argowt` entry point.
- Formatting, test, build, and release-check commands.
- Configuration and state path foundations.
- Version output.
- Package boundaries that keep domain code independent from Bubble Tea.
- README linking to these specifications.

Acceptance:

- `argowt --help` and `argowt --version` do not load kubeconfig.
- `go test ./...` passes without Kubernetes access.
- macOS/Linux ARM64/AMD64 builds compile.

## Milestone 1: active-workflow browser

Deliver:

- Native kubeconfig/client-go construction.
- CLI context, kubeconfig, namespace, all-namespace, and instance-ID options.
- Workflow repository with list/watch and resource-version recovery.
- Adaptive all-namespace behavior with namespace fallback.
- Startup active-workflow list.
- Deterministic sorting.
- Context/namespace/instance indicators.
- Loading, partial-RBAC, stale, reconnecting, and fatal-error states.
- Keyboard navigation, optional mouse selection, and contextual help.
- Fuzzy filtering foundation.

Acceptance:

- Unit tests use only fake clients.
- Partial namespace failures do not hide healthy results.
- Old watch messages cannot affect a switched context generation.
- Mutations remain unavailable because they are not yet implemented.

## Milestone 2: resource browser and diagnostics

Deliver:

- WorkflowTemplates, ClusterWorkflowTemplates, and CronWorkflows views.
- EventSource, Sensor, and EventBus discovery and views.
- Structured filters and label selectors.
- Resource command palette.
- Metadata, conditions, YAML, related pods, and Kubernetes events.
- Verified versus inferred Argo Events relationships.
- In-app context, namespace-scope, and controller-instance switching.

Acceptance:

- Missing Argo Events CRDs hide those views without failing startup.
- Forbidden resource types show scoped errors without breaking other views.
- Context switching cancels previous watches and streams.

## Milestone 3: workflow execution views

Deliver:

- Workflow node normalization.
- Tree view as default.
- Node table view.
- Terminal DAG representation.
- Stable selection across view switches.
- Node details: timing, messages, retries, inputs, outputs, artifacts, pods, and manifests.
- Responsive pane behavior for narrow terminals.

Acceptance:

- Complex steps and DAG fixtures render without cycles or panics.
- Unknown node/phase values degrade visibly and safely.
- Status remains understandable without color.

## Milestone 4: logs

Deliver:

- Pod/container discovery.
- Container selection.
- Current/previous logs where supported.
- Follow, timestamps, search, wrapping, copy, and explicit save.
- Bounded in-memory buffering.
- Correct cancellation on navigation, context switch, and shutdown.

Acceptance:

- Streams cannot leak into a different node or context.
- Loss of connectivity preserves buffered output, marks it stale, and disables inappropriate actions.
- Logs are never persisted in application state.

## Milestone 5: workflow operations and submission

Deliver:

- Retry, resubmit, suspend, resume, stop, terminate, and delete.
- Standard confirmations containing context, namespace, workflow, and action.
- Safe stale-resource conflict handling.
- Submission from existing Workflow, WorkflowTemplate, ClusterWorkflowTemplate, and local Workflow YAML.
- Simple parameter forms without parameter-history persistence.
- RBAC-aware action availability.

Acceptance:

- No operation automatically replays after a stale conflict.
- Local YAML cannot apply unrelated resource kinds.
- Destructive operations are integration-tested in the disposable cluster only.

## Milestone 6: persistent workflow notifications

Deliver:

- Mark/unmark workflow notification watches.
- UID-bound persisted watch records.
- Per-watch transition configuration.
- In-TUI notifications.
- Optional desktop notifications with graceful no-op fallback.
- Startup reconciliation for changes that occurred while `argowt` was closed.

Acceptance:

- Navigation away from a watched workflow does not stop notifications.
- Deletion/recreation with the same name does not inherit the old UID watch.
- Notifications stop when `argowt` exits; no daemon is installed.

## Milestone 7: isolated integration environment

The harness may be introduced earlier as needed, but this milestone completes it.

Deliver:

- Disposable `kind` cluster lifecycle.
- Generated, guarded kubeconfig.
- Full Argo Workflows 3.6/3.7 test lanes.
- Pinned Argo Events installation.
- Artifact repository and representative fixtures where needed.
- Integration and terminal-level E2E suites.
- Cleanup and stale-test-cluster tooling.

Acceptance:

- The harness refuses any kubeconfig or context it did not generate for the current run.
- Standard tests never inspect existing clusters.
- Workflow operations, logs, Events diagnostics, RBAC fallback, and notifications pass end to end.

## Milestone 8: release hardening

Deliver:

- Performance checks for hundreds of workflows across roughly ten namespaces.
- Escape-sequence and malformed-resource robustness tests.
- Documentation and built-in help.
- Cross-platform desktop-notification fallback tests.
- Reproducible macOS/Linux ARM64/AMD64 binaries and checksums.

Acceptance:

- No known path persists workflow bodies, logs, credentials, or submission values.
- Disconnect/reconnect and authentication failures leave the UI recoverable.
- Release artifacts report version and commit metadata.

## Deferred roadmap

After v1, consider:

1. Argo Server backend.
2. Archived workflows and logs.
3. Homebrew distribution.
4. Richer schema-aware submission forms.
5. Resource editing if a separate safety design is approved.
6. Optional notification daemon if users require alerts after application exit.
