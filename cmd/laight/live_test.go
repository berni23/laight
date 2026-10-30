package main

import (
	"bytes"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

func TestLiveAnswer(t *testing.T) {
	perm := &inbox.Item{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Kind: inbox.Permission, Choices: []string{inbox.Allow, inbox.Deny}}
	one := &inbox.Item{ID: "01BBBBBBBBBBBBBBBBBBBBBBBB", Kind: inbox.Question, Questions: []inbox.Ask{{Question: "q"}}}
	two := &inbox.Item{ID: "01CCCCCCCCCCCCCCCCCCCCCCCC", Kind: inbox.Question, Questions: []inbox.Ask{{Question: "q"}, {Question: "r"}}}
	open := []*inbox.Item{perm, one, two}
	for _, tt := range []struct {
		line string
		it   *inbox.Item
		want []string
	}{
		{"a", perm, []string{"allow"}},
		{"DENY", perm, []string{"deny"}},
		{"maybe", perm, []string{"maybe"}}, // no choice: answer refuses it
		{"bbbbbb use the yaml one", one, []string{"use the yaml one"}},
		{`bbbbbb "quoted words"`, one, []string{"quoted words"}},
		{`cccccc 2 "my own words"`, two, []string{"2", "my own words"}},
		{`cccccc 1,3   'x y'`, two, []string{"1,3", "x y"}},
	} {
		it, got, err := liveAnswer(open, tt.line)
		if err != nil || it != tt.it || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%q: %v %q %v, want %v %q", tt.line, it.Short(), got, err, tt.it.Short(), tt.want)
		}
	}
	if _, _, err := liveAnswer(open, `cccccc "open`); err == nil {
		t.Error("an open quote is an error")
	}
}

// The live inbox shows each item as it arrives, with what the agent said before asking, and
// answers the oldest with what is typed at the prompt.
func TestLiveInbox(t *testing.T) {
	home, id := doneEnv(t)
	liveEngine(t, home, id)
	pollEvery = 5 * time.Millisecond
	q := inbox.Item{ID: state.NewID(time.Now()), Run: id, Workflow: "wf", Seat: "a", Kind: inbox.Question,
		Title: "a asks: Keep which?", Detail: "Found two loaders.", Questions: []inbox.Ask{{Question: "Keep which?",
			Options: []inbox.Option{{Label: "yaml"}, {Label: "toml"}}}}}
	if err := inbox.Put(home, q); err != nil {
		t.Fatal(err)
	}

	in, typed := io.Pipe()
	var out syncBuffer
	done := make(chan int)
	go func() { done <- liveInbox(home, []string{id}, in, &out, &out) }()
	waitFor(t, &out, "answer "+(&q).Short()+"> ")
	for _, want := range []string{"what a said before asking:\n    Found two loaders.", "2. toml", "type: an option's number"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in:\n%s", want, out.String())
		}
	}
	io.WriteString(typed, "2\n")
	waitFor(t, &out, (&q).Short()+": toml")
	if it, _ := inbox.Answered(home, q.ID); it == nil || it.Answers[0] != "toml" || it.Picks[0][0] != 2 {
		t.Errorf("answered: %+v", it)
	}

	// An item answered elsewhere goes; a permission answered with a short choice.
	p := inbox.Item{ID: state.NewID(time.Now().Add(time.Second)), Run: id, Workflow: "wf", Seat: "a", Kind: inbox.Permission,
		Title: "a wants to use Bash", Choices: []string{inbox.Allow, inbox.Deny}}
	inbox.Put(home, p)
	waitFor(t, &out, "type: allow | deny")
	io.WriteString(typed, "d\n")
	waitFor(t, &out, (&p).Short()+": deny")
	if strings.Contains(out.String(), "answered elsewhere") {
		t.Errorf("an answer typed here is not elsewhere:\n%s", out.String())
	}

	// The run ending ends it.
	state.Update(home, id, func(r *state.Run) error { r.Status = state.Succeeded; return nil })
	select {
	case code := <-done:
		if code != 0 || !strings.Contains(out.String(), "run "+state.Short(id)+" succeeded") {
			t.Errorf("end: %d\n%s", code, out.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the live inbox outlived its run")
	}
	typed.Close()
}

// The engine window keeps its engine as its first pane: the inbox pane under it, focused, is
// not what Alive reads.
func TestAliveFirstPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	srv := tmux.Server{Socket: "laight-test-" + strings.ToLower(state.Short(state.NewID(time.Now())))}
	t.Cleanup(func() { srv.KillServer() })
	if err := srv.NewSession("s", tmux.Window{Name: rig.EngineWindow, Cmd: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.SplitWindow("s", rig.EngineWindow, tmux.Window{Cmd: "sleep 600"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		alive, err := srv.Alive("s", rig.EngineWindow)
		if err == nil && !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the engine exited, but Alive says %v %v", alive, err)
		}
	}
}

// syncBuffer is a bytes.Buffer a test can read while liveInbox writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, out *syncBuffer, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), want); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in:\n%s", want, out.String())
		}
	}
}
