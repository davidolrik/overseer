<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/assets/img/overseer.png">

# Overseer - SSH tunnel manager

Connect and manage multiple SSH tunnels, uses your existing OpenSSH config.

Configure connection reuse, socks proxies, port forwarding and jump hosts in `~/.ssh/config` and use `overseer` to manage your tunnels.

## Features

* Full support for everything OpenSSH can be configured to do
* Start tunnel via host alias
* Stop tunnel via host alias
* Status of running tunnels, in both plaintext and JSON
* Secure password storage for password-based SSH authentication

## Demo

## Installation

### Download binary from GitHub

### Install using mise

## SSH Config

```ssh-config
Host *
    # Reuse ssh connections for all hosts
    ControlMaster auto
    ControlPath ~/.ssh/control/%h_%p_%r

# Jump host
Host jump.example.com
    # SOCKS proxy via jump host
    DynamicForward 25000

# Hosts that use the jump host
Host *.internal.example.com
    ProxyJump jump.example.com
```

## Password Management

For SSH servers that require password authentication, Overseer can securely store passwords in your system keyring (Keychain on macOS, Secret Service on Linux, Credential Manager on Windows).

```bash
# Store a password for an SSH host
overseer password set jump.example.com

# List hosts with stored passwords
overseer password list

# Delete a stored password
overseer password delete jump.example.com
```

**Note**: SSH key-based authentication is more secure and recommended. Only use password storage for servers that require it. Passwords are provided to SSH using the SSH_ASKPASS mechanism, which works with all modern SSH clients without requiring additional tools.
