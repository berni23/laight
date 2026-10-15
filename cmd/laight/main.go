// Command laight runs teams of coding agents in tmux from YAML specs.
// The design is in CLAUDE.md; most commands are not implemented yet.
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

const usage = `laight runs teams of coding agents in tmux from YAML specs.

Usage:
  laight version    print the version
  laight help       show this help

laight is in its design phase: see https://github.com/berni23/laight
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "laight %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "laight: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
