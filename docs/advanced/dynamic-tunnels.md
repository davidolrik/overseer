# Dynamic Tunnels

OpenSSH's `Match exec` directive runs a command and applies configuration only when it succeeds. Combined with overseer's export files, you can make your SSH config context-aware — bypassing jump hosts when at the office, using different keys per location, or enabling proxies only on untrusted networks.

## Match exec Basics

The `Match` directive in `~/.ssh/config` applies settings conditionally. The `exec` keyword runs a shell command — if it exits 0, the block applies:

```ssh-config
Match host *.example.com exec "test -f ~/.local/var/use-proxy"
    ProxyJump proxy.example.com
```

This only uses the proxy if the file `~/.local/var/use-proxy` exists.

## Using Overseer Exports

Overseer writes context information to files configured in the `exports` block. These files can be read by `Match exec` commands:

```hcl
# config.hcl
exports {
  dotenv   = "~/.local/var/overseer.env"
  context  = "~/.local/var/overseer_context"
  location = "~/.local/var/overseer_location"
}
```

### Source the Dotenv File

The most flexible approach sources the dotenv file and checks a variable in a single expression:

```ssh-config
# Skip jump host when at HQ (check by public IP)
Match Host *.internal exec "[[ $(source ~/.local/var/overseer.env && echo $OVERSEER_PUBLIC_IP) = '203.0.113.42' ]]"
    ProxyJump none

# Skip jump host when at office (check by location name)
Match Host *.internal exec "[[ $(source ~/.local/var/overseer.env && echo $OVERSEER_LOCATION) = 'office' ]]"
    ProxyJump none
```

### Read Single-Value Files

For simple checks, read the single-value export files directly:

```ssh-config
# Apply when at home
Match Host *.example.com exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) = home"
    ProxyJump none

# Apply when untrusted
Match Host * exec "test $(cat ~/.local/var/overseer_context 2>/dev/null) = untrusted"
    ProxyJump vpn-gateway
```

## Common Patterns

### Bypass Jump Hosts at the Office

When you're on the office LAN, internal hosts are directly reachable. Skip the jump host:

```ssh-config
# Direct connection when at office
Match host 10.0.* exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) = office"
    ProxyJump none

# Default: use jump host
Host 10.0.*
    ProxyJump gate.example.com
    User deploy
```

### Bypass Jump Hosts at Home

If your home network has VPN or direct connectivity:

```ssh-config
Match host dc-* exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) = home"
    ProxyJump none

Host dc-*
    ProxyJump bastion.example.com
```

### SOCKS Proxy on Untrusted Networks

Route SSH through a SOCKS proxy when on public networks:

```ssh-config
Match host * exec "test $(cat ~/.local/var/overseer_context 2>/dev/null) = untrusted"
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
Match host *.internal exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) != office -a -f ~/.local/var/jump-via-bastion"
    ProxyJump bastion.example.com
```

## Using Match tagged

OpenSSH 9.8+ supports `Match tagged` which provides cleaner multi-condition config. First, tag connections based on context:

```ssh-config
# Tag based on overseer context
Match host *.example.com exec "test $(cat ~/.local/var/overseer_context 2>/dev/null) = trusted"
    Tag trusted

Match host *.example.com exec "test $(cat ~/.local/var/overseer_context 2>/dev/null) = untrusted"
    Tag untrusted
```

Then apply settings based on tags:

```ssh-config
# Direct connection for trusted networks
Match tagged trusted host *.internal.example.com
    ProxyJump none

# SOCKS proxy for untrusted networks
Match tagged untrusted
    ProxyCommand nc -X 5 -x localhost:1080 %h %p
```

This separates the "when" (context detection) from the "what" (SSH configuration), making your config easier to reason about.

## Match Block Ordering

SSH processes Match blocks in order. Important rules:

1. More specific matches should come **before** general ones
2. `Match` blocks apply **in addition to** matching `Host` blocks
3. A `Match` block that sets a value overrides the `Host` block value
4. Place your `Match exec` blocks at the **top** of the file, before `Host` blocks

```ssh-config
# 1. Context-based overrides (most specific)
Match host dc-* exec "test $(cat ~/.local/var/overseer_location 2>/dev/null) = office"
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
