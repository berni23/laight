# Contributing to laight

Thanks for your interest! laight is early: the design in [CLAUDE.md](CLAUDE.md) matters more than
any code right now, so design feedback is as valuable as a PR.

## Ways to help

- **Discuss the design**: questions and ideas go to
  [Discussions](https://github.com/berni23/laight/discussions). Concrete problems with the spec go
  to [Issues](https://github.com/berni23/laight/issues).
- **Report a bug** or **propose a feature** with the issue templates.
- **Pick up work**: issues labelled `good first issue` or `help wanted`. Comment on the issue first so
  nobody duplicates effort.
- **Share workflows**: a rig + workflow you'd like laight to run is a great test case.

## Ground rules

laight has four hard requirements (see [CLAUDE.md](CLAUDE.md)): **lightweight** (no daemon, server,
database or open port), **tmux**, **isolated runs**, and **one human channel**. A change that
weakens one of them won't be merged, however useful, so open an issue first for anything larger
than a fix.

## Development

You need Go 1.27+ (latest stable), tmux ≥ 3.0 and git. Claude Code is only needed for manual
end-to-end runs.

```bash
git clone https://github.com/berni23/laight && cd laight
make build      # ./bin/laight
make test       # go test -race ./...
make check      # gofmt, go vet, staticcheck, tests: what CI runs
```

- Tests never touch your own tmux sessions: laight uses its own tmux server (`tmux -L laight`), and
  integration tests start a throwaway one.
- Keep dependencies few and prefer the standard library. Explain any new dependency in the PR.

## Pull requests

1. Fork, branch from `main`, keep the PR focused on one thing.
2. `make check` passes. New behaviour has tests.
3. User-visible changes get a line in [CHANGELOG.md](CHANGELOG.md) under *Unreleased*.
4. Write commit messages in the imperative ("Add inbox snooze"), and say in the PR how you verified
   it. For anything that drives agents, that means a real run, not only unit tests.
5. Using an AI coding agent is fine; you are responsible for what it wrote. Agents read
   [CLAUDE.md](CLAUDE.md) / [AGENTS.md](AGENTS.md).

By contributing you agree your contribution is licensed under the [Apache 2.0 licence](LICENSE)
(section 5 of the licence). No CLA.

## Releases

Maintainers tag `vX.Y.Z` on `main`; GoReleaser publishes binaries and updates the Homebrew tap.
Versioning follows [SemVer](https://semver.org); before 1.0, minor versions may break specs, and the
CHANGELOG says how to migrate.
