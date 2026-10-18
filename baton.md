# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-18

**#8 — deferred, decided with the user.** No `run --resume` in M2: the engine lives in tmux and
rarely dies, and recovering a step in flight is most of the engine's hard cases. Instead,
`attach` and `abort` report a live run whose engine died (regression test `TestEngineDied`,
verified red without the fix). Spec §4, §8, §10 and §12.1 amended; the settled answer for the
day it comes back is on #8.

---

## Do this next

### 1. #9 minimal inbox + gates, then #10 PermissionRequest hook

Gates are the first inbox producer that needs no Claude; the permission hook is the first real one.
#15 needs the inbox, so it waits for this.

### 2. #11 — acceptance: the issue cycle on a real issue

Before `needs`/`matrix`: the issue cycle doesn't use them, and it is what proves M2. Its `merge`
step is a shell step, which works. `CLAUDE.local.md` names the reference setup. Also check
`session: fresh` with real Claude, and watch whether an engine ever dies (that is #8's trigger).

### 3. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 is decided (see its last comment); amend rule 4 of §3 when it lands.

---

## Waiting on the user

- Nothing open. #17 (shell step `cwd`) waits for a workflow that needs it.
