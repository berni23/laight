# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-22

**#12 done: `needs` and `matrix`, so M2 is done.** In two stacked PRs.
- **`needs`:** a need counts once it has finished and no live step can route back to it, which
  covers loops.
- **PR #38 review answered** (`/cross:cr` → `/cross:fix-cr`): steps whose needs are met in the
  same tick are judged on the pre-tick state, so file order doesn't change what starts; another
  ready step that can loop back to a need holds it unless this one is in that loop too.
  `validate` warns on `needs` of a gate. Rule 2 says a needs step runs at most once.
- **`matrix`:** each copy runs on its own seat instance `dev#k`, closed after its copy. A failed
  copy is held and retried alone. `.out`/`.exit` are lists. Rebased onto the #38 fixes: no code
  conflict (matrix never touched the 1b release; a running matrix counts as live there).
- **Real run** (2 Haiku copies, `permissions: default`, throwaway repo): each instance got its own
  worktree and window, and its permission item carried `dev#1`/`dev#2`. `range` rendered both
  outputs, and `dev#2`'s window closed after its Stop hook. `laight attach … dev#2` wasn't tried
  (no terminal); the same `session:dev#2` target took the paste.
- **A review agent found two bugs, both fixed with regression tests:** a held entry matrix was
  re-raised every tick, and a second route re-expanded a running matrix.
- **Decided on #12:** no matrix on gates; at most 32 copies. Dropped there: rejecting `.exit`
  "used as a string" (`validate` can't see how a field is used).

---

## Do this next

### 1. PR #40 — answer its review

`/cross:fix-cr 40`: review 5621587565 (one medium, the serial seat-instance start in
`rig.go` blocking the loop, see #39; four low) has no replies yet. Merge #38 first, then #40.

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

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
