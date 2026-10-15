#!/usr/bin/env bash
# Local kind cluster with Argo Workflows for end-to-end TUI testing.
# Everything lives under .dev/, so the user's kubeconfig and argowt state stay untouched.
#
#   dev/cluster.sh up     create cluster and install Argo
#   dev/cluster.sh down   delete cluster
#   dev/cluster.sh run    start argowt against the cluster
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER_NAME="argowt-dev"
ARGO_VERSION="v3.7.3"
ARGO_MANIFEST="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/quick-start-minimal.yaml"

export KUBECONFIG="$ROOT/.dev/kubeconfig"
export XDG_STATE_HOME="$ROOT/.dev/state"
export XDG_CONFIG_HOME="$ROOT/.dev/config"

require() {
	for tool in "$@"; do
		command -v "$tool" >/dev/null || { echo "missing tool: $tool" >&2; exit 1; }
	done
}

# Idempotent: rerunning repairs a partial setup.
up() {
	require kind kubectl
	mkdir -p "$XDG_STATE_HOME" "$XDG_CONFIG_HOME"
	if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
		kind create cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG"
	fi
	kubectl create namespace argo --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -n argo --server-side -f "$ARGO_MANIFEST"

	# Drop MinIO: its quick-start image is no longer pullable, and without an
	# artifact repository every workflow ends in Error when archiving logs.
	kubectl -n argo delete deployment minio --ignore-not-found
	kubectl -n argo delete configmap artifact-repositories --ignore-not-found
	kubectl -n argo patch configmap workflow-controller-configmap --type merge \
		-p '{"data":{"artifactRepository":null}}'

	kubectl -n argo rollout restart deployment/workflow-controller
	kubectl -n argo rollout status deployment/workflow-controller --timeout=300s
}

down() {
	require kind
	kind delete cluster --name "$CLUSTER_NAME" --kubeconfig "$KUBECONFIG"
	rm -rf "$ROOT/.dev"
}

run() {
	exec "$ROOT/bin/argowt" --namespace argo
}

case "${1:-}" in
up | down | run) "$1" ;;
*)
	echo "usage: $0 up|down|run" >&2
	exit 1
	;;
esac
