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
	"html"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Telegram's API terms require apps that show channels to show official sponsored messages, so they
// are kept, but sent by a dedicated "Telegram Ads" ghost and clearly labelled, so that nobody takes
// them for something the bridge or homeserver admin added.

// sponsoredGhostID is the ghost that sends sponsored messages. Telegram user IDs are numeric,
// so it can't collide with a real user's ghost.
const sponsoredGhostID networkid.UserID = "sponsored"

var sponsoredStrings = map[string]map[string]string{
	"en": {
		"sender":      "Telegram Ads",
		"header":      "Ad from Telegram",
		"recommended": "Recommended by Telegram",
		"advertiser":  "Advertiser",
		"notice":      "This ad is shown by Telegram itself in channels (official Telegram sponsored messages). The administrator of this Matrix server has nothing to do with it.",
	},
	"ru": {
		"sender":      "Реклама Telegram",
		"header":      "Реклама от Telegram",
		"recommended": "Рекомендация Telegram",
		"advertiser":  "Рекламодатель",
		"notice":      "Это объявление показывает сам Telegram в каналах (официальная реклама Telegram). Администратор этого сервера Matrix к нему не причастен.",
	},
}

func sponsoredText(lang, key string) string {
	if s, ok := sponsoredStrings[lang][key]; ok {
		return s
	}
	return sponsoredStrings["en"][key]
}

// formatSponsoredMessage replaces the body of an already converted ad with the labelled layout:
// header, title and text, button link, advertiser info and a notice that Telegram shows the ad.
func formatSponsoredMessage(lang string, msg *tg.SponsoredMessage, content *event.MessageEventContent) {
	header := sponsoredText(lang, "header")
	if msg.Recommended {
		header = sponsoredText(lang, "recommended")
	}
	var advertiser []string
	for _, s := range []string{msg.SponsorInfo, msg.AdditionalInfo} {
		if s = strings.TrimSpace(s); s != "" {
			advertiser = append(advertiser, s)
		}
	}
	buttonText := strings.TrimSpace(msg.ButtonText)
	if buttonText == "" {
		buttonText = msg.URL
	}

	var h, b strings.Builder
	fmt.Fprintf(&h, "<p><strong>%s</strong></p><blockquote>", html.EscapeString(header))
	fmt.Fprintf(&b, "%s\n", header)
	if msg.Title != "" {
		fmt.Fprintf(&h, "<strong>%s</strong><br>", html.EscapeString(msg.Title))
		fmt.Fprintf(&b, "> %s\n", msg.Title)
	}
	h.WriteString(content.FormattedBody)
	h.WriteString("</blockquote>")
	for _, line := range strings.Split(content.Body, "\n") {
		fmt.Fprintf(&b, "> %s\n", line)
	}
	if msg.URL != "" {
		fmt.Fprintf(&h, `<p><a href="%s">%s</a></p>`, html.EscapeString(msg.URL), html.EscapeString(buttonText))
		if buttonText != msg.URL {
			fmt.Fprintf(&b, "\n%s: %s\n", buttonText, msg.URL)
		} else {
			fmt.Fprintf(&b, "\n%s\n", msg.URL)
		}
	}
	if len(advertiser) > 0 {
		label := sponsoredText(lang, "advertiser")
		escaped := make([]string, len(advertiser))
		for i, s := range advertiser {
			escaped[i] = html.EscapeString(s)
		}
		fmt.Fprintf(&h, "<p>%s: %s</p>", html.EscapeString(label), strings.Join(escaped, "<br>"))
		fmt.Fprintf(&b, "%s: %s\n", label, strings.Join(advertiser, "; "))
	}
	notice := sponsoredText(lang, "notice")
	fmt.Fprintf(&h, "<p><em>%s</em></p>", html.EscapeString(notice))
	fmt.Fprintf(&b, "\n%s", notice)

	content.Body = b.String()
	content.FormattedBody = h.String()
	content.Format = event.FormatHTML
}

// sponsoredMessageIntent returns the "Telegram Ads" ghost, joined to the room and allowed to post
// there. Channel rooms only let privileged members send messages, and member syncs may reset the
// power levels, so the level is checked every time.
func (tc *TelegramClient) sponsoredMessageIntent(ctx context.Context, room id.RoomID) (bridgev2.MatrixAPI, error) {
	ghost, err := tc.main.Bridge.GetGhostByID(ctx, sponsoredGhostID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sponsored message ghost: %w", err)
	}
	ghost.UpdateName(ctx, sponsoredText(tc.main.Config.FolderSpaces.DefaultLanguage, "sender"))

	bot, ok := tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok {
		return nil, fmt.Errorf("bridge bot is %T, can't check power levels", tc.main.Bridge.Bot)
	}
	var pl event.PowerLevelsEventContent
	if err = bot.Matrix.StateEvent(ctx, room, event.StatePowerLevels, "", &pl); err != nil {
		return nil, fmt.Errorf("failed to get power levels: %w", err)
	}
	ghostMXID := ghost.Intent.GetMXID()
	if need := pl.GetEventLevel(event.EventMessage); pl.GetUserLevel(ghostMXID) < need {
		pl.SetUserLevel(ghostMXID, need)
		if _, err = tc.main.Bridge.Bot.SendState(ctx, room, event.StatePowerLevels, "", &event.Content{Parsed: &pl}, time.Now()); err != nil {
			return nil, fmt.Errorf("failed to let sponsored message ghost post: %w", err)
		}
	}
	if err = ghost.Intent.EnsureJoined(ctx, room); err != nil {
		return nil, fmt.Errorf("failed to join sponsored message ghost: %w", err)
	}
	return ghost.Intent, nil
}
