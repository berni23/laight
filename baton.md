# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-16

- **#66 done**: `laight run --from-failed <run>`. Decisions recorded on #66 and in spec §6.3
  (shape-only strictness, the hop count carries over, worktrees reused in place and handed over
  via `rerun_as`). Covered by unit tests (`internal/engine/rerun_test.go`) and the fake-runtime
  integration test (`TestRerunFromFailed`). Also verified on a real Haiku run: it failed on a
  deny rule, the rule was dropped, and the rerun resumed the same Claude session in the kept
  worktree, got the note and went green without rerunning step one.

---

## Do this next

### 1. #93, seat windows and the tmux server's environment

A §12.4 leak (a `LAIGHT_SECRET_*` value in tmux's global env reaches every later seat), and the
seat half of what #92 fixed for the engine. Reuse `engineEnv`'s filter (read-only names
included) for seats. Take #96 (scrub the reason in `rig.Down`) alongside: it's just as small.

### 2. #82, declared step outputs

Catch template errors before anything runs: `validate`, `laight done` in the producer's pane, and
a dry render at `laight run` (and at `--from-failed`, against the carried outputs).

### 3. #64, the `human:` policy and gate defaults

Next in §10's M3 order now that #66 is in.

---

## Waiting on the user

- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? Still undecided (2026-11-14); `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- #88: on the next supervised `background-hang` run, check `pgrep -fl "ping -c 900"` once it's red.
