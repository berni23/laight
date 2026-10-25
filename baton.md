# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-22

**#12 part 1 — `needs` works.** A step with `needs` is queued once every needed step finished
non-`failed` *and* no live step can route back to it (so `needs: [verify]` on a review/fix loop
waits for the loop to leave verify; the plan's simpler check would have released it on the first
`fail`). Held needs make dependents wait; a retry releases them. Unmet needs at the end →
`step.skipped`, run succeeds. Spec §3 rule 2 and §7 amended. `matrix` is still refused by
`engine.Supported`.

**PR #38 review answered** (`/cross:cr` → `/cross:fix-cr`). Steps whose needs are met in the same
tick are now judged on the pre-tick state, so file order no longer changes what starts; another
ready step that can loop back to a need holds it unless this one is in that loop too (the review's
"count the others" shape deadlocked two loop members). `validate` warns on `needs` of a gate.
Rule 2 says a needs step runs at most once. `mode: end` finding rejected (check.go forbids it
with `on`).

**#12 decided** (on the issue): `validate` rejects `matrix` on a gate; at most 32 copies, more is
an expansion error (held, retry/abort).

---

## Do this next

### 1. #12 part 2 — `matrix`

Written as PR #40, stacked on `12-needs-matrix`: rebase `12-matrix` onto it after these review
fixes (1b changed shape) and recheck. Ends with a supervised real run on `claude-code` seats, then
M2 is done.

### 2. #15 — stuck detection (M3)

Builds on the permission hook's blocking pattern. A gate is not timed today (a 23 h wait raised
nothing): decide in #15 whether it should be.

### 3. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
