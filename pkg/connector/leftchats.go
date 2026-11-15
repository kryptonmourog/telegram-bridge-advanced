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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
)

// A chat the user leaves in Telegram is removed from Matrix when its leave update arrives. Leaves
// that happen while the bridge is down never arrive, and chat sync only sees the chats the user is
// still in, so their rooms stayed. A chat sync that goes through the whole dialog list in one run
// finds them: rooms of chats that aren't in the list are reported to the user, who can remove them
// with `left-chats remove`.

// seenDialogs collects the chats of one complete chat sync.
type seenDialogs struct {
	lock  sync.Mutex
	peers map[networkid.PortalID]struct{}
}

func (sd *seenDialogs) start() {
	sd.lock.Lock()
	defer sd.lock.Unlock()
	sd.peers = make(map[networkid.PortalID]struct{})
}

func (sd *seenDialogs) add(key networkid.PortalKey) {
	sd.lock.Lock()
	defer sd.lock.Unlock()
	if sd.peers != nil {
		sd.peers[key.ID] = struct{}{}
	}
}

// finish returns the collected chats and stops collecting; nil if no collection was running.
func (sd *seenDialogs) finish() map[networkid.PortalID]struct{} {
	sd.lock.Lock()
	defer sd.lock.Unlock()
	peers := sd.peers
	sd.peers = nil
	return peers
}

// findLeftChats compares the rooms of the login with the chats seen in a complete chat sync and
// remembers the ones the user isn't in anymore, telling the user about new ones.
func (tc *TelegramClient) findLeftChats(ctx context.Context, seen map[networkid.PortalID]struct{}) {
	log := zerolog.Ctx(ctx)
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, tc.userLogin.UserLogin)
	if err != nil {
		log.Err(err).Msg("Failed to get portals to find left chats")
		return
	}
	var left []networkid.PortalID
	for _, up := range userPortals {
		_, _, topicID, err := ids.ParsePortalID(up.Portal.ID)
		if err != nil || topicID != 0 {
			// Topics and forum spaces go with their forum.
			continue
		}
		if _, ok := seen[up.Portal.ID]; ok {
			continue
		}
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		left = append(left, portal.ID)
	}
	slices.Sort(left)
	newOnes := 0
	for _, id := range left {
		if !slices.Contains(tc.metadata.LeftChats, id) {
			newOnes++
		}
	}
	tc.metadata.LeftChats = left
	if err = tc.userLogin.Save(ctx); err != nil {
		log.Err(err).Msg("Failed to save left chats")
	}
	log.Info().Int("left_chats", len(left)).Int("new", newOnes).Msg("Compared rooms with Telegram's chat list")
	if newOnes > 0 {
		tc.sendManagementNotice(ctx, fmt.Sprintf(
			"Found %d chats that have a room in Matrix but that you're no longer in on Telegram "+
				"(probably left while the bridge was offline).\n\n%s\n\nUse `left-chats remove <number>` or `left-chats remove all` to remove their rooms.",
			len(left), tc.describeLeftChats(ctx)))
	}
}

func (tc *TelegramClient) describeLeftChats(ctx context.Context) string {
	var sb strings.Builder
	for i, portalID := range tc.metadata.LeftChats {
		name := string(portalID)
		if peerType, chatID, _, err := ids.ParsePortalID(portalID); err == nil {
			if portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, tc.makePortalKeyFromID(peerType, chatID, 0)); err == nil && portal != nil && portal.Name != "" {
				name = portal.Name
			}
		}
		fmt.Fprintf(&sb, "%d. %s\n", i+1, name)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// removeLeftChat removes the room of a chat the user left, the way a leave update from Telegram does.
func (tc *TelegramClient) removeLeftChat(ctx context.Context, portalID networkid.PortalID) error {
	peerType, chatID, _, err := ids.ParsePortalID(portalID)
	if err != nil {
		return err
	}
	return tc.selfLeaveChat(ctx, tc.makePortalKeyFromID(peerType, chatID, 0), fmt.Errorf("not in the chat list on Telegram"))
}

func (tc *TelegramClient) sendManagementNotice(ctx context.Context, text string) {
	room := tc.userLogin.User.ManagementRoom
	if room == "" {
		return
	}
	content := &event.MessageEventContent{MsgType: event.MsgNotice, Body: text}
	if _, err := tc.main.Bridge.Bot.SendMessage(ctx, room, event.EventMessage, &event.Content{Parsed: content},
		&bridgev2.MatrixSendExtra{Timestamp: time.Now()}); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send notice to management room")
	}
}
