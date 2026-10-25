# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-26

- #55 (absorbs #34) — `AskUserQuestion` is a `question` item: the inbox shows the question and
  its numbered options, takes a number, `1,3` or free text per question, and the hook hands
  Claude the answers (allow + `updatedInput.answers`). The engine window announces hook items.
  Verified on real Claude Code 2.1.310 (auto and yolo seats, two questions with a multi-select,
  and a pane answer withdrawing the item). §6 updated.
- go-orchestrator run `aaqt7j` (#50) was aborted on exactly this gap; its worktrees are kept.

---

## Do this next

### 1. #50 — first supervised run of `laight run issue-cycle`, in go-orchestrator

Install the binary with #55 in it (`GOPROXY=direct go install github.com/berni23/laight/cmd/laight@main`),
then run it from `../go-orchestrator` with `laight run issue-cycle` and watch it end to end
(berni23/go-orchestrator#155 has the workflow). Its `pickup` asked about #115 last time: answer
it from `laight inbox` now. It holds on the `approve` gate before merging into `dev`. Then #53:
analyse the run, one issue per problem. Clean up `aaqt7j`'s worktrees with `laight abort aaqt7j`.

### 2. #55 — the rest of the inbox rethink

Questions are done. Still open there: a plain-text question that gets past the Stop hook's
reminder (show the agent's last message on the stuck item, with a free-text reply), and
permission titles that say what the tool does, not only its name.

### 3. #39 — seat launches block the engine loop

Matters once matrix copies are Claude seats with `max_parallel` > 2. Read the #15 comment on it
first: async launches must make `watch` skip a seat still launching.

### 4. M3: `laight inbox` (TUI)

The last M3 piece once questions land; every item kind now exists to show.

---

## Waiting on the user

- #17 (shell step `cwd`) waits for a workflow that needs it.
- `session: fresh` was exercised (the reviewer relaunched for verify) but not checked for an
  independent conversation; say if that needs its own check.
