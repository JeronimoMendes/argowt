// Package app owns the application lifecycle and, eventually, the top-level
// Bubble Tea model. It deliberately accepts plain values so UI wiring remains
// separate from Kubernetes and domain packages.
package app

import (
	"context"
	"errors"

	"argowt/internal/config"
)

// ErrNotImplemented is returned until the interactive browser is introduced in
// milestone 1.
var ErrNotImplemented = errors.New("the interactive workflow browser is not implemented yet")

// Options contains process-level settings established by the CLI. It contains
// no Kubernetes clients, credentials, or persisted resource data.
type Options struct {
	Kubeconfig    string
	Context       string
	Namespaces    []string
	AllNamespaces bool
	InstanceID    string
	Paths         config.Paths
	MouseEnabled  bool
}

// Run starts the application. Kubernetes client construction will be injected
// here in milestone 1; keeping the current implementation inert ensures the
// repository foundation cannot accidentally access a cluster.
func Run(_ context.Context, _ Options) error {
	return ErrNotImplemented
}
