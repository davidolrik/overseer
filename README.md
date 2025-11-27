<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/docs/static/overseer.png">

# Overseer - Contextual Computing

Detect security context based on sensors and manage SSH tunnels using your existing OpenSSH config.

Configure connection reuse, SOCKS proxies, port forwarding and jump hosts in `~/.ssh/config` and use
`overseer` to manage your SSH tunnels automatically based on your network location.

## Features

- **Full OpenSSH Integration**: Supports everything OpenSSH can do (connection reuse, SOCKS proxies, port forwarding, jump hosts)
- **Security Context Awareness**: Automatically detect your logical location and connect/disconnect SSH tunnels based on your context
- **Automatic Reconnection**: Tunnels automatically reconnect with exponential backoff when connections fail
- **Secure Password Storage**: Store passwords in your system keyring (Keychain/Secret Service)
- **Shell Completion**: Dynamic completion for commands and SSH host aliases (bash, zsh, fish)
- **Multiple Output Formats**: Status available in plaintext (with colors) and JSON for easy automation

## Installation

### Install using mise

```bash
mise use --global ubi:davidolrik/overseer@latest
```

### Install using go directly

```bash
go install overseer.olrik.dev@latest
```

## Quick Start

1. Start the overseer daemon:

   ```bash
   overseer start
   ```

2. Check current status:

   ```bash
   overseer status
   ```

3. Manually connect to an SSH host:

   ```bash
   overseer connect my-server
   ```

4. Configure automatic context-based connections by editing `~/.config/overseer/config.kdl`

## Commands

### Daemon Management

| Command | Description |
|---------|-------------|
| `overseer start` | Start the daemon in background |
| `overseer stop` | Stop the daemon and disconnect all tunnels |
| `overseer restart` | Cold restart (reconnects tunnels based on context) |
| `overseer reload` | Hot reload config (preserves active tunnels) |
| `overseer daemon` | Run daemon in foreground (for debugging) |

### Tunnel Management

| Command | Aliases | Description |
|---------|---------|-------------|
| `overseer connect <alias>` | `c` | Connect to an SSH host |
| `overseer disconnect [alias]` | `d` | Disconnect tunnel (or all if no alias) |
| `overseer reconnect <alias>` | `r` | Reconnect a tunnel |

### Status & Information

| Command | Aliases | Description |
|---------|---------|-------------|
| `overseer status` | `s`, `list`, `ls`, `context`, `ctx` | Show context, sensors, and tunnels |
| `overseer logs` | `log` | Stream daemon logs in real-time |
| `overseer version` | | Show version information |

### Password Management

| Command | Description |
|---------|-------------|
| `overseer password set <alias>` | Store password in system keyring |
| `overseer password delete <alias>` | Delete stored password |
| `overseer password list` | List hosts with stored passwords |

### Utility Commands

| Command | Description |
|---------|-------------|
| `overseer reset` | Reset retry counters for reconnecting tunnels |
| `overseer completion` | Generate shell completion scripts |

## Global Flags

```plain
--config-path <path>  Config directory (default: ~/.config/overseer)
-v, --verbose         Increase verbosity (repeat for more: -vvv)
-h, --help            Show help
```

## Configuration

Overseer uses [KDL](https://kdl.dev) format for configuration. The config file is located at `~/.config/overseer/config.kdl`.

### Global Settings

```kdl
verbose 0

ssh {
  server_alive_interval 15    // Keepalive interval in seconds
  server_alive_count_max 3    // Exit after N failed keepalives
  reconnect_enabled true      // Enable auto-reconnect
  initial_backoff "1s"        // First retry delay
  max_backoff "5m"            // Maximum retry delay
  backoff_factor 2            // Exponential backoff multiplier
  max_retries 10              // Give up after N attempts
}

exports {
  dotenv "/path/to/overseer.env"    // Export context as env file
  context "/path/to/context.txt"    // Export context name
  location "/path/to/location.txt"  // Export location name
  public_ip "/path/to/public_ip.txt" // Export public IP
  preferred_ip "ipv4"               // Preferred IP version (ipv4 or ipv6)
}
```

### Defining Locations

Locations represent physical or network locations detected by sensors:

```kdl
location "home" {
  display_name "Home Network"

  conditions {
    public_ip "203.0.113.42"        // Match specific IP
    public_ip "198.51.100.0/24"     // Or CIDR range
  }

  environment {
    LOCATION_TYPE "residential"     // Custom env vars to export
  }
}

location "office" {
  display_name "Office Network"

  conditions {
    env "HOSTNAME" "work-laptop"    // Match environment variable
  }
}
```

### Defining Contexts

Contexts group locations and define actions to take:

```kdl
context "trusted" {
  display_name "Trusted Network"

  // Reference one or more locations
  location "home"
  location "office"

  // Or use direct conditions
  conditions {
    public_ip "1.2.3.4"
  }

  environment {
    TRUST_LEVEL "high"
  }

  actions {
    connect "home-lab"      // Tunnels to connect in this context
    connect "dev-server"
    disconnect "vpn"        // Tunnels to disconnect
  }
}

context "untrusted" {
  display_name "Public Network"
  default true              // Fallback context when no other matches

  actions {
    connect "vpn"
    disconnect "home-lab"
  }
}
```

## Sensors

Overseer uses sensors to detect your current environment:

| Sensor | Type | Description |
|--------|------|-------------|
| `public_ipv4` | string | Your public IPv4 address |
| `public_ipv6` | string | Your public IPv6 address |
| `tcp` | boolean | Network connectivity status |
| `online` | boolean | General online status |
| `env:<VAR>` | string | Environment variable value |
| `context` | string | Current security context |
| `location` | string | Current location |

### Condition Types

Use these in `conditions` blocks:

- `public_ip "<ip>"` - Match IP address or CIDR range
- `online <true/false>` - Check online status
- `env "<VAR>" "<value>"` - Match environment variable

## Exports

Overseer can export context information to files for integration with other tools, scripts, or shell
prompts. Configure exports in the `exports` block:

```kdl
exports {
  dotenv "~/.config/overseer/overseer.env"  // Shell-sourceable env file
  context "~/.config/overseer/context.txt"  // Context name only
  location "~/.config/overseer/location.txt" // Location name only
  public_ip "~/.config/overseer/ip.txt"     // Public IP only
  preferred_ip "ipv4"                        // Which IP to use: ipv4 or ipv6
}
```

### Export Types

| Type | Description | Example Content |
|------|-------------|-----------------|
| `dotenv` | Shell-sourceable file with all variables | `export OVERSEER_CONTEXT="home"` |
| `context` | Plain text context name | `home` |
| `location` | Plain text location name | `hq` |
| `public_ip` | Plain text IP address | `203.0.113.42` |

### Dotenv Variables

The `dotenv` export includes these variables:

| Variable | Description |
|----------|-------------|
| `OVERSEER_CONTEXT` | Current context name |
| `OVERSEER_CONTEXT_DISPLAY_NAME` | Context display name |
| `OVERSEER_LOCATION` | Current location name |
| `OVERSEER_LOCATION_DISPLAY_NAME` | Location display name |
| `OVERSEER_PUBLIC_IP` | Preferred public IP (based on `preferred_ip`) |
| `OVERSEER_PUBLIC_IPV4` | Public IPv4 address |
| `OVERSEER_PUBLIC_IPV6` | Public IPv6 address |
| Custom variables | Any variables defined in context/location `environment` blocks |

### Shell Integration

Source the dotenv file in your shell to access overseer context:

**Bash/Zsh** (`~/.bashrc` or `~/.zshrc`):

```bash
# Source overseer environment if available
[[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env
```

**Fish** (`~/.config/fish/config.fish`):

```fish
# Source overseer environment if available
test -f ~/.config/overseer/overseer.env && source ~/.config/overseer/overseer.env
```

Use in your prompt or scripts:

```bash
# Show context in prompt
PS1="[$OVERSEER_CONTEXT] \w $ "

# Conditional behavior based on context
if [[ "$OVERSEER_CONTEXT" == "work" ]]; then
  export http_proxy="http://proxy.corp:8080"
fi
```

## Examples

### Basic Home/Away Setup

```kdl
location "home" {
  conditions {
    public_ip "203.0.113.42"
  }
}

context "home" {
  location "home"
  actions {
    connect "home-server"
  }
}

context "away" {
  default true
  actions {
    connect "vpn"
    disconnect "home-server"
  }
}
```

### Multi-Location with VPN

```kdl
location "home" {
  conditions {
    public_ip "203.0.113.0/24"
  }
}

location "office" {
  conditions {
    public_ip "198.51.100.0/24"
  }
}

context "corporate" {
  location "office"
  actions {
    connect "corp-gateway"
    connect "dev-cluster"
  }
}

context "remote-work" {
  location "home"
  actions {
    connect "corp-vpn"
    connect "dev-cluster"
  }
}

context "public" {
  default true
  actions {
    connect "secure-vpn"
  }
}
```

## Status Command

View current context and tunnel status:

```bash
# Text output (default)
overseer status

# JSON output for scripting
overseer status --format json

# Show more events
overseer status -n 50
```

## Shell Completion

Generate completion scripts for your shell:

```bash
# Bash
overseer completion bash > /etc/bash_completion.d/overseer

# Zsh
overseer completion zsh > "${fpath[1]}/_overseer"

# Fish
overseer completion fish > ~/.config/fish/completions/overseer.fish
```

## License

MIT License

Copyright (c) 2025

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
