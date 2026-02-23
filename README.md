# Overseer - Contextual Computing

<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/docs/public/overseer.png">

Automate your network connectivity based on where you are.
Overseer detects your context based upon your surroundings and manages SSH tunnels, VPN clients, and helper scripts automagically.

Define contexts like "home", "office", or "public wifi" with detection rules based on your public IP.
When your context changes, overseer connects the right tunnels and starts the right companion scripts,
VPN clients, SOCKS proxies, authentication helpers.

Whatever your workflow needs.

## Features

- **Full OpenSSH Integration**: Supports everything OpenSSH can do (connection reuse, SOCKS proxies, port forwarding, jump hosts)
- **Context Awareness**: Automatically detect your logical location and connect/disconnect SSH tunnels based on your context
- **Companion Scripts**: Run helper scripts alongside tunnels (VPN clients, proxies, setup scripts) with automatic restart on failure
- **Location/Context Hooks**: Execute scripts automatically when entering or leaving locations or contexts
- **Connectivity Statistics**: Track network stability with session history and quality ratings
- **Automatic Reconnection**: Tunnels automatically reconnect with exponential backoff when connections fail
- **Secure Password Storage**: Store passwords in your system keyring (Keychain/Secret Service)
- **Shell Completion**: Dynamic completion for commands and SSH host aliases (bash, zsh, fish)
- **Multiple Output Formats**: Status available in plaintext (with colors) and JSON for easy automation

## Installation

### Install using mise

```sh
mise use --global github:davidolrik/overseer@latest
```

### Install using go directly

```sh
go install go.olrik.dev/overseer@latest
```

### Manual installation

Download a precompiled binary from the [GitHub releases page](https://github.com/davidolrik/overseer/releases/latest),
extract it, and move it to a directory in your `PATH`:

```sh
# Example for macOS (Apple Silicon)
curl -L https://github.com/davidolrik/overseer/releases/latest/download/overseer_darwin_arm64.tar.gz | tar xz
sudo mv overseer /usr/local/bin/
```

#### Available binaries

- `overseer_darwin_arm64.tar.gz` - macOS (Apple Silicon)
- `overseer_darwin_amd64.tar.gz` - macOS (Intel)
- `overseer_linux_arm64.tar.gz` - Linux (ARM64)
- `overseer_linux_amd64.tar.gz` - Linux (x86_64)

## Quick Start

1. Start the overseer daemon:

   ```sh
   overseer start
   ```

2. Check current status:

   ```sh
   overseer status
   ```

3. Manually connect to an SSH host:

   ```sh
   overseer connect my-server
   ```

4. Configure automatic context-based connections by editing `~/.config/overseer/config.hcl`

## Commands

### Daemon Management

| Command            | Description                                        |
| ------------------ | -------------------------------------------------- |
| `overseer start`   | Start the daemon in background                     |
| `overseer stop`    | Stop the daemon and disconnect all tunnels         |
| `overseer restart` | Cold restart (reconnects tunnels based on context) |
| `overseer reload`  | Hot reload config (preserves active tunnels)       |
| `overseer daemon`  | Run daemon in foreground (for debugging)           |
| `overseer attach`  | Attach to daemon's log output (Ctrl+C to detach)   |

### Tunnel Management

| Command                                   | Aliases | Description                                           |
| ----------------------------------------- | ------- | ----------------------------------------------------- |
| `overseer connect <alias> [--tag TAG]...` | `c`     | Connect to an SSH host (sets OVERSEER_TAG env var)    |
| `overseer disconnect [alias]`             | `d`     | Disconnect tunnel (or all if no alias)                |
| `overseer reconnect <alias>`              | `r`     | Reconnect a tunnel                                    |

### Status & Information

| Command            | Aliases                                   | Description                              |
| ------------------ | ----------------------------------------- | ---------------------------------------- |
| `overseer status`  | `s`, `st`, `list`, `ls`, `context`, `ctx` | Show context, sensors, and tunnels       |
| `overseer qa`      | `q`, `stats`, `statistics`                | Show connectivity statistics and quality |
| `overseer logs`    | `log`                                     | Stream daemon logs in real-time          |
| `overseer version` |                                           | Show version information                 |

### Password Management

| Command                            | Description                      |
| ---------------------------------- | -------------------------------- |
| `overseer password set <alias>`    | Store password in system keyring |
| `overseer password delete <alias>` | Delete stored password           |
| `overseer password list`           | List hosts with stored passwords |

### Companion Management

| Command                                            | Description                                   |
| -------------------------------------------------- | --------------------------------------------- |
| `overseer companion list`                          | List all companions and their status          |
| `overseer companion status -T <tunnel>`            | Show detailed companion status                |
| `overseer companion start -T <tunnel> -N <name>`   | Start a specific companion                    |
| `overseer companion stop -T <tunnel> -N <name>`    | Stop a specific companion                     |
| `overseer companion restart -T <tunnel> -N <name>` | Restart a specific companion                  |
| `overseer companion attach -T <tunnel> -N <name>`  | Attach to companion output (Ctrl+C to detach) |

### Utility Commands

| Command               | Description                                   |
| --------------------- | --------------------------------------------- |
| `overseer reset`      | Reset retry counters for reconnecting tunnels |
| `overseer completion` | Generate shell completion scripts             |

## Global Flags

```plain
--config-path <path>  Config directory (default: ~/.config/overseer)
-v, --verbose         Increase verbosity (repeat for more: -vvv)
-h, --help            Show help
```

## Configuration

Overseer uses [HCL](https://github.com/hashicorp/hcl) format for configuration. The config file is located at `~/.config/overseer/config.hcl`.

### Global Settings

```hcl
verbose = 0

ssh {
  server_alive_interval = 15    # Keepalive interval in seconds
  server_alive_count_max = 3    # Exit after N failed keepalives
  reconnect_enabled = true      # Enable auto-reconnect
  initial_backoff = "1s"        # First retry delay
  max_backoff = "5m"            # Maximum retry delay
  backoff_factor = 2            # Exponential backoff multiplier
  max_retries = 10              # Give up after N attempts
}

exports {
  dotenv = "/path/to/overseer.env"     # Export context as env file
  context = "/path/to/context.txt"     # Export context name
  location = "/path/to/location.txt"   # Export location name
  public_ip = "/path/to/public_ip.txt" # Export public IP
  preferred_ip = "ipv4"                # Preferred IP version (ipv4 or ipv6)
}
```

### Split Config Files (`config.d/`)

As your configuration grows, you can split it into multiple files by creating a `config.d/` directory alongside `config.hcl`:

```plain
~/.config/overseer/
├── config.hcl          # Main config (loaded first)
└── config.d/           # Optional directory
    ├── home.hcl        # Home network locations & contexts
    ├── office.hcl      # Office network locations & contexts
    └── vpn-tunnels.hcl # Tunnel definitions
```

Files in `config.d/` are loaded in **alphabetical order** after the main config. Only `.hcl` files are loaded — other files and subdirectories are ignored. The directory is optional; if absent, behavior is unchanged.

**Merge rules:**

| Config element                                                 | Rule                                                                                                     |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| Scalars (`verbose`)                                            | Last non-zero value wins                                                                                 |
| Singleton blocks (`exports`, `ssh`, `companion`, global hooks) | Must only appear in one file (error if duplicated)                                                       |
| Locations / Tunnels                                            | Accumulated across files; duplicate names are an error                                                   |
| Contexts                                                       | Same-name contexts are deep-merged (locations, actions, hooks append + deduplicate; environment merges keys; scalars use first-non-empty). Distinct names accumulate in load order. Order matters: first match wins |

Changes to files in `config.d/` trigger an automatic daemon reload. If you create `config.d/` after the daemon is already running, use `overseer reload` to pick it up.

### Defining Locations

Locations represent physical or network locations detected by sensors:

```hcl
location "home" {
  display_name = "Home Network"

  conditions {
    public_ip = ["203.0.113.42"]         # Match specific IP
  }

  environment = {
    LOCATION_TYPE = "residential"        # Custom env vars to export
  }
}

location "office" {
  display_name = "Office Network"

  conditions {
    public_ip = ["198.51.100.0/24"]      # Match CIDR range
  }
}
```

### Defining Contexts

Contexts group locations and define actions to take:

```hcl
context "trusted" {
  display_name = "Trusted Network"

  # Reference one or more locations
  locations = ["home", "office"]

  environment = {
    TRUST_LEVEL = "high"
  }

  actions {
    connect = ["home-lab", "dev-server"]  # Tunnels to connect in this context
    disconnect = ["vpn"]                   # Tunnels to disconnect
  }
}

context "untrusted" {
  display_name = "Public Network"

  # Matches when online but no other context matches
  conditions {
    online = true
  }

  actions {
    connect = ["vpn"]
    disconnect = ["home-lab"]
  }
}
```

### Advanced Conditions

Use nested `any` or `all` blocks for complex matching:

```hcl
location "corporate" {
  display_name = "Corporate Network"

  conditions {
    all {
      online = true
      any {
        public_ip = ["198.51.100.0/24"]
        public_ip = ["203.0.113.0/24"]
      }
    }
  }
}
```

### Tunnel Configuration

Configure per-tunnel settings including SSH tags and companion scripts:

```hcl
tunnel "my-server" {
  tag = "production"  # Sets OVERSEER_TAG env var on the SSH process

  companion "setup-script" {
    command = "~/scripts/prepare-connection.sh"
  }
}
```

Tags are set as the `OVERSEER_TAG` environment variable on the SSH process. This propagates through ProxyJump chains (child processes inherit environment variables), unlike SSH's `-P` flag which doesn't. Use `Match exec` in your `~/.ssh/config` to match on it:

```plain
Match Host bastion exec "[[ $OVERSEER_TAG = production ]]"
  ProxyJump bastion.example.com
```

Tags from the config file can be overridden by `--tag` on the command line.

### Companion Scripts

Companion scripts are helper processes that run alongside tunnels.
They start before the tunnel connects and are terminated when the tunnel disconnects. Common use cases include:

- Starting a VPN client before connecting through it
- Running a HTTP proxy alongside a tunnel
- Executing setup scripts (starting Docker containers, etc.)
- Running authentication helpers

#### Basic Configuration

```hcl
tunnel "my-server" {
  companion "setup-script" {
    command = "~/scripts/prepare-connection.sh"
    timeout = "30s"
  }
}
```

#### Configuration Options

| Option         | Type     | Default      | Description                                                                |
| -------------- | -------- | ------------ | -------------------------------------------------------------------------- |
| `command`      | string   | *required*   | Command to execute (supports `~` expansion)                                |
| `workdir`      | string   | -            | Working directory for the command                                          |
| `environment`  | map      | `{}`         | Environment variables to set                                               |
| `wait_mode`    | string   | `completion` | How to determine readiness: `completion` or `string`                       |
| `wait_for`     | string   | -            | String to wait for (required when `wait_mode = "string"`)                  |
| `timeout`      | duration | `30s`        | Maximum time to wait for readiness                                         |
| `on_failure`   | string   | `block`      | Action on failure: `block` (abort tunnel) or `continue`                    |
| `keep_alive`   | bool     | `true`       | Keep running after tunnel connects                                         |
| `auto_restart` | bool     | `false`      | Automatically restart if the companion exits unexpectedly                  |
| `ready_delay`  | duration | -            | Delay after ready before proceeding (e.g., `2s` for network stabilization) |
| `persistent`   | bool     | `false`      | Keep running when tunnel disconnects (survives reconnect cycles)           |
| `stop_signal`  | string   | `INT`        | Signal to send on stop: `INT`, `TERM`, or `HUP`                            |

#### PTY-Based Process Control

Companion scripts run inside a pseudo-terminal (PTY) which provides:

- **Reliable Signal Delivery**: Ctrl+C is delivered via the terminal driver, ensuring signals reach even privileged processes (like `sudo openconnect`)
- **Process Group Handling**: The entire process group receives signals, properly terminating child processes
- **Unified Output**: stdout and stderr are merged into a single output stream

#### Wait Modes

**`completion`** - Wait for the script to exit successfully (exit code 0):

```hcl
companion "start-docker" {
  command = "docker compose up -d postgres"
  wait_mode = "completion"
  timeout = "120s"
}
```

**`string`** - Wait for a specific string in the output:

```hcl
companion "socks-proxy" {
  command = "ssh -D 1080 -N proxy-host"
  wait_mode = "string"
  wait_for = "Entering interactive session"
  timeout = "60s"
}
```

#### Long-Running Companions

For companions that need to stay running (proxies, VPN clients), use `keep_alive = true` (the default).
Add `auto_restart = true` to automatically restart if they crash:

```hcl
tunnel "corporate" {
  companion "vpn-client" {
    command = "~/bin/start-vpn.sh"
    wait_mode = "string"
    wait_for = "Connected"
    timeout = "60s"
    keep_alive = true       # Keep running (default)
    auto_restart = true     # Restart if it crashes
  }
}
```

#### Persistent Companions

Use `persistent = true` for companions that should keep running even when the tunnel disconnects.
This is useful for services that take a long time to start (VPNs, proxies) and should survive tunnel reconnect cycles:

```hcl
tunnel "home-server" {
  companion "socks-proxy" {
    command = "ssh -D 1080 -N proxy-host"
    wait_mode = "string"
    wait_for = "Entering interactive session"
    keep_alive = true
    persistent = true    # Survives tunnel disconnect/reconnect
  }
}
```

#### One-Shot Scripts

For setup scripts that should complete and exit, use `keep_alive = false`:

```hcl
tunnel "dev-server" {
  companion "db-migrate" {
    command = "~/scripts/run-migrations.sh"
    wait_mode = "completion"
    timeout = "60s"
    keep_alive = false      # Exit after completion is expected
    on_failure = "continue" # Connect anyway if migrations fail
  }
}
```

#### Multiple Companions

Companions run sequentially in the order defined. Use this for dependencies:

```hcl
tunnel "database-tunnel" {
  # First: Start the database container
  companion "start-db" {
    command = "docker compose up -d postgres"
    wait_mode = "completion"
    timeout = "120s"
    on_failure = "block"
  }

  # Second: Wait for it to be healthy
  companion "wait-healthy" {
    command = "docker compose exec postgres pg_isready"
    wait_mode = "completion"
    timeout = "30s"
    on_failure = "continue"  # Proceed even if health check fails
  }

  # Third: Start a proxy that stays running
  companion "db-proxy" {
    command = "~/bin/db-proxy.sh"
    wait_mode = "string"
    wait_for = "Listening on port 5432"
    keep_alive = true
    auto_restart = true
  }
}
```

#### Environment Variables

Pass custom environment variables to companions:

```hcl
companion "vpn-client" {
  command = "~/bin/vpn-connect.sh"
  environment = {
    VPN_PROFILE = "corporate"
    VPN_SERVER = "vpn.example.com"
  }
}
```

The tunnel alias is passed as the first argument to the command, so your script can access it as `$1`.

#### Managing Companions

```sh
# List all companions and their status
overseer companion list

# Show detailed status for a tunnel's companions
overseer companion status -T my-tunnel

# Manually start/stop/restart a companion
overseer companion start -T my-tunnel -N vpn-client
overseer companion stop -T my-tunnel -N vpn-client
overseer companion restart -T my-tunnel -N vpn-client

# Attach to companion output (useful for debugging)
overseer companion attach -T my-tunnel -N vpn-client
```

#### Companion States

| State      | Description                                      |
| ---------- | ------------------------------------------------ |
| `starting` | Companion is being started                       |
| `waiting`  | Waiting for readiness (completion or string)     |
| `ready`    | Became ready (for `keep_alive = false`)          |
| `running`  | Running and monitored (for `keep_alive = true`)  |
| `exited`   | Exited (normally or with error)                  |
| `failed`   | Failed to start or auto-restart failed           |
| `stopped`  | Stopped intentionally                            |

## Hooks

Hooks are scripts that run automatically when you enter or leave a location or context.
Unlike companion scripts which are tied to specific tunnels, hooks respond to state transitions and are useful for:

- Running setup/teardown scripts when entering/leaving locations
- Triggering notifications on context changes
- Mounting/unmounting network drives
- Starting/stopping services based on location

### Location Hooks

Define hooks that run when entering or leaving specific locations:

```hcl
location "office" {
  display_name = "Office Network"

  conditions {
    public_ip = ["198.51.100.0/24"]
  }

  hooks {
    on_enter {
      command = "~/scripts/office-setup.sh"
      timeout = "30s"
    }

    on_leave {
      command = "~/scripts/office-teardown.sh"
    }
  }
}
```

### Context Hooks

Define hooks that run when entering or leaving specific contexts:

```hcl
context "trusted" {
  display_name = "Trusted Network"
  locations = ["home", "office"]

  hooks {
    on_enter {
      command = "notify-send 'Entered trusted network'"
    }

    on_leave {
      command = "notify-send 'Left trusted network'"
    }
  }
}
```

### Global Hooks

Run hooks for ALL location or context changes using top-level blocks:

```hcl
# Runs for every location change
location_hooks {
  on_enter {
    command = "~/scripts/log-location.sh"
  }

  on_leave {
    command = "~/scripts/cleanup.sh"
  }
}

# Runs for every context change
context_hooks {
  on_enter {
    command = "~/scripts/context-changed.sh"
  }
}
```

### Hook Configuration Options

| Option    | Type     | Default    | Description                                    |
| --------- | -------- | ---------- | ---------------------------------------------- |
| `command` | string   | *required* | Command to execute (supports `~` expansion)    |
| `timeout` | duration | `30s`      | Maximum execution time before killing          |

### Hook Environment Variables

Hooks receive these environment variables:

| Variable                   | Description                                              |
| -------------------------- | -------------------------------------------------------- |
| `OVERSEER_HOOK_TYPE`       | `enter` or `leave`                                       |
| `OVERSEER_HOOK_TARGET_TYPE`| `location` or `context`                                  |
| `OVERSEER_HOOK_TARGET`     | Name of the location or context                          |
| `OVERSEER_CONTEXT`         | Current context name                                     |
| `OVERSEER_LOCATION`        | Current location name                                    |
| `OVERSEER_PUBLIC_IP`       | Public IP address (if available)                         |
| `OVERSEER_LOCAL_IP`        | Local IP address (if available)                          |
| Custom variables           | Any variables from context/location `environment` blocks |

### Hook Execution Order

When state changes, hooks execute in this order:

1. **Leave hooks** (if location/context changed):
   - Specific location/context leave hooks first
   - Global location/context leave hooks second

2. **Enter hooks** (if location/context changed):
   - Global location/context enter hooks first
   - Specific location/context enter hooks second

This LIFO (last-in-first-out) pattern ensures proper setup/teardown ordering.

### Hook Events in Status

Hook execution is logged and appears in `overseer status -E 20` output with amber/gold coloring. Event types include:

- `hook_executed` - Successful execution (shows duration)
- `hook_failed` - Failed execution (shows error)
- `hook_timeout` - Execution timed out

### Example: Complete Hook Setup

```hcl
# Global hooks for logging
location_hooks {
  on_enter {
    command = "logger 'Overseer: entered location'"
  }
}

location "home" {
  display_name = "Home"
  conditions {
    public_ip = ["203.0.113.42"]
  }

  hooks {
    on_enter {
      command = "~/scripts/mount-nas.sh"
      timeout = "60s"
    }

    on_leave {
      command = "~/scripts/unmount-nas.sh"
    }
  }
}

location "office" {
  display_name = "Office"
  conditions {
    public_ip = ["198.51.100.0/24"]
  }

  hooks {
    on_enter {
      command = "~/scripts/connect-printer.sh"
    }
  }
}

context "trusted" {
  locations = ["home", "office"]

  hooks {
    on_enter {
      command = "notify-send 'Welcome to trusted network'"
    }
  }
}
```

### Tunnel Hooks

Tunnel hooks run at specific points during the tunnel connection lifecycle.
Unlike location/context hooks which respond to state transitions, tunnel hooks are tied to the SSH connection process itself.

**Hook Types:**

- `before_connect` - Runs after companions are ready, but before SSH connection attempt
- `after_connect` - Runs after SSH connection is verified and established

```hcl
tunnel "my-server" {
  tag = "production"

  hooks {
    before_connect {
      command = "~/scripts/pre-tunnel.sh"
      timeout = "30s"
    }

    after_connect {
      command = "~/scripts/post-tunnel.sh"
    }
  }

  companion "vpn" {
    command = "~/bin/vpn.sh"
  }
}
```

**Tunnel Hook Environment Variables:**

| Variable                   | Description                                        |
| -------------------------- | -------------------------------------------------- |
| `OVERSEER_HOOK_TYPE`       | `before_connect` or `after_connect`                |
| `OVERSEER_HOOK_TARGET_TYPE`| `tunnel`                                           |
| `OVERSEER_HOOK_TARGET`     | Tunnel alias                                       |
| `OVERSEER_TUNNEL_ALIAS`    | Tunnel alias (explicit)                            |
| `OVERSEER_TUNNEL_STATE`    | Current tunnel state (`connecting` or `connected`) |

**Execution Flow:**

```plain
startTunnelStreaming(alias)
│
├─ Start companion scripts
│   └─ Wait for ready state
│
├─ Execute before_connect hooks (fire-and-forget)
│
├─ Build and start SSH process
│
├─ Verify connection
│
├─ State = connected
│
├─ Execute after_connect hooks (fire-and-forget)
│
└─ Start monitoring
```

**Design Notes:**

- Hooks are **fire-and-forget** - failures do NOT block tunnel connection
- Hook events appear in `overseer status -E 20` with amber coloring
- Only connect hooks are supported (no disconnect hooks at this time)

### Global Tunnel Hooks

Run hooks for ALL tunnel connections using a top-level `tunnel_hooks` block:

```hcl
# Runs for every tunnel connection
tunnel_hooks {
  before_connect {
    command = "~/scripts/log-tunnel-start.sh"
  }

  after_connect {
    command = "~/scripts/notify-tunnel-connected.sh"
  }
}

# Per-tunnel hooks (more specific)
tunnel "my-server" {
  hooks {
    before_connect {
      command = "~/scripts/server-specific-pre.sh"
    }
  }
}
```

**Global Tunnel Hook Execution Order:**

Following the location/context hook pattern, hooks execute in setup/cleanup order:

1. **before_connect (setup order):**
   - Global `tunnel_hooks` before_connect hooks first (outer wrapper)
   - Specific tunnel before_connect hooks second (inner)

2. **after_connect (LIFO/cleanup order):**
   - Specific tunnel after_connect hooks first (inner)
   - Global `tunnel_hooks` after_connect hooks second (outer wrapper)

This ensures global setup runs before specific setup, and specific cleanup runs before global cleanup.

## Sensors

Overseer uses sensors to detect your current environment:

| Sensor        | Type    | Description                                              |
| ------------- | ------- | -------------------------------------------------------- |
| `public_ipv4` | string  | Your public IPv4 address                                 |
| `public_ipv6` | string  | Your public IPv6 /64 prefix (privacy extensions ignored) |
| `local_ipv4`  | string  | Your local LAN IPv4 address                              |
| `online`      | boolean | Network connectivity status                              |
| `context`     | string  | Current security context                                 |
| `location`    | string  | Current location                                         |

### Condition Types

Use these in `conditions` blocks:

- `public_ip = ["<ip>", ...]` - Match IP address or CIDR range
- `online = true/false` - Check online status
- `env = { "VAR" = "value" }` - Match environment variable

## Connectivity Statistics

The `qa` command shows network quality and session history:

```sh
# Today's statistics
overseer qa

# Last 7 days
overseer qa -d 7

# Specific date range
overseer qa -s 2025-01-01 -d 7
```

### Quality Ratings

Networks are rated based on connection stability:

| Rating        | Description                                                      |
| ------------- | ---------------------------------------------------------------- |
| **Excellent** | Stable connection with long sessions, no consecutive disconnects |
| **Good**      | Mostly stable with occasional brief interruptions                |
| **Fair**      | Some stability issues detected                                   |
| **Poor**      | Frequent disconnects or many consecutive short sessions          |
| **New**       | Single session - insufficient data to assess stability           |

Quality assessment considers:

- Consecutive short sessions (strongest instability indicator)
- Total number of brief sessions (< 5 minutes)
- Reconnection rate per hour of online time

## Exports

Overseer can export context information to files for integration with other tools, scripts, or shell
prompts. Configure exports in the `exports` block:

```hcl
exports {
  dotenv = "~/.config/overseer/overseer.env"    # Shell-sourceable env file
  context = "~/.config/overseer/context.txt"    # Context name only
  location = "~/.config/overseer/location.txt"  # Location name only
  public_ip = "~/.config/overseer/ip.txt"       # Public IP only
  preferred_ip = "ipv4"                         # Which IP to use: ipv4 or ipv6
}
```

### Export Types

| Type        | Description                              | Example Content                  |
| ----------- | ---------------------------------------- | -------------------------------- |
| `dotenv`    | Shell-sourceable file with all variables | `export OVERSEER_CONTEXT="home"` |
| `context`   | Plain text context name                  | `home`                           |
| `location`  | Plain text location name                 | `hq`                             |
| `public_ip` | Plain text IP address                    | `203.0.113.42`                   |

### Dotenv Variables

The `dotenv` export includes these variables:

| Variable                         | Description                                                    |
| -------------------------------- | -------------------------------------------------------------- |
| `OVERSEER_CONTEXT`               | Current context name                                           |
| `OVERSEER_CONTEXT_DISPLAY_NAME`  | Context display name                                           |
| `OVERSEER_LOCATION`              | Current location name                                          |
| `OVERSEER_LOCATION_DISPLAY_NAME` | Location display name                                          |
| `OVERSEER_PUBLIC_IP`             | Preferred public IP (based on `preferred_ip`)                  |
| `OVERSEER_PUBLIC_IPV4`           | Public IPv4 address                                            |
| `OVERSEER_PUBLIC_IPV6`           | Public IPv6 /64 prefix                                         |
| `OVERSEER_LOCAL_IPV4`            | Local LAN IPv4 address                                         |
| Custom variables                 | Any variables defined in context/location `environment` blocks |

### Shell Integration

Since overseer context changes dynamically, source the dotenv file before each prompt using a precmd hook:

#### Zsh (`~/.zshrc`)

```zsh
# Source overseer environment before each prompt
function _overseer_precmd() {
  [[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env
}
precmd_functions+=(_overseer_precmd)
```

#### Bash (`~/.bashrc`)

```sh
# Source overseer environment before each prompt
PROMPT_COMMAND='[[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env'
```

#### Fish (`~/.config/fish/config.fish`)

```fish
# Source overseer environment before each prompt
function _overseer_precmd --on-event fish_prompt
  test -f ~/.config/overseer/overseer.env && source ~/.config/overseer/overseer.env
end
```

Use the variables in your prompt or scripts:

```sh
# Show context in prompt
PS1="[$OVERSEER_CONTEXT] \w $ "

# Conditional behavior based on context
if [[ "$OVERSEER_CONTEXT" == "work" ]]; then
  export http_proxy="http://proxy.corp:8080"
fi
```

## Examples

### Basic Home/Away Setup

```hcl
location "home" {
  conditions {
    public_ip = ["203.0.113.42"]
  }
}

context "home" {
  locations = ["home"]
  actions {
    connect = ["home-server"]
  }
}

context "away" {
  conditions {
    online = true
  }
  actions {
    connect = ["vpn"]
    disconnect = ["home-server"]
  }
}
```

### Multi-Location with VPN

```hcl
location "home" {
  conditions {
    public_ip = ["203.0.113.0/24"]
  }
}

location "office" {
  conditions {
    public_ip = ["198.51.100.0/24"]
  }
}

context "corporate" {
  locations = ["office"]
  actions {
    connect = ["corp-gateway", "dev-cluster"]
  }
}

context "remote-work" {
  locations = ["home"]
  actions {
    connect = ["corp-vpn", "dev-cluster"]
  }
}

context "public" {
  conditions {
    online = true
  }
  actions {
    connect = ["secure-vpn"]
  }
}
```

## Status Command

View current context and tunnel status:

```sh
# Text output (default)
overseer status

# JSON output for scripting
overseer status --format json

# Show more events
overseer status -N 50
```

The status output displays SSH hops and companions in a tree beneath each tunnel:

```plain
Active Tunnels:
  ✓ gateway (PID: 22187, Age: 6m17s)
  ├── → 203.0.113.10:22
  └── ✓ vpn [running]
  ✓ dev-server (PID: 74917, Age: 1h1m7s)
  └── → 198.51.100.50:22
```

## Shell Completion

Generate completion scripts for your shell:

```sh
# Bash
overseer completion bash > /etc/bash_completion.d/overseer

# Zsh
overseer completion zsh > "${fpath[1]}/_overseer"

# Fish
overseer completion fish > ~/.config/fish/completions/overseer.fish
```

## License

MIT License

Copyright (c) 2025 David Jack Wange Olrik

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
