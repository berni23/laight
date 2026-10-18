# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-17

**#7 — shell steps.** Run by the engine in the repo, routed on exit code (`exits:`), output in
`out/<step>-<hop>.log` with env secrets redacted; abort kills the command's process group.
`make check` plus real runs (mapped exit, redaction, abort mid-step).
**#16 — decided and implemented:** `env` only on shell steps; `validate` rejects it elsewhere.
**#12 — decided:** Archon's model (siblings finish, dependents skipped); rule 4 changes with #12.
**Filed** — #17 (shell step `cwd`). Comment on #8: a hard-killed engine orphans a shell step.
**§11** — agents may commit, push the item's branch and open its PR without being asked.

---

## Do this next

### 1. #8 — `laight run --resume`

Build in the delivery ack of §12.1 (the hop is saved before the paste; a `UserPromptSubmit` hook
acks it), fix the "hop saved, never pasted" gap, and handle a shell hop with no out file (the #8
comment: never re-run it automatically).

### 2. #9 minimal inbox + gates, then #10 PermissionRequest hook

Gates are the first inbox producer that needs no Claude; the permission hook is the first real one.
#15 needs the inbox, so it waits for this.

### 3. #11 — acceptance: the issue cycle on a real issue

Before `needs`/`matrix`: the issue cycle doesn't use them, and it is what proves M2. Its `merge`
step is a shell step, which now works. `CLAUDE.local.md` names the reference setup. Also check
`session: fresh` with real Claude here.

### 4. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 is decided (see its last comment); amend rule 4 of §3 when it lands.

---

## Waiting on the user

- Nothing open. #17 (shell step `cwd`) waits for a workflow that needs it.
