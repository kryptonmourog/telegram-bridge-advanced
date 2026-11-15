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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

type FolderSpacesConfig struct {
	Enabled                   bool   `yaml:"enabled"`
	DefaultLanguage           string `yaml:"default_language"`
	PrioritizePersonalFolders bool   `yaml:"prioritize_personal_folders"`
}

// FolderSpacesMetadata is the per-login state of folder spaces, stored in UserLoginMetadata.
// It is replaced as a whole (copy-on-write) so that a concurrent save never sees a map being modified.
type FolderSpacesMetadata struct {
	// Language chosen with `folders lang`, empty means the configured default.
	Language string `json:"language,omitempty"`
	// AllChats is the "All chats" sub-space, Folders the sub-space of each Telegram folder ID.
	AllChats id.RoomID         `json:"all_chats,omitempty"`
	Folders  map[int]id.RoomID `json:"folders,omitempty"`
	// Names that were last set for each sub-space.
	Names map[id.RoomID]string `json:"names,omitempty"`
	// RootChildren are the sub-spaces linked from the root space with their order.
	RootChildren map[id.RoomID]string `json:"root_children,omitempty"`
	// Children are the rooms linked from each sub-space.
	Children map[id.RoomID][]id.RoomID `json:"children,omitempty"`

	// ArchiveEnabled is set with `archive on`: archived chats are bridged into the ArchiveSpace.
	ArchiveEnabled bool      `json:"archive_enabled,omitempty"`
	ArchiveSpace   id.RoomID `json:"archive_space,omitempty"`
	// Archived are the archived chats (folderPeerID format), as last known.
	Archived []string `json:"archived,omitempty"`
	// LeftRooms are rooms of archived chats the user was made to leave while the archive is off.
	LeftRooms []id.RoomID `json:"left_rooms,omitempty"`
}

func (m *FolderSpacesMetadata) clone() *FolderSpacesMetadata {
	if m == nil {
		return &FolderSpacesMetadata{}
	}
	out := &FolderSpacesMetadata{
		Language:     m.Language,
		AllChats:     m.AllChats,
		Folders:      maps.Clone(m.Folders),
		Names:        maps.Clone(m.Names),
		RootChildren: maps.Clone(m.RootChildren),
		Children:     make(map[id.RoomID][]id.RoomID, len(m.Children)),

		ArchiveEnabled: m.ArchiveEnabled,
		ArchiveSpace:   m.ArchiveSpace,
		Archived:       slices.Clone(m.Archived),
		LeftRooms:      slices.Clone(m.LeftRooms),
	}
	for space, children := range m.Children {
		out.Children[space] = slices.Clone(children)
	}
	return out
}

var folderSpaceStrings = map[string]map[string]string{
	"en": {
		"all_chats":       "All chats",
		"all_chats_topic": "All your bridged Telegram chats",
		"folder_topic":    "Telegram folder",
		"archive":         "🗄 Archive",
		"archive_topic":   "Archived Telegram chats",
	},
	"ru": {
		"all_chats":       "Все чаты",
		"all_chats_topic": "Все ваши чаты Telegram, перенесённые в Matrix",
		"folder_topic":    "Папка Telegram",
		"archive":         "🗄 Архив",
		"archive_topic":   "Архивные чаты Telegram",
	},
}

func folderSpaceText(lang, key string) string {
	if s, ok := folderSpaceStrings[lang][key]; ok {
		return s
	}
	return folderSpaceStrings["en"][key]
}

func isSupportedFolderSpaceLanguage(lang string) bool {
	_, ok := folderSpaceStrings[lang]
	return ok
}

func supportedFolderSpaceLanguages() []string {
	return slices.Sorted(maps.Keys(folderSpaceStrings))
}

const (
	folderSpacesAfterConnectDelay = 30 * time.Second
	folderSpacesAfterChangeDelay  = 3 * time.Second
	folderSpacesAfterCreateDelay  = 15 * time.Second
)

// folderSpaceManager keeps Matrix spaces in line with the Telegram folders of one login.
type folderSpaceManager struct {
	tc *TelegramClient

	// lock serializes fetching folders and reconciling spaces (which may take a while).
	lock    sync.Mutex
	entries []folderEntry
	// needsRefetch is atomic so that Telegram update handlers never wait for a running reconcile.
	needsRefetch atomic.Bool

	// rootStateChecked is set once the root space's actual state has been cleaned up in this run.
	rootStateChecked bool
	// isBot caches whether a Telegram user is a bot (ghosts don't reliably store it). Guarded by lock.
	isBot map[int64]bool
	// archive tracks the archived chats, see archive.go.
	archive archiveSet
	// favouriteTagged holds pinned rooms already tagged as favourite in this run. Guarded by lock.
	favouriteTagged map[id.RoomID]struct{}

	scheduleLock sync.Mutex
	scheduled    bool
	warnedPFS    bool
}

func newFolderSpaceManager(tc *TelegramClient) *folderSpaceManager {
	fs := &folderSpaceManager{tc: tc}
	fs.needsRefetch.Store(true)
	return fs
}

func (fs *folderSpaceManager) enabled() bool {
	tc := fs.tc
	if !tc.main.Config.FolderSpaces.Enabled || tc.metadata.IsBot {
		return false
	}
	if tc.main.Bridge.Config.PersonalFilteringSpaces {
		fs.scheduleLock.Lock()
		if !fs.warnedPFS {
			fs.warnedPFS = true
			tc.main.Bridge.Log.Warn().Msg("folder_spaces is enabled, but bridge.personal_filtering_spaces is also enabled; folder spaces are disabled until personal_filtering_spaces is set to false")
		}
		fs.scheduleLock.Unlock()
		return false
	}
	return true
}

func (fs *folderSpaceManager) language() string {
	if meta := fs.tc.metadata.FolderSpaces; meta != nil && meta.Language != "" {
		return meta.Language
	}
	if lang := fs.tc.main.Config.FolderSpaces.DefaultLanguage; lang != "" {
		return lang
	}
	return "en"
}

// foldersChanged marks the folder list as outdated and schedules a reconcile.
func (fs *folderSpaceManager) foldersChanged() {
	fs.foldersChangedAfter(folderSpacesAfterChangeDelay)
}

// foldersChangedAfter is foldersChanged with a custom delay (e.g. after connecting, when folders
// may have changed while the bridge was offline).
func (fs *folderSpaceManager) foldersChangedAfter(delay time.Duration) {
	if !fs.enabled() {
		return
	}
	fs.needsRefetch.Store(true)
	fs.schedule(delay)
}

// folderSpacesPeriodicInterval is how often spaces are reconciled without a trigger. Rooms created
// or re-IDed (e.g. a group upgraded to a supergroup) without an event that schedules a reconcile
// would otherwise stay out of all spaces until something else triggered one.
const folderSpacesPeriodicInterval = 10 * time.Minute

func (fs *folderSpaceManager) periodicReconcile(ctx context.Context) {
	ticker := time.NewTicker(folderSpacesPeriodicInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fs.schedule(0)
		}
	}
}

// schedule runs a reconcile after the delay. While one is pending, further calls are no-ops,
// so a burst of events results in a single reconcile.
func (fs *folderSpaceManager) schedule(delay time.Duration) {
	if !fs.enabled() {
		return
	}
	fs.scheduleLock.Lock()
	defer fs.scheduleLock.Unlock()
	if fs.scheduled {
		return
	}
	fs.scheduled = true
	time.AfterFunc(delay, func() {
		fs.scheduleLock.Lock()
		fs.scheduled = false
		fs.scheduleLock.Unlock()
		ctx := fs.tc.clientCtx
		if ctx == nil || ctx.Err() != nil {
			return
		}
		log := fs.tc.main.Bridge.Log.With().
			Str("component", "folder spaces").
			Str("user_login_id", string(fs.tc.loginID)).
			Logger()
		if err := fs.reconcile(log.WithContext(ctx)); err != nil {
			log.Err(err).Msg("Failed to reconcile folder spaces")
		}
	})
}

// afterPossibleCreate schedules a reconcile if the event may have created a room for the chat.
func (fs *folderSpaceManager) afterPossibleCreate(ctx context.Context, portalKey networkid.PortalKey) {
	if !fs.enabled() {
		return
	}
	portal, err := fs.tc.main.Bridge.GetExistingPortalByKey(ctx, portalKey)
	if err == nil && portal != nil && portal.MXID != "" {
		return
	}
	fs.schedule(folderSpacesAfterCreateDelay)
}

// currentEntries returns the folder list, fetching it if needed. Must be called with fs.lock held.
func (fs *folderSpaceManager) currentEntries(ctx context.Context) ([]folderEntry, error) {
	// Swap before fetching: a folder update arriving during the fetch sets the flag again.
	if !fs.needsRefetch.Swap(false) && fs.entries != nil {
		return fs.entries, nil
	}
	resp, err := fs.tc.client.API().MessagesGetDialogFilters(ctx)
	if err != nil {
		fs.needsRefetch.Store(true)
		return nil, fmt.Errorf("failed to get dialog filters: %w", err)
	}
	fs.entries = buildFolderEntries(resp.Filters, fs.tc.telegramUserID)
	zerolog.Ctx(ctx).Debug().Int("entries", len(fs.entries)).Msg("Fetched Telegram folders")
	return fs.entries, nil
}

// deleteSubspaces deletes the "All chats" and folder sub-spaces (on logout). The root space is
// deleted by the bridge library together with the login.
func (fs *folderSpaceManager) deleteSubspaces(ctx context.Context) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	meta := fs.tc.metadata.FolderSpaces
	if meta == nil {
		return
	}
	rooms := slices.Collect(maps.Values(meta.Folders))
	for _, room := range []id.RoomID{meta.AllChats, meta.ArchiveSpace} {
		if room != "" {
			rooms = append(rooms, room)
		}
	}
	for _, room := range rooms {
		if err := fs.tc.main.Bridge.Bot.DeleteRoom(ctx, room, false); err != nil {
			zerolog.Ctx(ctx).Err(err).Stringer("space_id", room).Msg("Failed to delete folder sub-space")
		}
	}
	fs.tc.metadata.FolderSpaces = nil
}

// Entries returns the folder list for chat sync prioritization, fetching it if needed.
func (fs *folderSpaceManager) Entries(ctx context.Context) ([]folderEntry, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	return fs.currentEntries(ctx)
}

// reconcile brings the root space, the sub-spaces and their room links in line with the Telegram folders.
func (fs *folderSpaceManager) reconcile(ctx context.Context) error {
	if !fs.enabled() {
		return nil
	}
	fs.lock.Lock()
	defer fs.lock.Unlock()
	log := zerolog.Ctx(ctx)
	tc := fs.tc
	ul := tc.userLogin

	entries, err := fs.currentEntries(ctx)
	if err != nil {
		return err
	}
	root, err := fs.ensureRootSpace(ctx)
	if err != nil {
		return err
	}
	lang := fs.language()
	meta := tc.metadata.FolderSpaces.clone()
	fs.archive.load(meta.Archived)
	if !fs.archive.fetched {
		if err = fs.fetchArchived(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to fetch archived chats, using the last known list")
		}
	}
	archiveEnabled := meta.ArchiveEnabled
	if archiveEnabled {
		entries = append(slices.Clone(entries), folderEntry{Archive: true})
	}
	left := make(map[id.RoomID]struct{}, len(meta.LeftRooms))
	for _, room := range meta.LeftRooms {
		left[room] = struct{}{}
	}
	if meta.Folders == nil {
		meta.Folders = map[int]id.RoomID{}
	}
	if meta.Names == nil {
		meta.Names = map[id.RoomID]string{}
	}
	if meta.RootChildren == nil {
		meta.RootChildren = map[id.RoomID]string{}
	}
	stats := struct{ created, deleted, linked, unlinked int }{}

	// 1. One sub-space per entry, in Telegram's order.
	subspaceOf := make([]id.RoomID, len(entries))
	folderIDs := map[int]struct{}{}
	wantRoot := map[id.RoomID]string{}
	for i, entry := range entries {
		var name, topic, key string
		var room id.RoomID
		switch {
		case entry.AllChats:
			name, topic, key = folderSpaceText(lang, "all_chats"), folderSpaceText(lang, "all_chats_topic"), "__folder_space__:all"
			room = meta.AllChats
		case entry.Archive:
			name, topic, key = folderSpaceText(lang, "archive"), folderSpaceText(lang, "archive_topic"), "__folder_space__:archive"
			room = meta.ArchiveSpace
		default:
			folderIDs[entry.Folder.ID] = struct{}{}
			name, topic, key = entry.Folder.SpaceName(), folderSpaceText(lang, "folder_topic"), fmt.Sprintf("__folder_space__:%d", entry.Folder.ID)
			room = meta.Folders[entry.Folder.ID]
		}
		if room == "" {
			room, err = fs.createSpace(ctx, name, topic, key, root)
			if err != nil {
				return err
			}
			stats.created++
		} else if meta.Names[room] != name {
			if err = fs.setName(ctx, room, name); err != nil {
				return err
			}
		}
		meta.Names[room] = name
		switch {
		case entry.AllChats:
			meta.AllChats = room
		case entry.Archive:
			meta.ArchiveSpace = room
		default:
			meta.Folders[entry.Folder.ID] = room
		}
		subspaceOf[i] = room
		wantRoot[room] = fmt.Sprintf("%04d", i)
	}

	// 2. Sub-spaces of folders deleted in Telegram.
	for folderID, room := range meta.Folders {
		if _, ok := folderIDs[folderID]; ok {
			continue
		}
		if err = tc.main.Bridge.Bot.DeleteRoom(ctx, room, false); err != nil {
			log.Err(err).Stringer("space_id", room).Msg("Failed to delete sub-space of removed folder")
		}
		delete(meta.Folders, folderID)
		delete(meta.Names, room)
		delete(meta.Children, room)
		stats.deleted++
	}
	if !archiveEnabled && meta.ArchiveSpace != "" {
		if err = tc.main.Bridge.Bot.DeleteRoom(ctx, meta.ArchiveSpace, false); err != nil {
			log.Err(err).Stringer("space_id", meta.ArchiveSpace).Msg("Failed to delete archive sub-space")
		}
		delete(meta.Names, meta.ArchiveSpace)
		delete(meta.Children, meta.ArchiveSpace)
		meta.ArchiveSpace = ""
		stats.deleted++
	}

	// 3. Root space contains only the sub-spaces.
	for room, order := range wantRoot {
		if meta.RootChildren[room] != order {
			if err = fs.setSpaceChild(ctx, root, room, order, true); err != nil {
				return err
			}
			meta.RootChildren[room] = order
		}
	}
	for room := range meta.RootChildren {
		if _, ok := wantRoot[room]; !ok {
			if err = fs.setSpaceChild(ctx, root, room, "", false); err != nil {
				return err
			}
			delete(meta.RootChildren, room)
		}
	}
	// Once per run, also remove links the bridge no longer knows about (e.g. rooms of deleted portals
	// that personal_filtering_spaces linked from the root before folder spaces were enabled).
	if !fs.rootStateChecked {
		stale, err := fs.staleSpaceChildren(ctx, root, wantRoot)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to read root space state, not removing unknown links")
		} else {
			for _, room := range stale {
				if err = fs.setSpaceChild(ctx, root, room, "", false); err != nil {
					return err
				}
				stats.unlinked++
			}
			fs.rootStateChecked = true
		}
	}

	// 4. Rooms of this login and the sub-spaces they belong in.
	userPortals, err := tc.main.Bridge.DB.UserPortal.GetAllForLogin(ctx, ul.UserLogin)
	if err != nil {
		return fmt.Errorf("failed to get portals of login: %w", err)
	}
	contactIDs := fs.contactIDs(ctx)
	fs.resolveBots(ctx, userPortals)
	want := make(map[id.RoomID]map[id.RoomID]struct{}, len(entries))
	for _, room := range subspaceOf {
		want[room] = map[id.RoomID]struct{}{}
	}
	for _, up := range userPortals {
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		// Rooms linked from the root by personal_filtering_spaces before folder spaces were enabled.
		if up.InSpace != nil && *up.InSpace {
			if err = fs.setSpaceChild(ctx, root, portal.MXID, "", false); err != nil {
				return err
			}
			notInSpace := false
			up.InSpace = &notInSpace
			if err = tc.main.Bridge.DB.UserPortal.Put(ctx, up); err != nil {
				return fmt.Errorf("failed to save user portal: %w", err)
			}
		}
		// Rooms of archived chats are hidden from the user while the archive is off.
		archived := false
		if peerID, err := folderPeerIDFromPortalID(portal.ID); err == nil {
			archived = fs.archive.contains(peerID)
		}
		fs.setArchiveMembership(ctx, portal, archived && !archiveEnabled, left)
		if archived && !archiveEnabled {
			continue
		}
		// Forum topics live in the forum's own space; the forum is placed instead. Channels of a
		// Telegram community also have a parent, but are placed like any other chat.
		if portal.ParentKey.ID != "" {
			if _, _, parentTopic, err := ids.ParsePortalID(portal.ParentKey.ID); err == nil && parentTopic == ids.TopicIDSpaceRoom {
				continue
			}
		}
		chat, ok := fs.folderChatForPortal(ctx, portal, contactIDs)
		if !ok {
			continue
		}
		chat.Archived = archived
		for i, entry := range entries {
			if entry.Contains(chat) {
				want[subspaceOf[i]][portal.MXID] = struct{}{}
			}
		}
	}

	// 5. Link and unlink rooms.
	newChildren := make(map[id.RoomID][]id.RoomID, len(want))
	for space, rooms := range want {
		had := map[id.RoomID]struct{}{}
		for _, room := range meta.Children[space] {
			had[room] = struct{}{}
		}
		for room := range rooms {
			if _, ok := had[room]; ok {
				continue
			}
			if err = fs.setSpaceChild(ctx, space, room, "", true); err != nil {
				return err
			}
			stats.linked++
		}
		for room := range had {
			if _, ok := rooms[room]; ok {
				continue
			}
			if err = fs.setSpaceChild(ctx, space, room, "", false); err != nil {
				return err
			}
			stats.unlinked++
		}
		newChildren[space] = slices.Sorted(maps.Keys(rooms))
	}
	meta.Children = newChildren
	meta.Archived = fs.archive.list()
	meta.LeftRooms = slices.Sorted(maps.Keys(left))

	tc.metadata.FolderSpaces = meta
	if err = ul.Save(ctx); err != nil {
		return fmt.Errorf("failed to save login after folder space reconcile: %w", err)
	}
	fs.tagPinnedFavourites(ctx)
	log.Info().
		Int("folders", len(entries)-1).
		Int("rooms", len(userPortals)).
		Int("spaces_created", stats.created).
		Int("spaces_deleted", stats.deleted).
		Int("links_added", stats.linked).
		Int("links_removed", stats.unlinked).
		Msg("Reconciled folder spaces")
	return nil
}

// tagPinnedFavourites tags rooms of pinned dialogs as favourite. bridgev2 tags a new room before the
// user's double puppet has joined it, which the homeserver refuses, so pinned chats bridged for the
// first time would otherwise never become favourites. Reconciles run after rooms are created, so
// this catches them. Must be called with fs.lock held.
func (fs *folderSpaceManager) tagPinnedFavourites(ctx context.Context) {
	tc := fs.tc
	if !slices.Contains(tc.main.Bridge.Config.OnlyBridgeTags, event.RoomTagFavourite) {
		return
	}
	dp := tc.userLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return
	}
	if fs.favouriteTagged == nil {
		fs.favouriteTagged = make(map[id.RoomID]struct{})
	}
	log := zerolog.Ctx(ctx)
	for _, portalID := range tc.metadata.PinnedDialogs {
		peerType, chatID, topicID, err := ids.ParsePortalID(portalID)
		if err != nil {
			continue
		}
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, tc.makePortalKeyFromID(peerType, chatID, topicID))
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		if _, ok := fs.favouriteTagged[portal.MXID]; ok {
			continue
		}
		if err = dp.TagRoom(ctx, portal.MXID, event.RoomTagFavourite, true); err != nil {
			log.Warn().Err(err).Stringer("room_id", portal.MXID).Msg("Failed to tag pinned chat as favourite")
			continue
		}
		fs.favouriteTagged[portal.MXID] = struct{}{}
	}
}

// staleSpaceChildren reads the space's current state from the homeserver and returns the linked
// children that are not in keep.
func (fs *folderSpaceManager) staleSpaceChildren(ctx context.Context, space id.RoomID, keep map[id.RoomID]string) ([]id.RoomID, error) {
	intent, ok := fs.tc.main.Bridge.Bot.(*matrix.ASIntent)
	if !ok {
		return nil, fmt.Errorf("bridge bot is %T, can't read room state", fs.tc.main.Bridge.Bot)
	}
	state, err := intent.Matrix.State(ctx, space)
	if err != nil {
		return nil, err
	}
	var stale []id.RoomID
	for stateKey, evt := range state[event.StateSpaceChild] {
		child := id.RoomID(stateKey)
		if _, ok := keep[child]; ok {
			continue
		}
		if content := evt.Content.AsSpaceChild(); content != nil && len(content.Via) > 0 {
			stale = append(stale, child)
		}
	}
	return stale, nil
}

// resolveBots asks Telegram which users of the login's private chats are bots, for users not cached
// yet (a user doesn't stop being a bot). Must be called with fs.lock held. On error, the users stay
// unknown and the ghost's stored flag is used instead.
func (fs *folderSpaceManager) resolveBots(ctx context.Context, userPortals []*database.UserPortal) {
	if fs.isBot == nil {
		fs.isBot = map[int64]bool{}
	}
	var unknown []tg.InputUserClass
	for _, up := range userPortals {
		pt, userID, _, err := ids.ParsePortalID(up.Portal.ID)
		if err != nil || pt != ids.PeerTypeUser || userID == fs.tc.telegramUserID {
			continue
		}
		if _, ok := fs.isBot[userID]; ok {
			continue
		}
		accessHash, err := fs.tc.ScopedStore.GetAccessHash(ctx, ids.PeerTypeUser, userID)
		if err != nil {
			continue
		}
		unknown = append(unknown, &tg.InputUser{UserID: userID, AccessHash: accessHash})
	}
	for chunk := range slices.Chunk(unknown, 100) {
		users, err := fs.tc.client.API().UsersGetUsers(ctx, chunk)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Int("count", len(chunk)).Msg("Failed to get users to detect bots for folder spaces")
			return
		}
		for _, u := range users {
			if user, ok := u.(*tg.User); ok {
				fs.isBot[user.ID] = user.Bot
			}
		}
	}
}

// contactIDs returns the Telegram user IDs in the contact list. On error the list is empty,
// which only affects the "contacts" / "non-contacts" rules until the next reconcile.
func (fs *folderSpaceManager) contactIDs(ctx context.Context) map[int64]struct{} {
	out := map[int64]struct{}{}
	contacts, err := fs.tc.getContactsCached(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get contacts for folder spaces")
		return out
	}
	for _, contact := range contacts.Contacts {
		out[contact.UserID] = struct{}{}
	}
	return out
}

// folderChatForPortal describes a bridged room for folder matching, using what the bridge has stored.
func (fs *folderSpaceManager) folderChatForPortal(ctx context.Context, portal *bridgev2.Portal, contactIDs map[int64]struct{}) (folderChat, bool) {
	pt, chatID, topicID, err := ids.ParsePortalID(portal.ID)
	if err != nil {
		return folderChat{}, false
	}
	fc := folderChat{PeerID: folderPeerID(pt, chatID)}
	switch pt {
	case ids.PeerTypeUser:
		if chatID == fs.tc.telegramUserID {
			fc.Kind = folderChatSelf
			break
		}
		fc.Kind = folderChatUser
		if bot, known := fs.isBot[chatID]; known {
			if bot {
				fc.Kind = folderChatBot
			}
		} else if ghost, err := fs.tc.main.Bridge.GetExistingGhostByID(ctx, ids.MakeUserID(chatID)); err == nil && ghost != nil && ghost.IsBot {
			fc.Kind = folderChatBot
		}
		_, fc.Contact = contactIDs[chatID]
	case ids.PeerTypeChat:
		fc.Kind = folderChatGroup
	case ids.PeerTypeChannel:
		fc.Kind = folderChatBroadcast
		meta, _ := portal.Metadata.(*PortalMetadata)
		if topicID != 0 || (meta != nil && meta.IsSuperGroup) {
			fc.Kind = folderChatGroup
		}
	default:
		return folderChat{}, false
	}
	return fc, true
}

// ensureRootSpace returns the login's root space, creating it if needed. The same room ID field as
// personal_filtering_spaces is used, so an existing personal space is reused as the root.
func (fs *folderSpaceManager) ensureRootSpace(ctx context.Context) (id.RoomID, error) {
	ul := fs.tc.userLogin
	if ul.SpaceRoom != "" {
		return ul.SpaceRoom, nil
	}
	netName := fs.tc.main.Bridge.Network.GetName()
	room, err := fs.createSpace(ctx,
		fmt.Sprintf("%s (%s)", netName.DisplayName, ul.RemoteName),
		fmt.Sprintf("Your %s bridged chats - %s", netName.DisplayName, ul.RemoteName),
		"__personal_filtering_space__", "")
	if err != nil {
		return "", err
	}
	ul.SpaceRoom = room
	if err = ul.Save(ctx); err != nil {
		return "", fmt.Errorf("failed to save root space ID: %w", err)
	}
	return room, nil
}

// createSpace creates a space owned by the bridge bot and invites (or joins, with double puppeting) the user.
func (fs *folderSpaceManager) createSpace(ctx context.Context, name, topic, key string, parent id.RoomID) (id.RoomID, error) {
	br := fs.tc.main.Bridge
	ul := fs.tc.userLogin
	autoJoin := br.Matrix.GetCapabilities().AutoJoinInvites
	doublePuppet := ul.User.DoublePuppet(ctx)
	req := &mautrix.ReqCreateRoom{
		Visibility:      "private",
		Name:            name,
		Topic:           topic,
		CreationContent: map[string]any{"type": event.RoomTypeSpace},
		PowerLevelOverride: &event.PowerLevelsEventContent{
			Users: map[id.UserID]int{
				br.Bot.GetMXID(): 9001,
				ul.UserMXID:      50,
			},
		},
		Invite:            []id.UserID{ul.UserMXID},
		BeeperLocalRoomID: br.Matrix.GenerateDeterministicRoomID(networkid.PortalKey{ID: networkid.PortalID(key), Receiver: ul.ID}),
	}
	if parent == "" {
		req.InitialState = append(req.InitialState, &event.Event{
			Type:    event.StateRoomAvatar,
			Content: event.Content{Parsed: &event.RoomAvatarEventContent{URL: br.Network.GetName().NetworkIcon}},
		})
	} else {
		parentKey := parent.String()
		req.InitialState = append(req.InitialState, &event.Event{
			Type:     event.StateSpaceParent,
			StateKey: &parentKey,
			Content:  event.Content{Parsed: &event.SpaceParentEventContent{Via: []string{br.Matrix.ServerName()}, Canonical: true}},
		})
	}
	if autoJoin {
		req.BeeperInitialMembers = []id.UserID{ul.UserMXID}
		req.BeeperAutoJoinInvites = true
	}
	room, err := br.Bot.CreateRoom(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to create space %q: %w", name, err)
	}
	if !autoJoin && doublePuppet != nil {
		if err = doublePuppet.EnsureJoined(ctx, room); err != nil {
			zerolog.Ctx(ctx).Err(err).Stringer("space_id", room).Msg("Failed to join space with double puppet")
		}
	}
	zerolog.Ctx(ctx).Debug().Stringer("space_id", room).Str("name", name).Msg("Created space")
	return room, nil
}

func (fs *folderSpaceManager) setName(ctx context.Context, room id.RoomID, name string) error {
	_, err := fs.tc.main.Bridge.Bot.SendState(ctx, room, event.StateRoomName, "", &event.Content{
		Parsed: &event.RoomNameEventContent{Name: name},
	}, time.Now())
	if err != nil {
		return fmt.Errorf("failed to rename space %s: %w", room, err)
	}
	return nil
}

// setSpaceChild links (present) or unlinks a room from a space. An empty m.space.child removes the link.
func (fs *folderSpaceManager) setSpaceChild(ctx context.Context, space, child id.RoomID, order string, present bool) error {
	content := &event.SpaceChildEventContent{}
	if present {
		content.Via = []string{fs.tc.main.Bridge.Matrix.ServerName()}
		content.Order = order
	}
	_, err := fs.tc.main.Bridge.Bot.SendState(ctx, space, event.StateSpaceChild, child.String(), &event.Content{Parsed: content}, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update space child %s in %s: %w", child, space, err)
	}
	return nil
}

// Status describes the folder spaces for the `folders` command.
func (fs *folderSpaceManager) Status(ctx context.Context) (string, error) {
	fs.lock.Lock()
	defer fs.lock.Unlock()
	entries, err := fs.currentEntries(ctx)
	if err != nil {
		return "", err
	}
	meta := fs.tc.metadata.FolderSpaces
	lang := fs.language()
	var sb strings.Builder
	fmt.Fprintf(&sb, "Language: `%s` (supported: %s)\n\n", lang, strings.Join(supportedFolderSpaceLanguages(), ", "))
	for i, entry := range entries {
		var name string
		var room id.RoomID
		if entry.AllChats {
			name = folderSpaceText(lang, "all_chats")
			if meta != nil {
				room = meta.AllChats
			}
		} else {
			name = entry.Folder.SpaceName()
			if meta != nil {
				room = meta.Folders[entry.Folder.ID]
			}
		}
		count := 0
		if meta != nil && room != "" {
			count = len(meta.Children[room])
		}
		state := fmt.Sprintf("%d rooms", count)
		if room == "" {
			state = "no space yet"
		}
		fmt.Fprintf(&sb, "%d. %s — %s\n", i+1, name, state)
	}
	return sb.String(), nil
}

// SetLanguage changes the language of built-in sub-space names and schedules a reconcile.
func (fs *folderSpaceManager) SetLanguage(ctx context.Context, lang string) error {
	fs.lock.Lock()
	meta := fs.tc.metadata.FolderSpaces.clone()
	meta.Language = lang
	fs.tc.metadata.FolderSpaces = meta
	err := fs.tc.userLogin.Save(ctx)
	fs.lock.Unlock()
	if err != nil {
		return err
	}
	fs.schedule(folderSpacesAfterChangeDelay)
	return nil
}

// sortDialogsByFolderPriority orders dialogs for creating rooms: personal folders first,
// then other folders, then the rest. The order within each group is kept.
func sortDialogsByFolderPriority(entries []folderEntry, dialogs []prioritizedDialog) []prioritizedDialog {
	rank := func(d prioritizedDialog) int {
		switch {
		case inPersonalFolder(entries, d.chat):
			return 0
		case inAnyFolder(entries, d.chat):
			return 1
		default:
			return 2
		}
	}
	out := slices.Clone(dialogs)
	slices.SortStableFunc(out, func(a, b prioritizedDialog) int {
		return rank(a) - rank(b)
	})
	return out
}

// prioritizedDialog is a dialog fetched during chat sync, with the entities of its page.
type prioritizedDialog struct {
	dialog tg.DialogClass
	page   tg.ModifiedMessagesDialogs
	chat   folderChat
}
