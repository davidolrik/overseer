# Commands

## Daemon Management

| Command            | Description                                        |
| ------------------ | -------------------------------------------------- |
| `overseer start`   | Start the daemon in background                     |
| `overseer stop`    | Stop the daemon and disconnect all tunnels         |
| `overseer restart` | Cold restart (reconnects tunnels based on context) |
| `overseer reload`  | Hot reload config (preserves active tunnels)       |
| `overseer daemon`  | Run daemon in foreground (for debugging)           |
| `overseer attach`  | Attach to daemon's log output (Ctrl+C to detach)   |

### `start`

```sh
overseer start [flags]
```

Starts the daemon in the background. If the daemon is already running, reports its status and exits successfully.

| Flag          | Description                                       |
| ------------- | ------------------------------------------------- |
| `-q, --quiet` | Suppress output (useful for shell initialization) |

::: tip Auto-start
Add `overseer start -q` to your shell rc file to ensure the daemon is always running.
:::

### `reload`

Hot reload re-reads your config file without restarting the daemon. Active tunnels are preserved — only new context rules and actions take effect on the next context change.

### `restart`

Cold restart stops the daemon and all tunnels, then starts fresh. Tunnels reconnect based on the current context evaluation.

## Tunnel Management

| Command                       | Aliases | Description                            |
| ----------------------------- | ------- | -------------------------------------- |
| `overseer connect <alias>`    | `c`     | Connect to an SSH host                 |
| `overseer disconnect [alias]` | `d`     | Disconnect tunnel (or all if no alias) |
| `overseer reconnect <alias>`  | `r`     | Reconnect a tunnel                     |

### `connect`

```sh
overseer connect <alias>
```

Connects to an SSH host by its alias (as defined in `~/.ssh/config`). The daemon manages the SSH process and handles reconnection if configured.

### `disconnect`

```sh
overseer disconnect [alias]
```

Disconnects a specific tunnel, or all active tunnels if no alias is given.

### `reconnect`

```sh
overseer reconnect <alias>
```

Disconnects and immediately reconnects a tunnel. Useful after SSH config changes.

## Status and Information

| Command            | Aliases                                   | Description                              |
| ------------------ | ----------------------------------------- | ---------------------------------------- |
| `overseer status`  | `s`, `st`, `list`, `ls`, `context`, `ctx` | Show context, sensors, and tunnels       |
| `overseer qa`      | `q`, `stats`, `statistics`                | Show connectivity statistics and quality |
| `overseer logs`    | `log`                                     | Stream daemon logs in real-time          |
| `overseer version` |                                           | Show version information                 |

### `status`

```sh
overseer status [flags]
```

Displays your current security context, sensor values, active tunnels, and recent events.

| Flag                        | Description                                            |
| --------------------------- | ------------------------------------------------------ |
| `-F, --format <text\|json>` | Output format (default: `text`)                        |
| `-n, --events <count>`      | Number of recent events to show (default: `20`)        |
| `-R, --resolve`             | Resolve IPs in jump chain to hostnames via reverse DNS |

The text output includes:

- Context banner with location, context name, and IP addresses
- Context age (how long you've been in the current context)
- All sensor readings with values
- Active tunnels with state icons, PIDs, connection age, and reconnect counts
- SSH hops displayed as a cascading tree beneath each tunnel
- Companion scripts shown as tree siblings below hops
- Recent events (sensor changes, tunnel events, context transitions)

Example output with a single-hop tunnel with a companion, and a single-hop tunnel without:

```plain
  ✓ gateway (PID: 22187, Age: 6m17s)
  ├── → 203.0.113.10:22
  └── ✓ vpn [running]
  ✓ dev-server (PID: 74917, Age: 1h1m7s)
  └── → 198.51.100.50:22
```

Multi-hop tunnels show a cascading tree of hops:

```plain
  ✓ deep-internal (PID: 4521, Age: 1h30m)
  └── → gate.example.com:22
      └── → dmz.example.com:22
          └── → 10.10.1.50:22
```

Use `--resolve` to translate IP addresses in the hop chain to hostnames via reverse DNS.

JSON output includes all the same data in a structured format for scripting.

### `qa`

```sh
overseer qa [flags]
```

Shows connectivity statistics and network quality assessment based on session history.

| Flag                 | Description                                                          |
| -------------------- | -------------------------------------------------------------------- |
| `-s, --since <date>` | Start date: `today`, `yesterday`, or `YYYY-MM-DD` (default: `today`) |
| `-d, --days <count>` | Number of days to include (default: `1`)                             |

Examples:

```sh
overseer qa                       # Today only
overseer qa -d 7                  # Last 7 days
overseer qa -s yesterday -d 2     # Yesterday and today
overseer qa -s 2025-01-01 -d 7   # Specific date range
```

#### Quality Ratings

Networks are rated based on connection stability:

| Rating        | Description                                                      |
| ------------- | ---------------------------------------------------------------- |
| **Excellent** | Stable connection with long sessions, no consecutive disconnects |
| **Good**      | Mostly stable with occasional brief interruptions                |
| **Fair**      | Some stability issues detected                                   |
| **Poor**      | Frequent disconnects or many consecutive short sessions          |
| **New**       | Single session - insufficient data to assess stability           |

Quality assessment considers consecutive short sessions (strongest instability indicator), total number of brief sessions (< 5 minutes), and reconnection rate per hour of online time.

### `logs`

```sh
overseer logs
```

Streams the daemon's log output in real-time. Press Ctrl+C to stop streaming.

## Password Management

| Command                            | Description                      |
| ---------------------------------- | -------------------------------- |
| `overseer password set <alias>`    | Store password in system keyring |
| `overseer password delete <alias>` | Delete stored password           |
| `overseer password list`           | List hosts with stored passwords |

Passwords are stored in your system keyring (macOS Keychain on macOS, Secret Service on Linux). Overseer uses an internal askpass helper to supply passwords to SSH without terminal interaction.

## Utility Commands

| Command                       | Description                                   |
| ----------------------------- | --------------------------------------------- |
| `overseer reset`              | Reset retry counters for reconnecting tunnels |
| `overseer completion <shell>` | Generate shell completion scripts             |

### `reset`

Resets exponential backoff retry counters for all tunnels. Useful when a transient issue has been resolved and you want tunnels to retry immediately instead of waiting for the backoff timer.

### `completion`

```sh
overseer completion <bash|zsh|fish>
```

Generates shell completion scripts. See [Quick Start](/guide/quick-start#_6-shell-completion) for setup instructions.

## Global Flags

These flags are available on all commands:

| Flag                   | Description                                      |
| ---------------------- | ------------------------------------------------ |
| `--config-path <path>` | Config directory (default: `~/.config/overseer`) |
| `-v, --verbose`        | Increase verbosity (repeat for more: `-vvv`)     |
| `-h, --help`           | Show help                                        |
