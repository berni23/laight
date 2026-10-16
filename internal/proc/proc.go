// Package proc stops process groups: a shell step's command and whatever it started.
package proc

import (
	"syscall"
	"time"
)

// StopGroup sends SIGTERM to process group pgid, then SIGKILL to whatever is left of it after
// grace. A group that is already gone is not an error.
func StopGroup(pgid int, grace time.Duration) {
	if pgid <= 0 || syscall.Kill(-pgid, syscall.SIGTERM) != nil {
		return
	}
	for deadline := time.Now().Add(grace); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
}
