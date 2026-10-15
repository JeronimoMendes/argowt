package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const StateSchemaVersion = 1

// State is application-managed, non-sensitive local state. Kubernetes objects,
// logs, credentials, and submitted parameter values have no representation in
// this schema and therefore cannot be persisted by this package.
type State struct {
	SchemaVersion int             `json:"schemaVersion"`
	UI            UIState         `json:"ui,omitempty"`
	Watches       []WorkflowWatch `json:"workflowWatches,omitempty"`
}

type UIState struct {
	LastView string            `json:"lastView,omitempty"`
	Filters  map[string]string `json:"filters,omitempty"`
}

type WorkflowWatch struct {
	Context         string    `json:"context"`
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	UID             string    `json:"uid"`
	LastPhase       string    `json:"lastPhase,omitempty"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	ObservedAt      time.Time `json:"observedAt,omitempty"`
	Transitions     []string  `json:"transitions,omitempty"`
	Desktop         bool      `json:"desktop,omitempty"`
}

// NewState returns an empty state document using the current schema.
func NewState() State {
	return State{SchemaVersion: StateSchemaVersion}
}

// LoadState loads application state. A missing file is equivalent to empty
// state. Invalid JSON is left untouched so the caller can report and quarantine
// it explicitly rather than silently losing data.
func LoadState(path string) (State, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("open state: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	state := NewState()
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	if state.SchemaVersion != StateSchemaVersion {
		return State{}, fmt.Errorf("unsupported state schema version %d", state.SchemaVersion)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return State{}, err
	}
	return state, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing state data: %w", err)
	}
	return errors.New("state contains multiple JSON values")
}

// SaveState atomically replaces the state file with mode 0600. Temporary files
// are created beside the destination so rename remains atomic.
func SaveState(path string, state State) error {
	state.SchemaVersion = StateSchemaVersion
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, ".argowt-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	if err := directoryHandle.Sync(); err != nil {
		return err
	}
	return nil
}
