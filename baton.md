# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-27

- **#117: `.inputs` rejected in `run:` too** (maintainer's call on the review's open question):
  only what laight itself knows (`.exit`, `.base`, `.run`, literal matrix lists) is spliced into
  `run:`; inputs go through `env` like outputs, since they may come from a ticket later and a
  value with a space splits anyway. Same call kept the rule an error with no warning period, and
  plain-string matrix values rejected. New rows in `TestLoadProblems`, red without the check;
  §3, CHANGELOG and schema updated.

## Session before — 2026-11-22

- **v0.1 release triage** (no code changed): the bar is bug-free for a first user **and no
  breaking change after release**. `make check` green. Every open issue but #68 (the M3
  umbrella, spans tiers) and #43 (up for closing) now has a tier label (`v0.1: blocker`,
  `v0.1: should`, `after v0.1`); `contract` marks what users build on (YAML,
  `laight done`, CLI, run files), where a change after release breaks them. New issues
  #105–#114 for what had none (STABILITY.md, deny lists vs runtimes, unbuilt surface, schema URL,
  run-format version, naming freeze, Claude Code min version, README/packaging, release dry run,
  acceptance runs). **#115 (pinned) is the ordered checklist.** #82 moved after v0.1 (must stay
  optional); #48 re-scoped to failure redaction.
- **#41 + #52 done**: `validate` rejects agent output in a shell step's `run:` (`.steps.<id>.out`;
  `.steps`, `.` or `$` whole; a matrix value rendered from a template), an error, not a warning,
  so it can be relaxed later without breaking anyone. Scaffold, README and spec §3 use
  `env: { PR: … }` + `"$PR"`. New cases in `TestLoadProblems` / `TestMatrixErrors`, all checked
  red with the check disabled. `TestEngineShellRenderFailure` now fails on `.steps.two.exit`
  (a step that hasn't run), since `.out` in `run:` no longer gets past `validate`.
- **#117 review answered** (`/cross:fix-cr`): the engine now fails a hop whose exit the step
  doesn't allow (an agent can write its out file itself), so `.exit` in `run:` is safe; the
  `run:` hint names one output and runs the validity checks first; pins for the template
  walker; §3, CHANGELOG (marked Breaking) and schema say `.out.code` and `.matrix` whole are
  rejected. Filed #119 (`runtime: shell` objectives are shell input) and #120 (gate `ask`
  printed unscrubbed); flag injection noted on #82.

---

## Do this next

Work **#115** top to bottom: it is the order. Pick the first unchecked item that isn't waiting
on the maintainer (all of #115 §1 is, see below); a `contract` change says in its PR how v1 files
stay compatible. #64 waits on the maintainer's call on the default, so:

### 1. #108, pin the editor schema URL to the binary's version

`laight init` writes `…/laight/main/schema/…`; write the version tag instead (`dev` builds keep
`main`), and the schemas' `$id`.

### 2. #109, version the run format

A format version in `run.json` and the events; `--from-failed` and `abort` refuse a format they
don't know.

### 3. #107, don't ship unbuilt or parked surface

The parked `config.yaml` / `laight.yaml` schema, and anything still pointing users at the inbox.

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
