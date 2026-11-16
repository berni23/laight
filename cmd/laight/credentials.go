package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/berni23/laight/internal/engine"
	"github.com/berni23/laight/internal/spec"
)

// loginCmd runs a credential's login in the user's terminal. A variable, so tests can stand in.
var loginCmd = func(dir, command string) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// preflight checks what a run needs access to before its rig starts (CLAUDE.md §6.1): every
// secret its env names, then every credential's check. A check that fails runs its login, when
// it has one and there is a terminal to log in from (the one human moment, at the start), and is
// checked again. Any that still fails keeps the run from starting. When ctx ends (Ctrl-C), the
// check under way is stopped and so is the preflight.
func preflight(ctx context.Context, w *spec.Workflow, repo string, canLogin bool, stdout io.Writer) error {
	names := slices.Sorted(maps.Keys(w.Credentials))
	secrets := w.SecretNames()
	if len(names) == 0 && len(secrets) == 0 {
		return nil
	}
	fmt.Fprintln(stdout, "preflight:")
	var errs []error
	for _, s := range secrets {
		if _, err := spec.Secret(s); err != nil {
			fmt.Fprintf(stdout, "  ✕ secret %s\n", s)
			errs = append(errs, err)
			continue
		}
		fmt.Fprintf(stdout, "  ✓ secret %s\n", s)
	}
	for _, name := range names {
		c := w.Credentials[name]
		err := engine.CheckCredential(ctx, repo, c)
		if ctx.Err() != nil {
			return fmt.Errorf("preflight interrupted at credential %s", name)
		}
		if err != nil && c.Login != "" && canLogin {
			fmt.Fprintf(stdout, "  ✕ credential %s: %v\n    logging in: %s\n", name, err, c.Login)
			if lerr := loginCmd(repo, c.Login); lerr != nil {
				fmt.Fprintf(stdout, "    login: %v\n", lerr)
			}
			if ctx.Err() == nil {
				err = engine.CheckCredential(ctx, repo, c)
			}
			if ctx.Err() != nil {
				return fmt.Errorf("preflight interrupted at credential %s", name)
			}
		}
		if err != nil {
			fmt.Fprintf(stdout, "  ✕ credential %s: %v\n", name, err)
			hint := ""
			switch {
			case c.Login != "" && !canLogin:
				hint = fmt.Sprintf("; log in first (no terminal here): %s", c.Login)
			case c.Login == "":
				hint = fmt.Sprintf("; it has no login: make `%s` pass", c.Check)
			}
			errs = append(errs, fmt.Errorf("credential %s fails its check (%v)%s", name, err, hint))
			continue
		}
		fmt.Fprintf(stdout, "  ✓ credential %s\n", name)
	}
	return errors.Join(errs...)
}
