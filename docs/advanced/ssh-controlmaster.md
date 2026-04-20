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
    ControlPersist 300
```

| Directive        | Value                     | Description                                                    |
| ---------------- | ------------------------- | -------------------------------------------------------------- |
| `ControlMaster`  | `auto`                    | Create a master connection if none exists, otherwise reuse     |
| `ControlPath`    | `~/.ssh/sockets/%r@%h-%p` | Unix socket path for the master connection                     |
| `ControlPersist` | `300`                     | Keep the master alive for 5 minutes after last session closes  |

`ControlPersist` speeds up repeated short-lived commands (`scp`, `rsync`, successive `ssh`) to any host: the first command pays the auth cost and becomes the master; follow-up commands within the persist window reuse it instantly. The value is a tradeoff between how long connections linger after you stop using them and how often you pay full auth. 5 minutes works well in practice; raise or lower as suits your workflow.

For hosts that overseer manages, `ControlPersist` has no additional effect — overseer's own tunnel keeps the master alive for as long as the tunnel is connected, and tears it down on disconnect (see [below](#controlpersist-and-overseers-own-ssh-process)).

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
    ControlPersist 300

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

### ControlPersist and overseer's own ssh process

Overseer owns the mux master for the tunnel's lifetime and tears it down when the tunnel disconnects. To keep that lifecycle clean, overseer passes `-o ControlPersist=no` on its own `ssh` invocation (command-line `-o` overrides your config file for that one process only).

Without this override, OpenSSH would detach-fork the mux master into the background after authentication. The parent process — the one overseer tracks — exits immediately, so overseer would see the tunnel as "disconnected" within milliseconds of connecting and enter a reconnect loop.

Your config still applies to every *other* `ssh`/`scp`/`rsync` process: those continue to multiplex over overseer's live master socket as usual. The only difference is that the master goes away when overseer disconnects, instead of lingering for `ControlPersist` seconds afterward — which matches overseer's ownership model.

### Conflicts with other SSH masters

If a non-overseer `ssh` command (yours or a script's) has already set up a mux master for a host — for example you ran `ssh dev-server` directly, then closed the terminal while `ControlPersist` kept the master alive in the background — overseer would find that foreign master and be unable to establish its own tunnel.

Overseer handles this up-front before each connect attempt:

- **Interactive `overseer connect <alias>` / `overseer reconnect <alias>` (stdin is a terminal):** overseer refuses to touch the foreign master and shows a process tree identifying who owns it. Close that session (or run `ssh -O exit <alias>`) and retry. This protects any live session you're still using.
- **With `--force` / `-F`:** overseer runs `ssh -O exit <alias>` first, tearing down the foreign master, then proceeds. Use this when you know the lingering master isn't in use.
- **Non-interactive (scripts, cron, auto-connect from context changes):** overseer automatically uses `--force` — there's no user to resolve the conflict, and auto-connect is expected to succeed on its own.

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
