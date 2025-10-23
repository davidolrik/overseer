<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/assets/img/overseer.png">

# Overseer - Contextual Computing

Detect Security Context based upon sensors & manage SSH tunnels,
uses your existing OpenSSH config.

Configure connection reuse, socks proxies, port forwarding and jump hosts in `~/.ssh/config` and use `overseer` to manage your tunnels.

## Features

* **Full OpenSSH Integration**: Supports everything OpenSSH can do (connection reuse, SOCKS proxies, port forwarding, jump hosts)
* **Security Context Awareness**: Automatically detect your logical location and connect/disconnect tunnels based on your context
* **Automatic Reconnection**: Tunnels automatically reconnect with exponential backoff when connections fail
* **Secure Password Storage**: Store passwords in system keyring (Keychain/Secret Service/Credential Manager)
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

Configure reconnection behavior in `~/.config/overseer/config.kdl`:

```kdl
reconnect {
  enabled true              // Enable/disable auto-reconnect
  initial_backoff "1s"      // First retry delay
  max_backoff "5m"          // Maximum delay between retries
  backoff_factor 2          // Multiplier for each retry
  max_retries 10            // Give up after this many attempts
}
```

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
// Optional: Write current context to file for external integrations
context_output_file "/tmp/overseer-context.txt"

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
