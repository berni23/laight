# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-25

- #50 — `.laight/workflows/self-cycle.yaml`: laight's own development as a workflow (pickup →
  `/cross:cr` → compact + `/cross:fix-cr` → reviewer verifies → `gh pr merge`). New step field
  `compact: true` (pastes `/compact` before the objective; checked on Claude Code 2.1.310 that
  compacting fires `SessionStart`, not `Stop`, and queues the next message). Validated and
  unit-tested; **not run for real yet**.

---

## Do this next

### 1. #50 — first supervised run of `laight run self-cycle`

Watch it end to end: the slash commands as objectives (`/baton:pickup`, `/cross:cr <PR>` with a
trailing instruction), the compaction before `fix`, and that `verify` lands in the reviewer's
same conversation. It merges into `main` with no gate, so run it only with the maintainer's OK.
File what it shows as issues.

### 2. #34 — AskUserQuestion as a real `question` item (M3)

It arrives as a permission item today, so the answer can't be given there. The other M3 hook;
the stuck item's pattern (engine-raised item, answer acted on in `watch`) is reusable for the
plain-text question the Stop hook catches.

### 3. #39 — seat launches block the engine loop

Matters once matrix copies are Claude seats with `max_parallel` > 2. Read the #15 comment on it
first: async launches must make `watch` skip a seat still launching.

### 4. M3: `laight inbox` (TUI)

The last M3 piece once questions land; every item kind now exists to show.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
