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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

func testInlineMarkup() *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCallback{Text: "Free projects", Data: []byte("free")},
			&tg.KeyboardButtonURL{Text: "Site", URL: "https://example.com/?a=1&b=2"},
		}},
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCopy{Text: "Promo", CopyText: "START"},
			&tg.KeyboardButtonRequestPhone{Text: "Share phone"},
			&tg.KeyboardButtonCallback{Text: "Secret", Data: []byte("x"), RequiresPassword: true},
		}},
	}}
}

func TestConvertReplyMarkup(t *testing.T) {
	mb := convertReplyMarkup(testInlineMarkup())
	require.NotNil(t, mb)
	assert.False(t, mb.Keyboard)
	assert.Equal(t, []MessageButton{
		{Type: ButtonCallback, Text: "Free projects", Data: []byte("free")},
		{Type: ButtonURL, Text: "Site", URL: "https://example.com/?a=1&b=2"},
		{Type: ButtonCopy, Text: "Promo", Copy: "START"},
		{Type: ButtonUnsupported, Text: "Share phone"},
		{Type: ButtonUnsupported, Text: "Secret"},
	}, mb.Buttons)
	assert.True(t, mb.pressable())

	kb := convertReplyMarkup(&tg.ReplyKeyboardMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: "Yes"}}}}})
	require.NotNil(t, kb)
	assert.True(t, kb.Keyboard)
	assert.Equal(t, ButtonText, kb.Buttons[0].Type)

	links := convertReplyMarkup(&tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonURL{Text: "Read", URL: "https://x"}}}}})
	assert.False(t, links.pressable(), "links only don't need reactions")

	assert.Nil(t, convertReplyMarkup(&tg.ReplyKeyboardHide{}))
	assert.Nil(t, convertReplyMarkup(&tg.ReplyInlineMarkup{}))
}

func TestButtonIndex(t *testing.T) {
	buttons := convertReplyMarkup(testInlineMarkup()).Buttons
	assert.Equal(t, 1, buttonIndexForReply(buttons, " 2 "))
	assert.Equal(t, -1, buttonIndexForReply(buttons, "6"))
	assert.Equal(t, -1, buttonIndexForReply(buttons, "0"))
	assert.Equal(t, 0, buttonIndexForReply(buttons, "free PROJECTS"))
	assert.Equal(t, -1, buttonIndexForReply(buttons, "hello"))

	assert.Equal(t, 0, buttonIndexForReaction("1️⃣"))
	assert.Equal(t, 0, buttonIndexForReaction("1⃣"), "without the variation selector")
	assert.Equal(t, 9, buttonIndexForReaction("🔟"))
	assert.Equal(t, 10, buttonIndexForReaction("🇦"))
	assert.Equal(t, -1, buttonIndexForReaction("👍"))
}

func TestAppendButtonsToContent(t *testing.T) {
	mb := convertReplyMarkup(testInlineMarkup())
	content := &event.MessageEventContent{MsgType: event.MsgText, Body: "Welcome"}
	appendButtonsToContent(content, mb, nil, "ru")
	assert.Equal(t, event.FormatHTML, content.Format)
	assert.Contains(t, content.FormattedBody, "Welcome<p><strong>Кнопки</strong> <em>(нажмите реакцию с номером или ответьте номером)</em></p><ol>")
	assert.Contains(t, content.FormattedBody, "<li>Free projects</li>")
	assert.Contains(t, content.FormattedBody, `<li><a href="https://example.com/?a=1&amp;b=2">Site</a></li>`)
	assert.Contains(t, content.FormattedBody, "<li>Promo <em>(копировать)</em></li>")
	assert.Contains(t, content.FormattedBody, "<li>Share phone <em>(только в Telegram)</em></li>")
	assert.Contains(t, content.Body, "Welcome\n\nКнопки (нажмите реакцию с номером или ответьте номером):\n1. Free projects\n2. Site: https://example.com/?a=1&b=2\n")

	photo := &event.MessageEventContent{MsgType: event.MsgImage, Body: "image.jpg"}
	appendButtonsToContent(photo, mb, nil, "en")
	assert.Equal(t, "image.jpg", photo.FileName, "file name is kept when the buttons become the caption")
	assert.Contains(t, photo.Body, "Buttons (click the number reaction or reply with the number):\n1. Free projects")
	assert.NotContains(t, photo.Body, "image.jpg")
}
