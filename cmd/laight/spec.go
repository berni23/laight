package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/berni23/laight/internal/scaffold"
	"github.com/berni23/laight/internal/spec"
)

// cmdInit creates .laight/ at the root of the current git repo.
func cmdInit(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		return fail(stderr, "usage: laight init")
	}

	root, err := gitRoot(".")
	if err != nil {
		return fail(stderr, "init needs a git repository (worktrees are how runs stay isolated): %v", err)
	}
	dir := filepath.Join(root, spec.DirName)
	if _, err := os.Stat(dir); err == nil {
		return fail(stderr, "%s already exists; nothing changed", dir)
	}
	base := defaultBranch(root)

	files, err := scaffold.Files(base)
	if err != nil {
		return fail(stderr, "%v", err)
	}

	// Write everything into a temporary folder and rename it into place, so a failure never
	// leaves a half-written .laight/ behind.
	tmp, err := os.MkdirTemp(root, ".laight-init-")
	if err != nil {
		return fail(stderr, "%v", err)
	}
	defer os.RemoveAll(tmp)
	for _, name := range sortedKeys(files) {
		p := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fail(stderr, "%v", err)
		}
		if err := os.WriteFile(p, files[name], 0o644); err != nil {
			return fail(stderr, "%v", err)
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fail(stderr, "%v", err)
	}

	fmt.Fprintf(stdout, "created %s\n", dir)
	for _, name := range sortedKeys(files) {
		fmt.Fprintf(stdout, "  %s\n", name)
	}
	fmt.Fprintln(stdout)
	if code := validate(&spec.Project{Root: root, Dir: dir}, nil, stdout, stderr); code != 0 {
		return code
	}
	fmt.Fprintln(stdout, "\nNext: edit the workflow and guidance under .laight/, then `laight validate`.")
	return 0
}

// cmdValidate checks the named workflows, or all of the project's.
func cmdValidate(args []string, stdout, stderr io.Writer) int {
	p, err := spec.FindProject(".")
	if err != nil {
		return fail(stderr, "%v", err)
	}
	return validate(p, args, stdout, stderr)
}

func validate(p *spec.Project, names []string, stdout, stderr io.Writer) int {
	var files []string
	if len(names) == 0 {
		all, err := p.Workflows()
		if err != nil {
			return fail(stderr, "%v", err)
		}
		if len(all) == 0 {
			return fail(stderr, "no workflows in %s", filepath.Join(p.Dir, "workflows"))
		}
		files = all
	}
	for _, n := range names {
		f, err := p.WorkflowFile(n)
		if err != nil {
			return fail(stderr, "%v", err)
		}
		files = append(files, f)
	}
	cwd, _ := os.Getwd()
	code := 0
	for _, f := range files {
		w, probs := spec.Load(p.Dir, f)
		for _, pr := range probs {
			if rel, err := filepath.Rel(cwd, pr.File); err == nil && !strings.HasPrefix(rel, "..") {
				pr.File = rel
			}
			fmt.Fprintln(stdout, pr)
		}
		name := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		switch {
		case spec.HasErrors(probs) || w == nil:
			fmt.Fprintf(stdout, "✕ %s\n", name)
			code = 1
		default:
			fmt.Fprintf(stdout, "✓ %s: %d steps, %d seats\n", w.ID, len(w.Steps), len(w.Seats))
		}
	}
	return code
}

func gitRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// defaultBranch is origin's default branch, else the current branch, else main.
func defaultBranch(root string) string {
	if out, err := exec.Command("git", "-C", root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD").Output(); err == nil {
		if b := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); b != "" {
			return b
		}
	}
	if out, err := exec.Command("git", "-C", root, "symbolic-ref", "--quiet", "--short", "HEAD").Output(); err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return b
		}
	}
	return spec.DefaultBase
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
