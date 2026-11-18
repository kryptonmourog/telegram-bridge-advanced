// mautrix-telegram - A Matrix-Telegram puppeting bridge.
// Copyright (C) 2025 Sumner Evans
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

	"go.mau.fi/util/exsync"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/gotd/crypto"
	"go.mau.fi/mautrix-telegram/pkg/gotd/session"
)

func (tc *TelegramConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Ghost:     func() any { return &GhostMetadata{} },
		Portal:    func() any { return &PortalMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
		Reaction:  nil,
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

type GhostMetadata struct {
	IsPremium bool `json:"is_premium,omitempty"`
	Deleted   bool `json:"deleted,omitempty"`
	NotMin    bool `json:"not_min,omitempty"`

	ContactSource   int64 `json:"contact_source,omitempty"`
	SourceIsContact bool  `json:"source_is_contact,omitempty"`
}

func (gm *GhostMetadata) IsMin() bool {
	return !gm.NotMin
}

type PortalMetadata struct {
	// ChunkedBackfillPending is set while a forward backfill is sent in chunks, so that it can be
	// resumed after a restart (see chunkedbackfill.go).
	ChunkedBackfillPending bool `json:"chunked_backfill_pending,omitempty"`
	// CommentsSynced is set once the comments of the latest posts of a channel were fetched.
	CommentsSynced bool `json:"comments_synced,omitempty"`
	// CommentGhostsRemoved is set once the ghosts of comment authors have left the channel room.
	CommentGhostsRemoved bool `json:"fork_comment_ghosts_removed,omitempty"`
	// Settings are the user's settings of this room (see roomsettings.go).
	Settings *RoomSettings `json:"fork_settings,omitempty"`

	IsSuperGroup      bool          `json:"is_supergroup,omitempty"`
	IsForumGeneral    bool          `json:"is_forum_general,omitempty"`
	IsCommunity       bool          `json:"is_community,omitempty"`
	ReadUpTo          int           `json:"read_up_to,omitempty"` // FIXME this shouldn't be here
	AllowedReactions  []string      `json:"allowed_reactions"`
	LastSync          jsontime.Unix `json:"last_sync,omitempty"`
	FullSynced        bool          `json:"full_synced,omitempty"`
	ParticipantsCount int           `json:"member_count,omitempty"`

	SponsoredMessagePollTS  jsontime.Unix `json:"sponsored_message_poll_ts,omitempty"`
	SponsoredMessageEventID id.EventID    `json:"sponsored_message_event_id,omitempty"`
	// SponsoredMessageRoomID is where the last ad was sent; empty means the channel room.
	SponsoredMessageRoomID    id.RoomID           `json:"sponsored_message_room_id,omitempty"`
	SponsoredMessageRandomID  []byte              `json:"sponsored_message_random_id,omitempty"`
	LastMessageOnSponsorFetch networkid.MessageID `json:"last_message_on_sponsor_fetch,omitempty"`

	sponsoredMessageLock sync.Mutex
	sponsoredMessageSeen *exsync.Set[int64]
}

func (pm *PortalMetadata) SetIsSuperGroup(isSupergroup bool) (changed bool) {
	changed = pm.IsSuperGroup != isSupergroup
	pm.IsSuperGroup = isSupergroup
	return changed
}

func (pm *PortalMetadata) SetIsCommunity(isCommunity bool) (changed bool) {
	changed = pm.IsCommunity != isCommunity
	pm.IsCommunity = isCommunity
	return changed
}

func (pm *PortalMetadata) SetIsForumGeneral(isForumGeneral bool) (changed bool) {
	changed = pm.IsForumGeneral != isForumGeneral
	pm.IsForumGeneral = isForumGeneral
	return changed
}

type MessageMetadata struct {
	ViewLimited *event.BeeperViewLimitedMedia `json:"view_limited,omitempty"`
	ContentHash []byte                        `json:"content_hash,omitempty"`
	ContentURI  id.ContentURIString           `json:"content_uri,omitempty"`
	Poll        *PollMetadata                 `json:"poll,omitempty"`
	// Buttons is the bot keyboard of the message, see buttons.go.
	Buttons *MessageButtons `json:"buttons,omitempty"`
	// ButtonReactions are the number reactions the bridge bot put under the message, by key.
	ButtonReactions map[string]id.EventID `json:"button_reactions,omitempty"`
}

type PollMetadata struct {
	MatrixAnswerIDs map[string]string `json:"matrix_answer_ids,omitempty"`
}

type UserLoginMetadata struct {
	LoginPhone  string `json:"phone,omitempty"`
	LoginMethod string `json:"login_method,omitempty"`
	IsBot       bool   `json:"is_bot,omitempty"`
	// AdsDisabled is the old form of AdsMode "off".
	AdsDisabled bool `json:"ads_disabled,omitempty"`
	// LeftChats are rooms of chats not in Telegram's chat list anymore, see leftchats.go.
	LeftChats []networkid.PortalID `json:"left_chats,omitempty"`
	// CommentThreadsRead is set once the comment threads mirrored before threads were marked read
	// automatically have been marked read.
	CommentThreadsRead bool `json:"comment_threads_read,omitempty"`
	// AdsMode is how sponsored messages are shown, see adsroom.go. Empty means "room".
	AdsMode string `json:"ads_mode,omitempty"`
	// AdsRoom is the room sponsored messages go to in "room" mode.
	AdsRoom   id.RoomID        `json:"ads_room,omitempty"`
	Session   UserLoginSession `json:"session"`
	TakeoutID int64            `json:"takeout_id,omitempty"`

	DialogSyncComplete   bool               `json:"takeout_portal_crawl_done,omitempty"`
	DialogSyncCursor     networkid.PortalID `json:"takeout_portal_crawl_cursor,omitempty"`
	DialogSyncCount      int                `json:"dialog_sync_count,omitempty"`
	DialogSyncOffsetID   int                `json:"dialog_sync_offset_id,omitempty"`
	DialogSyncOffsetDate int                `json:"dialog_sync_offset_date,omitempty"`

	PinnedDialogs []networkid.PortalID `json:"pinned_dialogs,omitempty"`

	FolderSpaces *FolderSpacesMetadata `json:"folder_spaces,omitempty"`

	PushEncryptionKey []byte `json:"push_encryption_key,omitempty"`
}

func (u *UserLoginMetadata) ResetOnLogout() {
	u.Session.AuthKey = nil
	u.TakeoutID = 0
	u.DialogSyncComplete = false
	u.DialogSyncCursor = networkid.PortalID("")
	u.DialogSyncCount = 0
	u.DialogSyncOffsetID = 0
	u.DialogSyncOffsetDate = 0
	u.PushEncryptionKey = nil
}

type UserLoginSession struct {
	AuthKey       []byte `json:"auth_key,omitempty"`
	Datacenter    int    `json:"dc_id,omitempty"`
	ServerAddress string `json:"server_address,omitempty"`
	Salt          int64  `json:"salt,omitempty"`
}

func (u UserLoginSession) HasAuthKey() bool {
	return len(u.AuthKey) == 256
}

func (s *UserLoginSession) Load(_ context.Context) (*session.Data, error) {
	if !s.HasAuthKey() {
		return nil, session.ErrNotFound
	}
	keyID := crypto.Key(s.AuthKey).ID()
	return &session.Data{
		DC:        s.Datacenter,
		Addr:      s.ServerAddress,
		AuthKey:   s.AuthKey,
		AuthKeyID: keyID[:],
		Salt:      s.Salt,
	}, nil
}

func (s *UserLoginSession) Save(ctx context.Context, data *session.Data) error {
	s.Datacenter = data.DC
	s.ServerAddress = data.Addr
	s.AuthKey = data.AuthKey
	s.Salt = data.Salt
	// TODO save UserLogin to database?
	return nil
}

func updatePortalLastSyncAt(_ context.Context, portal *bridgev2.Portal) bool {
	meta := portal.Metadata.(*PortalMetadata)
	meta.LastSync = jsontime.UnixNow()
	return true
}
