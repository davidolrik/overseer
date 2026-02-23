# Configuration

Overseer uses [HCL](https://github.com/hashicorp/hcl) format for configuration. The config file is located at `~/.config/overseer/config.hcl`.

If no config file exists, overseer creates one with default values on first run.

## Split Config Files (`config.d/`)

As your configuration grows, you can split it into multiple files by creating a `config.d/` directory alongside `config.hcl`:

```
~/.config/overseer/
├── config.hcl          # Main config (loaded first)
└── config.d/           # Optional directory
    ├── home.hcl        # Home network locations & contexts
    ├── office.hcl      # Office network locations & contexts
    └── vpn-tunnels.hcl # Tunnel definitions
```

Files in `config.d/` are loaded in **alphabetical order** after the main config. Only `.hcl` files are loaded — other files and subdirectories are ignored.

The `config.d/` directory is optional. If it doesn't exist, behavior is unchanged.

### What Goes Where

| Config element | Where it belongs |
|---|---|
| Global settings (`verbose`) | Main config |
| Singleton blocks (`exports`, `ssh`, `companion`, `environment`, global hooks) | Main config only — defining these in more than one file is an error |
| Locations | Any file — accumulated across files; duplicate names are an error |
| Tunnels | Any file — accumulated across files; duplicate names are an error |
| Contexts | Any file — same-name contexts are deep-merged (locations, actions, hooks append + deduplicate; environment merges keys; scalars use first-non-empty). Distinct names accumulate in load order. Order matters: first match wins |

### Example

**`config.hcl`** — global settings and exports:
```hcl
verbose = 0

ssh {
  reconnect_enabled = true
  max_retries = 10
}

exports {
  dotenv = "~/.local/var/overseer.env"
}
```

**`config.d/home.hcl`** — home network:
```hcl
location "home" {
  display_name = "Home"
  conditions {
    public_ip = ["203.0.113.42"]
  }
}

context "home" {
  display_name = "At Home"
  locations = ["home"]
  actions {
    connect = ["home-lab"]
  }
}
```

**`config.d/office.hcl`** — office network:
```hcl
location "office" {
  display_name = "Office"
  conditions {
    public_ip = ["198.51.100.0/24"]
  }
}

context "office" {
  display_name = "At Office"
  locations = ["office"]
  actions {
    connect = ["corp-gateway"]
  }
}
```

::: tip Daemon Reload
When the daemon is running, changes to files in `config.d/` trigger an automatic reload, just like changes to `config.hcl`. If you create the `config.d/` directory after the daemon is already running, use `overseer reload` or restart the daemon to pick it up.
:::

## Global Settings

```hcl
# Verbosity level (0=quiet, 1=normal, 2=verbose, 3=debug)
verbose = 0
```

## Global Environment

The top-level `environment` block defines default environment variables that are always exported, regardless of which location or context is active:

```hcl
environment = {
  "OVERSEER_CONTEXT_BG" = "#3a579a"
  "MY_DEFAULT_VAR"      = "default-value"
}
```

These defaults can be overridden by location and context `environment` blocks. The merge priority is (lowest → highest):

**Global → Location → Context**

For example:

```hcl
environment = {
  "THEME_COLOR" = "#3a579a"  # default
}

location "home" {
  conditions {
    public_ip = ["203.0.113.42"]
  }
  environment = {
    "THEME_COLOR" = "#00ff00"  # overrides global when at home
  }
}

context "trusted" {
  locations = ["home"]
  environment = {
    "THEME_COLOR" = "#ff0000"  # overrides both global and location
  }
}
```

::: tip
Global environment is useful for variables you want set everywhere — like prompt colors or default settings — without duplicating them across every location and context block.
:::

## SSH Settings

The `ssh` block controls SSH connection behavior and automatic reconnection:

```hcl
ssh {
  # Keepalive — detect dead connections
  server_alive_interval = 15    # Send keepalive every N seconds (0 to disable)
  server_alive_count_max = 3    # Exit after N failed keepalives

  # Automatic reconnection
  reconnect_enabled = true      # Enable/disable auto-reconnect
  initial_backoff   = "1s"      # First retry delay
  max_backoff       = "5m"      # Maximum delay between retries
  backoff_factor    = 2         # Multiplier for each retry
  max_retries       = 10        # Give up after this many attempts
}
```

All values shown are the defaults. You only need to include settings you want to change.

## Sensors

Overseer detects your network environment through sensors:

| Sensor | Type | Description |
|--------|------|-------------|
| `public_ipv4` | string | Public IPv4 address (detected via DNS consensus) |
| `public_ipv6` | string | Public IPv6 /64 prefix (privacy extensions ignored) |
| `local_ipv4` | string | Local LAN IPv4 address |
| `online` | boolean | Network connectivity (TCP probe to well-known hosts) |

Use these sensor names in `conditions` blocks to match your network.

### Condition Types

| Condition | Syntax | Description |
|-----------|--------|-------------|
| `public_ip` | `public_ip = ["<ip>", ...]` | Match public IP address or CIDR range |
| `online` | `online = true/false` | Check online status |
| `env` | `env = { "VAR" = "value" }` | Match environment variable |

::: info
`public_ip` conditions match against the `public_ipv4` sensor. Multiple values in a list are OR'd together.
:::

## Locations

Locations represent physical or network environments identified by sensor conditions.

### Simple Conditions

```hcl
location "home" {
  display_name = "Home Network"

  conditions {
    public_ip = ["203.0.113.42"]
  }

  environment = {
    LOCATION_TYPE = "residential"
  }
}
```

Multiple IPs in the list are OR'd — any match activates the location:

```hcl
location "office" {
  display_name = "Office"

  conditions {
    public_ip = ["198.51.100.0/24", "203.0.113.0/24"]
  }
}
```

### Structured Conditions

For complex matching logic, use `any{}` (OR) and `all{}` (AND) blocks:

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

Structured conditions can be nested arbitrarily:

```hcl
location "trusted_network" {
  conditions {
    all {
      online = true
      any {
        public_ip = ["192.168.1.0/24"]
        public_ip = ["10.0.1.0/24"]
        env = { "TRUSTED_NETWORK" = "yes" }
      }
    }
  }
}
```

### Environment Variables

Locations can define custom environment variables that are exported when the location is active:

```hcl
location "home" {
  display_name = "Home"
  conditions {
    public_ip = ["203.0.113.42"]
  }
  environment = {
    LOCATION_TYPE  = "residential"
    NETWORK_SPEED  = "1000"
  }
}
```

These variables appear in the [dotenv export](/advanced/shell-integration) alongside the built-in `OVERSEER_*` variables.

### Special Locations

Two locations have special behavior:

| Location | Behavior |
|----------|----------|
| `offline` | Matches when `online = false`. Auto-generated if not defined. |
| `unknown` | Fallback when no other location matches. Auto-generated if not defined. |

You can customize these to add display names or environment variables:

```hcl
location "offline" {
  display_name = "Offline"
  environment = {
    OVERSEER_LOCATION_COLOR = "#999999"
  }
}

location "unknown" {
  display_name = "Unknown Network"
  environment = {
    OVERSEER_LOCATION_COLOR = "#aa0000"
  }
}
```

## Contexts

Contexts group locations into a security posture and define tunnel actions. They are evaluated in order — the **first match wins**.

### Referencing Locations

The most common pattern references one or more locations:

```hcl
context "trusted" {
  display_name = "Trusted Network"
  locations = ["home", "office"]

  actions {
    connect    = ["dev-server", "home-lab"]
    disconnect = ["vpn"]
  }

  environment = {
    TRUST_LEVEL = "high"
  }
}
```

### Inline Conditions

Contexts can also define their own conditions directly:

```hcl
context "mobile" {
  display_name = "Mobile Hotspot"
  conditions {
    env = { "MOBILE_HOTSPOT" = "yes" }
  }
  actions {
    connect = ["vpn"]
  }
}
```

### Actions

The `actions` block specifies which SSH tunnels to connect and disconnect when entering this context:

```hcl
actions {
  connect    = ["tunnel-a", "tunnel-b"]  # SSH host aliases to connect
  disconnect = ["tunnel-c"]              # SSH host aliases to disconnect
}
```

Host aliases must correspond to `Host` entries in your `~/.ssh/config`.

### The `untrusted` Context

The `untrusted` context is special — it acts as the catch-all fallback when no other context matches. It is always evaluated last regardless of where you define it in your config:

```hcl
context "untrusted" {
  display_name = "Untrusted Network"
  actions {
    connect    = ["vpn"]
    disconnect = ["home-lab", "dev-server"]
  }
}
```

If you don't define an `untrusted` context, overseer creates a default one with no actions.

### Environment Variables in Contexts

Context environment variables are merged with global and location environment variables. The full merge priority is (lowest → highest): **Global → Location → Context**.

```hcl
context "work" {
  locations = ["office"]
  environment = {
    TRUST_LEVEL  = "high"      # Overrides location's and global TRUST_LEVEL if set
    CONTEXT_TYPE = "corporate"
  }
}
```

## Exports

The `exports` block configures files that overseer writes whenever state changes. These files enable [shell integration](/advanced/shell-integration) and scripting.

```hcl
exports {
  dotenv      = "~/.config/overseer/overseer.env"  # Shell-sourceable env file
  context     = "~/.config/overseer/context.txt"    # Context name only
  location    = "~/.config/overseer/location.txt"   # Location name only
  public_ip   = "~/.config/overseer/ip.txt"         # Public IP only
  preferred_ip = "ipv4"                              # ipv4 (default) or ipv6
}
```

All export paths support `~` for home directory expansion.

### Export Types

| Type | Content | Example |
|------|---------|---------|
| `dotenv` | Shell-sourceable file with all variables | `export OVERSEER_CONTEXT="home"` |
| `context` | Plain text context name | `home` |
| `location` | Plain text location name | `hq` |
| `public_ip` | Plain text IP address | `203.0.113.42` |

### Dotenv Variables

The `dotenv` file includes these built-in variables:

| Variable | Description |
|----------|-------------|
| `OVERSEER_CONTEXT` | Current context name |
| `OVERSEER_CONTEXT_DISPLAY_NAME` | Context display name |
| `OVERSEER_LOCATION` | Current location name |
| `OVERSEER_LOCATION_DISPLAY_NAME` | Location display name |
| `OVERSEER_PUBLIC_IP` | Preferred public IP (based on `preferred_ip`) |
| `OVERSEER_PUBLIC_IPV4` | Public IPv4 address |
| `OVERSEER_PUBLIC_IPV6` | Public IPv6 /64 prefix |
| `OVERSEER_LOCAL_IP` | Local LAN IPv4 address |
| `OVERSEER_LOCAL_IPV4` | Local LAN IPv4 address |

Plus any custom variables defined in the [global `environment`](#global-environment) block and the active location's and context's `environment` blocks.

When switching contexts, all custom variables from the previous context/location are automatically unset before the new ones are exported.

## Complete Example

A real-world configuration with multiple locations and contexts (this can also be [split across multiple files](#split-config-files-config-d)):

```hcl
verbose = 0

ssh {
  server_alive_interval = 15
  server_alive_count_max = 3
  reconnect_enabled = true
  initial_backoff   = "1s"
  max_backoff       = "5m"
  backoff_factor    = 2
  max_retries       = 10
}

exports {
  dotenv    = "~/.local/var/overseer.env"
  context   = "~/.local/var/overseer_context"
  location  = "~/.local/var/overseer_location"
  public_ip = "~/.local/var/overseer_ip"
}

# Global defaults — always exported, can be overridden per-location/context
environment = {
  "OVERSEER_CONTEXT_BG" = "#3a579a"
}

# --- Locations ---

location "home" {
  display_name = "Home"
  conditions {
    public_ip = ["203.0.113.42"]
  }
  environment = {
    "OVERSEER_CONTEXT_BG" = "#00aa00"  # override global default
  }
}

location "office" {
  display_name = "Office"
  conditions {
    public_ip = ["198.51.100.0/24"]
  }
}

# --- Contexts ---

context "corporate" {
  display_name = "At Office"
  locations = ["office"]
  actions {
    connect    = ["corp-gateway", "dev-cluster"]
  }
}

context "remote-work" {
  display_name = "Working from Home"
  locations = ["home"]
  actions {
    connect    = ["corp-vpn", "dev-cluster"]
  }
}

context "untrusted" {
  display_name = "Public Network"
  actions {
    connect    = ["secure-vpn"]
    disconnect = ["corp-gateway", "dev-cluster"]
  }
}
```
