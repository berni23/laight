# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-02

- **#60 merged**: the spec is unattended by design (requirement 4, §3 rule 4, §6, §8, §10).
  #63's two calls are settled on the issue: running siblings finish before a run ends red; a
  stuck step nudges once, then fails as kind `stuck` (a quiet seat: `timeout`).
- **#57 fixed**: Claude Code's Stop hook input lists `background_tasks` (checked on 2.1.316); a
  turn that ends with one `running` gets no reminder and doesn't count toward stuck (§12.2). A
  withdrawn stuck item now logs `inbox.answered` (`answer: withdrawn`). After PR #71's review:
  only background *agents* count (shells may never end), and a seat isn't idle after `done` while
  one runs. Verified on real runs with `.laight/workflows/background-agent.yaml`. Filed #72 (the
  spinner keeps the seat log moving, so the quiet clock can't fire) and #73 (unchecked
  `background_tasks` types and statuses): both matter before #63 makes timeouts fail runs. Second
  review round: a seat held after `done` has no bound until #72, and #63 must stop a waiting
  agent before it frees its seat (noted on #63 and #72).

---

## Do this next

### 1. #61 and #63 — deny lists on auto mode, and the failure report

The first unattended code. #61 starts by checking three things against current Claude Code
(listed in the issue). #63's decisions are in its comments; #72 and #73 should land before or
with it, since #63 turns a timeout or a false stop into a failed run. Then #62 (credentials), then #66
(rerun from failed) before anything that makes runs fail more often. Order: §10 M3.

### 2. #67 — park the inbox in code

Hide `laight inbox`, stop opening the inbox pane by default; the spec already says so (§6.4, §8).
Small; can go alongside #61.

### 3. #50 — a real issue-cycle run in go-orchestrator, as evidence

Once #61/#63 land: what it gets denied and where it fails is the data for #64 and `/laight:plan`.
Its review and fix steps (background agents) no longer raise false stuck items. Clean up
`aaqt7j`'s worktrees with `laight abort aaqt7j`.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
