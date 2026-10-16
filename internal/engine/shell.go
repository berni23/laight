package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// ShellLog is where a shell step's output goes, relative to the run's folder.
func ShellLog(step string, hop int) string {
	return filepath.Join("out", fmt.Sprintf("%s-%d.log", step, hop))
}

// shell starts a shell step's command in the repo and returns; a goroutine records how it ended
// when it exits, so a long command never holds up the loop. Its output goes to ShellLog, with
// every secret its env read replaced by ***. A command that can't start is an error, which the
// caller records as the hop failing.
func (e *Engine) shell(ctx context.Context, r *state.Run, h state.Hop) error {
	st := r.Spec.Step(h.Step)
	data := r.TemplateData()
	command, err := spec.Render(st.ID+".run", st.Run, data)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	env := map[string]string{}
	var secrets []string
	for _, m := range []spec.StringMap{r.Spec.Env, st.Env} {
		for k, v := range m {
			out, used, err := spec.RenderWithSecrets("env "+k, v, data)
			if err != nil {
				return fmt.Errorf("env %s: %w", k, err)
			}
			env[k], secrets = out, append(secrets, used...)
		}
	}
	env["LAIGHT_RUN"], env["LAIGHT_HOME"], env["LAIGHT_STEP"] = r.ID, e.Home, st.ID

	logName := ShellLog(st.ID, h.N)
	logFile := filepath.Join(state.Dir(e.Home, r.ID), logName)
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil { // the first step may be this one
		return err
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	fmt.Fprintf(f, "$ %s\n", command) // rendered without secrets, so safe to keep
	out := &redactor{w: f, secrets: secrets}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir, cmd.Stdout, cmd.Stderr = r.Repo, out, out
	cmd.Env = shellEnv(os.Environ(), env)
	// Its own process group, so stopping the run stops whatever the command started too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		f.Close()
		return err
	}
	e.logf("▶ %s (shell, hop %d): %s", st.ID, h.N, logName)
	e.shells.Add(1)
	go func() {
		defer e.shells.Done()
		err := cmd.Wait()
		out.Flush()
		f.Close()
		if ctx.Err() != nil {
			return // the run is stopping: the command was killed, not finished
		}
		code := -1 // killed by a signal
		var exitErr *exec.ExitError
		if err == nil {
			code = 0
		} else if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		d := state.Done{Step: st.ID, Hop: h.N, Exit: shellExit(st, code), Out: map[string]any{"code": code}, Engine: true}
		if d.Exit == spec.ExitFailed {
			d.Out["error"] = fmt.Sprintf("exit code %d, output in %s", code, logName)
			if code < 0 {
				d.Out["error"] = fmt.Sprintf("%v, output in %s", err, logName)
			}
			e.logTail(filepath.Join(state.Dir(e.Home, r.ID), logName), 5)
		}
		if err := state.WriteDone(e.Home, r.ID, d); err != nil && !errors.Is(err, state.ErrAlreadyDone) {
			e.logf("warning: %s: %v", st.ID, err)
		}
	}()
	return nil
}

// shellExit maps a shell step's exit code to its exit: `exits:` first, then 0 → ok, else failed.
func shellExit(st *spec.Step, code int) string {
	if name, ok := st.Exits[strconv.Itoa(code)]; ok && code >= 0 {
		return name
	}
	if code == 0 {
		return spec.ExitOK
	}
	return spec.ExitFailed
}

// shellEnv is base with env laid over it, minus LAIGHT_SEAT: a shell step is not a seat, so a
// `laight done` inside it must not finish some seat's step.
func shellEnv(base []string, env map[string]string) []string {
	var out []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, over := env[k]; !over && k != "LAIGHT_SEAT" {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// logTail shows the last n lines of a log in the engine window.
func (e *Engine) logTail(file string, n int) {
	b, err := os.ReadFile(file)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for _, l := range lines[max(1, len(lines)-n):] { // line 0 is the command
		e.logf("  │ %s", l)
	}
}

// redactor writes to w with every secret replaced by ***. It holds back a partial line (up to
// a limit) so a secret split across two writes is still caught.
type redactor struct {
	w       io.Writer
	secrets []string
	buf     []byte
}

const redactHold = 64 << 10

func (r *redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	cut := bytes.LastIndexByte(r.buf, '\n') + 1
	if cut == 0 && len(r.buf) > redactHold {
		cut = len(r.buf) - redactHold/2
	}
	if cut > 0 {
		if _, err := r.w.Write(r.redact(r.buf[:cut])); err != nil {
			return 0, err
		}
		r.buf = append(r.buf[:0], r.buf[cut:]...)
	}
	return len(p), nil
}

// Flush writes what is held back.
func (r *redactor) Flush() {
	if len(r.buf) > 0 {
		r.w.Write(r.redact(r.buf))
		r.buf = r.buf[:0]
	}
}

func (r *redactor) redact(b []byte) []byte {
	for _, s := range r.secrets {
		b = bytes.ReplaceAll(b, []byte(s), []byte("***"))
	}
	return b
}
