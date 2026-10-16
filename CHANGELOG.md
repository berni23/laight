# Changelog

All notable changes to laight are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
  between steps is relaunched for its next one. Shell steps, gates and
  `needs`/`matrix` aren't supported yet: `laight run` refuses a workflow that uses them.
  `timeouts` and a step's `env` are accepted but not acted on yet.
- Templates see `.run.id`, `.run.project`, `.run.workflow` and `.steps.<id>.exit`.
  `{{ secret }}` is allowed only in `env`: `validate` rejects it in objectives and other text that
  can reach a pane (and so its log).
- `laight done <exit> [key=value…]`: how an agent finishes its step, with outputs (JSON values
  allowed) for later steps' templates. Claude seats get a Stop hook that reminds the agent, once,
  if it stops without it.
- Run state under `~/.laight/runs/<id>/`: `run.json`, `events.jsonl`, and each seat's pane log.
