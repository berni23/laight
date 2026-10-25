package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// cmdInbox lists what is waiting for a human, from every run of every project, oldest first;
// with an item, it shows that item in full; with an item and an answer (one per question, for a
// question item), it answers it (the TUI is M3).
func cmdInbox(args []string, stdout, stderr io.Writer) int {
	home, err := state.Home()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	switch len(args) {
	case 0:
	case 1:
		return showItem(home, args[0], stdout, stderr)
	default:
		return answerItem(home, args[0], args[1:], stdout, stderr)
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
		if it.Kind == inbox.Question {
			choices = strings.TrimPrefix(choices, "[]")
		}
		line := fmt.Sprintf("%s  %-7s %s  %s  %s  %s", it.Short(), it.Kind, ago(it.Created), where, it.Title, choices)
		fmt.Fprintln(stdout, strings.TrimRight(line, " "))
		if it.Kind == inbox.Question {
			printQuestions(stdout, it, false)
		}
	}
	if answerable {
		fmt.Fprintln(stdout, "\nanswer with: laight inbox <item> <answer>   (a question: an option's number, or your own words in quotes)")
		fmt.Fprintln(stdout, "see one in full: laight inbox <item>")
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

// printQuestions prints a question item's questions with their numbered options; in full, with
// the options' descriptions.
func printQuestions(w io.Writer, it *inbox.Item, full bool) {
	for i, q := range it.Questions {
		if len(it.Questions) > 1 || full && strings.Contains(q.Question, "\n") { // else the title says it
			fmt.Fprintf(w, "    %s%s\n", numbered(i, len(it.Questions)), indent(q.Question, "    "))
		}
		for k, o := range q.Options {
			fmt.Fprintf(w, "      %d. %s\n", k+1, o.Label)
			if full && o.Description != "" {
				fmt.Fprintf(w, "         %s\n", indent(o.Description, "         "))
			}
		}
		if q.MultiSelect {
			fmt.Fprintln(w, "      (several: their numbers with commas, e.g. 1,3)")
		}
	}
}

func numbered(i, n int) string {
	if n == 1 {
		return ""
	}
	return fmt.Sprintf("Q%d. ", i+1)
}

func indent(s, pad string) string { return strings.ReplaceAll(s, "\n", "\n"+pad) }

// showItem prints one item in full: what it asks, the agent's options, where it comes from, how
// to look and how to answer.
func showItem(home, ref string, stdout, stderr io.Writer) int {
	id, err := inbox.Find(home, ref)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	it, err := inbox.Get(home, id)
	if errors.Is(err, os.ErrNotExist) {
		if done, _ := inbox.Answered(home, id); done != nil {
			fmt.Fprintf(stdout, "%s: answered: %s\n", done.Short(), done.Answer)
			return 0
		}
	}
	if err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stdout, "%s  %s  %s/%s %s", it.Short(), it.Kind, filepath.Base(it.Repo), it.Workflow, state.Short(it.Run))
	if it.Step != "" {
		fmt.Fprintf(stdout, " %s", it.Step)
	}
	if it.Seat != "" {
		fmt.Fprintf(stdout, " (%s)", it.Seat)
	}
	fmt.Fprintf(stdout, ", %s ago\n\n%s\n", ago(it.Created), it.Title)
	if it.Kind == inbox.Question {
		fmt.Fprintln(stdout)
		printQuestions(stdout, it, true)
	}
	if it.Detail != "" && it.Detail != it.Title {
		fmt.Fprintf(stdout, "\n%s\n", it.Detail)
	}
	fmt.Fprintln(stdout)
	if it.Seat != "" {
		fmt.Fprintf(stdout, "look: laight attach %s %s\n", state.Short(it.Run), it.Seat)
	}
	if why := unread(home, tmux.Server{Socket: state.Socket(home)}, it); why != "" {
		fmt.Fprintf(stdout, "nothing will read an answer: %s\n", why)
		return 0
	}
	switch {
	case it.Kind != inbox.Question:
		fmt.Fprintf(stdout, "answer: laight inbox %s %s\n", it.Short(), strings.Join(it.Choices, "|"))
	case len(it.Questions) == 1:
		fmt.Fprintf(stdout, "answer: laight inbox %s <number> (or \"your own words\")\n", it.Short())
	default:
		fmt.Fprintf(stdout, "answer: laight inbox %s %s (a number or \"your own words\" each)\n", it.Short(),
			strings.TrimSpace(strings.Repeat("<answer> ", len(it.Questions))))
	}
	return 0
}

// replies turns what someone typed into a question item's answers: an option's number (several
// with commas, for a multi-select question) becomes its label; anything else is the answer.
func replies(it *inbox.Item, args []string) ([]string, error) {
	if len(args) != len(it.Questions) {
		return nil, fmt.Errorf("%s asks %d questions: give one answer each, quoted if it has spaces", it.Short(), len(it.Questions))
	}
	out := make([]string, len(args))
	for i, a := range args {
		q := it.Questions[i]
		out[i] = a
		var labels []string
		for _, n := range strings.Split(a, ",") {
			k, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil || k < 1 || k > len(q.Options) {
				labels = nil
				break
			}
			labels = append(labels, q.Options[k-1].Label)
		}
		switch {
		case len(labels) == 1, len(labels) > 1 && q.MultiSelect:
			out[i] = strings.Join(labels, ", ")
		case len(labels) > 1:
			return nil, fmt.Errorf("question %d takes one option, not %s", i+1, a)
		}
	}
	return out, nil
}

func answerItem(home, ref string, answers []string, stdout, stderr io.Writer) int {
	id, err := inbox.Find(home, ref)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	pending, err := inbox.Get(home, id)
	if err == nil {
		if why := unread(home, tmux.Server{Socket: state.Socket(home)}, pending); why != "" {
			return fail(stderr, "%s: nothing will read an answer (%s)", pending.Short(), why)
		}
	}
	var it *inbox.Item
	switch {
	case pending != nil && pending.Kind == inbox.Question:
		var given []string
		if given, err = replies(pending, answers); err != nil {
			return fail(stderr, "%v", err)
		}
		it, err = inbox.Reply(home, id, given)
	case len(answers) != 1:
		return fail(stderr, "usage: laight inbox [<item> [<answer>…]]")
	default:
		it, err = inbox.Answer(home, id, answers[0])
	}
	if errors.Is(err, inbox.ErrAlreadyAnswered) {
		return fail(stderr, "item %s was already answered: %s", it.Short(), it.Answer)
	}
	if err != nil {
		return fail(stderr, "%v", err)
	}
	state.Append(home, state.Event{Run: it.Run, Type: "inbox.answered", Step: it.Step, Seat: it.Seat, Hop: it.Hop,
		Data: map[string]any{"item": it.ID, "kind": it.Kind, "answer": it.Answer}})
	fmt.Fprintf(stdout, "%s: %s\n", it.Short(), it.Answer)
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
