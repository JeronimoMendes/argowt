// Package clipboard copies text to the system clipboard.
package clipboard

import (
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// nativeCommands are local clipboard tools, tried in order.
var nativeCommands = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
}

// Copy puts text on the clipboard. It tries a local tool first, then the
// OSC 52 escape sequence, which also works over SSH in most terminals.
func Copy(text string) error {
	for _, command := range nativeCommands {
		if copyNative(command, text) == nil {
			return nil
		}
	}
	return copyOSC52(os.Stdout, text)
}

func copyNative(command []string, text string) error {
	process := exec.Command(command[0], command[1:]...)
	process.Stdin = strings.NewReader(text)
	return process.Run()
}

func copyOSC52(output io.Writer, text string) error {
	_, err := io.WriteString(output, ansi.SetSystemClipboard(text))
	return err
}
