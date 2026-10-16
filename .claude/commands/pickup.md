---
description: Take the baton at session start — read the spec and baton.md, reconcile them against reality, then pick up and work the next item
argument-hint: [issue number or milestone item, to skip the pick] [scope notes, e.g. "just the design, don't implement"]
---

**This is the other half of `/baton`.** That one *passes* the baton at the end of a session; this
one *takes* it at the start. Optional target: $ARGUMENTS

`/baton` writes `baton.md` (repo root) in exactly three sections. This command reads those same
three and acts on each — §4, §5, §6 below. If a target was given, skip the pick in §5 but still do
everything else: the baton and the blocked-decisions list change *how* an item is worked, not only
which one.

## 1. Read the spec first

**`CLAUDE.md`** is the spec, and it is loaded automatically, which is exactly why it gets skimmed.
Once you know which item you are on, go back and **read the sections that touch it properly**: the
four hard requirements at the top, the section the item implements (§3 YAML, §4 layout, §5 driving
a seat, §6 the human channel…), and §11. Most defects are an interaction between a change and one of
those rules, not a mistake inside the hunk. Read `CLAUDE.local.md` too if it exists.

Then `baton.md`. `CLAUDE.md` is what laight must be; the baton is what is in flight right now.
Neither substitutes for the other.

## 2. Know what the baton deliberately does not contain

⚠️ `/baton` **forbids** merge state, push state, branch names and SHAs in that file, and it
**deletes rather than appends**. So the baton is lossy *by contract*, and the missing half is in git
and `gh`, which answer correctly and always:

```bash
git log --oneline -8
git status --porcelain          # uncommitted work the baton cannot know about
gh pr list                      # an open PR the handoff was not allowed to mention
gh issue list --limit 15
```

The milestone list and the `Status:` line at the top of `CLAUDE.md` say which milestone is current.
**Do not go looking in `baton.md` for something it was told not to write.** Ask git.

## 3. Reconcile, and repair what is stale

⚠️ **The baton is a lagging artifact. Trust the tree over its prose.** If they disagree, **fix the
file in this session** — that is part of taking the baton, not a detour. Say what was stale in one
line and move on.

- **An open PR carrying an unaddressed review is the next work** — `/check-cr` — not a new item.
  Finishing beats starting. A PR with no review at all is a candidate for `/cr`.
- Dependabot PRs are not handoff items; mention them only if one blocks CI.

## 4. §1 of the baton — "Last session"

Read it as *claims to verify*, not as history to trust. An issue listed done should be closed; work
described as landed should be in `git log`; a milestone listed done should match the `Status:` line
in `CLAUDE.md`. Close or correct whatever drifted — and check closing keywords did fire (a keyword
works only in a PR description or commit message, one per issue, into the default branch).

## 5. §2 of the baton — "Do this next"

Take its order. It encodes a *why* that §10 and `gh issue list` cannot show: what unblocks what, a
seam left mid-way, an ordering that costs a rewrite if taken backwards.

Then **read the source in full**: `gh issue view <n> --comments` for an issue (later comments
supersede the body), or the §10 line plus every spec section the item touches for a milestone item.
**The issue body and the spec win over the baton's one-line summary.**

**Milestones are indicators, not hard gates.** If the coherent change pulls in later-milestone work,
do it and say so, so §10 and the baton stay honest.

## 6. §3 of the baton — "Waiting on the user"

⚠️ **Check this before you start, not after you have built on a guess.** These entries gate real
work: an item is not ready just because it is next in the list.

Stop and ask when the item is ambiguous, when two candidates are genuinely tied, when the work
depends on one of those open decisions, or when it would change the spec — **for anything
ambiguous, ASK rather than assume.** When the user settles one, record it **on the issue or in
`CLAUDE.md`**, not only in the reply, or it gets re-litigated. Otherwise state the pick in one line
and get on with it; don't narrate options you are not going to take.

## 7. Work it, the way this repo works

- **Branch before the first edit.** Work never happens on `main`. Once the item is picked, if the
  current branch is `main`, create one named after the item and switch to it:
  `git switch -c <issue#>-<short-slug>` (e.g. `6-engine-loop`), or `m2-<slug>` for a milestone
  item with no issue. Uncommitted changes in the tree move with the switch, so this also rescues
  work a previous session left on `main`. Already on the item's branch (an open PR, `/check-cr`):
  stay on it. On some other item's branch: stop and ask. Commit, push the branch and open
  its PR when the work is verified (`CLAUDE.md` §11); no need to ask.
- **The four hard requirements beat features.** A change that needs a daemon, a server, a database,
  a new command or a required flag is wrong as shaped — say so before building it.
- **Every bug fix gets a regression test, verified to fail before the fix.** Mutate the fix away,
  watch it go red, restore it — and ⚠️ **assert the mutation actually landed**, because an edit that
  silently matches nothing reads as coverage while proving nothing.
- **Tests never touch the user's tmux.** laight uses `tmux -L laight`; tests use a throwaway server.
- **Follow the spec, or change it deliberately and say so.** If the code needs to differ from
  `CLAUDE.md`, amend the spec in the same change and call it out; never let them disagree silently.
- **If three candidate fixes all feel awkward, the shape is wrong** — say so instead of ranking
  workarounds.
- **Verify with what CI runs**, and do not report success on anything you have not run:

```bash
make check                      # gofmt, go vet, staticcheck, go test -race ./...
make build && ./bin/laight version
```

For anything that drives tmux or an agent, also do a real run (`./bin/laight run …` in a test repo,
`attach`, `abort`) — §11: a passing unit test does not prove an agent got its prompt.
- User-visible changes get a line in `CHANGELOG.md` under *Unreleased*.

## 8. Land what you learned, where it survives

⚠️ **Never let `baton.md` be a finding's only home** — `/baton` rewrites it every session:

- a design decision or an invariant a future change could innocently undo → **`CLAUDE.md`**
- work to be done, a latent bug, a deferred decision → **a GitHub issue** (`gh issue create`), even
  when you also mention it in the baton. A duplicate issue is cheap; a lost finding is not.

## 9. Hand back

Report what you did, what you verified — naming the commands you actually ran — and what is left.
Commit, push and open the PR without waiting to be asked (`CLAUDE.md` §11).

When you commit, the loop closes where it opened: `baton.md` goes in with the change,
the `baton-check` hook enforces it, and **`/baton` says what to write.**
