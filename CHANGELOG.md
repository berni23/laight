# Changelog

All notable changes to laight are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- The design specification (CLAUDE.md), project scaffolding, CI and the release pipeline.
- `laight version`.
- `laight init`: scaffolds `.laight/` with an issue-cycle workflow and guidance;
  `laight init --from openrig <dir>` converts an OpenRig rig and its workflows, and lists what changed.
- `laight validate [workflow…]`: JSON Schema plus semantic checks (unknown seats and steps, routes,
  `needs` cycles, unreachable steps, templates), each reported with file and line.
- JSON Schemas for workflows and for `config.yaml` under `schema/`.
