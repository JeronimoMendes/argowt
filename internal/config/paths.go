// Package config defines local configuration and state locations. It never
// loads kubeconfig or Kubernetes credentials.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	appDirectory   = "argowt"
	configFileName = "config.yaml"
	stateFileName  = "state.json"
)

// Paths keeps user-authored configuration separate from application-managed
// state.
type Paths struct {
	ConfigFile string
	StateFile  string
}

// ResolvePaths applies an explicit configuration override and otherwise uses
// XDG directories with platform-appropriate fallbacks.
func ResolvePaths(explicitConfig string) (Paths, error) {
	configFile := explicitConfig
	if configFile == "" {
		configHome, err := userConfigHome()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve configuration directory: %w", err)
		}
		configFile = filepath.Join(configHome, appDirectory, configFileName)
	}

	stateHome, err := userStateHome()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve state directory: %w", err)
	}

	return Paths{
		ConfigFile: filepath.Clean(configFile),
		StateFile:  filepath.Join(stateHome, appDirectory, stateFileName),
	}, nil
}

func userConfigHome() (string, error) {
	if path := os.Getenv("XDG_CONFIG_HOME"); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("XDG_CONFIG_HOME must be an absolute path")
		}
		return path, nil
	}
	return os.UserConfigDir()
}

func userStateHome() (string, error) {
	if path := os.Getenv("XDG_STATE_HOME"); path != "" {
		if !filepath.IsAbs(path) {
			return "", errors.New("XDG_STATE_HOME must be an absolute path")
		}
		return path, nil
	}

	if runtime.GOOS == "darwin" {
		return os.UserConfigDir()
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}
