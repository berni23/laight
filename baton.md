# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-25

- #44 — an unquoted integer in `env` (workflow, seat, shell step) now reaches the command exactly
  (`StringMap` decodes with `UseNumber`); `validate` warns, with the line, about an unquoted env
  value YAML rewrote (`0x1F`, `1.10`, `1e3`, `+5`, ints past uint64), in shared seat files too.
  Unit-tested, plus a real `laight run` of a shell step that printed `9007199254740993`.

---

## Do this next

### 1. #15 — stuck detection (M3)

Builds on the permission hook's blocking pattern. A gate is not timed today (a 23 h wait raised
nothing): decide in #15 whether it should be. Mind #39: a seat's launch blocks the loop, and
launch time must not count as stuck.

### 2. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there.

### 3. #39 — seat launches block the engine loop

Matters once matrix copies are Claude seats with `max_parallel` > 2.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
