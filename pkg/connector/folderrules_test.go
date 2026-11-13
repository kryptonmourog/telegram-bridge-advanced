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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

const testSelfID = 1000

var (
	chatSelf       = folderChat{PeerID: "user:1000", Kind: folderChatSelf}
	chatContact    = folderChat{PeerID: "user:1", Kind: folderChatUser, Contact: true}
	chatNonContact = folderChat{PeerID: "user:2", Kind: folderChatUser}
	chatBot        = folderChat{PeerID: "user:3", Kind: folderChatBot}
	chatBasicGroup = folderChat{PeerID: "chat:4", Kind: folderChatGroup}
	chatSuperGroup = folderChat{PeerID: "channel:5", Kind: folderChatGroup}
	chatChannel    = folderChat{PeerID: "channel:6", Kind: folderChatBroadcast}
	allTestChats   = []folderChat{chatSelf, chatContact, chatNonContact, chatBot, chatBasicGroup, chatSuperGroup, chatChannel}
)

func members(f *folderDef) (out []string) {
	for _, c := range allTestChats {
		if f.Contains(c) {
			out = append(out, c.PeerID)
		}
	}
	return
}

func TestFolderContains_TypeRules(t *testing.T) {
	tests := []struct {
		name   string
		folder folderDef
		want   []string
	}{
		{"contacts", folderDef{Contacts: true}, []string{"user:1000", "user:1"}},
		{"non-contacts", folderDef{NonContacts: true}, []string{"user:2"}},
		{"personal", folderDef{Contacts: true, NonContacts: true}, []string{"user:1000", "user:1", "user:2"}},
		{"bots", folderDef{Bots: true}, []string{"user:3"}},
		{"groups", folderDef{Groups: true}, []string{"chat:4", "channel:5"}},
		{"channels", folderDef{Broadcasts: true}, []string{"channel:6"}},
		{"nothing", folderDef{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, members(&tt.folder))
		})
	}
}

func TestFolderContains_IncludeExclude(t *testing.T) {
	f := folderDef{
		Groups:  true,
		Include: map[string]struct{}{"user:2": {}, "channel:6": {}},
		Exclude: map[string]struct{}{"chat:4": {}, "channel:6": {}},
	}
	// included wins over excluded and over type rules, excluded removes a type match
	assert.Equal(t, []string{"user:2", "channel:5", "channel:6"}, members(&f))
}

func TestFolderContains_Chatlist(t *testing.T) {
	f := folderDef{IsChatlist: true, Include: map[string]struct{}{"channel:6": {}}, Exclude: map[string]struct{}{}}
	assert.Equal(t, []string{"channel:6"}, members(&f))
	assert.False(t, f.IsPersonal())
}

func TestBuildFolderEntries(t *testing.T) {
	filters := []tg.DialogFilterClass{
		&tg.DialogFilter{
			ID:           2,
			Title:        tg.TextWithEntities{Text: " Work "},
			Emoticon:     "💼",
			Groups:       true,
			PinnedPeers:  []tg.InputPeerClass{&tg.InputPeerSelf{}},
			IncludePeers: []tg.InputPeerClass{&tg.InputPeerUser{UserID: 7}, &tg.InputPeerEmpty{}},
			ExcludePeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 5}},
		},
		&tg.DialogFilterDefault{},
		&tg.DialogFilterChatlist{ID: 3, Title: tg.TextWithEntities{Text: "Shared"}, IncludePeers: []tg.InputPeerClass{&tg.InputPeerChat{ChatID: 4}}},
	}
	entries := buildFolderEntries(filters, testSelfID)
	require.Len(t, entries, 3)

	work := entries[0].Folder
	require.NotNil(t, work)
	assert.Equal(t, "💼 Work", work.SpaceName())
	assert.Equal(t, map[string]struct{}{"user:1000": {}, "user:7": {}}, work.Include)
	assert.Equal(t, map[string]struct{}{"channel:5": {}}, work.Exclude)

	assert.True(t, entries[1].AllChats, "All chats keeps its position among folders")

	shared := entries[2].Folder
	require.NotNil(t, shared)
	assert.True(t, shared.IsChatlist)
	assert.Equal(t, []string{"chat:4"}, members(shared))
}

func TestBuildFolderEntries_NoDefault(t *testing.T) {
	entries := buildFolderEntries([]tg.DialogFilterClass{&tg.DialogFilter{ID: 2, Title: tg.TextWithEntities{Text: "A"}}}, testSelfID)
	require.Len(t, entries, 2)
	assert.True(t, entries[0].AllChats, "All chats goes first when Telegram doesn't list it")
	assert.Equal(t, 2, entries[1].Folder.ID)

	assert.Equal(t, []folderEntry{{AllChats: true}}, buildFolderEntries(nil, testSelfID))
}

func TestFolderPeerIDFromPortalID(t *testing.T) {
	for portalID, want := range map[string]string{
		string(ids.MakePortalID(ids.PeerTypeUser, 1)):    "user:1",
		string(ids.MakePortalID(ids.PeerTypeChat, 4)):    "chat:4",
		string(ids.MakePortalID(ids.PeerTypeChannel, 5)): "channel:5",
		string(ids.MakeTopicPortalID(5, 42)):             "channel:5",
		string(ids.MakeForumParentPortalID(5)):           "channel:5",
	} {
		got, err := folderPeerIDFromPortalID(networkid.PortalID(portalID))
		require.NoError(t, err)
		assert.Equal(t, want, got, portalID)
	}
}

func TestFolderChatFromEntities(t *testing.T) {
	users := map[int64]tg.UserClass{
		1: &tg.User{ID: 1, Contact: true},
		3: &tg.User{ID: 3, Bot: true},
	}
	chats := map[int64]tg.ChatClass{
		5: &tg.Channel{ID: 5, Megagroup: true},
		6: &tg.Channel{ID: 6, Broadcast: true},
	}
	assert.Equal(t, chatSelf, folderChatFromEntities(&tg.PeerUser{UserID: testSelfID}, testSelfID, users, chats))
	assert.Equal(t, chatContact, folderChatFromEntities(&tg.PeerUser{UserID: 1}, testSelfID, users, chats))
	assert.Equal(t, chatNonContact, folderChatFromEntities(&tg.PeerUser{UserID: 2}, testSelfID, users, chats))
	assert.Equal(t, chatBot, folderChatFromEntities(&tg.PeerUser{UserID: 3}, testSelfID, users, chats))
	assert.Equal(t, chatBasicGroup, folderChatFromEntities(&tg.PeerChat{ChatID: 4}, testSelfID, users, chats))
	assert.Equal(t, chatSuperGroup, folderChatFromEntities(&tg.PeerChannel{ChannelID: 5}, testSelfID, users, chats))
	assert.Equal(t, chatChannel, folderChatFromEntities(&tg.PeerChannel{ChannelID: 6}, testSelfID, users, chats))
}

func TestInPersonalFolder(t *testing.T) {
	entries := []folderEntry{
		{AllChats: true},
		{Folder: &folderDef{ID: 1, Groups: true}},
		{Folder: &folderDef{ID: 2, Contacts: true}},
	}
	assert.True(t, inPersonalFolder(entries, chatContact))
	assert.False(t, inPersonalFolder(entries, chatNonContact))
	assert.False(t, inPersonalFolder(entries, chatBasicGroup), "groups folder is not personal")
	assert.True(t, inAnyFolder(entries, chatBasicGroup))
	assert.False(t, inAnyFolder(entries, chatChannel))
}

func TestSortDialogsByFolderPriority(t *testing.T) {
	entries := []folderEntry{
		{AllChats: true},
		{Folder: &folderDef{ID: 1, Groups: true}},
		{Folder: &folderDef{ID: 2, Contacts: true}},
	}
	in := []prioritizedDialog{
		{chat: chatChannel},
		{chat: chatBasicGroup},
		{chat: chatNonContact},
		{chat: chatContact},
		{chat: chatSuperGroup},
		{chat: chatSelf},
	}
	var got []string
	for _, d := range sortDialogsByFolderPriority(entries, in) {
		got = append(got, d.chat.PeerID)
	}
	// personal folders first, then other folders, then the rest; order inside each group is kept
	assert.Equal(t, []string{"user:1", "user:1000", "chat:4", "channel:5", "channel:6", "user:2"}, got)
	assert.Equal(t, chatChannel, in[0].chat, "input slice is not modified")
}

func TestFormatSponsoredMessage(t *testing.T) {
	content := &event.MessageEventContent{Body: "Buy <things>", FormattedBody: "Buy &lt;things&gt;"}
	msg := &tg.SponsoredMessage{Title: "Shop", URL: "https://example.com/?a=1&b=2", ButtonText: "Open", SponsorInfo: "Shop LLC", AdditionalInfo: "INN 123"}
	formatSponsoredMessage("ru", msg, content)
	assert.Equal(t, event.FormatHTML, content.Format)
	assert.Contains(t, content.FormattedBody, "<strong>Реклама от Telegram</strong>")
	assert.Contains(t, content.FormattedBody, "<blockquote><strong>Shop</strong><br>Buy &lt;things&gt;</blockquote>")
	assert.NotContains(t, content.FormattedBody, "<a ", "button link is left out")
	assert.Contains(t, content.FormattedBody, "Рекламодатель: Shop LLC<br>INN 123")
	assert.Contains(t, content.Body, "Реклама от Telegram\n> Shop\n> Buy <things>\n")
	assert.NotContains(t, content.Body, "example.com")
	assert.True(t, strings.HasSuffix(content.Body, "Рекламодатель: Shop LLC; INN 123"))

	recommended := &event.MessageEventContent{Body: "x", FormattedBody: "x"}
	formatSponsoredMessage("de", &tg.SponsoredMessage{Recommended: true}, recommended)
	assert.Contains(t, recommended.Body, "Recommended by Telegram", "unknown language falls back to English")
	assert.NotContains(t, recommended.Body, "Advertiser")
}

func TestArchiveRules(t *testing.T) {
	archivedGroup := chatBasicGroup
	archivedGroup.Archived = true

	assert.True(t, folderEntry{AllChats: true}.Contains(chatBasicGroup))
	assert.False(t, folderEntry{AllChats: true}.Contains(archivedGroup), "All chats doesn't include the archive")
	assert.True(t, folderEntry{Archive: true}.Contains(archivedGroup))
	assert.False(t, folderEntry{Archive: true}.Contains(chatBasicGroup))

	groups := folderDef{Groups: true}
	assert.True(t, groups.Contains(archivedGroup), "folders without the rule include archived chats")
	groups.ExcludeArchived = true
	assert.False(t, groups.Contains(archivedGroup))
	groups.Include = map[string]struct{}{archivedGroup.PeerID: {}}
	assert.True(t, groups.Contains(archivedGroup), "explicitly included chats stay")

	pt, chatID, err := parseFolderPeerID("channel:123")
	require.NoError(t, err)
	assert.Equal(t, ids.PeerTypeChannel, pt)
	assert.EqualValues(t, 123, chatID)
	_, _, err = parseFolderPeerID("bad")
	assert.Error(t, err)
}
