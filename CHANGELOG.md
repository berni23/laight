# Changelog

All notable changes to laight are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `needs`: a step starts once every step it needs has finished (not `failed`, and out of any loop
  that could run it again), so steps on different seats run in parallel and a later step joins
  them. A needed step held on a `failed` item makes its dependents wait for the retry. A step
  whose needs can never be met is skipped when the run ends (`step.skipped` event). Steps released
  in the same tick don't depend on their order in the file. `validate` warns when a step needs a
  gate, since it runs whatever the answer. `matrix` is still refused at `laight run`.
- A seat finds `laight` on its `PATH` (a link to the binary that runs its rig comes first), so
  `laight done` works even when laight isn't installed or has another name, e.g. a build in `bin/`.
- The design specification (CLAUDE.md), project scaffolding, CI and the release pipeline.
- `laight version`.
- `laight init`: scaffolds `.laight/` with an issue-cycle workflow and guidance.
- `laight validate [workflow…]`: JSON Schema plus semantic checks (unknown seats and steps, routes,
  `needs` cycles, unreachable steps, templates), each reported with file and line.
- JSON Schemas for workflows and for `config.yaml` under `schema/`.
- `laight run <workflow> [key=value…]`: spins up the run's rig on laight's own tmux server
  (`tmux -L laight`): an engine window, then one window per seat, each in its own git worktree
  (detached at `origin/<base>`, `setup` run once) or in the repo. Claude Code seats start with a
  session id, their guidance and generated settings; their ready signal is the SessionStart hook,
  and the folder-trust dialog of a new worktree is answered for you.
  Seat env (secrets included) reaches the seat through a private file it deletes on start, never
  through a command line. A seat that crashed is relaunched, resuming its Claude conversation,
  and a failed `setup` is retried. Ctrl-C in the engine window stops the whole rig. `engine` is a
  reserved seat name. No longer needs tmux 3.2 (`new-session -e`).
- `laight attach [run] [seat]`: jump into a run's tmux session or one seat's window.
- `laight abort [run] [--yes]`: stops a live run (worktrees follow `on_finish.failure`); on a
  finished run, deletes the worktrees it kept. Lists what it will do and asks first. Safe to run
  from inside the rig's own panes.
- The engine loop: agent steps run on their seats, in series, routed on their exits
  (`next_hop.on`, `end`, loops), bounded by `loop_guards.max_hops`. A step's objective is rendered
  (`.inputs`, `.steps.<id>.out`, `.base`, `.run`) and pasted into its seat; a seat gets its next
  step once it has finished the last one and its turn has ended; `session: fresh` restarts the
  seat before each of its steps. A seat that crashes fails its step; one whose agent exits
  between steps is relaunched for its next one. `needs`/`matrix` aren't supported yet:
  `laight run` refuses a workflow that uses them.
  `timeouts` are accepted but not acted on yet.
- Shell steps (`run:`): the engine runs the command in the repo, with the workflow's and the
  step's `env` (the only way a secret reaches it), and routes on its exit code (0 → `ok`, else
  `failed`, or what `exits:` maps). Output goes to `out/<step>-<hop>.log` in the run folder, with
  the secrets its env read replaced by `***`; the code is `.steps.<id>.out.code`. The command
  runs without a terminal and doesn't inherit the engine's `LAIGHT_SECRET_*`. Whatever it leaves
  running is stopped when it exits, and the run ending (any branch failing, an abort, or an abort
  after the engine died) stops the command and whatever it started.
- The inbox, minimal: `laight inbox` lists what waits for a human in every run of every project,
  oldest first, and `laight inbox <item> <answer>` answers one (the first answer wins; a later one
  is told it was late; an item whose run ended or lost its engine is listed as such and refused;
  an item file that can't be read is reported, never taken for an empty inbox). Items are files under
  `~/.laight/inbox/`, their title and detail redacted (a long run only when it mixes upper case, lower case and
  digits, so commit SHAs and branch names survive) and stripped of control characters.
- Permission prompts go to the inbox: a Claude Code seat's `PermissionRequest` hook raises a
  `permission` item (the command, with the seat's secret env values redacted) and waits for
  `laight inbox <item> allow|deny`. Unanswered for an hour, it denies; an item answered in the
  seat's own dialog leaves the inbox by itself. A request the hook can't read (over 64 MiB, or
  malformed) is denied at once, never shown as an empty item; an expired item can't be answered.
- Gates (`type: gate`): the rendered `ask` goes to the inbox with the step's exits as choices, and
  the run routes on the answer.
- A step that fails with no route, or that `max_hops` refuses, no longer ends the run: it raises a
  `failed` item and the run waits, everything else carrying on. `retry` starts the step again
  and grants one more hop past `max_hops`; `abort` fails the run, its reason ending in
  `aborted from the inbox`. An aborted run withdraws its items.
  An answer file that can't be read raises the item again (a gate's fails the gate) instead of
  holding the run silently.
- A live run whose engine died (killed, or the tmux server gone after a reboot) is reported as
  such by `attach` and `abort` instead of looking live for ever; `laight abort` cleans it up.
  Such a run is not resumed: `laight run --resume` is deferred (#8). Its abort lists the shell
  commands it will stop (or leave running, when the boot time can't be read), and never signals a process group recorded before the last reboot
  (that pid may be one of yours now). A tmux socket laight can't open is no longer taken for a
  dead engine or a crashed seat.
- `validate` rejects `env` on agent steps and gates (an agent's env is its seat's), an
  `exits:` name that isn't in the step's `allowed_exits`, and an `exits:` code that can never
  match (`"03"`, `"256"`).
- Templates see `.run.id`, `.run.project`, `.run.workflow` and `.steps.<id>.exit`.
  `{{ secret }}` is allowed only in `env`: `validate` rejects it in objectives and other text that
  can reach a pane (and so its log).
- `laight done <exit> [key=value…]`: how an agent finishes its step, with outputs (JSON values
  allowed) for later steps' templates. Claude seats get a Stop hook that reminds the agent, once,
  if it stops without it.
- Run state under `~/.laight/runs/<id>/`: `run.json`, `events.jsonl`, and each seat's pane log.
