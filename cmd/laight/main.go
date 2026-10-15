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
  laight init [--from openrig <dir>]   create .laight/ (or import an OpenRig .openrig/ folder)
  laight validate [workflow…]          check the workflows: schema, routes, seats, templates
  laight version                       print the version

Coming next: run, watch, show, inbox, attach, abort, doctor, done.
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
