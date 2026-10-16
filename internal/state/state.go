// Package state is laight's on-disk state under ~/.laight: runs (run.json, events.jsonl) and the
// registered projects. Everything is a plain file; writes are atomic (temp file + rename), and
// run.json updates hold an flock so the engine and `laight abort` never interleave.
package state

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/berni23/laight/internal/spec"
)

// Run statuses. Starting and Running are live; the others are final.
const (
	Starting  = "starting"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
	Aborted   = "aborted"
)

// Run is run.json: everything the engine needs to continue a run, and nothing else.
type Run struct {
	ID       string            `json:"id"`
	Workflow string            `json:"workflow"`
	Repo     string            `json:"repo"`    // the project root
	Session  string            `json:"session"` // tmux session on laight's server
	Inputs   map[string]string `json:"inputs"`
	Spec     *spec.Workflow    `json:"spec"`     // snapshot taken at `laight run`
	SpecDir  string            `json:"spec_dir"` // the project's .laight folder, for guidance files
	Base     string            `json:"base_ref"` // the ref worktrees start from, resolved once
	Status   string            `json:"status"`
	Error    string            `json:"error,omitempty"`
	Seats    map[string]*Seat  `json:"seats"`
	Created  time.Time         `json:"created"`
	Finished *time.Time        `json:"finished,omitempty"`

	// The engine's progress.
	Hops   int                    `json:"hops"`             // step starts so far (loop_guards.max_hops)
	Queue  []string               `json:"queue,omitempty"`  // steps routed to, waiting for their seat
	Active []*Hop                 `json:"active,omitempty"` // steps running now
	Steps  map[string]*StepResult `json:"steps,omitempty"`  // the latest finished hop of each step
}

// Hop is one start of a step.
type Hop struct {
	Step    string    `json:"step"`
	Seat    string    `json:"seat,omitempty"`
	N       int       `json:"hop"`
	Started time.Time `json:"started"`
}

// StepResult is how a step's latest hop finished: what templates see as .steps.<id>.
type StepResult struct {
	Hop  int            `json:"hop"`
	Exit string         `json:"exit"`
	Out  map[string]any `json:"out"`
}

// Seat is one seat's live state.
type Seat struct {
	Window    string `json:"window"`
	Dir       string `json:"dir"`                // where the agent works
	Worktree  bool   `json:"worktree,omitempty"` // Dir is a worktree laight created and owns
	SessionID string `json:"session_id,omitempty"`
	SetupDone bool   `json:"setup_done,omitempty"` // the seat's setup succeeded in Dir
	Up        bool   `json:"up"`

	Step  string `json:"step,omitempty"`  // the step last given to the seat…
	Hop   int    `json:"hop,omitempty"`   // …and its hop
	Turn  bool   `json:"turn,omitempty"`  // the agent's turn on it hasn't ended yet
	Given int    `json:"given,omitempty"` // steps given to the seat in this run
}

// ActiveOn returns the hop running on seat, or nil.
func (r *Run) ActiveOn(seat string) *Hop {
	for _, h := range r.Active {
		if h.Seat == seat {
			return h
		}
	}
	return nil
}

// TemplateData is what workflow templates see: .inputs, .steps, .matrix, .base and .run.
func (r *Run) TemplateData() map[string]any {
	inputs := map[string]any{}
	for k, v := range r.Inputs {
		inputs[k] = v
	}
	steps := map[string]any{}
	for id, res := range r.Steps {
		out := res.Out
		if out == nil {
			out = map[string]any{}
		}
		steps[id] = map[string]any{"exit": res.Exit, "out": out}
	}
	return map[string]any{
		"inputs": inputs, "steps": steps, "matrix": map[string]any{}, "base": r.Spec.Base,
		"run": map[string]any{"id": r.ID, "project": filepath.Base(r.Repo), "workflow": r.Workflow},
	}
}

// Live reports whether the run hasn't reached a final status.
func (r *Run) Live() bool { return r.Status == Starting || r.Status == Running }

// Short is the id people see and type: the run's last six characters, lower case.
func (r *Run) Short() string { return Short(r.ID) }

// Short returns the short form of a run id.
func Short(id string) string { return strings.ToLower(id[max(0, len(id)-6):]) }

// SessionName is the tmux session of a run: laight-<workflow>-<short id>.
func SessionName(workflow, id string) string { return "laight-" + workflow + "-" + Short(id) }

// Home is laight's state folder: $LAIGHT_HOME, else ~/.laight.
func Home() (string, error) {
	if h := os.Getenv("LAIGHT_HOME"); h != "" {
		return filepath.Abs(h)
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".laight"), nil
}

// Socket is the tmux server for a laight home: "laight" for ~/.laight, and a name derived from
// the path for any other home, so tests and second homes get their own server.
func Socket(home string) string {
	if u, err := os.UserHomeDir(); err == nil && home == filepath.Join(u, ".laight") {
		return "laight"
	}
	sum := sha256.Sum256([]byte(home))
	return "laight-" + hex.EncodeToString(sum[:4])
}

// Dir is the run's folder.
func Dir(home, id string) string { return filepath.Join(home, "runs", id) }

// NewID returns a ULID: 48 bits of milliseconds then 80 random bits, Crockford base32, so ids
// sort by creation time.
func NewID(now time.Time) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var b [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		b[i] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err) // crypto/rand never fails on supported systems
	}
	// 128 bits → 26 characters of 5 bits, the first one holding the top 3 bits.
	out := make([]byte, 26)
	var acc uint64
	var bits uint
	j := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(b[i]) << bits
		bits += 8
		for bits >= 5 && j >= 0 {
			out[j] = alphabet[acc&31]
			acc >>= 5
			bits -= 5
			j--
		}
	}
	out[0] = alphabet[acc&31]
	return string(out)
}

// Create writes a new run's folder and run.json.
func Create(home string, r *Run) error {
	dir := Dir(home, r.ID)
	if err := os.MkdirAll(filepath.Join(dir, "seats"), 0o700); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "run.json"), r)
}

// Load reads a run.
func Load(home, id string) (*Run, error) {
	b, err := os.ReadFile(filepath.Join(Dir(home, id), "run.json"))
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("run %s: bad run.json: %w", Short(id), err)
	}
	return &r, nil
}

// Update applies f to the run under an exclusive lock and saves it if f returns nil.
func Update(home, id string, f func(*Run) error) (*Run, error) {
	lock, err := os.OpenFile(filepath.Join(Dir(home, id), "run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	r, err := Load(home, id)
	if err != nil {
		return nil, err
	}
	if err := f(r); err != nil {
		return r, err
	}
	return r, writeJSON(filepath.Join(Dir(home, id), "run.json"), r)
}

// List returns every run, newest first. Unreadable runs are skipped.
func List(home string) ([]*Run, error) {
	entries, err := os.ReadDir(filepath.Join(home, "runs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for _, e := range entries {
		if r, err := Load(home, e.Name()); err == nil {
			runs = append(runs, r)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })
	return runs, nil
}

// Find resolves what the user typed to one run of the project at repo. ref is a prefix of the
// short or the full id; empty means "the project's only live run", where live is decided by the
// caller (it knows about tmux).
func Find(home, repo, ref string, live func(*Run) bool) (*Run, error) {
	runs, err := List(home)
	if err != nil {
		return nil, err
	}
	var match []*Run
	ref = strings.ToLower(ref)
	for _, r := range runs {
		switch {
		case ref == "":
			if r.Repo == repo && live(r) {
				match = append(match, r)
			}
		case strings.HasPrefix(r.Short(), ref) || strings.HasPrefix(strings.ToLower(r.ID), ref):
			match = append(match, r)
		}
	}
	switch {
	case len(match) == 1:
		return match[0], nil
	case len(match) == 0 && ref == "":
		var recent []string
		for _, r := range runs {
			if r.Repo == repo && len(recent) < 5 {
				recent = append(recent, r.Short()+" ("+r.Workflow+", "+r.Status+")")
			}
		}
		if len(recent) == 0 {
			return nil, errors.New("no runs in this project yet")
		}
		return nil, fmt.Errorf("no live run in this project; name one: %s", strings.Join(recent, ", "))
	case len(match) == 0:
		return nil, fmt.Errorf("no run %q", ref)
	}
	ids := make([]string, len(match))
	for i, r := range match {
		ids[i] = r.Short() + " (" + r.Workflow + ")"
	}
	if ref == "" {
		return nil, fmt.Errorf("%d live runs in this project, name one: %s", len(match), strings.Join(ids, ", "))
	}
	return nil, fmt.Errorf("%q matches %d runs: %s", ref, len(match), strings.Join(ids, ", "))
}

// Event is one line of events.jsonl.
type Event struct {
	TS   time.Time      `json:"ts"`
	Run  string         `json:"run"`
	Type string         `json:"type"`
	Step string         `json:"step,omitempty"`
	Seat string         `json:"seat,omitempty"`
	Hop  int            `json:"hop,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

// Append adds an event to the run's events.jsonl with one write, so concurrent writers never
// interleave within a line.
func Append(home string, e Event) error {
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Dir(home, e.Run), "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Events reads the run's events.
func Events(home, id string) ([]Event, error) {
	b, err := os.ReadFile(filepath.Join(Dir(home, id), "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var evs []Event
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e Event
		if json.Unmarshal([]byte(line), &e) == nil {
			evs = append(evs, e)
		}
	}
	return evs, nil
}

// Done is what finished a hop: out/<step>-<hop>.json, written by `laight done`, or by the engine
// when the hop can't finish otherwise (a crashed seat, a step that couldn't be delivered).
type Done struct {
	Step string         `json:"step"`
	Hop  int            `json:"hop"`
	Exit string         `json:"exit"`
	Out  map[string]any `json:"out,omitempty"`
	TS   time.Time      `json:"ts"`
}

// ErrAlreadyDone is returned by WriteDone for a hop that has already finished.
var ErrAlreadyDone = errors.New("already finished")

func doneFile(home, id, step string, hop int) string {
	return filepath.Join(Dir(home, id), "out", fmt.Sprintf("%s-%d.json", step, hop))
}

// WriteDone records how a hop finished. The first write wins: later ones get ErrAlreadyDone.
func WriteDone(home, id string, d Done) error {
	if d.TS.IsZero() {
		d.TS = time.Now().UTC()
	}
	file := doneFile(home, id, d.Step, d.Hop)
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), ".done-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// A hard link fails if the file exists, where a rename would replace it.
	if err := os.Link(tmp.Name(), file); errors.Is(err, os.ErrExist) {
		return ErrAlreadyDone
	} else if err != nil {
		return err
	}
	return nil
}

// ReadDone returns how a hop finished, or nil while it hasn't.
func ReadDone(home, id, step string, hop int) (*Done, error) {
	b, err := os.ReadFile(doneFile(home, id, step, hop))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d Done
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", doneFile(home, id, step, hop), err)
	}
	return &d, nil
}

// Project is one entry of projects.json.
type Project struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// Register adds the project at root to projects.json if it isn't there yet.
func Register(home, root string) error {
	file := filepath.Join(home, "projects.json")
	var ps []Project
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &ps); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	for _, p := range ps {
		if p.Path == root {
			return nil
		}
	}
	return writeJSON(file, append(ps, Project{Path: root, Name: filepath.Base(root)}))
}

// writeJSON writes v to file atomically.
func writeJSON(file string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+"-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), file)
}
