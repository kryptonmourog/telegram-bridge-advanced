# Installing telegram-bridge-advanced

**English** | [Русский](INSTALL.ru.md)

`telegram-bridge-advanced` is a fork of [mautrix-telegram](https://github.com/mautrix/telegram) (Go, bridgev2,
based on `v0.2611.0`). It's installed and configured like regular mautrix-telegram, except that:

- the image is different (a separate repository, or built from source);
- the config has additional settings (see [The fork's settings](#the-forks-settings));
- many features need **double puppeting**: without it the bridge works, but some features stay silent.

What the fork can do and why it exists is in the [README](../README.md); this guide is only about installation and
maintenance.

There are four ways to install it:

| Variant | When it fits |
|---|---|
| [A. The matrix-docker-ansible-deploy playbook](#a-the-matrix-docker-ansible-deploy-playbook) | the Matrix server is deployed with this playbook, the bridge on the same server |
| [B. Manually, on the Matrix server](#b-manually-on-the-matrix-server) | your own Matrix server without the playbook, the bridge next to it |
| [C. Manually, the bridge on a separate server](#c-manually-the-bridge-on-a-separate-server) | Telegram isn't reachable from the Matrix server, or the bridge has to be kept apart |
| [D. The playbook, the bridge on a separate VPS](#d-the-playbook-the-bridge-on-a-separate-vps) | the Matrix server is deployed with the playbook, but Telegram isn't reachable from it |

## Requirements

1. **A Matrix server that supports appservices**: Synapse or another.
2. **A Telegram `api_id` and `api_hash`** of your own, from https://my.telegram.org/apps. Don't use someone
   else's: per Telegram's terms, ads and limits are tied to the app.
3. **Double puppeting.** It's needed for joining spaces and rooms on the user's behalf, the Favourites/Low
   priority tags, muting, read markers, votes in polls and the archive. The easiest way is the double puppeting
   appservice: `matrix_appservice_double_puppet_enabled: true` in the playbook, otherwise see the
   [mautrix documentation](https://docs.mau.fi/bridges/general/double-puppeting.html).
4. **Docker** (or Go 1.26+ and `libolm` to build without Docker).
5. **For large files** (videos from channels can be up to 4 GB):
   - Synapse: a larger `max_upload_size` than usual (ours is `4096M`), temporary files not in `tmpfs`
     (the `TMPDIR` environment variable of the Synapse container pointing to disk);
   - the reverse proxy in front of Synapse: a request read timeout of several hours (Traefik:
     `respondingTimeouts.readTimeout`, nginx: `client_body_timeout`, `proxy_read_timeout`, `client_max_body_size`);
   - the bridge: `network.matrix_request_timeout: 3h` (see below).

## Versions

A version of the fork is the mautrix-telegram version it's based on plus the fork's running release number:

```
v0.2610.0-adv.1
v0.2610.0-adv.2
v0.2611.0-adv.3   ← the fork moved to a new mautrix-telegram version, the number goes on
```

- **`v0.2610.0`** is the mautrix-telegram version the release is based on.
- **`adv.N`** is the fork's release number. It only grows and isn't reset when the base changes, so `adv.N`
  alone names a release ("fixed in adv.4") and a larger number is always newer. It's written with a dot
  (`adv.10`, not `adv10`), so that semver compares it as a number.
- The git tag and the image tag have the same version (Docker tags can't contain `+`, hence the `-`).
- Floating image tags:
  - `latest`: the latest release;
  - `v0.2610.0-adv`: the latest release on that mautrix-telegram base.
- The fork of mautrix-go the bridge depends on is numbered the same way, with its own counter
  (`v0.32.0-adv.1`, `v0.32.0-adv.2`, …). Which of its versions a bridge release uses is in `go.mod`.

## Image

Either way works:

- **A prebuilt image:** `ghcr.io/kryptonmourog/telegram-bridge-advanced:<version>`
  (the list is on the package's page on GitHub).
- **Building from source:**
  ```bash
  git clone https://github.com/kryptonmourog/telegram-bridge-advanced.git
  cd telegram-bridge-advanced
  git checkout <version or branch>
  docker build -t telegram-bridge-advanced:<version> .
  ```
  It needs access to GitHub: the mautrix-go module is replaced with the fork
  `github.com/kryptonmourog/mautrix-go-fork` (the `replace` line in `go.mod`).

## A. The matrix-docker-ansible-deploy playbook

The playbook already has a mautrix-telegram (bridgev2) role. The fork is installed with the same role: only the
image is replaced, and the fork's settings are added with `configuration_extension_yaml`.

In `inventory/host_vars/<domain>/vars.yml`:

```yaml
matrix_bridge_mautrix_telegram_enabled: true
matrix_bridge_mautrix_telegram_api_id: 1234567
matrix_bridge_mautrix_telegram_api_hash: "..."

# The fork's image, one of the two:
# 1) A prebuilt image:
matrix_bridge_mautrix_telegram_container_image: "ghcr.io/kryptonmourog/telegram-bridge-advanced:<version>"
# 2) Or built on the server from the repository (the playbook clones and builds it):
# matrix_bridge_mautrix_telegram_container_image_self_build: true
# matrix_bridge_mautrix_telegram_container_image_self_build_repo: "https://github.com/kryptonmourog/telegram-bridge-advanced.git"
# matrix_bridge_mautrix_telegram_container_image_self_build_branch: "<version or branch>"

# Double puppeting: many features of the fork need it.
matrix_appservice_double_puppet_enabled: true

# The fork manages the root space (Telegram folders).
matrix_bridge_mautrix_telegram_bridge_personal_filtering_spaces: false

matrix_bridge_mautrix_telegram_configuration_extension_yaml: |
  bridge:
    tag_only_on_create: false
    mute_only_on_create: false
  network:
    folder_spaces:
      enabled: true
      default_language: en
    # For large files and a slow link to the Matrix server:
    matrix_request_timeout: 3h
    # Used when the standard direct_media is enabled.
    direct_media_scope: channels
    backfill_split_media_mb: 10
    # Polls as Matrix polls (otherwise as text):
    extev_polls: true
    sync:
      update_limit: -1
      create_limit: -1
    max_member_count: 1000
    # Several users: a room of a channel per user (set before the first login).
    personal_channels: true
    # History depth per chat type (the rest: backfill.max_initial_messages).
    max_initial_messages_override: {user: 300, saved: 1000}
    # Files of encrypted chats on request (with direct_media enabled).
    encrypted_direct_media: large
    comment_poll: {on_read_seconds: 60, background_minutes: 15, background_hours: 24}
    # If Telegram is only reachable through a SOCKS5 proxy on the server:
    # proxy:
    #   type: socks5
    #   address: "172.17.0.1:10808"
  backfill:
    enabled: true
    max_initial_messages: 30
```

For large files (optional):

```yaml
matrix_synapse_max_upload_size_mb: 4096
traefik_config_entrypoint_web_secure_transport_respondingTimeouts_readTimeout: 10800s
matrix_synapse_container_extra_arguments:
  - "--env=TMPDIR=/matrix-media-store-parent/tmp"   # create /matrix/synapse/storage/tmp beforehand (owned by matrix)
```

Apply:

```bash
ansible-playbook -i inventory/hosts setup.yml --tags=setup-all,start
# or only the bridge and Synapse:
ansible-playbook -i inventory/hosts setup.yml --tags=setup-mautrix-telegram,setup-synapse,start
```

**Moving from regular mautrix-telegram:** the database is compatible: the fork creates its own tables (`fork_*`)
on start and doesn't touch the others. Back up the bridge's database before switching. The fork's version must
not be older than the one installed now (the fork is based on `v0.2611.0`).

## B. Manually, on the Matrix server

The bridge is a separate container next to Synapse; Synapse sends it events over HTTP.

1. A folder and the first run: the bridge creates a default `config.yaml`:
   ```bash
   mkdir -p /opt/telegram-bridge/data && cd /opt/telegram-bridge
   docker run --rm -v "$PWD/data:/data" <image>
   ```
2. In `data/config.yaml`:

   | Key | Value |
   |---|---|
   | `homeserver.address` | Synapse's address reachable from the bridge container (e.g. `http://synapse:8008`) |
   | `homeserver.domain` | the users' domain (`example.org`) |
   | `appservice.address` | the bridge's address reachable from Synapse (e.g. `http://telegram-bridge:29317`) |
   | `appservice.hostname` / `port` | `0.0.0.0` / `29317` |
   | `database` | Postgres, or `sqlite3-fk-wal` with `file:/data/mautrix-telegram.db?_txlock=immediate` |
   | `network.api_id` / `api_hash` | your Telegram keys |
   | `bridge.permissions` | who may use the bridge: `"example.org": user`, `"@admin:example.org": admin` |
   | `double_puppet.secrets` | `example.org: "as_token:<token of the double puppeting appservice>"` |
   | the fork's settings | see [below](#the-forks-settings) |

3. A second run of the same command creates `data/registration.yaml`. Add it to Synapse:
   ```yaml
   # homeserver.yaml
   app_service_config_files:
     - /path/to/registration.yaml
   ```
   and restart Synapse.
4. `docker-compose.yml`:
   ```yaml
   services:
     telegram-bridge:
       image: <image>
       container_name: telegram-bridge
       restart: unless-stopped
       volumes: [./data:/data]
       networks: [matrix]          # the network Synapse is in
       logging: {driver: json-file, options: {max-size: 50m, max-file: "3"}}
   networks:
     matrix: {external: true}
   ```
   `docker compose up -d`.

## C. Manually, the bridge on a separate server

This is the author's setup: Telegram is blocked at the Matrix server's hoster, so the bridge runs abroad. There
are two ways to connect Synapse with a remote bridge.

### C1. Websocket through mautrix-wsproxy (the bridge connects to the Matrix server itself)

The bridge's server needs no incoming connections: the bridge keeps a websocket to the Matrix server and gets the
events from Synapse through `wsproxy`.

```
Telegram ◄──► [bridge] ── wss://matrix.example.org/_matrix/client/unstable/fi.mau.as_sync ──► reverse proxy ─► [wsproxy] ◄── Synapse
                      └── https://matrix.example.org/_matrix/… (requests to Synapse, media) ────────────────────────────►
```

**The bridge's server:**
1. As in variant B, steps 1–3, but in `config.yaml`:
   - `homeserver.address: https://matrix.example.org` (the public address);
   - `homeserver.websocket: true`, `homeserver.ping_interval_seconds: 30`;
   - `appservice.address: http://localhost:29317` (unused).
2. `docker-compose.yml` as in variant B, without the external network.

**The Matrix server:**
1. `wsproxy` in Synapse's network, with the environment variables `APPSERVICE_ID` (= `id` from
   `registration.yaml`), `AS_TOKEN` and `HS_TOKEN` (from there too). For encryption, use
   [wsproxy-fork](https://github.com/kryptonmourog/wsproxy-fork) (`ghcr.io/kryptonmourog/wsproxy-fork`):
   upstream's `dock.mau.dev/mautrix/wsproxy` drops to-device events.
2. A copy of `registration.yaml` for Synapse with `url: http://<wsproxy container>:29331`. Add it to Synapse
   (`app_service_config_files`; in the playbook, `matrix_synapse_app_service_config_files` and
   `matrix_synapse_container_extra_arguments` with a `--mount` of the file).
3. The reverse proxy: the path `/_matrix/client/unstable/fi.mau.as_sync` on `matrix.example.org` to
   `wsproxy:29331` (with websocket support), the rest of `/_matrix` to Synapse as before. For Traefik, labels on the
   container:
   ```yaml
   traefik.http.routers.tg-wsproxy.rule: Host(`matrix.example.org`) && Path(`/_matrix/client/unstable/fi.mau.as_sync`)
   traefik.http.routers.tg-wsproxy.priority: "1000"
   traefik.http.routers.tg-wsproxy.tls: "true"
   traefik.http.services.tg-wsproxy.loadbalancer.server.port: "29331"
   ```
4. To check: `curl https://matrix.example.org/_matrix/client/unstable/fi.mau.as_sync` without a token answers
   `403 M_MISSING_TOKEN` (that's `wsproxy`). After the bridge starts, `wsproxy`'s log says `connected to websocket`.

The playbook's `matrix-bridge-mautrix-wsproxy` role doesn't fit here: it's made for other bridges.

#### Direct media with the websocket connection

The standard `direct_media` turns the bridge into a separate Matrix media server. For the selective mode:

```yaml
direct_media:
  enabled: true
  server_name: telegram-media.example.org
  media_id_prefix: tg_
  # Optional: neutral values for /_matrix/federation/v1/version.
  server_implementation_name: example media
  server_implementation_version: "1"
network:
  direct_media_scope: channels
```

`telegram-media.example.org` has to pass `/.well-known/matrix/server`, `/_matrix/key/*` and the client and
federation media endpoints to the bridge's HTTP router. In websocket mode the mautrix-go fork runs that router
alongside the websocket of events.

If the bridge's server must not accept incoming connections, use a separate streaming WSS tunnel: the
`telegram_media_gateway` role of
[telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible) sets one up
with Chisel and Traefik. Don't send large files through the `http_proxy` command of the main websocket appservice:
it buffers the response body in memory and encodes binary data as base64.

### C2. Direct HTTPS to the bridge's server

If the bridge's server may accept incoming connections: a reverse proxy with a certificate on the bridge's server
(`https://tg-bridge.example.org` → `http://localhost:29317`), `url: https://tg-bridge.example.org` in Synapse's
`registration.yaml`, `homeserver.websocket: false` in the bridge's config. `wsproxy` isn't needed. Close the
bridge's port to everyone but the proxy; access to it is protected by the `hs_token`.

## D. The playbook, the bridge on a separate VPS

Variant C, installed with Ansible: the separate repository
[telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible) goes inside
the `matrix-docker-ansible-deploy` directory (as the `telegram-advanced` folder) and doesn't change the playbook's
files, so `just update` keeps working. The settings live in the same `vars.yml`.

It installs the bridge on the VPS (Docker included), wsproxy and, optionally, the direct media gateway on the
Matrix server, takes double puppeting from `matrix-appservice-double-puppet` and restarts Synapse when the
registration changes. The step-by-step guide is in that repository's README.

## The fork's settings

The `network` section of the bridge's config:

| Key | Default | What it does |
|---|---|---|
| `folder_spaces.enabled` | `false` | Telegram folders as Matrix spaces. Needs `bridge.personal_filtering_spaces: false` |
| `folder_spaces.default_language` | `en` | language of built-in names and bot replies (`en`, `ru`); a user changes it with `lang` |
| `folder_spaces.prioritize_personal_folders` | `true` | create rooms of chats in the user's own folders first when syncing |
| `matrix_request_timeout` | empty (3 min) | total timeout of a request to the Matrix server; `3h` for large files |
| `direct_media_scope` | `channels` | direct media: `all`, `channels` (channels and supergroups) or `off` |
| `extev_polls` | `false` | an upstream key; in the fork it enables Matrix polls both ways: Telegram's results as a notice, your vote both ways |
| `backfill_split_media_mb` | `0` | backfill history in chunks split at files larger than N MB |
| `skip_media` | `[]` | don't bridge particular files: `portal`, `message`, `min_size_mb` |
| `max_initial_messages_override` | `{}` | history depth of new rooms per type: `user`, `bot`, `saved`, `normal_group`, `supergroup`, `topic`, `channel` (e.g. `{user: 300, saved: 1000}`); the rest: `backfill.max_initial_messages` |
| `personal_channels` | `false` | a room of a channel per user; supergroups and forums are shared, basic groups and private chats are per user anyway. Set before channels are bridged: existing rooms don't move |
| `encrypted_direct_media` | `off` | files of encrypted rooms through direct media on request: `large` from `matrix.upload_file_threshold`, `all` every document and video. Needs `bridge.direct_media.enabled` |
| `comment_poll.on_read_seconds` | `60` | how often comments are fetched when a channel room is read (for discussion groups the user isn't a member of) |
| `comment_poll.background_minutes`, `background_hours` | `15`, `24` | background polling of new comments to posts of the last N hours; `0` minutes turns it off |

Recommended regular mautrix-telegram settings:

| Key | Value | Why |
|---|---|---|
| `bridge.personal_filtering_spaces` | `false` | otherwise folder spaces are off |
| `bridge.tag_only_on_create`, `bridge.mute_only_on_create` | `false` | tags and mute are always synced |
| `network.sync.update_limit`, `create_limit` | `-1` | bridge all chats, not the latest N |
| `network.max_member_count` | e.g. `1000` | don't bridge huge groups |
| `bridge.permissions` | `'example.org': user` | who may use the bridge; settings of shared rooms are changed by `admin` only |
| `backfill.enabled`, `max_initial_messages` | `true`, `30` | history when a room is created |

### Encryption of private chats

After setting up the standard end-to-bridge encryption, enable in `config.yaml`:

```yaml
encryption:
    allow: true
    default: false
    personal: true
```

`personal: true` encrypts only new private portals. The bot's management room, groups and channels stay
unencrypted. In an existing private portal, `!tg encryption` shows the state and a warning, and
`!tg encryption on confirm` enables encryption irreversibly.

With transactions over the websocket it needs `encryption.appservice: true`, MSC2409 and MSC3202 in Synapse,
`org.matrix.msc3202: true` in the registration and a wsproxy that forwards `to_device` and fallback keys. MSC4190
also needs `encryption.msc4190: true`, `io.element.msc4190: true` in the registration and MSC4190 support in the
server.

Element warns that the sender of an event and the owner of the device don't match. The bridge encrypts the events
of ghosts and of double puppeting with one crypto device of the bot. The transport stays encrypted, but the client
can't cryptographically confirm the author's Matrix ID. A new device needs the keys from the backup; Element Web
doesn't search the contents of encrypted rooms.

## First start and login

1. Start the bridge; its log must have no errors connecting to the Matrix server.
2. In a Matrix client, open a private chat with the bot (`@telegrambot:example.org`) and wait until it joins.
3. `login qr` (or `login phone`). The bot asks for Telegram's cloud password separately. Delete the message with
   the password after logging in: the room with the bot isn't encrypted.
4. The bridge creates spaces and rooms; the first sync of a large account takes a while.

The fork's commands (in the chat with the bot; in rooms of chats with the prefix, `!tg` by default):

| Command | What it does |
|---|---|
| `help` | all commands |
| `lang ru|en` | language of bot replies and space names |
| `folders`, `folders resync` | folder spaces |
| `archive on|off` | whether archived chats are bridged |
| `ads room|on` | where Telegram ads are shown |
| `left-chats`, `left-chats remove <number>|all` | rooms of chats the user has left |
| `settings`, `settings <name> <value>` | settings of the room (in a room of a chat) |
| `encryption`, `encryption on confirm` | state of and irreversibly enabling encryption of a private portal |
| `sync-chats` | full sync of chats |
| `join-discussion` | in a channel room: join its discussion group when only members can comment |

Several users: double puppeting has to be allowed for every user of the bridge; in matrix-docker-ansible-deploy
that's `matrix_appservice_double_puppet_registration_namespace_user_regex` (the whole domain by default). Without
it, invites to rooms and spaces have to be accepted manually, and your own messages from Telegram come from a
ghost. If the login code doesn't arrive in the Telegram app, log in with `login qr`.

## Updating and going back

- **Updating:** back up `data/` (or the database), new image, restart. The fork's tables are upgraded
  automatically.
- **Going back to regular mautrix-telegram of the same version:** the database keeps working: upstream ignores
  the fork's `fork_*` tables and metadata fields. Folder spaces stop being updated, the rooms stay. The exception
  is rooms of channels made per user (`personal_channels`): upstream doesn't know them and makes new shared ones.
- Restart the bridge quickly: if it's unreachable for longer than a minute, Synapse backs off its delivery
  retries (up to ~8 minutes of delay after it's back).

## Known limitations

- In encrypted portals Element warns that the Matrix sender and the owner of the bot's device don't match.
- The Telegram API layer is 228: interactive bot widgets are shown as a placeholder.
- Don't connect other Telegram clients (Telethon etc.) to the bridge's session: a client with a different API
  layer on the same key makes Telegram reject the bridge's requests (`CONNECTION_LAYER_INVALID`) until it restarts.
