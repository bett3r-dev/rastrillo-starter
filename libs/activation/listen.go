// Package activation is the process side of ADR 0006's scale to zero:
// take the listener systemd hands over on a socket-activated start, and
// tell the process when it has been idle long enough to exit, so the
// next connection on the socket starts it again.
//
// It is the systemd protocol (sd_listen_fds(3)) and an in-flight
// counter, in the standard library only.
package activation

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// firstFD is SD_LISTEN_FDS_START: systemd passes its sockets from fd 3.
const firstFD = 3

// Listeners returns the sockets systemd passed this process, in order,
// or nil when it was not socket-activated (LISTEN_FDS unset, zero, or
// LISTEN_PID naming another process), so the caller falls back to its
// own address.
//
// LISTEN_PID must equal this process's pid, as sd_listen_fds requires:
// the variables are inherited by children, and a child that took them
// for its own would steal its parent's sockets. LISTEN_FDS,
// LISTEN_PID and LISTEN_FDNAMES are unset after reading, whatever the
// outcome, for the same reason. Each fd is marked close-on-exec and
// closed once net.FileListener has its own duplicate.
//
// Call it once, early: a second call finds the environment empty.
func Listeners() ([]net.Listener, error) {
	ls, _, err := listeners(os.Getenv, os.Unsetenv, os.Getpid())
	return ls, err
}

// Named is Listeners keyed by LISTEN_FDNAMES (the unit's
// FileDescriptorName=, "unknown" when systemd gives none). Two sockets
// under one name keep the first.
func Named() (map[string]net.Listener, error) {
	ls, names, err := listeners(os.Getenv, os.Unsetenv, os.Getpid())
	if ls == nil || err != nil {
		return nil, err
	}
	m := make(map[string]net.Listener, len(ls))
	for i, l := range ls {
		if _, dup := m[names[i]]; !dup {
			m[names[i]] = l
		}
	}
	return m, nil
}

// Listener is the one-socket case: the first socket systemd passed, or
// nil when the process was not socket-activated. More than one is an
// error, since the rest would be held open and never served.
func Listener() (net.Listener, error) {
	ls, err := Listeners()
	if err != nil || ls == nil {
		return nil, err
	}
	if len(ls) > 1 {
		for _, l := range ls {
			l.Close()
		}
		return nil, fmt.Errorf("activation: %d sockets passed, want 1", len(ls))
	}
	return ls[0], nil
}

// listeners is Listeners with the environment and the pid injected,
// so the refusals are testable in-process. The fds themselves are
// real: activation_test.go re-execs the test binary to put one at 3.
func listeners(getenv func(string) string, unsetenv func(string) error, pid int) ([]net.Listener, []string, error) {
	fds, pidVar, namesVar := getenv("LISTEN_FDS"), getenv("LISTEN_PID"), getenv("LISTEN_FDNAMES")
	for _, k := range []string{"LISTEN_FDS", "LISTEN_PID", "LISTEN_FDNAMES"} {
		unsetenv(k)
	}
	if fds == "" || pidVar != strconv.Itoa(pid) {
		return nil, nil, nil
	}
	n, err := strconv.Atoi(fds)
	if err != nil || n < 0 {
		return nil, nil, fmt.Errorf("activation: LISTEN_FDS=%q is not a count", fds)
	}
	if n == 0 {
		return nil, nil, nil
	}
	names := strings.Split(namesVar, ":")
	if namesVar == "" || len(names) != n {
		names = make([]string, n)
		for i := range names {
			names[i] = "unknown"
		}
	}
	ls := make([]net.Listener, 0, n)
	for i := 0; i < n; i++ {
		fd := firstFD + i
		syscall.CloseOnExec(fd)
		f := os.NewFile(uintptr(fd), "LISTEN_FD_"+strconv.Itoa(fd))
		l, err := net.FileListener(f)
		f.Close()
		if err != nil {
			for _, l := range ls {
				l.Close()
			}
			return nil, nil, fmt.Errorf("activation: fd %d (%s): %w", fd, names[i], err)
		}
		ls = append(ls, l)
	}
	return ls, names, nil
}
