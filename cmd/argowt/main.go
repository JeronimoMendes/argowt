package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"argowt/internal/app"
	"argowt/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, app.Run); err != nil {
		fmt.Fprintf(os.Stderr, "argowt: %v\n", err)
		os.Exit(1)
	}
}
