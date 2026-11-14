# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-14

- **#67 done**: `laight inbox` is out of `--help` (still works), and `laight run` no longer opens
  the inbox pane. Until #64, gates and questions are answered with `laight inbox`, which the
  engine window names. Spec status line and §6.4, CHANGELOG. #55, #59, #49, #26 were already
  labelled `parked`.

---

## Do this next

### 1. #62, credentials preflight

§10 M3 order. `credentials: { <name>: { check, login?, min_valid? } }`, run before the rig spins
up; a missing secret becomes failure kind `credential` instead of `crash`.

### 2. #66, rerun from failed

Matters more now that runs fail instead of waiting.

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
