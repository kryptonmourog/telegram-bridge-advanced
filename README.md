# telegram-bridge-advanced

A fork of [mautrix-telegram](https://github.com/mautrix/telegram) (bridgev2) with features for people who
live in Matrix and use Telegram heavily. Russian and English bot replies.

- Telegram folders as Matrix spaces, the archive, pinned chats, Telegram's notification defaults.
- Channel comments as threads under posts: both ways (text and media), joining the discussion group,
  comments fetched in the background for groups the user isn't in; replies to posts become comments.
- Several users: a channel room per user, shared supergroups and forums, settings of shared rooms
  for bridge admins only.
- Polls both ways, bot buttons, rich posts, Telegram ads in a separate room, per-room settings
  (`!tg settings`: media limit, pause, signatures, buttons), cleanup of rooms of left chats.
- Encryption of private chats; direct media for channels and, encrypted, for private chats; large
  files streamed without temporary files; history depth per chat type, history sent in chunks.

Installation and settings: [docs/INSTALL.ru.md](docs/INSTALL.ru.md). With matrix-docker-ansible-deploy,
including the bridge on a separate server:
[telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible).
Images: `ghcr.io/kryptonmourog/telegram-bridge-advanced:<version>` (versions: `v<mautrix-telegram>-adv.<N>`).
It depends on a fork of mautrix-go: [mautrix-go-fork](https://github.com/kryptonmourog/mautrix-go-fork).

The rest of this README is upstream's.

---

# mautrix-telegram
![Languages](https://img.shields.io/github/languages/top/mautrix/telegram.svg)
[![License](https://img.shields.io/github/license/mautrix/telegram.svg)](LICENSE)
[![Release](https://img.shields.io/github/release/mautrix/telegram/all.svg)](https://github.com/mautrix/telegram/releases)
[![GitLab CI](https://mau.dev/mautrix/telegram/badges/main/pipeline.svg)](https://mau.dev/mautrix/telegram/container_registry)

A Matrix-Telegram puppeting/relaybot bridge.

## Sponsors
* [Joel Lehtonen / Zouppen](https://github.com/zouppen)

## Documentation
All setup and usage instructions are located on
[docs.mau.fi](https://docs.mau.fi/bridges/go/telegram/index.html).
Some quick links:

* [Bridge setup](https://docs.mau.fi/bridges/go/setup.html?bridge=telegram)
  (or [with Docker](https://docs.mau.fi/bridges/general/docker-setup.html?bridge=telegram))
* Basic usage: [Authentication](https://docs.mau.fi/bridges/go/telegram/authentication.html)

### Features & Roadmap
[ROADMAP.md](ROADMAP.md) contains a general overview of what is supported by the bridge.

## Discussion
Matrix room: [`#telegram:maunium.net`](https://matrix.to/#/#telegram:maunium.net)

Telegram chat: [`mautrix_telegram`](https://t.me/mautrix_telegram) (bridged to Matrix room)
