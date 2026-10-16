# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-16

**#6 — review answered.** Every finding of the `/cr` review was fixed with a regression test (each
seen red), deferred to an issue, or answered on its thread. Real Claude seat (haiku), run before
and after the fixes: the Stop hook reminder fired once and worked, and step two waited for the Stop
hook. Not yet seen with real Claude: `session: fresh`.
**Filed** — #15 (stuck detection: second Stop, step timeouts, a failing Stop hook), #16 (step `env`
on agent steps). Comments with fix shapes on #7, #8, #12.
**Spec** — `CLAUDE.md` §12: how OpenRig, Gas Town, Archon, Orca and Claude Squad solve delivery,
stuck detection, branch failure and secrets, with a recommendation for each.

---

## Do this next

### 1. #7 — shell steps

Small now that the loop exists. `{{ secret }}` stays out of `run:` (§12.4, comment on #7): pass
secrets as the command's env.

### 2. #8 — `laight run --resume`

Build in the delivery ack of §12.1 (the hop is saved before the paste; a `UserPromptSubmit` hook
acks it), and fix the "hop saved, never pasted" gap noted on #8. Resume is what turns that gap into
a hang.

### 3. #9 minimal inbox + gates, then #10 PermissionRequest hook

Gates are the first inbox producer that needs no Claude; the permission hook is the first real one.
#15 needs the inbox, so it waits for this.

### 4. #11 — acceptance: the issue cycle on a real issue

Before `needs`/`matrix`: the issue cycle doesn't use them, and it is what proves M2.
`CLAUDE.local.md` names the reference setup. Also check `session: fresh` with real Claude here.

### 5. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 waits on the decision below.

---

## Waiting on the user

- #12: adopt Archon's model (siblings finish, dependents skipped)? It changes rule 4 of §3.
- #16: what a step's `env` means on agent steps (recommendation: shell steps only).
