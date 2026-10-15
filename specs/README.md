# argowt specifications

These files capture the decisions made for the initial `argowt` release.

| File | Purpose |
|---|---|
| [product-spec.md](product-spec.md) | Product scope, user experience, supported resources, actions, and explicit exclusions |
| [technical-spec.md](technical-spec.md) | Architecture, Kubernetes integration, state, errors, security, and distribution |
| [test-spec.md](test-spec.md) | Unit/integration/E2E strategy and strict isolation from existing Kubernetes clusters |
| [implementation-plan.md](implementation-plan.md) | Ordered milestones and acceptance gates |

## Status

This is the agreed v1 specification. Implementation details may be refined without expanding product scope. Any change to a product decision or safety invariant should update these files first.

## Product summary

`argowt` is a Go/Bubble Tea TUI for monitoring and operating live Argo Workflows through the Kubernetes API. It provides a `k9s`-style resource browser, workflow execution views, live logs, standard workflow operations, Argo Events diagnostics, cross-namespace navigation, and persistent per-workflow notifications.

Argo Server, archived workflows, resource authoring, and a background notification daemon are deliberately deferred.
