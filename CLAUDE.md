# laight

A lightweight framework for running teams of coding agents (Claude Code first, Codex later) in
**tmux**, driven entirely by **YAML**: a workflow file declares its agents (seats) and its steps, in
series or in parallel; starting it spins up a **rig** of those agents, which dies when it's done.
Every run is **isolated**, and every moment an agent needs a human lands in **one queue** that you
answer from one place (a terminal, your phone, Slack…), so nobody has to watch agents.

This file is the spec. README.md is the user-facing overview. Background, research and details for
later milestones are parked in `scratch.docs/scratch.spec_parked.md` (local, untracked).
Status: **M1 done** (`init`, `validate`, `run`, `attach`, `abort`: rigs spin up and tear down);
**M2 done**: the engine loop runs agent and shell steps (`laight done`, routing, `max_hops`),
gates, `needs` and `matrix` (#12), and a minimal inbox (`files` queue, `laight inbox`, `gate`,
`failed` and `permission` items, the last from the `PermissionRequest` hook). The acceptance run
passed (#11: two real issues to merged PRs, one review/fix round each). **M3** (the inbox) is
next. Resuming a run whose engine died is deferred (#8, §4).
**Starting a session? Read [`baton.md`](baton.md) after this file** (or run `/pickup`): what the
last session finished and what to pick up next.

The four hard requirements, in order of priority. Anything that compromises one is wrong:

1. **Lightweight.** One binary, no daemon, no database server, no always-on server. State is plain
   files. A user learns 10 commands (section 8), an agent learns 1. The one exception is `laight show`, a
   read-only web view bound to 127.0.0.1 that runs in the foreground only while you look at it.
2. **tmux.** Every agent is a real interactive CLI session in a tmux pane that a human can attach to,
   read and type into at any time.
3. **Isolation via rigs.** A rig is the group of agents that executes one run of a workflow. It
   spins up when the workflow starts and dies when it finishes, with its own tmux session, git
   worktrees and state folder. Many rigs coexist in a project; they never share a working tree or a
   terminal. Everything a rig does is declared in the workflow's YAML, as in Kubernetes or CircleCI.
4. **One channel for human intervention.** Permission prompts, questions, approval gates, stuck or
   failed steps, from every run in every project, are queued in one place and answered from one
   place. This is the most important feature. Files and a terminal inbox by default; a real queue
   (SQS…) and any messaging (phone push, Slack, a webhook, your own command) when you want them.

---

## 1. Where this comes from

laight comes from a working [OpenRig](https://github.com/mvschwarz/openrig) issue cycle the
maintainer ran on a Go project (`CLAUDE.local.md` has the details): an implementer and a reviewer
seat, `implement → review → fix → verify → merge`, unattended, issues to merged PRs with no human
typing. The idea worked; the machinery hurt, and each pain became a rule here:
- a daemon on a local port → **no daemon**, state in files;
- ~350 commands → **10 commands**, and agents learn only `laight done`;
- prompts stuck in panes nobody watched → **every blocking moment goes to the inbox**;
- long-lived rigs that collided → **one ephemeral rig per run**;
- team and steps in separate files → **one self-contained file per workflow**;
- setup steps outside the tool → **the engine owns worktrees and guidance**, never tracked files;
- yolo seats running as you → **per-seat permissions, not yolo by default**.

Take ideas from OpenRig, not code, unless its licence allows it and the maintainer agrees.

## 2. Concepts

**The config is the system.** As in Kubernetes or CircleCI, 100% of what happens is declared in
YAML: which agents exist, how many, where they work, what each step does, in series or in parallel,
what happens on each outcome. laight only executes it. If behaviour can't be read from the workflow
file, it's a bug in laight.

- **Project**: a git repo with a `.laight/` folder, registered in `~/.laight/projects.json` the
  first time laight runs there. Holds any number of workflows.
- **Workflow**: one self-contained YAML file, `.laight/workflows/<id>.yaml`: its **seats** and its
  **steps**.
- **Seat**: one agent CLI session (runtime + working folder + guidance + permission mode) in one
  tmux window. A step can multiply one (`matrix`), so the number of agents is exactly what the YAML says.
- **Rig / run**: one execution of a workflow and its agents: tmux session `laight-<workflow>-<run6>`
  (on laight's own tmux server), one worktree per seat that asks for one, one state folder. When the
  run ends, the session stops; worktrees are removed on success and kept on failure (`on_finish`).
- **Inbox item**: one thing waiting for a human. A **queue** carries items and answers (`files` by
  default); a **channel** shows and answers them (`terminal`, `web`, `ntfy`, `slack`, `webhook`,
  `command`), picked per item by routing rules.

## 3. Specs (YAML)

OpenRig's vocabulary for seats and routing (`objective`, `allowed_exits`, `next_hop.on`,
`loop_guards.max_hops`); CircleCI / GitHub Actions conventions for the rest (`needs`, `matrix`,
`env`, `timeout`, `if`). Templates use `{{ }}` (Go `text/template`). JSON Schemas in `schema/`
(draft 2020-12), referenced from generated files with `# yaml-language-server: $schema=…`.
`laight validate` uses them plus semantic checks (unknown seat, unreachable step, exit without a
route, `needs` cycle, missing guidance file).

```
.laight/
  workflows/issue-cycle.yaml      # one self-contained file per workflow: seats + steps
  seats/reviewer.yaml             # optional: seat definitions shared by several workflows (`uses:`)
  guidance/implementer.md
  laight.yaml                     # optional: per-project queue/channel routing overrides
```

### A workflow (the acceptance test: M2 is done when it runs end to end on a real issue)

```yaml
# yaml-language-server: $schema=https://…/workflow.schema.json
version: 1
id: issue-cycle
summary: One issue, implemented and independently reviewed, merged into {{ .base }}.
base: dev                         # branch worktrees start from and PRs go into
inputs:
  issue: { required: false, description: Issue number; empty = the next one in the backlog }

seats:                            # the rig: exactly these agents are started for a run
  impl:
    label: Implementer
    runtime: claude-code          # claude-code | codex | shell (any command)
    cwd: worktree                 # worktree (own per run, default) | repo (the main checkout) | <path>
    session: resume               # resume | fresh
    permissions: auto             # default | auto | yolo   (yolo prints a warning at `run`)
    guidance: guidance/implementer.md
    model: opus                   # optional, passed to the runtime
    env: { GH_TOKEN: "{{ secret \"gh-this-repo\" }}" }   # optional; secrets never logged
    setup: make deps              # optional, run once in a new worktree
  reviewer:
    uses: seats/reviewer.yaml     # shared definition; fields here replace its fields (env whole)
    session: fresh                # an independent review each cycle

steps:
  - id: implement
    seat: impl
    objective: |
      Implement issue {{ .inputs.issue }} on a branch from {{ .base }}, verify it, commit, push and
      open a PR into {{ .base }}. Finish with: laight done handoff pr=<PR URL> issue=<N>
    next_hop: { on: { handoff: review } }

  - id: review
    seat: reviewer
    objective: Check out the PR branch and review {{ .steps.implement.out.pr }}.
    next_hop: { on: { handoff: fix } }

  - id: fix
    seat: impl
    objective: Address every review finding on {{ .steps.implement.out.pr }}, with regression tests; push.
    next_hop: { on: { handoff: verify } }

  - id: verify
    seat: reviewer
    objective: Verify every finding. All resolved → laight done pass. Otherwise → laight done fail.
    allowed_exits: [pass, fail]
    next_hop: { on: { pass: approve, fail: fix } }

  - id: approve                   # optional human gate; delete it for fully unattended merges
    type: gate
    ask: Merge {{ .steps.implement.out.pr }} into {{ .base }}?
    allowed_exits: [yes, no]
    next_hop: { on: { yes: merge, no: end } }

  - id: merge
    type: shell                   # deterministic step, no agent
    run: gh pr merge {{ .steps.implement.out.pr }} --squash && gh issue close {{ .steps.implement.out.issue }}

loop_guards: { max_hops: 11 }     # exceeding it → a `failed` inbox item: retry or abort
timeouts: { step: 90m }           # a step quiet this long → a `stuck` inbox item (not a kill)
on_finish: { success: destroy, failure: keep }   # worktrees; the tmux session always stops
```

### Series and parallel

```yaml
steps:
  - id: plan
    seat: lead
    objective: Split the epic into independent tasks. laight done ok tasks='["a","b","c"]'
  - id: build
    needs: [plan]
    matrix: { task: "{{ .steps.plan.out.tasks }}" }   # one copy per task, each with its own `dev` seat
    seat: dev
    objective: Implement {{ .matrix.task }} and open a PR.
  - id: integrate
    needs: [build]                                    # waits for every copy of build
    seat: lead
    objective: Merge the PRs in order and run the full test suite.
```

**Scheduling rules** (the whole engine):

1. Entry steps start at once: the first step of the file (unless it has `needs`; it may be a loop
   target), and every other step with no `needs` and no incoming route.
2. A step with `needs` starts when every needed step (every matrix copy) has finished with a
   non-`failed` exit, and no queued, running or held step can route back to it (a step in a
   review/fix loop counts once the loop has left it); nor can another step whose needs are met
   in the same tick, unless this one can too (both are in that loop), so file order never
   matters. It runs at most once, even if a loop later runs a needed step again. Any non-`failed`
   exit counts, a gate's `no` too: to act on a decision, route on the gate's exit (`validate`
   warns). One whose needs can't be met by the time the run ends is skipped (`step.skipped`);
   the run still succeeds.
3. A finished step's `next_hop.on[<exit>]` starts that step (series, branches and loops). A step
   without `next_hop` ends its branch; `end` ends it explicitly.
4. The run succeeds when no step is running, ready or held. An unrouted `failed`, or a step
   `max_hops` refuses, is **held** on a `failed` inbox item while everything else carries on:
   `retry` starts that step again (past `max_hops`, with one more hop), `abort` fails the run.
   It also fails on `laight abort`. (Decided in #9: with no resume, waiting is how a run survives.)
5. One seat runs one step at a time; a step waiting for a busy seat queues for it. Matrix copies get
   their own seat instance (`dev#1`, `dev#2`…), with its own window, worktree and session, started
   with its copy (so the seat must be `cwd: worktree`); a seat only matrix steps use never starts
   on its own. The window closes once the copy's turn ends, unless the copy is held on a `failed`
   item: then it stays up to look at until the item is answered, and a `retry` is delivered to it.
   The worktree follows `on_finish`. `max_parallel` caps live copies (default 4).
6. Routes and `needs` don't mix: a step with `needs` can't be a route target, and a matrix step
   can't have `next_hop`. `.steps.<id>.out` is the latest hop's output. A hop is one step start
   (each matrix copy counts). `validate` enforces all of this.

**Matrix** (decided in #12): keys sorted, then the cartesian product in list order, numbers the
copies `#1…#N`. A value is a list of scalars, or a template: one that is a single action
(`{{ .steps.plan.out.tasks }}`) gives its value, anything else must render a JSON list. An empty
list, a non-scalar or more than 32 copies holds the step on a `failed` item before any copy
starts (`validate` rejects it already when every value is a literal list). A failed copy is held alone while its siblings run on; `retry` reruns that copy only.
Instance `dev#k` serves copy k of every matrix step on `dev`, and of every later expansion: it
keeps its worktree and, unless `session: fresh`, its conversation, like any seat. A gate can't
have a matrix. Later steps see a matrix step's `.out` as a **list**, one entry per copy
in copy order, and `.exit` as the list of exits (Argo's `withParam` model, not "the last copy to
finish wins"): `{{ range .steps.build.out }}{{ .pr }}{{ end }}`; `validate` rejects `.out.<key>`.

**Step types:** `agent` (default when `seat` is set), `shell` (`run:`; exit 0 → `ok`, else
`failed`, unless `exits:` maps codes), `gate` (a human decision, via the inbox).
A **shell step** has no seat: the engine runs `sh -c '<run>'` in the repo's main checkout, in its
own session with no terminal (a command that prompts fails instead of hanging), with the workflow's
and the step's `env`. Whatever it leaves running is stopped when `sh` exits or the run ends. Output
goes to `out/<step>-<hop>.log` with known secrets redacted; the exit code is `.steps.<id>.out.code`.
**A step's `env` exists only on shell steps** (#16): an agent's env is its seat's. Secrets reach a
shell command only through `env`, never spliced into `run:` (§12.4). An unquoted env value is
passed on as YAML read it: integers exactly, but `0x1F` as `31` and `1.10` as `1.1`, so quote a
value you need verbatim (`validate` warns).
**Exits:** any identifiers; `allowed_exits` defaults to `[handoff]`; `failed` is implicit and can
be routed. **`next_hop.mode: end`** ends the branch whatever the exit (an unrouted `failed` is
still held). **Outputs:** `laight done <exit> key=value…` sets `.steps.<id>.out.<key>` (JSON values
allowed). Templates see only `.inputs`, `.steps.<id>.out`/`.exit`, `.matrix`, `.base`,
`.run.id`/`.project`/`.workflow` and, **in `env` only**, `secret`: every other template can be
pasted into a pane, and panes are logged.

## 4. Runtime layout

```
~/.laight/                            # global, so the inbox spans every project
  projects.json
  runs/<run-id>/
    run.json                          # spec snapshot, inputs, status, current step, hops
    events.jsonl                      # append-only, the source of truth for observability
    out/<step>-<hop>.json             # what `laight done` wrote (or the engine, for shell steps)
    out/<step>-<hop>.log              # a shell step's output, known secrets redacted
    seats/<seat>.log                  # tmux pipe-pane of the seat
    seats/<seat>.idle, .stopped       # Stop hook: turn ended after `done`; stopped twice without it
    worktrees/<seat>/
  inbox/<item-id>.json                # one file per pending decision (the `files` queue)
  inbox/answered/<item-id>.json
  config.yaml                         # queue, channels, routing, defaults
```

- Run ids are ULIDs. Writes are atomic (temp + rename). The only lock is `flock` on `run.json`.
- **The engine** is `laight engine <run>` in window 0 of the rig's tmux session: a loop of read
  state → act → append event → sleep. When the run finishes it applies `on_finish`, stops the seats
  and kills the session.
- **When the engine dies**, the run is lost, not resumed: `attach` and `abort` report "engine
  died", `laight abort` cleans up, worktrees follow `on_finish.failure`, a new run starts over.
  **Resume is deferred (#8)** until real runs show engines dying; the state stays in files so it
  remains possible.

## 5. Driving a seat

- **Start:** a tmux window running the runtime with the seat's flags, env `LAIGHT_RUN`,
  `LAIGHT_SEAT`, `LAIGHT_HOME`, `PATH` (the seat's `env` one, else the engine's) with a run
  folder first that holds only a `laight` link to the binary running the rig (agents type
  `laight done`, installed or not; nothing else from its folder), and a generated per-seat
  settings file with laight's hooks. Ready when the `SessionStart` hook reports it (fallback:
  prompt glyph via `capture-pane`, timeout).
- **Give a step:** `tmux load-buffer` + `paste-buffer -p` (bracketed paste) + `Enter`. Never
  `send-keys` long text.
- **Finish a step:** the agent runs `laight done <exit> [k=v…]` (no flags; it reads env). If it
  stops without it, the **Stop hook** blocks once with a reminder; the second time it writes
  `seats/<seat>.stopped` instead of looping, and the engine raises a `stuck` item (§12.2). A seat
  gets its next step only after `done` *and* its Stop hook.
- **Fresh vs resume:** `fresh` restarts the CLI before each step (`/clear` is not enough);
  `resume` keeps the session. Every Claude session gets a `--session-id`, so a seat relaunched
  within a run continues its conversation with `--resume`.
- **Guidance and settings** are passed at launch (`--append-system-prompt-file`, `--settings`),
  never written into tracked files, `CLAUDE.md` or `~/.claude/settings.json`. The settings allow
  `Bash(<abs path to laight> done:*)` and `Bash(laight done:*)` (what agents type). Permission
  modes map to `--permission-mode`: `default` → `manual`, `auto` → `auto`, `yolo` →
  `bypassPermissions`.
- **Launch prompts:** the folder-trust dialog and yolo's warning are accepted via `capture-pane`,
  only for laight's own worktrees; `~/.claude.json` is never written.
- **Worktrees** start detached at `origin/<base>`; reviewers check PRs out detached.
- **Who writes what:** only the engine writes `run.json`; `done` and hooks write their own files
  (`out/<step>-<hop>.json`, first write wins; the Stop hook's `seats/<seat>.idle` and `.stopped`;
  `inbox/`), which the engine polls (~500 ms). When a hop can't finish otherwise (crashed seat,
  objective that didn't render), the engine writes its `failed` out file itself, so every ending
  goes through routing.
- **Secrets:** `{{ secret "x" }}` reads env `LAIGHT_SECRET_X`, then the OS keychain (service `laight`).
- Every runtime is an adapter: `launch`, `ready`, `deliver`, `hooks`, and `detectBlocked(screen)`
  for runtimes without hooks.

## 6. The human channel (the point of the project)

| Kind | Source | Answer goes back via |
|---|---|---|
| `permission` | Claude Code `PermissionRequest` hook | the hook blocks, polling the item file, and returns allow/deny |
| `question` | `PreToolUse` hook on `AskUserQuestion`; a plain-text question caught by the Stop hook | the hook's return value; for plain text, pasted into the seat |
| `gate` | a `type: gate` step | the engine routes on the chosen exit |
| `stuck` | Stop hook gave up; step quiet past its timeout; turn never ended after `done` | nudge / fail when the agent stopped; wait / fail otherwise |
| `failed` | unrouted `failed`, `max_hops` exceeded, seat crashed | retry step / abort run |

An item: `{id, run, repo, workflow, step, seat, hop, kind, title, detail, choices[], created,
expires?, answer?, answered?, match?}`. `detail` is a short excerpt (the command, the last lines of
the pane). **The queue itself redacts** title and detail (secret patterns, and the exact values of
the seat's secret env named in `LAIGHT_REDACT_ENV`), strips control characters, then cuts the
detail to 2000 bytes, so no producer can forget.

**Permission items** (#10, verified against Claude Code 2.1.302): title `<seat> wants to use
<tool>`, detail the command or tool input. Claude shows its own dialog while the hook waits, so
either place can answer: the inbox (the hook returns allow/deny), *No*/Esc in the pane (Claude
stops the hook, which drops its item), or *Yes* in the pane (a `PostToolUse` hook withdraws the
matching item by a hash of tool + input). The item expires a minute before the hook's 1 h timeout
and then denies; so does an unreadable request, any error, or the run ending. Checked on 2.1.305
(#32): the hash matches across both hooks for Bash, Write, Edit, WebFetch and MCP tools; No, Esc,
a hook timeout and a session that exits all stop the hook with SIGTERM; a 1 h timeout is honoured.
A seat's Bash keeps the run's `laight` ahead of whatever the user's rc puts on `PATH`.

- The item's state always lives with its run. A queue backend only *carries* items and answers.
  First answer wins, enforced with an atomic rename, so duplicate deliveries are harmless.
- `internal/inbox` is the `files` queue as plain functions; a `Queue`/`Channel` interface is
  extracted when a second backend arrives. Every backend besides `files` and `terminal` is
  optional, compiled in, and off unless configured.
- No channel ever opens an inbound port. Phone/chat answers carry the item's one-time token.
  Only title, project, run, step and choices leave the machine, never `detail`.
- **Routing** in `~/.laight/config.yaml` (overrides in `.laight/laight.yaml`):

```yaml
queue: { type: files }            # or: { type: sqs, items: <url>, answers: <url>, region: eu-west-1 }
channels:
  phone: { type: ntfy, server: https://ntfy.sh, topic: laight-<random> }
  team:  { type: slack, channel: "#agents" }
routes:                           # first match wins; terminal and web always see everything
  - { kind: permission, to: [phone] }
  - { to: [phone], escalate_after: 10m, then: [team] }
```

**Rules:** an agent must never be blocked without an inbox item existing for it. An expired item
falls back to *deny*, never allow. `permissions: auto` + the inbox should make `yolo` unnecessary
for most workflows; that is a design goal, measure it.

## 7. Observability

- `events.jsonl` per run: `ts, run, type, step?, seat?, hop?, data`. Types:
  `run.started|finished|aborted`, `step.started|done|skipped|timeout`, `seat.up|down|crashed`,
  `inbox.raised|answered`, `hook.<name>` (session start and stop), `tool` (name + duration, never
  arguments), `usage` (tokens/cost per seat per step).
- `laight watch`: every run, one line each. `laight watch <run>`: a live CI-style pipeline
  (✓ ● ✕ ○, ×N for loops), durations, cost, last events, open items. The engine window shows it.
- `laight show`: the same as a CircleCI-like web page on 127.0.0.1, live via SSE, plain HTML
  embedded with `go:embed` (no npm, no external requests); `--export` writes one static file.

## 8. CLI (the whole surface)

Ten commands, and it stays that way; a new command needs a use none of these covers.

```
laight init                          scaffold .laight/
laight validate [workflow]           schema + semantic checks (run also does this first)
laight run <workflow> [k=v…]         spin up the rig, start the engine, follow it live
laight watch [run]                   live pipeline view; no run = every run, one line each
laight show [run] [--export]         the web view (localhost, foreground)
laight inbox [<item> <answer>]       the human queue; with arguments, answer one (scripts)
laight attach <run> [seat]           jump into the run's tmux session (or one seat's window)
laight abort <run>                   stop a live run; on a finished one, remove its worktrees
laight doctor                        tmux, git, runtimes, hooks; queue and channels
laight done <exit> [k=v…]            for agents: finish the current step (the only one they learn)
```

Run arguments take an id prefix, or nothing when exactly one run of the project is live. Every
command with side effects outside the run asks first and lists what it will do; `--yes` skips it.

## 9. Implementation

- **Go, latest stable** (`go 1.27`), pure Go, `CGO_ENABLED=0`. `cmd/laight/`, packages under
  `internal/`. Few dependencies (YAML, JSON Schema, bubbletea); prefer the standard library.
  `gofmt`, `go vet`, `staticcheck` clean. Target ~2,000 lines before M5.
- tmux via the CLI on its own server (`tmux -L laight`), so the user's sessions are never touched.
- Hooks are the same binary: `laight hook <event>` reads the hook JSON on stdin.
- Tests: unit tests for spec parsing, routing, templating; an integration test with a fake runtime
  (`runtime: shell` calling `laight done`) for loops, gates, timeouts, abort and a dead engine, in
  a throwaway tmux server. Real Claude runs are manual and supervised.
- **Distribution:** one command, nothing to install first: `brew install berni23/tap/laight` (the
  cask pulls in tmux), built by GoReleaser on a `vX.Y.Z` tag; also `go install`, plain binaries,
  `.deb`/`.rpm`. No install scripts piped into a shell, no post-install steps.

## 10. Milestones

1. **M0** Schemas, `init`, `validate`.
2. **M1** tmux driver, rig spin-up/tear-down per run, worktrees, `on_finish`, `attach`, `abort`.
3. **M2** Engine: steps, `laight done`, routing, `max_hops`, shell steps; a minimal inbox
   (the `files` queue, `laight inbox <item> <answer>`, gates, the `PermissionRequest` hook); then
   `needs` and `matrix`. Acceptance: the issue cycle of section 3 on a real issue in a real repo.
4. **M3** Inbox: the remaining hooks (questions, Stop), `laight inbox` (TUI), stuck detection.
5. **M4** Channels and routing: `ntfy` (phone), `command`, then `slack` and `webhook`.
6. **M5** Observability: `watch`, `show`, usage/cost.
7. **M6** `sqs` queue (several machines, one inbox).
8. **M7** Codex runtime; `shell` runtime for arbitrary CLIs.

## 11. Rules for working on this repo

For contributors and for coding agents working here (AGENTS.md points to this file):

- **Agents may commit, push the item's branch and open its PR without being asked.** Never kill,
  merge or abort anything outside a throwaway test without asking. Never touch the user's own tmux sessions
  (laight uses its own server, `tmux -L laight`; tests use a throwaway one).
- **Work never happens on `main`.** Before the first edit for an item, switch to its own branch,
  `<issue#>-<short-slug>` (or `m2-<slug>` for a milestone item with no issue); `/pickup` does this.
- Requirement 1 beats features: if something needs a daemon, an always-on server or a database,
  it's out. Optional integrations (SQS, Slack…) stay optional: off unless configured, and the
  default install needs no account anywhere.
- No secrets in logs, events, inbox details or ntfy payloads.
- Verify against real tmux and a real Claude Code before calling a milestone done; a passing unit
  test does not prove an agent got its prompt.
- Ideas, not code, from other projects unless their licence allows it; say where an idea came from
  in the PR.
- Every change: `make check` passes (gofmt, vet, staticcheck, tests). User-visible changes get a
  line in CHANGELOG.md under *Unreleased*. See CONTRIBUTING.md.
- **Every commit passes the baton.** `baton.md` is the session handoff, in three sections only:
  done last session, do this next, waiting on the user. The `baton-check` hook
  (`.claude/hooks/baton-check.sh`) blocks a `git commit` that doesn't include it; `/baton` says what
  to write, `/pickup` reads it. It never holds merge/push state or a finding's only copy (that goes
  on an issue or into this spec). Escape hatch for a commit that teaches the next session nothing:
  `[no-baton]` in the command, and say so. Reviews: `/cr` posts a two-reviewer review, `/check-cr`
  answers it; their local scratch is `scratch.review` (gitignored).
- Maintainer-only local notes (paths, private reference repos) live in `CLAUDE.local.md`, which is
  gitignored. Read it if it exists; never commit it.

## 12. Hard problems: the decisions

Four problems from the engine-loop review (#14) that a patch can't fix. How OpenRig, Gas Town,
Archon, Orca and Claude Squad handle them is in the parked notes; ideas only, never code (§11).

### 12.1 Delivering a step exactly once (#8)

**Deferred with resume.** If resume comes back: write the hop to `run.json` as `delivered: false`
before the paste, and treat the paste as a nudge that can be resent (OpenRig's split, with files).
The agent's side acknowledges: a `UserPromptSubmit` hook writes `seats/<seat>.ack`. On resume, a
hop with no ack is pasted again; an acked hop with no `done` is left running; re-pasting an acked
hop is never automatic (Archon's rule): laight raises a `stuck` item. A hop whose seat died while
the engine was down continues only if the seat can resume its conversation, with a short note to
carry on, once per hop; otherwise it fails like a crashed seat. A seat that crashes again on a hop
resume respawned it for raises a `failed` item instead of being respawned again. Runtimes without hooks fall back to
a `capture-pane` snippet check.

### 12.2 Knowing an agent is idle, done or stuck (#15)

Explicit `laight done` plus hooks are the proof. Screen or output silence only ever *adds*
evidence; it never decides on its own. "Stuck" is computed at each tick from file timestamps
(`out/`, `.idle`, `.stopped`, `.ack`, the seat log's mtime, worktree writes outside `.git/`)
against the step's `timeout` (default `timeouts.step`; none means never timed): a step is stuck
when none of them moved for that long, not when it has run that long. Nothing new is stored but
the open item and when its clock last restarted, and nothing routes on it. An agent that stops
without `laight done` gets one reminder (the Stop hook's), and a `stuck` item at once when it
stops a second time (`.stopped`); a seat quiet past its timeout gets the item directly. It also
covers a seat that ran `done` but whose turn never ended (a Stop hook that failed), and a shell
step (its log is its only trace). Decided in #15 (2026-10-25, revised in the #47 review): the
choices follow what is known about the pane. A stopped agent is at its prompt: `nudge` (paste the
reminder) or `fail`; no `wait`, which would wait for ever. A quiet seat may be showing a dialog
that a paste's Enter would answer: `wait` (the clock starts again) or `fail`; so does a shell
step. `fail` ends the hop `failed` and routes (a shell step's command is stopped; a seat whose step
is already done is just freed). A quiet seat's turn may still be running, so it takes its next
step only after its Stop hook (a late `laight done` from that turn then finds no step to finish);
a seat without hooks is interrupted (`C-c`) instead. Looking first is `laight attach`, which the item's detail names. A
seat with a pending permission item is never stuck, and a gate is never timed: a human is already
asked (reminding them is M4's `escalate_after`). An item whose turn ends on its own is withdrawn.
A crashed seat fails its hop at once and is respawned only by a later step (or a `retry`).

### 12.3 One parallel branch fails while others run (#12)

Archon's model: when a parallel copy fails with no route, running siblings finish and their exits
are recorded; the failed one is held on its `failed` item (rule 4 of §3), the one thing the human
answers, and every step that `needs` it waits: `retry` can still let them run, `abort` fails the
run and they never start (decided in #12, 2026-10-21). Stopping siblings stays explicit
(`laight abort`, or later an opt-in `on_failure: cancel`).

### 12.4 Secrets and agent panes (#7, #16)

Secrets reach an agent or a shell step only as env vars (seat `env`, or a shell step's `env`),
never in a rendered prompt or command line. Redact by key name and by pattern in `events.jsonl` and
inbox details; a bare long run counts only when it mixes upper case, lower case and digits, so
commit SHAs and branch names survive. Known secret values can also be stripped from the pane log as
it is written (`pipe-pane` through a filter). **Raw pane logs can't be fully redacted** (an agent
can print whatever it can read), so they stay in the run folder (`0700`) and the docs say so
plainly instead of promising redaction.
