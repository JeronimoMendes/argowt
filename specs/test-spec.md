# Test and cluster-safety specification

## 1. Non-negotiable safety invariant

No automated test may access any Kubernetes cluster already configured on a developer machine.

This includes read-only operations. Tests must not inspect, list, authenticate to, or mutate existing contexts. In particular, they must never rely on the default `~/.kube/config`.

## 2. Test layers

### 2.1 Unit tests

Unit tests run without a cluster and without loading kubeconfig.

Use injected repositories, fake Argo/client-go clients, deterministic clocks, and synthetic Bubble Tea messages.

Required coverage includes:

- Active/terminal workflow classification.
- Workflow sorting and deterministic tie-breakers.
- Fuzzy and structured filter parsing/evaluation.
- Label-filter handling.
- Namespace all-access success and forbidden fallback behavior.
- Partial namespace failures.
- Controller instance-ID discovery/filtering.
- TUI loading, ready, partial-RBAC, stale, reconnecting, and error transitions.
- Ignoring messages from an obsolete context generation.
- Mutation disablement while stale.
- Stale-resource conflict behavior.
- Notification transition selection and UID identity.
- Configuration/state round trips without resource-body persistence.
- Terminal escape-sequence sanitization.

A unit test that unexpectedly reaches client-go's default config loading is a failure.

### 2.2 Integration tests

Integration tests use a disposable `kind` cluster created specifically for the current test run.

Install:

- Kubernetes through `kind`.
- Full Argo Workflows control plane and CRDs.
- Argo Events control plane and CRDs.
- EventBus support required by representative tests.
- A local artifact repository if needed by executable workflow fixtures.

Integration scenarios include:

- All-namespace list/watch.
- Namespace-scoped RBAC fallback.
- Workflow lifecycle events.
- Template and ClusterWorkflowTemplate submission.
- Local YAML submission.
- Suspend/resume and stop/terminate behavior.
- Retry/resubmit behavior.
- Delete with identity/conflict protection.
- Pod/container log follow and cancellation.
- Sensor/EventSource/EventBus diagnostics and relationships.
- Connection interruption/recovery where reliably simulatable.

### 2.3 End-to-end TUI tests

Run the compiled `argowt` binary against only the disposable cluster. Use a controllable terminal harness or Bubble Tea program driver to verify:

- Startup active-workflow screen.
- Filtering and resource switching.
- Tree/table/DAG navigation at supported terminal sizes.
- Confirmation dialogs.
- Submission forms.
- Log navigation.
- Marking a workflow for notifications and receiving a transition after navigating away.
- Graceful behavior under denied permissions.

Golden/snapshot tests may cover stable rendering regions, but behavioral assertions are preferred over full-screen snapshots.

## 3. Isolated kubeconfig

The cluster harness must:

1. Create a unique cluster name with an `argowt-test-` prefix.
2. Create a temporary directory owned by the test process.
3. Export the disposable cluster's kubeconfig to a file in that directory.
4. Set `KUBECONFIG` explicitly for every cluster-related subprocess.
5. Pass the kubeconfig explicitly to application test processes.
6. Never merge with or read the user's kubeconfig.
7. Delete the cluster and temporary files on completion where possible.

The harness must begin with a refusal guard. It may proceed only when all of the following hold:

- The kubeconfig path is inside the harness-created temporary directory.
- A harness marker file containing the generated run ID exists beside it.
- The expected context and cluster names match the generated `argowt-test-<run-id>` identity.
- The API endpoint matches the endpoint written by the same `kind` creation step.

A context-name match alone is not sufficient proof of isolation.

## 4. Environment sanitization

Before creating or using the disposable cluster, the harness should sanitize inherited variables that can redirect Kubernetes/Argo clients, including relevant `KUBECONFIG`, `ARGO_SERVER`, token, namespace, and context variables. It then sets only generated test values.

Logs and failure output must redact credentials and avoid dumping kubeconfig contents.

## 5. Cleanup and interrupted runs

Use cleanup traps for normal exit, test failure, and common termination signals. Because cleanup cannot be guaranteed after process or machine failure, provide an explicit command that only deletes clusters with the `argowt-test-` prefix after user confirmation.

Cleanup tooling must never delete an unrecognized cluster.

## 6. Local commands

The intended command shape is:

```sh
# No cluster access
go test ./...

# Creates and owns a disposable cluster for this invocation
make test-integration

# Same isolated environment, including terminal-level behavior
make test-e2e
```

`go test ./...` must remain cluster-free. Integration and E2E commands must not run implicitly as a side effect of ordinary unit tests.

## 7. CI matrix

CI should test:

- Supported Go versions chosen by the implementation.
- Argo Workflows 3.6 and 3.7 integration lanes.
- Linux AMD64 at minimum for cluster tests.
- Cross-compilation or build checks for macOS/Linux AMD64/ARM64.

Argo Events should be pinned to explicit compatible versions rather than installing an unbounded latest release.

## 8. External clusters

There is no default external-cluster test mode.

If one is added later, it requires a separate command, an explicit kubeconfig path, a dedicated namespace, and a deliberate opt-in flag. It must not weaken the disposable-cluster guards or be used by the standard test suite.

For the current specification, full-confidence testing means unit tests plus integration and E2E tests against the ephemeral `kind` environment—not the developer's configured clusters.
