# argowt

`argowt` is a terminal user interface for monitoring and operating Argo Workflows.

> **Status:** early implementation. Browsing, workflow visualization, live logs, notifications, and template submission are usable; lifecycle operations remain under development.

## Current capabilities

- Native kubeconfig and client-go authentication, including exec plugins.
- Current-context, explicit-context, namespace, all-namespace, and controller-instance scopes.
- Live active-workflow list/watch with deterministic ordering.
- Adaptive all-namespace access with namespace fallback under restricted RBAC.
- Visible fresh, partial-authorization, stale, reconnecting, and fatal states.
- Adaptive light/dark terminal colors, highlighted selections, and symbol-plus-text statuses that remain understandable without color.
- Keyboard and mouse navigation with fuzzy and structured filtering. Drag to select and copy text.
- Resource command palette for Workflows, WorkflowTemplates, ClusterWorkflowTemplates, CronWorkflows, EventSources, Sensors, and EventBuses.
- Dynamic Argo Events discovery: missing optional CRDs are hidden and forbidden resource scopes remain isolated.
- Read-only metadata, labels, annotations, owners, conditions, and YAML detail views with UID identity checks.
- Live workflow execution visualization with tree, DAG, and table modes plus node timing, messages, inputs, outputs, artifacts, retries, pod placement, and stable selection across views.
- Live node pod logs with container and previous-attempt selection, follow/reconnect, timestamps, search, wrapping, scrolling, bounded memory, and explicit file saving.
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

## Install

Install the latest release binary (macOS and Linux, amd64 and arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/JeronimoMendes/argowt/main/install.sh | sh
```

Set `BINDIR` (default `~/.local/bin`) or `ARGOWT_VERSION` (default latest, e.g. `v1.2.0`) to change the install directory or version.

`argowt update` replaces the binary with the latest release. The TUI shows a notice when a newer release exists.

Pushing a `v*` tag builds and publishes a release.

## Development

Go 1.24.2 or newer is required.

```sh
make fmt            # format Go sources
make test           # cluster-free unit tests
make build          # build bin/argowt
make install        # install to ~/.local/bin by default
make release-check  # format, test, vet, and cross-compile release targets
make dev-up         # local kind cluster with Argo and sample workflows (isolated under .dev/)
make dev-run        # run argowt against it
make dev-seed       # start the sample workflows again
make dev-submit T=etl-pipeline P="dataset=orders"  # start a workflow from a template
make dev-down       # delete it
```

The dev cluster comes with the templates, cluster templates, and cron workflows in `dev/samples/templates.yaml`, from 15-second checks to 30-minute jobs, plus a pipeline waiting on approval and jobs that fail or retry. A fresh cluster also starts the workflows in `dev/samples/workflows.yaml`.

Ordinary tests do not load kubeconfig or contact Kubernetes. Disposable-cluster integration commands will be added with the isolated integration harness described in the test specification.

The bootstrap module path is intentionally `argowt` because a canonical public repository URL has not been selected. It must be replaced before the first public release.

## Running

The default uses the current kubeconfig context and its namespace:

```sh
argowt
argowt --namespace argo
argowt --all-namespaces
argowt --context development --instanceid controller-a
argowt 'https://argo.example.com/workflows/argo/hello?nodeId=hello-123'
```

An Argo web UI URL opens its workflow (and node), template, cron workflow, event source, or sensor. The URL namespace becomes the scope unless `--namespace` or `--all-namespaces` is set. The kube context is not read from the URL; use `--context`.

Inside the browser, use `j`/`k` or arrow keys to navigate, `:` to switch resource types, `/` to filter, `Enter` to open a workflow or resource, `n`/`N` to toggle/configure notifications, `s` to submit a selected template, `?` for help, and `q` to quit. Workflow execution views use `t`, `d`, and `l` for tree, DAG, and table modes; press `Enter` or `o` on a node for live pod logs.

```text
argowt [flags] [URL]

--kubeconfig PATH
--context NAME
--namespace NAME        repeatable or comma-separated
--all-namespaces
--instanceid ID
--config PATH
--version
```

`argowt --help` and `argowt --version` are deliberately handled before kubeconfig or Kubernetes client setup.
