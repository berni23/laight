# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-30

- #55 — a question item's detail is what the agent said
  in its turn before asking: its text and tool calls from the transcript, plus its last message
  from its screen (Claude writes that one to the transcript only once the question is answered).
- #55 — `laight run` splits the engine window: `laight inbox --follow <run>` runs under the
  engine, with the focus, shows each item in full and answers at a prompt. tmux pane-level calls
  now target a window's first pane (`.{top-left}`), so the engine's liveness never reads the
  inbox pane. Both verified on real Claude Code 2.1.314 (`ask-context`, a throwaway workflow:
  answered by number and in words from the pane). §6 and §8 updated.

---

## Do this next

### 1. #50 — first supervised run of `laight run issue-cycle`, in go-orchestrator

Install the binary with #55 in it (`GOPROXY=direct go install github.com/berni23/laight/cmd/laight@main`),
then run it from `../go-orchestrator` with `laight run issue-cycle` and watch it end to end
(berni23/go-orchestrator#155 has the workflow). Its `pickup` asked about #115 last time: answer
it from `laight inbox` now. It holds on the `approve` gate before merging into `dev`. Then #53:
analyse the run, one issue per problem. Clean up `aaqt7j`'s worktrees with `laight abort aaqt7j`.

### 2. #55 — the rest of the inbox rethink

Questions, their context and the run's inbox pane are done. The pane is a trial: the user may
prefer a tmux popup or answering in the engine window; ask after they've used it. Still open: a plain-text question that gets past the Stop hook's
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
