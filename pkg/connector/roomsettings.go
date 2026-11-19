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
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// RoomSettings are the user's settings of one chat's room, changed with the `settings` command in it.
// The zero value is the default behaviour.
type RoomSettings struct {
	// MediaLimitMB: 0 bridges all media, a positive value skips files larger than that many MB,
	// a negative value skips all photos and files.
	MediaLimitMB int `json:"media_limit_mb,omitempty"`
	// Paused stops bridging new messages from Telegram; resuming catches up.
	Paused bool `json:"paused,omitempty"`
	// NoSignatures leaves out who posted in channels.
	NoSignatures bool `json:"no_signatures,omitempty"`
	// NoButtons leaves out bot keyboards.
	NoButtons bool `json:"no_buttons,omitempty"`
}

const mediaNone = -1

func portalSettings(portal *bridgev2.Portal) RoomSettings {
	if portal == nil {
		return RoomSettings{}
	}
	if meta, ok := portal.Metadata.(*PortalMetadata); ok && meta.Settings != nil {
		return *meta.Settings
	}
	return RoomSettings{}
}

// skipMediaByRoomSettings reports whether a photo (size 0) or file of the given size isn't bridged
// because of the room's settings.
func skipMediaByRoomSettings(portal *bridgev2.Portal, size int64) bool {
	limit := portalSettings(portal).MediaLimitMB
	switch {
	case limit == 0:
		return false
	case limit < 0:
		return true
	default:
		return size > int64(limit)*1024*1024
	}
}

// isPausedPortal reports whether bridging the chat is paused. It only looks at loaded portals.
func (tc *TelegramClient) isPausedPortal(ctx context.Context, key networkid.PortalKey) bool {
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, key)
	return err == nil && portal != nil && portalSettings(portal).Paused
}

// pausableEvents are the events dropped while a chat is paused: new content, not changes to the chat.
var pausableEvents = map[bridgev2.RemoteEventType]bool{
	bridgev2.RemoteEventMessage:         true,
	bridgev2.RemoteEventMessageUpsert:   true,
	bridgev2.RemoteEventEdit:            true,
	bridgev2.RemoteEventReaction:        true,
	bridgev2.RemoteEventReactionRemove:  true,
	bridgev2.RemoteEventReactionSync:    true,
	bridgev2.RemoteEventMessageRemove:   true,
	bridgev2.RemoteEventTyping:          true,
	bridgev2.RemoteEventReadReceipt:     true,
	bridgev2.RemoteEventMarkUnread:      true,
	bridgev2.RemoteEventDeliveryReceipt: true,
}

var cmdSettings = &commands.FullHandler{
	Func: fnSettings,
	Name: "settings",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Show or change the settings of this chat's room",
		Args:        "[_setting_ _value_]",
	},
	RequiresLogin:  true,
	RequiresPortal: true,
}

func fnSettings(ce *commands.Event) {
	login := ce.User.GetDefaultLogin()
	if login == nil {
		ce.Reply("You're not logged in")
		return
	}
	client := login.Client.(*TelegramClient)
	_, isChannel := client.isBroadcastPortal(ce.Portal)
	settings := portalSettings(ce.Portal)
	if len(ce.Args) == 0 {
		ce.Reply("%s", client.describeRoomSettings(ce, settings, isChannel))
		return
	}
	// A shared room (supergroups and forums) is the same for all its users: pausing it or skipping its
	// media would do so for everyone, so only bridge admins change its settings.
	if ce.Portal.Receiver == "" && !ce.User.Permissions.Admin {
		ce.Reply("This room is shared with other users of the bridge, only bridge admins can change its settings.")
		return
	}
	name := strings.ToLower(ce.Args[0])
	var value string
	if len(ce.Args) > 1 {
		value = strings.ToLower(ce.Args[1])
	}
	onOff := func() (bool, bool) {
		switch value {
		case "on", "вкл", "да":
			return true, true
		case "off", "выкл", "нет":
			return false, true
		}
		ce.Reply("Use `on` or `off`.")
		return false, false
	}
	wasPaused := settings.Paused
	switch name {
	case "media":
		switch value {
		case "all":
			settings.MediaLimitMB = 0
		case "none":
			settings.MediaLimitMB = mediaNone
		default:
			mb, err := strconv.Atoi(value)
			if err != nil || mb <= 0 {
				ce.Reply("Usage: `$cmdprefix settings media all`, `$cmdprefix settings media none` or `$cmdprefix settings media <MB>`")
				return
			}
			settings.MediaLimitMB = mb
		}
	case "pause":
		on, ok := onOff()
		if !ok {
			return
		}
		settings.Paused = on
	case "signatures":
		if !isChannel {
			ce.Reply("Signatures are only shown in channels.")
			return
		}
		on, ok := onOff()
		if !ok {
			return
		}
		settings.NoSignatures = !on
	case "buttons":
		on, ok := onOff()
		if !ok {
			return
		}
		settings.NoButtons = !on
	default:
		ce.Reply("Unknown setting `%s`, see `$cmdprefix settings`.", name)
		return
	}
	meta := ce.Portal.Metadata.(*PortalMetadata)
	if settings == (RoomSettings{}) {
		meta.Settings = nil
	} else {
		meta.Settings = &settings
	}
	if err := ce.Portal.Save(ce.Ctx); err != nil {
		ce.Reply("Failed to save the setting: %v", err)
		return
	}
	if wasPaused && !settings.Paused {
		client.resyncWithBackfill(ce.Portal.PortalKey, false)
		// Catching up is bridgev2's: it brings the newest max_catchup_messages, older ones stay out.
		ce.Reply("Bridging resumed, the last %d messages sent meanwhile are being caught up.", ce.Bridge.Config.Backfill.MaxCatchupMessages)
		return
	}
	ce.Reply("Saved. %s", client.describeRoomSettings(ce, settings, isChannel))
}

func (tc *TelegramClient) describeRoomSettings(ce *commands.Event, s RoomSettings, isChannel bool) string {
	onOff := func(on bool) string {
		if on {
			return ce.T("on")
		}
		return ce.T("off")
	}
	var media string
	switch {
	case s.MediaLimitMB == 0:
		media = ce.T("all")
	case s.MediaLimitMB < 0:
		media = ce.T("none (photos and files aren't bridged)")
	default:
		media = fmt.Sprintf(ce.T("files up to %d MB"), s.MediaLimitMB)
	}
	pause := ce.T("bridging")
	if s.Paused {
		pause = ce.T("paused")
	}
	lines := []string{
		fmt.Sprintf(ce.T("Media: **%s** — `$cmdprefix settings media all|none|<MB>`"), media),
		fmt.Sprintf(ce.T("New messages: **%s** — `$cmdprefix settings pause on|off`"), pause),
	}
	if isChannel {
		lines = append(lines, fmt.Sprintf(ce.T("Author signatures: **%s** — `$cmdprefix settings signatures on|off`"), onOff(!s.NoSignatures)))
	}
	lines = append(lines, fmt.Sprintf(ce.T("Bot buttons: **%s** — `$cmdprefix settings buttons on|off`"), onOff(!s.NoButtons)))
	var sb strings.Builder
	sb.WriteString(ce.T("Settings of this room:"))
	sb.WriteString("\n")
	for i, line := range lines {
		fmt.Fprintf(&sb, "\n%d. %s", i+1, line)
	}
	sb.WriteString("\n\n")
	sb.WriteString(ce.T("Settings apply to new messages."))
	return strings.ReplaceAll(sb.String(), "$cmdprefix", ce.Bridge.Config.CommandPrefix)
}
