// Radar's executable delegates presentation and domain orchestration to internal/cli.
package main

import (
	"context"
	"github.com/Jake-Network/radar/internal/cli"
	"os"
	"os/signal"
)

func main() {
	// Bare `radar` on a terminal opens the menu; scripts and pipes keep help.
	// The menu traps interrupts only while a command runs.
	if len(os.Args) == 1 && terminal(os.Stdin) && terminal(os.Stdout) {
		os.Exit(cli.Interactive(context.Background(), ".", os.Stdin, os.Stdout, os.Stderr))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func terminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
