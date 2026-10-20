# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-20

**#11 — M2 acceptance passed.** The §3 issue cycle ran twice on go-orchestrator with real Claude
Code seats: #138 → PR #149 and #120 → PR #151, each implement → review → fix → verify (pass) →
gate (answered from `laight inbox`) → merge shell step, rig torn down with `on_finish: destroy`.
No typing into panes. The second run's gate waited ~23 h and the engine survived it. The first run
raised an `AskUserQuestion` permission item (that is #34). No engine died, so nothing for #8.

**Found on the first real run, fixed:** `laight done: command not found` when laight isn't
installed. A seat's `PATH` now starts with `<run>/bin/`, holding only a `laight` link to
the binary running its rig (spec §5). #35's review caught the first version prepending the
binary's whole folder (with brew, `/opt/homebrew/bin`, whose `claude` would shadow the user's).
`TestSeatFindsLaightOnPath` pins a renamed binary and no sibling leaking in, and was seen red on the
old code. Spec §5 now also names the bare `Bash(laight done:*)` allow rule (unit-tested).

---

## Do this next

### 1. #32 — check the permission hook against real Claude

Still not run. Four questions (the fourth, from #35's review, is in an issue comment: does a
real Claude seat's Bash find the rig's `laight`, or an older one from the user's rc). The others: does `tool_input` match across `PermissionRequest` and
`PostToolUse` for Edit, Write, WebFetch and an MCP tool; which signal Claude sends on No/Esc; does
it honour the 1 h hook timeout. Needs a supervised run with hooks that `tee` their stdin.

### 2. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 is decided (its last comment), but rule 4 now holds a failed step instead of failing the run
(§12.3 says so): decide what a step that `needs` a held one does. #15 can build on the permission
hook's blocking pattern; a gate is not timed today (the 23 h wait raised nothing), decide in #15
whether it should be.

### 3. #34 — AskUserQuestion as a real `question` item (M3)

Seen in the acceptance run: it arrives as a permission item, so the answer can't be given there.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
