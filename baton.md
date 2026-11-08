# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-08

- **#63 done**: a failed run ends red. The first unrouted `failed` (or `max_hops` refusal) sets
  `failure: {step, hop, seat, copy, kind, reason, detail, at}` in run.json; nothing new starts,
  running steps finish, and the run ends. `Down` emits `run.failed` with it (an abort records
  `kind: aborted`, a rig/engine error `crash`). `laight run`/`attach` print the report (step, why,
  pane or log tail redacted, every `permission.denied`) and exit 1; checked in a real terminal.
  The failed-item hold (retry/abort, `Blocked`, `ExtraHops`) is gone. Stuck steps ask nobody: a
  stopped agent is nudged once, then fails `stuck`; a quiet one fails `timeout`; a seat whose
  turn never ends after `laight done` is stopped and relaunched by its next step.
- **#78's review answered** (PR #79): #78 was merged before the fix commit was pushed, again.
  Push review fixes before saying "merge".
- **#79's and #80's reviews answered**: the deny guard catches rules aimed at a `laight done`
  exit; the failure report is redacted where it is built, a denial makes it `permission`, a dead
  engine is `crash`, an open gate no longer holds a failed run, a reminder that can't be pasted
  fails the hop `stuck`. Template errors are `crash` in two places and `exit` in two: which kind
  they should share is the user's call.

---

## Do this next

### 1. #73, then #72: make stuck detection trustworthy on a real Claude

#63 turns a false stop into a failed run (nudge, then `stuck`). #73: check which
`background_tasks` statuses a real Claude sends (queued, failed, killed, several at once) and make
`waiting()` (cmd/laight/rig.go) match. #72: the spinner keeps the seat log moving, so a hung turn
never times out; pick a clock source the spinner doesn't touch (the transcript's mtime is the lead)
on a real run. Both need a supervised Claude session.

### 2. #67, park the inbox in code

Hide `laight inbox` and stop opening the inbox pane by default. Only questions still raise items
(gates too, until #64). Small.

### 3. #62 (credentials preflight), then #66 (rerun from failed)

§10 M3 order. #66 matters more now that runs fail instead of waiting.

---

## Waiting on the user

- Template errors (objective, `run:`, gate `ask`, matrix values): which failure kind should they
  share? Today `crash` for the first two, `exit` for the others (#80 review).
- #74: should a seat's `permissions` default to `auto`? In `default` mode every prompt is now
  denied.
- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
