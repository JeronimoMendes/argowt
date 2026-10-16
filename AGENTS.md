# Agent instructions

## Test every change on the dev cluster

Unit tests are not enough. Before you report a change as done, run it against the local kind cluster and see it work in the TUI.

Never use the user's own kube contexts. They can be production.

```sh
make dev-up    # kind cluster "argowt-dev" with Argo Workflows and samples; safe to rerun
make dev-run   # build and run argowt against it
make dev-seed  # start the sample workflows again
make dev-submit T=quick-hello P="message=hi"  # start a workflow from any template
make dev-down  # delete the cluster and .dev/
```

A fresh cluster starts with running workflows at different steps, and the templates and cron workflows in `dev/samples/templates.yaml`. Pick a template by how long it runs: `quick-hello` and `quick-checks` take about 15 s, `etl-pipeline` and `build-test-deploy` take minutes, `release-approval` waits on a suspend step, and `always-fails` and `flaky-job` fail.

`dev/cluster.sh` keeps everything in `.dev/` (git-ignored): the kubeconfig, and argowt's state and config. Use the same isolation for direct `kubectl` calls:

```sh
export KUBECONFIG=$PWD/.dev/kubeconfig
kubectl -n argo create -f dev/notify-test.yaml   # 40 s workflow that succeeds
```

## Drive the TUI with tmux

```sh
tmux new-session -d -s argowt -x 200 -y 40 "make -s dev-run"
tmux send-keys -t argowt n          # send keys
tmux capture-pane -t argowt -p      # read the screen
tmux kill-session -t argowt
```

Wait for cluster state with `kubectl wait`, not fixed sleeps, e.g.:

```sh
kubectl -n argo wait wf/<name> --for=jsonpath='{.status.phase}'=Succeeded --timeout=120s
```

Report what you saw on screen. If you could not test a change on the cluster, say so.
