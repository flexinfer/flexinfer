// Package render provides shared rendering primitives for the loom CLI.
//
// Status: pre-1.0; CLI internal.
//
// The package collects the four ergonomics primitives required by the
// UNIFY-4 visibility surface (cost, rbac, health, presence list, sessions
// list, tasks list, catalog status) so each subcommand renders consistently
// with the same flag semantics:
//
//   - JSON: writes a pretty-printed JSON document to a writer with HTML
//     escaping disabled. Subcommands wire it from `--json`.
//   - Table: a row/header struct with column-aligned text rendering. The
//     non-JSON path for any list-shaped output. Honors NO_COLOR.
//   - Watch: a render loop that re-invokes a callback at a fixed interval,
//     clearing the screen between draws. Subcommands wire it from
//     `--watch=<duration>`. Caller owns context cancellation.
//   - Filter: a `key=value,key=value` selector parsed once per invocation
//     and applied row-by-row. Subcommands wire it from `--filter`.
//
// Call-site shape:
//
//	if jsonOutput {
//	    return render.JSON(cmd.OutOrStdout(), payload)
//	}
//	return render.Table{Headers: ..., Rows: ...}.Render(cmd.OutOrStdout(), render.Options{})
//
// No-color contract: the package treats the NO_COLOR environment variable
// as authoritative (https://no-color.org/). When it is set to any non-empty
// value the package never emits ANSI escape sequences, regardless of caller
// options. Callers may additionally set Options.NoColor for explicit opt-out.
//
// Exit-code policy contract: this package never calls os.Exit. Render
// functions return errors; subcommands map errors to exit codes following
// the convention introduced by `loom status` (UNIFY-3): zero for healthy,
// non-zero when the underlying surface reports unhealthy state. The
// rendering layer is purely descriptive.
package render
