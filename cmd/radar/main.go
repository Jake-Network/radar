// Radar's executable delegates presentation and domain orchestration to internal/cli.
package main

import (
	"context"
	"github.com/radar-engine/radar/internal/cli"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
