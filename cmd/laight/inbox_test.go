package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
)

// An item whose run already ended is listed as such, and an answer to it is refused: nothing
// would read it.
func TestInboxRunEnded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAIGHT_HOME", home)
	id := state.NewID(time.Now())
	if err := state.Create(home, &state.Run{ID: id, Workflow: "demo", Status: state.Succeeded}); err != nil {
		t.Fatal(err)
	}
	it := inbox.Item{ID: state.NewID(time.Now()), Run: id, Workflow: "demo", Kind: inbox.Gate, Title: "Go?", Choices: []string{"yes", "no"}}
	if err := inbox.Put(home, it); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := cmdInbox(nil, &out, &errs); code != 0 || !strings.Contains(out.String(), "run succeeded") ||
		strings.Contains(out.String(), "yes|no") || strings.Contains(out.String(), "answer with") {
		t.Errorf("list: %d\n%s%s", code, out.String(), errs.String())
	}
	out.Reset()
	if code := cmdInbox([]string{it.Short(), "yes"}, &out, &errs); code == 0 || !strings.Contains(errs.String(), "run succeeded") {
		t.Errorf("answered an ended run's item: %d\n%s%s", code, out.String(), errs.String())
	}
	if done, _ := inbox.Answered(home, it.ID); done != nil {
		t.Errorf("answer recorded: %+v", done)
	}
}

// A pending file that can't be read is reported, not taken for an empty inbox (§6: nobody waits
// without an item a human can see).
func TestInboxUnreadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAIGHT_HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "inbox", "01AAAAAAAAAAAAAAAAAAAAAAA1.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := cmdInbox(nil, &out, &errs); code == 0 || strings.Contains(out.String(), "inbox empty") || !strings.Contains(errs.String(), "01AAAAAAAAAAAAAAAAAAAAAAA1") {
		t.Errorf("an unreadable item: %d\n%s%s", code, out.String(), errs.String())
	}
}

// An expired item's hook has denied, or is gone: an answer to it is refused, not reported as
// given.
func TestInboxExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAIGHT_HOME", home)
	past := time.Now().Add(-time.Second)
	it := inbox.Item{ID: state.NewID(time.Now()), Run: "01NOSUCHRUN000000000000000", Kind: inbox.Permission, Title: "a wants to use Bash",
		Choices: []string{inbox.Allow, inbox.Deny}, Expires: &past}
	if err := inbox.Put(home, it); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := cmdInbox([]string{it.Short(), "allow"}, &out, &errs); code == 0 || !strings.Contains(errs.String(), "expired") {
		t.Errorf("answered an expired item: %d\n%s%s", code, out.String(), errs.String())
	}
}
