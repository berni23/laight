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
  and the folder-trust dialog of a new worktree is answered for you. Steps don't run yet (M2).
- `laight attach [run] [seat]`: jump into a run's tmux session or one seat's window.
- `laight abort [run] [--yes]`: stops a live run (worktrees follow `on_finish.failure`); on a
  finished run, deletes the worktrees it kept. Lists what it will do and asks first.
- Run state under `~/.laight/runs/<id>/`: `run.json`, `events.jsonl`, and each seat's pane log.
