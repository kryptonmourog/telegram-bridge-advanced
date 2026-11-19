# telegram-bridge-advanced

**Русский** | [English](README.md)

Неофициальный форк [mautrix-telegram](https://github.com/mautrix/telegram) — моста Matrix ↔ Telegram (bridgev2,
Go). Это основной репозиторий проекта: код моста, образы Docker, инструкция по установке и выпуски.

## Зачем он создан

Для тех, кто переносит свою жизнь в Telegram в Matrix и хочет пользоваться им так же, как привык: с папками,
комментариями к постам каналов, опросами и ботами. Upstream хорошо переносит сообщения, но многое из того, как
устроен Telegram, по дороге теряется. Ещё форк рассчитан на случай, когда Telegram с сервера Matrix недоступен
(мост работает на другом сервере и сам подключается к Matrix), и на домашний сервер, которым пользуются несколько
человек.

## Что он добавляет

- Папки Telegram как пространства Matrix, архив, закреплённые чаты, уведомления по умолчанию как в Telegram.
- Комментарии к постам каналов — ветками под постами, в обе стороны (текст и медиа), в том числе для групп
  обсуждения, где пользователь не состоит; обычный ответ на пост становится комментарием.
- Несколько пользователей одного моста: у каждого своя комната канала, общие супергруппы и форумы, настройки
  общих комнат меняет только администратор моста.
- Опросы в обе стороны, кнопки ботов, богатые посты, реклама Telegram в отдельной комнате, настройки комнаты
  (`!tg settings`: лимит медиа, пауза, подписи, кнопки), уборка комнат покинутых чатов, ответы бота по-русски.
- Шифрование личных чатов; прямые медиа для каналов и, с шифрованием, для личных чатов; большие файлы потоком
  без временных файлов; глубина истории по типам чатов, история порциями.

## Установка

- Инструкция: [docs/INSTALL.ru.md](docs/INSTALL.ru.md) — ручная установка, роль matrix-docker-ansible-deploy,
  мост на отдельном сервере, настройки форка.
- С matrix-docker-ansible-deploy: [telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible).
- Образ: `ghcr.io/kryptonmourog/telegram-bridge-advanced:<версия>`, например `v0.2611.0-adv.1` (схема версий,
  обновление и возврат на upstream — в [инструкции](docs/INSTALL.ru.md#версии)).

## Проект

| Репозиторий | Что это |
|---|---|
| **telegram-bridge-advanced** (вы здесь) | мост: код, образы, инструкция по установке, выпуски |
| [mautrix-go-fork](https://github.com/kryptonmourog/mautrix-go-fork) | форк mautrix-go, с которым собирается мост |
| [wsproxy-fork](https://github.com/kryptonmourog/wsproxy-fork) | websocket-прокси для моста на отдельном сервере, с шифрованием |
| [telegram-bridge-advanced-ansible](https://github.com/kryptonmourog/telegram-bridge-advanced-ansible) | установка через matrix-docker-ansible-deploy, в том числе на отдельный сервер |

## Лицензия

AGPL-3.0, как у mautrix-telegram. Документация upstream: [docs.mau.fi](https://docs.mau.fi/bridges/go/telegram/index.html).
