# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-25

- #15 — stuck detection: a step quiet past its `timeout` (pane log, worktree writes), an agent
  that stops twice without `laight done` (Stop hook's `.stopped` mark), a seat whose turn never
  ended after `done`, and a quiet shell step each raise a `stuck` item (`nudge | wait | fail`;
  shell: `wait | fail`). Integration + unit tests, mutation-checked; a real Claude Code 2.1.310
  run exercised the second stop, nudge, wait (idle pane stays quiet, timeout fired) and fail.
  Choices decided on #15 and written into CLAUDE.md §6/§12.2.

---

## Do this next

### 1. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there. The other M3 hook;
the stuck item's pattern (engine-raised item, answer acted on in `watch`) is reusable for the
plain-text question the Stop hook catches.

### 2. #39 — seat launches block the engine loop

Matters once matrix copies are Claude seats with `max_parallel` > 2. Read the #15 comment on it
first: async launches must make `watch` skip a seat still launching.

### 3. M3: `laight inbox` (TUI)

The last M3 piece once questions land; every item kind now exists to show.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
