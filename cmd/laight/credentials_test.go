package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// The preflight (#62): a failing check with a login runs it (in the terminal) and checks again;
// without a terminal, or without a login, or still failing, the run doesn't start. Secrets the
// env reads are looked up too.
func TestPreflight(t *testing.T) {
	repo := t.TempDir()
	session := filepath.Join(t.TempDir(), "session")
	var logins []string
	defer func(f func(string, string) error) { loginCmd = f }(loginCmd)
	loginCmd = func(dir, command string) error {
		logins = append(logins, command)
		if command == "fix" {
			return os.WriteFile(session, nil, 0o600)
		}
		return nil
	}
	aws := spec.Credential{Check: "test -f " + session + " || { echo 'Token has expired' >&2; exit 255; }", Login: "fix"}
	for _, c := range []struct {
		name     string
		cred     spec.Credential
		terminal bool
		logins   int
		err      string // "" means the run may start
	}{
		{"login fixes it", aws, true, 1, ""},
		{"no terminal", aws, false, 0, "log in first (no terminal here): fix"},
		{"login doesn't help", spec.Credential{Check: aws.Check, Login: "noop"}, true, 1, "credential aws fails its check (exit 255: Token has expired)"},
		{"no login", spec.Credential{Check: aws.Check}, true, 0, "it has no login"},
	} {
		t.Run(c.name, func(t *testing.T) {
			os.Remove(session)
			logins = nil
			w := &spec.Workflow{Credentials: map[string]spec.Credential{"aws": c.cred, "gh": {Check: "true"}}}
			var out bytes.Buffer
			err := preflight(context.Background(), w, repo, c.terminal, &out)
			if len(logins) != c.logins {
				t.Errorf("logins %q", logins)
			}
			if (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
				t.Errorf("err %v, want %q\n%s", err, c.err, out.String())
			}
			if !strings.Contains(out.String(), "✓ credential gh") {
				t.Errorf("output:\n%s", out.String())
			}
		})
	}

	t.Setenv("LAIGHT_SECRET_LAIGHT_TEST_FOUND", "x")
	w := &spec.Workflow{Env: spec.StringMap{"A": `{{ secret "laight-test-found" }}`, "B": `{{ secret "laight-test-no-such-secret" }}`}}
	var out bytes.Buffer
	err := preflight(context.Background(), w, repo, true, &out)
	if err == nil || !strings.Contains(err.Error(), `secret "laight-test-no-such-secret" not found`) ||
		!strings.Contains(out.String(), "✓ secret laight-test-found") || !strings.Contains(out.String(), "✕ secret laight-test-no-such-secret") {
		t.Errorf("secrets: %v\n%s", err, out.String())
	}
	if err := preflight(context.Background(), &spec.Workflow{}, repo, true, &out); err != nil {
		t.Errorf("nothing to check: %v", err)
	}
}

// `laight run` stops before the rig when a credential fails: no run, no tmux session.
func TestRunRefusedByPreflight(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := gitRepo(t)
	home := t.TempDir()
	t.Setenv("LAIGHT_HOME", home)
	runOK(t, "init")
	wf := filepath.Join(dir, ".laight", "workflows", "issue-cycle.yaml")
	b, err := os.ReadFile(wf)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(b), "\nseats:", "\ncredentials:\n  vault: { check: echo sealed >&2; exit 2 }\nseats:", 1)
	if src == string(b) {
		t.Fatal("no seats: in the scaffold")
	}
	if err := os.WriteFile(wf, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if code := run([]string{"run", "issue-cycle", "issue=1"}, &o, &e); code != 1 ||
		!strings.Contains(e.String(), "credential vault fails its check (exit 2: sealed)") || !strings.Contains(e.String(), "the run didn't start") {
		t.Fatalf("run = %d\nstdout: %s\nstderr: %s", code, o.String(), e.String())
	}
	if runs, _ := filepath.Glob(filepath.Join(home, "runs", "*")); len(runs) > 0 {
		t.Errorf("a run was created: %v", runs)
	}
}

// A seat whose secret can't be found at rig start (here named by an expression, which the
// preflight can't look up) fails the run as credential, naming the seat, its reason scrubbed.
func TestEngineSeatSecretMissing(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a:
    runtime: shell
    command: sh
    cwd: repo
    env: { TOK: '{{ secret (print "laight-test-no-" "such-secret") }}' }
steps:
  - { id: one, seat: a, objective: '"$L" done handoff' }
`))
	e.mustLaight("run", "demo")
	r := e.waitFailed(e.onlyRun().ID, state.KindCredential)
	if r.Failure.Seat != "a" || !strings.Contains(r.Failure.Reason, `secret "laight-test-no-such-secret" not found`) {
		t.Errorf("failure %+v", r.Failure)
	}
}

// Ctrl-C during a check stops the check and the preflight at once; nothing is left running.
func TestPreflightInterrupted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	w := &spec.Workflow{Credentials: map[string]spec.Credential{"slow": {Check: "echo $$ > " + pidFile + "; exec sleep 30"}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- preflight(ctx, w, t.TempDir(), true, &bytes.Buffer{}) }()
	for range 100 {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "interrupted") {
			t.Errorf("err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the preflight kept waiting for the check")
	}
	waitGone(t, readPid(t, pidFile))
}

// The engine runs with the environment `laight run` saw (and the preflight checked), even when
// laight's tmux server was started from another shell: a secret exported for this run reaches
// its seats.
func TestEngineGetsRunEnv(t *testing.T) {
	e := newRigEnv(t, "")
	// The server is already up, started without the secret.
	if err := e.srv.NewSession("other", tmux.Window{Name: "x", Cmd: "sleep 600"}); err != nil {
		t.Fatal(err)
	}
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo, env: { TOK: '{{ secret "laight-test-run-env" }}' } }
steps:
  - { id: one, seat: a, objective: 'test "$TOK" = from-this-shell && "$L" done handoff || "$L" done failed' }
`))
	c := exec.Command(e.bin, "run", "demo")
	c.Dir = e.repo
	c.Env = append(os.Environ(), "LAIGHT_HOME="+e.home, "LAIGHT_SECRET_LAIGHT_TEST_RUN_ENV=from-this-shell", "BAD-NAME=x")
	if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "✓ secret laight-test-run-env") {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if r := e.waitEnded(e.onlyRun().ID); r.Status != state.Succeeded {
		t.Fatalf("status %s, failure %+v", r.Status, r.Failure)
	}
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, e.onlyRun().ID), "engine.env")); !os.IsNotExist(err) {
		t.Errorf("engine.env left behind: %v", err)
	}
}

// The engine's env file loads in sh whatever `laight run` inherited: a name bash's sh holds
// read-only would abort it, and the engine window with it.
func TestEngineEnvLoads(t *testing.T) {
	env := engineEnv([]string{"SHELLOPTS=braceexpand", "BASHOPTS=x", "UID=1", "EUID=1", "PPID=1", "FOO=bar", "BAD-NAME=x", "TMUX=/s,1,0"}, "/h")
	file := filepath.Join(t.TempDir(), "engine.env")
	if err := tmux.WriteEnv(file, env); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", tmux.EnvCmd(file, `echo "$FOO $LAIGHT_HOME"`)).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "bar /h" {
		t.Fatalf("env file didn't load: %v\n%s", err, out)
	}
}
