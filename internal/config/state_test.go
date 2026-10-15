package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	want := State{
		UI: UIState{
			LastView: "workflows",
			Filters:  map[string]string{"workflows": "status=Running"},
		},
		Watches: []WorkflowWatch{{
			Context:         "development",
			Namespace:       "argo",
			Name:            "example",
			UID:             "uid-1",
			LastPhase:       "Running",
			ResourceVersion: "42",
			ObservedAt:      time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Transitions:     []string{"Succeeded", "Failed"},
		}},
	}

	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	want.SchemaVersion = StateSchemaVersion
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadState() = %#v, want %#v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("state mode = %o, want 600", gotMode)
	}
}

func TestLoadMissingStateReturnsCurrentEmptyState(t *testing.T) {
	got, err := LoadState(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.SchemaVersion != StateSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", got.SchemaVersion, StateSchemaVersion)
	}
}

func TestLoadStateRejectsUnknownData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"resourceBodies":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Fatal("LoadState() expected an error for an unknown field")
	}
}
