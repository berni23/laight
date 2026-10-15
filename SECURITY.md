# Security policy

laight starts coding agents that run commands, edit files and push with your credentials, so
security reports are taken seriously.

## Reporting a vulnerability

**Please don't open a public issue.** Report it privately through GitHub:
[Report a vulnerability](https://github.com/berni23/laight/security/advisories/new)
(the repo's *Security* tab → *Report a vulnerability*).

Include what you found, how to reproduce it, and the impact you expect. You'll get an
acknowledgement within 7 days, and we'll agree a disclosure date with you once a fix is ready.
Credit is given in the advisory unless you prefer otherwise.

## Supported versions

Before 1.0, only the latest release gets security fixes.

## Scope

In scope: laight itself, for example secrets leaking into logs, events, inbox items or push
notifications; the inbox answering on the wrong item or allowing on timeout; hooks or answers
reachable by another local user; command injection through specs or templates.

Out of scope: what an agent does with permissions you granted it (laight's job is to ask you, not
to sandbox the agent), and vulnerabilities in Claude Code, Codex, tmux or ntfy themselves; report
those upstream.
