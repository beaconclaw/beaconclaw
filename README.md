# BeaconClaw Agent

Fleet monitoring sidecar for [OpenClaw](https://github.com/openclaw/openclaw). Runs alongside each OpenClaw gateway, reports health upstream to the BeaconClaw manager over outbound WebSocket.

> **Status:** early software. The hosted BeaconClaw manager is currently invitation-only, so you need an agent token from an existing account to use this.

## How It Works

The agent is a lightweight daemon that:

1. Probes the local OpenClaw gateway (`/health`, `/ready`) every 30 seconds
2. Collects system metrics (CPU, memory, disk)
3. Sends heartbeats to the BeaconClaw manager over WSS
4. Reconnects with exponential backoff if the connection drops
5. Keeps up to 100 recent heartbeats in memory while disconnected and sends them on reconnect

All connections are **outbound**. No inbound firewall rules needed.

The buffer is not saved to disk. Heartbeats can be lost during a long outage or when the agent restarts.

## What It Sends

Outbound-only means nothing connects in to your machine. It does not mean no data leaves it. This is everything the agent sends, every 30 seconds:

| Group | Fields |
|-------|--------|
| Identity | Agent ID, timestamp, sequence number |
| Host | Hostname, operating system, architecture |
| System metrics | CPU percent; memory total, used and percent; root disk total, used and percent |
| Runtime (OpenClaw gateway) | Which runtime it is; whether it is present, healthy and ready; names of failing channels; uptime; gateway port; runtime version |
| Agent | BeaconClaw agent version, protocol version, and what the runtime adapter can report (sent once per connection) |

The runtime's state is sent twice in each heartbeat: once in a form that fits any runtime, and once in the original OpenClaw-named form, so that a manager on either protocol version understands it. Both carry the same facts.

What it does not do, as of this version:

- It does not read or send conversations, sessions, messages, prompts, logs, files, credentials or usage data.
- It reads `~/.openclaw/openclaw.json` only to find the gateway port. If that file is missing it looks for the same setting in the older `~/.clawdbot/` and `~/.moldbot/` folders.
- It runs `openclaw --version` to learn the OpenClaw version, and nothing else.
- It cannot receive or run commands from the manager.
- It opens no listening ports.

Heartbeats the manager has not yet acknowledged are kept in `outbox.json` in the agent's config folder, readable only by your user, so an outage or a restart does not lose them. By default up to two hours' worth are kept; the oldest are dropped first. They are removed as soon as the manager confirms them.

## Requirements

- An OpenClaw gateway running locally
- A BeaconClaw agent token (`bc_` prefix)
- Go 1.27+ only if you build from source

## Install

Download the binary for your platform from the [releases page](https://github.com/beaconclaw/beaconclaw/releases), make it executable, and put it on your `PATH`. Binaries are published for Linux, macOS and Windows on amd64 and arm64.

Each release includes a `checksums.txt` file with the SHA-256 of every binary.

## Release Tracks

| Track | What it gets | Tag form |
|-------|--------------|----------|
| `stable` (default) | Official releases only | `v0.1.0` |
| `beta` | Pre-releases as well | `v0.1.0-beta.1` |

A copy stays on `stable` unless you move it. Pre-releases are published as GitHub pre-releases and are never marked "latest".

## Updating

```sh
# Update to the newest release on this copy's track
beaconclaw update

# Only report what is available
beaconclaw update --check

# Switch track, then update. The choice is remembered.
beaconclaw update --channel beta

# Install an exact version
beaconclaw update --version v0.1.0-beta.1

# Show or set the track without updating
beaconclaw channel
beaconclaw channel beta
```

`update` downloads the binary for your platform, checks it against the release's `checksums.txt`, confirms the new binary runs, replaces the old one in a single step, and restarts the service if it was running. If any step fails, the old binary stays in place. If the service does not come back on the new version, the old binary is restored and restarted.

Without `--version`, `update` never moves to an older version than the one installed.

There are no automatic updates. A copy changes version only when someone runs `update`.

## Local Development

```sh
# Install dependencies
go mod tidy

# Build
make build

# Run tests
make test

# Cross-compile (linux/darwin/windows, amd64/arm64)
make cross-compile
```

The binary is output to `bin/beaconclaw`.

## Usage

```sh
# Authenticate with your agent token (one-time setup)
beaconclaw auth --token bc_your_token_here

# Install as a user-level service (starts at login or boot, restarts on failure)
beaconclaw install

# Manage the daemon
beaconclaw start
beaconclaw stop
beaconclaw restart

# Check status
beaconclaw status

# Remove the system service
beaconclaw uninstall

# Print version and release track
beaconclaw version
```

`beaconclaw install` registers a service for the current user, not a system-wide one: a systemd user unit on Linux, a launchd agent on macOS, and a scheduled task on Windows. It needs no root access. `restart` restarts the BeaconClaw agent, not OpenClaw.

### Profiles

A profile is a second, independent copy of the agent on the same machine, for example one reporting to a test manager beside the normal one. Each profile has its own config folder, agent ID, token, release track, service and logs.

```sh
beaconclaw --profile dev auth --token bc_your_other_token --endpoint wss://manager.example.com/ws
beaconclaw --profile dev install
beaconclaw --profile dev status
```

| | Default profile | Profile `dev` |
|---|---|---|
| Config folder | `~/.beaconclaw/` | `~/.beaconclaw-dev/` |
| Linux service | `beaconclaw.service` | `beaconclaw-dev.service` |
| macOS service | `ai.beaconclaw.agent` | `ai.beaconclaw.agent.dev` |
| Windows task | `BeaconClaw` | `BeaconClaw-dev` |

`--endpoint` saves the manager address in that profile's config. `auth` also reads the token from `BEACONCLAW_TOKEN`, which keeps it out of the command line and the shell history.

Every command acts on one profile and leaves the others alone. Choose the profile with `--profile`, with `BEACONCLAW_PROFILE`, or by installing the binary under the name `beaconclaw-<profile>`: a binary named `beaconclaw-dev` acts on the `dev` profile with no flag. Giving each profile its own binary also lets them run different versions, since `update` replaces only the binary it is run from.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `BEACONCLAW_TOKEN` | Agent token (required) | — |
| `BEACONCLAW_ENDPOINT` | Manager WebSocket URL | `wss://api.beaconclaw.ai/ws` |
| `BEACONCLAW_OPENCLAW_PORT` | Override OpenClaw gateway port | auto-detected |
| `BEACONCLAW_PROFILE` | Profile to act on | default profile |

`beaconclaw update` also reads `GITHUB_TOKEN` if it is set, and sends it to GitHub only to raise the download rate limit. `BEACONCLAW_RELEASES_API` and `BEACONCLAW_RELEASES_DOWNLOAD` point the updater at another release host for testing; it must be an `https` address unless it is on this machine.

The manager address must start with `wss://`. An unencrypted `ws://` address is accepted only for a manager on the same machine.

On Linux, `beaconclaw install` copies your current `PATH` into the service definition so the service can find `openclaw`.

The agent auto-detects the OpenClaw gateway port from `~/.openclaw/openclaw.json`. You can also set `OPENCLAW_GATEWAY_PORT` to override.

### Config File

Optional. Place at `~/.beaconclaw/config.json`:

```json
{
  "token": "bc_...",
  "endpoint": "wss://api.beaconclaw.ai/ws",
  "openclaw_port": 18789,
  "channel": "stable"
}
```

A named profile reads `~/.beaconclaw-<profile>/config.json` instead.

Environment variables override the config file, which overrides the defaults.

## Releasing

Releases are automated via GitHub Actions. Pushing a version tag builds cross-platform binaries and publishes them as a GitHub Release. The tag decides the track:

```sh
# Pre-release on the beta track
git tag v0.1.0-beta.1
git push origin v0.1.0-beta.1

# Official release on the stable track
git tag v0.1.0
git push origin v0.1.0
```

The workflow runs tests, cross-compiles for all targets (linux/darwin/windows, amd64/arm64), writes `checksums.txt`, and attaches the files to the release at:

```
https://github.com/beaconclaw/beaconclaw/releases/download/v0.1.0/beaconclaw-linux-amd64
```

A tag with a pre-release suffix (`-alpha.N`, `-beta.N`, `-rc.N`) is published as a pre-release and is not marked "latest". Any other tag shape is rejected. The workflow ends by checking that "latest" still points at a stable release.

The version tag is baked into the binary via `-ldflags` at build time, so `beaconclaw version` will print the tag (e.g. `beaconclaw v0.1.0 (track: stable)`).

### Manual Deploy

To deploy without a release (e.g. testing on a VM):

```sh
# Build for the target platform
GOOS=linux GOARCH=amd64 make build

# Copy to the server, into the directory on the service user's PATH where the binary is installed
scp bin/beaconclaw-linux-amd64 user@your-vm:.local/bin/beaconclaw
ssh user@your-vm 'chmod +x ~/.local/bin/beaconclaw && beaconclaw restart'
```

## Project Structure

```
cmd/beaconclaw/      CLI entrypoint
internal/
  adapter/           The boundary between the agent and the runtime it watches
  agent/             Daemon orchestrator
  config/            Config loading and validation
  heartbeat/         Payload assembly and system metrics
  outbox/            Heartbeats waiting to be acknowledged, kept on disk
  openclaw/          The OpenClaw adapter: health probes, version and port lookup
  profile/           Named profiles: separate copies on one machine
  update/            Release lookup, checksum verification, binary replacement
  ws/                WebSocket client and reconnection
```

## Security

To report a vulnerability, use "Report a vulnerability" under this repository's Security tab. Please do not open a public issue for it.

## License

Apache 2.0 — see [LICENSE](LICENSE).
