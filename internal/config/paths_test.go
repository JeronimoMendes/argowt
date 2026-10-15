package config

import (
	"path/filepath"
	"testing"
)

func TestResolvePathsUsesXDGDirectories(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	paths, err := ResolvePaths("")
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if want := filepath.Join(configHome, "argowt", "config.yaml"); paths.ConfigFile != want {
		t.Fatalf("ConfigFile = %q, want %q", paths.ConfigFile, want)
	}
	if want := filepath.Join(stateHome, "argowt", "state.json"); paths.StateFile != want {
		t.Fatalf("StateFile = %q, want %q", paths.StateFile, want)
	}
}

func TestResolvePathsHonorsExplicitConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-invalid-path")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	explicit := filepath.Join(t.TempDir(), "custom.yaml")

	paths, err := ResolvePaths(explicit)
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if paths.ConfigFile != explicit {
		t.Fatalf("ConfigFile = %q, want %q", paths.ConfigFile, explicit)
	}
}

func TestResolvePathsRejectsRelativeXDGPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if _, err := ResolvePaths(""); err == nil {
		t.Fatal("ResolvePaths() expected an error")
	}
}
