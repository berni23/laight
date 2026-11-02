# laight

A lightweight framework for running teams of coding agents (Claude Code first, Codex later) in
**tmux**, driven entirely by **YAML**: a workflow file declares its agents (seats) and its steps, in
series or in parallel; starting it spins up a **rig** of those agents, which dies when it's done.
Every run is **isolated** and **unattended by design**: what a run needs from a human (permissions,
credentials, answers, decisions) is declared or settled before it starts, so nobody has to watch
agents; a run ends green or red, and a red one says where and why, like a CircleCI pipeline.

This file is the spec. README.md is the user-facing overview. Background, research and details for
later milestones are parked in `scratch.docs/scratch.spec_parked.md` (local, untracked).
Status: **M1 done** (`init`, `validate`, `run`, `attach`, `abort`: rigs spin up and tear down);
**M2 done**: the engine loop runs agent and shell steps (`laight done`, routing, `max_hops`),
gates, `needs` and `matrix` (#12), and a minimal inbox (`files` queue, `laight inbox`, `gate`,
`failed` and `permission` items, the last from the `PermissionRequest` hook). The acceptance run
passed (#11: two real issues to merged PRs, one review/fix round each); `question` items followed
(#55). **Direction change (#68, 2026-11-02): unattended by design.** The inbox is parked, not
deleted (§6.4, #67), and **M3** is now the unattended model (§10): deny lists (#61) and failure
reports (#63) are next. Where this spec describes that model, the code catches up issue by issue:
until #63 lands, a failed step still holds on a `failed` inbox item instead of ending the run.
**Starting a session? Read [`baton.md`](baton.md) after this file** (or run `/pickup`): what the
last session finished and what to pick up next.

The four hard requirements, in order of priority. Anything that compromises one is wrong:

1. **Lightweight.** One binary, no daemon, no database server, no always-on server. State is plain
   files. A user learns 9 commands (section 8), an agent learns 1. The one exception is `laight show`, a
   read-only web view bound to 127.0.0.1 that runs in the foreground only while you look at it.
2. **tmux.** Every agent is a real interactive CLI session in a tmux pane that a human can attach to,
   read and type into at any time.
3. **Isolation via rigs.** A rig is the group of agents that executes one run of a workflow. It
   spins up when the workflow starts and dies when it finishes, with its own tmux session, git
   worktrees and state folder. Many rigs coexist in a project; they never share a working tree or a
   terminal. Everything a rig does is declared in the workflow's YAML, as in Kubernetes or CircleCI.
4. **Unattended by design.** The point of laight is to cut the interaction between a human and
   the agents as far as it goes. Everything a run needs (permissions, credentials, answers,
   decisions) is declared in the workflow or obtained before the run starts; **if an agent needs a
   human mid-run, the entry point (issue, ticket, prompt) wasn't specified enough**. When a question
   comes up anyway, the agent **assumes** by default: it picks the most reasonable answer, records
   the assumption and carries on, and the assumptions are reviewed on the PR. A workflow can
   instead fail fast, wait in the pane, or hand questions to a supervisor seat (§6). A run is
   green or red; a red one names the step that failed and why, and is rerun from that step.
   Humans don't disappear, they move: logins to the start of the run, merge approvals into declared
   gates, the review of assumptions onto the PR.

---

## 1. Where this comes from

laight comes from a working [OpenRig](https://github.com/mvschwarz/openrig) issue cycle the
maintainer ran on a Go project (`CLAUDE.local.md` has the details): an implementer and a reviewer
seat, `implement → review → fix → verify → merge`, unattended, issues to merged PRs with no human
typing. The idea worked; the machinery hurt, and each pain became a rule here:
- a daemon on a local port → **no daemon**, state in files;
- ~350 commands → **9 commands**, and agents learn only `laight done`;
- prompts stuck in panes nobody watched → **nothing waits on a human mid-run** (first answered
  with an inbox, now parked: a queue of prompts still needs someone watching it, §6);
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
- **Human policy**: what a run does when an agent needs a human anyway (`human:`, §6): assume,
  fail, wait, or ask a supervisor seat. Permissions are deny lists (`permissions.deny`), access is
  a credentials preflight; neither ever asks mid-run.
- **Failure report**: why a red run is red: the step, hop, seat, kind and reason (§6, #63).

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
  laight.yaml                     # optional: per-project overrides (parked: inbox routing)
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

human: { mode: assume }           # assume (default) | fail | wait | supervisor: §6 (#64)
permissions:                      # what no seat may ever do; auto mode judges the rest (#61)
  deny: ["Bash(git push --force:*)", "Bash(gh pr merge:*)"]

seats:                            # the rig: exactly these agents are started for a run
  impl:
    label: Implementer
    runtime: claude-code          # claude-code | codex | shell (any command)
    cwd: worktree                 # worktree (own per run, default) | repo (the main checkout) | <path>
    session: resume               # resume | fresh
    permissions: auto             # default | auto | yolo   (yolo prints a warning at `run`);
                                  # or { mode: auto, deny: [...] }, added to the workflow's list
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

  - id: approve                   # a declared human decision; delete it for unattended merges
    type: gate
    ask: Merge {{ .steps.implement.out.pr }} into {{ .base }}?
    allowed_exits: [yes, no]
    default: no                   # taken when no human answers (#64); none → the run fails
    next_hop: { on: { yes: merge, no: end } }

  - id: merge
    type: shell                   # deterministic step, no agent
    run: gh pr merge {{ .steps.implement.out.pr }} --squash && gh issue close {{ .steps.implement.out.issue }}

loop_guards: { max_hops: 11 }     # exceeding it fails the run (rule 4)
timeouts: { step: 90m }           # a step quiet this long is stuck: it fails (§12.2)
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
   non-`failed` exit, and no queued or running step can route back to it (a step in a
   review/fix loop counts once the loop has left it); nor can another step whose needs are met
   in the same tick, unless this one can too (both are in that loop), so file order never
   matters. It runs at most once, even if a loop later runs a needed step again. Any non-`failed`
   exit counts, a gate's `no` too: to act on a decision, route on the gate's exit (`validate`
   warns). One whose needs can't be met by the time the run ends is skipped (`step.skipped`);
   the run still succeeds.
3. A finished step's `next_hop.on[<exit>]` starts that step (series, branches and loops). A step
   without `next_hop` ends its branch; `end` ends it explicitly.
4. The run succeeds when no step is running or ready. An unrouted `failed`, or a step `max_hops`
   refuses, **fails the run** (#63): nothing new starts, steps already running finish and their
   exits are recorded (§12.3), then the run ends red with its failure report (§6) and worktrees
   follow `on_finish.failure`. It also fails on `laight abort`. A failed run is retried by
   **rerunning it from the failed step** (`laight run --from-failed`, #66), after fixing the cause.
   (This reverses #9, which held the step on a `failed` inbox item for retry or abort while the
   rest carried on: a held run needs someone watching it, and once a rerun is cheap a red run
   costs nothing to continue. Decided in #60, 2026-11-02.)
5. One seat runs one step at a time; a step waiting for a busy seat queues for it. Matrix copies get
   their own seat instance (`dev#1`, `dev#2`…), with its own window, worktree and session, started
   with its copy (so the seat must be `cwd: worktree`); a seat only matrix steps use never starts
   on its own. The window closes once the copy's turn ends; the worktree follows `on_finish`. `max_parallel` caps live copies (default 4).
6. Routes and `needs` don't mix: a step with `needs` can't be a route target, and a matrix step
   can't have `next_hop`. `.steps.<id>.out` is the latest hop's output. A hop is one step start
   (each matrix copy counts). `validate` enforces all of this.

**Matrix** (decided in #12): keys sorted, then the cartesian product in list order, numbers the
copies `#1…#N`. A value is a list of scalars, or a template: one that is a single action
(`{{ .steps.plan.out.tasks }}`) gives its value, anything else must render a JSON list. An empty
list, a non-scalar or more than 32 copies fails the step before any copy starts (`validate`
rejects it already when every value is a literal list). A failed copy fails the run once its
running siblings finish; a rerun from failed reruns the failed copies only.
Instance `dev#k` serves copy k of every matrix step on `dev`, and of every later expansion: it
keeps its worktree and, unless `session: fresh`, its conversation, like any seat. A gate can't
have a matrix. Later steps see a matrix step's `.out` as a **list**, one entry per copy
in copy order, and `.exit` as the list of exits (Argo's `withParam` model, not "the last copy to
finish wins"): `{{ range .steps.build.out }}{{ .pr }}{{ end }}`; `validate` rejects `.out.<key>`.

**Step types:** `agent` (default when `seat` is set), `shell` (`run:`; exit 0 → `ok`, else
`failed`, unless `exits:` maps codes), `gate` (a decision the workflow asks a human for: answered
in the engine window under `human.mode: wait`, else it takes its `default:` exit, §6).
A **shell step** has no seat: the engine runs `sh -c '<run>'` in the repo's main checkout, in its
own session with no terminal (a command that prompts fails instead of hanging), with the workflow's
and the step's `env`. Whatever it leaves running is stopped when `sh` exits or the run ends. Output
goes to `out/<step>-<hop>.log` with known secrets redacted; the exit code is `.steps.<id>.out.code`.
**A step's `env` exists only on shell steps** (#16): an agent's env is its seat's. Secrets reach a
shell command only through `env`, never spliced into `run:` (§12.4). An unquoted env value is
passed on as YAML read it: integers exactly, but `0x1F` as `31` and `1.10` as `1.1`, so quote a
value you need verbatim (`validate` warns).
**`compact: true`** on an agent step pastes `/compact` before the objective, which Claude queues
until compaction is done (checked on 2.1.310: compacting fires `SessionStart`, not `Stop`, so the
step still ends with `laight done`). Only on a claude-code seat with `session: resume`. On a
seat's first step there is nothing to compact: Claude says so and runs the objective anyway.
**Exits:** any identifiers; `allowed_exits` defaults to `[handoff]`; `failed` is implicit and can
be routed. **`next_hop.mode: end`** ends the branch whatever the exit (an unrouted `failed` still
fails the run). An agent fails a step with `laight done failed reason="…"`: the reason goes into
the failure report. **Outputs:** `laight done <exit> key=value…` sets `.steps.<id>.out.<key>` (JSON values
allowed). Templates see only `.inputs`, `.steps.<id>.out`/`.exit`, `.matrix`, `.base`,
`.run.id`/`.project`/`.workflow` and, **in `env` only**, `secret`: every other template can be
pasted into a pane, and panes are logged.

## 4. Runtime layout

```
~/.laight/                            # global: every project's runs in one place
  projects.json
  runs/<run-id>/
    run.json                          # spec snapshot, inputs, status, current step, hops,
                                      #   and a red run's `failure` (§6.3)
    events.jsonl                      # append-only, the source of truth for observability
    out/<step>-<hop>.json             # what `laight done` wrote (or the engine, for shell steps)
    out/<step>-<hop>.log              # a shell step's output, known secrets redacted
    seats/<seat>.log                  # tmux pipe-pane of the seat
    seats/<seat>.idle, .stopped       # Stop hook: turn ended after `done`; stopped twice without it
    worktrees/<seat>/
  inbox/<item-id>.json                # parked (§6.4): one file per pending item
  inbox/answered/<item-id>.json
  config.yaml                         # defaults (parked: queue, channels, routing)
```

- Run ids are ULIDs. Writes are atomic (temp + rename). The only lock is `flock` on `run.json`.
- **The engine** is `laight engine <run>` in window 0 of the rig's tmux session: a loop of read
  state → act → append event → sleep. When the run finishes it applies `on_finish`, stops the seats
  and kills the session.
- **When the engine dies**, the run is lost, not resumed: `attach` and `abort` report "engine
  died", `laight abort` cleans up, worktrees follow `on_finish.failure`, a new run starts over.
  **Resume is deferred (#8)**: rerun from failed (#66) comes first and may later cover a dead
  engine too; the state stays in files so it remains possible.

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
  `seats/<seat>.stopped` instead of looping, and the step is stuck (§12.2). A seat
  gets its next step only after `done` *and* its Stop hook.
- **Fresh vs resume:** `fresh` restarts the CLI before each step (`/clear` is not enough);
  `resume` keeps the session. Every Claude session gets a `--session-id`, so a seat relaunched
  within a run continues its conversation with `--resume`.
- **Guidance and settings** are passed at launch (`--append-system-prompt-file`, `--settings`),
  never written into tracked files, `CLAUDE.md` or `~/.claude/settings.json`. The settings allow
  `Bash(<abs path to laight> done:*)` and `Bash(laight done:*)` (what agents type). Permission
  modes map to `--permission-mode`: `default` → `manual`, `auto` → `auto`, `yolo` →
  `bypassPermissions`. The workflow's and the seat's `permissions.deny` go into the same settings
  file's `permissions.deny` (#61).
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

## 6. Humans: before the run, not during it (the point of the project)

Decided in #60 (2026-11-02), umbrella #68. Every moment a run would need a human is moved out of
the run: settled before it, declared as policy in the workflow, or reported after it. The
companion plugins (berni23/laight-plugins, §11) cover the human ends: **`/laight:plan`** turns
an entry point (issue, ticket, prompt) into a brief, deny lists and credentials before the run;
**`/laight:postmortem`** turns a red run's report into the fix.

### 6.1 Before the run

- **Permissions are deny lists on auto mode** (#61), never allowlists: a complete list of what a
  seat may do can't be written in advance, so the workflow says what agents must never do
  (`permissions.deny`, workflow-wide plus per seat), Claude blocks a matching call itself, and
  auto mode judges the rest. A request that would still ask a human (auto mode escalating, a seat
  in `default` mode) is **denied at once** by the `PermissionRequest` hook, which tells the agent
  no human is available and records the denial (tool and input, redacted) on the step. The step
  doesn't fail on its own; the agent decides. Every denial is in the run report, green runs too.
- **Credentials are a preflight** (#62): `credentials: { <name>: { check, login?, min_valid? } }`.
  `laight run` runs every `check` before the rig spins up; a failing one with a `login` runs it in
  the user's terminal (the one human moment, at the start) and checks again; otherwise the run
  doesn't start. A credential that expires mid-run fails its step, never hangs.
- **Declared gates stay**: they are decisions the YAML asks for, not gaps in the spec.

### 6.2 During the run: the `human:` policy (#64)

```yaml
human:
  mode: assume        # assume (default) | fail | wait | supervisor
  timeout: 15m        # for wait, and for a supervisor's escalation
```

| Mode | A question (`AskUserQuestion`) | A gate |
|---|---|---|
| `assume` | denied by the hook: the agent picks the most reasonable answer, records it (`laight done … assumptions='[…]'`) and carries on; assumptions go into the run report and the PR description | takes its `default:` exit; none → the run fails (`kind: question`) |
| `fail` | the step fails, `kind: question`, the question as its reason | as `assume` |
| `wait` | answered in the agent's own pane (Claude's dialog, found with `laight attach`); unanswered within `timeout` → the step fails | answered in the engine window; unanswered within `timeout` → `default:`, else fails |
| `supervisor` | a turn for the supervisor seat (#65), which answers or escalates | as `wait` |

Permissions are never a question: denied and recorded in every mode, the supervisor's included
(§6.1). `assume` is the default so runs finish instead of failing on every gap; how often an
assumption turns out wrong decides whether it stays the default (measure it). **The supervisor**
(#65, built last) is a seat with the run's context that answers questions from the brief and is
the one place a human can talk to the run live; its answers are recorded like assumptions.

**Rules:** an agent is never blocked on a human nobody was asked for: every hook answers at once
except under `wait`, whose clock is the stuck clock (§12.2). Whatever times out falls back to
*deny* or *fail*, never *allow*. `permissions: auto` + deny lists should make `yolo` unnecessary
for most workflows; that is a design goal, measure it.

### 6.3 After the run: the failure report (#63)

A run is green or red. A red run's `run.json` holds `failure: {step, hop, seat, kind, reason,
detail, at}`, `kind` one of `exit` (`laight done failed reason=…`), `permission`, `question`,
`credential`, `timeout` (quiet past its timeout), `stuck` (stopped twice without `laight done`),
`crash`, `max_hops`, `shell`, `aborted`; `detail` is the last lines of
the pane or the shell step's log, redacted (§12.4). A `run.failed` event carries the same.
`laight watch` shows ✕ and the reason on the failed step, and `laight run` exits non-zero and
prints the report. Every run's report also lists the denied requests and the assumptions made.

### 6.4 Parked: the inbox (#67)

The inbox, M3's first design, is **parked, not deleted**: it may come back (phone, Slack, a team
inbox) once runs are unattended by default. Its code stays, builds and passes its tests, and its
hooks are what #61, #64 and the supervisor start from. `laight inbox` is hidden (out of `--help`,
the README and §8) but still works, and `laight run` stops opening the inbox pane by default (#67).
What follows is the design as built, kept as the reference for those hooks.

| Kind | Source | Answer goes back via |
|---|---|---|
| `permission` | Claude Code `PermissionRequest` hook | the hook blocks, polling the item file, and returns allow/deny |
| `question` | `PermissionRequest` hook on `AskUserQuestion` (#55); a plain-text question caught by the Stop hook (not built: the Stop hook's reminder steers agents to the tool) | the hook returns the answers; for plain text, pasted into the seat |
| `gate` | a `type: gate` step | the engine routes on the chosen exit |
| `stuck` | Stop hook gave up; step quiet past its timeout; turn never ended after `done` | nudge / fail when the agent stopped; wait / fail otherwise |
| `failed` | unrouted `failed`, `max_hops` exceeded, seat crashed | retry step / abort run |

An item: `{id, run, repo, workflow, step, seat, hop, kind, title, detail, choices[], created,
expires?, answer?, answered?, match?, questions[]?, answers[]?, picks[]?}`. `detail` is a short excerpt (the command, the last lines of
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

**Question items** (#55, verified against Claude Code 2.1.310): every seat may ask a human with
`AskUserQuestion`, in every permission mode (`yolo` included, it still fires `PermissionRequest`).
The hook raises a `question` item, not a permission: its title is the question (`<seat> asks:
…`), it carries the questions and the agent's options (labels and descriptions, redacted like
the title, never cut), and it has no fixed choices. `laight inbox` lists the options numbered,
`laight inbox <item>` shows the descriptions too, and the answer is one per question: an option's
number (several, `1,3`, for a multi-select), or anything else typed, which Claude takes as written.
The item records the numbers picked (`picks`), and the hook maps them to the agent's own labels by
position, since redacted labels can look alike; typed words go as typed. The hook returns allow
with the tool's input plus `answers` keyed by each question as the agent wrote it, which Claude
takes as the user's answer. The answered item keeps the answer as typed, for the hook; the
`inbox.answered` event gets it redacted. A call laight can't read as questions is denied at once,
saying why: allowed, it would leave Claude's own dialog in a pane with no item to answer. Answered in the pane instead, PostToolUse's
input has gained `answers` and `annotations`, which the match ignores, so the item is withdrawn.
An unanswered question expires like a permission: denied. The engine window prints every item a
seat's hook raises (the question and its options, or the command) with how to answer it.
A question's `detail` is what the agent said in its turn before asking (#55, checked on 2.1.314):
its text and one line per tool it ran, from the transcript (`transcript_path`), then its last
message, from its screen, since Claude writes the question's own message to the transcript only
once it is answered. It is redacted, then the last 1500 bytes are kept (a cut secret no longer
matches, so whatever cuts text before the queue scrubs it first); like any detail it never leaves
the machine.

**The run's inbox pane** (#55): `laight run` splits the engine window and runs
`laight inbox --follow <run>` under the engine, with the focus. It prints each of the run's items
in full as it arrives, and answers the oldest with each line typed (another one when the line
starts with its id; a choice may be shortened to `y`, `a`…). When the item the prompt named goes
before a line reaches it, that line is refused, never given to the next item. It ends with the
run, and says so when its engine dies. A window's
command is its first pane, so the engine's liveness, capture and keys never go to the inbox pane.
A trial: a popup or the engine window itself may replace it.

- The item's state always lives with its run. A queue backend only *carries* items and answers.
  First answer wins, enforced with an atomic rename, so duplicate deliveries are harmless.
- `internal/inbox` is the `files` queue as plain functions; a `Queue`/`Channel` interface is
  extracted when a second backend arrives. Every backend besides `files` and `terminal` is
  optional, compiled in, and off unless configured.
- No channel ever opens an inbound port. Phone/chat answers carry the item's one-time token.
  Only title, project, run, step and choices leave the machine, never `detail`. A question's
  option labels count as choices (redacted, as the inbox shows them); their descriptions stay
  local like `detail` (decided in the #56 review).
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

Its rule, while it was the model: an agent was never blocked without an inbox item existing for
it, and an expired item fell back to *deny*, never allow.

## 7. Observability

- `events.jsonl` per run: `ts, run, type, step?, seat?, hop?, data`. Types:
  `run.started|finished|failed|aborted` (`failed` carries the failure report, §6.3),
  `step.started|done|skipped|timeout`, `seat.up|down|crashed`, `permission.denied` and
  `assumption` (#61, #64), `inbox.raised|answered` (parked), `hook.<name>` (session start and
  stop), `tool` (name + duration, never arguments), `usage` (tokens/cost per seat per step).
- `laight watch`: every run, one line each. `laight watch <run>`: a live CI-style pipeline
  (✓ ● ✕ ○, ×N for loops), durations, cost, last events, and a red run's failure report. The
  engine window shows it.
- `laight show`: the same as a CircleCI-like web page on 127.0.0.1, live via SSE, plain HTML
  embedded with `go:embed` (no npm, no external requests); `--export` writes one static file.

## 8. CLI (the whole surface)

Nine commands, and it stays that way; a new command needs a use none of these covers. (A tenth,
`laight inbox`, is parked: hidden, still working, §6.4.)

```
laight init                          scaffold .laight/
laight validate [workflow]           schema + semantic checks (run also does this first)
laight run <workflow> [k=v…]         preflight credentials, spin up the rig, follow it live;
           --from-failed <run>       rerun a red run from its failed step (#66)
laight watch [run]                   live pipeline view; no run = every run, one line each
laight show [run] [--export]         the web view (localhost, foreground)
laight attach <run> [seat]           jump into the run's tmux session (or one seat's window)
laight abort <run>                   stop a live run; on a finished one, remove its worktrees
laight doctor                        tmux (≥ 3.1), git, runtimes, hooks
laight done <exit> [k=v…]            for agents: finish the current step (the only one they learn)
```

Run arguments take an id prefix, or nothing when exactly one run of the project is live. Every
command with side effects outside the run asks first and lists what it will do; `--yes` skips it.

## 9. Implementation

- **Go, latest stable** (`go 1.27`), pure Go, `CGO_ENABLED=0`. `cmd/laight/`, packages under
  `internal/`. Few dependencies (YAML, JSON Schema, bubbletea); prefer the standard library.
  `gofmt`, `go vet`, `staticcheck` clean. Target ~2,000 lines before M4.
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
4. **M3** Unattended (#68, in this order): deny lists on auto mode (#61) and the failure report,
   a failed run ending (#63); the credentials preflight (#62); rerun from failed (#66), before
   anything makes runs fail more often; the `human:` policy and gate defaults (#64); parking the
   inbox (#67). With `/laight:plan` and `/laight:postmortem` in berni23/laight-plugins.
   Stuck detection (§12.2) and `question` items (#55) were built before the change.
5. **M4** Observability: `watch`, `show` (#58: the pipeline, click a step for its agent's
   terminal), usage/cost.
6. **M5** Supervisor seat (#65), once real runs show which questions come up.
7. **M6** Codex runtime; `shell` runtime for arbitrary CLIs.

**Parked** (the old M3–M6, §6.4): the inbox TUI; channels and routing (`ntfy`, `command`,
`slack`, `webhook`); the `sqs` queue.

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
- No secrets in logs, events, failure reports, inbox details or anything that leaves the machine.
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
- **Companion plugins live in [berni23/laight-plugins](https://github.com/berni23/laight-plugins)**
  (Claude Code plugins: `baton`, `cross`, and laight's own, such as a workflow planner). A skill
  that works alongside laight but isn't part of the binary goes there, not here; this repo
  refers to it by plugin and skill name. Check that repo before writing a new one.
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
hop is never automatic (Archon's rule): the hop is stuck (§12.2). A hop whose seat died while
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
reminder; skipped if it has since reached a permission dialog, whose item already asks) or
`fail`; no `wait`, which would wait for ever. A quiet seat may be showing a dialog
that a paste's Enter would answer: `wait` (the clock starts again) or `fail`; so does a shell
step. `fail` ends the hop `failed` and routes (a shell step's command is stopped; a seat whose step
is already done is just freed). A quiet seat's turn may still be running, so it takes its next
step only after its Stop hook (a late `laight done` from that turn then finds no step to finish);
a seat without hooks is interrupted (`C-c`) instead. Looking first is `laight attach`, which the item's detail names. A
seat with a pending permission or question item is never stuck, and a gate is never timed: a human is already
asked. An item whose turn ends on its own is withdrawn.
A crashed seat fails its hop at once and is respawned only by a later step (or a rerun).

**Unattended (#60), decided on #63 (2026-11-02):** nobody is asked any more. A stopped agent is
nudged once by the engine itself (the same reminder, pasted), and fails its hop (`kind: stuck`)
if it stops again; a quiet seat or shell step fails its hop (`kind: timeout`), since a paste
could answer a dialog nobody has read, and the report's pane lines show what it was waiting on.
Two kinds, because the fixes differ: the objective or guidance for `stuck`, a hang or a dialog
for `timeout`. Failing on a false stop would kill a healthy run, so #57 (a turn that ends while
background agents run looks like a stop) is fixed before or with #63.
Under `human.mode: wait` the `stuck` item is still raised as above, with `human.timeout` as its
clock. Until #63 lands the code raises the item in every mode.

### 12.3 One parallel branch fails while others run (#12)

Archon's model: when a parallel copy (or any step) fails with no route, running siblings finish
and their exits are recorded, nothing new starts, and the run ends red (rule 4 of §3); the steps
that `needs` it never start. Letting siblings finish, unlike CircleCI's cancel, keeps their work
for a rerun from failed (#66), which reruns only what failed (decided in #12, 2026-10-21; held
on a `failed` item until #60 reversed that, 2026-11-02). Stopping siblings stays explicit
(`laight abort`, or later an opt-in `on_failure: cancel`).

### 12.4 Secrets and agent panes (#7, #16)

Secrets reach an agent or a shell step only as env vars (seat `env`, or a shell step's `env`),
never in a rendered prompt or command line. Redact by key name and by pattern in `events.jsonl` and
inbox details; a bare long run counts only when it mixes upper case, lower case and digits, so
commit SHAs and branch names survive. Known secret values can also be stripped from the pane log as
it is written (`pipe-pane` through a filter). **Raw pane logs can't be fully redacted** (an agent
can print whatever it can read), so they stay in the run folder (`0700`) and the docs say so
plainly instead of promising redaction.
