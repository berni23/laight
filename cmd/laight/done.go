package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// cmdDone is how an agent finishes its step: `laight done <exit> [key=value…]`. The run and the
// seat come from the environment the rig gave the seat, so there are no flags to get wrong.
func cmdDone(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail(stderr, "usage: laight done <exit> [key=value…]")
	}
	runID, seat := os.Getenv("LAIGHT_RUN"), os.Getenv("LAIGHT_SEAT")
	if runID == "" || seat == "" {
		return fail(stderr, "laight done finishes a step inside a laight run; LAIGHT_RUN and LAIGHT_SEAT are not set here")
	}
	home, err := state.Home()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	r, err := state.Load(home, runID)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	h := r.ActiveOn(seat)
	if h == nil {
		return fail(stderr, "seat %s has no step running", seat)
	}
	st, exit := r.Spec.Step(h.Step), args[0]
	if !st.Allows(exit) {
		return fail(stderr, "step %s can't finish with %q; its exits: %s", st.ID, exit, strings.Join(st.AllowedExits, ", ")+", failed")
	}
	out, err := parseOut(args[1:])
	if err != nil {
		return fail(stderr, "%v", err)
	}
	err = state.WriteDone(home, runID, state.Done{Step: h.Step, Hop: h.N, Exit: exit, Out: out})
	if errors.Is(err, state.ErrAlreadyDone) {
		d, _ := state.ReadDone(home, runID, h.Step, h.N)
		if d != nil {
			return fail(stderr, "step %s already finished with %s", h.Step, d.Exit)
		}
	}
	if err != nil {
		return fail(stderr, "%v", err)
	}
	fmt.Fprintf(stdout, "step %s done: %s\n", h.Step, exit)
	return 0
}

// parseOut turns key=value arguments into a step's outputs. A value that parses as JSON is kept
// as JSON (42, true, ["a","b"]), numbers digit for digit; anything else is a string, and null is
// empty (a template would print it as "<no value>").
func parseOut(args []string) (map[string]any, error) {
	out := map[string]any{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("outputs are key=value, got %q", a)
		}
		switch j, err := spec.DecodeJSON(v); {
		case err != nil:
			out[k] = v
		case j == nil:
			out[k] = ""
		default:
			out[k] = j
		}
	}
	return out, nil
}
