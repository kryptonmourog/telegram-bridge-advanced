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
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Sponsored messages are shown in one of three ways (`ads on|room|off`):
//   - room (default): all ads go to a separate "Telegram Ads" room, which is muted and low priority,
//     so channel rooms stay clean. An ad counts as viewed when the user reads that room.
//   - on: in the channel room, as upstream does. Viewed when the user reads the channel.
//   - off: not requested at all.
//
// Premium users get no sponsored messages from Telegram, so the room is only created with the first ad.

const (
	AdsModeRoom = "room"
	AdsModeOn   = "on"
	AdsModeOff  = "off"
)

// adViewWindow is how long after it was fetched an ad can still be reported as viewed.
const adViewWindow = time.Hour

func (tc *TelegramClient) adsMode() string {
	switch tc.metadata.AdsMode {
	case AdsModeRoom, AdsModeOn, AdsModeOff:
		return tc.metadata.AdsMode
	}
	if tc.metadata.AdsDisabled {
		return AdsModeOff
	}
	return AdsModeRoom
}

type pendingAdView struct {
	eventID  id.EventID
	randomID []byte
	sentAt   time.Time
}

// adsRoomState tracks ads sent to the ads room that weren't reported as viewed yet.
type adsRoomState struct {
	lock    sync.Mutex
	pending []pendingAdView
	// createLock serializes creating the room.
	createLock sync.Mutex
}

var adsRoomStrings = map[string]map[string]string{
	"en": {
		"name":    "Telegram Ads",
		"topic":   "Sponsored messages Telegram shows in your channels. The bridge puts them here instead of the channels.",
		"channel": "In channel",
	},
	"ru": {
		"name":    "Реклама Telegram",
		"topic":   "Реклама, которую Telegram показывает в ваших каналах. Мост собирает её здесь, а не в самих каналах.",
		"channel": "В канале",
	},
}

func adsRoomText(lang, key string) string {
	if s, ok := adsRoomStrings[lang][key]; ok {
		return s
	}
	return adsRoomStrings["en"][key]
}

// ensureAdsRoom returns the ads room, creating it (muted and low priority for the user) if needed.
func (tc *TelegramClient) ensureAdsRoom(ctx context.Context) (id.RoomID, error) {
	tc.adsRoom.createLock.Lock()
	defer tc.adsRoom.createLock.Unlock()
	if tc.metadata.AdsRoom != "" {
		return tc.metadata.AdsRoom, nil
	}
	lang := tc.folderSpaces.language()
	ghost, err := tc.main.Bridge.GetGhostByID(ctx, sponsoredGhostID)
	if err != nil {
		return "", fmt.Errorf("failed to get sponsored message ghost: %w", err)
	}
	ghost.UpdateName(ctx, sponsoredText(lang, "sender"))
	userMXID := tc.userLogin.UserMXID
	room, err := tc.main.Bridge.Bot.CreateRoom(ctx, &mautrix.ReqCreateRoom{
		Name:     adsRoomText(lang, "name"),
		Topic:    adsRoomText(lang, "topic"),
		Preset:   "private_chat",
		IsDirect: false,
		Invite:   []id.UserID{userMXID, ghost.Intent.GetMXID()},
		PowerLevelOverride: &event.PowerLevelsEventContent{
			Users: map[id.UserID]int{
				tc.main.Bridge.Bot.GetMXID(): 100,
				ghost.Intent.GetMXID():       50,
			},
			EventsDefault: 50,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create ads room: %w", err)
	}
	if err = ghost.Intent.EnsureJoined(ctx, room); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to join sponsored message ghost to ads room")
	}
	if dp := tc.userLogin.User.DoublePuppet(ctx); dp != nil {
		if err = dp.EnsureJoined(ctx, room); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to join user to ads room")
		}
		if err = dp.MuteRoom(ctx, room, event.MutedForever); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to mute ads room")
		}
		if err = dp.TagRoom(ctx, room, event.RoomTagLowPriority, true); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to mark ads room as low priority")
		}
	}
	tc.metadata.AdsRoom = room
	if err = tc.userLogin.Save(ctx); err != nil {
		return room, fmt.Errorf("failed to save ads room: %w", err)
	}
	zerolog.Ctx(ctx).Info().Stringer("room_id", room).Msg("Created ads room")
	return room, nil
}

func (tc *TelegramClient) trackAdForView(eventID id.EventID, randomID []byte) {
	tc.adsRoom.lock.Lock()
	defer tc.adsRoom.lock.Unlock()
	cutoff := time.Now().Add(-adViewWindow)
	kept := tc.adsRoom.pending[:0]
	for _, p := range tc.adsRoom.pending {
		if p.sentAt.After(cutoff) {
			kept = append(kept, p)
		}
	}
	tc.adsRoom.pending = append(kept, pendingAdView{eventID: eventID, randomID: randomID, sentAt: time.Now()})
}

// markAdsViewed reports the ads of the ads room the user has read (sent before the read receipt or
// the receipt's own event) as viewed.
func (tc *TelegramClient) markAdsViewed(ctx context.Context, readEvent id.EventID, readAt time.Time) {
	tc.adsRoom.lock.Lock()
	var viewed []pendingAdView
	kept := tc.adsRoom.pending[:0]
	for _, p := range tc.adsRoom.pending {
		if p.eventID == readEvent || !p.sentAt.After(readAt) {
			if time.Since(p.sentAt) < adViewWindow {
				viewed = append(viewed, p)
			}
			continue
		}
		kept = append(kept, p)
	}
	tc.adsRoom.pending = kept
	tc.adsRoom.lock.Unlock()
	log := zerolog.Ctx(ctx)
	for _, p := range viewed {
		if _, err := tc.client.API().MessagesViewSponsoredMessage(ctx, p.randomID); err != nil {
			log.Err(err).Msg("Failed to mark sponsored message as viewed")
		} else {
			log.Debug().Str("random_id", base64.StdEncoding.EncodeToString(p.randomID)).Msg("Marked sponsored message in ads room as viewed")
		}
	}
}

// handleAdsRoomReceipt is registered for read receipts of all rooms; the ads room isn't a portal,
// so bridgev2 doesn't pass its receipts to the network connector.
func (tc *TelegramConnector) handleAdsRoomReceipt(ctx context.Context, evt *event.Event) {
	content, ok := evt.Content.Parsed.(*event.ReceiptEventContent)
	if !ok {
		return
	}
	for _, login := range tc.Bridge.GetAllCachedUserLogins() {
		client, ok := login.Client.(*TelegramClient)
		if !ok || client.metadata.AdsRoom != evt.RoomID || client.adsMode() != AdsModeRoom {
			continue
		}
		for eventID, receipts := range *content {
			for receiptType, users := range receipts {
				if receiptType != event.ReceiptTypeRead && receiptType != event.ReceiptTypeReadPrivate {
					continue
				}
				if receipt, ok := users[login.UserMXID]; ok {
					go client.markAdsViewed(context.WithoutCancel(ctx), eventID, receipt.Timestamp)
				}
			}
		}
	}
}

func (tc *TelegramConnector) registerAdsRoomReceipts() {
	if mc, ok := tc.Bridge.Matrix.(*matrix.Connector); ok && mc.EventProcessor != nil {
		mc.EventProcessor.On(event.EphemeralEventReceipt, tc.handleAdsRoomReceipt)
	}
}
