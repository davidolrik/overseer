# Port already in use

When overseer connects a tunnel, SSH attempts to bind every local port
configured via `LocalForward` or `DynamicForward` in your SSH config. If
another process already holds one of those ports, SSH exits (overseer sets
`-o ExitOnForwardFailure=yes` so it remains the authoritative owner of the
tunnel) and overseer reports the conflict:

```log
ERR SSH process terminated unexpectedly
ERR Tunnel 'prod-db' failed to connect: port 5432 is already in use
ERR Port 5432 on 127.0.0.1 is held by the following process tree:
ERR └─────     1 root  launchd
ERR  └────  1087 user  /Applications/TablePlus.app/Contents/MacOS/TablePlus
ERR   └─── 89196 user  ssh -W [db.internal.example.com]:22 jump-host
ERR    └── 89229 user  ssh -L 5432:localhost:5432 prod-db
```

The process tree shows exactly which application is holding the port and the
full chain of parent processes, so you can tell whether the blocker is a GUI
app (TablePlus, VS Code, Cursor, etc.) that spawned its own SSH connection
or a manual `ssh` session you left running.

## Solving the conflict

**Quit the blocking application.** The process tree tells you which app to
close. In the example above, TablePlus started an SSH connection to the same
host, binding port 5432 before overseer could. Quitting TablePlus (or
disconnecting its SSH session) frees the port, after which `overseer connect`
succeeds.

**Kill the specific SSH process.** If you don't want to quit the entire
application, kill the leaf `ssh` process directly:

```sh
kill 89229   # use the PID from the process tree
```

The PID is shown in the process tree. The parent application may reconnect
automatically — if it does, you'll need to reconfigure it to avoid the
conflicting port or quit it entirely.

**Reconfigure the other application.** Many database GUIs and remote-development
tools let you configure which local port they bind for SSH tunnels. Change their
port to avoid colliding with the one overseer manages. This is the most durable
fix because it prevents the conflict from recurring.

**Check what's holding a port manually.** If overseer cannot identify the
owning process (common on macOS when the holder runs as a different user),
you'll see a warning instead of the tree:

```log
WRN Port 5432 is in use, but overseer could not identify the owning process
    (likely owned by another user; try: sudo lsof -i :5432).
```

Run the suggested `lsof` command to find the holder.

## Why overseer insists on owning the port

Overseer sets `ExitOnForwardFailure=yes` deliberately. Without it, SSH would
silently skip any forward it cannot bind and connect anyway — leaving you with
a tunnel that *looks* connected but has no working port forwards. By failing
fast, overseer ensures you know immediately when a forward is broken rather
than discovering it minutes later when a database query or SOCKS proxy times
out.
