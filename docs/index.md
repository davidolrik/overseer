---
# https://vitepress.dev/reference/default-theme-home-page
layout: home

hero:
  name: "Overseer"
  text: "SSH Tunnel Manager"
  image: /overseer.png
  tagline: Contextual awareness for your SSH tunnels — detect your network, activate the right connections
  actions:
    - theme: brand
      text: What is Overseer?
      link: /guide/what-is-overseer
    - theme: alt
      text: Quick Start
      link: /guide/quick-start
    - theme: alt
      text: GitHub
      link: https://github.com/davidolrik/overseer

features:
  - title: Full OpenSSH Integration
    details: Supports everything OpenSSH can do — connection reuse, SOCKS proxies, port forwarding, jump hosts
    link: /guide/what-is-overseer
  - title: Security Context Awareness
    details: Automatically detect your network location and connect/disconnect SSH tunnels based on your context
    link: /guide/what-is-overseer#how-it-works
  - title: Automatic Reconnection
    details: Tunnels automatically reconnect with exponential backoff when connections fail
    link: /guide/configuration#ssh-settings
  - title: Connectivity Statistics
    details: Track network stability with session history and quality ratings per IP address
    link: /guide/commands#qa
  - title: Secure Password Storage
    details: Store passwords in your system keyring (macOS Keychain / Linux Secret Service)
    link: /guide/authentication#password-authentication
  - title: Shell Completion
    details: Dynamic completion for commands and SSH host aliases (bash, zsh, fish)
    link: /guide/quick-start#_6-shell-completion
---
