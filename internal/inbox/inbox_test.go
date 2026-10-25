package inbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestPutAnswer(t *testing.T) {
	home := t.TempDir()
	a := Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAA1", Run: "r1", Kind: Gate, Title: "merge?", Choices: []string{"yes", "no"}}
	b := Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAB2", Run: "r2", Kind: Failed, Choices: []string{Retry, Abort},
		Title:  "step x failed: \x1b[2Jtoken ghp_abcdefghijklmnopqrstuvwxyz0123\nnext",
		Detail: "token ghp_abcdefghijklmnopqrstuvwxyz0123 in /Users/someone/a/long/path/without/any/dots/at/all\n\x07line two"}
	for _, it := range []Item{b, a, a} { // a twice: putting it again does nothing
		if err := Put(home, it); err != nil {
			t.Fatal(err)
		}
	}
	items, err := Pending(home)
	if err != nil || len(items) != 2 || items[0].ID != a.ID {
		t.Fatalf("pending %v, %v", items, err)
	}
	if d := items[1].Detail; strings.Contains(d, "ghp_") || !strings.Contains(d, "/Users/someone/a/long/path/without/any/dots/at/all") {
		t.Errorf("detail not redacted right: %s", d)
	}
	// The title leaves the machine (§6) and both reach a terminal: no token, no control characters.
	if ti := items[1].Title; ti != "step x failed:  [2Jtoken *** next" {
		t.Errorf("title = %q", ti)
	}
	if d := items[1].Detail; !strings.HasSuffix(d, "\n line two") {
		t.Errorf("detail = %q", d)
	}

	if _, err := Answer(home, a.ID, "maybe"); err == nil || !strings.Contains(err.Error(), "choose one of: yes, no") {
		t.Errorf("a wrong choice: %v", err)
	}
	if it, _ := Answered(home, a.ID); it != nil {
		t.Errorf("answered by a wrong choice: %+v", it)
	}
	if id, err := Find(home, "aaaaa1"); err != nil || id != a.ID {
		t.Errorf("find = %s, %v", id, err)
	}
	if _, err := Find(home, "01a"); err == nil || !strings.Contains(err.Error(), "matches 2 items") {
		t.Errorf("an ambiguous find: %v", err)
	}

	// Answered from two places at once: exactly one wins.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, choice := range []string{"yes", "no"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Answer(home, a.ID, choice)
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("answers: %v", errs)
	}
	lost := errs[0]
	if lost == nil {
		lost = errs[1]
	}
	if !errors.Is(lost, ErrAlreadyAnswered) {
		t.Errorf("the late answer: %v", lost)
	}
	it, _ := Answered(home, a.ID)
	if it == nil || (it.Answer != "yes" && it.Answer != "no") || it.Answered == nil {
		t.Fatalf("answered: %+v", it)
	}
	if late, err := Answer(home, a.ID, "yes"); !errors.Is(err, ErrAlreadyAnswered) || late.Answer != it.Answer {
		t.Errorf("an answer after: %+v %v", late, err)
	}
	if id, err := Find(home, "aaaaa1"); err != nil || id != a.ID {
		t.Errorf("an answered item is still found: %s, %v", id, err)
	}
	if items, _ := Pending(home); len(items) != 1 || items[0].ID != b.ID {
		t.Errorf("pending after the answer: %v", items)
	}
	if id, err := Find(home, ""); err == nil { // an unset variable in a script must not pick one
		t.Errorf("an empty ref found %s", id)
	}
	if err := Put(home, a); err != nil {
		t.Fatal(err)
	}
	if items, _ := Pending(home); len(items) != 1 {
		t.Errorf("an answered item came back: %v", items)
	}

	// An answered file that can't be read is an error, never "already answered" with no item.
	c := Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAC3", Run: "r2", Kind: Gate, Title: "c", Choices: []string{"yes"}}
	if err := Put(home, c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(answeredFile(home, c.ID), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if it, err := Answer(home, c.ID, "yes"); it == nil && (err == nil || errors.Is(err, ErrAlreadyAnswered)) {
		t.Errorf("a broken answer: %v, %v", it, err)
	}
	os.Remove(answeredFile(home, c.ID))

	Withdraw(home, "r2")
	if items, _ := Pending(home); len(items) != 0 {
		t.Errorf("withdrawn: %v", items)
	}
	if _, err := os.Stat(answeredFile(home, a.ID)); err != nil {
		t.Errorf("withdraw touched an answer: %v", err)
	}
}

// A long token is redacted only when it mixes cases and digits: a commit SHA or a branch name
// in a gate title is what the human is deciding on.
func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"Merge branch 123-make-the-engine-hold-failed-steps-for-retry into dev?": "Merge branch 123-make-the-engine-hold-failed-steps-for-retry into dev?",
		"revert 24285a546915eb4da1a26ed2d6ec51fe8dccaaff?":                       "revert 24285a546915eb4da1a26ed2d6ec51fe8dccaaff?",
		"key AKIAxyz0123456789abcdefGHIJKLmnopqrstuvwx9 leaked":                  "key *** leaked",
		"sk-abcdefghijklmnop0123 and Bearer abc.def.ghi":                         "*** and ***",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Put strips the seat's secret env values itself: an item raised inside a seat can't leak them
// because its producer forgot to pass them.
func TestPutStripsSeatSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("K", "v4lue-x")
	t.Setenv(RedactEnv, "K,UNSET")
	if err := Put(home, Item{ID: "01S", Run: "r", Kind: Permission, Title: "uses v4lue-x", Detail: "a v4lue-x b", Choices: []string{Allow}}); err != nil {
		t.Fatal(err)
	}
	it, err := Get(home, "01S")
	if err != nil || it.Title != "uses ***" || it.Detail != "a *** b" {
		t.Errorf("item %+v %v", it, err)
	}
}

// A detail can be JSON (a tool's input), where a secret appears escaped, with or without HTML
// escaping.
func TestPutStripsEscapedSecrets(t *testing.T) {
	home := t.TempDir()
	secret := "ab\"cd\\ef<"
	t.Setenv("K", secret)
	t.Setenv(RedactEnv, "K")
	html, _ := json.Marshal(map[string]string{"c": secret})
	var plain bytes.Buffer
	enc := json.NewEncoder(&plain)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]string{"d": secret})
	detail := string(html) + strings.TrimSpace(plain.String())
	if err := Put(home, Item{ID: "01E", Run: "r", Kind: Permission, Title: "t", Detail: detail, Choices: []string{Allow}}); err != nil {
		t.Fatal(err)
	}
	if it, err := Get(home, "01E"); err != nil || it.Detail != `{"c":"***"}{"d":"***"}` {
		t.Errorf("detail %q from %q: %v", it.Detail, detail, err)
	}
}

// A long detail is cut short after redaction, so a secret the cut runs through can't leave its
// first half behind.
func TestPutRedactsBeforeCut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("K", "hunter2-not-a-pattern")
	t.Setenv(RedactEnv, "K")
	detail := strings.Repeat("x", 1990) + "hunter2-not-a-pattern" + strings.Repeat("y", 100)
	if err := Put(home, Item{ID: "01C", Run: "r", Kind: Permission, Title: "t", Detail: detail, Choices: []string{Allow}}); err != nil {
		t.Fatal(err)
	}
	it, err := Get(home, "01C")
	if err != nil || strings.Contains(it.Detail, "hunt") || !strings.HasSuffix(it.Detail, "…") || len(it.Detail) > 2000+len("…") {
		t.Errorf("detail ends %q: %v", it.Detail[max(0, len(it.Detail)-30):], err)
	}
}

// A question item takes one non-empty answer per question, through Reply only; its questions
// are redacted and cleaned like its title, and the first answer wins.
func TestReply(t *testing.T) {
	home := t.TempDir()
	q := Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAQ1", Run: "r", Kind: Question, Title: "a asks: which?",
		Questions: []Ask{{Question: "which ghp_abcdefghijklmnopqrstuvwxyz0123?\x1b[2J", Options: []Option{{Label: "x\x07", Description: "long\nwhy"}}}}}
	if err := Put(home, q); err != nil {
		t.Fatal(err)
	}
	it, _ := Get(home, q.ID)
	if got := it.Questions[0]; got.Question != "which ***? [2J" || got.Options[0].Label != "x " || got.Options[0].Description != "long\nwhy" {
		t.Errorf("questions as put: %+v", got)
	}
	if _, err := Answer(home, q.ID, "x"); err == nil {
		t.Error("Answer answered a question")
	}
	for _, bad := range [][]string{nil, {"a", "b"}, {" "}} {
		if _, err := Reply(home, q.ID, bad); err == nil {
			t.Errorf("Reply took %q", bad)
		}
	}
	if it, err := Reply(home, q.ID, []string{"x "}); err != nil || it.Answer != "x " || it.Answered == nil {
		t.Fatalf("reply: %+v, %v", it, err)
	}
	if it, err := Reply(home, q.ID, []string{"other"}); !errors.Is(err, ErrAlreadyAnswered) || it.Answers[0] != "x " {
		t.Errorf("a late reply: %+v, %v", it, err)
	}
	g := Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAG1", Run: "r", Kind: Gate, Title: "go?", Choices: []string{"yes"}}
	if err := Put(home, g); err != nil {
		t.Fatal(err)
	}
	if _, err := Reply(home, g.ID, []string{"yes"}); err == nil {
		t.Error("Reply answered a gate")
	}
}
