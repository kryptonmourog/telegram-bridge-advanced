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
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

type roomEncryptionMatrix interface {
	CanEncryptRooms() bool
	IsRoomEncrypted(context.Context, id.RoomID) (bool, error)
	EnableRoomEncryption(context.Context, id.RoomID) error
}

var cmdEncryption = &commands.FullHandler{
	Func: fnEncryption,
	Name: "encryption",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionChats,
		Description: "Enable encryption permanently in this private chat's room",
		Args:        "[`on confirm`]",
	},
	RequiresLogin:  true,
	RequiresPortal: true,
}

func fnEncryption(ce *commands.Event) {
	if ce.Portal.RoomType != database.RoomTypeDM {
		ce.Reply("Encryption can only be enabled in private chats.")
		return
	}
	matrix, ok := ce.Bridge.Matrix.(roomEncryptionMatrix)
	if !ok {
		ce.Reply("Matrix connector doesn't support room encryption.")
		return
	}
	if !matrix.CanEncryptRooms() {
		ce.Reply("Encryption isn't enabled in the bridge config, so the bridge couldn't read an encrypted room.")
		return
	}
	encrypted, err := matrix.IsRoomEncrypted(ce.Ctx, ce.RoomID)
	if err != nil {
		ce.Reply("Failed to check room encryption: %v", err)
		return
	} else if encrypted {
		ce.Reply("Encryption is already enabled in this room.")
		return
	}
	if len(ce.Args) != 2 || strings.ToLower(ce.Args[0]) != "on" || strings.ToLower(ce.Args[1]) != "confirm" {
		ce.Reply("This is irreversible. Element may warn that the event sender and device owner don't match. To enable encryption permanently, use `$cmdprefix encryption on confirm`.")
		return
	}
	if err = matrix.EnableRoomEncryption(ce.Ctx, ce.RoomID); err != nil {
		ce.Reply("Failed to enable encryption: %v", err)
		return
	}
	ce.Reply("Encryption is now enabled in this room. It cannot be disabled.")
}
