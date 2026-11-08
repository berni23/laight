# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-11-02

- **#61 done**: deny lists (workflow and per seat, `permissions: { mode, deny }`) go into each
  seat's `--settings`. A permission prompt is denied at once and recorded as a `permission.denied`
  event (auto mode's own refusals too, through `PermissionDenied`). `validate` rejects rules
  Claude would skip or never match, and `init` scaffolds a starter list. Checked on 2.1.316: deny
  rules hold under yolo, and a deny-rule hit reaches no hook (noted on #63, which must read those
  hits from transcripts if its report lists them). Verified on a real run with
  `.laight/workflows/deny-check.yaml`. Not yet seen on a run: `PermissionDenied`, and auto mode
  falling back to a prompt after 3 or 20 refusals; #50 is where they'll show up.
- **PR #75's review answered**: validate now accepts hyphenated MCP rules and rejects templates
  and rules that would forbid `laight done`; it warns about `permissions` without a `mode`.
  Denials in the run's last tick are printed, and the starter list covers `+refspec` and
  `refs/heads/<base>`. Filed #76 (push rules have other spellings; this repo's `dev` and `main`
  aren't protected on GitHub).
- **#75's review answer** missed its merge; it went in through #78, and #78's own review through
  #79: the `laight done` guard covers every exit a seat's claude-code agent steps take (`failed`
  included, and any rule aimed at `laight done <exit> `, outputs or not), any `permissions` map
  without a mode warns, and claude-code-only warnings stay off other runtimes.

---

## Do this next

### 1. #63, the failure report (with #72 and #73)

A failed run ends red with `failure: {…}` in run.json and a report from `laight run`, listing the
`permission.denied` events (#61's comment on #63 has the fields). #72 and #73 should land before
or with it, since #63 turns a timeout or a false stop into a failed run. Then #62 (credentials),
then #66 (rerun from failed). Order: §10 M3.

### 2. #67, park the inbox in code

Hide `laight inbox` and stop opening the inbox pane by default. Since #61 the only items a hook
raises are questions. Small; can go alongside #63.

### 3. #50, a real issue-cycle run in go-orchestrator, as evidence

The data for #64 and `/laight:plan`: which requests get denied (deny rule, hook or auto mode) and
where the run fails. It also confirms the `PermissionDenied` hook on a real refusal. Clean up
`aaqt7j`'s worktrees with `laight abort aaqt7j`.

---

## Waiting on the user

- #74: should a seat's `permissions` default to `auto`? In `default` mode every prompt is now
  denied.
- Render `.base` in deny rules (`Bash(git push * {{ .base }})`), so a changed `base:` keeps its
  push protection? `validate` rejects templates in rules for now.
- Protect `dev` and `main` on GitHub (#76): they aren't today.
- #17 (shell step `cwd`) waits for a workflow that needs it.
