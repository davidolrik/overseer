# Companion Scripts

Overseer manages SSH tunnels, but real-world setups often involve more than just SSH. You might need to start a VPN before connecting, run a setup script when entering a context, or toggle services based on your location. This page covers patterns for using scripts alongside overseer.

## Context-Driven Scripts

Using overseer's [shell integration](/advanced/shell-integration), you can run scripts that react to context changes.

### Shell Precmd Approach

The simplest approach runs a script on every prompt, using the exported context:

```sh
# ~/.zshrc or ~/.bashrc
function _overseer_context_hook() {
  source ~/.config/overseer/overseer.env 2>/dev/null || return

  # Track context changes
  if [[ "$OVERSEER_CONTEXT" != "$_LAST_OVERSEER_CONTEXT" ]]; then
    _LAST_OVERSEER_CONTEXT="$OVERSEER_CONTEXT"
    _on_context_change "$OVERSEER_CONTEXT"
  fi
}

function _on_context_change() {
  local context="$1"
  case "$context" in
    untrusted)
      # Start local SOCKS proxy
      if ! pgrep -x privoxy > /dev/null; then
        privoxy ~/.config/privoxy/config 2>/dev/null &
      fi
      ;;
    trusted|corporate)
      # Stop proxy if running
      pkill -x privoxy 2>/dev/null
      ;;
  esac
}

precmd_functions+=(_overseer_context_hook)
```

### File-Watching Approach

For actions that should happen immediately (not waiting for the next prompt), watch the context file:

```sh
#!/bin/bash
# ~/.local/bin/overseer-watcher.sh
# Run with: overseer-watcher.sh &

CONTEXT_FILE="$HOME/.config/overseer/context.txt"
LAST_CONTEXT=""

while true; do
  if [[ -f "$CONTEXT_FILE" ]]; then
    CONTEXT=$(cat "$CONTEXT_FILE")
    if [[ "$CONTEXT" != "$LAST_CONTEXT" ]]; then
      LAST_CONTEXT="$CONTEXT"
      echo "Context changed to: $CONTEXT"

      case "$CONTEXT" in
        untrusted)
          # Start VPN
          ~/.local/bin/vpn-connect.sh
          ;;
        *)
          # Stop VPN if running
          ~/.local/bin/vpn-disconnect.sh
          ;;
      esac
    fi
  fi
  sleep 2
done
```

## VPN Companion Scripts

A common pattern is connecting a VPN when overseer enters certain contexts.

### OpenConnect VPN

```sh
#!/bin/bash
# ~/.local/bin/vpn-connect.sh

VPN_HOST="vpn.example.com"
VPN_USER="david"

# Check if already connected
if pgrep -x openconnect > /dev/null; then
  echo "VPN already connected"
  exit 0
fi

# Get password from system keyring (macOS)
VPN_PASS=$(security find-generic-password -a "$VPN_USER" -s "vpn" -w 2>/dev/null)

if [[ -z "$VPN_PASS" ]]; then
  echo "No VPN password found in keyring"
  echo "Store it with: security add-generic-password -a '$VPN_USER' -s 'vpn' -w"
  exit 1
fi

echo "$VPN_PASS" | sudo openconnect \
  --user="$VPN_USER" \
  --passwd-on-stdin \
  --background \
  "$VPN_HOST"
```

### WireGuard VPN

```sh
#!/bin/bash
# ~/.local/bin/wg-toggle.sh

INTERFACE="wg0"

case "$1" in
  up)
    if ! wg show "$INTERFACE" > /dev/null 2>&1; then
      sudo wg-quick up "$INTERFACE"
    fi
    ;;
  down)
    if wg show "$INTERFACE" > /dev/null 2>&1; then
      sudo wg-quick down "$INTERFACE"
    fi
    ;;
esac
```

## Startup Scripts

Combine overseer with your system startup:

```sh
# ~/.zshrc
# Start overseer daemon (silently if already running)
overseer start -q

# Source context environment
[[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env
```

## Helper Script: Active Gateway Check

This script checks if you need to use a jump host, useful in SSH config `Match exec`:

```sh
#!/bin/bash
# ~/.local/bin/need-gateway.sh
# Usage in ssh config: Match host *.internal exec "~/.local/bin/need-gateway.sh"

source ~/.config/overseer/overseer.env 2>/dev/null || exit 1

case "$OVERSEER_LOCATION" in
  office|datacenter)
    # On the local network, no gateway needed
    exit 1
    ;;
  *)
    # Everywhere else, use the gateway
    exit 0
    ;;
esac
```

```ssh-config
Match host *.internal.example.com exec "~/.local/bin/need-gateway.sh"
    ProxyJump gate.example.com
```

## Writing Your Own Scripts

When writing scripts that integrate with overseer:

1. **Read from export files** — Don't call `overseer status` in tight loops. Read the exported files instead — they're updated atomically on every state change.

1. **Handle missing files gracefully** — The export files may not exist if overseer hasn't started yet:

```sh
CONTEXT=$(cat ~/.config/overseer/context.txt 2>/dev/null || echo "unknown")
```

1. **Use the dotenv file for multiple variables** — If you need more than one variable, source the dotenv file once rather than reading multiple files:

```sh
source ~/.config/overseer/overseer.env 2>/dev/null
```

1. **Debounce reactions** — If watching for file changes, add a small delay to avoid reacting to intermediate states during rapid network transitions.
