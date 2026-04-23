# macOS permission prompts on every run (App Data / App Management)

If a companion script reads data belonging to another app — for example calling
`op` to fetch a secret from 1Password, or reading a configuration file inside
another app's sandboxed container — macOS shows this dialog:

> **"overseer" would like to access data from other apps.**

Clicking **Allow** should make the decision persistent, but for overseer — and
for command-line tools in general — the dialog often reappears on every single
invocation even after you've approved it.

## Why the dialog doesn't stick

macOS records consent decisions in TCC (Transparency, Consent, and Control).
For properly bundled `.app` applications, the decision is keyed on the bundle
identifier and survives upgrades cleanly. For unbundled Mach-O executables like
overseer, TCC records the decision by absolute path, and — for the specific
`kTCCServiceSystemPolicyAppData` service that governs "access data from other
apps" — the record written by the dialog's **Allow** button is frequently not
honored on subsequent access attempts. The row is in the database, but macOS
keeps re-evaluating and re-prompting anyway.

This is a long-standing TCC behavior for command-line tools, not something
overseer can fix on its side.

## The fix

The combination below is reliable and survives overseer upgrades.

### 1. Create a stable path to the binary

The goal is a fixed path that always points at the current overseer install,
so that a permission you grant once doesn't evaporate on the next release.

If you installed overseer via `mise`, create a symlink through the
`latest` directory mise maintains:

```sh
ln -s ~/.local/share/mise/installs/github-davidolrik-overseer/latest/overseer \
      ~/.local/bin/overseer
```

Make sure `~/.local/bin` is early in your `$PATH`. If you installed via
Homebrew, `/opt/homebrew/bin/overseer` is already a stable symlink — skip this
step and use that path in the next one.

### 2. Grant the binary Full Disk Access

Full Disk Access is a superset of the App Data permission and, unlike the
dialog's **Allow** button, grants added manually through System Settings are
honored reliably.

1. Open **System Settings → Privacy & Security → Full Disk Access**.
2. Click **`+`**.
3. Press **⇧⌘G** and paste the stable path from the previous step
   (`~/.local/bin/overseer` or `/opt/homebrew/bin/overseer`).
4. Toggle the entry on.

If you prefer a scoped permission instead of Full Disk Access, **App
Management** in the same pane works the same way and covers just the
"access data from other apps" category.
