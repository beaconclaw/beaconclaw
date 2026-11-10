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
| OpenClaw gateway | Healthy, ready, names of failing channels, uptime, gateway port, OpenClaw version |
| Agent | BeaconClaw agent version (sent once per connection) |

What it does not do, as of this version:

- It does not read or send conversations, sessions, messages, prompts, logs, files, credentials or usage data.
- It reads `~/.openclaw/openclaw.json` only to find the gateway port.
- It cannot receive or run commands from the manager.
- It opens no listening ports.

## Requirements

- An OpenClaw gateway running locally
- A BeaconClaw agent token (`bc_` prefix)
- Go 1.26+ only if you build from source

## Install

Download the binary for your platform from the [releases page](https://github.com/beaconclaw/beaconclaw/releases), make it executable, and put it on your `PATH`. Binaries are published for Linux, macOS and Windows on amd64 and arm64.

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

# Print version
beaconclaw version
```

`beaconclaw install` registers a service for the current user, not a system-wide one: a systemd user unit on Linux, a launchd agent on macOS, and a scheduled task on Windows. It needs no root access. `restart` restarts the BeaconClaw agent, not OpenClaw.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `BEACONCLAW_TOKEN` | Agent token (required) | — |
| `BEACONCLAW_ENDPOINT` | Manager WebSocket URL | `wss://api.beaconclaw.ai/ws` |
| `BEACONCLAW_OPENCLAW_PORT` | Override OpenClaw gateway port | auto-detected |

The agent auto-detects the OpenClaw gateway port from `~/.openclaw/openclaw.json`. You can also set `OPENCLAW_GATEWAY_PORT` to override.

### Config File

Optional. Place at `~/.beaconclaw/config.json`:

```json
{
  "token": "bc_...",
  "endpoint": "wss://api.beaconclaw.ai/ws",
  "openclaw_port": 18789
}
```

Environment variables override the config file, which overrides the defaults.

## Releasing

Releases are automated via GitHub Actions. Pushing a version tag builds cross-platform binaries and publishes them as a GitHub Release.

```sh
# Tag the release
git tag v0.1.0

# Push the tag (triggers the release workflow)
git push origin main --tags
```

The workflow runs tests, cross-compiles for all targets (linux/darwin/windows, amd64/arm64), and attaches the binaries to the release at:

```
https://github.com/beaconclaw/beaconclaw/releases/download/v0.1.0/beaconclaw-linux-amd64
```

The version tag is baked into the binary via `-ldflags` at build time, so `beaconclaw version` will print the tag (e.g. `beaconclaw v0.1.0`).

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
  agent/             Daemon orchestrator
  buffer/            Ring buffer for offline heartbeats
  config/            Config loading and validation
  heartbeat/         Payload assembly and system metrics
  openclaw/          HTTP health probes and version detection
  ws/                WebSocket client and reconnection
```

## Security

To report a vulnerability, use "Report a vulnerability" under this repository's Security tab. Please do not open a public issue for it.

## License

Apache 2.0 — see [LICENSE](LICENSE).
