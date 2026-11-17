# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-17

- **#93 done**: seat windows start from the run's environment (the engine's), minus every
  `LAIGHT_SECRET_*`, through their env file, which also unsets what laight's tmux server holds
  and the run doesn't; the tmux client never hands the server a secret; a seat's `setup:` gets
  no secret either. Spec §5 and §12.4 updated. Pinned by `TestSeatGetsRunEnv` (stale server) and
  `TestServerEnv`, both checked red with the fix mutated away; verified on a real Haiku run.
  Its review answered (all 6 findings fixed): the caller's Claude Code session and git's
  repo-local variables no longer reach the engine or seats; `Stale` fails on a tmux error and
  removes an old server's secrets; a seat's `setup:` gets no secret, now pinned. Filed #102
  (`laight run`'s own git calls). Real Haiku run from inside Claude Code: the seat holds its own
  session id, not the caller's.
- **#96 done**: `rig.Down` scrubs its reason once, so `run.json`'s `error` and the run event are
  redacted too. Pinned by `TestDownScrubsReason` (checked red without the fix).

---

## Do this next

### 1. #82, declared step outputs

Catch template errors before anything runs: `validate`, `laight done` in the producer's pane, and
a dry render at `laight run` (and at `--from-failed`, against the carried outputs).

### 2. #64, the `human:` policy and gate defaults

Next in §10's M3 order now that #66 is in.

---

## Waiting on the user

- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? Still undecided (2026-11-14); `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- #88: on the next supervised `background-hang` run, check `pgrep -fl "ping -c 900"` once it's red.
