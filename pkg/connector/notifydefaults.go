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
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Telegram has default notification settings for all private chats, all groups and all channels
// ("Notifications and Sounds"). A chat without its own setting follows the default of its kind, but
// the bridge treated such chats as unmuted, so e.g. channels muted by the "all channels" switch
// notified in Matrix.

type notifyKind int

const (
	notifyUsers notifyKind = iota
	notifyChats
	notifyBroadcasts
)

type notifyDefaults struct {
	lock    sync.RWMutex
	fetched bool
	muted   map[notifyKind]time.Time
}

func (nd *notifyDefaults) get(kind notifyKind) (time.Time, bool) {
	nd.lock.RLock()
	defer nd.lock.RUnlock()
	until, ok := nd.muted[kind]
	return until, ok && nd.fetched
}

func (nd *notifyDefaults) set(kind notifyKind, until time.Time) {
	nd.lock.Lock()
	defer nd.lock.Unlock()
	if nd.muted == nil {
		nd.muted = make(map[notifyKind]time.Time)
	}
	nd.muted[kind] = until
}

func muteUntilFromSettings(settings tg.PeerNotifySettings) (time.Time, bool) {
	if mu, ok := settings.GetMuteUntil(); ok {
		return time.Unix(int64(mu), 0), true
	}
	return time.Time{}, false
}

// fetchNotifyDefaults gets the default notification settings of the three kinds of chats.
func (tc *TelegramClient) fetchNotifyDefaults(ctx context.Context) {
	peers := map[notifyKind]tg.InputNotifyPeerClass{
		notifyUsers:      &tg.InputNotifyUsers{},
		notifyChats:      &tg.InputNotifyChats{},
		notifyBroadcasts: &tg.InputNotifyBroadcasts{},
	}
	for kind, peer := range peers {
		settings, err := tc.client.API().AccountGetNotifySettings(ctx, peer)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Int("kind", int(kind)).Msg("Failed to get default notification settings")
			return
		}
		until, _ := muteUntilFromSettings(*settings)
		tc.notifyDefaults.set(kind, until)
	}
	tc.notifyDefaults.lock.Lock()
	tc.notifyDefaults.fetched = true
	tc.notifyDefaults.lock.Unlock()
}

// notifyKindOfPeer tells which default applies to a chat; chats is used to tell channels from groups.
func notifyKindOfPeer(peer tg.PeerClass, chats map[int64]tg.ChatClass) notifyKind {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return notifyUsers
	case *tg.PeerChannel:
		if channel, ok := chats[p.ChannelID].(*tg.Channel); ok && channel.Broadcast {
			return notifyBroadcasts
		}
	}
	return notifyChats
}

// notifyKindOfPortal is notifyKindOfPeer for an existing portal.
func notifyKindOfPortal(portal *bridgev2.Portal) notifyKind {
	peerType, _, _, err := ids.ParsePortalID(portal.ID)
	if err != nil {
		return notifyChats
	}
	switch peerType {
	case ids.PeerTypeUser:
		return notifyUsers
	case ids.PeerTypeChannel:
		if meta, ok := portal.Metadata.(*PortalMetadata); ok && !meta.IsSuperGroup {
			return notifyBroadcasts
		}
	}
	return notifyChats
}

// effectiveMute returns when a chat is muted until: its own setting, or the default of its kind.
func (tc *TelegramClient) effectiveMute(settings tg.PeerNotifySettings, kind notifyKind) *time.Time {
	if until, ok := muteUntilFromSettings(settings); ok {
		return &until
	}
	if until, ok := tc.notifyDefaults.get(kind); ok && until.After(time.Now()) {
		return &until
	}
	return &bridgev2.Unmuted
}

// onDefaultNotifySettings handles a change of a default: chats are synced again so that the ones
// without their own setting follow it.
func (tc *TelegramClient) onDefaultNotifySettings(ctx context.Context, kind notifyKind, settings tg.PeerNotifySettings) {
	until, _ := muteUntilFromSettings(settings)
	tc.notifyDefaults.set(kind, until)
	zerolog.Ctx(ctx).Info().Int("kind", int(kind)).Time("muted_until", until).Msg("Default notification settings changed, syncing chats")
	go func() {
		if err := tc.syncChats(context.WithoutCancel(ctx), 0, false, true); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to sync chats after default notification settings changed")
		}
	}()
}

// muteForPortalUpdate is the mute of a chat whose own setting changed (or was reset to the default).
func (tc *TelegramClient) muteForPortalUpdate(ctx context.Context, key networkid.PortalKey, settings tg.PeerNotifySettings) *time.Time {
	if until, ok := muteUntilFromSettings(settings); ok {
		return ptr.Ptr(until)
	}
	kind := notifyChats
	if portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, key); err == nil && portal != nil {
		kind = notifyKindOfPortal(portal)
	}
	return tc.effectiveMute(settings, kind)
}

// topicMute is when a forum topic is muted until: its own setting, else the forum's own setting,
// else the default for groups. Topic rooms had no mute at all, so every topic notified in Matrix.
func (tc *TelegramClient) topicMute(ctx context.Context, forum tg.InputPeerClass, topicSettings tg.PeerNotifySettings) *time.Time {
	if until, ok := muteUntilFromSettings(topicSettings); ok {
		return &until
	}
	forumSettings, err := tc.client.API().AccountGetNotifySettings(ctx, &tg.InputNotifyPeer{Peer: forum})
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get notification settings of forum")
		return tc.effectiveMute(tg.PeerNotifySettings{}, notifyChats)
	}
	return tc.effectiveMute(*forumSettings, notifyChats)
}
