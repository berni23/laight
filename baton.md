# Baton

**What the last session finished, and what to pick up next. Nothing else.**

Read `CLAUDE.md` first: it is the spec. This file holds only the handoff, and is deliberately short.
Items are GitHub issues (`#N`) or milestone items from `CLAUDE.md` §10 (`M2: <item>`); the detail is
there, not here. Written by `/baton` before each commit, read by `/pickup` at session start.

⚠️ **This file does not track merge or push state.** `git log --oneline -5` and `gh pr list` answer
that.

---

## Last session — 2026-10-18

**#10 — PermissionRequest hook into the inbox.** A Claude seat's prompt becomes a `permission`
item (detail = the command, the seat's secret env values redacted via `LAIGHT_REDACT_ENV`); the
hook blocks until `laight inbox <item> allow|deny`, denies on expiry (1 h hook timeout, item
expires a minute earlier) or on any error. Claude shows its own dialog while the hook waits: No/Esc
there stops the hook, Yes there is caught by a `PostToolUse` hook matching the call (spec §6 has
the details). `make check`, plus real Claude Code 2.1.302 runs with `permissions: default`: inbox
allow, inbox deny, pane Yes, pane No, pane Esc. The expiry deny is unit-tested only (an hour).

**#28's review answered** (one commit per finding, each test seen red first). Fixed:
- the hook denies an input it can't read, with no item;
- `inbox.Put` strips the seat's secrets itself, JSON-escaped too, and cuts the detail only after
  redacting;
- the cancel signals are caught before the item exists, and a run that ended meanwhile drops
  the item;
- an expired item can't be answered;
- the tool hooks no longer log a data-less event per call.

New tests cover cancel, answer order, other seats, events and the generated settings. Rejected:
permission items stay unanswerable once the engine died (§4: the run is lost; abort withdraws
them). Not done: a Stop-hook sweep of leftover items, because a background subagent can ask
after the turn ends.

---

## Do this next

### 1. #32 — check the permission hook against real Claude, then #11

Three questions only a real run settles: does `tool_input` match across the two hooks for
non-Bash tools, which signal does Claude send on Esc, and does it honour the 1 h timeout? Do it
in the same supervised session as #11.

### 1. #11 — acceptance: the issue cycle on a real issue

Every piece it uses now works (agent and shell steps, the gate, permission prompts). It proves
M2 before `needs`/`matrix`. `CLAUDE.local.md` names the reference setup. Also check
`session: fresh` with real Claude, and watch whether an engine ever dies (#8's trigger).

### 2. #12 — `needs` and `matrix`, then #15 — stuck detection (M3)

#12 is decided (its last comment), but rule 4 now holds a failed step instead of failing the run
(§12.3 says so): decide what a step that `needs` a held one does. #15 can now build on the
permission hook's blocking pattern.

---

## Waiting on the user

- #19 review, open question: should an abort answered from the inbox say so in the run's reason
  (e.g. `<step error>; aborted from the inbox`), or keep the step's error alone?
- #17 (shell step `cwd`) waits for a workflow that needs it.
