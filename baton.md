# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-08 (2)

- **#73 done**: probed Claude Code 2.1.319 on a real session and in its source. `background_tasks`
  lists in-flight work only (`running` or `pending`); a finished, failed or stopped task drops
  out. `waiting()` now counts `subagent` and `workflow` entries, what Claude's own "Waiting for N
  background agents and M dynamic workflows" counts; shells and Monitors (both `shell`) don't.
  Details on #73; spec §12.2 updated.
- **#72 measured** (comment on #72): the spinner grows the pane log ~70 B/s while the main
  transcript and `<session>/subagents/agent-*.jsonl` stay still. They are the clock to use.

---

## Do this next

### 1. #72: take the quiet clock from the transcripts, not the seat log

For claude-code seats, the max mtime of the transcript (`transcript_path`, from the SessionStart
hook) and `<session-id>/subagents/*.jsonl` replaces the seat log's mtime in `stuck.go`, the
post-done path (`h == nil` in `watchSeat`) included. Then a seat waiting on a background agent
that never returns times out instead of waiting for ever. Check on a real run.

### 2. #67, park the inbox in code

Hide `laight inbox` and stop opening the inbox pane by default. Only questions still raise items
(gates too, until #64). Small.

### 3. #62 (credentials preflight), then #66 (rerun from failed)

§10 M3 order. #66 matters more now that runs fail instead of waiting.

### 4. #82, declared step outputs

Catch template errors before anything runs: `validate`, `laight done` in the producer's pane, and
a dry render at `laight run`. Required exactly for steps whose outputs another step reads.

---

## Waiting on the user

- #74: should a seat's `permissions` default to `auto`? In `default` mode every prompt is now
  denied.
- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
