# BeaconClaw Agent

Fleet monitoring sidecar for [OpenClaw](https://github.com/openclaw/openclaw) instances. Runs alongside each OpenClaw gateway, reports health upstream to the BeaconClaw manager over outbound WebSocket.

## How It Works

The agent is a lightweight daemon that:

1. Probes the local OpenClaw gateway (`/health`, `/ready`) every 30 seconds
2. Collects system metrics (CPU, memory, disk)
3. Sends heartbeats to the BeaconClaw manager over WSS
4. Reconnects with exponential backoff if the connection drops
5. Buffers heartbeats during disconnection — no data loss

All connections are **outbound**. No inbound firewall rules needed.

## Requirements

- Go 1.22+
- An OpenClaw instance running locally
- A BeaconClaw fleet token (`bc_` prefix)

## Local Development

```sh
# Install dependencies
go mod tidy

# Build
make build

# Run tests
make test

# Cross-compile (linux/darwin, amd64/arm64)
make cross-compile
```

The binary is output to `bin/beaconclaw`.

## Usage

```sh
# Start the daemon
beaconclaw --token bc_your_token_here

# Check local OpenClaw status
beaconclaw status

# Print version
beaconclaw version
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `BEACONCLAW_TOKEN` | Fleet token (required) | — |
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

Priority: CLI flags > env vars > config file > defaults.

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

## License

Apache 2.0 — see [LICENSE](LICENSE).
