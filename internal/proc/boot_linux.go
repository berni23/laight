package proc

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// BootTime is when the machine last booted: the btime line of /proc/stat.
func BootTime() (time.Time, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			s, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Unix(s, 0), err
		}
	}
	return time.Time{}, errors.New("/proc/stat has no btime")
}
