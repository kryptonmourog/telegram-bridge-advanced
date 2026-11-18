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
	"database/sql"
	"errors"

	"github.com/rs/zerolog"
)

// With personal_channels, broadcast channels get a room per user (like private chats) while
// supergroups, forums included, stay shared like on Telegram, where members see each other. Both
// have channel IDs, so the portal key depends on the kind of the channel, which is remembered from
// every channel Telegram sends.

// setChannelKind remembers whether a channel is a broadcast channel.
func (tc *TelegramConnector) setChannelKind(ctx context.Context, channelID int64, broadcast bool) {
	if known, ok := tc.channelKinds.Load(channelID); ok && known.(bool) == broadcast {
		return
	}
	_, err := tc.Store.Exec(ctx, `
		INSERT INTO fork_channel_kind (channel_id, broadcast) VALUES ($1, $2)
		ON CONFLICT (channel_id) DO UPDATE SET broadcast=excluded.broadcast`, channelID, broadcast)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Int64("channel_id", channelID).Msg("Failed to save channel kind")
		return
	}
	tc.channelKinds.Store(channelID, broadcast)
}

// isBroadcastChannel reports whether a channel ID is a broadcast channel rather than a supergroup.
// A channel not seen yet is fetched from Telegram.
func (tc *TelegramClient) isBroadcastChannel(ctx context.Context, channelID int64) bool {
	if known, ok := tc.main.channelKinds.Load(channelID); ok {
		return known.(bool)
	}
	var broadcast bool
	err := tc.main.Store.QueryRow(ctx, `SELECT broadcast FROM fork_channel_kind WHERE channel_id=$1`, channelID).Scan(&broadcast)
	if err == nil {
		tc.main.channelKinds.Store(channelID, broadcast)
		return broadcast
	} else if !errors.Is(err, sql.ErrNoRows) {
		zerolog.Ctx(ctx).Err(err).Int64("channel_id", channelID).Msg("Failed to get channel kind")
		return false
	}
	// Fetching the channel saves its kind (see updateChannel).
	channel, err := tc.getSingleChannel(ctx, channelID)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Int64("channel_id", channelID).Msg("Failed to get channel kind, treating it as a supergroup")
		return false
	}
	return channel.Broadcast
}
