// Package tmux drives laight's own tmux server through the tmux CLI.
//
// Every call goes to one named server socket (`tmux -L <socket>`), so the user's own tmux sessions
// are never listed, touched or killed.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Server is one tmux server, addressed by its socket name.
type Server struct {
	Socket string
}

// Window says how to start a tmux window. Its environment is set by Cmd itself (see EnvCmd):
// `-e` needs tmux 3.2, and values on the tmux client's argv would show in `ps`.
type Window struct {
	Name string
	Dir  string
	Cmd  string // a shell command line; empty starts the default shell
}

// Version returns the installed tmux version string, e.g. "tmux 3.4".
func Version() (string, error) {
	out, err := exec.Command("tmux", "-V").Output()
	if err != nil {
		return "", fmt.Errorf("tmux not found: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s Server) cmd(args ...string) *exec.Cmd {
	c := exec.Command("tmux", append([]string{"-L", s.Socket}, args...)...)
	// Run the client outside any tmux the caller is in, so tmux never refuses to nest.
	c.Env = withoutTMUX(os.Environ())
	return c
}

func (s Server) run(args ...string) (string, error) {
	var stderr bytes.Buffer
	c := s.cmd(args...)
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("tmux %s: %s", args[0], msg)
	}
	return string(out), nil
}

// HasSession reports whether the session exists.
func (s Server) HasSession(name string) bool {
	return s.cmd("has-session", "-t", "="+name).Run() == nil
}

// NewSession starts a detached session whose first window is w. Panes stay open after their
// command exits (remain-on-exit), so a crash leaves its last output on screen.
func (s Server) NewSession(name string, w Window) error {
	// remain-on-exit is set in the same client call, before the window's command can exit.
	// The server is laight's own, so a global window option touches nothing of the user's.
	args := []string{"start-server", ";", "set-option", "-g", "-w", "remain-on-exit", "on", ";",
		"new-session", "-d", "-s", name, "-n", w.Name, "-x", "220", "-y", "50"}
	_, err := s.run(append(args, windowArgs(w)...)...)
	return err
}

// NewWindow adds w to the session.
func (s Server) NewWindow(session string, w Window) error {
	args := []string{"new-window", "-d", "-t", "=" + session + ":", "-n", w.Name}
	_, err := s.run(append(args, windowArgs(w)...)...)
	return err
}

func windowArgs(w Window) []string {
	var args []string
	if w.Dir != "" {
		args = append(args, "-c", w.Dir)
	}
	if w.Cmd != "" {
		args = append(args, w.Cmd)
	}
	return args
}

// EnvCmd returns a command line that exports the variables in envFile (written by WriteEnv),
// deletes the file and then runs cmd (the user's shell when cmd is empty). It runs under sh,
// whatever the user's default shell is.
func EnvCmd(envFile, cmd string) string {
	if cmd == "" {
		cmd = `"${SHELL:-sh}"`
	}
	f := Quote(envFile)
	return Join([]string{"sh", "-c", "set -a && . " + f + " && set +a && rm -f " + f + " && exec " + cmd})
}

// WriteEnv writes env as an sh script readable only by the user, for EnvCmd.
func WriteEnv(file string, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, Quote(env[k]))
	}
	return os.WriteFile(file, []byte(b.String()), 0o600)
}

// CurrentWindow returns the name of the window of session that this process runs in, or "" when
// it doesn't run inside that session of this server.
func (s Server) CurrentWindow(session string) string {
	pane, sock := os.Getenv("TMUX_PANE"), os.Getenv("TMUX")
	sockPath, _, _ := strings.Cut(sock, ",")
	if pane == "" || sockPath == "" || filepath.Base(sockPath) != s.Socket {
		return ""
	}
	out, err := s.run("display-message", "-p", "-t", pane, "#{session_name}\t#{window_name}")
	if err != nil {
		return ""
	}
	sess, win, _ := strings.Cut(strings.TrimSpace(out), "\t")
	if sess != session {
		return ""
	}
	return win
}

// target is session:window, matched exactly.
func target(session, window string) string { return "=" + session + ":" + window }

// KillWindow stops one window and its process. A missing window is not an error.
func (s Server) KillWindow(session, window string) error {
	if !s.HasWindow(session, window) {
		return nil
	}
	_, err := s.run("kill-window", "-t", target(session, window))
	return err
}

// KillSession stops the session and every process in it. A missing session is not an error.
func (s Server) KillSession(name string) error {
	if !s.HasSession(name) {
		return nil
	}
	_, err := s.run("kill-session", "-t", "="+name)
	return err
}

// Windows lists the session's window names.
func (s Server) Windows(session string) ([]string, error) {
	out, err := s.run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// HasWindow reports whether the session has a window with that name.
func (s Server) HasWindow(session, window string) bool {
	ws, err := s.Windows(session)
	if err != nil {
		return false
	}
	for _, w := range ws {
		if w == window {
			return true
		}
	}
	return false
}

// PaneDead reports whether the window's command has exited (its pane is kept by remain-on-exit).
func (s Server) PaneDead(session, window string) bool {
	out, err := s.run("display-message", "-p", "-t", target(session, window), "#{pane_dead}")
	return err == nil && strings.TrimSpace(out) == "1"
}

// ErrGone is returned by Alive when the window, its session or the whole server no longer exists.
var ErrGone = errors.New("window gone")

// Alive reports whether the window's command is still running. It returns ErrGone when the
// window doesn't exist any more, and any other error when tmux couldn't say: a caller deciding
// that an agent crashed must not mistake one for the other.
func (s Server) Alive(session, window string) (bool, error) {
	out, err := s.run("list-windows", "-t", "="+session, "-F", "#{window_name}\t#{pane_dead}")
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "can't find session") || strings.Contains(msg, "no server running") ||
			strings.Contains(msg, "error connecting to") {
			return false, ErrGone
		}
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if name, dead, _ := strings.Cut(line, "\t"); name == window {
			return dead != "1", nil
		}
	}
	return false, ErrGone
}

// Capture returns the visible text of the window's pane.
func (s Server) Capture(session, window string) (string, error) {
	return s.run("capture-pane", "-p", "-J", "-t", target(session, window))
}

// PipeTo appends everything the window's pane prints to file.
func (s Server) PipeTo(session, window, file string) error {
	_, err := s.run("pipe-pane", "-o", "-t", target(session, window), "cat >> "+Quote(file))
	return err
}

// SendKeys sends tmux key names (Enter, Down, "2"…) to the window. For text, use Paste.
func (s Server) SendKeys(session, window string, keys ...string) error {
	_, err := s.run(append([]string{"send-keys", "-t", target(session, window)}, keys...)...)
	return err
}

// Paste types text into the window as one bracketed paste, then presses Enter.
func (s Server) Paste(session, window, text string) error {
	buf := "laight-" + session + "-" + window
	c := s.cmd("load-buffer", "-b", buf, "-")
	c.Stdin = strings.NewReader(text)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %s", strings.TrimSpace(string(out)))
	}
	if _, err := s.run("paste-buffer", "-p", "-d", "-b", buf, "-t", target(session, window)); err != nil {
		return err
	}
	return s.SendKeys(session, window, "Enter")
}

// AttachCmd returns the command that attaches the terminal to the session (at window, if set).
// Inside the user's own tmux it nests, since laight's sessions live on another server.
func (s Server) AttachCmd(session, window string) *exec.Cmd {
	t := "=" + session
	if window != "" {
		t = target(session, window)
	}
	c := s.cmd("attach-session", "-t", t)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c
}

// SelectWindow makes window the session's current one.
func (s Server) SelectWindow(session, window string) error {
	_, err := s.run("select-window", "-t", target(session, window))
	return err
}

// KillServer stops the whole server and removes its socket file. Only tests use it, on their
// own throwaway socket.
func (s Server) KillServer() error {
	err := s.cmd("kill-server").Run()
	// Where tmux puts -L sockets; the file outlives the server.
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	os.Remove(fmt.Sprintf("%s/tmux-%d/%s", dir, os.Getuid(), s.Socket))
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil // no server running
	}
	return err
}

// Quote quotes s for a POSIX shell (tmux runs window commands with the default shell).
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@,+%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join quotes each argument and joins them into one command line.
func Join(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

func withoutTMUX(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			out = append(out, e)
		}
	}
	return out
}
