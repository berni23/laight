# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-25

**PR #40 (matrix) review answered** (`/cross:fix-cr`; #38 is merged). Fixed, each with a
regression test seen red first:
- A rendered matrix list and a `laight done` output must be exactly one JSON value: trailing text
  or a stray `]` was silently dropped (`spec.DecodeJSON`, shared).
- `validate` rejects a matrix agent step whose seat isn't `cwd: worktree`, and a literal matrix
  over 32 copies.
- `inbox.raised` carries the seat, hop and copy it holds.
- Spec: instance `dev#k` keeps its worktree (and conversation unless `fresh`) across expansions.
- Filed #41 (agent output spliced into a shell step's `run:`); the serial launch is #39.

---

## Do this next

### 1. PR #40 — two review questions, then merge

The user decides (see the PR's replies): max_hops refusing every pending copy, one `failed` item
each; and whether a held copy's window stays open until its item is answered. Then retarget #40
to `main` (its base `12-needs-matrix` was merged in #38) and merge.

### 2. #15 — stuck detection (M3)

Builds on the permission hook's blocking pattern. A gate is not timed today (a 23 h wait raised
nothing): decide in #15 whether it should be. Mind #39: a seat's launch blocks the loop, and
launch time must not count as stuck.

### 3. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there.

### 4. #39 — seat launches block the engine loop

Matters once matrix copies are Claude seats with `max_parallel` > 2.

---

## Waiting on the user

- PR #40's two questions (threads on `engine.go` max_hops, and the review body's held-copy
  window): asked in the reply comment there.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
