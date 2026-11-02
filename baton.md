# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-02

- **Direction change: unattended by design** (umbrella #68). A run should need no human mid-run:
  questions are settled before the run (`/laight:plan`), permissions are deny lists on auto mode,
  credentials are a preflight, and a failed run ends with where and why, CircleCI-style. Filed
  #60–#67 and berni23/laight-plugins#3–#4; rewrote #58 (`laight show`) for it. The inbox is
  **parked, not deleted** (#67; #55, #59, #49, #26 labelled `parked`).
- #55's last piece (question context and the run's inbox pane) is committed as finished work;
  the rest of #55 is superseded by #64 and #61.
- `CLAUDE.md` §11 points to the companion plugins repo, berni23/laight-plugins.

---

## Do this next

### 1. #60 — rewrite the spec for "unattended by design"

On branch `60-unattended-spec`. Requirement 4, §3 rule 4 (a failed run ends), §6 (parked inbox,
`human:` policy), §8 (`inbox` hidden), §10 (new milestones). The six decisions are listed in #60;
the user confirmed `assume` as the default and deny lists over allowlists. No code changes.

### 2. #61 and #63 — deny lists on auto mode, and the failure report

The first code. #61 starts by checking three things against current Claude Code (listed in the
issue). Then #62 (credentials), then #66 (rerun from failed) before anything that makes runs fail
more often. Build order: #68.

### 3. #50 — a real issue-cycle run in go-orchestrator, as evidence

Once #61/#63 land: what it gets denied and where it fails is the data for #64 and `/laight:plan`.
Clean up `aaqt7j`'s worktrees with `laight abort aaqt7j`.

---

## Waiting on the user

- `inbox_vs_supervisor.md` (untracked, repo root): the design comparison from this session; keep
  it somewhere (e.g. `scratch.docs/`) or delete it. Not committed.
- #17 (shell step `cwd`) waits for a workflow that needs it.
