# telegram-bridge-advanced

**English** | [Русский](README.ru.md)

An unofficial fork of [mautrix-telegram](https://github.com/mautrix/telegram), the Matrix ↔ Telegram bridge
(bridgev2, Go). This is the main repository of the project: the bridge's code, its Docker images, the installation
guide and the releases.

## Why it exists

It was made for people who move their Telegram life into Matrix and keep using Telegram the way they're used to:
with folders, channel comments, polls and bots. Upstream bridges messages well, but much of how Telegram is
organised gets lost on the way. The fork also covers setups where Telegram isn't reachable from the Matrix server
(the bridge runs on another server and connects to Matrix by itself), and a home server shared by several people.

## What it adds

- Telegram folders as Matrix spaces, the archive, pinned chats, Telegram's notification defaults.
- Channel comments as threads under posts, both ways (text and media), including discussion groups the user isn't
  a member of; plain replies to a post become comments.
- Several users of one bridge: a room of a channel per user, shared supergroups and forums, settings of shared
  rooms changed by bridge admins only.
- Polls both ways, bot buttons, rich posts, Telegram ads in a separate room, per-room settings (`!tg settings`:
  media limit, pause, signatures, buttons), cleanup of rooms of left chats, Russian bot replies.
- Encryption of private chats; direct media for channels and, encrypted, for private chats; large files
  streamed without temporary files; history depth per chat type, history sent in chunks.

## Installation

- Guide (in Russian for now): [docs/INSTALL.ru.md](docs/INSTALL.ru.md) — manual setup, the
  matrix-docker-ansible-deploy role, the bridge on a separate server, the fork's settings.
- With matrix-docker-ansible-deploy: [telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible).
- Image: `ghcr.io/kryptonmourog/telegram-bridge-advanced:<version>`, e.g. `v0.2611.0-adv.1` (the version
  scheme, updating and going back to upstream are in the guide).

## The project

| Repository | What it is |
|---|---|
| **telegram-bridge-advanced** (you are here) | the bridge itself: code, images, installation guide, releases |
| [mautrix-go-fork](https://github.com/kryptonmourog/mautrix-go-fork) | the fork of mautrix-go the bridge is built with |
| [wsproxy-fork](https://github.com/kryptonmourog/wsproxy-fork) | the websocket proxy for a bridge on a separate server, with encryption |
| [telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible) | installation with matrix-docker-ansible-deploy, including on a separate server |

## License

AGPL-3.0, like mautrix-telegram. Upstream documentation: [docs.mau.fi](https://docs.mau.fi/bridges/go/telegram/index.html).
