# Companion-плейбук для удалённого Telegram-моста

Плейбук ставит мост на отдельный VPS, а wsproxy и медиашлюз на сервер Matrix. Он работает рядом с оригинальным
`matrix-docker-ansible-deploy` и не меняет его файлы.

## Что он устанавливает

| Группа inventory | Компоненты |
|---|---|
| `telegram_bridge_servers` | mautrix-telegram и исходящий Chisel client |
| `matrix_servers` | mautrix-wsproxy, Chisel server, `socat` и маршруты Traefik |

Плейбук передаёт регистрацию моста между хостами внутри Ansible. Задачи с токенами используют `no_log`, а файлы
с секретами получают режим `0600`.

## Требования

- На обеих машинах уже работают Docker и Docker Compose v2.
- Сервер Matrix установлен оригинальным `matrix-docker-ansible-deploy` и использует Traefik.
- DNS медиадомена указывает на сервер Matrix.
- Контроллер Ansible имеет `community.docker` версии 3.6.0 или новее.
- Для режима `local` нужный образ уже находится в локальном Docker контроллера.

Установить коллекцию:

```bash
ansible-galaxy collection install -r collections/requirements.yml
```

## Inventory и переменные

Добавьте EU VPS в inventory оригинального плейбука. Оригинальный `setup.yml` использует только
`matrix_servers`, поэтому он не начнёт ставить Matrix на EU VPS.

```ini
[matrix_servers]
matrix.example.org ansible_host=matrix.example.org ansible_user=root

[telegram_bridge_servers]
telegram-vps.example.org ansible_host=telegram-vps.example.org ansible_user=root
```

Создайте рабочий файл переменных рядом с этим README. Он исключён из Git:

```bash
cp group_vars/all.example.yml group_vars/all.yml
```

Зашифруйте Telegram `api_hash` и общий секрет Chisel через Ansible Vault:

```bash
ansible-vault encrypt_string --name telegram_api_hash 'TELEGRAM_API_HASH'
ansible-vault encrypt_string --name telegram_media_tunnel_auth 'bridge:LONG_RANDOM_PASSWORD'
```

Образы поддерживают три источника:

- `registry`: плейбук скачивает образ из registry;
- `local`: плейбук экспортирует образ из Docker на контроллере и загружает на нужный сервер;
- `existing`: плейбук требует, чтобы образ уже находился на сервере.

Для локально собранных форков задайте:

```yaml
telegram_bridge_image: telegram-bridge-advanced:folder-spaces-COMMIT
telegram_bridge_image_source: local
telegram_wsproxy_image: wsproxy-advanced:TAG
telegram_wsproxy_image_source: local
```

## Подключение регистрации к Synapse

Companion-плейбук создаёт `/opt/matrix-telegram-wsproxy/registration.yaml`, но конфигурацией Synapse продолжает
управлять оригинальный плейбук. Объедините `synapse-vars-snippet.yml` со своим `vars.yml`.

Если `matrix_synapse_container_additional_volumes` или `matrix_synapse_app_service_config_files` уже заданы,
добавьте элементы в существующие списки. Не создавайте второй ключ YAML с тем же именем.

Сначала проверьте изменения Synapse:

```bash
cd /path/to/matrix-docker-ansible-deploy
ansible-playbook -i inventory/hosts setup.yml --tags=setup-synapse --check --diff
```

Затем примените их без тега `start`:

```bash
ansible-playbook -i inventory/hosts setup.yml --tags=setup-synapse
```

После этого отдельно перезапустите Synapse.

## Установка companion-компонентов

Запускайте `site.yml` из этого каталога. Путь inventory может указывать на оригинальный плейбук:

```bash
ansible-playbook \
  -i /path/to/matrix-docker-ansible-deploy/inventory/hosts \
  site.yml
```

Первый запуск делает следующее:

1. Создаёт стандартный `config.yaml` моста на EU VPS.
2. Накладывает управляемые настройки из Ansible, но сохраняет остальные настройки стандартного конфига.
3. Создаёт `registration.yaml` и передаёт его на Matrix-сервер без вывода токенов.
4. Запускает wsproxy, мост и медиатуннель.

Плейбук не удаляет базу, Telegram-сессии и существующую регистрацию. Для шифрования он добавляет флаги MSC3202
и MSC4190 в копию регистрации на Matrix-сервере, не меняя её токены.

Чтобы намеренно создать новые токены, задайте `telegram_bridge_regenerate_registration: true` на один запуск.
После этого снова примените настройку Synapse, потому что токены регистрации изменятся.

## Проверка

```bash
ansible-playbook \
  -i /path/to/matrix-docker-ansible-deploy/inventory/hosts \
  verify.yml
```

Проверка требует, чтобы все контейнеры работали. Для direct media она также проверяет federation version на
медиадомене.

## Обновление

Укажите новый неизменяемый тег образа и снова запустите `site.yml`. Для локального образа используйте новый тег,
иначе роль посчитает, что образ уже загружен на сервер.

## Отключение direct media

Установите:

```yaml
telegram_direct_media_enabled: false
telegram_direct_media_scope: off
```

Новые файлы пойдут через Synapse. Старые ссылки `mxc://` с медиадомена всё ещё требуют работающий медиашлюз,
поэтому не удаляйте его, пока такие ссылки нужны пользователям.
