# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-21

**#32 — the permission hook checked against real Claude Code 2.1.305; no code change.** The
`toolMatch` hash matches across both hooks for Write, Edit, WebFetch and an MCP tool; No, Esc, a
hook timeout and a dying session all send SIGTERM (caught); `timeout: 3600` is honoured to the
second; a real seat's Bash finds the rig's `laight` ahead of the rc's `PATH`. Details in #32's last
comment, summary in spec §6. Not tried: a fake old `laight` inside an rc folder (writing to
`~/.local/bin` was refused); the `PATH` order observed covers it.

**#12 decided:** a step that `needs` a held step waits; `retry` can still let it run, `abort` fails
the run. Spec §12.3 says so.

---

## Do this next

### 1. #12 — `needs` and `matrix`

Fully decided now (its last two comments, spec §3 rules 1–6 and §12.3). Lands M2.

### 2. #15 — stuck detection (M3)

Builds on the permission hook's blocking pattern. A gate is not timed today (a 23 h wait raised
nothing): decide in #15 whether it should be. Seen in #32's run: an agent wrote `laight done` as
text instead of running it, and the Stop hook's reminder fixed it; that path works.

### 3. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
