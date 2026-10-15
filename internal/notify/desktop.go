package notify

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

const (
	desktopTitle = "argowt"

	// desktopTimeout bounds a hung tool, e.g. notify-send without a D-Bus session.
	desktopTimeout = 5 * time.Second
)

// appleScript reads title and body from argv, so workflow text is never
// parsed as AppleScript.
var appleScript = []string{
	"-e", "on run argv",
	"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
	"-e", "end run",
}

var errNoDesktop = errors.New("desktop notifications are not supported on this platform")

// NewDesktop returns the OS notifier, or a no-op where none is supported.
func NewDesktop() Desktop {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return NoopDesktop{}
	}
	return systemDesktop{}
}

// systemDesktop sends notifications through the OS notification tool:
// osascript on macOS, notify-send on Linux.
type systemDesktop struct{}

func (systemDesktop) Notify(event Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), desktopTimeout)
	defer cancel()

	body := fmt.Sprintf("%s/%s: %s → %s", event.Namespace, event.Name, event.From, event.To)
	command, err := desktopCommand(ctx, runtime.GOOS, body)
	if err != nil {
		return err
	}

	if err := command.Run(); err != nil {
		return fmt.Errorf("desktop notification: %w", err)
	}
	return nil
}

func desktopCommand(ctx context.Context, goos, body string) (*exec.Cmd, error) {
	switch goos {
	case "darwin":
		arguments := append(append([]string(nil), appleScript...), desktopTitle, body)
		return exec.CommandContext(ctx, "osascript", arguments...), nil
	case "linux":
		return exec.CommandContext(ctx, "notify-send", desktopTitle, body), nil
	}
	return nil, errNoDesktop
}
