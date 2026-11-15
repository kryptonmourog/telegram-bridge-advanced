// mautrix-telegram - A Matrix-Telegram puppeting bridge.
// Copyright (C) 2026 kryptonmourog
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

// Bot replies are translated with bridgev2.Translate (added in our mautrix-go fork): the English text
// from the code, before formatting, is looked up in the catalog of the user's language. The language
// is the one chosen with `lang` (or `folders lang`), otherwise network.folder_spaces.default_language.
// Texts that aren't in the catalog stay in English.

func (tc *TelegramConnector) installTranslator() {
	bridgev2.Translate = tc.translate
}

func (tc *TelegramConnector) translate(ctx context.Context, userID id.UserID, msg string) string {
	if msg == "" {
		return msg
	}
	catalog, ok := translations[tc.userLanguage(ctx, userID)]
	if !ok {
		return msg
	}
	if tr, ok := catalog[msg]; ok {
		return tr
	}
	return msg
}

// userLanguage returns the language of the user's default login, or the configured default.
func (tc *TelegramConnector) userLanguage(ctx context.Context, userID id.UserID) string {
	if userID != "" && tc.Bridge != nil {
		if user, err := tc.Bridge.GetExistingUserByMXID(ctx, userID); err == nil && user != nil {
			if login := user.GetDefaultLogin(); login != nil {
				if client, ok := login.Client.(*TelegramClient); ok && client.folderSpaces != nil {
					return client.folderSpaces.language()
				}
			}
		}
	}
	if lang := tc.Config.FolderSpaces.DefaultLanguage; lang != "" {
		return lang
	}
	return "en"
}

// t translates a text of the bridge for the user of this login.
func (tc *TelegramClient) t(ctx context.Context, msg string) string {
	return tc.main.translate(ctx, tc.userLogin.UserMXID, msg)
}

var translations = map[string]map[string]string{
	"ru": ruTranslations,
}

var ruTranslations = map[string]string{
	// Commands of this bridge
	"No matching logins found with provided ID(s)":                                                                          "Не найдено входов с указанными ID",
	"Failed to synchronize chats for %s: %v":                                                                                "Не удалось синхронизировать чаты для %s: %v",
	"Successfully synchronized chats for %s":                                                                                "Чаты для %s синхронизированы",
	"You're not logged in":                                                                                                  "Вы не вошли в аккаунт",
	"Sponsored messages: %s.\nModes: `ads room` (default), `ads on`.":                                                       "Реклама: %s.\nРежимы: `ads room` (по умолчанию), `ads on`.",
	"Failed to save the setting: %v":                                                                                        "Не удалось сохранить настройку: %v",
	"Saved, but removing shown ads failed after %d: %v":                                                                     "Сохранено, но удаление показанной рекламы прервалось после %d: %v",
	"Sponsored messages: %s. Removed %d shown ads.":                                                                         "Реклама: %s. Удалено показанных объявлений: %d.",
	"Usage: `ads` (status), `ads room`, `ads on`":                                                                           "Использование: `ads` (состояние), `ads room`, `ads on`",
	"`room`: Telegram's ads go to a separate muted, low priority room instead of the channels":                              "`room`: реклама Telegram приходит в отдельную заглушённую комнату с низким приоритетом, а не в каналы",
	"`on`: Telegram's ads are shown in the channel rooms":                                                                   "`on`: реклама Telegram показывается в комнатах каналов",
	"`off`: ads aren't requested at all (Telegram's API terms ask apps to show them)":                                       "`off`: реклама не запрашивается совсем (условия API Telegram требуют её показывать)",
	"No rooms of chats you've left were found. The list is updated by a full chat sync (`sync-chats`).":                     "Комнат чатов, из которых вы вышли, не найдено. Список обновляется при полной синхронизации чатов (`sync-chats`).",
	"Rooms of chats you're no longer in on Telegram:\n\n%s\n\nUse `left-chats remove <number>` or `left-chats remove all`.": "Комнаты чатов, в которых вас больше нет в Telegram:\n\n%s\n\nУдалить: `left-chats remove <номер>` или `left-chats remove all`.",
	"Usage: `left-chats` (list), `left-chats remove <number>`, `left-chats remove all`":                                     "Использование: `left-chats` (список), `left-chats remove <номер>`, `left-chats remove all`",
	"There's no chat number %s in the list, see `left-chats`.":                                                              "В списке нет чата с номером %s, см. `left-chats`.",
	"Failed to remove %s: %v":                                                                                               "Не удалось удалить %s: %v",
	"Failed to save the list: %v":                                                                                           "Не удалось сохранить список: %v",
	"Removed %d rooms.":                                                                                                     "Удалено комнат: %d.",
	"Found %d chats that have a room in Matrix but that you're no longer in on Telegram (probably left while the bridge was offline).\n\n%s\n\nUse `left-chats remove <number>` or `left-chats remove all` to remove their rooms.": "Найдено чатов, у которых есть комната в Matrix, но в которых вас больше нет в Telegram (вероятно, вы вышли, пока мост был выключен): %d.\n\n%s\n\nУдалить их комнаты: `left-chats remove <номер>` или `left-chats remove all`.",
	"The archive setting needs folder spaces, which are disabled. Archived chats are bridged like any other chat.":                                                                                                                 "Настройка архива работает только с пространствами папок, а они выключены. Архивные чаты переносятся как обычные.",
	"Archive is %s.\nArchived chats: %d. Use `archive on` or `archive off` to change it.":                                                                                                                                          "Архив %s.\nАрхивных чатов: %d. Изменить: `archive on` или `archive off`.",
	"off: archived chats aren't bridged and their rooms are hidden":                                                                                                                                                                "выключен: архивные чаты не переносятся, их комнаты скрыты",
	"on: archived chats are bridged into the archive space":                                                                                                                                                                        "включён: архивные чаты переносятся в пространство архива",
	"Bridging archived chats, this may take a while...":                                                                                                                                                                            "Переношу архивные чаты, это может занять время...",
	"Failed to turn the archive on: %v":                                                                                                                                                                                            "Не удалось включить архив: %v",
	"Archive is on. Rooms of archived chats are back in the archive space and catching up; %d new rooms are being created.":                                                                                                        "Архив включён. Комнаты архивных чатов вернулись в пространство архива и догружают сообщения; создаётся новых комнат: %d.",
	"Failed to turn the archive off: %v":                                                                                                                                                                                           "Не удалось выключить архив: %v",
	"Archive is off. You left the rooms of archived chats; they're kept, so `archive on` only catches up on new messages.":                                                                                                         "Архив выключен. Вы вышли из комнат архивных чатов; они сохранены, поэтому `archive on` догрузит только новые сообщения.",
	"Usage: `archive` (status), `archive on`, `archive off`":                                                                                                                                                                       "Использование: `archive` (состояние), `archive on`, `archive off`",
	"Folder spaces are disabled. They need `network.folder_spaces.enabled: true` and `bridge.personal_filtering_spaces: false` in the bridge config.":                                                                              "Пространства папок выключены. Для них нужны `network.folder_spaces.enabled: true` и `bridge.personal_filtering_spaces: false` в конфиге моста.",
	"Failed to get folders: %v":                                           "Не удалось получить папки: %v",
	"Usage: `folders lang <code>`, supported: %s":                         "Использование: `folders lang <код>`, доступны: %s",
	"Unsupported language `%s`, supported: %s":                            "Язык `%s` не поддерживается, доступны: %s",
	"Failed to save language: %v":                                         "Не удалось сохранить язык: %v",
	"Language set to `%s`, space names will be updated in a few seconds":  "Язык: `%s`, названия пространств обновятся через несколько секунд",
	"Language set to `%s`.":                                               "Язык: `%s`.",
	"Language: `%s` (supported: %s). Change it with `lang <code>`.":       "Язык: `%s` (доступны: %s). Изменить: `lang <код>`.",
	"Show or change the language of the bridge's replies and space names": "Показать или изменить язык ответов моста и названий пространств",
	"[_code_]":                         "[_код_]",
	"Failed to sync folder spaces: %v": "Не удалось синхронизировать пространства папок: %v",
	"Folder spaces synchronized":       "Пространства папок синхронизированы",
	"Usage: `folders` (status), `folders lang <code>`, `folders resync`":                "Использование: `folders` (состояние), `folders lang <код>`, `folders resync`",
	"No logins found to upgrade the chat":                                               "Не найден вход для преобразования чата",
	"Failed to find a login to upgrade the chat.":                                       "Не удалось найти вход для преобразования чата.",
	"Failed to parse portal ID":                                                         "Не удалось разобрать ID портала",
	"Only minigroups can be upgraded (this is already a channel/supergroup)":            "Преобразовать можно только обычную группу (это уже канал или супергруппа)",
	"Only minigroups can be upgraded (this is direct chat)":                             "Преобразовать можно только обычную группу (это личный чат)",
	"Failed to upgrade chat: %v":                                                        "Не удалось преобразовать чат: %v",
	"Usage: `$cmdprefix join [login ID] <invite link>`":                                 "Использование: `$cmdprefix join [ID входа] <ссылка-приглашение>`",
	"No login found with the provided ID":                                               "Вход с таким ID не найден",
	"Failed to resolve username from invite link: %v":                                   "Не удалось найти имя из ссылки-приглашения: %v",
	"That username does not belong to a channel or supergroup":                          "Это имя не принадлежит каналу или супергруппе",
	"Channel information not found in resolve response":                                 "В ответе Telegram нет данных о канале",
	"Failed to join chat: %v":                                                           "Не удалось вступить в чат: %v",
	"Invalid invite link":                                                               "Неверная ссылка-приглашение",
	"Invite link expired":                                                               "Срок действия ссылки истёк",
	"You're already a member of %s":                                                     "Вы уже состоите в %s",
	"Invalid invite link format":                                                        "Неверный формат ссылки-приглашения",
	"Successfully joined %s":                                                            "Вы вступили в %s",
	"The chat wants you to open a webview to join. Please use the native Telegram app.": "Чтобы вступить, чат просит открыть веб-страницу. Используйте приложение Telegram.",
	"Unexpected response for join request":                                              "Неожиданный ответ на запрос вступления",
	"Usage: `$cmdprefix emoji-pack <upload/download/list/help> [args...]`":              "Использование: `$cmdprefix emoji-pack <upload/download/list/help> [аргументы...]`",
	"Synchronize your chats":                                                            "Синхронизировать чаты",
	"Show or change where Telegram's sponsored messages are shown: in a separate room or in the channels": "Показать или изменить, где показывать рекламу Telegram: в отдельной комнате или в каналах",
	"List rooms of chats you're no longer in on Telegram, or remove them":                                 "Показать комнаты чатов, в которых вас больше нет в Telegram, или удалить их",
	"Show whether archived Telegram chats are bridged, or turn bridging them on or off":                   "Показать, переносятся ли архивные чаты Telegram, или включить/выключить их перенос",
	"Show Telegram folder spaces, change their language or re-sync them":                                  "Показать пространства папок Telegram, сменить их язык или пересинхронизировать",
	"Upgrade a minigroup to a supergroup on Telegram":                                                     "Преобразовать группу в супергруппу в Telegram",
	"Join a Telegram group using an invite link.":                                                         "Вступить в группу Telegram по ссылке-приглашению.",
	"Bridge emoji packs between Matrix and Telegram.":                                                     "Переносить наборы эмодзи между Matrix и Telegram.",
	"[_login ID_]":                          "[_ID входа_]",
	"[`room` | `on`]":                       "[`room` | `on`]",
	"[`remove` <_number_ | `all`>]":         "[`remove` <_номер_ | `all`>]",
	"[`lang` _code_ | `resync`]":            "[`lang` _код_ | `resync`]",
	"[login ID] <invite link>":              "[ID входа] <ссылка-приглашение>",
	"<upload/download/list/help> [args...]": "<upload/download/list/help> [аргументы...]",

	// Emoji packs
	"Failed to list image packs: %v": "Не удалось получить список наборов: %v",
	"Unexpected response type: %T":   "Неожиданный тип ответа: %T",
	"Your packs:\n\n%s":              "Ваши наборы:\n\n%s",
	"Usage: `$cmdprefix emoji-pack upload <telegram shortcode> <room ID> <state key>`": "Использование: `$cmdprefix emoji-pack upload <короткое имя в Telegram> <ID комнаты> <ключ состояния>`",
	"Double puppeting is required to fetch emoji packs from Matrix":                    "Чтобы брать наборы эмодзи из Matrix, нужен double puppeting",
	"Matrix connector does not implement required interface":                           "Matrix-коннектор не поддерживает нужный интерфейс",
	"Failed to join room: %v":                                            "Не удалось войти в комнату: %v",
	"Failed to get state event: %v":                                      "Не удалось получить событие состояния: %v",
	"Unexpected parsed content type %T":                                  "Неожиданный тип содержимого %T",
	"Failed to synchronize emoji pack: %v":                               "Не удалось синхронизировать набор эмодзи: %v",
	"Successfully synchronized %s":                                       "%s синхронизирован",
	"Failed to reupload %s: %v":                                          "Не удалось перезагрузить %s: %v",
	"Failed to add %s/%d to pack: %v":                                    "Не удалось добавить %s/%d в набор: %v",
	"Usage: `$cmdprefix emoji-pack download <pack shortcode or link>`":   "Использование: `$cmdprefix emoji-pack download <короткое имя набора или ссылка>`",
	"Failed to get space room: %v":                                       "Не удалось получить пространство: %v",
	"Can't bridge image packs if personal filtering spaces are disabled": "Наборы нельзя перенести, когда личные пространства выключены",
	"Failed to import pack: %v":                                          "Не удалось импортировать набор: %v",
	"Failed to send image pack to space: %v":                             "Не удалось отправить набор в пространство: %v",
	"Successfully bridged image pack to %s":                              "Набор перенесён в %s",
	"Usage: `$cmdprefix import-image-pack <url>`":                        "Использование: `$cmdprefix import-image-pack <url>`",
	"Failed to import pack: %s":                                          "Не удалось импортировать набор: %s",
	"Successfully bridged image pack to %s%s":                            "Набор перенесён в %s%s",
	"Import a sticker or emoji pack from the remote network":             "Импортировать набор стикеров или эмодзи из Telegram",

	// Login
	"Phone Number":                           "Номер телефона",
	"Phone number":                           "Номер телефона",
	"QR Code":                                "QR-код",
	"Bot token":                              "Токен бота",
	"Manual":                                 "Вручную",
	"Code":                                   "Код",
	"Password":                               "Пароль",
	"Session data JSON":                      "JSON с данными сессии",
	"Login using your Telegram phone number": "Вход по номеру телефона Telegram",
	"Login by scanning a QR code from your phone":                                                                        "Вход по QR-коду с телефона",
	"Log in as a bot using the bot token provided by BotFather":                                                          "Вход как бот по токену от BotFather",
	"Log in using existing session credentials (advanced, do not use)":                                                   "Вход с данными существующей сессии (для опытных, не используйте)",
	"You have two-factor authentication enabled.":                                                                        "У вас включена двухфакторная аутентификация.",
	"Incorrect password, please try again. Use the official Telegram app to reset your password if you've forgotten it.": "Неверный пароль, попробуйте ещё раз. Если вы его забыли, сбросьте его в официальном приложении Telegram.",
	"Please enter the bot token you want to log in as":                                                                   "Введите токен бота, под которым войти",
	"Include the country code with +":                                                                                    "С кодом страны через +",
	"Incorrect code":                                                                                                     "Неверный код",
	"Scan the QR code on your phone to log in":                                                                           "Отсканируйте QR-код телефоном, чтобы войти",
	"The code was sent to the Telegram app on your phone":                                                                "Код отправлен в приложение Telegram на телефоне",
	"The code was sent to your phone via SMS":                                                                            "Код отправлен на телефон по SMS",
	"The code will be read to you in a phone call":                                                                       "Код продиктуют в телефонном звонке",
	"Please enter your %s":                                                                                               "Введите: %s",
	"Options: `%s`":                                                                                                      "Варианты: `%s`",
	"Enter `skip` to skip this field":                                                                                    "Введите `skip`, чтобы пропустить",
	"Usage: `$cmdprefix relogin <login ID> [_flow ID_]`\n\nYour logins:\n\n%s":                                           "Использование: `$cmdprefix relogin <ID входа> [_способ_]`\n\nВаши входы:\n\n%s",
	"You have reached the maximum number of logins (%d). ":                                                               "Достигнуто максимальное число входов (%d). ",
	"You already have an ongoing %s. You can use `$cmdprefix cancel` to cancel it.":                                      "У вас уже идёт %s. Отменить: `$cmdprefix cancel`.",
	"No login flows available.":                                                                                          "Нет доступных способов входа.",
	"Invalid login flow `%s`. Available options:\n\n%s":                                                                  "Неизвестный способ входа `%s`. Доступны:\n\n%s",
	"Please specify a login flow, e.g. `relogin %s %s`.\n\n%s":                                                           "Укажите способ входа, например `relogin %s %s`.\n\n%s",
	"Please specify a login flow, e.g. `login %s`.\n\n%s":                                                                "Укажите способ входа, например `login %s`.\n\n%s",
	"⚠️ This is not your management room. Entering login info must be prefixed with `$cmdprefix` like other commands.": "⚠️ Это не ваша комната управления. Данные для входа, как и команды, нужно начинать с `$cmdprefix`.",
	"Failed to prepare login process: %v":                           "Не удалось подготовить вход: %v",
	"Failed to start login: %v":                                     "Не удалось начать вход: %v",
	"Invalid number of extra parameters (expected 0 or %d, got %d)": "Неверное число дополнительных параметров (нужно 0 или %d, передано %d)",
	"Invalid value for %s: %v":                                      "Неверное значение для %s: %v",
	"Invalid value for %s: `%s` doesn't match regex `%s`":           "Неверное значение для %s: `%s` не подходит под `%s`",
	"Failed to submit input: %v":                                    "Не удалось отправить ввод: %v",
	"Invalid value: %v":                                             "Неверное значение: %v",
	"Failed to send QR code: %v":                                    "Не удалось отправить QR-код: %v",
	"Login failed: %v":                                              "Вход не удался: %v",
	"Login URL: <%s>":                                               "Ссылка для входа: <%s>",
	"Failed to parse input as JSON: %v":                             "Не удалось разобрать ввод как JSON: %v",
	"Input is not valid JSON":                                       "Ввод не является корректным JSON",
	"Usage: `$cmdprefix logout <login ID>`\n\nYour logins:\n\n%s":   "Использование: `$cmdprefix logout <ID входа>`\n\nВаши входы:\n\n%s",
	"Logged out": "Вы вышли",
	"Usage: `$cmdprefix set-preferred-login <login ID>`\n\nYour logins:\n\n%s": "Использование: `$cmdprefix set-preferred-login <ID входа>`\n\nВаши входы:\n\n%s",
	"Failed to set preferred login: %v":                                        "Не удалось задать основной вход: %v",
	"Preferred login set":                                                      "Основной вход задан",
	"Log into the bridge":                                                      "Войти в мост",
	"Re-authenticate an existing login":                                        "Повторно войти в существующий аккаунт",
	"List your logins":                                                         "Показать ваши входы",
	"Log out of the bridge":                                                    "Выйти из моста",
	"Set the preferred login ID for sending messages to this portal (only relevant when logged into multiple accounts via the bridge)": "Выбрать вход для отправки сообщений в эту комнату (нужно, только если в мост вошли несколько аккаунтов)",
	"[_flow ID_]":              "[_способ_]",
	"<_login ID_> [_flow ID_]": "<_ID входа_> [_способ_]",
	"<_login ID_>":             "<_ID входа_>",

	// Bridge state notices
	"State update for %s: `%s`":      "Состояние %s: `%s`",
	" not resolved after waiting %s": " и не восстановилось за %s",
	"STARTING":                       "запуск",
	"UNCONFIGURED":                   "не настроен",
	"RUNNING":                        "работает",
	"BRIDGE_UNREACHABLE":             "мост недоступен",
	"CONNECTING":                     "подключение",
	"BACKFILLING":                    "загрузка истории",
	"CONNECTED":                      "подключён",
	"TRANSIENT_DISCONNECT":           "временно отключён",
	"BAD_CREDENTIALS":                "неверные данные входа",
	"UNKNOWN_ERROR":                  "неизвестная ошибка",
	"LOGGED_OUT":                     "вышел из аккаунта",
	"Telegram client disconnected":   "Клиент Telegram отключился",

	// Cleanup and management room
	"Failed to delete portal: %v":                                                      "Не удалось удалить портал: %v",
	"Failed to clean up room: %v":                                                      "Не удалось очистить комнату: %v",
	"Failed to get portals: %v":                                                        "Не удалось получить порталы: %v",
	"Failed to delete portal %s: %v":                                                   "Не удалось удалить портал %s: %v",
	"Failed to clean up room %s: %v":                                                   "Не удалось очистить комнату %s: %v",
	"This room is already your management room":                                        "Эта комната уже ваша комната управления",
	"This is a portal room: you can't set this as your management room":                "Это комната чата: её нельзя сделать комнатой управления",
	"Failed to get room members":                                                       "Не удалось получить участников комнаты",
	"The bridge bot must be in the room to set it as your management room":             "Чтобы сделать комнату комнатой управления, в ней должен быть бот моста",
	"Your management room must not have any members other than you and the bridge bot": "В комнате управления не должно быть никого, кроме вас и бота моста",
	"Failed to save management room":                                                   "Не удалось сохранить комнату управления",
	"Management room updated":                                                          "Комната управления изменена",
	"Delete the current portal room":                                                   "Удалить текущую комнату чата",
	"Delete all portals the bridge knows about":                                        "Удалить все комнаты чатов, известные мосту",
	"Mark this room as your management room":                                           "Сделать эту комнату комнатой управления",

	// Debug
	"Usage: `$cmdprefix debug-register-push <login ID> <push type> <push token>`\n\nYour logins:\n\n%s": "Использование: `$cmdprefix debug-register-push <ID входа> <тип push> <push-токен>`\n\nВаши входы:\n\n%s",
	"Unknown push type `%s`. Allowed types: `web`, `apns`, `fcm`":                                       "Неизвестный тип push `%s`. Допустимые: `web`, `apns`, `fcm`",
	"Login `%s` not found": "Вход `%s` не найден",
	"This network connector does not support push registration":   "Этот мост не поддерживает регистрацию push",
	"Failed to register pusher: %v":                               "Не удалось зарегистрировать push: %v",
	"Pusher de-registered successfully":                           "Push отключён",
	"Pusher registered successfully":                              "Push зарегистрирован",
	"Failed to parse JSON: %v":                                    "Не удалось разобрать JSON: %v",
	"Failed to deserialize content: %v":                           "Не удалось разобрать содержимое: %v",
	"Result: %+v":                                                 "Результат: %+v",
	"Network connector does not support resetting HTTP transport": "Мост не поддерживает сброс HTTP-соединений",
	"Register a pusher":                                           "Зарегистрировать push",
	"Send a room account data event to the bridge":                "Отправить мосту данные аккаунта для комнаты",
	"Reset network connections to the remote network":             "Сбросить соединения с Telegram",

	// Command handler
	"Failed to get room power levels to see if you're allowed to use that command": "Не удалось получить права в комнате, чтобы проверить доступ к команде",
	"That command is limited to bridge administrators.":                            "Эта команда только для администраторов моста.",
	"You do not have permissions to log into this bridge.":                         "У вас нет прав входить в этот мост.",
	"That command requires room admin rights.":                                     "Для этой команды нужны права администратора комнаты.",
	"That command can only be ran in portal rooms.":                                "Эту команду можно выполнить только в комнате чата.",
	"That command requires you to be logged in.":                                   "Для этой команды нужно войти в аккаунт.",
	"Unknown command, use the `help` command for help.":                            "Неизвестная команда, список команд: `help`.",
	"%s cancelled.":             "%s отменено.",
	"No ongoing command.":       "Нет выполняемой команды.",
	"Cancel an ongoing action.": "Отменить текущее действие.",

	// Help
	"This is your management room: prefixing commands with `%s` is not required.":                                    "Это ваша комната управления: префикс `%s` перед командами не нужен.",
	"**This is a portal room**: you must always prefix commands with `%s`. Management commands will not be bridged.": "**Это комната чата**: команды всегда начинаются с `%s`. Команды управления в Telegram не отправляются.",
	"This is not your management room: prefixing commands with `%s` is required.":                                    "Это не ваша комната управления: команды нужно начинать с `%s`.",
	"Parameters in [square brackets] are optional, while parameters in <angle brackets> are required.":               "Параметры в [квадратных скобках] необязательны, в <угловых скобках> — обязательны.",
	"Show this help message.":     "Показать эту справку.",
	"Unclassified":                "Прочее",
	"General":                     "Общие",
	"Authentication":              "Вход",
	"Starting and managing chats": "Создание чатов и управление ими",
	"Miscellaneous":               "Разное",
	"Administration":              "Администрирование",

	// Managing chats
	"This room is bridged to %s%s on %s":                                                            "Эта комната связана с %s%s в %s",
	"This room is already bridged to %s%s on %s":                                                    "Эта комната уже связана с %s%s в %s",
	"You don't have permission to bridge this room":                                                 "У вас нет прав связать эту комнату",
	"Usage: `$cmdprefix bridge [login ID] <chat ID>`":                                               "Использование: `$cmdprefix bridge [ID входа] <ID чата>`",
	"Failed to check power levels in room: %v":                                                      "Не удалось проверить права в комнате: %v",
	"That chat is already bridged to another room, and you don't have the permission to delete it.": "Этот чат уже связан с другой комнатой, а удалить её у вас нет прав.",
	"That chat is already bridged to [%s](%s). Use `--overwrite` to delete the existing room.":      "Этот чат уже связан с [%s](%s). Чтобы удалить ту комнату, добавьте `--overwrite`.",
	"Failed to get chat info: %v":                                                                   "Не удалось получить данные чата: %v",
	"Chat info not found":                                                                           "Данные чата не найдены",
	"Failed to plumb room: %v":                                                                      "Не удалось связать комнату: %v",
	"Successfully plumbed this room to %s on %s%s":                                                  "Комната связана с %s в %s%s",
	"You don't have permission to unbridge this portal":                                             "У вас нет прав отвязать эту комнату",
	"Failed to remove portal mxid: %v":                                                              "Не удалось отвязать комнату: %v",
	"Failed to find login: %v":                                                                      "Не удалось найти вход: %v",
	"No login found for sync":                                                                       "Не найден вход для синхронизации",
	"Invalid duration: %v":                                                                          "Неверная длительность: %v",
	"Failed to %s chat: %v":                                                                         "Не удалось выполнить %s для чата: %v",
	"Failed to delete chat: %v":                                                                     "Не удалось удалить чат: %v",
	"This command can only be used in DM portals":                                                   "Эту команду можно выполнить только в личном чате",
	"Failed to block user: %v":                                                                      "Не удалось заблокировать пользователя: %v",
	"View the internal network ID of the current portal room":                                       "Показать внутренний ID чата этой комнаты",
	"Bridge an existing chat on the remote network to this Matrix room":                             "Связать существующий чат Telegram с этой комнатой Matrix",
	"Unbridge the current portal room":                                                              "Отвязать эту комнату от чата",
	"Sync the current portal room":                                                                  "Синхронизировать эту комнату",
	"Mute or unmute a chat on the remote network":                                                   "Выключить или включить уведомления чата в Telegram",
	"Delete the current chat on the remote network":                                                 "Удалить этот чат в Telegram",
	"Block the recipient of the current DM chat on the remote network":                              "Заблокировать собеседника этого личного чата в Telegram",
	"Manage the room creation filter. Changes are currently in-memory only":                         "Настроить фильтр создания комнат. Изменения пока не сохраняются после перезапуска",
	"[login ID] <chat ID>":                                                                          "[ID входа] <ID чата>",
	"[duration]":                                                                                    "[длительность]",

	// Relay
	"This bridge does not allow relay mode":                                  "Этот мост не разрешает режим ретрансляции",
	"You don't have permission to manage the relay in this room":             "У вас нет прав настраивать ретрансляцию в этой комнате",
	"Failed to save relay settings":                                          "Не удалось сохранить настройки ретрансляции",
	"This portal doesn't have a relay set.":                                  "В этой комнате ретрансляция не настроена.",
	"Stopped relaying messages for users who haven't logged in":              "Сообщения пользователей без входа больше не ретранслируются",
	"Use your account to relay messages sent by users who haven't logged in": "Ретранслировать через ваш аккаунт сообщения пользователей без входа",
	"Stop relaying messages sent by users who haven't logged in":             "Прекратить ретрансляцию сообщений пользователей без входа",

	// Starting chats
	"Usage: `$cmdprefix %s <identifier>`":                "Использование: `$cmdprefix %s <идентификатор>`",
	"Failed to resolve identifier: %v":                   "Не удалось найти идентификатор: %v",
	"You already have a direct chat with %s at [%s](%s)": "У вас уже есть личный чат с %s: [%s](%s)",
	"Created chat with %s: [%s](%s)":                     "Создан чат с %s: [%s](%s)",
	"Found %s":                                           "Найден %s",
	"Failed to get room members: %v":                     "Не удалось получить участников комнаты: %v",
	"Failed to create group: %v":                         "Не удалось создать группу: %v",
	"No login found with ID %s":                          "Вход с ID %s не найден",
	"Login %s does not belong to you":                    "Вход %s принадлежит не вам",
	"Login %s is not logged in":                          "Вход %s не авторизован",
	"Failed to get portal":                               "Не удалось получить комнату чата",
	"No portal found with ID %s. You may need to receive a message in the chat first": "Чат с ID %s не найден. Возможно, сначала в нём должно прийти сообщение",
	"Usage: `$cmdprefix create-portal [login ID] <chat ID>`":                          "Использование: `$cmdprefix create-portal [ID входа] <ID чата>`",
	"That chat already has a Matrix room at [%s](%s)":                                 "У этого чата уже есть комната Matrix: [%s](%s)",
	"That chat already has a Matrix room":                                             "У этого чата уже есть комната Matrix",
	"Failed to create portal room: %v":                                                "Не удалось создать комнату чата: %v",
	"Successfully created portal room [%s](%s)":                                       "Комната чата создана: [%s](%s)",
	"Usage: `$cmdprefix search <query>`":                                              "Использование: `$cmdprefix search <запрос>`",
	"Failed to search for users: %v":                                                  "Не удалось найти пользователей: %v",
	"Search results:\n\n%s":                                                           "Результаты поиска:\n\n%s",
	"Check if a given identifier is on the remote network":                            "Проверить, есть ли такой идентификатор в Telegram",
	"Start a direct chat with the given user":                                         "Начать личный чат с пользователем",
	"Create a new group chat for the current Matrix room":                             "Создать группу в Telegram для этой комнаты Matrix",
	"Create a new Matrix room for an existing chat on the remote network":             "Создать комнату Matrix для существующего чата Telegram",
	"Search for users on the remote network":                                          "Найти пользователей в Telegram",
	"[_login ID_] <_identifier_>":                                                     "[_ID входа_] <_идентификатор_>",
	"[_group type_]":                                                                  "[_тип группы_]",
	"<_query_>":                                                                       "<_запрос_>",

	// Sudo
	"Run a command as a different user.": "Выполнить команду от имени другого пользователя.",
	"Run a command in a different room.": "Выполнить команду в другой комнате.",

	// Invites and message status
	"Failed to finish configuring portal. The chat may or may not work": "Не удалось до конца настроить комнату. Чат может не работать",
	"Your reaction was not bridged":                                     "Ваша реакция не доставлена в Telegram",
	"Your reaction may not have been bridged":                           "Ваша реакция, возможно, не доставлена в Telegram",
	"Your redaction was not bridged":                                    "Ваше удаление не доставлено в Telegram",
	"Your redaction may not have been bridged":                          "Ваше удаление, возможно, не доставлено в Telegram",
	"Your message was not bridged":                                      "Ваше сообщение не доставлено в Telegram",
	"Your message may not have been bridged":                            "Ваше сообщение, возможно, не доставлено в Telegram",
	"Handling your command panicked":                                    "Команда завершилась аварийно",
}
