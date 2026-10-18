package inbox

import (
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
