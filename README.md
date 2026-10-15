# argowt

`argowt` is a terminal user interface for monitoring and operating Argo Workflows.

> **Status:** early implementation. The repository foundation and CLI contract are in place; the interactive workflow browser is the next milestone.

## Specifications

- [Specification index](specs/README.md)
- [Product specification](specs/product-spec.md)
- [Technical specification](specs/technical-spec.md)
- [Test and cluster-safety specification](specs/test-spec.md)
- [Implementation plan](specs/implementation-plan.md)

## Development

Go 1.23 or newer is required.

```sh
make fmt            # format Go sources
make test           # cluster-free unit tests
make build          # build bin/argowt
make release-check  # format, test, vet, and cross-compile release targets
```

Ordinary tests do not load kubeconfig or contact Kubernetes. Disposable-cluster integration commands will be added with the isolated integration harness described in the test specification.

The bootstrap module path is intentionally `argowt` because a canonical public repository URL has not been selected. It must be replaced before the first public release.

## Current CLI

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

`argowt --help` and `argowt --version` are deliberately handled before any application or Kubernetes client setup.
