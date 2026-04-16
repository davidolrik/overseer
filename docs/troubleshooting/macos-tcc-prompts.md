# macOS: TCC prompts attributed to the wrong app

**TCC** (*Transparency, Consent, and Control*) is the macOS subsystem that
gates access to protected resources — files in other apps' containers, the
camera and microphone, location, and similar — behind user consent. It's
what produces the *"X would like to access Y"* dialogs and tracks the
grants you see under **System Settings → Privacy & Security**.

When connecting a tunnel, you may see a macOS permission prompt like
*"Zed would like to access data from other apps"* or the same prompt naming
Raycast, VS Code, Cursor, or another GUI app you have installed — even though
that app isn't involved in the tunnel.

This is a macOS attribution quirk, not an overseer bug. When a binary on disk
has a `com.apple.provenance` extended attribute, every process launched from
that binary is attributed to the "provenance owner" app, regardless of who
actually invoked it. Binaries pick up this attribute based on which GUI app
happened to be the responsible process when they were downloaded or written —
which for Homebrew casks is often whichever app was in the foreground at
install time.

Because overseer frequently shells out to helper binaries from hooks and
companions (`op` for credentials, `openconnect` for VPN, custom scripts, etc.),
any of those helpers carrying a stray provenance tag will surface as a prompt
naming the wrong app when the tunnel connects.

## Identify the culprit

Inspect the binaries your tunnel config invokes:

```sh
xattr -l "$(which op)"
xattr -l /opt/homebrew/Caskroom/1password-cli/*/op
xattr -l "$(which openconnect)"
```

If you see `com.apple.provenance` listed, that binary is the source of the
misattribution.

## Strip the attribute

```sh
xattr -d com.apple.provenance /path/to/binary
```

The next TCC prompt will then be attributed to the binary's real code
signature (e.g., `1Password CLI`), at which point clicking *Allow* once
creates a persistent grant that survives future invocations.

## Diagnose from scratch

If you're not sure which binary is triggering the prompt, watch TCC decisions
live while reproducing the connect:

```sh
log stream --info --style compact \
  --predicate 'subsystem == "com.apple.TCC"'
```

The `AttributionChain` lines name the accessing binary and the responsible
app. That tells you which binary to strip.

## Keep it durable

Homebrew rewrites these attributes on every `brew upgrade` of a cask. If a
particular helper keeps coming back tagged, wrap your upgrade command or add
a small script that re-strips `com.apple.provenance` from the affected
binaries after each upgrade.
