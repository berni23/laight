package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// cmdInbox lists what is waiting for a human, from every run of every project, oldest first; with
// an item and an answer, it answers that item (the TUI is M3).
func cmdInbox(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 && len(args) != 2 {
		return fail(stderr, "usage: laight inbox [<item> <answer>]")
	}
	home, err := state.Home()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if len(args) == 2 {
		return answerItem(home, args[0], args[1], stdout, stderr)
	}
	items, err := inbox.Pending(home)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "inbox empty")
		return 0
	}
	for _, it := range items {
		where := filepath.Base(it.Repo) + "/" + it.Workflow + " " + state.Short(it.Run)
		if it.Step != "" {
			where += " " + it.Step
		}
		fmt.Fprintf(stdout, "%s  %-7s %s  %s  %s  [%s]\n", it.Short(), it.Kind, ago(it.Created), where, it.Title, strings.Join(it.Choices, "|"))
	}
	fmt.Fprintln(stdout, "\nanswer with: laight inbox <item> <answer>")
	return 0
}

func answerItem(home, ref, answer string, stdout, stderr io.Writer) int {
	id, err := inbox.Find(home, ref)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	// A run whose engine died never reads the answer (CLAUDE.md §4): say so instead.
	if it, err := inbox.Get(home, id); err == nil {
		if r, err := state.Load(home, it.Run); err == nil && engineDied(tmux.Server{Socket: state.Socket(home)}, r) {
			return fail(stderr, "%s", deadEngine(r))
		}
	}
	it, err := inbox.Answer(home, id, answer)
	if errors.Is(err, inbox.ErrAlreadyAnswered) {
		return fail(stderr, "item %s was already answered: %s", it.Short(), it.Answer)
	}
	if err != nil {
		return fail(stderr, "%v", err)
	}
	state.Append(home, state.Event{Run: it.Run, Type: "inbox.answered", Step: it.Step, Seat: it.Seat, Hop: it.Hop,
		Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": answer}})
	fmt.Fprintf(stdout, "%s: %s\n", it.Short(), answer)
	return 0
}

// ago is how long ago t was, for people: 40s, 12m, 3h, 2d.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
