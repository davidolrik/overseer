# Authentication

Overseer manages SSH connections in the background without terminal access, so authentication must work non-interactively. There are two approaches: SSH keys (recommended) or passwords stored in the system keyring.

## SSH Keys (Recommended)

SSH keys are the preferred authentication method for Overseer. Since Overseer connects in the background as a daemon, there is no terminal available to prompt for credentials — keys allow fully non-interactive authentication.

### Key Requirements

- Keys **must not** have a passphrase — Overseer connects in the background with no terminal to prompt for one
- Use Ed25519 keys for best security and performance

### Generating a Key

```sh
ssh-keygen -t ed25519 -N "" -f ~/.ssh/id_ed25519_overseer
```

The `-N ""` flag sets an empty passphrase.

Copy the public key to your server:

```sh
ssh-copy-id -i ~/.ssh/id_ed25519_overseer.pub user@dev.example.com
```

### SSH Config

Point your host entry at the key with `IdentityFile`:

```ssh-config
Host dev-server
    HostName dev.example.com
    User deploy
    IdentityFile ~/.ssh/id_ed25519_overseer
```

### Storing Keys in 1Password

For users who want key material secured at rest rather than as a plain file on disk, 1Password's SSH agent can serve keys and requires biometric touch to authorize each use.

1. Store the key in 1Password (via the desktop app → SSH Keys)
1. Enable the 1Password SSH agent in **Settings → Developer → SSH Agent**
1. Configure SSH to use the 1Password agent socket:

```ssh-config
Host *
    IdentityAgent "~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock"
```

1. Each SSH connection triggers a Touch ID prompt — the key never exists as a plain file on disk

::: tip
The 1Password SSH agent replaces `IdentityFile` — you don't need both. 1Password presents the right key automatically based on the server's accepted key types.
:::

::: warning
The first connection after a reboot or after 1Password locks will require touch. If Overseer tries to connect multiple tunnels simultaneously, you may get several Touch ID prompts in quick succession.
:::

## Password Authentication

For hosts that require password auth when key-based login is not available.

### Storing Passwords

```sh
overseer password set dev-server
```

Passwords are stored in the system keyring:

- **macOS** — Keychain
- **Linux** — Secret Service (GNOME Keyring / KDE Wallet)

### How It Works

Overseer sets the `SSH_ASKPASS` environment variable to point to its built-in askpass helper. When SSH prompts for a password, the helper retrieves it from the system keyring — no terminal interaction required.

### Managing Passwords

```sh
overseer password list               # List hosts with stored passwords
overseer password delete dev-server   # Remove a stored password
```

### Limitations

- Passwords can't handle interactive 2FA/MFA prompts
- If the password changes on the server, you need to run `overseer password set` again
- Some SSH configurations (keyboard-interactive) may not work with askpass

## Comparison

| Method | Security | Convenience | Best For |
|--------|----------|-------------|----------|
| [SSH key (plain file)](#ssh-keys-recommended) | Good | High | Simple setups, trusted machines |
| [SSH key (1Password)](#storing-keys-in-1password) | Excellent | High (touch required) | Security-conscious users |
| [Password (keyring)](#password-authentication) | Fair | Medium | Hosts without key auth |
