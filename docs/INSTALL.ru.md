# Установка telegram-bridge-advanced

**Русский** | [English](INSTALL.md)

`telegram-bridge-advanced` — форк [mautrix-telegram](https://github.com/mautrix/telegram) (Go, bridgev2,
основа `v0.2611.0`). Ставится и настраивается так же, как обычный mautrix-telegram, но:

- образ другой (свой репозиторий или собранный из исходников);
- в конфиге есть дополнительные настройки (раздел [«Настройки форка»](#настройки-форка));
- многим функциям нужен **двойной вход** (double puppeting) — без него мост работает, но часть функций молчит.

Что умеет форк и зачем он создан — в [README](../README.ru.md); здесь только установка и обслуживание.

Ниже четыре варианта установки:

| Вариант | Когда подходит |
|---|---|
| [A. Плейбук matrix-docker-ansible-deploy](#a-плейбук-matrix-docker-ansible-deploy) | сервер Matrix развёрнут этим плейбуком, мост на том же сервере |
| [B. Вручную, на сервере Matrix](#b-вручную-на-сервере-matrix) | свой сервер Matrix без плейбука, мост рядом с ним |
| [C. Вручную, мост на отдельном сервере](#c-вручную-мост-на-отдельном-сервере) | Telegram недоступен с сервера Matrix, или мост нужно держать отдельно |
| [D. Плейбук matrix-docker-ansible-deploy, мост на отдельном VPS](#d-плейбук-matrix-docker-ansible-deploy-мост-на-отдельном-vps) | сервер Matrix развёрнут плейбуком, а Telegram с него недоступен |

## Требования

1. **Сервер Matrix с поддержкой приложений (appservice)** — Synapse и др.
2. **`api_id` и `api_hash` Telegram** — свои, с https://my.telegram.org/apps. Чужие не использовать:
   по условиям Telegram показ рекламы и лимиты привязаны к приложению.
3. **Двойной вход (double puppeting).** Нужен для: вступления в пространства и комнаты от имени пользователя,
   меток «Избранное»/«Низкий приоритет», заглушения, отметок о прочтении, голосов в опросах, архива.
   Удобнее всего — через отдельное приложение (appservice) двойного входа: в плейбуке это
   `matrix_appservice_double_puppet_enabled: true`, вручную — см. [документацию mautrix](https://docs.mau.fi/bridges/general/double-puppeting.html).
4. **Docker** (или Go 1.26+ и `libolm` для сборки без Docker).
5. **Для больших файлов** (видео из каналов бывают до 4 ГБ):
   - Synapse: `max_upload_size` больше обычного (у нас `4096M`), временные файлы не в `tmpfs`
     (переменная окружения `TMPDIR` контейнера Synapse на диск);
   - обратный прокси перед Synapse: таймаут чтения запроса в несколько часов (Traefik —
     `respondingTimeouts.readTimeout`, nginx — `client_body_timeout`, `proxy_read_timeout`, `client_max_body_size`);
   - мост: `network.matrix_request_timeout: 3h` (см. ниже).

## Версии

Версия форка — версия mautrix-telegram, на которой он основан, плюс сквозной номер выпуска форка:

```
v0.2610.0-adv.1
v0.2610.0-adv.2
v0.2611.0-adv.3   ← форк перенесён на новую версию mautrix-telegram, номер продолжается
```

- **`v0.2610.0`** — версия mautrix-telegram, на которой основан выпуск.
- **`adv.N`** — номер выпуска форка. Он только растёт и при смене основы не сбрасывается, поэтому
  `adv.N` сам по себе называет выпуск («исправлено в adv.4»), а больший номер всегда новее.
  Номер пишется через точку (`adv.10`, а не `adv10`), чтобы по semver он сравнивался как число.
- Одна и та же версия — у git-тега и у тега образа (в тегах Docker нельзя `+`, поэтому разделитель `-`).
- Плавающие теги образа:
  - `latest` — последний выпуск;
  - `v0.2610.0-adv` — последний выпуск на этой основе mautrix-telegram.
- Форк mautrix-go, от которого зависит мост, нумеруется так же, своим счётчиком
  (`v0.32.0-adv.1`, `v0.32.0-adv.2`, …). Какая его версия в каком выпуске моста — видно в `go.mod`.

## Образ

Подходит любой из способов:

- **Готовый образ:** `ghcr.io/kryptonmourog/telegram-bridge-advanced:<версия>`
  (список — на странице пакета в GitHub).
- **Сборка из исходников:**
  ```bash
  git clone https://github.com/kryptonmourog/telegram-bridge-advanced.git
  cd telegram-bridge-advanced
  git checkout <версия или ветка>
  docker build -t telegram-bridge-advanced:<версия> .
  ```
  Нужен доступ к GitHub: модуль mautrix-go подменяется форком `github.com/kryptonmourog/mautrix-go-fork`
  (строка `replace` в `go.mod`).

## A. Плейбук matrix-docker-ansible-deploy

В плейбуке уже есть роль mautrix-telegram (bridgev2). Форк ставится этой же ролью — подменяется только образ,
а настройки форка добавляются через `configuration_extension_yaml`.

В `inventory/host_vars/<домен>/vars.yml`:

```yaml
matrix_bridge_mautrix_telegram_enabled: true
matrix_bridge_mautrix_telegram_api_id: 1234567
matrix_bridge_mautrix_telegram_api_hash: "..."

# Образ форка — один из двух вариантов.
# 1) Готовый образ:
matrix_bridge_mautrix_telegram_container_image: "ghcr.io/kryptonmourog/telegram-bridge-advanced:<версия>"
# 2) Или сборка на сервере из репозитория (плейбук сам клонирует и соберёт):
# matrix_bridge_mautrix_telegram_container_image_self_build: true
# matrix_bridge_mautrix_telegram_container_image_self_build_repo: "https://github.com/kryptonmourog/telegram-bridge-advanced.git"
# matrix_bridge_mautrix_telegram_container_image_self_build_branch: "<версия или ветка>"

# Двойной вход — нужен многим функциям форка.
matrix_appservice_double_puppet_enabled: true

# Корневым пространством управляет форк (папки Telegram).
matrix_bridge_mautrix_telegram_bridge_personal_filtering_spaces: false

matrix_bridge_mautrix_telegram_configuration_extension_yaml: |
  bridge:
    tag_only_on_create: false
    mute_only_on_create: false
  network:
    folder_spaces:
      enabled: true
      default_language: ru
    # Для больших файлов и медленного канала до сервера Matrix:
    matrix_request_timeout: 3h
    # Используется, когда включён стандартный direct_media.
    direct_media_scope: channels
    backfill_split_media_mb: 10
    # Опросы как опросы Matrix (иначе — текстом):
    extev_polls: true
    sync:
      update_limit: -1
      create_limit: -1
    max_member_count: 1000
    # Несколько пользователей: у каждого своя комната канала (включать до первого входа).
    personal_channels: true
    # Глубина истории по типам чатов (остальное — backfill.max_initial_messages).
    max_initial_messages_override: {user: 300, saved: 1000}
    # Файлы зашифрованных чатов по запросу (при включённом direct_media).
    encrypted_direct_media: large
    comment_poll: {on_read_seconds: 60, background_minutes: 15, background_hours: 24}
    # Если Telegram доступен только через SOCKS5-прокси на сервере:
    # proxy:
    #   type: socks5
    #   address: "172.17.0.1:10808"
  backfill:
    enabled: true
    max_initial_messages: 30
```

Для больших файлов (по желанию):

```yaml
matrix_synapse_max_upload_size_mb: 4096
traefik_config_entrypoint_web_secure_transport_respondingTimeouts_readTimeout: 10800s
matrix_synapse_container_extra_arguments:
  - "--env=TMPDIR=/matrix-media-store-parent/tmp"   # папку /matrix/synapse/storage/tmp создать заранее (владелец matrix)
```

Применить:

```bash
ansible-playbook -i inventory/hosts setup.yml --tags=setup-all,start
# или только мост и Synapse:
ansible-playbook -i inventory/hosts setup.yml --tags=setup-mautrix-telegram,setup-synapse,start
```

**Переход с обычного mautrix-telegram:** база совместима — форк при запуске сам создаёт свои таблицы
(`fork_*`) и не трогает чужие. Перед переходом сделать резервную копию базы моста.
Версия форка должна быть не старее той, что стоит сейчас (форк основан на `v0.2611.0`).

## B. Вручную, на сервере Matrix

Мост — отдельный контейнер рядом с Synapse; Synapse отправляет ему события по HTTP.

1. Папка и первый запуск — мост создаст `config.yaml` по умолчанию:
   ```bash
   mkdir -p /opt/telegram-bridge/data && cd /opt/telegram-bridge
   docker run --rm -v "$PWD/data:/data" <образ>
   ```
2. В `data/config.yaml`:

   | Ключ | Значение |
   |---|---|
   | `homeserver.address` | адрес Synapse, доступный из контейнера моста (например `http://synapse:8008`) |
   | `homeserver.domain` | домен пользователей (`example.org`) |
   | `appservice.address` | адрес моста, доступный из Synapse (например `http://telegram-bridge:29317`) |
   | `appservice.hostname` / `port` | `0.0.0.0` / `29317` |
   | `database` | Postgres или `sqlite3-fk-wal` с `file:/data/mautrix-telegram.db?_txlock=immediate` |
   | `network.api_id` / `api_hash` | свои ключи Telegram |
   | `bridge.permissions` | кто может пользоваться мостом: `"example.org": user`, `"@admin:example.org": admin` |
   | `double_puppet.secrets` | `example.org: "as_token:<токен приложения двойного входа>"` |
   | настройки форка | см. [ниже](#настройки-форка) |

3. Второй запуск той же командой создаёт `data/registration.yaml`. Подключить его к Synapse:
   ```yaml
   # homeserver.yaml
   app_service_config_files:
     - /path/to/registration.yaml
   ```
   и перезапустить Synapse.
4. `docker-compose.yml`:
   ```yaml
   services:
     telegram-bridge:
       image: <образ>
       container_name: telegram-bridge
       restart: unless-stopped
       volumes: [./data:/data]
       networks: [matrix]          # сеть, в которой Synapse
       logging: {driver: json-file, options: {max-size: 50m, max-file: "3"}}
   networks:
     matrix: {external: true}
   ```
   `docker compose up -d`.

## C. Вручную, мост на отдельном сервере

Так устроено у автора: Telegram заблокирован у хостера сервера Matrix, поэтому мост стоит за границей.
Есть два способа связать Synapse с удалённым мостом.

### C1. Websocket через mautrix-wsproxy (мост сам подключается к серверу Matrix)

Входящих соединений на сервер моста не нужно: мост держит websocket к серверу Matrix, события от Synapse
забирает через `wsproxy`.

```
Telegram ◄──► [мост] ── wss://matrix.example.org/_matrix/client/unstable/fi.mau.as_sync ──► обратный прокси ─► [wsproxy] ◄── Synapse
                    └── https://matrix.example.org/_matrix/… (запросы к Synapse, медиа) ─────────────────────────────────►
```

**Сервер моста:**
1. Как в варианте B, шаги 1–3, но в `config.yaml`:
   - `homeserver.address: https://matrix.example.org` (публичный адрес);
   - `homeserver.websocket: true`, `homeserver.ping_interval_seconds: 30`;
   - `appservice.address: http://localhost:29317` (не используется).
2. `docker-compose.yml` — как в варианте B, без внешней сети.

**Сервер Matrix:**
1. `wsproxy` в сети Synapse, с переменными окружения: `APPSERVICE_ID` (= `id` из `registration.yaml`),
   `AS_TOKEN`, `HS_TOKEN` (оттуда же). Для шифрования — [wsproxy-fork](https://github.com/kryptonmourog/wsproxy-fork)
   (`ghcr.io/kryptonmourog/wsproxy-fork`): upstream `dock.mau.dev/mautrix/wsproxy` отбрасывает to-device события.
2. Копия `registration.yaml` для Synapse, в которой `url: http://<контейнер wsproxy>:29331`.
   Подключить её к Synapse (`app_service_config_files`; в плейбуке — `matrix_synapse_app_service_config_files`
   и `matrix_synapse_container_extra_arguments` с `--mount` файла).
3. Обратный прокси: путь `/_matrix/client/unstable/fi.mau.as_sync` на `matrix.example.org` — в `wsproxy:29331`
   (с поддержкой websocket), остальной `/_matrix` — как раньше в Synapse. Для Traefik — метки на контейнер:
   ```yaml
   traefik.http.routers.tg-wsproxy.rule: Host(`matrix.example.org`) && Path(`/_matrix/client/unstable/fi.mau.as_sync`)
   traefik.http.routers.tg-wsproxy.priority: "1000"
   traefik.http.routers.tg-wsproxy.tls: "true"
   traefik.http.services.tg-wsproxy.loadbalancer.server.port: "29331"
   ```
4. Проверка: `curl https://matrix.example.org/_matrix/client/unstable/fi.mau.as_sync` без токена отвечает
   `403 M_MISSING_TOKEN` (это ответ `wsproxy`). В логе `wsproxy` после запуска моста — `connected to websocket`.

Роль `matrix-bridge-mautrix-wsproxy` из плейбука для этого не подходит: она рассчитана на другие мосты.

#### Прямые медиа при websocket-подключении

Стандартный `direct_media` превращает мост в отдельный Matrix-медиасервер. Для выборочного режима:

```yaml
direct_media:
  enabled: true
  server_name: telegram-media.example.org
  media_id_prefix: tg_
  # Необязательно: нейтральные значения для /_matrix/federation/v1/version.
  server_implementation_name: example media
  server_implementation_version: "1"
network:
  direct_media_scope: channels
```

`telegram-media.example.org` должен передавать `/.well-known/matrix/server`, `/_matrix/key/*`, клиентские и
федерационные media endpoint в HTTP-роутер моста. В websocket-режиме форк mautrix-go запускает этот HTTP-роутер
параллельно с websocket событий.

Если входящие соединения на сервер моста запрещены, используйте отдельный потоковый WSS-туннель: его ставит роль
`telegram_media_gateway` из [telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible)
(Chisel и Traefik). Не передавайте большие файлы командой `http_proxy` основного websocket appservice: она
буферизует тело ответа в памяти и кодирует бинарные данные в base64.

### C2. Прямой HTTPS до сервера моста

Если на сервер моста можно принимать входящие соединения: обратный прокси с сертификатом на сервере моста
(`https://tg-bridge.example.org` → `http://localhost:29317`), в `registration.yaml` для Synapse —
`url: https://tg-bridge.example.org`, в конфиге моста `homeserver.websocket: false`. `wsproxy` не нужен.
Порт моста закрыть от всех, кроме прокси; доступ к нему защищён токеном `hs_token`.

## D. Плейбук matrix-docker-ansible-deploy, мост на отдельном VPS

Вариант C, но установленный Ansible: отдельный репозиторий
[telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible) кладётся
внутрь каталога `matrix-docker-ansible-deploy` (как папка `telegram-advanced`) и не меняет файлы плейбука, поэтому
`just update` продолжает работать. Настройки — в том же `vars.yml`.

Он ставит мост на VPS (Docker — сам), wsproxy и, по желанию, медиашлюз прямых медиа на сервер Matrix, берёт
двойной вход из `matrix-appservice-double-puppet` и перезапускает Synapse при смене регистрации. Пошаговая
инструкция — в README этого репозитория.

## Настройки форка

Раздел `network` конфига моста:

| Ключ | По умолчанию | Что делает |
|---|---|---|
| `folder_spaces.enabled` | `false` | папки Telegram как пространства Matrix. Требует `bridge.personal_filtering_spaces: false` |
| `folder_spaces.default_language` | `en` | язык встроенных названий и ответов бота (`en`, `ru`); пользователь меняет командой `lang` |
| `folder_spaces.prioritize_personal_folders` | `true` | при синхронизации сначала создавать комнаты чатов из личных папок |
| `matrix_request_timeout` | пусто (3 мин) | общий таймаут запроса к серверу Matrix; для больших файлов — `3h` |
| `direct_media_scope` | `channels` | прямые медиа: `all`, `channels` (каналы и супергруппы) или `off` |
| `extev_polls` | `false` | ключ upstream; в форке включает опросы Matrix в обе стороны: итоги Telegram уведомлением, свой голос в обе стороны |
| `backfill_split_media_mb` | `0` | подгружать историю порциями, разрезая на файлах больше N МБ |
| `skip_media` | `[]` | не переносить отдельные файлы: `portal`, `message`, `min_size_mb` |
| `max_initial_messages_override` | `{}` | глубина истории новых комнат по типам: `user`, `bot`, `saved`, `normal_group`, `supergroup`, `topic`, `channel` (например `{user: 300, saved: 1000}`); остальное — `backfill.max_initial_messages` |
| `personal_channels` | `false` | у каждого пользователя своя комната канала; супергруппы и форумы общие, обычные группы и ЛС и так у каждого свои. Включать до переноса каналов: существующие комнаты не переезжают |
| `encrypted_direct_media` | `off` | файлы зашифрованных комнат через прямые медиа по запросу: `large` — от `matrix.upload_file_threshold`, `all` — все документы и видео. Нужен `bridge.direct_media.enabled` |
| `comment_poll.on_read_seconds` | `60` | как часто подтягивать комментарии при чтении комнаты канала (для групп обсуждения, где пользователь не участник) |
| `comment_poll.background_minutes`, `background_hours` | `15`, `24` | фоновый опрос новых комментариев к постам за последние N часов; `0` минут — выключить |

Рекомендуемые обычные настройки mautrix-telegram:

| Ключ | Значение | Зачем |
|---|---|---|
| `bridge.personal_filtering_spaces` | `false` | иначе пространства папок выключены |
| `bridge.tag_only_on_create`, `bridge.mute_only_on_create` | `false` | метки и заглушение синхронизируются всегда |
| `network.sync.update_limit`, `create_limit` | `-1` | переносить все чаты, а не последние N |
| `network.max_member_count` | например `1000` | не переносить огромные группы |
| `bridge.permissions` | `'example.org': user` | кто может пользоваться мостом; настройки общих комнат меняет только `admin` |
| `backfill.enabled`, `max_initial_messages` | `true`, `30` | история при создании комнаты |

### Шифрование личных чатов

После настройки стандартного end-to-bridge encryption включить в `config.yaml`:

```yaml
encryption:
    allow: true
    default: false
    personal: true
```

`personal: true` шифрует только новые личные порталы. Служебная комната бота, группы и каналы остаются без
шифрования. В существующем личном портале команда `!tg encryption` показывает состояние и предупреждение,
а `!tg encryption on confirm` необратимо включает шифрование.

При передаче транзакций через websocket нужны `encryption.appservice: true`, MSC2409 и MSC3202 в Synapse,
`org.matrix.msc3202: true` в регистрации и wsproxy, который передаёт `to_device` и fallback-ключи. Для MSC4190
также нужны `encryption.msc4190: true`, `io.element.msc4190: true` в регистрации и поддержка MSC4190 сервером.

Element предупреждает, что отправитель события и владелец устройства не совпадают. Мост шифрует события
двойников и двойного входа одним криптоустройством бота. Транспорт остаётся зашифрованным, но клиент не может
криптографически подтвердить Matrix ID автора. На новом устройстве нужны ключи из резервной копии; Element Web
не ищет по содержимому зашифрованных комнат.

## Первый запуск и вход

1. Запустить мост, в логе не должно быть ошибок подключения к серверу Matrix.
2. В клиенте Matrix открыть личный чат с ботом (`@telegrambot:example.org`), дождаться, пока он войдёт.
3. `login qr` (или `login phone`). Облачный пароль Telegram бот спросит отдельно.
   Сообщение с паролем после входа удалить — комната с ботом не зашифрована.
4. Мост создаст пространства и комнаты; первая синхронизация большого аккаунта идёт долго.

Команды форка (в чате с ботом; в комнатах чатов — с префиксом, по умолчанию `!tg`):

| Команда | Что делает |
|---|---|
| `help` | все команды |
| `lang ru|en` | язык ответов бота и названий пространств |
| `folders`, `folders resync` | пространства папок |
| `archive on|off` | переносить ли архивные чаты |
| `ads room|on` | где показывать рекламу Telegram |
| `left-chats`, `left-chats remove <номер>|all` | комнаты чатов, из которых пользователь вышел |
| `settings`, `settings <имя> <значение>` | настройки комнаты (в комнате чата) |
| `encryption`, `encryption on confirm` | состояние и необратимое включение шифрования личного портала |
| `sync-chats` | полная синхронизация чатов |
| `join-discussion` | в комнате канала: вступить в его группу обсуждения, если комментировать могут только участники |

Несколько пользователей: двойной вход (double puppet) должен быть разрешён для всех пользователей моста —
в matrix-docker-ansible-deploy это `matrix_appservice_double_puppet_registration_namespace_user_regex`
(по умолчанию весь домен). Без него приглашения в комнаты и пространства приходится принимать вручную, а свои
сообщения из Telegram приходят от двойника. Если код входа не приходит в приложение Telegram, войти по `login qr`.

## Обновление и откат

- **Обновление:** резервная копия `data/` (или базы), новый образ, перезапуск. Таблицы форка обновляются сами.
- **Откат на обычный mautrix-telegram той же версии:** база остаётся рабочей — лишние таблицы `fork_*` и поля
  метаданных обычный мост игнорирует. Пространства папок перестанут обновляться, комнаты останутся. Исключение —
  личные комнаты каналов (`personal_channels`): upstream их не знает и создаст новые общие.
- Перезапускать мост быстро: если он недоступен дольше минуты, Synapse переходит к редким повторам доставки
  (до ~8 минут задержки после возвращения).

## Известные ограничения

- В зашифрованных порталах Element предупреждает о несовпадении Matrix-отправителя и владельца устройства бота.
- Слой Telegram API — 228: интерактивные виджеты ботов показываются заглушкой.
- Не подключать к сессии моста другие клиенты Telegram (Telethon и т. п.): клиент с другим слоем API на том же
  ключе заставляет Telegram отвергать запросы моста (`CONNECTION_LAYER_INVALID`) до его перезапуска.
