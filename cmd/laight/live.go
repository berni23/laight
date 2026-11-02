package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// liveInbox keeps the inbox open: it prints each item, in full, as it arrives, and answers the
// oldest with each line typed (another one when the line starts with its id). `laight run`
// opens it for its run in a pane under the engine, so whoever started the run answers there.
// With a run, it shows that run's items and ends with it; without one, every run's.
func liveInbox(home string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		return fail(stderr, "usage: laight inbox --follow [run]")
	}
	var run *state.Run
	if len(args) == 1 {
		_, _, r, err := findRun(args, func(r *state.Run) bool { return r.Live() })
		if err != nil {
			return fail(stderr, "%v", err)
		}
		run = r
	}
	srv := tmux.Server{Socket: state.Socket(home)}

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()

	if run != nil {
		fmt.Fprintf(stdout, "laight inbox for run %s: what its agents ask shows up here; answer at the prompt.\n", run.Short())
	} else {
		fmt.Fprintln(stdout, "laight inbox: what agents ask shows up here; answer at the prompt.")
	}
	shown := map[string]*inbox.Item{} // printed and still open, by id
	mine := map[string]bool{}         // answered here: no need to say it went
	prompted := ""
	stale := "" // the item the prompt named went before a line reached it: refuse the next id-less line
	for {
		// First: a run that ended, or whose engine died, takes its items with it, and they
		// went for that reason, not because someone answered them elsewhere.
		if run != nil {
			if r, err := state.Load(home, run.ID); err == nil && engineDied(srv, r) {
				fmt.Fprintln(stdout, "\n"+deadEngine(r))
				return 1
			} else if err == nil && !r.Live() {
				fmt.Fprintf(stdout, "\nrun %s %s\n", r.Short(), r.Status)
				return 0
			}
		}
		items, _ := inbox.Pending(home)
		var open []*inbox.Item
		why := map[string]string{} // still pending, but nothing will read an answer: why not
		for _, it := range items {
			if run == nil || it.Run == run.ID {
				if w := unread(home, srv, it); w == "" {
					open = append(open, it)
				} else {
					why[it.ID] = w
				}
			}
		}
		changed := false
		for id, it := range shown {
			if !has(open, id) {
				if !mine[id] {
					w := why[id]
					if w == "" {
						w = "answered elsewhere, or withdrawn"
					}
					fmt.Fprintf(stdout, "\n%s: %s\n", it.Short(), w)
					if id == prompted {
						stale = it.Short()
					}
				}
				delete(shown, id)
				changed = true
			}
		}
		for _, it := range open {
			if shown[it.ID] == nil {
				fmt.Fprintln(stdout, "\n"+strings.Repeat("─", 60))
				printItem(stdout, it)
				fmt.Fprintln(stdout, liveHint(it))
				shown[it.ID] = it
				changed = true
			}
		}
		if len(open) > 0 && (changed || prompted != open[0].ID) {
			fmt.Fprintf(stdout, "\nanswer %s> ", open[0].Short())
			prompted = open[0].ID
		} else if len(open) == 0 && prompted != "" {
			fmt.Fprintln(stdout, "\nnothing waiting")
			prompted = ""
		}
		select {
		case line, ok := <-lines:
			if !ok {
				return 0
			}
			gone := stale
			stale = ""
			if strings.TrimSpace(line) == "" {
				prompted = "" // prompt again
				continue
			}
			if len(open) == 0 {
				fmt.Fprintln(stdout, "nothing to answer")
				continue
			}
			// A line typed for an item that has since gone is not given to the next one, which
			// the human may not have read: they type it again, or start it with an item's id.
			if gone != "" && !namesItem(open, line) {
				fmt.Fprintf(stdout, "%s went before your answer reached it; nothing sent. %s is waiting: type your answer again.\n", gone, open[0].Short())
				prompted = ""
				continue
			}
			it, answers, err := liveAnswer(open, line)
			if err == nil {
				it, err = answer(home, it.ID, answers)
			}
			if err != nil {
				fmt.Fprintf(stdout, "%v\n", err)
			} else {
				mine[it.ID] = true
				fmt.Fprintf(stdout, "%s: %s\n", it.Short(), it.Answer)
			}
			prompted = ""
		case <-tick.C:
		}
	}
}

// namesItem says whether line starts with the id of one of the open items.
func namesItem(open []*inbox.Item, line string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(line), " ")
	for _, o := range open {
		if strings.EqualFold(first, o.Short()) {
			return true
		}
	}
	return false
}

func has(items []*inbox.Item, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// liveHint says what to type at the prompt for it.
func liveHint(it *inbox.Item) string {
	switch {
	case it.Kind != inbox.Question:
		return "type: " + strings.Join(it.Choices, " | ")
	case len(it.Questions) == 1:
		return "type: an option's number, or your own words"
	}
	return fmt.Sprintf("type: %d answers, a number or \"your own words\" each", len(it.Questions))
}

// liveAnswer reads a line typed at the prompt: the item it answers (the oldest, unless the line
// starts with another's id) and its answers. A lone question takes the whole line as typed;
// several questions take one word each, quoted for spaces. A choice may be shortened (y, n, a…)
// when only one choice starts that way.
func liveAnswer(open []*inbox.Item, line string) (*inbox.Item, []string, error) {
	it, rest := open[0], strings.TrimSpace(line)
	first, after, _ := strings.Cut(rest, " ")
	for _, o := range open {
		if strings.EqualFold(first, o.Short()) {
			it, rest = o, strings.TrimSpace(after)
		}
	}
	if it.Kind == inbox.Question && len(it.Questions) == 1 {
		return it, []string{unquote(rest)}, nil
	}
	words, err := splitWords(rest)
	if err != nil {
		return nil, nil, err
	}
	if it.Kind != inbox.Question && len(words) == 1 && words[0] != "" { // "" is not short for anything
		var match []string
		for _, c := range it.Choices {
			if strings.HasPrefix(c, strings.ToLower(words[0])) {
				match = append(match, c)
			}
		}
		if len(match) == 1 {
			words[0] = match[0]
		}
	}
	return it, words, nil
}

// splitWords splits a line into words, keeping what is in quotes together.
func splitWords(s string) ([]string, error) {
	var words []string
	var w strings.Builder
	quote, in := rune(0), false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			w.WriteRune(r)
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				words, in = append(words, w.String()), false
				w.Reset()
			}
		default:
			w.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a quote isn't closed: %s", s)
	}
	if in {
		words = append(words, w.String())
	}
	return words, nil
}

// unquote drops the quotes around a whole answer, typed as at the command line.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
