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
	"errors"
	"fmt"
	"html"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"golang.org/x/sync/singleflight"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/connector/matrixfmt"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tgerr"
)

// Comments of channel posts live in the channel's linked discussion group: every post is copied
// there automatically, and comments are group messages replying to that copy. Besides the group's
// own room (if it's bridged), comments are mirrored into the channel room as a thread under the post,
// sent by the commenter's ghost. Thread replies sent from Matrix in the channel room become comments.
//
// Comments are found three ways: live updates of the discussion group (only if the user is a
// member), the full comment history fetched after posts are backfilled, and polling the latest
// posts when the user reads the channel room (for groups the user isn't a member of).

type commentManager struct {
	// lock keeps comments of all threads in order and prevents mirroring one twice.
	lock sync.Mutex
	// notThreads caches group messages known not to be copies of channel posts.
	notThreads map[[2]int64]struct{}
	// lastPoll throttles polling per channel room.
	lastPoll map[id.RoomID]time.Time
	// discussionGroups caches whether a supergroup is the discussion group of a channel.
	discussionGroups map[int64]discussionGroupState
	// discussionChecks runs one check per group at a time.
	discussionChecks singleflight.Group
	// openRooms are channel rooms already checked to let commenters post.
	openRooms map[id.RoomID]struct{}
}

const commentPollInterval = 5 * time.Minute

// commentAPICall makes a request for comments, waiting out flood waits. Comments are fetched in the
// background for many posts in a row, and Telegram asks to slow down: without waiting, every following
// post failed right away and its comments were never mirrored.
func commentAPICall[U hasUpdates](ctx context.Context, tc *TelegramClient, fn func() (U, error)) (U, error) {
	for attempt := 1; ; attempt++ {
		resp, err := APICallWithUpdates(ctx, tc, fn)
		if err == nil || attempt == 5 {
			return resp, err
		}
		if retry, err := tgerr.FloodWait(ctx, err); !retry {
			return resp, err
		}
	}
}

// commentRootID returns the copy of the post a group message comments on, or 0.
func commentRootID(msg *tg.Message) int {
	hdr, ok := msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok || hdr.ForumTopic {
		return 0
	}
	if top, ok := hdr.GetReplyToTopID(); ok && top != 0 {
		return top
	}
	return hdr.ReplyToMsgID
}

func (tc *TelegramClient) isBroadcastPortal(portal *bridgev2.Portal) (int64, bool) {
	peerType, channelID, topicID, err := ids.ParsePortalID(portal.ID)
	if err != nil || peerType != ids.PeerTypeChannel || topicID != 0 {
		return 0, false
	}
	meta, ok := portal.Metadata.(*PortalMetadata)
	return channelID, ok && !meta.IsSuperGroup && !meta.IsCommunity
}

func (tc *TelegramClient) inputChannel(ctx context.Context, channelID int64) (*tg.InputChannel, error) {
	accessHash, err := tc.ScopedStore.GetAccessHash(ctx, ids.PeerTypeChannel, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get access hash of channel %d: %w", channelID, err)
	}
	return &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash}, nil
}

// recordCommentThreadFromCopy remembers the thread if a group message is the automatic copy of a post.
func (tc *TelegramClient) recordCommentThreadFromCopy(ctx context.Context, groupID int64, msg *tg.Message) *commentThread {
	fwd, ok := msg.GetFwdFrom()
	if !ok {
		return nil
	}
	peer, ok := fwd.GetSavedFromPeer()
	if !ok {
		return nil
	}
	channel, ok := peer.(*tg.PeerChannel)
	postID, ok2 := fwd.GetSavedFromMsgID()
	if !ok || !ok2 || channel.ChannelID == groupID {
		return nil
	}
	t := commentThread{ChannelID: channel.ChannelID, PostID: postID, GroupID: groupID, CopyID: msg.ID}
	if err := tc.saveCommentThread(ctx, t); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment thread")
	}
	return &t
}

// resolveCommentThread finds the post a thread root (a message of the group) is the copy of.
func (tc *TelegramClient) resolveCommentThread(ctx context.Context, groupID int64, copyID int) (*commentThread, error) {
	if t, err := tc.getCommentThreadByCopy(ctx, groupID, copyID); err != nil || t != nil {
		return t, err
	}
	key := [2]int64{groupID, int64(copyID)}
	tc.comments.lock.Lock()
	_, known := tc.comments.notThreads[key]
	tc.comments.lock.Unlock()
	if known {
		return nil, nil
	}
	channel, err := tc.inputChannel(ctx, groupID)
	if err != nil {
		return nil, err
	}
	resp, err := commentAPICall(ctx, tc, func() (tg.ModifiedMessagesMessages, error) {
		res, err := tc.client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: copyID}},
		})
		if err != nil {
			return nil, err
		}
		modified, ok := res.AsModified()
		if !ok {
			return nil, fmt.Errorf("unexpected response type %T", res)
		}
		return modified, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get thread root message: %w", err)
	}
	for _, m := range resp.GetMessages() {
		if msg, ok := m.(*tg.Message); ok && msg.ID == copyID {
			if t := tc.recordCommentThreadFromCopy(ctx, groupID, msg); t != nil {
				return t, nil
			}
		}
	}
	tc.comments.lock.Lock()
	if tc.comments.notThreads == nil {
		tc.comments.notThreads = make(map[[2]int64]struct{})
	}
	tc.comments.notThreads[key] = struct{}{}
	tc.comments.lock.Unlock()
	return nil, nil
}

// commentThreadForPost finds the discussion copy of a channel post, asking Telegram if needed.
func (tc *TelegramClient) commentThreadForPost(ctx context.Context, channelID int64, postID int) (*commentThread, error) {
	if t, err := tc.getCommentThreadByPost(ctx, channelID, postID); err != nil || t != nil {
		return t, err
	}
	channel, err := tc.inputChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	resp, err := commentAPICall(ctx, tc, func() (*tg.MessagesDiscussionMessage, error) {
		return tc.client.API().MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
			Peer:  &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			MsgID: postID,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get discussion message: %w", err)
	}
	for _, m := range resp.Messages {
		msg, ok := m.(*tg.Message)
		if !ok {
			continue
		}
		group, ok := msg.PeerID.(*tg.PeerChannel)
		if !ok || group.ChannelID == channelID {
			continue
		}
		t := commentThread{ChannelID: channelID, PostID: postID, GroupID: group.ChannelID, CopyID: msg.ID}
		if err = tc.saveCommentThread(ctx, t); err != nil {
			return nil, err
		}
		return &t, nil
	}
	return nil, nil
}

// handleGroupMessageForComments is called for every new message of a supergroup: it records post
// copies and mirrors comments into the channel room.
func (tc *TelegramClient) handleGroupMessageForComments(ctx context.Context, msg *tg.Message) {
	group, ok := msg.PeerID.(*tg.PeerChannel)
	if !ok {
		return
	}
	if tc.recordCommentThreadFromCopy(ctx, group.ChannelID, msg) != nil {
		return
	}
	if commentRootID(msg) == 0 || !tc.isDiscussionGroup(ctx, group.ChannelID) {
		return
	}
	if err := tc.mirrorComment(ctx, group.ChannelID, msg); err != nil {
		zerolog.Ctx(ctx).Err(err).Int("msg_id", msg.ID).Msg("Failed to mirror comment into channel room")
	}
}

// discussionGroupState is what's known about a supergroup being a discussion group.
type discussionGroupState struct {
	isDiscussion bool
	// retryAt is when to check again after the check failed, zero once it's known.
	retryAt time.Time
}

// discussionCheckRetry is how long a failed check is not repeated. Replies in a busy group would
// otherwise check it again for every message and run the account into flood waits.
const discussionCheckRetry = 10 * time.Minute

// isDiscussionGroup reports whether a supergroup is linked to a channel as its discussion group.
// It's asked once per group and run, so that replies in ordinary groups cost no extra requests.
func (tc *TelegramClient) isDiscussionGroup(ctx context.Context, groupID int64) bool {
	tc.comments.lock.Lock()
	state, ok := tc.comments.discussionGroups[groupID]
	tc.comments.lock.Unlock()
	if ok && (state.retryAt.IsZero() || time.Now().Before(state.retryAt)) {
		return state.isDiscussion
	}
	result, _, _ := tc.comments.discussionChecks.Do(strconv.FormatInt(groupID, 10), func() (any, error) {
		state := discussionGroupState{}
		var err error
		if state.isDiscussion, err = tc.fetchIsDiscussionGroup(ctx, groupID); err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Int64("group_id", groupID).Msg("Failed to check if group is a discussion group")
			state.retryAt = time.Now().Add(discussionCheckRetry)
		}
		tc.comments.lock.Lock()
		if tc.comments.discussionGroups == nil {
			tc.comments.discussionGroups = make(map[int64]discussionGroupState)
		}
		tc.comments.discussionGroups[groupID] = state
		tc.comments.lock.Unlock()
		return state.isDiscussion, nil
	})
	return result.(bool)
}

func (tc *TelegramClient) fetchIsDiscussionGroup(ctx context.Context, groupID int64) (bool, error) {
	channel, err := tc.inputChannel(ctx, groupID)
	if err != nil {
		return false, err
	}
	full, err := commentAPICall(ctx, tc, func() (*tg.MessagesChatFull, error) {
		return tc.client.API().ChannelsGetFullChannel(ctx, channel)
	})
	if err != nil {
		return false, err
	}
	channelFull, okFull := full.FullChat.(*tg.ChannelFull)
	megagroup := false
	for _, c := range full.Chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == groupID {
			megagroup = ch.Megagroup
		}
	}
	return okFull && megagroup && channelFull.LinkedChatID != 0, nil
}

// ensureCommentsAllowed lowers events_default of a channel room so that commenters can post threads.
// New rooms get it from the chat info, rooms made before comments were mirrored only on their next
// info sync. Must be called with the comment lock held.
func (tc *TelegramClient) ensureCommentsAllowed(ctx context.Context, room id.RoomID) error {
	if _, ok := tc.comments.openRooms[room]; ok {
		return nil
	}
	bot, ok := tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok {
		return nil
	}
	var pl event.PowerLevelsEventContent
	if err := bot.Matrix().StateEvent(ctx, room, event.StatePowerLevels, "", &pl); err != nil {
		return fmt.Errorf("failed to get power levels: %w", err)
	}
	if pl.EventsDefault > 0 {
		pl.EventsDefault = 0
		if _, err := tc.main.Bridge.Bot.SendState(ctx, room, event.StatePowerLevels, "", &event.Content{Parsed: &pl}, time.Now()); err != nil {
			return fmt.Errorf("failed to let commenters post: %w", err)
		}
	}
	if tc.comments.openRooms == nil {
		tc.comments.openRooms = make(map[id.RoomID]struct{})
	}
	tc.comments.openRooms[room] = struct{}{}
	return nil
}

// Comments of other people are sent by one "Comments" account with the author's name as a link to
// their ghost's profile, instead of by each author's ghost: those would all join the channel room,
// thousands in big channels, filling the timeline and member list and the room state. From the profile
// the user can still start a private chat with the author. The user's own comments come from them.

const commenterGhostID networkid.UserID = "commenter"

var commenterNames = map[string]string{"en": "Comments", "ru": "Комментарии"}

// commentAuthor is who wrote a comment sent by the commenter account; nil for the user's own.
type commentAuthor struct {
	Name string
	MXID id.UserID
}

// addTo puts "Author: " before the comment, linking the name to the author's profile.
func (a *commentAuthor) addTo(content *event.MessageEventContent) {
	if a == nil {
		return
	}
	if content.MsgType.IsMedia() && content.FileName == "" {
		// The body becomes the caption, keep the file name in its own field.
		content.FileName = content.Body
		content.Body = ""
		content.FormattedBody = ""
		content.Format = ""
	}
	content.EnsureHasHTML()
	link := fmt.Sprintf(`<a href="https://matrix.to/#/%s">%s</a>`, a.MXID, html.EscapeString(a.Name))
	if content.Body == "" {
		content.Body = a.Name
		content.FormattedBody = "<strong>" + link + "</strong>"
	} else {
		content.Body = a.Name + ": " + content.Body
		content.FormattedBody = "<strong>" + link + ":</strong> " + content.FormattedBody
	}
	content.Format = event.FormatHTML
}

// commentSender returns who sends a comment into the channel room: the user's double puppet for
// their own comments, the commenter account with the author otherwise. The author's ghost is
// registered (so that its profile opens) but doesn't join the room.
func (tc *TelegramClient) commentSender(ctx context.Context, portal *bridgev2.Portal, sender bridgev2.EventSender) (bridgev2.MatrixAPI, *commentAuthor, error) {
	if sender.IsFromMe {
		intent, ok := portal.GetIntentFor(ctx, sender, tc.userLogin, bridgev2.RemoteEventMessage)
		if !ok {
			return nil, nil, nil
		}
		return intent, nil, intent.EnsureJoined(ctx, portal.MXID)
	}
	author := &commentAuthor{}
	if authorIntent, ok := portal.GetIntentFor(ctx, sender, tc.userLogin, bridgev2.RemoteEventMessage); ok {
		author.MXID = authorIntent.GetMXID()
	}
	if ghost, err := tc.main.Bridge.GetGhostByID(ctx, sender.Sender); err == nil && ghost != nil {
		author.Name = ghost.Name
	}
	if author.Name == "" {
		author.Name = string(sender.Sender)
	}
	commenter, err := tc.main.Bridge.GetGhostByID(ctx, commenterGhostID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get commenter account: %w", err)
	}
	// The account is shared by all users, its name is in the bridge's language rather than the user's.
	name, ok := commenterNames[tc.main.Config.FolderSpaces.DefaultLanguage]
	if !ok {
		name = commenterNames["en"]
	}
	commenter.UpdateName(ctx, name)
	if err = commenter.Intent.EnsureJoined(ctx, portal.MXID); err != nil {
		return nil, nil, fmt.Errorf("failed to join commenter account to channel room: %w", err)
	}
	return commenter.Intent, author, nil
}

// mirrorSentByAuthor reports whether a mirrored comment was sent by the author's ghost, which
// comments mirrored before the commenter account was used were.
func (tc *TelegramClient) mirrorSentByAuthor(ctx context.Context, mirror *commentMirror) bool {
	bot, ok := tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok {
		return false
	}
	evt, err := bot.Matrix().GetEvent(ctx, mirror.RoomID, mirror.EventID)
	if err != nil {
		return false
	}
	ghostID, isGhost := tc.main.Bridge.Matrix.ParseGhostMXID(evt.Sender)
	return isGhost && ghostID != commenterGhostID
}

// replaceAuthorSentComment replaces a comment sent by the author's ghost with one sent by the
// commenter account, with the edit applied.
func (tc *TelegramClient) replaceAuthorSentComment(ctx context.Context, groupID int64, mirror *commentMirror, msg *tg.Message) {
	tc.comments.lock.Lock()
	current, err := tc.getCommentMirror(ctx, mirror.RoomID, groupID, msg.ID)
	if err == nil && current != nil && current.EventID == mirror.EventID {
		err = tc.deleteCommentMirror(ctx, mirror.RoomID, groupID, msg.ID)
	}
	tc.comments.lock.Unlock()
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to replace edited comment")
		return
	}
	tc.redactAsBot(ctx, mirror.RoomID, mirror.EventID, "comment edited")
	// Mirrors the comment again unless a concurrent edit already did.
	if err = tc.mirrorComment(ctx, groupID, msg); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to mirror edited comment")
	}
}

// readCommentThread marks a comment thread read for the user. Like in Telegram, comments under
// other people's posts don't ask for attention unless the user takes part in the discussion;
// otherwise every channel with new comments lit up in the client's thread activity.
func (tc *TelegramClient) readCommentThread(ctx context.Context, room id.RoomID, threadRoot, eventID id.EventID) {
	dp, ok := tc.userLogin.User.DoublePuppet(ctx).(*matrix.ASIntent)
	if !ok || dp == nil {
		return
	}
	err := dp.Matrix().SendReceipt(ctx, room, eventID, event.ReceiptTypeRead, map[string]any{"thread_id": threadRoot})
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to mark comment thread read")
	}
}

// mirrorComment sends a comment to the channel room as a thread message under its post.
func (tc *TelegramClient) mirrorComment(ctx context.Context, groupID int64, msg *tg.Message) error {
	rootID := commentRootID(msg)
	if rootID == 0 || rootID == msg.ID {
		return nil
	}
	thread, err := tc.resolveCommentThread(ctx, groupID, rootID)
	if err != nil || thread == nil {
		return err
	}
	tc.comments.lock.Lock()
	defer tc.comments.lock.Unlock()
	if tc.folderSpaces.skipArchivedPeer(&tg.PeerChannel{ChannelID: thread.ChannelID}) {
		return nil
	}
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, tc.makePortalKeyFromID(ids.PeerTypeChannel, thread.ChannelID, 0))
	if err != nil || portal == nil || portal.MXID == "" {
		return err
	}
	if existing, err := tc.getCommentMirror(ctx, portal.MXID, groupID, msg.ID); err != nil || existing != nil {
		return err
	}
	post, err := tc.main.Bridge.DB.Message.GetFirstPartByID(ctx, portal.Receiver, ids.MakeMessageID(&tg.PeerChannel{ChannelID: thread.ChannelID}, thread.PostID))
	if err != nil || post == nil || post.MXID == "" {
		// The post isn't in Matrix (older than the backfilled history), there's nothing to thread on.
		return err
	}

	if err = tc.ensureCommentsAllowed(ctx, portal.MXID); err != nil {
		return err
	}
	sender := tc.getEventSender(msg, false)
	intent, author, err := tc.commentSender(ctx, portal, sender)
	if err != nil || intent == nil {
		return err
	}
	converted, err := tc.convertToMatrix(ctx, portal, intent, msg)
	if err != nil {
		return fmt.Errorf("failed to convert comment: %w", err)
	}

	// Replies to other comments point at their mirror, other comments fall back to the post.
	replyTo, isFallback := post.MXID, true
	if hdr, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok && hdr.ReplyToMsgID != rootID {
		if target, err := tc.getCommentMirror(ctx, portal.MXID, groupID, hdr.ReplyToMsgID); err == nil && target != nil {
			replyTo, isFallback = target.EventID, false
		}
	}
	var firstEvent, lastEvent id.EventID
	for i, part := range converted.Parts {
		content := part.Content
		// The ghost sends the comment itself, the channel's "Name: " signature isn't needed.
		content.RemovePerMessageProfileFallback()
		content.BeeperPerMessageProfile = nil
		if i == 0 {
			author.addTo(content)
		}
		content.RelatesTo = &event.RelatesTo{
			Type:          event.RelThread,
			EventID:       post.MXID,
			IsFallingBack: isFallback,
			InReplyTo:     &event.InReplyTo{EventID: replyTo},
		}
		resp, err := intent.SendMessage(ctx, portal.MXID, part.Type, &event.Content{Parsed: content, Raw: part.Extra},
			&bridgev2.MatrixSendExtra{Timestamp: time.Unix(int64(msg.Date), 0)})
		if err != nil {
			return fmt.Errorf("failed to send comment part %d: %w", i, err)
		}
		if i == 0 {
			firstEvent = resp.EventID
		}
		lastEvent = resp.EventID
	}
	if firstEvent == "" {
		return nil
	}
	if sender.IsFromMe {
		if err = tc.markCommentParticipation(ctx, groupID, rootID); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment thread participation")
		}
	} else if !tc.isParticipatingInComments(ctx, groupID, rootID) {
		tc.readCommentThread(ctx, portal.MXID, post.MXID, lastEvent)
	}
	if editDate, ok := msg.GetEditDate(); ok {
		// Mirrored with this edit already applied.
		if err = tc.saveCommentEditDate(ctx, portal.MXID, groupID, msg.ID, editDate); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment edit date")
		}
	}
	return tc.saveCommentMirror(ctx, commentMirror{GroupID: groupID, MsgID: msg.ID, CopyID: rootID, RoomID: portal.MXID, EventID: firstEvent})
}

// commentPortalOfGroup returns the user's channel portal that comments of a discussion group are
// mirrored into, nil if there is none.
func (tc *TelegramClient) commentPortalOfGroup(ctx context.Context, groupID int64) (*bridgev2.Portal, error) {
	channelID, err := tc.channelOfGroup(ctx, groupID)
	if err != nil || channelID == 0 {
		return nil, err
	}
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, tc.makePortalKeyFromID(ids.PeerTypeChannel, channelID, 0))
	if err != nil || portal == nil || portal.MXID == "" {
		return nil, err
	}
	return portal, nil
}

// mirrorCommentEdit applies an edit of a mirrored comment to its thread message.
func (tc *TelegramClient) mirrorCommentEdit(ctx context.Context, msg *tg.Message) {
	group, ok := msg.PeerID.(*tg.PeerChannel)
	if !ok {
		return
	}
	portal, err := tc.commentPortalOfGroup(ctx, group.ChannelID)
	if err != nil || portal == nil {
		return
	}
	mirror, err := tc.getCommentMirror(ctx, portal.MXID, group.ChannelID, msg.ID)
	if err != nil || mirror == nil {
		return
	}
	// Skip edits already applied (polling sees the same edit again).
	if applied, err := tc.getCommentEditDate(ctx, portal.MXID, group.ChannelID, msg.ID); err == nil && msg.EditDate != 0 && msg.EditDate <= applied {
		return
	}
	defer func() {
		if err := tc.saveCommentEditDate(ctx, portal.MXID, group.ChannelID, msg.ID, msg.EditDate); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment edit date")
		}
	}()
	tc.updateMirroredComment(ctx, group.ChannelID, mirror, msg, time.Unix(int64(msg.EditDate), 0))
}

// updateMirroredComment replaces the content of a mirrored comment with the current one of msg.
func (tc *TelegramClient) updateMirroredComment(ctx context.Context, groupID int64, mirror *commentMirror, msg *tg.Message, ts time.Time) {
	portal, err := tc.main.Bridge.GetPortalByMXID(ctx, mirror.RoomID)
	if err != nil || portal == nil {
		return
	}
	if tc.mirrorSentByAuthor(ctx, mirror) {
		// Comments mirrored before the commenter account was used were sent by the author's ghost.
		// Only the sender can edit a message, and the ghost has left the room (see
		// removeCommentGhosts), so the comment is replaced instead.
		tc.replaceAuthorSentComment(ctx, groupID, mirror, msg)
		return
	}
	intent, author, err := tc.commentSender(ctx, portal, tc.getEventSender(msg, false))
	if err != nil || intent == nil {
		return
	}
	converted, err := tc.convertToMatrix(ctx, portal, intent, msg)
	if err != nil || len(converted.Parts) == 0 {
		return
	}
	content := converted.Parts[0].Content
	content.RemovePerMessageProfileFallback()
	content.BeeperPerMessageProfile = nil
	author.addTo(content)
	content.SetEdit(mirror.EventID)
	if _, err = intent.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content},
		&bridgev2.MatrixSendExtra{Timestamp: ts}); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to edit mirrored comment")
	}
}

// mirrorCommentDeletes removes mirrored comments that were deleted in the discussion group.
func (tc *TelegramClient) mirrorCommentDeletes(ctx context.Context, groupID int64, msgIDs []int) {
	portal, err := tc.commentPortalOfGroup(ctx, groupID)
	if err != nil || portal == nil {
		return
	}
	for _, msgID := range msgIDs {
		mirror, err := tc.getCommentMirror(ctx, portal.MXID, groupID, msgID)
		if err != nil || mirror == nil {
			continue
		}
		tc.redactAsBot(ctx, mirror.RoomID, mirror.EventID, "comment deleted")
		if err = tc.deleteCommentMirror(ctx, mirror.RoomID, groupID, msgID); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to delete comment mirror")
		}
	}
}

// fetchPostComments mirrors the comments of a post that aren't in Matrix yet, oldest first. It returns
// an error if fetching failed or any comment couldn't be mirrored.
func (tc *TelegramClient) fetchPostComments(ctx context.Context, channelID int64, postID int) error {
	thread, err := tc.commentThreadForPost(ctx, channelID, postID)
	if err != nil || thread == nil {
		return err
	}
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, tc.makePortalKeyFromID(ids.PeerTypeChannel, channelID, 0))
	if err != nil || portal == nil || portal.MXID == "" {
		return err
	}
	minID, err := tc.lastMirroredComment(ctx, portal.MXID, thread.GroupID, thread.CopyID)
	if err != nil {
		return err
	}
	channel, err := tc.inputChannel(ctx, channelID)
	if err != nil {
		return err
	}
	peer := &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}
	var comments []*tg.Message
	offsetID := 0
	for page := 0; page < 1000; page++ {
		resp, err := commentAPICall(ctx, tc, func() (tg.ModifiedMessagesMessages, error) {
			res, err := tc.client.API().MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
				Peer:     peer,
				MsgID:    postID,
				OffsetID: offsetID,
				Limit:    100,
				MinID:    minID,
			})
			if err != nil {
				return nil, err
			}
			modified, ok := res.AsModified()
			if !ok {
				return nil, fmt.Errorf("unexpected response type %T", res)
			}
			return modified, nil
		})
		if err != nil {
			return fmt.Errorf("failed to get comments: %w", err)
		}
		msgs := resp.GetMessages()
		for _, m := range msgs {
			if msg, ok := m.(*tg.Message); ok {
				comments = append(comments, msg)
			}
		}
		if len(msgs) < 100 {
			break
		}
		offsetID = msgs[len(msgs)-1].GetID()
	}
	// Returned newest first.
	slices.Reverse(comments)
	if minID > 0 {
		// Groups without live updates (not bridged, too big) only reach the bridge through polling,
		// so also look at the latest comments for edits of ones already in Matrix.
		tc.pollCommentEdits(ctx, peer, postID)
	}
	var failed error
	for _, msg := range comments {
		if err = tc.mirrorComment(ctx, thread.GroupID, msg); err != nil {
			zerolog.Ctx(ctx).Err(err).Int("msg_id", msg.ID).Msg("Failed to mirror comment")
			failed = err
		}
	}
	if len(comments) > 0 {
		zerolog.Ctx(ctx).Debug().Int64("channel_id", channelID).Int("post_id", postID).Int("count", len(comments)).Msg("Mirrored comments of post")
	}
	return failed
}

// pollCommentEdits applies edits of the latest comments of a post that are already in Matrix.
func (tc *TelegramClient) pollCommentEdits(ctx context.Context, peer *tg.InputPeerChannel, postID int) {
	resp, err := commentAPICall(ctx, tc, func() (tg.ModifiedMessagesMessages, error) {
		res, err := tc.client.API().MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: peer, MsgID: postID, Limit: 100})
		if err != nil {
			return nil, err
		}
		modified, ok := res.AsModified()
		if !ok {
			return nil, fmt.Errorf("unexpected response type %T", res)
		}
		return modified, nil
	})
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Int("post_id", postID).Msg("Failed to check comments for edits")
		return
	}
	for _, m := range resp.GetMessages() {
		if msg, ok := m.(*tg.Message); ok && msg.EditDate != 0 {
			tc.mirrorCommentEdit(ctx, msg)
		}
	}
}

// postIDsWithComments returns the posts among the messages that have comments.
func postIDsWithComments(messages []*tg.Message) (postIDs []int) {
	for _, msg := range messages {
		if replies, ok := msg.GetReplies(); ok && replies.Comments && replies.Replies > 0 {
			postIDs = append(postIDs, msg.ID)
		}
	}
	return
}

// backfillComments fetches all comments of the given posts and reports whether all of them made it.
func (tc *TelegramClient) backfillComments(channelID int64, postIDs []int) bool {
	ctx := tc.main.Bridge.Log.With().Str("action", "backfill comments").Int64("channel_id", channelID).Logger().WithContext(context.Background())
	ok := true
	for _, postID := range postIDs {
		if err := tc.fetchPostComments(ctx, channelID, postID); err != nil {
			zerolog.Ctx(ctx).Err(err).Int("post_id", postID).Msg("Failed to backfill comments")
			ok = false
		}
	}
	return ok
}

// latestPostsWithComments asks Telegram which of the latest bridged posts of a channel have comments.
func (tc *TelegramClient) latestPostsWithComments(ctx context.Context, portal *bridgev2.Portal, channelID int64, limit int) ([]*tg.Message, error) {
	latest, err := tc.main.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, limit)
	if err != nil {
		return nil, err
	}
	var inputIDs []tg.InputMessageClass
	seen := map[int]struct{}{}
	for _, m := range latest {
		_, msgID, err := ids.ParseMessageID(m.ID)
		if err != nil {
			continue
		}
		if _, dup := seen[msgID]; !dup {
			seen[msgID] = struct{}{}
			inputIDs = append(inputIDs, &tg.InputMessageID{ID: msgID})
		}
	}
	if len(inputIDs) == 0 {
		return nil, nil
	}
	channel, err := tc.inputChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	resp, err := commentAPICall(ctx, tc, func() (tg.ModifiedMessagesMessages, error) {
		res, err := tc.client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: inputIDs})
		if err != nil {
			return nil, err
		}
		modified, ok := res.AsModified()
		if !ok {
			return nil, fmt.Errorf("unexpected response type %T", res)
		}
		return modified, nil
	})
	if err != nil {
		return nil, err
	}
	var posts []*tg.Message
	for _, m := range resp.GetMessages() {
		if msg, ok := m.(*tg.Message); ok {
			posts = append(posts, msg)
		}
	}
	return posts, nil
}

// maybePollComments checks the latest posts of a channel room for new comments, at most every few
// minutes per room. Needed for discussion groups the user isn't a member of, which send no updates.
func (tc *TelegramClient) maybePollComments(ctx context.Context, portal *bridgev2.Portal) {
	channelID, ok := tc.isBroadcastPortal(portal)
	if !ok || portal.MXID == "" {
		return
	}
	tc.comments.lock.Lock()
	if tc.comments.lastPoll == nil {
		tc.comments.lastPoll = make(map[id.RoomID]time.Time)
	}
	if time.Since(tc.comments.lastPoll[portal.MXID]) < commentPollInterval {
		tc.comments.lock.Unlock()
		return
	}
	tc.comments.lastPoll[portal.MXID] = time.Now()
	tc.comments.lock.Unlock()

	posts, err := tc.latestPostsWithComments(ctx, portal, channelID, 10)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to check posts for new comments")
		return
	}
	for _, postID := range postIDsWithComments(posts) {
		if err = tc.fetchPostComments(ctx, channelID, postID); err != nil {
			zerolog.Ctx(ctx).Err(err).Int("post_id", postID).Msg("Failed to poll comments")
		}
	}
}

// readExistingCommentThreads marks every comment thread mirrored so far read, once. Participation
// wasn't recorded before, so all of them count as threads the user isn't in.
func (tc *TelegramClient) readExistingCommentThreads(ctx context.Context) {
	if tc.metadata.CommentThreadsRead {
		return
	}
	threads, err := tc.latestCommentPerThread(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to list comment threads to mark read")
		return
	}
	for _, t := range threads {
		room, channelID, postID, eventID := t[0].(id.RoomID), t[1].(int64), t[2].(int), t[3].(id.EventID)
		key := tc.makePortalKeyFromID(ids.PeerTypeChannel, channelID, 0)
		if portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, key); err != nil || portal == nil || portal.MXID != room {
			// Another user's room of the channel.
			continue
		}
		post, err := tc.main.Bridge.DB.Message.GetFirstPartByID(ctx, key.Receiver, ids.MakeMessageID(&tg.PeerChannel{ChannelID: channelID}, postID))
		if err != nil || post == nil || post.MXID == "" {
			continue
		}
		tc.readCommentThread(ctx, room, post.MXID, eventID)
	}
	tc.metadata.CommentThreadsRead = true
	if err = tc.userLogin.Save(ctx); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save login after marking comment threads read")
	}
	zerolog.Ctx(ctx).Info().Int("threads", len(threads)).Msg("Marked existing comment threads read")
}

// removeCommentGhosts makes the ghosts of comment authors leave channel rooms, once per room.
// Comments were sent by the authors' ghosts before the commenter account was used, and the ghosts
// stayed in the room: hundreds in big channels, each name or avatar change of theirs showing up in
// the room. Posts of a channel are always sent by the channel's ghost, no other ghost needs to stay.
func (tc *TelegramClient) removeCommentGhosts(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	bot, ok := tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok {
		return
	}
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, tc.userLogin.UserLogin)
	if err != nil {
		log.Err(err).Msg("Failed to get portals to remove comment authors from")
		return
	}
	for _, up := range userPortals {
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		channelID, ok := tc.isBroadcastPortal(portal)
		meta := portal.Metadata.(*PortalMetadata)
		if !ok || meta.CommentGhostsRemoved {
			continue
		}
		members, err := bot.Matrix().JoinedMembers(ctx, portal.MXID)
		if err != nil {
			log.Err(err).Stringer("room_id", portal.MXID).Msg("Failed to get members to remove comment authors")
			continue
		}
		removed, failed := 0, false
		for userID := range members.Joined {
			ghostID, isGhost := tc.main.Bridge.Matrix.ParseGhostMXID(userID)
			if !isGhost || ghostID == commenterGhostID || ghostID == ids.MakeChannelUserID(channelID) {
				continue
			}
			if _, err = bot.Connector.AS.Intent(userID).LeaveRoom(ctx, portal.MXID); err != nil {
				log.Err(err).Stringer("user_id", userID).Stringer("room_id", portal.MXID).Msg("Failed to remove comment author from channel room")
				failed = true
				continue
			}
			removed++
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
		if failed {
			// Try again on the next start.
			continue
		}
		meta.CommentGhostsRemoved = true
		if err = portal.Save(ctx); err != nil {
			log.Err(err).Msg("Failed to save comment author removal state")
		}
		if removed > 0 {
			log.Info().Stringer("room_id", portal.MXID).Int("ghosts", removed).Msg("Removed comment authors from channel room")
		}
	}
}

// fixCommentMedia corrects links to media of comments mirrored before the links named the chat the
// comment is in, once per channel room. They named the channel with the comment's message ID in the
// discussion group, so the media couldn't be downloaded. The comments get their media again by an
// edit (or are sent again if the author's ghost sent them).
func (tc *TelegramClient) fixCommentMedia(ctx context.Context) {
	log := zerolog.Ctx(ctx)
	bot, ok := tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok || !tc.main.useDirectMedia {
		return
	}
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, tc.userLogin.UserLogin)
	if err != nil {
		log.Err(err).Msg("Failed to get portals to fix comment media")
		return
	}
	for _, up := range userPortals {
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		channelID, ok := tc.isBroadcastPortal(portal)
		meta := portal.Metadata.(*PortalMetadata)
		if !ok || meta.CommentMediaFixed {
			continue
		}
		broken, err := tc.findBrokenCommentMedia(ctx, bot, portal.MXID, channelID)
		if err != nil {
			log.Err(err).Stringer("room_id", portal.MXID).Msg("Failed to find comment media to fix")
			continue
		}
		fixed, failed := 0, false
		for groupID, mirrors := range broken {
			n, err := tc.refreshComments(ctx, groupID, mirrors)
			fixed += n
			if err != nil {
				log.Err(err).Int64("group_id", groupID).Msg("Failed to fix comment media")
				failed = true
			}
			if ctx.Err() != nil {
				return
			}
		}
		if failed {
			// Try again on the next start.
			continue
		}
		meta.CommentMediaFixed = true
		if err = portal.Save(ctx); err != nil {
			log.Err(err).Msg("Failed to save comment media fix state")
		}
		if fixed > 0 {
			log.Info().Stringer("room_id", portal.MXID).Int("comments", fixed).Msg("Fixed media links of comments")
		}
	}
}

// findBrokenCommentMedia returns the mirrored comments of a channel room whose media link names the
// channel instead of the discussion group, by group.
func (tc *TelegramClient) findBrokenCommentMedia(ctx context.Context, bot *matrix.ASIntent, room id.RoomID, channelID int64) (map[int64][]*commentMirror, error) {
	broken := make(map[int64][]*commentMirror)
	filter := &mautrix.FilterPart{ContainsURL: ptr.Ptr(true)}
	from := ""
	for {
		resp, err := bot.Matrix().Messages(ctx, room, from, "", mautrix.DirectionBackward, filter, 500)
		if err != nil {
			return nil, err
		}
		for _, evt := range resp.Chunk {
			mirror, err := tc.getCommentMirrorByEvent(ctx, evt.ID)
			if err != nil {
				return nil, err
			} else if mirror == nil {
				continue
			}
			if err = evt.Content.ParseRaw(evt.Type); err != nil {
				continue
			}
			content, ok := evt.Content.Parsed.(*event.MessageEventContent)
			if !ok {
				continue
			}
			url := content.URL
			if content.File != nil {
				url = content.File.URL
			}
			mediaID, err := bot.Connector.ParseContentURI(ctx, url)
			if err != nil {
				continue
			}
			info, err := ids.ParseDirectMediaInfo(mediaID)
			if err == nil && info.PeerType == ids.PeerTypeChannel && info.PeerID == channelID && info.PeerID != mirror.GroupID {
				broken[mirror.GroupID] = append(broken[mirror.GroupID], mirror)
			}
		}
		if resp.End == "" || len(resp.Chunk) == 0 {
			return broken, nil
		}
		from = resp.End
	}
}

// refreshComments fetches comments from the discussion group and updates their mirrors.
func (tc *TelegramClient) refreshComments(ctx context.Context, groupID int64, mirrors []*commentMirror) (int, error) {
	channel, err := tc.inputChannel(ctx, groupID)
	if err != nil {
		return 0, err
	}
	updated := 0
	for batch := range slices.Chunk(mirrors, 100) {
		byID := make(map[int]*commentMirror, len(batch))
		inputIDs := make([]tg.InputMessageClass, 0, len(batch))
		for _, mirror := range batch {
			byID[mirror.MsgID] = mirror
			inputIDs = append(inputIDs, &tg.InputMessageID{ID: mirror.MsgID})
		}
		resp, err := commentAPICall(ctx, tc, func() (tg.ModifiedMessagesMessages, error) {
			res, err := tc.client.API().ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: channel, ID: inputIDs})
			if err != nil {
				return nil, err
			}
			modified, ok := res.AsModified()
			if !ok {
				return nil, fmt.Errorf("unexpected response type %T", res)
			}
			return modified, nil
		})
		if err != nil {
			return updated, err
		}
		for _, m := range resp.GetMessages() {
			// Deleted comments come back empty, their mirrors are removed with the deletion.
			if msg, ok := m.(*tg.Message); ok && byID[msg.ID] != nil {
				tc.updateMirroredComment(ctx, groupID, byID[msg.ID], msg, time.Now())
				updated++
			}
		}
	}
	return updated, nil
}

// syncExistingComments fetches the comments of channel rooms bridged before comments were mirrored,
// once per room.
func (tc *TelegramClient) syncExistingComments(ctx context.Context) {
	tc.readExistingCommentThreads(ctx)
	tc.removeCommentGhosts(ctx)
	tc.fixCommentMedia(ctx)
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, tc.userLogin.UserLogin)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to get portals to sync comments")
		return
	}
	for _, up := range userPortals {
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		channelID, ok := tc.isBroadcastPortal(portal)
		meta := portal.Metadata.(*PortalMetadata)
		if !ok || meta.CommentsSynced || tc.folderSpaces.skipArchivedPeer(&tg.PeerChannel{ChannelID: channelID}) {
			continue
		}
		posts, err := tc.latestPostsWithComments(ctx, portal, channelID, 50)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Stringer("portal_key", portal.PortalKey).Msg("Failed to check posts for comments")
			continue
		}
		if !tc.backfillComments(channelID, postIDsWithComments(posts)) {
			// Try again on the next start.
			continue
		}
		meta.CommentsSynced = true
		if err = portal.Save(ctx); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment sync state")
		}
		// Spread the work, every channel is a few requests.
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

var errCommentSent = bridgev2.WrapErrorInStatus(errors.New("sent as a comment")).
	WithStatus(event.MessageStatusSuccess).
	WithIsCertain(true).
	WithSendNotice(false)

// maybeSendComment sends a thread reply in a channel room as a comment to the post.
func (tc *TelegramClient) maybeSendComment(ctx context.Context, msg *bridgev2.MatrixMessage) (handled bool, err error) {
	// bridgev2 only resolves msg.ThreadRoot for networks that declare thread support, so read the
	// thread from the event itself.
	threadParent := msg.Content.RelatesTo.GetThreadParent()
	if threadParent == "" {
		return false, nil
	}
	channelID, ok := tc.isBroadcastPortal(msg.Portal)
	if !ok {
		return false, nil
	}
	// From here on the message is never sent as a channel post, which is what a thread reply would
	// otherwise become.
	root := msg.ThreadRoot
	if root == nil {
		if root, err = tc.main.Bridge.DB.Message.GetPartByMXID(ctx, threadParent); err != nil {
			return true, fmt.Errorf("failed to find the post of the thread: %w", err)
		}
	}
	if root == nil {
		return true, fmt.Errorf("the post of this thread isn't known to the bridge")
	}
	_, postID, err := ids.ParseMessageID(root.ID)
	if err != nil {
		return true, fmt.Errorf("the thread doesn't start at a channel post")
	}
	thread, err := tc.commentThreadForPost(ctx, channelID, postID)
	if err != nil {
		return true, fmt.Errorf("failed to find comments of post: %w", err)
	} else if thread == nil {
		return true, fmt.Errorf("this post has no comments in Telegram")
	}
	replyToID := thread.CopyID
	if replyEvent := msg.Content.RelatesTo.GetNonFallbackReplyTo(); replyEvent != "" && replyEvent != threadParent {
		if target, err := tc.getCommentMirrorByEvent(ctx, replyEvent); err == nil && target != nil && target.GroupID == thread.GroupID {
			replyToID = target.MsgID
		}
	}
	group, err := tc.inputChannel(ctx, thread.GroupID)
	if err != nil {
		return true, err
	}
	peer := &tg.InputPeerChannel{ChannelID: group.ChannelID, AccessHash: group.AccessHash}
	replyTo := &tg.InputReplyToMessage{ReplyToMsgID: replyToID, TopMsgID: thread.CopyID}
	isMedia := msg.Content.MsgType.IsMedia() || msg.Event.Type == event.EventSticker
	text, entities := matrixfmt.Parse(ctx, tc.matrixParser, msg.Content, msg.Portal, tc.getMaxMessageLength(ctx, isMedia))
	var randomID [8]byte
	_, _ = rand.Read(randomID[:])
	var send func() (tg.UpdatesClass, error)
	if isMedia {
		// Uploaded to Telegram before taking the comment lock, which a large file would hold up.
		forceDocument, _ := msg.Event.Content.Raw["fi.mau.telegram.force_document"].(bool)
		media, err := tc.transferMediaToTelegram(ctx, msg.Content, msg.Event.Type == event.EventSticker, false, forceDocument)
		if err != nil {
			return true, err
		}
		send = func() (tg.UpdatesClass, error) {
			return tc.client.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
				Peer:     peer,
				Message:  text,
				Entities: entities,
				Media:    media,
				ReplyTo:  replyTo,
				RandomID: int64(binary.LittleEndian.Uint64(randomID[:])),
			})
		}
	} else {
		send = func() (tg.UpdatesClass, error) {
			return tc.client.API().MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
				Peer:     peer,
				Message:  text,
				Entities: entities,
				ReplyTo:  replyTo,
				RandomID: int64(binary.LittleEndian.Uint64(randomID[:])),
			})
		}
	}
	if err = tc.sendComment(ctx, thread, msg, send); err != nil {
		if tgerr.Is(err, tg.ErrChatGuestSendForbidden) {
			return true, tc.joinDiscussionNeededError(ctx, thread.GroupID, err)
		}
		return true, err
	}
	return true, errCommentSent
}

// joinDiscussionNeededError tells the user how to join the discussion group when it only lets members
// comment, as Telegram apps offer to join first.
func (tc *TelegramClient) joinDiscussionNeededError(ctx context.Context, groupID int64, err error) error {
	name := tc.tl("the discussion group")
	var link string
	if channel, chErr := tc.getSingleChannel(ctx, groupID); chErr == nil {
		name = channel.Title
		if channel.Username != "" {
			link = " https://t.me/" + channel.Username
		}
	}
	text := fmt.Sprintf(tc.tl("Only members of %s can comment. To join it, send `%s join-discussion` in this room."), name, tc.main.Bridge.Config.CommandPrefix) + link
	return bridgev2.WrapErrorInStatus(err).WithMessage(text).WithIsCertain(true).WithSendNotice(true)
}

// sendComment sends a comment written in Matrix and records the Matrix event as its mirror. Telegram
// can deliver the new comment as an update before the request returns; the comment lock keeps
// mirrorComment from mirroring it back into the thread until the mirror is saved.
func (tc *TelegramClient) sendComment(ctx context.Context, thread *commentThread, msg *bridgev2.MatrixMessage, send func() (tg.UpdatesClass, error)) error {
	tc.comments.lock.Lock()
	defer tc.comments.lock.Unlock()
	updates, err := send()
	if err != nil {
		return fmt.Errorf("failed to send comment: %w", err)
	}
	if err = tc.markCommentParticipation(ctx, thread.GroupID, thread.CopyID); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save comment thread participation")
	}
	if newID := sentMessageID(updates); newID != 0 {
		if err = tc.saveCommentMirror(ctx, commentMirror{
			GroupID: thread.GroupID, MsgID: newID, CopyID: thread.CopyID, RoomID: msg.Portal.MXID, EventID: msg.Event.ID,
		}); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save sent comment")
		}
	} else {
		zerolog.Ctx(ctx).Warn().Msg("Telegram didn't return the ID of the sent comment, it may appear twice in the thread")
	}
	return nil
}

// sentMessageID finds the ID of a sent message in the updates Telegram returned.
func sentMessageID(updates tg.UpdatesClass) int {
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	case *tg.UpdateShortSentMessage:
		return u.ID
	}
	for _, upd := range list {
		switch u := upd.(type) {
		case *tg.UpdateMessageID:
			return u.ID
		case *tg.UpdateNewChannelMessage:
			return u.Message.GetID()
		}
	}
	return 0
}

var cmdJoinDiscussion = &commands.FullHandler{
	Func: fnJoinDiscussion,
	Name: "join-discussion",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Join the discussion group of this channel, for comments that need membership",
	},
	RequiresLogin:  true,
	RequiresPortal: true,
}

func fnJoinDiscussion(ce *commands.Event) {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You're not logged in")
		return
	}
	tc := login.Client.(*TelegramClient)
	channelID, ok := tc.isBroadcastPortal(ce.Portal)
	if !ok {
		ce.Reply("This command only works in the room of a channel.")
		return
	}
	channel, err := tc.inputChannel(ce.Ctx, channelID)
	if err != nil {
		ce.Reply("Failed to find the channel: %v", err)
		return
	}
	full, err := commentAPICall(ce.Ctx, tc, func() (*tg.MessagesChatFull, error) {
		return tc.client.API().ChannelsGetFullChannel(ce.Ctx, channel)
	})
	if err != nil {
		ce.Reply("Failed to get the channel's discussion group: %v", err)
		return
	}
	channelFull, ok := full.FullChat.(*tg.ChannelFull)
	if !ok || channelFull.LinkedChatID == 0 {
		ce.Reply("This channel has no discussion group.")
		return
	}
	var group *tg.Channel
	for _, c := range full.Chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == channelFull.LinkedChatID {
			group = ch
		}
	}
	if group == nil {
		ce.Reply("Failed to get the channel's discussion group: %v", "not in the response")
		return
	}
	// Joining makes Telegram send the group, the bridge creates its room like for any new chat.
	if _, err = tc.client.API().ChannelsJoinChannel(ce.Ctx, group.AsInput()); err != nil {
		if tgerr.Is(err, tg.ErrUserAlreadyParticipant) {
			ce.Reply("You're already a member of %s", html.EscapeString(group.Title))
			return
		}
		ce.Reply("Failed to join chat: %v", err)
		return
	}
	ce.Reply("Joined %s, comments to this channel can be sent now.", html.EscapeString(group.Title))
}
