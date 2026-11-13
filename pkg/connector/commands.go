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
	"errors"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/format"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tgerr"
)

var cmdSyncChats = &commands.FullHandler{
	Func: fnSyncChats,
	Name: "sync-chats",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Synchronize your chats",
		Args:        "[_login ID_]",
	},
	RequiresLogin: true,
}

func fnSyncChats(ce *commands.Event) {
	logins := ce.User.GetUserLogins()
	if len(ce.Args) > 0 {
		logins = slices.DeleteFunc(logins, func(login *bridgev2.UserLogin) bool {
			return !slices.Contains(ce.Args, string(login.ID))
		})
		if len(logins) == 0 {
			ce.Reply("No matching logins found with provided ID(s)")
			return
		}
	}
	for _, login := range logins {
		client := login.Client.(*TelegramClient)
		if err := client.syncChats(ce.Ctx, 0, false, true); err != nil {
			ce.Reply("Failed to synchronize chats for %s: %v", format.SafeMarkdownCode(login.ID), err)
		} else {
			ce.Reply("Successfully synchronized chats for %s", format.SafeMarkdownCode(login.ID))
		}
	}
}

var cmdAds = &commands.FullHandler{
	Func: fnAds,
	Name: "ads",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Show or change where Telegram's sponsored messages are shown: in a separate room or in the channels",
		Args:        "[`room` | `on`]",
	},
	RequiresLogin: true,
}

func fnAds(ce *commands.Event) {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You're not logged in")
		return
	}
	client := login.Client.(*TelegramClient)
	var sub string
	if len(ce.Args) > 0 {
		sub = strings.ToLower(ce.Args[0])
	}
	describe := map[string]string{
		AdsModeRoom: "`room`: Telegram's ads go to a separate muted, low priority room instead of the channels",
		AdsModeOn:   "`on`: Telegram's ads are shown in the channel rooms",
		AdsModeOff:  "`off`: ads aren't requested at all (Telegram's API terms ask apps to show them)",
	}
	switch sub {
	case "":
		ce.Reply("Sponsored messages: %s.\nModes: `ads room` (default), `ads on`.", describe[client.adsMode()])
	// AdsModeOff stays supported in the code, but isn't offered: with the user's own api_id, not
	// requesting ads at all is what Telegram's API terms forbid.
	case AdsModeRoom, AdsModeOn:
		client.metadata.AdsMode = sub
		client.metadata.AdsDisabled = false
		if err := login.Save(ce.Ctx); err != nil {
			ce.Reply("Failed to save the setting: %v", err)
			return
		}
		// Ads already shown where they no longer belong are removed.
		removed, err := 0, error(nil)
		switch sub {
		case AdsModeRoom:
			removed, err = client.removeSponsoredMessages(ce.Ctx, true)
		case AdsModeOff:
			removed, err = client.removeSponsoredMessages(ce.Ctx, false)
		}
		if err != nil {
			ce.Reply("Saved, but removing shown ads failed after %d: %v", removed, err)
			return
		}
		ce.Reply("Sponsored messages: %s. Removed %d shown ads.", describe[sub], removed)
	default:
		ce.Reply("Usage: `ads` (status), `ads room`, `ads on`")
	}
}

var cmdArchive = &commands.FullHandler{
	Func: fnArchive,
	Name: "archive",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Show whether archived Telegram chats are bridged, or turn bridging them on or off",
		Args:        "[`on` | `off`]",
	},
	RequiresLogin: true,
}

func fnArchive(ce *commands.Event) {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You're not logged in")
		return
	}
	client := login.Client.(*TelegramClient)
	fs := client.folderSpaces
	if !fs.enabled() {
		ce.Reply("The archive setting needs folder spaces, which are disabled. Archived chats are bridged like any other chat.")
		return
	}
	var sub string
	if len(ce.Args) > 0 {
		sub = strings.ToLower(ce.Args[0])
	}
	switch sub {
	case "":
		state := "off: archived chats aren't bridged and their rooms are hidden"
		if fs.archiveEnabled() {
			state = "on: archived chats are bridged into the archive space"
		}
		count := 0
		if meta := client.metadata.FolderSpaces; meta != nil {
			count = len(meta.Archived)
		}
		ce.Reply("Archive is %s.\nArchived chats: %d. Use `archive on` or `archive off` to change it.", state, count)
	case "on":
		ce.Reply("Bridging archived chats, this may take a while...")
		created, err := fs.SetArchive(ce.Ctx, true)
		if err != nil {
			ce.Reply("Failed to turn the archive on: %v", err)
			return
		}
		ce.Reply("Archive is on. Rooms of archived chats are back in the archive space and catching up; %d new rooms are being created.", created)
	case "off":
		if _, err := fs.SetArchive(ce.Ctx, false); err != nil {
			ce.Reply("Failed to turn the archive off: %v", err)
			return
		}
		ce.Reply("Archive is off. You left the rooms of archived chats; they're kept, so `archive on` only catches up on new messages.")
	default:
		ce.Reply("Usage: `archive` (status), `archive on`, `archive off`")
	}
}

var cmdFolders = &commands.FullHandler{
	Func: fnFolders,
	Name: "folders",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Show Telegram folder spaces, change their language or re-sync them",
		Args:        "[`lang` _code_ | `resync`]",
	},
	RequiresLogin: true,
}

func fnFolders(ce *commands.Event) {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You're not logged in")
		return
	}
	client := login.Client.(*TelegramClient)
	if !client.folderSpaces.enabled() {
		ce.Reply("Folder spaces are disabled. They need `network.folder_spaces.enabled: true` and `bridge.personal_filtering_spaces: false` in the bridge config.")
		return
	}
	var sub string
	if len(ce.Args) > 0 {
		sub = strings.ToLower(ce.Args[0])
	}
	switch sub {
	case "":
		status, err := client.folderSpaces.Status(ce.Ctx)
		if err != nil {
			ce.Reply("Failed to get folders: %v", err)
			return
		}
		ce.Reply("%s", status)
	case "lang":
		if len(ce.Args) < 2 {
			ce.Reply("Usage: `folders lang <code>`, supported: %s", strings.Join(supportedFolderSpaceLanguages(), ", "))
			return
		}
		lang := strings.ToLower(ce.Args[1])
		if !isSupportedFolderSpaceLanguage(lang) {
			ce.Reply("Unsupported language `%s`, supported: %s", lang, strings.Join(supportedFolderSpaceLanguages(), ", "))
			return
		}
		if err := client.folderSpaces.SetLanguage(ce.Ctx, lang); err != nil {
			ce.Reply("Failed to save language: %v", err)
			return
		}
		ce.Reply("Language set to `%s`, space names will be updated in a few seconds", lang)
	case "resync":
		client.folderSpaces.needsRefetch.Store(true)
		if err := client.folderSpaces.reconcile(ce.Ctx); err != nil {
			ce.Reply("Failed to sync folder spaces: %v", err)
			return
		}
		ce.Reply("Folder spaces synchronized")
	default:
		ce.Reply("Usage: `folders` (status), `folders lang <code>`, `folders resync`")
	}
}

var cmdUpgrade = &commands.FullHandler{
	Func: fnUpgrade,
	Name: "upgrade",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Upgrade a minigroup to a supergroup on Telegram",
	},
	RequiresPortal: true,
}

func fnUpgrade(ce *commands.Event) {
	login, _, err := ce.Portal.FindPreferredLogin(ce.Ctx, ce.User, false)
	if errors.Is(err, bridgev2.ErrNotLoggedIn) {
		ce.Reply("No logins found to upgrade the chat")
	} else if err != nil {
		ce.Log.Err(err).Msg("Failed to find preferred login for upgrade command")
		ce.Reply("Failed to find a login to upgrade the chat.")
	} else if peerType, chatID, _, err := ids.ParsePortalID(ce.Portal.ID); err != nil {
		ce.Log.Err(err).Str("portal_id", string(ce.Portal.ID)).Msg("Failed to parse portal ID for upgrade command")
		ce.Reply("Failed to parse portal ID")
	} else if peerType == ids.PeerTypeChannel {
		ce.Reply("Only minigroups can be upgraded (this is already a channel/supergroup)")
	} else if peerType == ids.PeerTypeUser {
		ce.Reply("Only minigroups can be upgraded (this is direct chat)")
	} else if resp, err := login.Client.(*TelegramClient).client.API().MessagesMigrateChat(ce.Ctx, chatID); err != nil {
		ce.Log.Err(err).Int64("chat_id", chatID).Msg("Failed to upgrade chat")
		ce.Reply("Failed to upgrade chat: %v", err)
	} else {
		ce.Log.Trace().Any("response", resp).Msg("Updates from chat upgrade")
		ce.Log.Info().Int64("old_chat_id", chatID).Msg("Successfully upgraded chat")
		ce.React("\u2705\ufe0f")
		err = login.Client.(*TelegramClient).dispatcher.Handle(ce.Ctx, resp)
		if err != nil {
			ce.Log.Err(err).Msg("Failed to handle updates from chat upgrade")
		} else {
			ce.Log.Debug().Msg("Finished handling updates from chat upgrade")
		}
	}
}

var cmdJoin = &commands.FullHandler{
	Func: fnJoin,
	Name: "join",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Join a Telegram group using an invite link.",
		Args:        "[login ID] <invite link>",
	},
	RequiresLogin: true,
}

var usernameLinkRe = regexp.MustCompile(`^(?:(?:https?://)?t(?:elegram)?\.(?:me|dog)/|tg:/{0,2}resolve\?domain=)([a-zA-Z]\w{3,30}[a-zA-Z\d])(?:\?.+)?$`)
var inviteLinkRe = regexp.MustCompile(`^(?:(?:https?://)?t(?:elegram)?\.(?:me|dog)/(?:joinchat/|\+)|tg:/{0,2}join\?invite=)([a-zA-Z0-9_-]{8,64})(?:\?.+)?$`)

func fnJoin(ce *commands.Event) {
	if len(ce.Args) == 0 || len(ce.Args) > 2 {
		ce.Reply("Usage: `$cmdprefix join [login ID] <invite link>`")
		return
	}
	var login *bridgev2.UserLogin
	if len(ce.Args) == 2 {
		targetLogin := ce.Bridge.GetCachedUserLoginByID(networkid.UserLoginID(ce.Args[0]))
		if targetLogin == nil || targetLogin.UserMXID != ce.User.MXID {
			ce.Reply("No login found with the provided ID")
			return
		}
		login = targetLogin
		ce.Args = ce.Args[1:]
	} else {
		login = ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("You're not logged in")
			return
		}
	}
	t := login.Client.(*TelegramClient)
	var resp tg.MessagesChatInviteJoinResultClass
	var chatName string
	if usernameMatch := usernameLinkRe.FindStringSubmatch(ce.Args[0]); usernameMatch != nil {
		resolve, err := t.client.API().ContactsResolveUsername(ce.Ctx, &tg.ContactsResolveUsernameRequest{Username: usernameMatch[1]})
		if err != nil {
			ce.Log.Err(err).Msg("Failed to resolve username from invite link")
			ce.Reply("Failed to resolve username from invite link: %v", err)
			return
		}
		peer, isChannel := resolve.Peer.(*tg.PeerChannel)
		if !isChannel {
			ce.Reply("That username does not belong to a channel or supergroup")
			return
		}
		var inputChannel *tg.InputChannel
		for _, chat := range resolve.Chats {
			if chat.GetID() == peer.ChannelID {
				switch typedChat := chat.(type) {
				case *tg.Channel:
					inputChannel = typedChat.AsInput()
					chatName = typedChat.Title
				case *tg.Community:
					chatName = typedChat.Title
					inputChannel = &tg.InputChannel{
						ChannelID:  typedChat.ID,
						AccessHash: typedChat.AccessHash,
					}
				}
			}
		}
		if inputChannel == nil {
			ce.Reply("Channel information not found in resolve response")
			return
		}
		resp, err = t.client.API().ChannelsJoinChannel(ce.Ctx, inputChannel)
		if err != nil {
			ce.Log.Err(err).Msg("Failed to join chat with invite link")
			ce.Reply("Failed to join chat: %v", err)
			return
		}
	} else if inviteLinkMatch := inviteLinkRe.FindStringSubmatch(ce.Args[0]); inviteLinkMatch != nil {
		resolve, err := t.client.API().MessagesCheckChatInvite(ce.Ctx, inviteLinkMatch[1])
		if tgerr.Is(err, tg.ErrInviteHashInvalid) {
			ce.Reply("Invalid invite link")
			return
		} else if tgerr.Is(err, tg.ErrInviteHashExpired) {
			ce.Reply("Invite link expired")
			return
		}
		switch typed := resolve.(type) {
		case *tg.ChatInviteAlready:
			titler, ok := typed.Chat.(interface {
				GetTitle() string
			})
			if ok {
				chatName = titler.GetTitle()
			} else {
				chatName = "that chat"
			}
			ce.Reply("You're already a member of %s", html.EscapeString(chatName))
			return
		case *tg.ChatInvite:
			chatName = typed.Title
		default:
			ce.Log.Warn().Type("resolved_type", typed).Msg("Unexpected response type from MessagesCheckChatInvite")
		}
		resp, err = t.client.API().MessagesImportChatInvite(ce.Ctx, inviteLinkMatch[1])
		if err != nil {
			ce.Log.Err(err).Msg("Failed to join chat with invite link")
			ce.Reply("Failed to join chat: %v", err)
			return
		}
	} else {
		ce.Reply("Invalid invite link format")
		return
	}
	switch realResp := resp.(type) {
	case *tg.MessagesChatInviteJoinResultOk:
		err := t.dispatcher.Handle(ce.Ctx, realResp.Updates)
		if err != nil {
			ce.Log.Err(err).Msg("Failed to handle updates from joining chat with invite link")
		} else {
			ce.Log.Debug().Msg("Finished handling updates from joining chat with invite link")
		}
		ce.Reply("Successfully joined %s", html.EscapeString(chatName))
	case *tg.MessagesChatInviteJoinResultWebView:
		ce.Reply("The chat wants you to open a webview to join. Please use the native Telegram app.")
	default:
		ce.Reply("Unexpected response for join request")
	}
}

var cmdEmojiPack = &commands.FullHandler{
	Func:    fnEmojiPack,
	Name:    "emoji-pack",
	Aliases: []string{"pack", "sticker-pack", "emojipack", "stickerpack"},
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionMisc,
		Description: "Bridge emoji packs between Matrix and Telegram.",
		Args:        "<upload/download/list/help> [args...]",
	},
	RequiresLogin: true,
}

const emojiPackHelp = `This command can be used to transfer emoji packs between Matrix and Telegram.

* $cmdprefix emoji-pack upload <telegram shortcode> <room ID> <state key> - Transfer a pack from Matrix to Telegram.
* $cmdprefix emoji-pack download <pack shortcode or link> - Transfer a pack from Telegram to Matrix.
* $cmdprefix emoji-pack list - List your current emoji packs on Telegram.
* $cmdprefix emoji-pack help - Show this help message.`

func fnEmojiPack(ce *commands.Event) {
	var login *bridgev2.UserLogin
	if len(ce.Args) > 0 {
		targetLogin := ce.Bridge.GetCachedUserLoginByID(networkid.UserLoginID(ce.Args[0]))
		if targetLogin != nil && targetLogin.UserMXID == ce.User.MXID {
			ce.Args = ce.Args[1:]
			login = targetLogin
		}
	}
	var command string
	if len(ce.Args) > 0 {
		command = strings.ToLower(ce.Args[0])
		ce.Args = ce.Args[1:]
	}

	if login == nil {
		login = ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("You're not logged in")
			return
		}
	}
	client := login.Client.(*TelegramClient)

	switch command {
	case "help", "":
		ce.Reply(emojiPackHelp)
	case "list":
		client.fnListEmojiPacks(ce)
	case "upload":
		client.fnUploadEmojiPack(ce)
	case "download":
		client.fnDownloadEmojiPack(ce)
	default:
		ce.Reply("Usage: `$cmdprefix emoji-pack <upload/download/list/help> [args...]`")
	}
}
