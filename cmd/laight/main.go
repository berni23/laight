// Command laight runs teams of coding agents in tmux from YAML workflow files.
// The design is in CLAUDE.md.
package main

import (
	"fmt"
	"io"
	"os"
)

// Set at build time by GoReleaser (-ldflags "-X main.version=… -X main.commit=… -X main.date=…").
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `laight runs teams of coding agents in tmux from YAML workflow files.

Usage:
  laight init                          create .laight/
  laight validate [workflow…]          check the workflows: schema, routes, seats, templates
  laight run <workflow> [key=value…]   spin up the rig of a new run and follow it
  laight attach [run] [seat]           jump into a run's tmux session, or one seat's window
  laight abort [run] [--yes]           stop a live run; on a finished one, delete its worktrees
  laight inbox [<item> <answer>]       what waits for a human, in every run; or answer one
  laight done <exit> [key=value…]      for agents: finish the current step
  laight version                       print the version

[run] is a run id or its start; leave it out when the project has one live run.
<item> is an inbox item id or its start.
Coming next: watch, show, doctor.
See https://github.com/berni23/laight
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return cmdInit(rest, stdout, stderr)
	case "validate":
		return cmdValidate(rest, stdout, stderr)
	case "run":
		return cmdRun(rest, stdout, stderr)
	case "attach":
		return cmdAttach(rest, stdout, stderr)
	case "abort":
		return cmdAbort(rest, stdout, stderr)
	case "inbox":
		return cmdInbox(rest, stdout, stderr)
	case "done":
		return cmdDone(rest, stdout, stderr)
	case "engine": // window 0 of a rig, started by run
		return cmdEngine(rest, stdout, stderr)
	case "hook": // called by the agent runtime's hooks
		return cmdHook(rest, os.Stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "laight %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "laight: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// fail prints a one-line error and returns exit code 1.
func fail(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "laight: "+format+"\n", args...)
	return 1
}
