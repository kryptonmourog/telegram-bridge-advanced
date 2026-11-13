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
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/variationselector"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/connector/emojis"
	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Matrix has no buttons, so bot keyboards are shown as a numbered list under the message. A button is
// pressed by clicking the matching number reaction, which the bridge bot puts under the message, or by
// replying to the message with the number or the button text.

type ButtonType string

const (
	// ButtonCallback sends callback data to the bot.
	ButtonCallback ButtonType = "callback"
	// ButtonText is a reply keyboard button: pressing it sends its text as a message.
	ButtonText ButtonType = "text"
	// ButtonURL opens a link (also used for web apps and login links).
	ButtonURL ButtonType = "url"
	// ButtonCopy copies a text.
	ButtonCopy ButtonType = "copy"
	// ButtonUnsupported only works in Telegram (phone, location, payments, inline mode...).
	ButtonUnsupported ButtonType = "unsupported"
)

// MessageButton is a bot keyboard button of a bridged message, stored in the message metadata.
type MessageButton struct {
	Type ButtonType `json:"type"`
	Text string     `json:"text"`
	Data []byte     `json:"data,omitempty"`
	URL  string     `json:"url,omitempty"`
	Copy string     `json:"copy,omitempty"`
	// Icon is the custom emoji document ID shown on the button, if any.
	Icon int64 `json:"icon,omitempty"`
}

// MessageButtons is the keyboard of a bridged message.
type MessageButtons struct {
	// Keyboard is set for reply keyboards (shown in place of the keyboard in Telegram), as opposed to
	// buttons attached to the message.
	Keyboard bool            `json:"keyboard,omitempty"`
	Buttons  []MessageButton `json:"buttons"`
}

// pressable reports whether the buttons need reactions: lists of only links are usable as is.
func (mb *MessageButtons) pressable() bool {
	if mb == nil {
		return false
	}
	for _, b := range mb.Buttons {
		if b.Type != ButtonURL {
			return true
		}
	}
	return false
}

// buttonReactionKeys are the reactions for the buttons, in order. Buttons past these can only be
// pressed by replying.
var buttonReactionKeys = []string{
	"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟",
	"🇦", "🇧", "🇨", "🇩", "🇪", "🇫", "🇬", "🇭", "🇮", "🇯",
}

func buttonIndexForReaction(key string) int {
	key = variationselector.Remove(key)
	for i, k := range buttonReactionKeys {
		if variationselector.Remove(k) == key {
			return i
		}
	}
	return -1
}

// buttonIndexForReply finds the button a reply means: its number or its exact text.
func buttonIndexForReply(buttons []MessageButton, reply string) int {
	reply = strings.TrimSpace(reply)
	if n, err := strconv.Atoi(reply); err == nil {
		if n >= 1 && n <= len(buttons) {
			return n - 1
		}
		return -1
	}
	for i, b := range buttons {
		if strings.EqualFold(strings.TrimSpace(b.Text), reply) {
			return i
		}
	}
	return -1
}

var buttonStrings = map[string]map[string]string{
	"en": {
		"buttons":     "Buttons",
		"keyboard":    "Reply options",
		"how":         "click the number reaction or reply with the number",
		"how_reply":   "reply with the number",
		"telegram":    "works only in Telegram",
		"copy":        "copy",
		"bot_answer":  "Bot",
		"link":        "Link",
		"copy_text":   "Text to copy",
		"only_tg":     "This button works only in the Telegram app.",
		"gone":        "This message has no button with that number any more.",
		"failed":      "Failed to press the button",
		"no_response": "The bot didn't respond to the button.",
	},
	"ru": {
		"buttons":     "Кнопки",
		"keyboard":    "Варианты ответа",
		"how":         "нажмите реакцию с номером или ответьте номером",
		"how_reply":   "ответьте на сообщение номером",
		"telegram":    "только в Telegram",
		"copy":        "копировать",
		"bot_answer":  "Бот",
		"link":        "Ссылка",
		"copy_text":   "Текст для копирования",
		"only_tg":     "Эта кнопка работает только в приложении Telegram.",
		"gone":        "У этого сообщения больше нет кнопки с таким номером.",
		"failed":      "Не удалось нажать кнопку",
		"no_response": "Бот не ответил на нажатие.",
	},
}

func buttonText(lang, key string) string {
	if s, ok := buttonStrings[lang][key]; ok {
		return s
	}
	return buttonStrings["en"][key]
}

// convertReplyMarkup converts the keyboard of a message. It returns nil if there is none.
func convertReplyMarkup(markup tg.ReplyMarkupClass) *MessageButtons {
	var rows []tg.KeyboardButtonRow
	out := &MessageButtons{}
	switch typed := markup.(type) {
	case *tg.ReplyInlineMarkup:
		rows = typed.Rows
	case *tg.ReplyKeyboardMarkup:
		rows = typed.Rows
		out.Keyboard = true
	default:
		return nil
	}
	for _, row := range rows {
		for _, button := range row.Buttons {
			mb := MessageButton{Text: button.GetText(), Type: ButtonUnsupported}
			if style, ok := button.GetStyle(); ok {
				mb.Icon = style.Icon
			}
			switch b := button.(type) {
			case *tg.KeyboardButtonCallback:
				if !b.RequiresPassword {
					mb.Type = ButtonCallback
					mb.Data = b.Data
				}
			case *tg.KeyboardButton:
				mb.Type = ButtonText
			case *tg.KeyboardButtonURL:
				mb.Type, mb.URL = ButtonURL, b.URL
			case *tg.KeyboardButtonWebView:
				mb.Type, mb.URL = ButtonURL, b.URL
			case *tg.KeyboardButtonSimpleWebView:
				mb.Type, mb.URL = ButtonURL, b.URL
			case *tg.KeyboardButtonURLAuth:
				mb.Type, mb.URL = ButtonURL, b.URL
			case *tg.KeyboardButtonCopy:
				mb.Type, mb.Copy = ButtonCopy, b.CopyText
			}
			out.Buttons = append(out.Buttons, mb)
		}
	}
	if len(out.Buttons) == 0 {
		return nil
	}
	return out
}

func (mb *MessageButtons) hashInput() []byte {
	data, _ := json.Marshal(mb)
	return data
}

// appendButtonsToContent adds the numbered button list under the text or caption of a message part.
func appendButtonsToContent(content *event.MessageEventContent, mb *MessageButtons, icons map[networkid.EmojiID]emojis.EmojiInfo, lang string) {
	switch content.MsgType {
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		// For media the body becomes the caption, so the file name has to move to its own field.
		if content.FileName == "" {
			content.FileName = content.Body
			content.Body = ""
			content.FormattedBody = ""
			content.Format = ""
		}
	}
	content.EnsureHasHTML()

	header := buttonText(lang, "buttons")
	if mb.Keyboard {
		header = buttonText(lang, "keyboard")
	}
	how := buttonText(lang, "how_reply")
	if mb.pressable() {
		how = buttonText(lang, "how")
	}

	var h, b strings.Builder
	if content.FormattedBody != "" {
		h.WriteString(content.FormattedBody)
		b.WriteString(content.Body)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&h, "<p><strong>%s</strong> <em>(%s)</em></p><ol>", html.EscapeString(header), html.EscapeString(how))
	fmt.Fprintf(&b, "%s (%s):", header, how)
	for i, button := range mb.Buttons {
		label := html.EscapeString(button.Text)
		if info, ok := icons[ids.MakeEmojiIDFromDocumentID(button.Icon)]; ok && button.Icon != 0 {
			if info.Emoji != "" {
				label = html.EscapeString(info.Emoji) + " " + label
			} else if info.EmojiURI != "" {
				label = fmt.Sprintf(`<img data-mx-emoticon src="%s" height="20" width="20" alt=""> %s`, html.EscapeString(string(info.EmojiURI)), label)
			}
		}
		plain := button.Text
		switch button.Type {
		case ButtonURL:
			label = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(button.URL), label)
			plain += ": " + button.URL
		case ButtonCopy:
			label += " <em>(" + html.EscapeString(buttonText(lang, "copy")) + ")</em>"
		case ButtonUnsupported:
			label += " <em>(" + html.EscapeString(buttonText(lang, "telegram")) + ")</em>"
			plain += " (" + buttonText(lang, "telegram") + ")"
		}
		fmt.Fprintf(&h, "<li>%s</li>", label)
		fmt.Fprintf(&b, "\n%d. %s", i+1, plain)
	}
	h.WriteString("</ol>")
	content.FormattedBody = h.String()
	content.Body = b.String()
	content.Format = event.FormatHTML
}

// addButtons converts the keyboard of a message onto its first part and returns the bytes to hash, so
// that keyboard changes edit the message.
func (tc *TelegramClient) addButtons(ctx context.Context, msg *tg.Message, part *bridgev2.ConvertedMessagePart) (*MessageButtons, []byte) {
	markup, ok := msg.GetReplyMarkup()
	if !ok || part == nil || part.Content == nil {
		return nil, nil
	}
	mb := convertReplyMarkup(markup)
	if mb == nil {
		return nil, nil
	}
	var iconIDs []int64
	for _, b := range mb.Buttons {
		if b.Icon != 0 {
			iconIDs = append(iconIDs, b.Icon)
		}
	}
	var icons map[networkid.EmojiID]emojis.EmojiInfo
	if len(iconIDs) > 0 {
		var err error
		if icons, err = tc.transferEmojisToMatrix(ctx, iconIDs); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to transfer button icons")
		}
	}
	appendButtonsToContent(part.Content, mb, icons, tc.folderSpaces.language())
	return mb, mb.hashInput()
}

// errButtonPressed tells bridgev2 that a Matrix event was handled as a button press: it's not sent to
// Telegram, and it's marked as delivered without a notice.
var errButtonPressed = bridgev2.WrapErrorInStatus(errors.New("handled as a bot button press")).
	WithStatus(event.MessageStatusSuccess).
	WithIsCertain(true).
	WithSendNotice(false)

// firstPartWithButtons returns the first part of a message, which holds its buttons.
func (tc *TelegramClient) firstPartWithButtons(ctx context.Context, portal *bridgev2.Portal, msg *database.Message) (*database.Message, *MessageButtons) {
	if msg == nil {
		return nil, nil
	}
	first := msg
	if msg.PartID != "" {
		var err error
		first, err = tc.main.Bridge.DB.Message.GetFirstPartByID(ctx, portal.Receiver, msg.ID)
		if err != nil || first == nil {
			return nil, nil
		}
	}
	meta, ok := first.Metadata.(*MessageMetadata)
	if !ok || meta.Buttons == nil || len(meta.Buttons.Buttons) == 0 {
		return nil, nil
	}
	return first, meta.Buttons
}

// maybePressButtonByReply handles a reply with a button number or text to a message with buttons.
// Reply keyboard buttons are sent as their text, so the message is rewritten and sent normally.
func (tc *TelegramClient) maybePressButtonByReply(ctx context.Context, msg *bridgev2.MatrixMessage) (handled bool, err error) {
	if msg.ReplyTo == nil || msg.Content.MsgType != event.MsgText {
		return false, nil
	}
	target, mb := tc.firstPartWithButtons(ctx, msg.Portal, msg.ReplyTo)
	if mb == nil {
		return false, nil
	}
	idx := buttonIndexForReply(mb.Buttons, event.TrimReplyFallbackText(msg.Content.Body))
	if idx < 0 {
		return false, nil
	}
	button := mb.Buttons[idx]
	if button.Type == ButtonText {
		msg.Content.Body = button.Text
		msg.Content.FormattedBody = ""
		msg.Content.Format = ""
		msg.ReplyTo = nil
		return false, nil
	}
	tc.pressButton(ctx, msg.Portal, target, button)
	return true, errButtonPressed
}

// maybePressButtonByReaction handles a number reaction to a message with buttons. The reaction is
// removed afterwards, so that the same button can be pressed again.
func (tc *TelegramClient) maybePressButtonByReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (handled bool, err error) {
	target, mb := tc.firstPartWithButtons(ctx, msg.Portal, msg.TargetMessage)
	if mb == nil || !mb.pressable() {
		return false, nil
	}
	idx := buttonIndexForReaction(msg.Content.RelatesTo.Key)
	if idx < 0 {
		return false, nil
	}
	go tc.redactAsBot(context.WithoutCancel(ctx), msg.Portal.MXID, msg.Event.ID, "button pressed")
	if idx >= len(mb.Buttons) {
		tc.sendButtonNotice(ctx, msg.Portal, buttonText(tc.folderSpaces.language(), "gone"), "")
		return true, errButtonPressed
	}
	button := mb.Buttons[idx]
	if button.Type == ButtonText {
		if err = tc.sendButtonText(ctx, msg.Portal, button.Text); err != nil {
			tc.sendButtonNotice(ctx, msg.Portal, buttonText(tc.folderSpaces.language(), "failed")+": "+err.Error(), "")
		}
		return true, errButtonPressed
	}
	tc.pressButton(ctx, msg.Portal, target, button)
	return true, errButtonPressed
}

// pressButton does what pressing a non-text button does and shows the result as a notice.
func (tc *TelegramClient) pressButton(ctx context.Context, portal *bridgev2.Portal, target *database.Message, button MessageButton) {
	lang := tc.folderSpaces.language()
	log := zerolog.Ctx(ctx).With().Str("button_type", string(button.Type)).Logger()
	switch button.Type {
	case ButtonURL:
		tc.sendButtonNotice(ctx, portal, buttonText(lang, "link")+": "+button.URL,
			fmt.Sprintf(`%s: <a href="%s">%s</a>`, html.EscapeString(buttonText(lang, "link")), html.EscapeString(button.URL), html.EscapeString(button.Text)))
	case ButtonCopy:
		tc.sendButtonNotice(ctx, portal, buttonText(lang, "copy_text")+": "+button.Copy,
			fmt.Sprintf("%s: <code>%s</code>", html.EscapeString(buttonText(lang, "copy_text")), html.EscapeString(button.Copy)))
	case ButtonCallback:
		answer, err := tc.pressCallbackButton(ctx, portal, target, button)
		if err != nil {
			log.Err(err).Msg("Failed to press callback button")
			if !tg.IsBotResponseTimeout(err) {
				tc.sendButtonNotice(ctx, portal, buttonText(lang, "failed")+": "+err.Error(), "")
			}
			return
		}
		if answer.Message != "" {
			tc.sendButtonNotice(ctx, portal, buttonText(lang, "bot_answer")+": "+answer.Message, "")
		}
		if answer.URL != "" {
			tc.sendButtonNotice(ctx, portal, buttonText(lang, "link")+": "+answer.URL, "")
		}
	default:
		tc.sendButtonNotice(ctx, portal, buttonText(lang, "only_tg"), "")
	}
}

func (tc *TelegramClient) pressCallbackButton(ctx context.Context, portal *bridgev2.Portal, target *database.Message, button MessageButton) (*tg.MessagesBotCallbackAnswer, error) {
	peer, _, err := tc.inputPeerForPortalID(ctx, portal.ID)
	if err != nil {
		return nil, err
	}
	_, msgID, err := ids.ParseMessageID(target.ID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return tc.client.API().MessagesGetBotCallbackAnswer(ctx, &tg.MessagesGetBotCallbackAnswerRequest{
		Peer:  peer,
		MsgID: msgID,
		Data:  button.Data,
	})
}

// sendButtonText sends the text of a reply keyboard button like the Telegram app does. Telegram
// echoes it back, so it shows up in Matrix as a normal message.
func (tc *TelegramClient) sendButtonText(ctx context.Context, portal *bridgev2.Portal, text string) error {
	peer, _, err := tc.inputPeerForPortalID(ctx, portal.ID)
	if err != nil {
		return err
	}
	var randomID [8]byte
	_, _ = rand.Read(randomID[:])
	_, err = tc.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: int64(binary.LittleEndian.Uint64(randomID[:])),
	})
	return err
}

func (tc *TelegramClient) sendButtonNotice(ctx context.Context, portal *bridgev2.Portal, body, formatted string) {
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: body}
	if formatted != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = formatted
	}
	_, err := tc.main.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, &bridgev2.MatrixSendExtra{Timestamp: time.Now()})
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send button notice")
	}
}

func (tc *TelegramClient) redactAsBot(ctx context.Context, room id.RoomID, evt id.EventID, reason string) {
	_, err := tc.main.Bridge.Bot.SendMessage(ctx, room, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{Redacts: evt, Reason: reason},
	}, &bridgev2.MatrixSendExtra{Timestamp: time.Now()})
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Stringer("event_id", evt).Msg("Failed to redact event")
	}
}

// syncButtonReactions puts the number reactions under a message with pressable buttons, and removes
// the ones that no longer have a button after the keyboard changed. It runs after the message or its
// edit has been sent to Matrix.
func (tc *TelegramClient) syncButtonReactions(ctx context.Context, portal *bridgev2.Portal, msgID networkid.MessageID) {
	if portal.MXID == "" {
		return
	}
	log := zerolog.Ctx(ctx)
	first, err := tc.main.Bridge.DB.Message.GetFirstPartByID(ctx, portal.Receiver, msgID)
	if err != nil || first == nil || first.MXID == "" {
		return
	}
	meta, ok := first.Metadata.(*MessageMetadata)
	if !ok {
		return
	}
	want := 0
	if meta.Buttons.pressable() {
		want = min(len(meta.Buttons.Buttons), len(buttonReactionKeys))
	}
	if want == 0 && len(meta.ButtonReactions) == 0 {
		return
	}
	changed := false
	for key, reactionID := range meta.ButtonReactions {
		if idx := buttonIndexForReaction(key); idx < 0 || idx >= want {
			tc.redactAsBot(ctx, portal.MXID, reactionID, "button removed")
			delete(meta.ButtonReactions, key)
			changed = true
		}
	}
	for i := 0; i < want; i++ {
		key := buttonReactionKeys[i]
		if _, ok := meta.ButtonReactions[key]; ok {
			continue
		}
		resp, err := tc.main.Bridge.Bot.SendMessage(ctx, portal.MXID, event.EventReaction, &event.Content{
			Parsed: &event.ReactionEventContent{RelatesTo: event.RelatesTo{
				Type:    event.RelAnnotation,
				EventID: first.MXID,
				Key:     key,
			}},
		}, &bridgev2.MatrixSendExtra{Timestamp: time.Now()})
		if err != nil {
			log.Err(err).Str("key", key).Msg("Failed to add button reaction")
			break
		}
		if meta.ButtonReactions == nil {
			meta.ButtonReactions = make(map[string]id.EventID)
		}
		meta.ButtonReactions[key] = resp.EventID
		changed = true
	}
	if changed {
		if err = tc.main.Bridge.DB.Message.Update(ctx, first); err != nil {
			log.Err(err).Msg("Failed to save button reactions")
		}
	}
}
