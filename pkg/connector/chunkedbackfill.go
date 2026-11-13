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
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// bridgev2 converts all messages of a forward backfill before sending any of them, so a room whose
// recent history has large videos stays empty until every one of them is uploaded. With
// backfill_split_media_mb set, forward backfill is split into chunks that end at large files: each
// chunk is sent as soon as it's converted, and a chat resync then backfills the rest.
//
// bridgev2 drops backfilled messages older than the newest bridged one, so a new message bridged
// between chunks would cut off the rest of the history. New messages of a room are therefore held
// while its chunked backfill runs and released after the last chunk (backfill already includes them,
// and bridgev2 skips the duplicates).

// chunkedBackfillMaxHold is how long new messages are held at most, in case a chunk never completes.
const chunkedBackfillMaxHold = 2 * time.Hour

type heldPortal struct {
	events []func()
	timer  *time.Timer
	// lastAnchor is the anchor of the previous chunk, to notice a chunk that made no progress.
	lastAnchor networkid.MessageID
}

type chunkedBackfill struct {
	lock sync.Mutex
	held map[networkid.PortalKey]*heldPortal
}

// start marks a forward backfill of the portal as running, so that new messages are held.
func (cb *chunkedBackfill) start(key networkid.PortalKey, release func()) {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	if cb.held == nil {
		cb.held = make(map[networkid.PortalKey]*heldPortal)
	}
	if hp, ok := cb.held[key]; ok {
		hp.timer.Reset(chunkedBackfillMaxHold)
		return
	}
	cb.held[key] = &heldPortal{timer: time.AfterFunc(chunkedBackfillMaxHold, release)}
}

// repeatedAnchor records the anchor of a new chunk and reports whether it's the same as the previous
// chunk's, which means the previous chunk didn't get bridged and splitting would loop.
func (cb *chunkedBackfill) repeatedAnchor(key networkid.PortalKey, anchor networkid.MessageID) bool {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	hp, ok := cb.held[key]
	if !ok || anchor == "" {
		return false
	}
	repeated := hp.lastAnchor == anchor
	hp.lastAnchor = anchor
	return repeated
}

// hold queues a new message event of the portal if a backfill of it is running.
func (cb *chunkedBackfill) hold(key networkid.PortalKey, fn func()) bool {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	hp, ok := cb.held[key]
	if !ok {
		return false
	}
	hp.events = append(hp.events, fn)
	return true
}

// finish ends the backfill of the portal and returns the held events.
func (cb *chunkedBackfill) finish(key networkid.PortalKey) []func() {
	cb.lock.Lock()
	defer cb.lock.Unlock()
	hp, ok := cb.held[key]
	if !ok {
		return nil
	}
	hp.timer.Stop()
	delete(cb.held, key)
	return hp.events
}

func (tc *TelegramClient) backfillSplitBytes() int64 {
	return tc.main.Config.BackfillSplitMediaMB * 1024 * 1024
}

// isLargeBackfillMedia reports whether a message has a file large enough to end a backfill chunk.
func isLargeBackfillMedia(msg *tg.Message, limit int64) bool {
	if limit <= 0 {
		return false
	}
	media, ok := msg.GetMedia()
	if !ok {
		return false
	}
	doc, ok := media.(*tg.MessageMediaDocument)
	if !ok {
		return false
	}
	document, ok := doc.Document.(*tg.Document)
	return ok && document.Size > limit
}

func (tc *TelegramClient) startChunkedBackfill(key networkid.PortalKey) {
	tc.chunkedBackfill.start(key, func() {
		tc.main.Bridge.Log.Warn().Stringer("portal_key", key).Msg("Chunked backfill didn't finish in time, releasing held messages")
		tc.finishChunkedBackfill(key, false)
	})
}

// setChunkedBackfillPending remembers in the portal metadata whether a chunked backfill is unfinished.
func (tc *TelegramClient) setChunkedBackfillPending(ctx context.Context, portal *bridgev2.Portal, pending bool) {
	meta := portal.Metadata.(*PortalMetadata)
	if meta.ChunkedBackfillPending == pending {
		return
	}
	meta.ChunkedBackfillPending = pending
	if err := portal.Save(ctx); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save chunked backfill state")
	}
}

// resumeChunkedBackfills continues chunked backfills that were interrupted by a restart.
func (tc *TelegramClient) resumeChunkedBackfills(ctx context.Context) {
	if tc.backfillSplitBytes() <= 0 {
		return
	}
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, tc.userLogin.UserLogin)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to get portals to resume chunked backfill")
		return
	}
	for _, up := range userPortals {
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" || !portal.Metadata.(*PortalMetadata).ChunkedBackfillPending {
			continue
		}
		if peerType, chatID, _, err := ids.ParsePortalID(portal.ID); err == nil && tc.folderSpaces.skipArchivedPeer(peerForID(peerType, chatID)) {
			// Archived while its backfill was running; resyncing would bring the user back into the room.
			tc.setChunkedBackfillPending(ctx, portal, false)
			continue
		}
		zerolog.Ctx(ctx).Info().Stringer("portal_key", portal.PortalKey).Msg("Resuming interrupted chunked backfill")
		tc.startChunkedBackfill(portal.PortalKey)
		tc.continueChunkedBackfill(portal.PortalKey)
	}
}

// finishChunkedBackfill releases the new messages held during the backfill of the portal. Only a
// backfill that sent its last chunk is done; one that failed or was interrupted (e.g. by stopping the
// bridge) stays pending and is resumed when the client connects again.
func (tc *TelegramClient) finishChunkedBackfill(key networkid.PortalKey, completed bool) {
	if completed {
		if portal, err := tc.main.Bridge.GetExistingPortalByKey(context.Background(), key); err == nil && portal != nil {
			tc.setChunkedBackfillPending(context.Background(), portal, false)
		}
	}
	events := tc.chunkedBackfill.finish(key)
	if len(events) > 0 {
		tc.main.Bridge.Log.Debug().Stringer("portal_key", key).Int("count", len(events)).Msg("Releasing messages held during backfill")
	}
	for _, fn := range events {
		fn()
	}
}

// createRoomWithBackfill creates the room of a chat that has none through a chat resync instead of
// letting the new message create it. bridgev2 sends the message after the first backfill chunk, which
// would cut off the rest of the history; the backfill includes the message anyway. Returns false if
// the chat already has a room or splitting is disabled.
func (tc *TelegramClient) createRoomWithBackfill(ctx context.Context, key networkid.PortalKey, msg *tg.Message) bool {
	if tc.backfillSplitBytes() <= 0 || !tc.main.Bridge.Config.Backfill.Enabled {
		return false
	}
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, key)
	if err != nil || (portal != nil && portal.MXID != "") {
		return false
	}
	res := tc.main.Bridge.QueueRemoteEvent(tc.userLogin, &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    key,
			CreatePortal: true,
			Timestamp:    time.Unix(int64(msg.Date), 0),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("action", "create room for new message").Int("message_id", msg.ID)
			},
		},
		GetChatInfoFunc: tc.GetChatInfo,
		LatestMessageTS: time.Unix(int64(msg.Date), 0),
	})
	if !res.Success && !res.Queued {
		zerolog.Ctx(ctx).Warn().Err(res.Error).Msg("Failed to queue room creation, handling the message normally")
		return false
	}
	tc.folderSpaces.afterPossibleCreate(ctx, key)
	return true
}

// continueChunkedBackfill backfills the rest of the history after a chunk was sent.
func (tc *TelegramClient) continueChunkedBackfill(key networkid.PortalKey) {
	if peerType, chatID, _, err := ids.ParsePortalID(key.ID); err == nil && tc.folderSpaces.skipArchivedPeer(peerForID(peerType, chatID)) {
		tc.finishChunkedBackfill(key, true)
		return
	}
	res := tc.main.Bridge.QueueRemoteEvent(tc.userLogin, &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatResync,
			PortalKey: key,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("action", "continue chunked backfill")
			},
		},
		CheckNeedsBackfillFunc: func(ctx context.Context, latestMessage *database.Message) (bool, error) {
			return true, nil
		},
	})
	if !res.Success && !res.Queued {
		tc.main.Bridge.Log.Warn().Err(res.Error).Stringer("portal_key", key).Msg("Failed to continue chunked backfill")
		tc.finishChunkedBackfill(key, false)
	}
}

// peerForID builds a Telegram peer from a parsed portal ID.
func peerForID(peerType ids.PeerType, chatID int64) tg.PeerClass {
	switch peerType {
	case ids.PeerTypeUser:
		return &tg.PeerUser{UserID: chatID}
	case ids.PeerTypeChat:
		return &tg.PeerChat{ChatID: chatID}
	default:
		return &tg.PeerChannel{ChannelID: chatID}
	}
}
