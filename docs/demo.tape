# Records docs/demo.gif with https://github.com/charmbracelet/vhs.
#
#   make dev-up && make build
#   vhs docs/demo.tape
#
# Run it a few minutes after `make dev-up` (or `make dev-seed`), so the sample
# workflows are spread across their steps.

Output docs/demo.gif

Set Shell bash
Set FontFamily "JetBrains Mono"
Set FontSize 16
Set Width 1400
Set Height 820
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "export KUBECONFIG=$PWD/.dev/kubeconfig XDG_STATE_HOME=$PWD/.dev/state XDG_CONFIG_HOME=$PWD/.dev/config PATH=$PWD/bin:$PATH PS1='$ ' && clear"
Enter
Show

# Live list of active workflows.
Type "argowt --namespace argo"
Sleep 400ms
Enter
Sleep 3s
Down@500ms 3
Up@500ms 3
Sleep 500ms

# Filter, then open a workflow's execution graph.
Type "/etl-pay"
Sleep 600ms
Enter
Sleep 800ms
Enter
Sleep 3s
Type "d"
Sleep 3s
Type "l"
Sleep 2.5s
Type "t"
Sleep 1s

# Live pod logs for a running node.
Down@400ms 2
Sleep 800ms
Tab
Sleep 3s
Enter
Sleep 4s
Escape
Sleep 500ms
Escape
Sleep 300ms
Escape
Sleep 1s

# Submit a WorkflowTemplate with a parameter, and watch it for a notification.
Tab
Sleep 1.5s
Type "/quick-hello"
Sleep 500ms
Enter
Sleep 600ms
Type "s"
Sleep 1.2s
Down
Sleep 400ms
Backspace@25ms 17
Type "hi from the README"
Sleep 800ms
Ctrl+S
Sleep 1.5s
Type "y"
Sleep 1.5s
Type "/quick-hello"
Sleep 400ms
Enter
Sleep 400ms
Type "n"
Sleep 2s

# Approve a release that waits on a suspend step.
Escape
Sleep 300ms
Type "/release"
Sleep 500ms
Enter
Sleep 800ms
Type "a"
Sleep 2s
Type "u"
Sleep 2s
Type "y"
Sleep 1.5s
Enter
Sleep 2s
Down@300ms 6
Sleep 5s
Escape
Sleep 4s

Type "q"
Sleep 500ms
