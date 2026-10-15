package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{args: []string{"version"}, code: 0, inStdout: "laight dev"},
		{args: []string{"help"}, code: 0, inStdout: "Usage:"},
		{args: nil, code: 2, inStderr: "Usage:"},
		{args: []string{"nope"}, code: 2, inStderr: `unknown command "nope"`},
		{args: []string{"init", "--from"}, code: 1, inStderr: "usage: laight init"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != tt.code {
			t.Errorf("run(%q) = %d, want %d", tt.args, code, tt.code)
		}
		if !strings.Contains(out.String(), tt.inStdout) {
			t.Errorf("run(%q) stdout = %q, want it to contain %q", tt.args, out.String(), tt.inStdout)
		}
		if !strings.Contains(errOut.String(), tt.inStderr) {
			t.Errorf("run(%q) stderr = %q, want it to contain %q", tt.args, errOut.String(), tt.inStderr)
		}
	}
}

// gitRepo makes an empty git repo on branch main and changes into it.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(dir)
	return dir
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 {
		t.Fatalf("laight %s = %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out.String(), errOut.String())
	}
	return out.String()
}

func TestInitValidate(t *testing.T) {
	dir := gitRepo(t)
	out := runOK(t, "init")
	if !strings.Contains(out, "✓ issue-cycle: 6 steps, 2 seats") {
		t.Errorf("init output:\n%s", out)
	}
	wf := filepath.Join(dir, ".laight", "workflows", "issue-cycle.yaml")
	b, err := os.ReadFile(wf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "base: main") {
		t.Error("base not filled in")
	}

	// A second init changes nothing.
	var o, e bytes.Buffer
	if code := run([]string{"init"}, &o, &e); code != 1 || !strings.Contains(e.String(), "already exists") {
		t.Errorf("second init = %d, %q", code, e.String())
	}

	// validate works from a subfolder, by id and for everything.
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	runOK(t, "validate")
	runOK(t, "validate", "issue-cycle")

	// A broken workflow fails with file, line and the reason.
	broken := strings.Replace(string(b), "pass: approve", "pass: aprove", 1)
	if err := os.WriteFile(wf, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Reset()
	e.Reset()
	code := run([]string{"validate"}, &o, &e)
	if code != 1 || !regexpMatch(`issue-cycle\.yaml:\d+: error: steps\[3\]\.next_hop\.on\.pass: unknown step "aprove"`, o.String()) {
		t.Errorf("validate = %d\n%s%s", code, o.String(), e.String())
	}

	o.Reset()
	e.Reset()
	if code := run([]string{"validate", "nope"}, &o, &e); code != 1 || !strings.Contains(e.String(), "workflows: issue-cycle") {
		t.Errorf("validate nope = %d, %q", code, e.String())
	}
}

func TestInitFromOpenRig(t *testing.T) {
	src, err := filepath.Abs("../../internal/openrig/testdata/openrig")
	if err != nil {
		t.Fatal(err)
	}
	dir := gitRepo(t)
	out := runOK(t, "init", "--from", "openrig", src)
	for _, want := range []string{"workflows/cycle.yaml", "Check these:", "✓ cycle"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".laight", "guidance", "impl.md")); err != nil {
		t.Error(err)
	}
	// No temporary folder left behind.
	if m, _ := filepath.Glob(filepath.Join(dir, ".laight-init-*")); len(m) > 0 {
		t.Errorf("left behind: %v", m)
	}
}

func TestInitOutsideGit(t *testing.T) {
	t.Chdir(t.TempDir())
	var o, e bytes.Buffer
	if code := run([]string{"init"}, &o, &e); code != 1 || !strings.Contains(e.String(), "git repository") {
		t.Errorf("init outside git = %d, %q", code, e.String())
	}
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }
