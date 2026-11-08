# laight

[![CI](https://github.com/berni23/laight/actions/workflows/ci.yml/badge.svg)](https://github.com/berni23/laight/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**Run teams of coding agents in tmux from one YAML file, unattended: settle what they need from
you before the run, and get a green or red result, CircleCI-style.**

> **Status: early.** `init`, `validate`, `run`, `attach`, `abort` and `done` work: a run spins up
> its rig of agents in tmux, hands each agent its steps, in series or in parallel (`needs`, and
> `matrix` for one agent per item), runs shell steps and gates, routes on their exits (loops
> included), and tears the rig down. **The design just changed to unattended by design**
> ([#68](https://github.com/berni23/laight/issues/68)): deny lists are in (a permission prompt is
> denied at once and recorded), and a failed run ends red with a report naming the step and why;
> a credentials preflight and rerun-from-failed are next (M3, see the
> [milestones](CLAUDE.md#10-milestones)); until the `human:` policy lands, questions still go to a
> (now parked) inbox.
> The specification is in [CLAUDE.md](CLAUDE.md).
> Feedback on the design is very welcome: open an [issue](https://github.com/berni23/laight/issues) or a
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
- **Unattended by design**: what a run needs from you is settled before it starts. Permissions are
  deny lists (what agents must never do; Claude's auto mode judges the rest), credentials are
  checked (and logged into) before any agent starts, and when an agent has a question anyway it
  makes a reasonable assumption and records it for review on the PR, or the workflow says to fail,
  wait, or ask a supervisor agent. A run is green or red, and a red one says which step failed and
  why; rerun it from that step once you've fixed the cause.
- **See it**: `laight show` opens a simple, CircleCI-style web view of your workflows and runs.
- **Plain YAML**: OpenRig-style vocabulary (seats, steps, `allowed_exits`, `next_hop`, `max_hops`),
  so OpenRig users read it instantly, and a published JSON Schema for editor completion and validation.
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
reminds them; if that fails too, the step fails with the reason.

## Install (planned)

```bash
brew install berni23/tap/laight     # also installs tmux
# or: go install github.com/berni23/laight/cmd/laight@latest
laight doctor                       # checks Claude Code, gh and tmux
```

## Use (planned)

```bash
laight init                 # scaffolds .laight/ with an issue-cycle workflow
laight validate
laight run issue-cycle      # spins up the workflow's rig and shows the live view
laight watch                # every run / one run's live pipeline
laight attach <run> impl    # jump into an agent's terminal
laight abort <run>          # asks first, then tears down only that rig
laight run --from-failed <run>   # a red run, rerun from the step that failed
```

## When an agent needs you anyway

```yaml
human: { mode: assume }        # assume (default) | fail | wait | supervisor
permissions:
  deny: ["Bash(git push --force:*)", "Bash(terraform apply:*)"]
credentials:
  aws-dev: { check: "aws sts get-caller-identity", login: "aws sso login" }
```

- **Questions**: by default the agent picks the most reasonable answer, records the assumption and
  carries on; the assumptions are listed in the run's report and the PR. Or fail the step, wait for
  you in the agent's pane, or let a supervisor agent with the run's context answer.
- **Permissions** are never asked mid-run: a deny rule blocks, auto mode judges the rest, and a
  request that would need a human is denied and recorded.
- **Logins** happen in your terminal before the agents start, not halfway through.
- **Gates** you declare still ask you; with no one around they take their `default:`.

## Seeing what happens

```bash
laight show            # opens http://127.0.0.1:<port>: runs, and each run as a CI-style pipeline
laight watch           # the same pipeline, live in the terminal
```

`laight show` is a small read-only page served from your machine only while the command runs:
runs per project, each run's steps as a pipeline (done, running, failed, loops ×N, durations),
each step's events and agent log, and a red run's failure report: the step, the reason, the
denied requests and the assumptions made. Every run also writes an append-only
`events.jsonl` (steps, exits, hooks, tool calls, tokens and cost) and a full log of every pane.

## Contributing

laight is open source under the [Apache 2.0 licence](LICENSE) and contributions are welcome:
design feedback, issues, docs and code. Start with [CONTRIBUTING.md](CONTRIBUTING.md); please
follow the [Code of Conduct](CODE_OF_CONDUCT.md). Security issues: see [SECURITY.md](SECURITY.md).

A VS Code extension, laight-vscode-ui (not public yet), will give laight a sidebar: the pipeline and the agents' terminals.
