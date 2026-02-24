# Dynamic Tunnels

OpenSSH's `Match exec` directive runs a command and applies configuration only when it succeeds. Overseer sets computed state variables (`OVERSEER_CONTEXT`, `OVERSEER_LOCATION`, etc.) directly on SSH processes, so you can make your SSH config context-aware — bypassing jump hosts when at the office, using different keys per location, or enabling proxies only on untrusted networks.

## Match exec Basics

The `Match` directive in `~/.ssh/config` applies settings conditionally. The `exec` keyword runs a shell command — if it exits 0, the block applies:

```ssh-config
Match host *.example.com exec "test -f ~/.local/var/use-proxy"
    ProxyJump proxy.example.com
```

This only uses the proxy if the file `~/.local/var/use-proxy` exists.

## Computed State Variables

Overseer sets the following environment variables on every SSH process it manages:

| Variable | Description |
|----------|-------------|
| `OVERSEER_CONTEXT` | Current security context (e.g. `office`, `untrusted`) |
| `OVERSEER_CONTEXT_DISPLAY_NAME` | Human-readable context name |
| `OVERSEER_LOCATION` | Current location (e.g. `home`, `office`) |
| `OVERSEER_LOCATION_DISPLAY_NAME` | Human-readable location name |
| `OVERSEER_PUBLIC_IP` | Preferred public IP (respects `preferred_ip` config) |
| `OVERSEER_PUBLIC_IPV4` | Public IPv4 address |
| `OVERSEER_PUBLIC_IPV6` | Public IPv6 address |
| `OVERSEER_LOCAL_IP` | Local network IPv4 address |
| `OVERSEER_LOCAL_IPV4` | Local network IPv4 address |

Custom environment variables from your config (`environment` blocks at global, location, and context level) are also included.

These variables are read from the daemon's live state at connection time, so they always reflect the current context and location. On reconnection, state variables are refreshed automatically.

Use `Match exec` to match on them directly:

```ssh-config
# Skip jump host when at office
Match Host *.internal exec "[[ $OVERSEER_LOCATION = office ]]"
    ProxyJump none

# Skip jump host when at HQ (check by public IP)
Match Host *.internal exec "[[ $OVERSEER_PUBLIC_IP = 203.0.113.42 ]]"
    ProxyJump none

# Route through VPN on untrusted networks
Match Host * exec "[[ $OVERSEER_CONTEXT = untrusted ]]"
    ProxyCommand nc -X 5 -x localhost:1080 %h %p
```

## Using Overseer Exports (Manual SSH)

The computed state variables are only available on SSH processes launched by overseer. If you run `ssh` directly from your shell, these variables won't be set unless you have [shell integration](/advanced/shell-integration) configured to source the dotenv file. For manual SSH usage without shell integration, you can read the export files directly in `Match exec`:

```hcl
# config.hcl
exports {
  dotenv   = "~/.local/var/overseer.env"
  context  = "~/.local/var/overseer_context"
  location = "~/.local/var/overseer_location"
}
```

### Source the Dotenv File

```ssh-config
Match Host *.internal exec "[[ $(source ~/.local/var/overseer.env && echo $OVERSEER_LOCATION) = 'office' ]]"
    ProxyJump none
```

### Read Single-Value Files

```ssh-config
Match Host *.example.com exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) = home"
    ProxyJump none
```

## Common Patterns

### Bypass Jump Hosts at the Office

When you're on the office LAN, internal hosts are directly reachable. Skip the jump host:

```ssh-config
# Direct connection when at office
Match host 10.0.* exec "[[ $OVERSEER_LOCATION = office ]]"
    ProxyJump none

# Default: use jump host
Host 10.0.*
    ProxyJump gate.example.com
    User deploy
```

### Bypass Jump Hosts at Home

If your home network has VPN or direct connectivity:

```ssh-config
Match host dc-* exec "[[ $OVERSEER_LOCATION = home ]]"
    ProxyJump none

Host dc-*
    ProxyJump bastion.example.com
```

### SOCKS Proxy on Untrusted Networks

If you have a SOCKS5 proxy running locally (e.g., from a VPN client, `ssh -D 1080 trusted-server`, or a tool like Tailscale), you can route SSH connections through it when on public networks:

```ssh-config
Match host * exec "[[ $OVERSEER_CONTEXT = untrusted ]]"
    ProxyCommand nc -X 5 -x localhost:1080 %h %p
```

## File-Based Toggles

For situations where you want manual control alongside automatic context:

```sh
# Create a toggle file
touch ~/.local/var/jump-via-bastion

# Remove it to disable
rm ~/.local/var/jump-via-bastion
```

```ssh-config
Match host *.internal exec "test -f ~/.local/var/jump-via-bastion"
    ProxyJump bastion.example.com
```

You can combine file-based toggles with overseer context:

```ssh-config
# Use bastion only when NOT at office AND toggle file exists
Match host *.internal exec "[[ $OVERSEER_LOCATION != office ]] && test -f ~/.local/var/jump-via-bastion"
    ProxyJump bastion.example.com
```

## Using Environment Variables on SSH Processes

Overseer sets environment variables on SSH processes from multiple sources. These propagate through ProxyJump chains (child processes inherit environment variables), so each hop in the chain can see them.

Environment variables come from four sources, merged in priority order (highest wins):

1. **CLI `-E` flags** — per-invocation overrides
2. **Tunnel config** `environment` block — per-tunnel defaults
3. **Computed state variables** — `OVERSEER_*` vars and custom env from global/location/context config
4. **Fallback** — `core.Config.Environment` when no state orchestrator is running (e.g. remote mode)

Use `Match exec` in your SSH config to match on them:

```ssh-config
# Route through bastion when tagged as production
Match Host *.internal exec "[[ $OVERSEER_TAG = production ]]"
    ProxyJump bastion.example.com

# Use specific key for staging tunnels
Match Host * exec "[[ $OVERSEER_TAG = staging ]]"
    IdentityFile ~/.ssh/staging_ed25519

# Route through a specific bastion
Match Host *.internal exec "[[ $JUMP_VIA = bastion2 ]]"
    ProxyJump bastion2.example.com
```

Set environment variables from the command line:

```sh
overseer connect my-server -E OVERSEER_TAG=production
overseer connect my-server -E JUMP_VIA=bastion2 -E OVERSEER_TAG=staging
```

Or configure them in your overseer config:

```hcl
# Global defaults (applied to all tunnels)
environment = {
  OVERSEER_TAG = "default"
}

# Per-tunnel overrides
tunnel "my-server" {
  environment = {
    OVERSEER_TAG = "production"
  }
}
```

::: tip Why not SSH's -P flag?
SSH's `-P` flag sets a tag for `Match tagged`, but those tags don't propagate through ProxyJump chains — each hop starts fresh. Environment variables are inherited by all child processes, so they work across the entire jump chain.
:::

## Match Block Ordering

SSH processes Match blocks in order. Important rules:

1. More specific matches should come **before** general ones
2. `Match` blocks apply **in addition to** matching `Host` blocks
3. A `Match` block that sets a value overrides the `Host` block value
4. Place your `Match exec` blocks at the **top** of the file, before `Host` blocks

```ssh-config
# 1. Context-based overrides (most specific)
Match host dc-* exec "[[ $OVERSEER_LOCATION = office ]]"
    ProxyJump none

# 2. Host definitions (defaults)
Host dc-*
    ProxyJump gate.example.com
    User deploy

Host dc-web
    HostName 10.0.1.10

Host dc-db
    HostName 10.0.1.20
```
