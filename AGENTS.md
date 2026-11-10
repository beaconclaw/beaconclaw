# Working in this repository

This is the BeaconClaw agent: a small Go daemon that runs beside an agent runtime, probes it locally and reports health to a BeaconClaw manager over an outbound WebSocket. `README.md` describes what it does and what it sends.

## This repository is public

Everything written here must be safe for anyone to read, in code, commits, pull requests, issues and comments:

- No references to private repositories, their tickets or internal documents.
- No customer, client or machine names.
- No internal process notes, private links or session links.
- No secrets, tokens or real endpoints other than the public defaults.

Describe a change by what it does and why, in terms a stranger can follow.

## Principles that are expensive to get wrong

1. **Outbound only.** The agent dials out. It never opens a listening port and never needs an inbound firewall rule.
2. **Say exactly what leaves the machine.** `README.md` lists every field the agent sends. Any change to what is collected or sent updates that list in the same pull request. Never read or send conversations, prompts, files, credentials or logs.
3. **Runtime-neutral core.** OpenClaw is the first supported runtime, not the only one. Enrolment, transport, buffering, host metrics, profiles, updates and service install must not assume OpenClaw. Runtime knowledge belongs behind an adapter, and new protocol fields get neutral names. The interface is in `internal/adapter`; a runtime's own package (today only `internal/openclaw`) implements it, and nothing outside that package may know how the runtime is probed.
4. **Installed copies are long-lived.** Machines run old versions for months. Do not break the wire protocol, the config file, or the service and folder names for copies already installed. The default profile keeps its original names.
5. **Profiles are isolated.** A command acts on one profile and must never touch another profile's config, binary or service.
6. **An update must not strand a machine.** `update` verifies the checksum, proves the new binary runs, replaces it in one step and restores the old one if the service does not come back. Keep those guarantees, and keep their tests.
7. **No automatic updates.** A copy changes version only when someone runs `update` or the installer.
8. **Runs as the user.** The service is a per-user service and needs no root access.

## Release tracks

| Track | Tag form | Who may publish |
| :--- | :--- | :--- |
| `beta` | `v0.1.0-beta.1` | Pre-releases, for testing. |
| `stable` | `v0.1.0` | Only with the maintainer's explicit sign-off. |

A stable tag is a production release: installers and `update` hand it to every copy on the default track. Never push a stable tag as part of routine work. The release workflow marks hyphenated tags as pre-releases and checks that "latest" stays stable; do not weaken that check.

## Commands

```sh
make build           # bin/beaconclaw
make test            # go test ./...
make cross-compile   # all platforms
make checksums       # bin/checksums.txt for the cross-compiled binaries
```

Before opening a pull request: `gofmt`, `go vet ./...` for linux, darwin and windows, and `go test ./...`. Behaviour changes come with tests.

## Commits and pull requests

- No AI attribution of any kind: no `Co-Authored-By` line for an assistant, no "Generated with" line, no session link.
- Commits are signed. Do not turn signing off.
