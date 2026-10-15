# laight

[![CI](https://github.com/berni23/laight/actions/workflows/ci.yml/badge.svg)](https://github.com/berni23/laight/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**Run teams of coding agents in tmux from two small YAML files, and answer everything they need
from you in one place.**

> **Status: design phase.** The specification is in [CLAUDE.md](CLAUDE.md); the code is just
> starting (see the [milestones](CLAUDE.md#10-milestones)). Feedback on the design is very welcome:
> open an [issue](https://github.com/berni23/laight/issues) or a
> [discussion](https://github.com/berni23/laight/discussions).

## Why

Running an implementer agent and a reviewer agent through
*implement → review → fix → verify → merge* works: issues go from pickup to a merged, independently
reviewed PR without anyone typing. Tools like [OpenRig](https://github.com/mvschwarz/openrig) proved
it. But today that means a daemon to babysit, a CLI with hundreds of commands, agents that must call
it correctly to hand work over, and questions that sit unseen in one of many terminal panes until
someone looks. Watching every agent doesn't scale.

laight keeps the idea and drops the machinery:

- **Lightweight**: one Go binary. No daemon, no always-on server, no database. State is files.
- **tmux**: every agent is a real, interactive session you can attach to and type into.
- **Config is everything**: one YAML file per workflow declares its agents and its steps, in series
  or in parallel. Starting it spins up a rig of those agents; finishing it tears the rig down.
- **Isolated rigs**: each rig gets its own tmux session, git worktrees and state. Many rigs run side
  by side in one project.
- **One human channel**: permission prompts, questions, approval gates and stuck steps from every
  run in every project land in one queue, answered from one place: `laight inbox` in a terminal,
  your phone ([ntfy](https://ntfy.sh)), Slack, a webhook or your own command. The queue is files by
  default, or a real queue such as Amazon SQS when your agents run on several machines.
- **See it**: `laight show` opens a simple, CircleCI-style web view of your workflows and runs.
- **Plain YAML**: OpenRig-style vocabulary (seats, steps, `allowed_exits`, `next_hop`, `max_hops`),
  a published JSON Schema for editor completion and validation, and an importer for OpenRig setups.
- **Agent-agnostic**: Claude Code first, Codex next, any CLI through the `shell` runtime.

## One file is the whole system

Like a CircleCI or Kubernetes config, a workflow file declares everything: the agents (seats), the
steps, in series or in parallel, and what happens on each outcome. `laight run` spins up a **rig**
of exactly those agents for that run, and the rig dies when the run finishes. Several rigs can run
side by side in the same project.

`.laight/workflows/issue-cycle.yaml`:

```yaml
version: 1
id: issue-cycle
base: dev
seats:
  impl:     { runtime: claude-code, cwd: worktree, session: resume, permissions: auto, guidance: guidance/implementer.md }
  reviewer: { runtime: claude-code, cwd: worktree, session: fresh,  permissions: auto, guidance: guidance/reviewer.md }
steps:
  - { id: implement, seat: impl,     objective: "Implement the issue, open a PR. laight done handoff pr=<url>", next_hop: { on: { handoff: review } } }
  - { id: review,    seat: reviewer, objective: "Review {{ .steps.implement.out.pr }}",                      next_hop: { on: { handoff: fix } } }
  - { id: fix,       seat: impl,     objective: "Address the review on {{ .steps.implement.out.pr }}",       next_hop: { on: { handoff: verify } } }
  - { id: verify,    seat: reviewer, objective: "Verify each finding.", allowed_exits: [pass, fail],        next_hop: { on: { pass: approve, fail: fix } } }
  - { id: approve,   type: gate,     ask: "Merge {{ .steps.implement.out.pr }}?",                            next_hop: { on: { yes: merge, no: end } } }
  - { id: merge,     type: shell,    run: "gh pr merge {{ .steps.implement.out.pr }} --squash" }
loop_guards: { max_hops: 11 }
```

Parallel work uses `needs` and `matrix`, CircleCI-style:

```yaml
  - { id: build, needs: [plan], matrix: { task: "{{ .steps.plan.out.tasks }}" }, seat: dev, objective: "Implement {{ .matrix.task }}" }
  - { id: audit, needs: [plan], seat: auditor, objective: "Threat-model the plan" }
  - { id: integrate, needs: [build, audit], seat: lead, objective: "Merge the PRs and run the full suite" }
```

Agents finish a step with one command, `laight done <exit> [key=value…]`. If they forget, a hook
reminds them; if that fails too, you get an inbox item.

## Install (planned)

```bash
brew install berni23/tap/laight     # also installs tmux
# or: go install github.com/berni23/laight/cmd/laight@latest
laight doctor                       # checks Claude Code, gh and tmux
```

## Use (planned)

```bash
laight init                 # or: laight init --from openrig .openrig
laight validate
laight run issue-cycle      # spins up the workflow's rig and shows the live view
laight inbox                # everything waiting for you, across all runs
laight ps | laight watch    # what's running / live pipeline view
laight attach <run> impl    # jump into an agent's terminal
laight abort <run>          # asks first, then tears down only that rig
```

## The inbox

```
 INBOX  3 waiting                                                        laight
 ─────────────────────────────────────────────────────────────────────────────
 ⚠ permission  issue-cycle·01M4…S7  impl     Bash: gh pr create --base dev …    [a]llow [d]eny
 ? question    issue-cycle·01M5…2Q  impl     "The issue has two specs. Which?"  [r]eply  [o]pen
 ✋ gate        docs·01M5…9X      approve  Merge PR #144 into dev?            [1] yes [2] no
```

The same items can reach your phone, Slack or any command you configure, with buttons. Answers go straight back to the waiting agent:
permission hooks block until you decide, replies are pasted into the pane, gates route the run.
Unanswered permissions time out to *deny*, never *allow*.

## Seeing what happens

```bash
laight show            # opens http://127.0.0.1:<port>: runs, and each run as a CI-style pipeline
laight watch           # the same pipeline, live in the terminal
```

`laight show` is a small read-only page served from your machine only while the command runs:
runs per project, each run's steps as a pipeline (done, running, failed, loops ×N, durations),
each step's events and agent log, and the open inbox items. Every run also writes an append-only
`events.jsonl` (steps, exits, hooks, tool calls, tokens and cost) and a full log of every pane.

## Contributing

laight is open source under the [Apache 2.0 licence](LICENSE) and contributions are welcome:
design feedback, issues, docs and code. Start with [CONTRIBUTING.md](CONTRIBUTING.md); please
follow the [Code of Conduct](CODE_OF_CONDUCT.md). Security issues: see [SECURITY.md](SECURITY.md).

A VS Code extension, laight-vscode-ui (not public yet), will give laight a sidebar: the pipeline, the agents' terminals and the inbox.
