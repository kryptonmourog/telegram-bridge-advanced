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
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// folderChatKind is the kind of chat as far as Telegram folder rules are concerned.
type folderChatKind int

const (
	folderChatUnknown   folderChatKind = iota
	folderChatSelf                     // Saved Messages
	folderChatUser                     // private chat with a human
	folderChatBot                      // private chat with a bot
	folderChatGroup                    // basic group, supergroup or forum
	folderChatBroadcast                // broadcast channel
)

// folderChat describes a chat for matching against folder rules.
type folderChat struct {
	// PeerID identifies the Telegram chat without topic or receiver, see folderPeerID.
	PeerID   string
	Kind     folderChatKind
	Contact  bool
	Archived bool
}

// folderDef is a Telegram chat folder (dialogFilter or dialogFilterChatlist).
type folderDef struct {
	ID       int
	Title    string
	Emoticon string
	// IsChatlist is true for shared folders, which only contain explicitly listed chats.
	IsChatlist bool

	Contacts    bool
	NonContacts bool
	Groups      bool
	Broadcasts  bool
	Bots        bool
	// ExcludeArchived is the "exclude archived" rule.
	ExcludeArchived bool

	// Include contains pinned and included chats, Exclude the excluded ones (folderPeerID format).
	Include map[string]struct{}
	Exclude map[string]struct{}
}

// folderEntry is one entry of the folder tab list: either "All chats" or a folder.
type folderEntry struct {
	AllChats bool
	// Archive is the archive sub-space, added when archived chats are bridged.
	Archive bool
	Folder  *folderDef
}

// Contains reports whether the chat belongs to the entry. "All chats" doesn't include archived
// chats, like in Telegram.
func (e folderEntry) Contains(c folderChat) bool {
	switch {
	case e.AllChats:
		return !c.Archived
	case e.Archive:
		return c.Archived
	default:
		return e.Folder.Contains(c)
	}
}

// folderPeerID returns the peer identifier used for folder matching ("user:123", "chat:123", "channel:123").
func folderPeerID(pt ids.PeerType, id int64) string {
	return fmt.Sprintf("%s:%d", pt, id)
}

// folderPeerIDFromPortalID strips the topic from a portal ID, so that forum portals match their channel.
func folderPeerIDFromPortalID(portalID networkid.PortalID) (string, error) {
	pt, id, _, err := ids.ParsePortalID(portalID)
	if err != nil {
		return "", err
	}
	return folderPeerID(pt, id), nil
}

// folderPeerIDFromInputPeer converts a peer from a folder definition. selfID is needed for inputPeerSelf.
func folderPeerIDFromInputPeer(peer tg.InputPeerClass, selfID int64) string {
	switch p := peer.(type) {
	case *tg.InputPeerSelf:
		return folderPeerID(ids.PeerTypeUser, selfID)
	case *tg.InputPeerUser:
		return folderPeerID(ids.PeerTypeUser, p.UserID)
	case *tg.InputPeerUserFromMessage:
		return folderPeerID(ids.PeerTypeUser, p.UserID)
	case *tg.InputPeerChat:
		return folderPeerID(ids.PeerTypeChat, p.ChatID)
	case *tg.InputPeerChannel:
		return folderPeerID(ids.PeerTypeChannel, p.ChannelID)
	case *tg.InputPeerChannelFromMessage:
		return folderPeerID(ids.PeerTypeChannel, p.ChannelID)
	default:
		return ""
	}
}

func folderPeerSet(selfID int64, lists ...[]tg.InputPeerClass) map[string]struct{} {
	set := make(map[string]struct{})
	for _, list := range lists {
		for _, peer := range list {
			if id := folderPeerIDFromInputPeer(peer, selfID); id != "" {
				set[id] = struct{}{}
			}
		}
	}
	return set
}

// buildFolderEntries converts the result of messages.getDialogFilters into the ordered tab list.
// If the list has no "All chats" entry, it is put first (as Telegram clients do).
func buildFolderEntries(filters []tg.DialogFilterClass, selfID int64) []folderEntry {
	entries := make([]folderEntry, 0, len(filters)+1)
	hasAllChats := false
	for _, f := range filters {
		switch filter := f.(type) {
		case *tg.DialogFilterDefault:
			if !hasAllChats {
				entries = append(entries, folderEntry{AllChats: true})
				hasAllChats = true
			}
		case *tg.DialogFilter:
			entries = append(entries, folderEntry{Folder: &folderDef{
				ID:              filter.ID,
				Title:           strings.TrimSpace(filter.Title.Text),
				Emoticon:        filter.Emoticon,
				Contacts:        filter.Contacts,
				NonContacts:     filter.NonContacts,
				Groups:          filter.Groups,
				Broadcasts:      filter.Broadcasts,
				Bots:            filter.Bots,
				ExcludeArchived: filter.ExcludeArchived,
				Include:         folderPeerSet(selfID, filter.PinnedPeers, filter.IncludePeers),
				Exclude:         folderPeerSet(selfID, filter.ExcludePeers),
			}})
		case *tg.DialogFilterChatlist:
			entries = append(entries, folderEntry{Folder: &folderDef{
				ID:         filter.ID,
				Title:      strings.TrimSpace(filter.Title.Text),
				Emoticon:   filter.Emoticon,
				IsChatlist: true,
				Include:    folderPeerSet(selfID, filter.PinnedPeers, filter.IncludePeers),
				Exclude:    map[string]struct{}{},
			}})
		}
	}
	if !hasAllChats {
		entries = append([]folderEntry{{AllChats: true}}, entries...)
	}
	return entries
}

// Contains reports whether the chat belongs to the folder. Pinned and included chats are always in,
// excluded ones never; otherwise the "exclude archived" and type rules decide. The "exclude muted"
// and "exclude read" rules are intentionally not evaluated.
// Saved Messages count as a contact, like in the official clients.
func (f *folderDef) Contains(c folderChat) bool {
	if _, ok := f.Include[c.PeerID]; ok {
		return true
	}
	if _, ok := f.Exclude[c.PeerID]; ok {
		return false
	}
	if f.IsChatlist || (c.Archived && f.ExcludeArchived) {
		return false
	}
	switch c.Kind {
	case folderChatSelf:
		return f.Contacts
	case folderChatUser:
		if c.Contact {
			return f.Contacts
		}
		return f.NonContacts
	case folderChatBot:
		return f.Bots
	case folderChatGroup:
		return f.Groups
	case folderChatBroadcast:
		return f.Broadcasts
	default:
		return false
	}
}

// IsPersonal reports whether the folder is driven by the contacts / non-contacts rules.
func (f *folderDef) IsPersonal() bool {
	return f.Contacts || f.NonContacts
}

// SpaceName is the Matrix space name for the folder: "<emoji> <title>".
func (f *folderDef) SpaceName() string {
	if f.Emoticon != "" && f.Title != "" {
		return f.Emoticon + " " + f.Title
	} else if f.Title != "" {
		return f.Title
	}
	return f.Emoticon
}

// folderChatFromEntities builds a folderChat from Telegram entities (used while syncing dialogs).
func folderChatFromEntities(peer tg.PeerClass, selfID int64, users map[int64]tg.UserClass, chats map[int64]tg.ChatClass) folderChat {
	switch p := peer.(type) {
	case *tg.PeerUser:
		fc := folderChat{PeerID: folderPeerID(ids.PeerTypeUser, p.UserID), Kind: folderChatUser}
		if p.UserID == selfID {
			fc.Kind = folderChatSelf
		} else if user, ok := users[p.UserID].(*tg.User); ok {
			if user.Bot {
				fc.Kind = folderChatBot
			}
			fc.Contact = user.Contact
		}
		return fc
	case *tg.PeerChat:
		return folderChat{PeerID: folderPeerID(ids.PeerTypeChat, p.ChatID), Kind: folderChatGroup}
	case *tg.PeerChannel:
		fc := folderChat{PeerID: folderPeerID(ids.PeerTypeChannel, p.ChannelID), Kind: folderChatBroadcast}
		if channel, ok := chats[p.ChannelID].(*tg.Channel); ok && !channel.Broadcast {
			fc.Kind = folderChatGroup
		}
		return fc
	default:
		return folderChat{}
	}
}

// inPersonalFolder reports whether the chat is in any folder driven by the contacts / non-contacts rules.
func inPersonalFolder(entries []folderEntry, chat folderChat) bool {
	for _, e := range entries {
		if e.Folder != nil && e.Folder.IsPersonal() && e.Folder.Contains(chat) {
			return true
		}
	}
	return false
}

// inAnyFolder reports whether the chat is in any folder (not counting "All chats").
func inAnyFolder(entries []folderEntry, chat folderChat) bool {
	for _, e := range entries {
		if e.Folder != nil && e.Folder.Contains(chat) {
			return true
		}
	}
	return false
}
