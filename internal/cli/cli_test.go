package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"argowt/internal/app"
)

func TestHelpDoesNotStartApplicationOrResolvePaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-is-invalid")
	called := false
	var stdout bytes.Buffer

	err := Run(context.Background(), []string{"--help"}, &stdout, &bytes.Buffer{}, func(context.Context, app.Options) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Fatal("application runner was called for --help")
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("help output missing usage: %q", stdout.String())
	}
}

func TestVersionDoesNotStartApplicationOrResolvePaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-is-invalid")
	called := false
	var stdout bytes.Buffer

	err := Run(context.Background(), []string{"--version"}, &stdout, &bytes.Buffer{}, func(context.Context, app.Options) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if called {
		t.Fatal("application runner was called for --version")
	}
	if !strings.HasPrefix(stdout.String(), "argowt ") {
		t.Fatalf("unexpected version output: %q", stdout.String())
	}
}

func TestOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ARGO_INSTANCEID", "from-environment")

	var got app.Options
	err := Run(context.Background(), []string{
		"--kubeconfig", "/tmp/kubeconfig",
		"--context", "development",
		"--namespace", "one,two",
		"--namespace", "two",
		"--all-namespaces",
		"--instanceid", "from-flag",
		"--config", "/tmp/argowt.yaml",
		"--no-mouse",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(_ context.Context, options app.Options) error {
		got = options
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.Kubeconfig != "/tmp/kubeconfig" || got.Context != "development" {
		t.Fatalf("unexpected connection options: %+v", got)
	}
	if strings.Join(got.Namespaces, ",") != "one,two" {
		t.Fatalf("namespaces = %v, want [one two]", got.Namespaces)
	}
	if !got.AllNamespaces || got.InstanceID != "from-flag" || got.MouseEnabled {
		t.Fatalf("unexpected scope/input options: %+v", got)
	}
	if got.Paths.ConfigFile != "/tmp/argowt.yaml" {
		t.Fatalf("config path = %q", got.Paths.ConfigFile)
	}
}

func TestInstanceIDFallsBackToEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ARGO_INSTANCEID", "controller-a")

	var got app.Options
	err := Run(context.Background(), nil, &bytes.Buffer{}, &bytes.Buffer{}, func(_ context.Context, options app.Options) error {
		got = options
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.InstanceID != "controller-a" {
		t.Fatalf("instance ID = %q, want controller-a", got.InstanceID)
	}
	if !got.MouseEnabled {
		t.Fatal("mouse should be enabled by default")
	}
}
