# argowt

`argowt` is a terminal user interface for monitoring and operating Argo Workflows.

> **Status:** early implementation. Browsing, workflow visualization, notifications, and template submission are usable; logs and lifecycle operations remain under development.

## Current capabilities

- Native kubeconfig and client-go authentication, including exec plugins.
- Current-context, explicit-context, namespace, all-namespace, and controller-instance scopes.
- Live active-workflow list/watch with deterministic ordering.
- Adaptive all-namespace access with namespace fallback under restricted RBAC.
- Visible fresh, partial-authorization, stale, reconnecting, and fatal states.
- Keyboard and mouse navigation with fuzzy and structured filtering.
- Resource command palette for Workflows, WorkflowTemplates, ClusterWorkflowTemplates, CronWorkflows, EventSources, Sensors, and EventBuses.
- Dynamic Argo Events discovery: missing optional CRDs are hidden and forbidden resource scopes remain isolated.
- Read-only metadata, labels, annotations, owners, conditions, and YAML detail views with UID identity checks.
- Live workflow execution visualization with tree, DAG, and table modes plus node timing, messages, inputs, outputs, artifacts, retries, pod placement, and stable selection across views.
- Persistent UID-bound workflow notification watches, startup reconciliation, configurable transitions, in-TUI alerts, and graceful desktop-notification fallback.
- Workflow submission from WorkflowTemplates and ClusterWorkflowTemplates with parameter forms and explicit context/namespace/source confirmation.
- Bounded reconnect backoff and resource-version recovery.
- Cluster-free unit tests using narrow fake clients.

Template submission is implemented. Retry, resubmit, suspend, resume, stop, terminate, delete, and local-YAML submission are not implemented yet.

## Specifications

- [Specification index](specs/README.md)
- [Product specification](specs/product-spec.md)
- [Technical specification](specs/technical-spec.md)
- [Test and cluster-safety specification](specs/test-spec.md)
- [Implementation plan](specs/implementation-plan.md)

## Development

Go 1.24.2 or newer is required.

```sh
make fmt            # format Go sources
make test           # cluster-free unit tests
make build          # build bin/argowt
make install        # install to ~/.local/bin by default
make release-check  # format, test, vet, and cross-compile release targets
```

Ordinary tests do not load kubeconfig or contact Kubernetes. Disposable-cluster integration commands will be added with the isolated integration harness described in the test specification.

The bootstrap module path is intentionally `argowt` because a canonical public repository URL has not been selected. It must be replaced before the first public release.

## Running

The default uses the current kubeconfig context and its namespace:

```sh
argowt
argowt --namespace argo
argowt --all-namespaces
argowt --context development --instanceid controller-a
```

Inside the browser, use `j`/`k` or arrow keys to navigate, `:` to switch resource types, `/` to filter, `Enter` to open a workflow or resource, `n`/`N` to toggle/configure notifications, `s` to submit a selected template, `?` for help, and `q` to quit. Workflow execution views use `t`, `d`, and `l` for tree, DAG, and table modes.

```text
argowt [flags]

--kubeconfig PATH
--context NAME
--namespace NAME        repeatable or comma-separated
--all-namespaces
--instanceid ID
--config PATH
--no-mouse
--version
```

`argowt --help` and `argowt --version` are deliberately handled before kubeconfig or Kubernetes client setup.
