// Package cli parses process arguments without constructing Kubernetes clients.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"argowt/internal/app"
	"argowt/internal/argourl"
	"argowt/internal/config"
	"argowt/internal/release"
	"argowt/internal/version"
)

const helpText = `argowt monitors and operates Argo Workflows from the terminal.

Usage:
  argowt [flags] [URL]
  argowt update           install the latest release

URL is an Argo web UI link to open, e.g.
https://argo.example.com/workflows/argo/hello?nodeId=hello-123

Flags:
  --kubeconfig PATH       path to a kubeconfig file
  --context NAME          kubeconfig context to use
  --namespace NAME        initial namespace (repeatable or comma-separated)
  --all-namespaces        start with all accessible namespaces
  --instanceid ID         Argo controller instance ID (defaults to ARGO_INSTANCEID)
  --config PATH           path to the argowt configuration file
  --version               print version information and exit
  -h, --help              show this help
`

// Runner is the application entry point injected after argument parsing.
type Runner func(context.Context, app.Options) error

// Run parses arguments and invokes run. Help and version return before paths are
// resolved or any application dependencies are constructed.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, run Runner) error {
	if len(args) == 1 && args[0] == "update" {
		return update(ctx, stdout)
	}

	flags := flag.NewFlagSet("argowt", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, helpText)
	}

	var opts app.Options
	var namespaces namespaceValue
	var instanceID optionalString
	var configPath string
	var showVersion bool
	var showHelp bool

	flags.StringVar(&opts.Kubeconfig, "kubeconfig", "", "path to a kubeconfig file")
	flags.StringVar(&opts.Context, "context", "", "kubeconfig context to use")
	flags.Var(&namespaces, "namespace", "initial namespace (repeatable or comma-separated)")
	flags.BoolVar(&opts.AllNamespaces, "all-namespaces", false, "start with all accessible namespaces")
	flags.Var(&instanceID, "instanceid", "Argo controller instance ID")
	flags.StringVar(&configPath, "config", "", "path to the argowt configuration file")
	flags.BoolVar(&showVersion, "version", false, "print version information and exit")
	flags.BoolVar(&showHelp, "help", false, "show help")
	flags.BoolVar(&showHelp, "h", false, "show help")

	if err := flags.Parse(args); err != nil {
		return err
	}

	// Flags may follow the URL: argowt URL --context dev.
	var link string
	if flags.NArg() > 0 {
		link = flags.Arg(0)
		if err := flags.Parse(flags.Args()[1:]); err != nil {
			return err
		}
	}
	if showHelp {
		_, err := io.WriteString(stdout, helpText)
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if showVersion {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}
	if run == nil {
		return errors.New("application runner is nil")
	}

	paths, err := config.ResolvePaths(configPath)
	if err != nil {
		return err
	}
	opts.Paths = paths
	opts.Namespaces = namespaces.values

	// Scope the session to the URL namespace unless flags set a scope.
	if link != "" {
		opts.Open, err = argourl.Parse(link)
		if err != nil {
			return err
		}
		if opts.Open.Namespace != "" && len(opts.Namespaces) == 0 && !opts.AllNamespaces {
			opts.Namespaces = []string{opts.Open.Namespace}
		}
	}
	if instanceID.set {
		opts.InstanceID = instanceID.value
	} else {
		opts.InstanceID = os.Getenv("ARGO_INSTANCEID")
	}

	return run(ctx, opts)
}

// update replaces the running binary with the latest release.
func update(ctx context.Context, stdout io.Writer) error {
	tag, err := release.Latest(ctx)
	if err != nil {
		return fmt.Errorf("find latest release: %w", err)
	}
	if !release.Newer(version.Version, tag) {
		_, err := fmt.Fprintf(stdout, "argowt is up to date (%s)\n", tag)
		return err
	}

	// Resolve symlinks so the real file is replaced, not the link.
	path, err := os.Executable()
	if err != nil {
		return err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}

	if err := release.Install(ctx, tag, path); err != nil {
		return fmt.Errorf("install %s: %w", tag, err)
	}
	_, err = fmt.Fprintf(stdout, "Updated argowt %s → %s\n", version.Version, tag)
	return err
}

type optionalString struct {
	value string
	set   bool
}

func (v *optionalString) String() string { return v.value }

func (v *optionalString) Set(value string) error {
	v.value = value
	v.set = true
	return nil
}

type namespaceValue struct {
	values []string
}

func (v *namespaceValue) String() string { return strings.Join(v.values, ",") }

func (v *namespaceValue) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return errors.New("namespace must not be empty")
		}
		if !contains(v.values, item) {
			v.values = append(v.values, item)
		}
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
