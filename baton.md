# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-16

**M1 — done.** `run`, `attach`, `abort`: rigs spin up and tear down.
**Filed** — M2 split into #6–#12 (GitHub milestone `M2`), one issue per item of §10.
**Added** — the baton workflow: `/baton`, `/pickup`, `/cr`, `/check-cr`, the `code-reviewer` agent.
**Done** — the #5 review fixed on `m1-rig`: a reserved `engine` seat name, `laight abort` safe inside
the rig, crashed seats relaunched (Claude resumed), setup retried, Ctrl-C stops the rig, no
`new-session -e`. Not yet checked: tmux 3.0/3.1, and resume against a real Claude seat.

---

## Do this next

### 1. #6 — engine loop: agent steps, `laight done`, routing, `max_hops`

First, with a `runtime: shell` seat whose script calls `laight done`, inside a throwaway tmux
server: every other M2 item plugs into this loop, and the fake runtime is how the rest gets tested
without Claude.

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
