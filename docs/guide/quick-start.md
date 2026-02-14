# Quick Start

This guide walks you through setting up overseer with a simple home/away configuration. By the end, your SSH tunnels will automatically connect and disconnect based on your network location.

## Prerequisites

- Overseer [installed](/guide/installation)
- At least one SSH host configured in `~/.ssh/config`

## 1. Create Your Configuration

Overseer uses [HCL](https://github.com/hashicorp/hcl) for configuration. The config file lives at `~/.config/overseer/config.hcl` (created automatically on first run with defaults).

Create a minimal configuration with one location and two contexts:

```hcl
# ~/.config/overseer/config.hcl

# SSH connection settings
ssh {
  server_alive_interval = 15
  server_alive_count_max = 3
  reconnect_enabled = true
}

# Define your home network by its public IP
location "home" {
  display_name = "Home Network"
  conditions {
    public_ip = ["203.0.113.42"]  # Replace with your actual public IP
  }
}

# When at home, connect your dev server
context "home" {
  display_name = "At Home"
  locations = ["home"]
  actions {
    connect = ["dev-server"]  # Must match a Host in ~/.ssh/config
  }
}

# When anywhere else, connect your VPN tunnel
context "away" {
  display_name = "Away"
  conditions {
    online = true
  }
  actions {
    connect    = ["vpn"]
    disconnect = ["dev-server"]
  }
}
```

::: tip Finding Your Public IP
Run `curl -s ifconfig.me` to see your current public IP address.
:::

## 2. Set Up SSH Hosts

Make sure the host aliases you reference in your overseer config exist in `~/.ssh/config`:

```ssh-config
# ~/.ssh/config

Host dev-server
    HostName dev.example.com
    User deploy
    IdentityFile ~/.ssh/id_ed25519

Host vpn
    HostName vpn.example.com
    User tunnel
    DynamicForward 1080
    IdentityFile ~/.ssh/id_ed25519
```

## 3. Start the Daemon

```sh
overseer start
```

The daemon runs in the background, monitoring your network and managing tunnels. It persists until you explicitly stop it.

## 4. Check Status

```sh
overseer status
```

This shows your current context, location, sensor readings, and active tunnels:

```sh
Home Network trusted (LAN: 192.168.1.42, WAN: 203.0.113.42)

Context Age: 2h15m

Sensors:
  local_ipv4: 192.168.1.42
  online: true
  public_ipv4: 203.0.113.42

Active Tunnels:
  ✓ dev-server (PID: 12345, Age: 2h15m)
```

## 5. Manual Tunnel Control

You can manually connect and disconnect tunnels at any time:

```sh
# Connect to a host
overseer connect my-server

# Disconnect a specific tunnel
overseer disconnect my-server

# Disconnect all tunnels
overseer disconnect
```

## 6. Shell Completion

Set up tab completion for your shell:

::: code-group

```sh [Bash]
overseer completion bash > /etc/bash_completion.d/overseer
```

```sh [Zsh]
overseer completion zsh > "${fpath[1]}/_overseer"
```

```sh [Fish]
overseer completion fish > ~/.config/fish/completions/overseer.fish
```

:::

Completions include all commands, flags, and dynamically resolved SSH host aliases from your `~/.ssh/config`.

## Next Steps

- [Configuration](/guide/configuration) — Full reference for locations, contexts, sensors, conditions, and exports
- [Commands](/guide/commands) — Complete CLI reference
- [Shell Integration](/advanced/shell-integration) — Export context to your shell environment and prompts
