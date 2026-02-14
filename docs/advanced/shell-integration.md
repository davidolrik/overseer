# Shell Integration

Overseer exports context information to files that your shell, scripts, and other tools can consume. This page covers how to configure exports and integrate them into your workflow.

## Configuring Exports

Enable exports in your `config.hcl`:

```hcl
exports {
  dotenv      = "~/.config/overseer/overseer.env"
  context     = "~/.config/overseer/context.txt"
  location    = "~/.config/overseer/location.txt"
  public_ip   = "~/.config/overseer/ip.txt"
  preferred_ip = "ipv4"
}
```

Files are written atomically (via temp file + rename) whenever overseer's state changes. The `dotenv` file is the most useful — it contains all variables in a shell-sourceable format.

## Available Variables

The dotenv file exports these built-in variables:

| Variable | Example | Description |
|----------|---------|-------------|
| `OVERSEER_CONTEXT` | `trusted` | Current context name |
| `OVERSEER_CONTEXT_DISPLAY_NAME` | `Trusted Network` | Human-readable context name |
| `OVERSEER_LOCATION` | `home` | Current location name |
| `OVERSEER_LOCATION_DISPLAY_NAME` | `Home Network` | Human-readable location name |
| `OVERSEER_PUBLIC_IP` | `203.0.113.42` | Preferred public IP |
| `OVERSEER_PUBLIC_IPV4` | `203.0.113.42` | Public IPv4 address |
| `OVERSEER_PUBLIC_IPV6` | `2a05:f6c3:dd4d::` | Public IPv6 /64 prefix |
| `OVERSEER_LOCAL_IP` | `192.168.1.42` | Local LAN IPv4 address |
| `OVERSEER_LOCAL_IPV4` | `192.168.1.42` | Local LAN IPv4 address |

Plus any custom variables defined in your location and context `environment` blocks.

When contexts change, all custom variables from the previous context are unset before the new ones are exported.

## Shell Precmd Hooks

Since context changes dynamically, source the dotenv file **before each prompt** using a precmd hook. This ensures your shell always has current values.

::: code-group

```zsh [Zsh (~/.zshrc)]
function _overseer_precmd() {
  [[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env
}
precmd_functions+=(_overseer_precmd)
```

```sh [Bash (~/.bashrc)]
PROMPT_COMMAND='[[ -f ~/.config/overseer/overseer.env ]] && source ~/.config/overseer/overseer.env'
```

```fish [Fish (~/.config/fish/config.fish)]
function _overseer_precmd --on-event fish_prompt
  test -f ~/.config/overseer/overseer.env && source ~/.config/overseer/overseer.env
end
```

:::

## Prompt Integration

Use overseer variables to show your current context in your shell prompt:

```sh
# Simple context in prompt
PS1="[$OVERSEER_CONTEXT] \w $ "

# Show location@context
PS1="[$OVERSEER_LOCATION@$OVERSEER_CONTEXT] \w $ "
```

For Zsh with colors:

```zsh
# Add to your prompt
PROMPT='%F{cyan}[${OVERSEER_CONTEXT}]%f %~ %# '
```

## Conditional Behavior in Scripts

Use overseer variables for context-dependent behavior in your shell config:

```sh
# Set proxy when on untrusted networks
if [[ "$OVERSEER_CONTEXT" == "untrusted" ]]; then
  export http_proxy="socks5://localhost:1080"
  export https_proxy="socks5://localhost:1080"
fi

# Corporate proxy at office
if [[ "$OVERSEER_LOCATION" == "office" ]]; then
  export http_proxy="http://proxy.corp.example.com:8080"
fi
```

## Using Export Files in SSH Config

The dotenv file can be sourced directly inside `Match exec` directives in `~/.ssh/config`. This lets you make SSH routing decisions based on overseer's context:

```ssh-config
# Skip the jump host when at HQ
Match Host *.internal exec "[[ $(source ~/.local/var/overseer.env && echo $OVERSEER_PUBLIC_IP) = '203.0.113.42' ]]"
    ProxyJump none
```

The single-value export files (`context`, `location`, `public_ip`) are also useful for simpler checks:

```sh
# Read context name
CONTEXT=$(cat ~/.config/overseer/context.txt 2>/dev/null)

# Read public IP
IP=$(cat ~/.config/overseer/ip.txt 2>/dev/null)
```

See [Dynamic Tunnels](/advanced/dynamic-tunnels) for more patterns using these with SSH config's `Match` directive.

## Integration with Other Tools

### Privoxy / HTTP Proxy

Toggle a local HTTP proxy based on context:

```sh
# In your shell rc, after sourcing overseer.env
if [[ "$OVERSEER_CONTEXT" == "untrusted" ]]; then
  export http_proxy="http://localhost:8118"
  export https_proxy="http://localhost:8118"
else
  unset http_proxy https_proxy
fi
```

### Git Configuration

Use different Git identities based on location:

```sh
if [[ "$OVERSEER_LOCATION" == "office" ]]; then
  export GIT_AUTHOR_EMAIL="david@corp.example.com"
  export GIT_COMMITTER_EMAIL="david@corp.example.com"
fi
```

### tmux Status Bar

Show the current context in your tmux status bar:

```sh
# ~/.tmux.conf
set -g status-right '#(cat ~/.config/overseer/context.txt 2>/dev/null || echo "?") '
```
