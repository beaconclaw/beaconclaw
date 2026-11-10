# Security

## Reporting a vulnerability

Please report it privately, not in a public issue or pull request.

Use **Report a vulnerability** on this repository's
[Security tab](https://github.com/beaconclaw/beaconclaw/security/advisories/new).
That opens a private report only the maintainers can see.

Say what you found, how to reproduce it, and which version it affects
(`beaconclaw version`). You will get a reply within a few working days.

## What is in scope

- The agent in this repository: what it reads on the machine, what it sends,
  how it updates itself, and how it stores its token.
- The install scripts and the release workflow that builds the binaries.

## Supported versions

Fixes go into the newest release. Update with `beaconclaw update`.

## How releases are protected

- Release binaries are built by the workflow in `.github/workflows/release.yml`
  from a version tag, never from a pull request or a fork.
- Version tags cannot be moved or deleted once published.
- Each release carries a `checksums.txt`, which `beaconclaw update` verifies
  before replacing the running copy.
- An official (stable) release needs the maintainer's approval before it is
  published.
