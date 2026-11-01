package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
)

// Gauntlet gates that run as `flexinfer-bench <name> [flags]` instead of the
// flag-driven benchmark in main. A gate registers itself from its own file's
// init, so adding one never edits main.go:
//
//	func init() { registerSubcommand("my_gate", "one line", runMyGate) }
//
// main calls dispatchSubcommand(os.Args[1:]) first; it exits with the gate's
// code when the first argument names a gate and returns otherwise, so every
// flag invocation of the benchmark is unchanged.

type subcommandFunc func(ctx context.Context, args []string, stdout, stderr io.Writer) int

type subcommand struct {
	summary string
	run     subcommandFunc
}

var subcommands = map[string]subcommand{}

func registerSubcommand(name, summary string, run subcommandFunc) {
	if _, dup := subcommands[name]; dup {
		panic("flexinfer-bench: duplicate subcommand " + name)
	}
	subcommands[name] = subcommand{summary: summary, run: run}
}

// dispatchSubcommand runs the gate named by args[0] and exits with its code.
// It returns when args[0] is not a gate name (or is a flag).
func dispatchSubcommand(args []string) {
	code, handled := runSubcommand(context.Background(), args, os.Stdout, os.Stderr)
	if handled {
		os.Exit(code)
	}
}

// runSubcommand is dispatchSubcommand without the exit, for tests. The
// gate's context is cancelled on SIGINT or SIGTERM.
func runSubcommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	if args[0] == "help" || args[0] == "subcommands" {
		writeSubcommandHelp(stdout)
		return 0, true
	}
	sc, ok := subcommands[args[0]]
	if !ok {
		return 0, false
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sc.run(ctx, args[1:], stdout, stderr), true
}

func writeSubcommandHelp(w io.Writer) {
	names := make([]string, 0, len(subcommands))
	for name := range subcommands {
		names = append(names, name)
	}
	sort.Strings(names)
	_, _ = fmt.Fprintln(w, "flexinfer-bench gates (flexinfer-bench <gate> -h for flags):")
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "  %-24s %s\n", name, subcommands[name].summary)
	}
	_, _ = fmt.Fprintln(w, "Without a gate name, flexinfer-bench runs the benchmark (flexinfer-bench -h).")
}
