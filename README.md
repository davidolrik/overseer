<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/assets/img/overseer.png">

# Overseer - Contextual Computing

Detect Security Context based upon sensors & manage SSH tunnels,
using your existing OpenSSH config.

Configure connection reuse, socks proxies, port forwarding and jump hosts in
`~/.ssh/config` and use `overseer` to manage your SSH tunnels.

## Features

* **Full OpenSSH Integration**: Supports everything OpenSSH can do (connection reuse, SOCKS proxies, port forwarding, jump hosts)
* **Security Context Awareness**: Automatically detect your logical location and connect/disconnect SSH tunnels based on your context
* **Automatic Reconnection**: Tunnels automatically reconnect with exponential backoff when connections fail
* **Secure Password Storage**: Store passwords in your system keyring (Keychain/Secret Service)
* **Shell Completion**: Dynamic completion commands and SSH host aliases (bash, zsh, fish)
* **Multiple Output Formats**: Status available in plaintext (with colors) and JSON for easy automation

## Quick Start

```bash
# Start overseer daemon
overseer start

# Connect a tunnel using SSH config alias
overseer connect jump.example.com

# Check tunnel status
overseer status

# Disconnect a specific tunnel
overseer disconnect jump.example.com

# Disconnect all tunnels and shutdown daemon
overseer stop
```

## Installation

### Download binary from GitHub

Download the latest release directly from the GitHub [release page](https://github.com/davidolrik/overseer/releases).

### Install using mise

```sh
mise use --global ubi:davidolrik/overseer@latest
```

## SSH Config

All configuration related to your tunnels must be defined in your SSH config,
`overseer` will only manage running tunnels based upon the SSH config.

```ssh-config
Host *
    # Reuse ssh connections for all hosts
    ControlMaster auto
    ControlPath ~/.ssh/control/%h_%p_%r

# Jump host
Host jump.example.com
    # SOCKS proxy via jump host
    DynamicForward 25000

# Hosts that use the jump host
Host *.internal.example.com
    ProxyJump jump.example.com
```

## SSH servers with password

For SSH servers that require password authentication, Overseer can securely store passwords in your system keyring
(Keychain on macOS, Secret Service on Linux, Credential Manager on Windows).

**Note**: SSH key-based authentication is more secure and recommended. Only use password storage for servers that
require it. Passwords are provided to SSH using the SSH_ASKPASS mechanism, which works with all modern SSH clients
without requiring additional tools.

## Automatic Reconnection

Overseer automatically reconnects tunnels when they fail, using exponential backoff to avoid overwhelming the SSH server:

* **Smart Backoff**: 1s → 2s → 4s → 8s → 16s → ... up to 5 minutes
* **Visual Status**: Real-time connection state with countdown timers
* **Stability Tracking**: See total reconnection count for each tunnel
* **Configurable**: Adjust backoff timing, max retries, and enable/disable per tunnel

```bash
# View connection status with reconnection info
overseer status

# Example output:
# Active Tunnels:
#   ✓ production-db (PID: 12345, Age: 2h15m)
#   ⟳ staging-server (PID: 12346, Age: 5m23s, Reconnects: 3) (next attempt in 8s) [attempt 4]
#   ✗ backup-server (PID: 0, Age: 10m)
```

Configure connection monitoring and reconnection behavior in `~/.config/overseer/config.kdl`:

```kdl
// SSH connection health monitoring
ssh {
  server_alive_interval 15  // Send keepalive every N seconds (0 to disable)
  server_alive_count_max 3  // Exit after N failed keepalives
}

// Automatic reconnection settings
reconnect {
  enabled true              // Enable/disable auto-reconnect
  initial_backoff "1s"      // First retry delay
  max_backoff "5m"          // Maximum delay between retries
  backoff_factor 2          // Multiplier for each retry
  max_retries 10            // Give up after this many attempts
}
```

**Connection Health Monitoring:**

* Detects dead connections within 45 seconds (with default settings)
* SSH automatically exits when connection becomes unresponsive
* Triggers automatic reconnection with exponential backoff
* Can be customized or disabled (set `server_alive_interval` to 0)

## Security Context Awareness

Overseer automatically detects your network context and can connect/disconnect tunnels
based on where you are.
This enables true contextual computing - your tunnels adapt to your environment.

**How it works:**

* Monitors your public IP address (via DNS query to OpenDNS)
* Detects network changes in real-time (macOS/Linux)
* Evaluates context rules to determine your location
* Automatically manages tunnels based on context changes

**Example configuration** (`~/.config/overseer/config.kdl`):

```kdl
// Contexts are evaluated from top to bottom (first match wins)
// Place more specific contexts first
context "home" {
  display_name "Home"

  conditions {
    public_ip "92.0.2.42"       // Your home IP
    public_ip "192.168.1.0/24"  // Or local network range
  }

  actions {
    connect "home-lab"          // Connect to home services
    disconnect "office-vpn"     // Disconnect from office
  }
}

context "office" {
  display_name "Office"

  conditions {
    public_ip "198.51.100.0/24" // Office IP range
  }

  actions {
    connect "office-vpn"
    disconnect "home-lab"
  }
}

// Fallback context when no rules match
// (automatically added if not defined)
context "untrusted" {
  display_name "Untrusted"

  actions {
    disconnect "home-lab"
    disconnect "office-vpn"
  }
}
```

**View current context:**

```bash
# Detailed context information
overseer context

# Quick view in status output
overseer status
```

**Pattern matching in conditions:**

* Exact IP: `public_ip "123.45.67.89"`
* CIDR range: `public_ip "192.168.1.0/24"`
* Wildcards: `public_ip "192.168.*"`
* Multiple values: `public_ip "123.45.67.89"` and `public_ip "123.45.67.90"` (matches any)

## Context Exports & Custom Environment Variables

Overseer can export your current security context to files in various formats, enabling powerful
integrations with shell scripts, automation tools, status bars, and external systems like Home Assistant.

### Export Types

Configure exports in your `~/.config/overseer/config.kdl`:

```kdl
exports {
  dotenv "/home/user/.local/var/overseer.env"
  context "/home/user/.local/var/context.txt"
  location "/home/user/.local/var/location.txt"
  public_ip "/home/user/.local/var/public_ip.txt"
}
```

**Available export types:**

* **`dotenv`** - Shell-compatible environment variable file with all context data + custom variables
* **`context`** - Simple text file with just the context name (e.g., "home")
* **`location`** - Simple text file with just the location name (e.g., "hq")
* **`public_ip`** - Simple text file with just your public IP address

**Standard variables in dotenv exports:**

The `dotenv` export always includes these standard variables (when available):

* `OVERSEER_CONTEXT` - Current context name (e.g., "home", "office", "untrusted")
* `OVERSEER_CONTEXT_DISPLAY_NAME` - Human-friendly context name (e.g., "Home", "Office")
* `OVERSEER_LOCATION` - Current location name (e.g., "hq", "downtown")
* `OVERSEER_LOCATION_DISPLAY_NAME` - Human-friendly location name (e.g., "HQ", "Downtown Office")
* `OVERSEER_PUBLIC_IP` - Your current public IP address

### Custom Environment Variables

The most powerful feature is defining **arbitrary custom environment variables** that get included
in your dotenv exports. This enables endless customization and integration possibilities.

**Define variables in `environment {}` blocks:**

```kdl
context "home" {
  display_name "Home"
  location "hq"

  environment {
    TRUST_LEVEL "high"
    ALLOW_SSH "true"
    VPN_REQUIRED "false"
    OVERSEER_CONTEXT_COLOR "#00aa00"
  }

  conditions {
    public_ip "123.45.67.89"
  }

  actions {
    connect "homelab"
    disconnect "office-vpn"
  }
}

context "office" {
  display_name "Office"

  environment {
    TRUST_LEVEL "medium"
    OVERSEER_CONTEXT_COLOR "#3a579a"
    VPN_REQUIRED "true"
  }

  conditions {
    public_ip "98.76.54.0/24"
  }

  actions {
    connect "office-vpn"
  }
}

context "untrusted" {
  display_name "Untrusted"

  environment {
    TRUST_LEVEL "low"
    ALLOW_SSH "false"
    OVERSEER_CONTEXT_COLOR "#aa0000"
  }

  actions {
    connect "vpn-tunnel"
    disconnect "homelab"
  }
}

location "hq" {
  display_name "HQ"

  environment {
    BUILDING "headquarters"
    FLOOR "3"
    DESK "42"
  }

  conditions {
    public_ip "192.168.1.0/24"
  }
}
```

**Variable merging:** When a context has a location, environment variables are merged with
**context variables taking precedence** over location variables.

### Dotenv Export Format

The `dotenv` export includes both standard Overseer variables and your custom environment variables:

```bash
# Standard Overseer variables
OVERSEER_CONTEXT="home"
OVERSEER_CONTEXT_DISPLAY_NAME="Home"
OVERSEER_LOCATION="hq"
OVERSEER_LOCATION_DISPLAY_NAME="HQ"
OVERSEER_PUBLIC_IP="185.15.72.56"

# Custom variables from context and location
TRUST_LEVEL="high"
ALLOW_SSH="true"
VPN_REQUIRED="false"
OVERSEER_CONTEXT_COLOR="#00aa00"
BUILDING="headquarters"
FLOOR="3"
DESK="42"
```

### Export Update Triggers

Exports are automatically updated when:

* **Context changes** - You move to a different location/network
* **Config reloads** - Config file changes are detected (even if context stays the same)
* **Network changes** - Network state changes detected by the system
* **Daemon startup** - Initial context detection

All exports use **atomic writes** (temp file + rename) to ensure readers never see partial data.

### Integration Examples

**Shell script integration:**

```bash
#!/bin/bash
# Source the dotenv file to access variables
source ~/.local/var/overseer.env

if [ "$OVERSEER_CONTEXT" = "home" ]; then
  echo "Welcome home! Trust level: $TRUST_LEVEL"

  if [ "$ALLOW_SSH" = "true" ]; then
    echo "SSH access enabled"
  fi
fi
```

**Shell prompt with context:**

```bash
# In your .bashrc or .zshrc
PS1='[$(cat ~/.local/var/context.txt)] $ '
```

**Colored status bar indicator:**

```bash
#!/bin/bash
source ~/.local/var/overseer.env

# Convert hex color to RGB for terminal
echo -e "\033[48;2;${OVERSEER_CONTEXT_COLOR}m ${OVERSEER_CONTEXT_DISPLAY_NAME} \033[0m"
```

**Home Assistant integration:**

```yaml
# Monitor context file
sensor:
  - platform: file
    name: "Network Context"
    file_path: "/home/user/.local/var/context.txt"

# Monitor location
sensor:
  - platform: file
    name: "Network Location"
    file_path: "/home/user/.local/var/location.txt"

# Create automations based on context changes
automation:
  - alias: "Arrived Home"
    trigger:
      platform: state
      entity_id: sensor.network_context
      to: "home"
    action:
      service: script.welcome_home
```

**Conditional execution script:**

```bash
#!/bin/bash
CONTEXT=$(cat ~/.local/var/context.txt)

case $CONTEXT in
  home)
    echo "Home detected - syncing files to NAS"
    rsync -av ~/Documents/ nas:/backup/
    ;;
  office)
    echo "Office detected - connecting to internal services"
    /usr/local/bin/connect-office-services
    ;;
  untrusted)
    echo "Untrusted network - enabling VPN"
    /usr/local/bin/enable-vpn
    ;;
esac
```

**tmux/status bar integration:**

```bash
# In your .tmux.conf
set -g status-right '#[fg=white,bg=blue] #(cat ~/.local/var/context.txt) #[default]'
```

### Complete Configuration Example

```kdl
# Export configuration
exports {
  dotenv "~/.local/var/overseer.env"
  context "~/.local/var/context.txt"
  location "~/.local/var/location.txt"
}

# Location definitions (shared conditions)
location "hq" {
  display_name "HQ"

  environment {
    BUILDING "headquarters"
    FLOOR "3"
  }

  conditions {
    public_ip "192.168.1.0/24"
  }
}

location "home-network" {
  display_name "Home Network"

  environment {
    NETWORK_TYPE "residential"
  }

  conditions {
    public_ip "10.0.0.0/8"
  }
}

# Context definitions (evaluated in order)
context "home" {
  display_name "Home"
  location "home-network"

  environment {
    TRUST_LEVEL "high"
    ALLOW_SSH "true"
    VPN_REQUIRED "false"
    OVERSEER_CONTEXT_COLOR "#00aa00"
  }

  conditions {
    public_ip "123.45.67.89"
  }

  actions {
    connect "homelab"
    disconnect "office-vpn"
  }
}

context "office" {
  display_name "Office"
  location "hq"

  environment {
    TRUST_LEVEL "high"
    ALLOW_SSH "true"
    VPN_REQUIRED "false"
    OVERSEER_CONTEXT_COLOR "#3a579a"
  }

  conditions {
    public_ip "98.76.54.0/24"
  }

  actions {
    connect "office-vpn"
    disconnect "homelab"
  }
}

context "untrusted" {
  display_name "Untrusted Network"

  environment {
    TRUST_LEVEL "low"
    ALLOW_SSH "false"
    VPN_REQUIRED "true"
    OVERSEER_CONTEXT_COLOR "#aa0000"
  }

  actions {
    connect "secure-vpn"
    disconnect "homelab"
    disconnect "office-vpn"
  }
}
```

## Commands

### Tunnel Management

```sh
# Connect a tunnel (daemon starts automatically if not running)
overseer connect <ssh-alias>
overseer c <ssh-alias>  # alias for 'connect'

# Disconnect a specific tunnel
overseer disconnect <ssh-alias>
overseer d <ssh-alias>  # alias for 'disconnect'

# Disconnect all tunnels and shutdown daemon
overseer quit

# View tunnel status
overseer status

# JSON output (useful for scripting)
overseer status -F json
```

### Password Management

```sh
# Store password for an SSH host
overseer password set <ssh-alias>

# List hosts with stored passwords
overseer password list

# Delete stored password
overseer password delete <ssh-alias>
```

### Version and Help

```sh
# Check version (shows both client and daemon versions)
overseer version

# Get help
overseer help
overseer --help

# Command-specific help
overseer connect --help
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
