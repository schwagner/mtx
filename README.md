# mtx

A keyboard-driven terminal client for [Matrix](https://matrix.org) with
end-to-end encryption, built from [slk](https://github.com/gammons/slk)'s TUI.

mtx keeps slk's layout and keybindings (sidebar, message pane, thread pane,
vim-style modes, fuzzy finder, themes) and replaces the Slack engine with a
Matrix one built on [mautrix-go](https://github.com/mautrix/go) and gomuks'
[hicli](https://pkg.go.dev/go.mau.fi/gomuks/pkg/hicli).

## What works

- Login through the browser on servers with next-generation auth (Matrix
  Authentication Service, e.g. matrix.org): mtx prints a link and a code,
  you approve it in any browser on any device. Password login elsewhere.
- Device verification with your recovery key, or first-time
  setup of cross-signing, secret storage and key backup for a new account
- Encrypted rooms and DMs (Megolm, via mautrix's pure-Go `goolm`); history
  from before this device existed is restored from key backup
- Room list with unread dots and mention counts, DMs grouped separately
- History with infinite scroll-back, day separators, the "new" divider
- Send, edit, delete; Markdown formatting; @mentions; replies show the
  quoted message
- Threads (`m.thread`), in the thread pane
- Reactions (add, remove, list)
- Read receipts, typing indicators, desktop notifications (push rules)
- New DM with Ctrl+N: pick someone you share a room with, or type a full
  `@user:server` ID; invites appear as a toast and in the Ctrl+T finder
- Rooms with no activity for 30 days drop out of the sidebar (Ctrl+T still
  finds them); set `hide_inactive_after_days = 0` under `[sidebar]` in
  `~/.config/mtx/config.toml` to keep everything listed
- Spaces in the left rail, like Cinny: Home (rooms in no space) first, then
  each top-level space, e.g. a bridge's "Slack (Work)" or "Signal". Switch
  with 1–9, a click, or the workspace finder; subspaces fold into their
  top-level space
- Threads view (threads you are in) and Activity view (your mentions), for
  the current space
- Local full-text search of decrypted messages (`/` in a room)
- File upload (paste or path) and download (`d`), encrypted in encrypted
  rooms
- Inline images (kitty, sixel or half-block; `image_protocol` under
  `[appearance]`) and user avatars beside message authors
- Clickable links; bare URLs in sent messages are linkified
- Delivery indicators on your own messages (✓ sent, ✓✓ delivered/read) in
  bridged rooms whose bridge reports them (e.g. mautrix-signal)
- Vi normal mode in the composer (see below)

Not done yet: legacy SSO login (Synapse's own OIDC/SAML/CAS without MAS)
and OAuth servers without the device-code grant, interactive emoji
(SAS) verification of other people's devices, room creation/settings beyond
DMs, workspace-wide search (Ctrl+F), presence.

## Install

With Nix:

```sh
nix run github:schwagner/mtx         # or: nix build, then ./result/bin/mtx
```

From source (needs Go 1.26+, a C compiler; libX11 headers on Linux):

```sh
make build && ./bin/mtx
```

The build tags `sqlite_fts5,goolm` are required; the Makefile and flake set
them.

## First run

```
$ mtx
Log in to Matrix.
User ID (e.g. @you:example.com): @alice:matrix.example.com
Homeserver URL [https://matrix.example.com]:
Password:
Logged in.

Verify this device with your recovery key (or security passphrase).
Recovery key:
```

Use the recovery key Element shows under Settings → Encryption. If the account
has no secret storage yet, mtx offers to set it up and prints a new recovery
key: save it.

`mtx logout` logs the device out and deletes its local data.

## Files

| Path | Contents |
|---|---|
| `~/.config/mtx/config.toml` | Settings (slk's format: themes, sidebar, notifications) |
| `~/.config/mtx/themes/` | Custom themes |
| `~/.local/share/mtx/mtx.db` | Account, encryption keys, message store |
| `~/.local/share/mtx/pickle.key` | Key that encrypts the Olm/Megolm secrets in the database |
| `~/.cache/mtx/mtx.log` | Client log (`MTX_DEBUG=1` for debug level) |

The database holds your encryption keys. Keep the data directory private
(it is created mode 0700) and include it in backups only if they are
encrypted.

## Keys

Press `?` in the app. The essentials: `i` to write, `Esc` for normal mode,
`j`/`k` to move, `Enter` to open a thread, `r` to react, `E` edit, `D` delete,
`Ctrl+T` switch room, `Ctrl+N` new DM, `Ctrl+A` activity, `Ctrl+C` quit.

### Vi editing in the composer

slk's `Esc` leaves the composer. With

```toml
[compose]
vim_mode = true
```

`Esc` puts the composer in vi normal mode instead (`COMPOSE` in the status
bar, steady cursor) and a second `Esc` leaves it; an empty composer still
leaves on the first. In normal mode:

| Keys | |
|---|---|
| `h j k l` `w b e W B E` `0 ^ $` `gg G` `{ }` `%` `f t F T ; ,` | motions, with counts |
| `d c y` `gu gU g~` + motion or text object | operators: `dd`, `d3w`, `c$`, `yy`, `gUiw` |
| `iw aw iW aW` `i" a" i' a'` `` i` a` `` `i( a( ib` `i[ a[` `i{ a{ iB` `i< a<` | text objects |
| `x X D C s S Y r ~ J p P` | the usual shorthands |
| `i a I A o O` | back to insert (`3ia<Esc>` repeats) |
| `u` `Ctrl+R` `.` | undo, redo, repeat |
| `Enter` | send (or save an edit), as in insert mode |
| `Ctrl+E` | edit in `$EDITOR` |

Not yet: visual mode, named registers and the system clipboard (`"+y`),
`:` commands inside the composer.

## Development

See [AGENTS.md](AGENTS.md). `nix develop` gives a shell with everything needed.

## License

MIT, like slk. Copyright for the TUI belongs to slk's authors; see LICENSE.
mautrix-go and hicli are MPL-2.0 and are used unmodified.
