package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berni23/laight/internal/spec"
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
			err := preflight(w, repo, c.terminal, &out)
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
	err := preflight(w, repo, true, &out)
	if err == nil || !strings.Contains(err.Error(), `secret "laight-test-no-such-secret" not found`) ||
		!strings.Contains(out.String(), "✓ secret laight-test-found") || !strings.Contains(out.String(), "✕ secret laight-test-no-such-secret") {
		t.Errorf("secrets: %v\n%s", err, out.String())
	}
	if err := preflight(&spec.Workflow{}, repo, true, &out); err != nil {
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
