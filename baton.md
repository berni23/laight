# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-02

- **#60 done: the spec is unattended by design.** Requirement 4, §1, §2, §3 (rule 4: a failed run
  ends; example with `human:`, `permissions.deny`, gate `default:`), §6 rewritten as before /
  during / after the run with the inbox parked in §6.4, §7, §8 (nine commands,
  `run --from-failed`), §10 (M3 unattended, M4 observability, M5 supervisor, inbox parked),
  §12.2–12.3. README and CHANGELOG follow. The status line says where code still lags (#63).
- Two calls made in the spec and put on #63 to overturn: running siblings finish before a run
  ends red; stuck steps nudge once then fail instead of asking. #68 maps each issue to its section.
- Stacked on PR #69 (both edit `CLAUDE.md`).

---

## Do this next

### 1. #61 and #63 — deny lists on auto mode, and the failure report

The first code. #61 starts by checking three things against current Claude Code (listed in the
issue). #63: read its comment first (two decisions made in the spec). Then #62 (credentials),
then #66 (rerun from failed) before anything that makes runs fail more often. Order: §10 M3.

### 2. #67 — park the inbox in code

Hide `laight inbox`, stop opening the inbox pane by default; the spec already says so (§6.4, §8).
Small; can go alongside #61.

### 3. #50 — a real issue-cycle run in go-orchestrator, as evidence

Once #61/#63 land: what it gets denied and where it fails is the data for #64 and `/laight:plan`.
Clean up `aaqt7j`'s worktrees with `laight abort aaqt7j`.

---

## Waiting on the user

- Merge PR #69, then this one (stacked on it).
- #63's two decisions (comment on the issue): confirm or overturn before #63 is built.
- #17 (shell step `cwd`) waits for a workflow that needs it.
