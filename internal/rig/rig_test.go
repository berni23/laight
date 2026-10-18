package rig

import (
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// After a reboot the pgid in run.json may be any process of the user's: Down must not signal
// it. A group started since the boot is still stopped.
func TestDownSparesGroupsFromBeforeBoot(t *testing.T) {
	home := t.TempDir()
	group := func() (*exec.Cmd, <-chan error) {
		c := exec.Command("sleep", "300")
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Process.Kill() })
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		return c, done
	}
	stranger, strangerDone := group()
	ours, oursDone := group()
	r := &state.Run{
		ID: state.NewID(time.Now()), Workflow: "demo", Status: state.Running, Session: "laight-demo-x",
		Spec: &spec.Workflow{OnFinish: spec.OnFinish{Success: "destroy", Failure: "keep"}},
		Active: []*state.Hop{
			{Step: "before", N: 1, Pgid: stranger.Process.Pid}, // started before the boot
			{Step: "since", N: 2, Pgid: ours.Process.Pid, Started: time.Now()},
		},
	}
	if err := state.Create(home, r); err != nil {
		t.Fatal(err)
	}
	if hops := Groups(r); len(hops) != 1 || hops[0].Step != "since" {
		t.Errorf("groups = %+v", hops)
	}
	if err := New(home, "laight", io.Discard).Down(r.ID, state.Aborted, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oursDone:
	case <-time.After(10 * time.Second):
		t.Error("the run's own group was not stopped")
	}
	select {
	case err := <-strangerDone:
		t.Errorf("a group from before the boot was signalled: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
