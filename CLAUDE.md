# laight

A lightweight framework for running teams of coding agents (Claude Code first, Codex later) in
**tmux**, driven entirely by **YAML**: a workflow file declares its agents (seats) and its steps, in
series or in parallel; starting it spins up a **rig** of those agents, which dies when it's done. Every run is **isolated**, and every moment an agent needs a human lands in
**one queue** that you answer from one place (a terminal, your phone, Slack…), so nobody has to
watch agents.

This file is the spec. README.md is the user-facing overview. Status: **M1 done** (`init`,
`validate`, `run`, `attach`, `abort`: rigs spin up and tear down); **M2 in progress**: the engine
loop runs agent and shell steps (`laight done`, routing, `max_hops`); resume, the inbox and
`needs`/`matrix` are next.
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
   place. This is the most important feature. The queue and the delivery are pluggable: files and
   a terminal inbox by default; a real queue (SQS…) and any messaging (phone push, Slack, a
   webhook, your own command) when you want them (section 6).

---

## 1. Where this comes from: a real OpenRig issue cycle

laight started from a working [OpenRig](https://github.com/mvschwarz/openrig) setup the maintainer
ran on a Go project (go-orchestrator, private; maintainers have the local details in
`CLAUDE.local.md`). Its shape is the reference use case:

- **The rig:** two Claude Code seats. The implementer works in the main checkout; the reviewer works
  in a separate git worktree with a fresh context every cycle, so its review stays independent.
  Unattended (`permission_policy: builtin:yolo`, `non_interruptive: true`).
- **The workflow:** `implement → review → fix → verify ─(pass)→ merge`, `verify ─(fail)→ fix`,
  `max_hops: 9`. Steps are short objectives; the details live in per-seat guidance files, which
  call the repo's own slash commands (`/pickup`, `/baton`, `/cr`, `/check-cr`).
- **The wrappers:** the raw OpenRig CLI was too much, so a Makefile hid it behind
  `setup-workflow`, `start-workflow`, `track-workflow`, `show-workflow`, `abort-workflow`,
  `stop-workflow` and `answer-workflow`. laight's CLI must be at that level, so no wrappers are needed.
- **It worked:** issues went from pickup to a merged PR with a review/fix round and no human typing.
  The idea is sound; the machinery around it is what hurt.

**What OpenRig got right, keep it:**

- Declarative YAML for the team and for the workflow, versioned in the repo under one folder.
- Steps owned by a role, with explicit exits (`handoff`, `done`, `failed`) and a routing table
  (`next_hop.on`), plus a loop guard (`max_hops`). This reads well and validates well.
- Real Claude Code sessions in tmux that you can attach to. Per-seat guidance files.
- Fresh vs resumed context per seat (`restore_policy`).

**What hurt, never repeat it (each one happened):**

| OpenRig friction | laight rule |
|---|---|
| A daemon on a local port: a wrong bind host (on a Tailscale machine), orphaned daemon processes, a stale start lock | No daemon. The engine is a process in the run's own tmux window; state is files; restart = re-read the files. |
| ~350 commands; flags required but not marked so (`--created-by`, `--actor-session`) | ~10 commands. Every flag optional or validated with a clear message. |
| Agents had to call the CLI correctly to hand off | An agent finishes a step with **one** command, `laight done <exit>`, and a Stop hook reminds it if it forgets. |
| A question or prompt sat in one seat's pane until someone looked (hence `make answer-workflow`) | Hooks put every blocking moment in the inbox and on the phone. Watching panes is optional. |
| A web UI "experimental, in maintenance mode", no auto-refresh | Observability is an event log plus a live terminal view; HTML is an optional render of the same log. |
| Rigs are long-lived per-project teams, so runs collide and cleanup needs `abort` + `down` + lock surgery | A rig is ephemeral: it exists for one run and dies with it; `laight abort` tears down exactly that rig. Several rigs per project. |
| The team (rig.yaml) and the steps (workflow) live in different files with different ids | One self-contained file per workflow: seats + steps. |
| Setup steps outside the tool (worktree add, `CLAUDE.local.md` into `info/exclude`) | Worktrees and guidance delivery are the engine's job; nothing is written into the user's tracked files. |
| Yolo seats run as you: they can read `~/.ssh`, `~/.aws` and use every signed-in `gh` account | Permission mode is per seat, default is *not* yolo, and permission prompts are cheap because they go to the inbox (section 6). |

Take ideas from OpenRig, not code, unless its licence allows it and the maintainer agrees.

## 2. Concepts

**The config is the system.** As in Kubernetes or CircleCI, 100% of what happens is declared in
YAML: which agents exist, how many, where they work, what each step does, in series or in parallel,
what happens on each outcome. laight only executes it. If behaviour can't be read from the workflow
file, it's a bug in laight.

- **Project**: a git repo with a `.laight/` folder. laight finds it from the current directory and
  registers it in `~/.laight/projects.json` the first time it runs there (for `watch`, `show`, `inbox`).
  A project holds any number of workflows.
- **Workflow**: one YAML file, `.laight/workflows/<id>.yaml`, self-contained: its **seats** (the
  agents) and its **steps** (who does what, in which order, routed on outcomes).
- **Seat**: one agent CLI session (runtime + working folder + guidance + permission mode) in one
  tmux window. The workflow declares its seats; a step can multiply one (`matrix`), so the number
  of agents is exactly what the YAML says.
- **Rig**: the **ephemeral group of agents that executes one run of a workflow**, in series or in
  parallel. Unlike OpenRig, a rig is not a long-lived per-project team: **starting a workflow spins
  its rig up automatically, and the rig dies when the run finishes.** Several rigs coexist in a
  project (different workflows, or the same workflow several times), each fully isolated.
- **Run**: one execution of a workflow, and the identity of its rig: tmux session
  `laight-<workflow>-<run6>` (on laight's own tmux server), one worktree per seat that asks for one,
  one state folder. When the run ends, the tmux session and agents are stopped; worktrees are
  removed on success and kept on failure for inspection (`on_finish`, below; `laight abort <run>` removes them later).
- **Inbox item**: one thing waiting for a human (a permission, a question, a gate…).
- **Queue**: where items wait and answers come back. `files` by default; `sqs` and others optional.
- **Channel**: where items are shown and answered: `terminal` (`laight inbox`), `web`
  (`laight show`), `ntfy`, `slack`, `webhook`, `command`. Routing rules pick channels per item.

## 3. Specs (YAML)

**Standardisation decision.** There is no standard for agent-team workflows (checked 2026-10:
Serverless Workflow is generic, Open Agent Spec targets LLM-API agents, Archon, Gas Town and OpenRig
each have their own). So:

- Stay **close to OpenRig's vocabulary** for agents and routing (seats with `runtime`/`cwd`, steps
  with `objective`, `allowed_exits`, `next_hop.on`, `loop_guards.max_hops`), so OpenRig users read
  it instantly and can convert a setup by hand in minutes. (An importer existed in M0 and was
  removed: one user, one setup, and more code to keep safe than a hand conversion costs.)
- Borrow **CircleCI / GitHub Actions** conventions for the rest: one file per workflow, `needs` for
  dependencies and parallelism, `matrix` for fan-out, `env`, `timeout`, `if`. Templates use
  `{{ }}` (Go `text/template`), not `${{ }}`.
- Ship a **JSON Schema** (`schema/workflow.schema.json`, `schema/config.schema.json`, draft 2020-12)
  and put `# yaml-language-server: $schema=…` in generated files, so editors autocomplete and
  agents that write specs can validate them. `laight validate` uses the same schemas plus semantic
  checks (unknown seat, unreachable step, exit without a route, `needs` cycle, missing guidance file).

Files live in the repo under `.laight/`:

```
.laight/
  workflows/issue-cycle.yaml      # one self-contained file per workflow: seats + steps
  workflows/triage.yaml
  seats/reviewer.yaml             # optional: seat definitions shared by several workflows (`uses:`)
  guidance/implementer.md
  guidance/reviewer.md
  laight.yaml                     # optional: per-project queue/channel routing overrides
```

### A workflow

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
    session: resume               # resume | fresh  (OpenRig's restore_policy)
    permissions: auto             # default | auto | yolo   (yolo prints a warning at `run`)
    guidance: guidance/implementer.md
    model: opus                   # optional, passed to the runtime
    env: { GH_TOKEN: "{{ secret \"gh-this-repo\" }}" }   # optional; secrets never logged
    setup: make deps              # optional, run once in a new worktree (gitignored files aren't there)
  reviewer:
    uses: seats/reviewer.yaml     # shared definition; fields here override it
    session: fresh                # an independent review each cycle

steps:
  - id: implement
    seat: impl
    objective: |
      Implement issue {{ .inputs.issue }} on a branch from {{ .base }}, verify it, commit, push and
      open a PR into {{ .base }}. Finish with: laight done handoff pr=<PR URL> issue=<N>
    allowed_exits: [handoff]
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

loop_guards: { max_hops: 11 }     # exceeding it → run fails → inbox item
timeouts: { step: 90m }           # a step over its timeout → inbox item (not a kill)
on_finish: { success: destroy, failure: keep }   # worktrees; the tmux session always stops
```

### Series and parallel

Steps run in series through routing, and in parallel through `needs` and `matrix`:

```yaml
steps:
  - id: plan
    seat: lead
    objective: Split the epic into independent tasks. laight done ok tasks='["a","b","c"]'
  - id: build
    needs: [plan]
    matrix: { task: "{{ .steps.plan.out.tasks }}" }   # one copy of the step per task…
    seat: dev                                         # …each with its own `dev` seat and worktree
    objective: Implement {{ .matrix.task }} and open a PR.
  - id: security
    needs: [plan]                                     # runs at the same time as `build`
    seat: auditor
    objective: Threat-model the plan.
  - id: integrate
    needs: [build, security]                          # waits for every copy of build, and security
    seat: lead
    objective: Merge the PRs in order and run the full test suite.
```

**Scheduling rules** (the whole engine, in six lines):

1. Entry steps start at once: the first step of the file (unless it has `needs`; it may be a loop
   target), and every other step with no `needs` and no incoming route.
2. A step with `needs` starts when every needed step (every matrix copy) has finished with a
   non-`failed` exit.
3. A finished step's `next_hop.on[<exit>]` starts that step (series, branches and loops). A step
   without `next_hop` simply ends its branch; `end` ends it explicitly.
4. The run succeeds when no step is running or ready and none failed unrouted; it fails on an
   unrouted `failed`, `max_hops`, or an `abort`.
5. One seat runs one step at a time; a step waiting for a busy seat queues for it. Matrix copies get
   their own seat instance (`dev#1`, `dev#2`…), so parallelism is never hidden contention.
   `max_parallel` on a matrix step caps live copies (default 4).
6. Routes and `needs` don't mix: a step with `needs` can't be a route target, and a matrix step
   can't have `next_hop` (it joins only through `needs`). `.steps.<id>.out` is the latest hop's
   output. A hop is one step start (each matrix copy counts). `validate` enforces all of this.

**Step types:** `agent` (default when `seat` is set), `shell` (`run:`; exit code 0 → `ok`, else
`failed`, unless `exits:` maps codes), `gate` (a human decision, always via the inbox).
A **shell step** has no seat and never waits for one: the engine runs `sh -c '<run>'` in the
repo's main checkout, with the workflow's `env` plus the step's own `env` (and `LAIGHT_RUN`,
`LAIGHT_HOME`, `LAIGHT_STEP`; never `LAIGHT_SEAT` or the engine's `LAIGHT_SECRET_*`), in a session
of its own: no terminal, so a command that prompts fails instead of hanging the run. When `sh`
exits, whatever it left running is stopped (SIGTERM, then SIGKILL after 5s); so is the whole
command when the run ends, by any branch, an abort, or an abort after the engine died (its
process group is kept on the hop in run.json). Its output goes to `out/<step>-<hop>.log`, with
the secret values its env read replaced by `***` (exact values only: an encoded copy isn't
caught); its exit code is `.steps.<id>.out.code`.
**A step's `env` exists only on shell steps** (#16): an agent's env is its seat's, fixed when the
seat starts. Secrets reach a shell command only through `env`, never spliced into `run:` (§12.4).
**Exits:** any identifiers; `allowed_exits` defaults to `[handoff]` for agent steps; `failed` is
implicit on every step and can be routed. **Outputs:**
`laight done <exit> key=value…` sets `.steps.<id>.out.<key>` (JSON values allowed, numbers kept
digit for digit; `null` is empty). Templates see only `.inputs`, `.steps.<id>.out`/`.exit`,
`.matrix`, `.base`, `.run.id`/`.project`/`.workflow` and, **in `env` only**, `secret`: every other
template can be pasted into a pane, and panes are logged. **`next_hop.mode: end`** ends the branch
whatever the exit, except an unrouted `failed`, which still fails the run (rule 4).

Guidance is delivered **without touching tracked files**: for Claude Code via
`--append-system-prompt-file` / `--settings` on launch (whatever the installed version supports,
check `claude --help`), never by editing `CLAUDE.md`. No `CLAUDE.local.md` dance.

The issue-cycle workflow above is the **acceptance test**: M2 is done when it runs end to end
on a real issue.

## 4. Runtime layout

```
~/.laight/                            # global, so the inbox spans every project
  projects.json                       # registered projects: path, name
  runs/<run-id>/
    run.json                          # spec snapshot, inputs, repo, status, current step, hops
    events.jsonl                      # append-only, the source of truth for observability
    out/<step>-<hop>.json             # what `laight done` wrote (or the engine, for shell steps)
    out/<step>-<hop>.log              # a shell step's output, known secrets redacted
    seats/<seat>.log                  # tmux pipe-pane of the seat
    worktrees/<seat>/                 # or under the repo's parent dir, configurable
  inbox/<item-id>.json                # one file per pending decision (the `files` queue)
  inbox/answered/<item-id>.json
  config.yaml                         # queue, channels, routing, defaults (section 6)
```

- Run ids are ULIDs. Writes are atomic (write temp + rename). No locks other than `flock` on
  `run.json` while the engine updates it.
- **The engine** is `laight engine <run>` running in window 0 (`engine`) of the rig's tmux session.
  When the run finishes, the engine applies `on_finish`, stops the seats and kills the session.
  It is a loop: read state → act → append event → sleep/wake on file change. Kill it and run
  `laight run --resume <run>`, and it continues from `run.json` + `events.jsonl`. That is the whole
  durability story.

## 5. Driving a seat

- **Start:** `tmux new-window -t laight-<workflow>-<run6> -n <seat>` running the runtime with the seat's
  flags, env `LAIGHT_RUN`, `LAIGHT_SEAT`, `LAIGHT_HOME`, and per-seat hook settings (section 6). Wait for
  the `SessionStart` hook to report ready (fallback: prompt glyph via `capture-pane`, timeout).
- **Give a step:** render the objective, `tmux load-buffer` + `paste-buffer -p` (bracketed paste)
  + `Enter`. Never `send-keys` long text.
- **Finish a step:** the agent runs `laight done <exit> [k=v…]` (it reads `LAIGHT_RUN`/`LAIGHT_SEAT`
  from env, so no flags). If the agent stops without it, the **Stop hook** returns
  `decision: block` with "finish the step with `laight done <exit>`, exits: …", once; the second time
  it raises an inbox item instead of looping.
- **Fresh vs resume:** `fresh` restarts the seat's CLI before each of its steps (`/clear` is not
  enough for independence); `resume` keeps the session across steps of a run. laight gives every
  Claude session a `--session-id`, so `laight run --resume` relaunches a crashed seat with `--resume`.
- **Idle:** a seat gets its next step only after `laight done` *and* its Stop hook (the turn ended).
- **Launch prompts:** Claude's folder-trust dialog (new worktree) and yolo's warning are accepted by
  the driver via `capture-pane`, only for laight's own worktrees; `~/.claude.json` is never written.
- **Generated settings** allow `Bash(<abs path to laight> done:*)`, so finishing a step never prompts.
  Permission modes map to Claude's `--permission-mode`: `default` → `manual`, `auto` → `auto`,
  `yolo` → `bypassPermissions`.
- **Worktrees** start detached at `origin/<base>`. A branch can be checked out in one worktree
  only, so reviewers check PRs out detached (say so in their guidance).
- **Who writes what:** only the engine writes `run.json`; `done` and hooks write their own files
  (`out/<step>-<hop>.json`, first write wins; the Stop hook's `seats/<seat>.idle`; `inbox/`),
  which the engine polls (~500 ms). No fsnotify. When a hop can't finish otherwise (its seat
  crashed, its objective didn't render), the engine writes its `failed` out file itself, so every
  ending goes through the same routing.
- **Secrets:** `{{ secret "x" }}` reads env `LAIGHT_SECRET_X`, then the OS keychain (service `laight`).
- Every runtime is an adapter: `launch(cmd, flags)`, `ready()`, `deliver(text)`,
  `hooks()` and `detectBlocked(screen)` for runtimes without hooks. Claude Code first; Codex second.

## 6. The human channel (the point of the project)

Everything that needs a human becomes an **inbox item**:

| Kind | Source | Answer goes back via |
|---|---|---|
| `permission` | Claude Code `PermissionRequest` hook | the hook process itself: it blocks, polling the item file, and returns allow/deny JSON to Claude |
| `question` | a `PreToolUse` hook on `AskUserQuestion` (blocks like `permission`); a plain-text question caught by the Stop hook | the hook returns the answer to the agent; for plain text, paste it into the seat |
| `gate` | a `type: gate` step | the engine routes on the chosen exit |
| `stuck` | step timeout, Stop hook gave up, idle with no `laight done` | choices: nudge / open pane / fail step |
| `failed` | unrouted `failed`, `max_hops` exceeded, seat crashed | choices: retry step / abort run |

An item: `{id, run, repo, workflow, step, seat, kind, title, detail, choices[], created, expires?}`.
`detail` is a short excerpt (the command asking permission, the last lines of the pane),
**redacted** (env values, tokens, anything matching common secret patterns).

**Queue and channels.** Two small Go interfaces keep this pluggable without making the core heavy:

```go
type Queue interface {            // where items wait and answers come back
    Put(ctx, Item) error
    Pending(ctx) ([]Item, error)
    Answer(ctx, id, Answer) error  // first answer wins; later ones get ErrAlreadyAnswered
    Watch(ctx) (<-chan Event, error)
}
type Channel interface {          // where items are shown and answered
    Notify(ctx, Item) error
    Answers(ctx) (<-chan Answer, error) // nil if the channel can't answer
}
```

- **The item's state always lives with its run** (`~/.laight/inbox/` on the host running it). A
  queue backend *carries* items and answers; it never decides. "First answer wins" is enforced on
  the run's side with an atomic rename, so duplicate deliveries (SQS is at-least-once) are harmless.
- **Queues:**
  - `files` (default): the folder above. Zero setup, one machine.
  - `sqs` (optional): an `items` queue and an `answers` queue (FIFO, deduplicated by item id).
    Runs on several machines publish to it; `laight inbox` on any machine sees them all and its
    answers flow back. AWS credentials come from the standard AWS chain, never from laight's config.
  - Others behind the same interface later (Redis Streams, NATS, Google Pub/Sub). Each one is a
    separate package, compiled in, and off unless configured.
- **Channels** (all optional except `terminal`):
  - `terminal`: `laight inbox`, one full-screen TUI, oldest first, every run in every project.
    Keys: `a` allow, `d` deny, `1-9` pick a choice, `r` type a reply, `o` open the seat's pane,
    `s` snooze. This is the "single terminal where decisions are queued".
  - `web`: the inbox panel of `laight show` (section 7), same actions.
  - `ntfy`: a push to your phone with action buttons (self-hosted or ntfy.sh; a long random topic in
    `config.yaml`, never logged). Buttons publish to a *reply* topic that laight *subscribes* to
    (outbound SSE). Every answer must carry the item's random one-time token, and `doctor` warns
    when a public server is used without an access token: an answer can allow a command.
  - `slack`: a message with buttons in a channel or DM, using Socket Mode (an outbound WebSocket),
    so no public URL. Tokens from env or the OS keychain.
  - `webhook`: POSTs the item as JSON to a URL; answers come back with `laight inbox <id> <choice>`
    or through the queue.
  - `command`: runs any executable with the item JSON on stdin; if it prints an answer as JSON,
    that's the answer. The escape hatch for Telegram, Discord, email, a desktop notification…
  - No channel ever opens an inbound port.
- **Routing** in `~/.laight/config.yaml` (per project overrides in `.laight/laight.yaml`):

```yaml
queue: { type: files }            # or: { type: sqs, items: <queue url>, answers: <queue url>, region: eu-west-1 }
channels:
  phone: { type: ntfy, server: https://ntfy.sh, topic: laight-<random> }
  team:  { type: slack, channel: "#agents" }
routes:                           # first match wins; terminal and web always see everything
  - { kind: permission, to: [phone] }
  - { kind: gate, project: my-api, to: [team] }
  - { to: [phone], escalate_after: 10m, then: [team] }
```

- Only the title, project, run, step and choices leave the machine through a channel or queue,
  never the full detail (which can contain code or command output).
- `laight status --short` prints a summary for tmux's `status-right` (`⏳2 ▶3`).

**Rules:** an agent must never be blocked without an inbox item existing for it. An item that
expires (permission hook timeout) falls back to *deny*, never allow. `permissions: auto` + the inbox
should make `yolo` unnecessary for most workflows; that is a design goal, measure it.

## 7. Observability

- `events.jsonl` per run, one JSON object per line: `ts, run, type, step?, seat?, hop?, data`.
  Types: `run.started|finished|aborted`, `step.started|done|timeout`, `seat.up|down|crashed`,
  `inbox.raised|answered`, `hook.<name>`, `tool` (from `PostToolUse`: tool name + duration, not
  arguments), `usage` (tokens/cost per seat per step, read from the runtime's transcript).
- `laight watch` with no run: every run, one line each (project, workflow, step, hop, age, waiting items).
- `laight watch [run]`: a live CI-style view: the steps as a pipeline (✓ ● ✕ ○, ×N for loops),
  per-step duration and cost, the last events, the open inbox items. Default view of the engine window.
- The seat's pane log and the event log are plain files in the run folder (`seats/<seat>.log`,
  `events.jsonl`); `show` and `watch` render them. No extra commands for reading files.
- `laight show [run]`: the web view, **CircleCI-inspired and deliberately simple**. It serves on
  127.0.0.1 (a random free port), opens the browser, and stays in the foreground until Ctrl-C (or
  10 minutes with no browser connected). Pages:
  - **Runs**: every project's runs, newest first: project, workflow, status badge, current step,
    duration, waiting items. Filter by project and status.
  - **Run**: the steps as a horizontal pipeline of boxes (green done, blue running, red failed,
    grey pending, ×N for loops, durations), loops drawn as a back-edge. Click a step for its events,
    exits and outputs, and the tail of its seat's log. The open inbox items for the run, answerable.
  - Live updates by Server-Sent Events from `events.jsonl`. Plain HTML, CSS and a little vanilla JS
    embedded with `go:embed`: no npm, no build step, no external requests.
  - `laight show --export <run> > run.html` writes the same run page as one static file, to share.
- Optional: forward Claude Code's own OpenTelemetry to a collector; laight doesn't depend on it.

## 8. CLI (the whole surface)

Ten commands, and it stays that way. They map one to one onto the Makefile wrappers people wrote
around OpenRig (`setup`/`start`/`track`/`show`/`abort`/`answer-workflow`); a new command needs a
use that none of these covers.

```
laight init                          scaffold .laight/                                    ← setup
laight validate [workflow]           schema + semantic checks (run also does this first)
laight run <workflow> [k=v…]         spin up the rig, start the engine, follow it live     ← start
laight run --resume <run>            restart a run whose engine died (crash, reboot)
laight watch [run]                   live pipeline view; no run = every run, one line each ← track
laight show [run] [--export]         the web view (localhost, foreground)                 ← show
laight inbox [<item> <answer>]       the human queue; with arguments, answer one (scripts)  ← answer
laight attach <run> [seat]           jump into the run's tmux session (or one seat's window)
laight abort <run>                   stop a live run; on a finished one, remove its worktrees ← abort/stop
laight doctor                        tmux, git, runtimes, hooks; queue and channels (sends a test item)
laight done <exit> [k=v…]            for agents: finish the current step (the only one they learn)
```

`run`, `watch`, `attach` and `abort` take a run id prefix, or nothing when exactly one run of the
project is live. There is no "current run" bookkeeping to go stale.

Every command with side effects outside the run (deleting worktrees, killing sessions) asks first
and lists what it will do; `--yes` skips it for scripts.

## 9. Implementation

- **Language: Go, latest stable** (1.27.2 on 2026-10-15; `go 1.27` in `go.mod`, bump with each Go
  release). Pure Go, `CGO_ENABLED=0`, so the binary is static and cross-compiles. Standard layout:
  `cmd/laight/main.go`, packages under `internal/` (`spec`, `engine`, `tmux`, `runtime`, `inbox`,
  `events`, `tui`). Few dependencies (a YAML lib, a JSON Schema validator, bubbletea for the TUIs);
  prefer the standard library. `gofmt`, `go vet`, `staticcheck` clean. Target ~2,000 lines before M5.
- tmux via the CLI (`tmux -L laight` own server socket by default, so the user's own tmux sessions are
  never touched; `attach` handles the switch).
- Hooks are the same binary: `laight hook <event>` reads the hook JSON on stdin. Installed per seat via
  a generated settings file passed at launch, never into the user's `~/.claude/settings.json`.
- Tests: unit tests for spec parsing, routing, templating; an integration test with a fake runtime
  (`runtime: shell` running a script that calls `laight done`) exercising loops, gates, timeouts,
  abort and resume, inside a throwaway tmux server. Real Claude runs are manual and supervised.

### Distribution

Installing must be one command, with no runtime to install first.

- **Homebrew** is the main channel: `brew install berni23/tap/laight`, from the tap repo
  [`berni23/homebrew-tap`](https://github.com/berni23/homebrew-tap). The cask depends on the `tmux`
  formula, so brew installs it. homebrew-core comes later, once laight is stable and has users.
- **Releases with [GoReleaser](https://goreleaser.com)** (`.goreleaser.yaml`, v2): pushing a `vX.Y.Z`
  tag runs `.github/workflows/release.yml`, which builds darwin/linux × arm64/amd64, publishes a
  GitHub Release with checksums, and updates the tap's cask (`homebrew_casks`, GoReleaser ≥ 2.10).
  The tap push needs the repo secret `HOMEBREW_TAP_GITHUB_TOKEN`: a fine-grained token with
  *Contents: read and write* on `berni23/homebrew-tap` only.
- **macOS signing:** binaries are unsigned for now, so the cask's post-install hook removes the
  quarantine attribute (GoReleaser's documented approach; Apple may stop allowing it). Signing and
  notarisation with an Apple Developer ID is the proper fix once there are users.
- **Also from the same pipeline:** `go install github.com/berni23/laight/cmd/laight@latest`, plain
  binaries on GitHub Releases, and `.deb`/`.rpm` for Linux.
- `laight version` prints the version, commit and date (set with `-ldflags` at build time).
  `laight doctor` checks what brew can't: Claude Code installed and signed in, `gh` signed in,
  and tmux ≥ 3.0.
- No install scripts piped into a shell, no post-install steps.

## 10. Milestones

1. **M0** Schemas, `init`, `validate`.
2. **M1** tmux driver, rig spin-up/tear-down per run, worktrees, `on_finish`, `attach`, `abort`.
3. **M2** Engine: steps, `laight done`, routing, `max_hops`, shell steps, resume; a minimal inbox
   (the `files` queue, `laight inbox <item> <answer>`, gates, the `PermissionRequest` hook); then `needs`
   and `matrix` (parallel). Acceptance: the
   issue cycle of section 3 on a real issue in a real repo.
4. **M3** Inbox: the remaining hooks (questions, Stop), `laight inbox` (TUI), stuck detection.
5. **M4** Channels and routing: `ntfy` (phone), `command`, then `slack` and `webhook`.
6. **M5** Observability: `watch`, `show` (web view), usage/cost.
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

## 12. Hard problems, and how other orchestrators solve them

Four problems surfaced in the engine-loop review (#14) can't be fixed by a patch. Each one is a
design choice. This section records how other tools handle them (checked 2026-10, from their source
and docs) and what fits laight's rules: no daemon, plain files, tmux. Ideas only, never code (§11).
The tools are:
- **OpenRig**: a daemon with SQLite, agents in tmux. Apache-2.0. Read locally from `@openrig/cli` 0.6.7.
- **Gas Town** (`steveyegge/gastown`): Claude Code in tmux, state in "beads".
- **Archon** (`coleam00/Archon`): YAML DAG workflows that call agents through SDKs; no tmux.
- **Orca** (`stablyai/orca`, onorca.dev): an Electron app whose daemon owns PTYs. MIT.
- **Claude Squad** (`smtg-ai/claude-squad`): manages tmux sessions in git worktrees.

### 12.1 Delivering a step exactly once (#8)

The engine records a hop, then pastes it into the pane. If the engine dies in between, a restarted
engine can't tell whether the agent got the prompt. tmux gives no acknowledgement.

- **OpenRig** creates the step as a `pending` queue row in the same transaction as the workflow
  state, and sends the tmux "nudge" only after the commit, as a best effort. The agent *claims* the
  row through the CLI. A watchdog armed in that same transaction re-nudges a row that is never
  claimed. It checks a paste by looking for a text snippet in `capture-pane` (`delivered` or
  `rendered-unconfirmed`, never "failed"). A retry presses Enter alone, and only when the staged
  text is visible.
- **Gas Town** pulls instead of pushing. Work is pinned to the agent, and on `SessionStart` or
  `PreCompact` the agent re-reads it ("if you find something on your hook, you run it"). A crash
  respawns the agent and restarts the step (at-least-once). Its own code says checking the pane
  can't prove delivery.
- **Archon** skips completed nodes on resume. It won't replay a node that has a start event and no
  end event, because it "cannot prove whether its side effects finished", and asks an operator.
- **Orca** keeps a per-run inbox that the agent acks (`check --ack <deliveryId>`).
- **Claude Squad** pastes and presses Enter, and tracks nothing.

**For laight:** use OpenRig's split, with files instead of a database. The hop is written to
`run.json` as `delivered: false` before the paste, and the paste is a nudge that can be sent again.
**Acknowledgement comes from the agent's side**: a `UserPromptSubmit` hook writes
`seats/<seat>.ack` with the step and hop, so no new command is needed. On resume:
- a hop with no ack is pasted again;
- an acked hop with no `done` is left running, since the agent has it;
- re-pasting a hop that was acked is never automatic (Archon's rule): if laight can't tell, it
  raises a `stuck` item.

Runtimes without hooks fall back to OpenRig's capture-pane snippet check.

### 12.2 Knowing an agent is idle, done or stuck (#15)

Today `laight done` says the step is finished, and the Stop hook says the turn ended. Neither
detects an agent that stops without `done`, a Stop hook that fails, or a step that runs too long.

- **OpenRig** feeds hooks (`SessionStart`, `UserPromptSubmit`, `Stop`, `Notification`) into an
  activity record. "Done" is explicit: closing a step requires a reason. It never *stores* "stuck":
  it computes it from claim timestamps and a threshold. Stuck drives nudges only, never routing. An
  escalation ladder goes nudge → orchestrator → operator.
- **Gas Town**: done is explicit (`gt done`), and a session still alive 60 s after done is killed.
  It declares "stuck" only from several signals together: self-reported heartbeats, staleness of
  heartbeat files, and tmux `window_activity`. After 3 crashes on the same step it stops respawning
  and escalates.
- **Orca** ranks evidence of idleness: hooks first, then the terminal title or a known prompt, then
  a quiet screen.
- **Archon**: done means the SDK call returned. It has idle and node timeouts, and human items go
  to an attention queue where the first answer wins.
- **Claude Squad** hashes `capture-pane` every 500 ms (changed means running, unchanged means
  ready). It has no hooks and no stuck handling.

**For laight:** keep explicit `laight done` plus hooks as the proof. Screen or output silence only
ever *adds* evidence; it never decides on its own (Gas Town, Orca). Compute "stuck" at each tick
from file timestamps (`out/`, `.idle`, `.ack`, the seat log's modification time) against
`timeouts.step`. Store nothing new, and never route on it (OpenRig). The escalation is one nudge
(the Stop hook's reminder, which exists today), then a `stuck` inbox item. A seat that crashes
twice on the same hop raises a `failed` item instead of being respawned again (Gas Town's limit).

### 12.3 One parallel branch fails while others run (#12)

Today rule 4 fails the run as soon as a step fails with no route, and the teardown kills whatever
else is running. That is harmless in series. With `needs`/`matrix`, it throws away running
siblings' work, and their worktrees with it.

- **Archon** runs siblings to completion (`Promise.allSettled`). Their dependents are skipped under
  the default `trigger_rule: all_success`. A failing fan-out child "does not stop its siblings";
  only an explicit `cancel:` node stops in-flight work.
- **Orca**: each worker reports `succeeded|failed` exactly once, and a retry is explicit.
- **OpenRig** has no parallel steps yet (one active step per workflow). Gas Town and Claude Squad
  don't address it.

**For laight:** Archon's model is the strongest prior. When a parallel copy fails with no route,
running siblings finish and their exits are recorded. Every step that `needs` the failed one is
skipped, and the run fails once nothing is running, with one `failed` inbox item for the human.
Stopping the siblings stays explicit (`laight abort`, or later an opt-in `on_failure: cancel`).
**This changes rule 4 of §3**, so it is decided in #12, not here.

### 12.4 Secrets and agent panes (#7, #16)

Everything pasted into a pane is in `seats/<seat>.log` (`pipe-pane`), and in the agent's own
transcript. Since #14, `{{ secret }}` renders only into `env`.

- **Archon** keeps its secrets apart from the target repo's `.env`, encrypts credentials at rest
  and masks tokens in logs. Inputs reach bash nodes **as env vars, never spliced into the script**.
  It redacts saved values whose keys end in TOKEN, KEY, SECRET or PASSWORD, but not printed stdout.
- **OpenRig** redacts transcripts by pattern (`sk-`, `gh*_`, `github_pat_`, Bearer, long base64)
  before its API serves them. Credentials reach seats through the tmux environment, and are never
  logged.
- **Gas Town**, in its sandboxed mode, keeps the GitHub token on the host and gives each worker a
  short-lived certificate, passed by path.
- **Orca** and **Claude Squad** don't address it. Orca runs agents with permission prompts
  skipped by default.

**For laight:** keep the current rule and extend it the way Archon does. Secrets reach an agent or
a shell step only as env vars (seat `env`, or a shell step's `env`, as #16 settled it). A
secret never goes into a rendered prompt or command line, so #7 should not allow `secret` in
`run:`. Redact by key name and by pattern (OpenRig's list) in `events.jsonl` and inbox details.
The values laight knows (the secrets it rendered into seat env) can also be stripped from the pane
log as it is written, by piping `pipe-pane` through a filter instead of `cat`. **But raw pane logs
can't be fully redacted**: an agent can print whatever it can read. So pane logs stay inside the
run folder, which only the user can read (`0700`), and the docs say so plainly, instead of
promising redaction.
