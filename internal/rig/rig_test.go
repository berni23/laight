package rig

import (
	"io"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
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

// An engine tick runs under the run's lock and may raise an item while Down tears the run down.
// Down withdraws the run's items only once its final status is recorded under that lock, after
// which no tick acts: none is left behind.
func TestDownWithdrawsLateItems(t *testing.T) {
	home := t.TempDir()
	r := &state.Run{ID: state.NewID(time.Now()), Workflow: "demo", Status: state.Running, Session: "laight-demo-x",
		Spec: &spec.Workflow{OnFinish: spec.OnFinish{Success: "destroy", Failure: "keep"}}}
	if err := state.Create(home, r); err != nil {
		t.Fatal(err)
	}
	locked, release, ticked := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := state.Update(home, r.ID, func(r *state.Run) error {
			close(locked)
			<-release
			return inbox.Put(home, inbox.Item{ID: state.NewID(time.Now()), Run: r.ID, Kind: inbox.Failed, Title: "late"})
		})
		ticked <- err
	}()
	<-locked
	down := make(chan error, 1)
	go func() { down <- New(home, "laight", io.Discard).Down(r.ID, state.Aborted, "") }()
	time.Sleep(300 * time.Millisecond) // Down is now waiting for the lock
	close(release)
	if err := <-ticked; err != nil {
		t.Fatal(err)
	}
	if err := <-down; err != nil {
		t.Fatal(err)
	}
	if items, _ := inbox.Pending(home); len(items) != 0 {
		t.Errorf("left behind: %+v", items[0])
	}
}
