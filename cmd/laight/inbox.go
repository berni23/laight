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
	// An item that can't be read still holds its run: say so, never "inbox empty".
	items, err := inbox.Pending(home)
	if err != nil {
		fmt.Fprintf(stderr, "laight: can't read an inbox item: %v\n", err)
	}
	if len(items) == 0 {
		if err != nil {
			return 1
		}
		fmt.Fprintln(stdout, "inbox empty")
		return 0
	}
	srv := tmux.Server{Socket: state.Socket(home)}
	answerable := false
	for _, it := range items {
		where := filepath.Base(it.Repo) + "/" + it.Workflow + " " + state.Short(it.Run)
		if it.Step != "" {
			where += " " + it.Step
		}
		choices := "[" + strings.Join(it.Choices, "|") + "]"
		if why := unread(home, srv, it); why != "" {
			choices = "(" + why + ")"
		} else {
			answerable = true
		}
		fmt.Fprintf(stdout, "%s  %-7s %s  %s  %s  %s\n", it.Short(), it.Kind, ago(it.Created), where, it.Title, choices)
	}
	if answerable {
		fmt.Fprintln(stdout, "\nanswer with: laight inbox <item> <answer>")
	}
	if err != nil {
		return 1
	}
	return 0
}

// unread says why nothing will read an answer to the item: it expired (its hook denied, or was
// killed), its run ended, or its engine died (CLAUDE.md §4). It is empty when the run is live,
// or can't be read.
func unread(home string, srv tmux.Server, it *inbox.Item) string {
	if it.Expires != nil && time.Now().After(*it.Expires) {
		return "it expired"
	}
	r, err := state.Load(home, it.Run)
	switch {
	case err != nil:
		return ""
	case engineDied(srv, r):
		return "its engine died: laight abort " + r.Short()
	case !r.Live():
		return "run " + r.Status
	}
	return ""
}

func answerItem(home, ref, answer string, stdout, stderr io.Writer) int {
	id, err := inbox.Find(home, ref)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if it, err := inbox.Get(home, id); err == nil {
		if why := unread(home, tmux.Server{Socket: state.Socket(home)}, it); why != "" {
			return fail(stderr, "%s: nothing will read an answer (%s)", it.Short(), why)
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
