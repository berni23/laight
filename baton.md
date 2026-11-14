# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-14

- **#72 and #73 merged** (PR #84, with #85 merged into it). The #84 review was answered with
  `/cross:cr` and `/cross:fix-cr`: a stale CHANGELOG caveat removed, §5 lists `.ready`, and
  §12.2's `.ack` is marked as coming with #8. Whether a seat's background processes outlive
  teardown is #88.
- **#74 done** (decided by the user): a seat's `permissions` defaults to `auto`, also when a
  `permissions` map has no mode. `validate` warns about a claude-code seat in `default` mode
  instead of about a map with no mode. Spec §3 and §6.1, schema, CHANGELOG.

---

## Do this next

### 1. #67, park the inbox in code

Hide `laight inbox` and stop opening the inbox pane by default. Only questions still raise items
(gates too, until #64). Small.

### 2. #62 (credentials preflight), then #66 (rerun from failed)

§10 M3 order. #66 matters more now that runs fail instead of waiting.

### 3. #82, declared step outputs

Catch template errors before anything runs: `validate`, `laight done` in the producer's pane, and
a dry render at `laight run`. Required exactly for steps whose outputs another step reads.

---

## Waiting on the user

- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? Still undecided (2026-11-14); `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- #88: on the next supervised `background-hang` run, check `pgrep -fl "ping -c 900"` once it's red.
