<img width="25%" align="right" alt="Overseer logo" src="https://raw.githubusercontent.com/davidolrik/overseer/main/assets/img/overseer.png">

# Overseer - SSH tunnel manager

Connect and manage multiple SSH tunnels, uses your existing OpenSSH config.

Configure connection reuse, socks proxies, port forwarding and jump hosts in `~/.ssh/config` and use `overseer` to manage your tunnels.

## Features

* Full support for everything OpenSSH can be configured to do
* Start tunnel via host alias
* Stop tunnel via host alias
* Status of running tunnels, in both plaintext and JSON

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
