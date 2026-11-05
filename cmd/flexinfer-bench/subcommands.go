package main

import "fmt"

// subcommands are the flexinfer-bench modes selected by the first argument.
// Each owns the rest of the command line and returns the process exit code.
// A mode registers itself from its own file's init, so adding one never edits
// main.go; without a registered first argument the classic flag-only
// benchmark runs unchanged.
var subcommands = map[string]func(args []string) int{}

func registerSubcommand(name string, run func(args []string) int) {
	if _, dup := subcommands[name]; dup {
		panic(fmt.Sprintf("flexinfer-bench: subcommand %q registered twice", name))
	}
	subcommands[name] = run
}

// dispatchSubcommand runs the subcommand args[0] names, if one is registered.
func dispatchSubcommand(args []string) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	run, ok := subcommands[args[0]]
	if !ok {
		return 0, false
	}
	return run(args[1:]), true
}
