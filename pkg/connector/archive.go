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
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Archived Telegram chats (folder 1) aren't bridged by default: no rooms are created for them and new
// messages in them are ignored. `archive on` bridges them into an "Archive" sub-space, `archive off`
// makes the user leave their rooms, which stay on the homeserver, so that turning it back on only
// catches up on what was missed. "All chats" never contains archived chats, like in Telegram.

const archiveFolderID = 1

// archiveSet tracks which chats are archived (folderPeerID format). It's read on every new message,
// so it has its own lock instead of the folder space lock.
type archiveSet struct {
	lock   sync.RWMutex
	peers  map[string]struct{}
	loaded bool
	// fetched is set once the list was fetched from Telegram in this run.
	fetched bool
}

func (as *archiveSet) load(saved []string) {
	as.lock.Lock()
	defer as.lock.Unlock()
	if as.loaded {
		return
	}
	as.peers = make(map[string]struct{}, len(saved))
	for _, p := range saved {
		as.peers[p] = struct{}{}
	}
	as.loaded = true
}

func (as *archiveSet) contains(peerID string) bool {
	as.lock.RLock()
	defer as.lock.RUnlock()
	_, ok := as.peers[peerID]
	return ok
}

// set marks a chat as archived or not and reports whether that changed anything.
func (as *archiveSet) set(peerID string, archived bool) bool {
	as.lock.Lock()
	defer as.lock.Unlock()
	if as.peers == nil {
		as.peers = make(map[string]struct{})
	}
	_, was := as.peers[peerID]
	if archived {
		as.peers[peerID] = struct{}{}
	} else {
		delete(as.peers, peerID)
	}
	return was != archived
}

func (as *archiveSet) replace(peers map[string]struct{}) {
	as.lock.Lock()
	defer as.lock.Unlock()
	as.peers = peers
	as.loaded = true
	as.fetched = true
}

func (as *archiveSet) list() []string {
	as.lock.RLock()
	defer as.lock.RUnlock()
	return slices.Sorted(maps.Keys(as.peers))
}

// archiveEnabled reports whether archived chats are bridged. Without folder spaces, they always are.
func (fs *folderSpaceManager) archiveEnabled() bool {
	if !fs.enabled() {
		return true
	}
	meta := fs.tc.metadata.FolderSpaces
	return meta != nil && meta.ArchiveEnabled
}

func (fs *folderSpaceManager) isArchived(peerID string) bool {
	if meta := fs.tc.metadata.FolderSpaces; meta != nil {
		fs.archive.load(meta.Archived)
	}
	return fs.archive.contains(peerID)
}

// skipArchivedPeer reports whether a chat must not be bridged because it's archived.
func (fs *folderSpaceManager) skipArchivedPeer(peer tg.PeerClass) bool {
	if fs.archiveEnabled() {
		return false
	}
	return fs.isArchived(folderPeerIDFromPeer(peer))
}

func folderPeerIDFromPeer(peer tg.PeerClass) string {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return folderPeerID(ids.PeerTypeUser, p.UserID)
	case *tg.PeerChat:
		return folderPeerID(ids.PeerTypeChat, p.ChatID)
	case *tg.PeerChannel:
		return folderPeerID(ids.PeerTypeChannel, p.ChannelID)
	default:
		return ""
	}
}

// noteDialogFolder records the folder of a dialog seen during chat sync.
func (fs *folderSpaceManager) noteDialogFolder(dialog *tg.Dialog) {
	if !fs.enabled() {
		return
	}
	folderID, _ := dialog.GetFolderID()
	if meta := fs.tc.metadata.FolderSpaces; meta != nil {
		fs.archive.load(meta.Archived)
	}
	if fs.archive.set(folderPeerIDFromPeer(dialog.Peer), folderID == archiveFolderID) {
		fs.schedule(folderSpacesAfterChangeDelay)
	}
}

// fetchArchived gets the archived chats from Telegram. Must be called with fs.lock held.
func (fs *folderSpaceManager) fetchArchived(ctx context.Context) error {
	tc := fs.tc
	peers := make(map[string]struct{})
	req := &tg.MessagesGetDialogsRequest{FolderID: archiveFolderID, OffsetPeer: &tg.InputPeerEmpty{}, Limit: 100}
	for page := 0; page < 50; page++ {
		resp, err := APICallWithUpdates(ctx, tc, func() (tg.ModifiedMessagesDialogs, error) {
			box, err := tc.client.API().MessagesGetDialogs(ctx, req)
			if err != nil {
				return nil, err
			}
			modified, ok := box.AsModified()
			if !ok {
				return nil, fmt.Errorf("unexpected response type %T", box)
			}
			return modified, nil
		})
		if err != nil {
			return fmt.Errorf("failed to get archived dialogs: %w", err)
		}
		list := resp.GetDialogs()
		for _, d := range list {
			// The response also lists the pinned chats of the main list, only take the archived ones.
			if dialog, ok := d.(*tg.Dialog); ok {
				if folderID, ok := dialog.GetFolderID(); ok && folderID == archiveFolderID {
					peers[folderPeerIDFromPeer(dialog.Peer)] = struct{}{}
				}
			}
		}
		if len(list) < req.Limit {
			break
		}
		last, ok := list[len(list)-1].(*tg.Dialog)
		if !ok {
			break
		}
		req.OffsetID, req.OffsetDate = getDialogOffset(list, resp.GetMessages())
		if req.OffsetPeer, _, err = tc.inputPeerForPortalID(ctx, tc.makePortalKeyFromPeer(last.Peer, 0).ID); err != nil {
			return fmt.Errorf("failed to get input peer for archive pagination: %w", err)
		}
	}
	fs.archive.replace(peers)
	zerolog.Ctx(ctx).Debug().Int("count", len(peers)).Msg("Fetched archived chats")
	return nil
}

// onFolderPeers handles chats moved into or out of the archive.
func (tc *TelegramClient) onFolderPeers(ctx context.Context, update *tg.UpdateFolderPeers) error {
	fs := tc.folderSpaces
	if !fs.enabled() {
		return nil
	}
	if meta := tc.metadata.FolderSpaces; meta != nil {
		fs.archive.load(meta.Archived)
	}
	for _, fp := range update.FolderPeers {
		archived := fp.FolderID == archiveFolderID
		if !fs.archive.set(folderPeerIDFromPeer(fp.Peer), archived) {
			continue
		}
		zerolog.Ctx(ctx).Debug().Stringer("peer", fp.Peer).Bool("archived", archived).Msg("Chat moved to or from the archive")
		if !archived && !fs.archiveEnabled() {
			// The chat is bridged again: create its room, or catch up on what was missed.
			tc.resyncWithBackfill(tc.makePortalKeyFromPeer(fp.Peer, 0), true)
		}
	}
	fs.schedule(folderSpacesAfterChangeDelay)
	return nil
}

// resyncWithBackfill queues a chat resync that backfills messages newer than the last bridged one,
// creating the room if allowed and needed.
func (tc *TelegramClient) resyncWithBackfill(key networkid.PortalKey, create bool) {
	go func() {
		res := tc.main.Bridge.QueueRemoteEvent(tc.userLogin, &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				PortalKey:    key,
				CreatePortal: create,
				LogContext: func(c zerolog.Context) zerolog.Context {
					return c.Str("action", "archive resync")
				},
			},
			GetChatInfoFunc: tc.GetChatInfo,
			CheckNeedsBackfillFunc: func(ctx context.Context, latestMessage *database.Message) (bool, error) {
				return true, nil
			},
		})
		if !res.Success && !res.Queued && res.Error != nil {
			tc.main.Bridge.Log.Warn().Err(res.Error).Stringer("portal_key", key).Msg("Failed to resync chat")
		}
	}()
}

// setArchiveMembership makes the user leave the room of an archived chat while the archive is off,
// and brings them back when it's on again (or the chat left the archive), catching up on messages.
// Must be called with fs.lock held; left is the set of rooms the user was made to leave.
func (fs *folderSpaceManager) setArchiveMembership(ctx context.Context, portal *bridgev2.Portal, hide bool, left map[id.RoomID]struct{}) {
	_, isLeft := left[portal.MXID]
	if hide == isLeft {
		return
	}
	log := zerolog.Ctx(ctx).With().Stringer("room_id", portal.MXID).Bool("hide", hide).Logger()
	dp := fs.tc.userLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return
	}
	if hide {
		asIntent, ok := dp.(*matrix.ASIntent)
		if !ok {
			return
		}
		// Leaving fails if the user isn't in the room, which hides it just as well, so the room
		// counts as left either way and isn't retried on every reconcile.
		if _, err := asIntent.Matrix.LeaveRoom(ctx, portal.MXID); err != nil {
			log.Warn().Err(err).Msg("Failed to leave room of archived chat")
		}
		left[portal.MXID] = struct{}{}
		return
	}
	if err := fs.tc.main.Bridge.Bot.EnsureInvited(ctx, portal.MXID, fs.tc.userLogin.UserMXID); err != nil {
		log.Err(err).Msg("Failed to invite user back to room of archived chat")
		return
	}
	if err := dp.EnsureJoined(ctx, portal.MXID); err != nil {
		log.Err(err).Msg("Failed to rejoin room of archived chat")
		return
	}
	delete(left, portal.MXID)
	fs.tc.resyncWithBackfill(portal.PortalKey, false)
}

// SetArchive turns bridging of archived chats on or off.
func (fs *folderSpaceManager) SetArchive(ctx context.Context, enabled bool) (created int, err error) {
	fs.lock.Lock()
	meta := fs.tc.metadata.FolderSpaces.clone()
	meta.ArchiveEnabled = enabled
	fs.tc.metadata.FolderSpaces = meta
	if err = fs.tc.userLogin.Save(ctx); err != nil {
		fs.lock.Unlock()
		return 0, fmt.Errorf("failed to save login: %w", err)
	}
	if enabled {
		if err = fs.fetchArchived(ctx); err != nil {
			fs.lock.Unlock()
			return 0, err
		}
	}
	fs.lock.Unlock()
	if err = fs.reconcile(ctx); err != nil {
		return 0, err
	}
	if !enabled {
		return 0, nil
	}
	// Archived chats without rooms get one, with the usual initial backfill.
	for _, peerID := range fs.archive.list() {
		pt, chatID, err := parseFolderPeerID(peerID)
		if err != nil {
			continue
		}
		key := fs.tc.makePortalKeyFromID(pt, chatID, 0)
		if portal, err := fs.tc.main.Bridge.GetExistingPortalByKey(ctx, key); err == nil && portal != nil && portal.MXID != "" {
			continue
		}
		fs.tc.resyncWithBackfill(key, true)
		created++
		// Spread the room creations a bit, they're heavy on both Telegram and the homeserver.
		time.Sleep(200 * time.Millisecond)
	}
	return created, nil
}

func parseFolderPeerID(peerID string) (ids.PeerType, int64, error) {
	pt, rawID, ok := strings.Cut(peerID, ":")
	if !ok {
		return "", 0, fmt.Errorf("invalid peer ID %q", peerID)
	}
	chatID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid peer ID %q: %w", peerID, err)
	}
	return ids.PeerType(pt), chatID, nil
}
