# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-22

- **v0.1 release triage** (no code changed): the bar is bug-free for a first user **and no
  breaking change after release**. `make check` green. Every open issue but #68 (the M3
  umbrella, spans tiers) and #43 (up for closing) now has a tier label (`v0.1: blocker`,
  `v0.1: should`, `after v0.1`); `contract` marks what users build on (YAML,
  `laight done`, CLI, run files), where a change after release breaks them. New issues
  #105–#114 for what had none (STABILITY.md, deny lists vs runtimes, unbuilt surface, schema URL,
  run-format version, naming freeze, Claude Code min version, README/packaging, release dry run,
  acceptance runs). **#115 (pinned) is the ordered checklist.** #82 moved after v0.1 (must stay
  optional); #48 re-scoped to failure redaction.

---

## Do this next

Work **#115** top to bottom: it is the order. Pick the first unchecked item that isn't waiting
on the maintainer (all of #115 §1 is, see below); a `contract` change says in its PR how v1 files stay compatible.

### 1. #41 + #52, agent output through `env`, never spliced into `run:`

The scaffold, README and spec §3 all splice `.steps.implement.out.pr` into a shell command.

### 2. #64, the `human:` policy, v0.1 scope

`assume` and `fail`, gate `default:`, `assumptions` in `laight done`; no `wait`/`supervisor` in
the enum yet. Needs the maintainer's call on the default first (see below).

---

## Waiting on the user

- **v0.1 design calls** (#115 §1): the v1 naming/defaults pass (#110); `human.mode`'s default or
  a required `human:` (#64); deny-rule syntax vs non-Claude runtimes (#106); then STABILITY.md
  (#105), written once #110 settles the vocabulary it describes.
- Add the `HOMEBREW_TAP_GITHUB_TOKEN` Actions secret (#113).
- Close #43? Obsolete since #63 (comment there).
- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? Still undecided (2026-11-14); `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- #88: on the next supervised `background-hang` run, check `pgrep -fl "ping -c 900"` once it's red.
