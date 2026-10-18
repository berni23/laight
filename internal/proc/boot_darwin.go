package proc

import (
	"time"

	"golang.org/x/sys/unix"
)

// BootTime is when the machine last booted.
func BootTime() (time.Time, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(tv.Unix()), nil
}
