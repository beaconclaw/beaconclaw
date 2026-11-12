# BeaconClaw Agent

Fleet monitoring sidecar for self-hosted AI agents: [OpenClaw](https://github.com/openclaw/openclaw) and [Hermes Agent](https://github.com/NousResearch/hermes-agent). Runs alongside each agent's gateway, reports health upstream to the BeaconClaw manager over outbound WebSocket.

> **Status:** early software. The hosted BeaconClaw manager is currently invitation-only, so you need an agent token from an existing account to use this.

## How It Works

The agent is a lightweight daemon that:

1. Checks the local runtime every 30 seconds: for OpenClaw, the gateway's `/health` and `/ready`; for Hermes, the state file its gateway keeps (see [Hermes Agent](#hermes-agent))
2. Collects system metrics (CPU, memory, disk)
3. Sends heartbeats to the BeaconClaw manager over WSS
4. Reconnects with exponential backoff if the connection drops
5. Keeps the heartbeats the manager has not yet confirmed, up to two hours' worth, and sends them on reconnect

All connections are **outbound**. No inbound firewall rules needed.

Unconfirmed heartbeats are kept on disk, so a restart does not lose them. In an outage longer than two hours the oldest are dropped first.

## What It Sends

Outbound-only means nothing connects in to your machine. It does not mean no data leaves it. This is everything the agent sends. Everything but the inventory goes every 30 seconds:

| Group | Fields |
|-------|--------|
| Identity | Agent ID, timestamp, sequence number |
| Host | Hostname, operating system, architecture |
| System metrics | CPU percent; memory total, used and percent; root disk total, used and percent |
| Runtime (OpenClaw or Hermes gateway) | Which runtime it is; whether it is present, healthy and ready; names of failing channels; uptime; gateway port (OpenClaw); runtime version |
| Inventory (every 15 minutes) | Names of the skills the runtime has loaded and where each comes from (for example, shipped with the runtime or added on this machine); names of the chat channels, tool servers and model providers it is set up to use; the names of the systems it holds a credential for (for example "Jira"), and how many declared credentials were not recognised; the commit of the runtime's workspace folder and whether that folder has uncommitted changes |
| Usage (every hour) | For today and yesterday, as numbers only: how many conversations had activity, how many user messages, assistant messages and tool calls, the tokens used and what the runtime says they cost, in total and split by where the conversations came from (for example a chat service or a schedule) |
| Agent | BeaconClaw agent version, protocol version, and what the runtime adapter can report (sent once per connection) |

The runtime's state is sent twice in each heartbeat: once in a form that fits any runtime, and once in the original OpenClaw-named form, so that a manager on either protocol version understands it. Both carry the same facts.

What it does not do, as of this version:

- It does not read or send conversations, sessions, messages, prompts, logs, files or credentials. Usage is sent as counts only, as listed above.
- It reads `~/.openclaw/openclaw.json` only to find the gateway port and, for the inventory, the names of the entries under `channels`, `mcp.servers` and `models.providers`, with whether each is switched on. It reads nothing inside those entries: no tokens, keys, addresses or account names. If that file is missing it looks in the older `~/.clawdbot/` and `~/.moldbot/` folders.
- For the inventory it runs `openclaw skills list --json` and keeps each usable skill's name and source. A skill's description and contents are not sent. A name that is not a short plain label (a path, an address, a sentence) is left out.
- **BeaconClaw never opens your environment file.** To learn which systems the runtime can reach, it asks OpenClaw which skills are ready and which credentials they declare (the same `openclaw skills list --json` output). A credential counts when a usable skill declares it and OpenClaw does not report it missing. BeaconClaw sees the credential's name, never its value, and a variable that no skill declares is never seen at all.
- A credential's name is turned into a system's name by a list that ships with the agent ([`internal/catalogue/systems.txt`](internal/catalogue/systems.txt)): `JIRA_API_TOKEN` is sent as "Jira". A name the list does not recognise is not sent; only the number of such names is. `beaconclaw inventory` shows on the machine which systems are sent and which names stayed behind. To add a system to the list, add a line and open a pull request.
- For usage it runs `openclaw gateway call sessions.usage` once for each of the two days and keeps the counts listed above. From each conversation it reads only its channel and its message counts. It does not read or send conversation names, contents, participants, tool names or file names. If the runtime has not finished counting a day, that day is left out rather than sent as zero.
- If the workspace folder is a git checkout it runs `git rev-parse HEAD` and `git status --porcelain` there, and sends the commit and a yes or no for uncommitted changes. It does not send the repository's address, branch names, file names or contents.
- To learn the OpenClaw version it looks for the running gateway among the machine's processes, by command line, and reads the `name` and `version` fields of that copy's `package.json`. Command lines of other processes are not kept or sent. If that finds nothing, it follows the `openclaw` command to its package and reads the same two fields, and as a last resort runs `openclaw --version`.
- When you run `beaconclaw maintenance start` or `end`, it sends one request to the manager with the agent's token: the reason you typed and the length in minutes, or nothing for `end`. This is the only thing it sends that you type yourself. The token can only put its own agent into maintenance.
- **Out of the box it cannot receive or run anything from the manager.** Remote actions are an add-on you switch on yourself, one action at a time; see [Remote actions](#remote-actions). Even then the manager can only name an action from a short fixed list. It cannot send a command line, and it cannot switch an action on.
- With a remote action switched on, and only when one is asked for, it sends back the result: for `diagnostics`, each check's name, whether it passed and a short state such as a version or "41% used"; for `restart_gateway`, whether the gateway answered again, whether the runtime's restart command reported an error, and how many seconds the gateway took to answer after that command returned. A refused request sends back the reason. When it connects it also sends the names of the actions you have switched on.
- It opens no listening ports.

The points above about `openclaw.json`, `openclaw skills list`, credentials, `sessions.usage` and the version describe an OpenClaw machine. On a Hermes machine, see the next section.

### Hermes Agent

On a machine running [Hermes Agent](https://github.com/NousResearch/hermes-agent), the agent reads Hermes's home folder (`$HERMES_HOME`, or `~/.hermes`) as follows, and nothing else in it:

- **Health:** `gateway_state.json`, the file the Hermes gateway rewrites at least once a minute. It keeps the gateway's state, each messaging platform's state, the gateway's process ID and the Hermes version. The gateway counts as healthy when its process is alive, its state is running (or degraded or draining), and the file is less than two minutes old. A file that has stopped being rewritten while the process is still alive is reported as a failing `gateway`: the case where Hermes still says "connected" but has stopped working. A platform that is retrying, disconnected, failed or needs attention is reported by name.
- **Inventory:** the names of the folders under `skills/` that hold a `SKILL.md`, marked as shipped with Hermes when they appear in its `.bundled_manifest`; the names of the platforms in the state file; and, from `config.yaml`, the names of the platforms switched on, the entries under `mcp_servers` and `providers`, and `model.provider`. It reads nothing inside those entries. Hermes has no list of declared credentials, so no system names are sent.
- **Usage:** `state.db`, opened read-only. For each day it counts conversations with activity, user and assistant messages, tool results and tokens, split by where the conversation came from (for example `telegram` or `cron`). It reads only roles, timestamps, token counts and the conversation's source. It never reads a message's text.
- **Version:** the `code_version` Hermes writes into its state file.
- **It never opens Hermes's `.env` file** or `auth.json`.

Each copy of the agent reports on one runtime. `beaconclaw auth` saves which: the one given with `--runtime openclaw` or `--runtime hermes`, or the only one installed. A saved choice never changes by itself, so a runtime that is down is reported as down rather than swapped for another. With nothing saved, a machine with only Hermes is reported as Hermes, and anything else as OpenClaw, as in earlier versions. `BEACONCLAW_RUNTIME` overrides the saved choice. A Hermes home other than the default is set with `"hermes_home"` or `BEACONCLAW_HERMES_HOME`.

### Both runtimes on one machine

Each runtime is a separate agent in BeaconClaw, with its own token. Run one copy for each, as two [profiles](#profiles):

```sh
beaconclaw auth --runtime openclaw                       # the default profile watches OpenClaw
beaconclaw --profile hermes auth --token bc_... --runtime hermes
beaconclaw --profile hermes install
```

When both are installed and no choice is saved, `beaconclaw status` and the agent's log say so and print these commands. The default profile keeps reporting on OpenClaw meanwhile.

Heartbeats the manager has not yet acknowledged are kept in `outbox.json` in the agent's config folder, readable only by your user, so an outage or a restart does not lose them. By default up to two hours' worth are kept; the oldest are dropped first. They are removed as soon as the manager confirms them.

## Requirements

- An OpenClaw or Hermes Agent gateway running locally
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

# Tell the manager this agent is being worked on, then that the work is done
beaconclaw maintenance start --reason "runtime upgrade" --for 2h
beaconclaw maintenance end

# Check status
beaconclaw status

# Show which connected systems are reported, and what stays on this machine
beaconclaw inventory

# Remove the system service
beaconclaw uninstall

# Print version and release track
beaconclaw version
```

`beaconclaw install` registers a service for the current user, not a system-wide one: a systemd user unit on Linux, a launchd agent on macOS, and a scheduled task on Windows. It needs no root access. `restart` restarts the BeaconClaw agent, not OpenClaw.

### Maintenance

While an agent is in maintenance, the manager holds back its alerts and leaves the time out of its uptime. Start it before working on the agent and end it after:

```sh
beaconclaw maintenance start --reason "runtime upgrade" --for 2h   # --for defaults to 1h; at most 14 days
beaconclaw maintenance end
```

Starting again while it is already in maintenance keeps the one window, made longer if the new request runs later. If you forget to end it, it ends by itself at the time given.

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
| `BEACONCLAW_RUNTIME` | `openclaw` or `hermes` | found automatically |
| `BEACONCLAW_HERMES_HOME` | Hermes's home folder | `$HERMES_HOME`, then `~/.hermes` |
| `BEACONCLAW_PROFILE` | Profile to act on | default profile |
| `BEACONCLAW_INVENTORY` | `false` to stop reporting skill and connection names | `true` |
| `BEACONCLAW_INVENTORY_ALL_NAMES` | `true` to also send the names of declared credentials the list does not recognise | `false` |
| `BEACONCLAW_USAGE` | `false` to stop reporting daily usage counts | `true` |

`beaconclaw update` also reads `GITHUB_TOKEN` if it is set, and sends it to GitHub only to raise the download rate limit. `BEACONCLAW_RELEASES_API` and `BEACONCLAW_RELEASES_DOWNLOAD` point the updater at another release host for testing; it must be an `https` address unless it is on this machine.

The manager address must start with `wss://`. An unencrypted `ws://` address is accepted only for a manager on the same machine.

On Linux, `beaconclaw install` copies your current `PATH` into the service definition so the service can find `openclaw` or `hermes`.

The agent auto-detects the OpenClaw gateway port from `~/.openclaw/openclaw.json`. You can also set `OPENCLAW_GATEWAY_PORT` to override.

### Switching the inventory off

The inventory is on by default. To send health only, set `"inventory": false` in the config file or `BEACONCLAW_INVENTORY=false` in the service's environment, then restart the agent. Nothing is then collected or sent for it, and the agent tells the manager it does not report an inventory.

### Sending unrecognised credential names

Off by default. To also send the names of declared credentials the list does not recognise, set `"inventory_all_names": true` in the config file or `BEACONCLAW_INVENTORY_ALL_NAMES=true` in the service's environment, then restart the agent. Names only, as before, and only those a skill declares. They are shown to the organization that operates the agent.

### Switching usage off

Usage is on by default. To stop it, set `"usage": false` in the config file or `BEACONCLAW_USAGE=false` in the service's environment, then restart the agent. The inventory and usage switches are independent.

### Remote actions

An add-on, **off unless you switch it on on this machine**. With an action switched on, someone who operates the agent can ask this machine to do that one thing from the manager, without signing in to the machine.

| Action | What it does here |
| :-- | :-- |
| `diagnostics` | Runs the agent's own checks again (gateway answering and ready, versions, memory, disk) and sends back each check's name and state. Changes nothing. |
| `restart_gateway` | Runs the runtime's own restart, the command you would run by hand, then reports whether the gateway answered again. On OpenClaw that is `openclaw gateway restart`: conversations in progress are cut off, and the gateway is away for a few minutes. On Hermes it is `hermes gateway restart` without `--yes`, so Hermes refuses rather than interrupt a conversation or job in progress; it lets the gateway finish its work and comes back in seconds. A gateway that is completely frozen cannot finish its work, so this restart does not bring it back, and the result says so. |

```sh
beaconclaw actions                              # what is on and off
beaconclaw actions allow diagnostics            # switch one on
beaconclaw actions allow restart_gateway
beaconclaw actions disallow --all               # switch everything off
```

What keeps this safe:

- **Only this command switches an action on.** There is no setting for it on the manager and no environment variable. The choice is kept in this profile's config file.
- **The list is fixed in the code.** The manager sends the name of an action and nothing else. There is no field for a command line or arguments, and an unknown name is refused.
- **Each request is checked here**, whatever the manager says: it must be for this agent, switched on, not expired (the manager allows one minute), and not seen before. At most one restart every five minutes.
- **Every request is recorded on this machine**, carried out or refused, in `actions.log` in the agent's config folder. That file is your copy; it is never sent.
- **Nothing is queued.** A request reaches the agent only while it is connected, over the connection the agent itself opened. No port is opened.
- What comes back is names and states: no logs, no output from the runtime, no conversation content.

To build a copy that can never perform a remote action, whatever its settings say: `go build -tags noactions ./cmd/beaconclaw`.

### Config File

Optional. Place at `~/.beaconclaw/config.json`:

```json
{
  "token": "bc_...",
  "endpoint": "wss://api.beaconclaw.ai/ws",
  "openclaw_port": 18789,
  "runtime": "openclaw",
  "channel": "stable",
  "inventory": true,
  "usage": true
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
  actions/           Remote actions: the fixed list, and the checks before one is carried out
  adapter/           The boundary between the agent and the runtime it watches
  agent/             Daemon orchestrator
  config/            Config loading and validation
  heartbeat/         Payload assembly and system metrics
  outbox/            Heartbeats waiting to be acknowledged, kept on disk
  hermes/            The Hermes Agent adapter: state file, inventory, usage, restart
  openclaw/          The OpenClaw adapter: health probes, version and port lookup
  runtimes/          Picks the adapter for the runtime on this machine
  profile/           Named profiles: separate copies on one machine
  update/            Release lookup, checksum verification, binary replacement
  ws/                WebSocket client and reconnection
```

## Security

To report a vulnerability, use "Report a vulnerability" under this repository's Security tab. Please do not open a public issue for it.

## License

Apache 2.0 — see [LICENSE](LICENSE).
