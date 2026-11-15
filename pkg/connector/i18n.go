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
	"fmt"
	"strings"
	"time"

	"go.mau.fi/util/exfmt"

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
	return translateTo(tc.userLanguage(ctx, userID), msg)
}

// translateTo looks msg up in the catalog of lang. An error text like "failed to do X: details" that
// isn't in the catalog as a whole is translated by its first part.
func translateTo(lang, msg string) string {
	catalog, ok := translations[lang]
	if !ok || msg == "" {
		return msg
	}
	if tr, ok := catalog[msg]; ok {
		return tr
	}
	if head, rest, ok := strings.Cut(msg, ": "); ok {
		if tr, ok := catalog[head]; ok {
			return tr + ": " + rest
		}
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

// tl translates a text put into rooms of this login (service messages, placeholders) into its language.
func (tc *TelegramClient) tl(msg string) string {
	return translateTo(tc.folderSpaces.language(), msg)
}

// t translates a text of the login process for the user logging in.
func (bl *baseLogin) t(msg string) string {
	return bl.main.translate(context.Background(), bl.user.MXID, msg)
}

// formatDuration formats a call or video chat duration in the language of this login.
func (tc *TelegramClient) formatDuration(d time.Duration) string {
	if tc.folderSpaces.language() != "ru" {
		return exfmt.Duration(d)
	}
	d = d.Round(time.Second)
	h, m, sec := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%d ч", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%d мин", m))
	}
	if sec > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d с", sec))
	}
	return strings.Join(parts, " ")
}

// formatDateTime formats a scheduled time in the language of this login.
func (tc *TelegramClient) formatDateTime(t time.Time) string {
	if tc.folderSpaces.language() == "ru" {
		return t.Format("02.01 15:04")
	}
	return t.Format("Jan 2, 15:04")
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
	"You have reached the maximum number of logins (%d). Please logout from an existing login before creating a new one. If you want to re-authenticate an existing login, use the `$cmdprefix relogin` command.": "Достигнуто максимальное число входов (%d). Сначала выйдите из одного из входов. Чтобы войти заново в существующий, используйте `$cmdprefix relogin`.",
	"You already have an ongoing %s. You can use `$cmdprefix cancel` to cancel it.": "У вас уже идёт %s. Отменить: `$cmdprefix cancel`.",
	"No login flows available.":                                "Нет доступных способов входа.",
	"Invalid login flow `%s`. Available options:\n\n%s":        "Неизвестный способ входа `%s`. Доступны:\n\n%s",
	"Please specify a login flow, e.g. `relogin %s %s`.\n\n%s": "Укажите способ входа, например `relogin %s %s`.\n\n%s",
	"Please specify a login flow, e.g. `login %s`.\n\n%s":      "Укажите способ входа, например `login %s`.\n\n%s",
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

	// Commands of the Matrix connector (mautrix-go bridgev2/matrix)
	"Discard the Megolm session in the room":                             "Сбросить сессию Megolm в комнате",
	"This bridge instance doesn't have end-to-bridge encryption enabled": "В этом мосте шифрование не включено",
	"Successfully reset Megolm session in this room. New decryption keys will be shared the next time a message is sent from the remote network.": "Сессия Megolm в комнате сброшена. Новые ключи будут отправлены со следующим сообщением из Telegram.",
	"Change the power level in a portal room.":                          "Изменить уровень прав в комнате чата.",
	"[_user ID_] <_power level_>":                                       "[_ID пользователя_] <_уровень прав_>",
	"Invalid power level \"%s\"":                                        "Неверный уровень прав «%s»",
	"Invalid user ID \"%s\"":                                            "Неверный ID пользователя «%s»",
	"**Usage:** `set-pl [user] <level>`":                                "**Использование:** `set-pl [пользователь] <уровень>`",
	"Failed to set power levels: %v":                                    "Не удалось изменить права: %v",
	"Enable double puppeting.":                                          "Включить double puppeting.",
	"<_access token_>":                                                  "<_токен доступа_>",
	"You don't have permission to manage double puppeting.":             "У вас нет прав управлять double puppeting.",
	"**Usage:** `login-matrix <access token>`":                          "**Использование:** `login-matrix <токен доступа>`",
	"Failed to enable double puppeting: %v":                             "Не удалось включить double puppeting: %v",
	"Successfully switched puppets":                                     "Double puppeting включён",
	"Ping the Matrix server with the double puppet.":                    "Проверить связь с сервером Matrix от имени double puppet.",
	"You don't have double puppeting enabled.":                          "У вас не включён double puppeting.",
	"Failed to validate Matrix login: %v":                               "Не удалось проверить вход в Matrix: %v",
	"Confirmed valid access token for %s (appservice double puppeting)": "Токен доступа для %s действителен (double puppeting через appservice)",
	"Confirmed valid access token for %s / %s":                          "Токен доступа для %s / %s действителен",
	"Disable double puppeting.":                                         "Выключить double puppeting.",
	"Successfully disabled double puppeting.":                           "Double puppeting выключен.",
	"Get the bridge version.":                                           "Показать версию моста.",

	// More command replies and arguments
	"[`on` | `off`]": "[`on` | `off`]",
	"<_login ID_> <_push type_> <_push token_>":                "<_ID входа_> <_тип push_> <_push-токен_>",
	"Usage: `$cmdprefix debug-account-data <type> <content>":   "Использование: `$cmdprefix debug-account-data <тип> <содержимое>",
	"<_type_> <_content_>":                                     "<_тип_> <_содержимое_>",
	"[--reset-transport]":                                      "[--reset-transport]",
	"<url>":                                                    "<url>",
	"Invalid extra parameters for display and wait login step": "Неверные дополнительные параметры для шага входа с ожиданием",
	"Invalid extra parameters for webauthn login step":         "Неверные дополнительные параметры для шага входа WebAuthn",
	"Invalid extra parameters for client HTTP login step":      "Неверные дополнительные параметры для шага входа через HTTP клиента",
	"Unsupported display type %q":                              "Неподдерживаемый тип отображения %q",
	"Failed to parse curl: %v":                                 "Не удалось разобрать curl: %v",
	"Failed to parse cookie %s: %v":                            "Не удалось разобрать cookie %s: %v",
	"Some keys can't be extracted from a cURL request: %+v\n\nPlease provide a JSON object instead.": "Некоторые ключи не удалось извлечь из запроса cURL: %+v\n\nПришлите вместо него JSON-объект.",
	"Missing some keys: %+v":         "Не хватает ключей: %+v",
	"Failed to send attachments: %v": "Не удалось отправить вложения: %v",
	"This login flow requires a client that supports client HTTP requests":                                                             "Этот способ входа требует клиента с поддержкой HTTP-запросов",
	"I don't have sufficient permissions for `m.bridge` events. Adjust power levels or use the `--ignore-permissions` flag to ignore.": "У бота не хватает прав на события `m.bridge`. Измените права или добавьте `--ignore-permissions`.",
	"Only users with relay management permissions can use sync-portal through the relay":                                               "Через ретрансляцию sync-portal могут использовать только те, кто управляет ретрансляцией",
	"[--for-everyone]":                            "[--for-everyone]",
	"[--report-spam]":                             "[--report-spam]",
	"<allow/deny> <chat ID> [receiver]":           "<allow/deny> <ID чата> [получатель]",
	"Usage: %s <allow/deny> <chat ID> [receiver]": "Использование: %s <allow/deny> <ID чата> [получатель]",
	"%s is already on the allow list":             "%s уже в списке разрешённых",
	"Added %s to allow list":                      "%s добавлен в список разрешённых",
	"%s is not on the deny list":                  "%s нет в списке запрещённых",
	"Removed %s from deny list":                   "%s убран из списка запрещённых",
	"%s is not on the allow list":                 "%s нет в списке разрешённых",
	"Removed %s from allow list":                  "%s убран из списка разрешённых",
	"%s is already on the deny list":              "%s уже в списке запрещённых",
	"Added %s to deny list":                       "%s добавлен в список запрещённых",
	"You're not logged in and there are no default relay users configured":                                                        "Вы не вошли, а ретрансляторы по умолчанию не настроены",
	"Failed to get logins in portal to find default relay":                                                                        "Не удалось получить входы в комнате, чтобы найти ретранслятор по умолчанию",
	"You're not allowed to use yourself as relay and none of the default relay users are in the chat":                             "Вам нельзя быть ретранслятором, а ретрансляторов по умолчанию в чате нет",
	"None of the default relay users are in the chat. If you want to use yourself as the relay, specify your login ID explicitly": "В чате нет ретрансляторов по умолчанию. Чтобы ретранслировать через себя, явно укажите ID своего входа",
	"You're not logged in and none of the default relay users are in the chat":                                                    "Вы не вошли, а ретрансляторов по умолчанию в чате нет",
	"In split portals, only the receiver (%s) can be set as relay":                                                                "В раздельных комнатах ретранслятором может быть только получатель (%s)",
	"User login with ID `%s` not found":                                                                                           "Вход с ID `%s` не найден",
	"Only bridge admins can set another user's login as the relay":                                                                "Назначить ретранслятором чужой вход могут только администраторы моста",
	"You're not allowed to use yourself as relay":                                                                                 "Вам нельзя быть ретранслятором",
	"Messages sent by users who haven't logged in will now be relayed through %s ([%s](%s)'s login)":                              "Сообщения пользователей без входа теперь ретранслируются через %s (вход [%s](%s))",
	"This bridge does not support %s":                                                                                             "Этот мост не поддерживает %s",
	"Identifier <code>%s</code> not found":                                                                                        "Идентификатор <code>%s</code> не найден",
	"Matrix connector doesn't support fetching room state":                                                                        "Matrix-коннектор не умеет получать состояние комнаты",
	"No group creation types defined in network capabilities":                                                                     "Мост не поддерживает создание групп",
	"Please specify type of group to create: `%s`":                                                                                "Укажите тип создаваемой группы: `%s`",
	"Successfully created group `%s`%s":                                                                                           "Группа `%s` создана%s",
	"You're not logged in and none of the default relays were found":                                                              "Вы не вошли, а ретрансляторы по умолчанию не найдены",
	"No portal found with ID %s. Try `$cmdprefix filter allow` instead":                                                           "Чат с ID %s не найден. Попробуйте `$cmdprefix filter allow`",
	"[--create] <_user ID_> <_command_> [_args..._]":                                                                              "[--create] <_ID пользователя_> <_команда_> [_аргументы..._]",
	"Usage: `$cmdprefix sudo [--create] <user ID> <command> [args...]`":                                                           "Использование: `$cmdprefix sudo [--create] <ID пользователя> <команда> [аргументы...]`",
	"Invalid user ID `%s`": "Неверный ID пользователя `%s`",
	"Failed to get user":   "Не удалось получить пользователя",
	"User not found. Use `--create` if you want to run commands as a user who has never used the bridge.": "Пользователь не найден. Чтобы выполнить команду от имени того, кто ещё не пользовался мостом, добавьте `--create`.",
	"<_room ID_> <_command_> [_args..._]":                    "<_ID комнаты_> <_команда_> [_аргументы..._]",
	"Usage: `$cmdprefix doin <room ID> <command> [args...]`": "Использование: `$cmdprefix doin <ID комнаты> <команда> [аргументы...]`",
	"Failed to check if you're in the target room":           "Не удалось проверить, есть ли вы в нужной комнате",
	"You must be in the target room to run commands there":   "Чтобы выполнять там команды, нужно быть в этой комнате",
	"Successfully logged in as %s (`%d`)":                    "Вы вошли как %s (`%d`)",
	"The code was sent to your email (%s)":                   "Код отправлен на почту (%s)",
	"The code was sent to your Fragment account (%s)":        "Код отправлен в аккаунт Fragment (%s)",

	// Errors of messages sent from Matrix
	"panic in event handler":                            "сбой при обработке события",
	"this bridge does not support edits":                "мост не поддерживает правки",
	"edits are not allowed in this chat":                "в этом чате правки запрещены",
	"captions are not supported here":                   "подписи здесь не поддерживаются",
	"location messages are not supported here":          "геопозиции здесь не поддерживаются",
	"the message is too old to be edited":               "сообщение слишком старое для правки",
	"the message has been edited too many times":        "сообщение правили слишком много раз",
	"this bridge does not support reactions":            "мост не поддерживает реакции",
	"this bridge does not support polls":                "мост не поддерживает опросы",
	"vote target poll not found":                        "опрос для голоса не найден",
	"this bridge does not support deleting messages":    "мост не поддерживает удаление сообщений",
	"unexpected parsed content type":                    "неожиданный тип содержимого",
	"internal database error":                           "внутренняя ошибка базы данных",
	"target message not found":                          "исходное сообщение не найдено",
	"unsupported message type":                          "неподдерживаемый тип сообщения",
	"unsupported media type":                            "неподдерживаемый тип медиа",
	"unsupported view-limited type":                     "неподдерживаемый тип одноразового медиа",
	"media duration too long":                           "медиа слишком длинное",
	"voice message too long":                            "голосовое сообщение слишком длинное",
	"media too large":                                   "файл слишком большой",
	"failed to download media":                          "не удалось скачать медиа",
	"failed to reupload media":                          "не удалось перезагрузить медиа",
	"failed to convert media":                           "не удалось преобразовать медиа",
	"remote echo timed out":                             "Telegram не подтвердил сообщение вовремя",
	"remote ack timed out":                              "Telegram не подтвердил сообщение вовремя",
	"public media is not enabled in the bridge config":  "публичные медиа не включены в конфиге моста",
	"failed to generate public media URL":               "не удалось создать публичную ссылку на медиа",
	"invalid disappearing timer":                        "неверный таймер исчезающих сообщений",
	"you don't have permission to send messages":        "у вас нет прав отправлять сообщения",
	"you don't have permission to use commands":         "у вас нет прав использовать команды",
	"Unsupported reaction":                              "Неподдерживаемая реакция",
	"View limit does not match the message":             "Ограничение просмотров не совпадает с сообщением",
	"Invalid view-limited media message":                "Неверное одноразовое медиа",
	"Outgoing media is not view-limited":                "Исходящее медиа не одноразовое",
	"sent as a comment":                                 "отправлено комментарием",
	"handled as a bot button press":                     "обработано как нажатие кнопки бота",
	"Processing messages from Telegram is stuck":        "Обработка сообщений из Telegram зависла",
	"An error occurred while processing an incoming %s": "Ошибка при обработке входящего события %s",
	"Set the disappearing message timer to %s":          "Таймер исчезающих сообщений: %s",
	"Room has been replaced.":                           "Комната заменена.",
	"This room has been merged":                         "Эта комната объединена с другой",

	// Telegram errors (humanise), the most common ones
	"You can't write in this chat": "Вы не можете писать в этот чат",
	"Chat admin privileges are required to do that in the specified chat (for example, to send a message in a channel which is not yours), or invalid permissions used for the channel or group": "Для этого нужны права администратора чата (например, чтобы писать в чужой канал)",
	"The channel specified is private and you lack permission to access it. Another reason may be that you were banned from it":                                                                  "Канал закрыт, и у вас нет к нему доступа (или вас в нём заблокировали)",
	"You're banned from sending messages in supergroups/channels": "Вам запрещено писать в супергруппах и каналах",
	"Message was too long":                    "Сообщение слишком длинное",
	"Empty or invalid UTF-8 message was sent": "Сообщение пустое или в неверной кодировке",
	"Content of the message was not modified": "Сообщение не изменилось",
	"The specified message ID is invalid or you can't do that operation on such message":                              "Неверный ID сообщения, или с таким сообщением это сделать нельзя",
	"You can't edit this message anymore, too much time has passed since its creation.":                               "Это сообщение уже нельзя изменить: прошло слишком много времени.",
	"You can't delete one of the messages you tried to delete, most likely because it is a service message.":          "Одно из сообщений нельзя удалить, скорее всего это служебное сообщение.",
	"The provided media object is invalid or the current account may not be able to send it (such as games as users)": "Медиа неверное, или этот аккаунт не может его отправить",
	"Media invalid":           "Неверное медиа",
	"The caption is too long": "Подпись слишком длинная",
	"An invalid Peer was used. Make sure to pass the right peer type and that the value is valid (for instance, bots cannot start conversations)": "Неверный получатель (например, боты не могут сами начинать диалог)",
	"User is blocked":       "Пользователь заблокирован",
	"You blocked this user": "Вы заблокировали этого пользователя",
	"The user's privacy settings do not allow you to do this":                              "Настройки приватности пользователя этого не разрешают",
	"You can't send media in this chat":                                                    "В этот чат нельзя отправлять медиа",
	"You can't send stickers in this chat":                                                 "В этот чат нельзя отправлять стикеры",
	"You can't send gifs in this chat":                                                     "В этот чат нельзя отправлять GIF",
	"You cannot send photos results in this chat":                                          "В этот чат нельзя отправлять фото",
	"You cannot send videos results in this Chat":                                          "В этот чат нельзя отправлять видео",
	"You cannot send voices results in this chat":                                          "В этот чат нельзя отправлять голосовые",
	"You cannot send plain results in this chat":                                           "В этот чат нельзя отправлять текст",
	"The chat is restricted and cannot be used in that request":                            "Чат ограничен, это действие в нём недоступно",
	"Invalid reaction provided (only emoji are allowed)":                                   "Неверная реакция (можно только эмодзи)",
	"A premium account is required to execute this action":                                 "Для этого нужен Telegram Premium",
	"The chat the user tried to join has expired and is not valid anymore":                 "Ссылка на чат истекла",
	"The invite hash is invalid":                                                           "Неверная ссылка-приглашение",
	"You have successfully requested to join this chat or channel":                         "Заявка на вступление отправлена",
	"The authenticated user is already a participant of the chat":                          "Вы уже участник этого чата",
	"You have joined too many channels/supergroups":                                        "Вы состоите в слишком многих каналах и супергруппах",
	"The phone code entered was invalid":                                                   "Неверный код",
	"The confirmation code has expired":                                                    "Срок действия кода истёк",
	"The phone number is invalid":                                                          "Неверный номер телефона",
	"The password (and thus its hash value) you entered is invalid":                        "Неверный пароль",
	"The key is not registered in the system":                                              "Сессия не зарегистрирована (нужно войти заново)",
	"The authorization has been invalidated, because of the user terminating all sessions": "Сессия завершена: в Telegram были закрыты все сеансы",
	"The topic was deleted":                                                                "Тема удалена",
	"The message ID used in the peer was invalid":                                          "Неверный ID сообщения",
	"The bot did not answer to the callback query in time":                                 "Бот не ответил на нажатие кнопки вовремя",
	"This user's privacy settings forbid you from sending voice messages":                  "Настройки приватности пользователя запрещают голосовые сообщения",
	"You can't send messages as the specified peer":                                        "Нельзя отправлять сообщения от имени этого отправителя",

	// Texts in rooms
	"%s are not yet supported. Open Telegram to view.": "%s пока не поддерживается. Откройте Telegram, чтобы посмотреть.",
	"Empty message":               "Пустое сообщение",
	"Forwarded message from %s%s": "Переслано от %s%s",
	"unknown user":                "неизвестного пользователя",
	"unknown chat":                "неизвестного чата",
	"unknown channel":             "неизвестного канала",
	"unknown source":              "неизвестного источника",
	"You received a view once photo. For added privacy, you can only open it on the Telegram app.": "Вам прислали одноразовое фото. Открыть его можно только в приложении Telegram.",
	"You received a view once file. For added privacy, you can only open it on the Telegram app.":  "Вам прислали одноразовый файл. Открыть его можно только в приложении Telegram.",
	"Unsupported photo message. Check Telegram app.":                                               "Неподдерживаемое фото. Посмотрите в Telegram.",
	"Unsupported document message. Check Telegram app.":                                            "Неподдерживаемый файл. Посмотрите в Telegram.",
	"Unsupported media message. Check Telegram app.":                                               "Неподдерживаемое медиа. Посмотрите в Telegram.",
	"Unsupported location message. Check Telegram app.":                                            "Неподдерживаемая геопозиция. Посмотрите в Telegram.",
	"Voice message":                  "Голосовое сообщение",
	"Shared contact info for %s: %s": "Контакт %s: %s",
	"Live Location (see your Telegram client for live updates)": "Трансляция геопозиции (обновления — в Telegram)",
	"Location": "Геопозиция",
	"Poll: %s\n%s\nOpen the Telegram app to vote.":                       "Опрос: %s\n%s\nГолосовать — в приложении Telegram.",
	"<strong>Poll</strong>: %s<ol>%s</ol>Open the Telegram app to vote.": "<strong>Опрос</strong>: %s<ol>%s</ol>Голосовать — в приложении Telegram.",
	"Game: %s. Open the Telegram app to play.":                           "Игра: %s. Играть — в приложении Telegram.",
	" Dart throw":                         " Бросок дротика",
	" Dice roll":                          " Бросок кубика",
	" Basketball throw":                   " Бросок мяча в корзину",
	" Slot machine":                       " Игровой автомат",
	" Bowling":                            " Боулинг",
	" Football kick":                      " Удар по мячу",
	" result: ":                           ", результат: ",
	"miss":                                "мимо",
	"1 pin down":                          "сбита 1 кегля",
	"3 pins down, split":                  "сбиты 3 кегли, сплит",
	"4 pins down, split":                  "сбиты 4 кегли, сплит",
	"5 pins down":                         "сбиты 5 кеглей",
	"strike 🎉":                            "страйк 🎉",
	"hit the woodwork":                    "в штангу",
	"goal":                                "гол",
	"goal 🎉":                              "гол 🎉",
	"Created the group":                   "Группа создана",
	"Video call missed":                   "Пропущенный видеозвонок",
	"Video call disconnected":             "Видеозвонок прерван",
	"Video call ended":                    "Видеозвонок завершён",
	"Video call rejected":                 "Видеозвонок отклонён",
	"Call missed":                         "Пропущенный звонок",
	"Call disconnected":                   "Звонок прерван",
	"Call ended":                          "Звонок завершён",
	"Call rejected":                       "Звонок отклонён",
	"Started a video call":                "Начат видеозвонок",
	"Started a call":                      "Начат звонок",
	"Started a video chat":                "Начат видеочат",
	"Ended the video chat (%s)":           "Видеочат завершён (%s)",
	"Video chat scheduled for %s":         "Видеочат запланирован на %s",
	"Upgraded this group to a supergroup": "Группа преобразована в супергруппу",
	"Language: `%s` (supported: %s)\n\n":  "Язык: `%s` (доступны: %s)\n\n",
	"%d rooms":                            "комнат: %d",
	"no space yet":                        "пространства ещё нет",
	"Failed to get login to handle event": "Не найден вход для обработки события",
	"sender not found for event":          "отправитель события не найден",
}
