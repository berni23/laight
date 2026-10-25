// Package spec parses and checks laight workflow files (.laight/workflows/*.yaml).
//
// Load runs the JSON Schema first, then resolves `uses:`, applies defaults and runs the semantic
// checks of CLAUDE.md section 3 (unknown seats, unreachable steps, routes into `needs` steps…).
package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Defaults that the schema documents.
const (
	DefaultBase        = "main"
	DefaultMaxHops     = 50
	DefaultMaxParallel = 4
	DefaultAgentExit   = "handoff"
	ExitFailed         = "failed" // implicit on every step
	ExitOK             = "ok"     // shell steps that exit 0
	End                = "end"    // route target that ends a branch
)

// Step types.
const (
	TypeAgent = "agent"
	TypeShell = "shell"
	TypeGate  = "gate"
)

// Workflow is one workflow file after `uses:` and defaults are applied.
type Workflow struct {
	Version    int              `json:"version"`
	ID         string           `json:"id"`
	Summary    string           `json:"summary"`
	Base       string           `json:"base"`
	Inputs     map[string]Input `json:"inputs"`
	Env        StringMap        `json:"env"`
	Seats      map[string]*Seat `json:"seats"`
	Steps      []*Step          `json:"steps"`
	LoopGuards LoopGuards       `json:"loop_guards"`
	Timeouts   Timeouts         `json:"timeouts"`
	OnFinish   OnFinish         `json:"on_finish"`

	// File is the path the workflow was loaded from.
	File string `json:"-"`
}

// Input is a value passed with `laight run <workflow> key=value`.
type Input struct {
	Required    bool   `json:"required"`
	Default     any    `json:"default"`
	Description string `json:"description"`
}

// Seat is one agent CLI session.
type Seat struct {
	Uses        string    `json:"uses"`
	Label       string    `json:"label"`
	Runtime     string    `json:"runtime"`
	Command     string    `json:"command"`
	Cwd         string    `json:"cwd"`
	Session     string    `json:"session"`
	Permissions string    `json:"permissions"`
	Guidance    string    `json:"guidance"`
	Model       string    `json:"model"`
	Env         StringMap `json:"env"`
	Setup       string    `json:"setup"`
}

// Step is one unit of work: an agent step, a shell command or a human gate.
type Step struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Seat         string            `json:"seat"`
	Objective    string            `json:"objective"`
	Run          string            `json:"run"`
	Exits        map[string]string `json:"exits"` // shell: exit code → exit name
	Ask          string            `json:"ask"`
	AllowedExits []string          `json:"allowed_exits"`
	NextHop      NextHop           `json:"next_hop"`
	Needs        []string          `json:"needs"`
	Matrix       map[string]any    `json:"matrix"` // a list, or a template rendering a JSON list
	MaxParallel  int               `json:"max_parallel"`
	Timeout      *Duration         `json:"timeout,omitempty"` // nil: timeouts.step; 0: never timed
	Env          StringMap         `json:"env"`
}

// Quiet is how long the step may go quiet before it is stuck (CLAUDE.md §12.2); 0 means never.
func (s *Step) Quiet() time.Duration {
	if s.Timeout == nil {
		return 0
	}
	return time.Duration(*s.Timeout)
}

// NextHop routes a finished step: On[exit] is the next step id or End.
type NextHop struct {
	On   map[string]string `json:"on"`
	Mode string            `json:"mode"` // "end" or empty
}

// LoopGuards bound a run.
type LoopGuards struct {
	MaxHops int `json:"max_hops"`
}

// Timeouts apply to every step but gates, unless the step sets its own.
type Timeouts struct {
	Step Duration `json:"step"`
}

// OnFinish says what happens to worktrees when the run ends: "destroy" or "keep".
type OnFinish struct {
	Success string `json:"success"`
	Failure string `json:"failure"`
}

// Allows reports whether exit is a valid way to finish the step.
func (s *Step) Allows(exit string) bool {
	if exit == ExitFailed {
		return true
	}
	for _, e := range s.AllowedExits {
		if e == exit {
			return true
		}
	}
	return false
}

// Step returns the step with the given id, or nil.
func (w *Workflow) Step(id string) *Step {
	for _, s := range w.Steps {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// Duration is a time.Duration written as a Go duration string ("90m").
type Duration time.Duration

// MarshalJSON implements json.Marshaler, so a run's spec snapshot reads back the same.
func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// StringMap is an env-style map whose YAML values may be strings, numbers or booleans.
type StringMap map[string]string

// UnmarshalJSON implements json.Unmarshaler.
func (m *StringMap) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber() // a float64 would round integers above 2^53 (#44)
	if err := d.Decode(&raw); err != nil {
		return err
	}
	*m = make(StringMap, len(raw))
	for k, v := range raw {
		(*m)[k] = envString(v)
	}
	return nil
}

// envString is the text an env value decoded from JSON is passed on as.
func envString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		// An integer as written; a float as plain digits, as JSON writes 1e-7 and 1e+21.
		if !strings.ContainsAny(string(v), ".eE") {
			return string(v)
		}
		if f, err := v.Float64(); err == nil {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return fmt.Sprint(v) // true, false
}
