# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-18

**#29 — flaky `TestRigUpAndAbort`.** The test typed into seat `a` while the engine was delivering
step one there; it now waits for that step's output in the pane log first. Not reproduced
locally (it failed once, on macOS CI); passes `-race -count=10`.

**#30 — an inbox abort says so:** the run's reason is `<step error>; aborted from the inbox`.
Unit-tested, mutation checked.

---

## Do this next

### 1. #10 — PermissionRequest hook into the inbox

The first real-agent producer, on the queue #9 built: the hook blocks polling
`inbox/answered/<id>.json` (`inbox.Answered`), expiry falls back to deny. Needs a real Claude seat
with `permissions: default` to verify. #15 waits for it.

### 2. #11 — acceptance: the issue cycle on a real issue

Before `needs`/`matrix`: the issue cycle doesn't use them, and it proves M2. Its `approve` gate
and `merge` shell step both work now. `CLAUDE.local.md` names the reference setup. Also check
`session: fresh` with real Claude, and watch whether an engine ever dies (#8's trigger).

### 3. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 is decided (its last comment), but rule 4 now holds a failed step instead of failing the run
(§12.3 says so): decide what a step that `needs` a held one does.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
