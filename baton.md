# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-18

**#8 — deferred, decided with the user.** No `run --resume` in M2. Instead, `attach` and
`abort` report a live run whose engine died. Spec §4/§8/§10/§12.1 amended; the answer for the
day it comes back is on #8. #19's review is answered: abort no longer signals a process group
recorded before the last reboot, and a tmux socket laight can't open isn't read as "gone".
Filed from it: #21 (test the `--resume` relaunch), #22 (concurrent abort teardown), #23 (pid
reuse within one boot).
**#9 — minimal inbox + gates.** `files` queue (`internal/inbox`), `laight inbox [<item> <answer>]`,
gates routed on the answer, and **rule 4 changed (decided with the user, on #9)**: an unrouted
`failed` or a `max_hops` refusal holds the run on a `failed` item (`retry`/`abort`) instead of
ending it. `make check` plus a manual run of the built binary (gate → yes → held failure → abort).
Its review (posted on #20) is answered: every retry grants a hop, titles are redacted and
cleaned, a dead engine's items refuse answers, `Down` withdraws after its final status. Filed
#25 (redact events/run.json), #26 (engine records answers), #27 (prune `~/.laight`).

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

- Nothing open. #17 (shell step `cwd`) waits for a workflow that needs it.
