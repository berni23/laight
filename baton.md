# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-16

**#6 — done.** The engine loop: agent steps, `laight done`, routing, `max_hops`, the Stop hook. Fake
`sh` runtime tests cover series, a `fail → fix` loop, `max_hops`, unrouted `failed`, a crash
mid-step and a seat relaunched after exiting between steps. One real run with a Claude Code seat
(haiku, worktree): two steps, outputs templated. Not yet seen with real Claude: the Stop hook's
one-time reminder, `session: fresh`.
**Changed** — `/pickup` branches before the first edit; `CLAUDE.md` §11 says work never happens on
`main`.

---

## Do this next

### 1. Review #6's PR

`/cr`, then `/check-cr`: the next items build on this loop, so its review comes first.

### 2. #7 shell steps, then #8 `run --resume`

Shell steps are small once the loop exists. Resume next, while the event log format is still fresh:
it forces everything the engine needs into `run.json` + `events.jsonl`.

### 3. #9 minimal inbox + gates, then #10 PermissionRequest hook

Gates are the first inbox producer that needs no Claude; the permission hook is the first real one.

### 4. #11 — acceptance: the issue cycle on a real issue

Before `needs`/`matrix`: the issue cycle doesn't use them, and it is what proves M2.
`CLAUDE.local.md` names the reference setup.

### 5. #12 — `needs` and `matrix`

---

## Waiting on the user

Nothing.
