# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-15

- **#62 built** (not closed: its done-when needs a real AWS SSO run, below): `credentials:
  { <name>: { check, login? } }`, the preflight in `laight run` (secrets the env names, then every
  check; a failing one's login in the terminal), and `kind: credential` for a missing secret and
  for a step that fails the run while a check fails (the engine re-checks outside the lock).
  Decisions on #62 and in spec §6.1; `min_valid` split off to #91.

---

## Do this next

### 1. #66, rerun from failed

§10 M3 order. Matters more now that runs fail instead of waiting. The rerun should run the
preflight too (`preflight` in `cmd/laight/credentials.go`).

### 2. #82, declared step outputs

Catch template errors before anything runs: `validate`, `laight done` in the producer's pane, and
a dry render at `laight run`. Required exactly for steps whose outputs another step reads.

### 3. #64, the `human:` policy and gate defaults

Next in §10's M3 order after #66.

---

## Waiting on the user

- #62's done-when: run a workflow with an expired AWS SSO credential from a terminal, check the
  login prompts before any seat starts and a seat then uses the profile; close #62 if it does.
- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? Still undecided (2026-11-14); `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
- #88: on the next supervised `background-hang` run, check `pgrep -fl "ping -c 900"` once it's red.
