# SSH ControlMaster

SSH ControlMaster allows multiple SSH sessions to share a single TCP connection. This pairs well with overseer — when overseer connects a tunnel, subsequent SSH sessions to the same host reuse that connection instantly.

## What ControlMaster Does

Without ControlMaster, every `ssh` command opens a new TCP connection, performs a new TLS handshake, and authenticates again. With ControlMaster, the first connection creates a persistent Unix socket. Subsequent connections multiplex over it — connecting in milliseconds with no authentication overhead.

Benefits:

- Instant connections to hosts overseer already manages
- No repeated password/key prompts
- Lower resource usage (one TCP connection instead of many)
- `scp` and `rsync` reuse existing connections transparently

## Configuration

Add this to your `~/.ssh/config`:

```ssh-config
Host *
    ControlMaster auto
    ControlPath ~/.ssh/sockets/%r@%h-%p
    ControlPersist 600
```

| Directive        | Value                     | Description                                                    |
| ---------------- | ------------------------- | -------------------------------------------------------------- |
| `ControlMaster`  | `auto`                    | Create a master connection if none exists, otherwise reuse     |
| `ControlPath`    | `~/.ssh/sockets/%r@%h-%p` | Unix socket path for the master connection                     |
| `ControlPersist` | `600`                     | Keep the master alive for 10 minutes after last session closes |

Create the socket directory:

```sh
mkdir -p ~/.ssh/sockets
chmod 700 ~/.ssh/sockets
```

### ControlPath Patterns

The `ControlPath` value uses tokens:

| Token | Meaning |
|-------|---------|
| `%r` | Remote username |
| `%h` | Remote hostname |
| `%p` | Remote port |
| `%C` | Hash of `%l%h%p%r` (short, avoids long paths) |

If your hostnames are long, use `%C` to avoid hitting the Unix socket path length limit:

```ssh-config
ControlPath ~/.ssh/sockets/%C
```

## Best Practices

### Exclude Problematic Hosts

Some connections shouldn't use ControlMaster — interactive sessions that need their own TTY, or connections through proxies:

```ssh-config
# Default: enable for everything
Host *
    ControlMaster auto
    ControlPath ~/.ssh/sockets/%C
    ControlPersist 600

# Disable for hosts where it causes issues
Host bastion-*.example.com
    ControlMaster no
```

### Exclude localhost

Connections to localhost (e.g., port forwards to local services) can conflict:

```ssh-config
Host localhost 127.0.0.1 ::1
    ControlMaster no
```

## How Overseer Leverages ControlMaster

When overseer connects a tunnel (e.g., `overseer connect dev-server`), SSH establishes a master connection. Any subsequent `ssh dev-server` in your terminal reuses that connection — you get instant access with no additional authentication.

This is especially powerful with [ProxyJump](/advanced/proxy-jump): overseer keeps the jump host connection alive, and your interactive sessions through it connect instantly.

## Troubleshooting

### Stale Sockets

If a connection dies uncleanly, a stale socket file may remain and block new connections:

```sh
# Check if a socket is alive
ssh -O check -S ~/.ssh/sockets/%C dev-server

# Remove a stale socket
ssh -O exit -S ~/.ssh/sockets/%C dev-server

# Or just delete the socket file
rm ~/.ssh/sockets/<socket-file>
```

### Permission Errors

The socket directory must be owned by you with restricted permissions:

```sh
chmod 700 ~/.ssh/sockets
```

### "ControlPath too long"

Unix socket paths have a ~104 character limit. Use `%C` (a short hash) instead of `%r@%h-%p`:

```ssh-config
ControlPath ~/.ssh/sockets/%C
```
