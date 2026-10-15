package kube

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClientBuilderUsesExplicitKubeconfigWithoutNetworkAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	contents := `apiVersion: v1
kind: Config
clusters:
- name: test-cluster
  cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
contexts:
- name: first
  context:
    cluster: test-cluster
    user: test-user
    namespace: first-ns
- name: selected
  context:
    cluster: test-cluster
    user: test-user
    namespace: selected-ns
current-context: first
users:
- name: test-user
  user: {}
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	connection, err := (ClientBuilder{}).Build(Options{Kubeconfig: path, Context: "selected"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if connection.Context != "selected" || connection.DefaultNamespace != "selected-ns" {
		t.Fatalf("unexpected connection scope: %+v", connection)
	}
	if connection.RESTConfig.Host != "https://127.0.0.1:1" {
		t.Fatalf("host = %q", connection.RESTConfig.Host)
	}
}

func TestClientBuilderRejectsUnknownContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	contents := `apiVersion: v1
kind: Config
clusters: []
contexts: []
current-context: ""
users: []
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (ClientBuilder{}).Build(Options{Kubeconfig: path, Context: "missing"}); err == nil {
		t.Fatal("Build() expected an error")
	}
}
