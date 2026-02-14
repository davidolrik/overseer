# Installation

## mise (Recommended) {#mise}

[mise](https://mise.jdx.dev/) is a polyglot runtime manager. It handles versioning and updates automatically.

```sh
mise use --global github:davidolrik/overseer@latest
```

To update later:

```sh
mise upgrade overseer
```

## go install {#go-install}

If you have Go installed:

```sh
go install overseer.olrik.dev@latest
```

This installs to your `$GOPATH/bin` directory (usually `~/go/bin`). Make sure it's in your `PATH`.

## Manual Download {#manual}

Download a precompiled binary from the [GitHub releases page](https://github.com/davidolrik/overseer/releases/latest), extract it, and move it to a directory in your `PATH`:

```sh
# Example for macOS (Apple Silicon)
curl -L https://github.com/davidolrik/overseer/releases/latest/download/overseer_darwin_arm64.tar.gz | tar xz
sudo mv overseer /usr/local/bin/
```

### Available Binaries

| Binary | Platform |
|--------|----------|
| `overseer_darwin_arm64.tar.gz` | macOS (Apple Silicon) |
| `overseer_darwin_amd64.tar.gz` | macOS (Intel) |
| `overseer_linux_arm64.tar.gz` | Linux (ARM64) |
| `overseer_linux_amd64.tar.gz` | Linux (x86_64) |

## Verify Installation

```sh
overseer version
```

## Next Steps

Continue to the [Quick Start](/guide/quick-start) to set up your first configuration.
