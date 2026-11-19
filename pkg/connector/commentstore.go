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

	"maunium.net/go/mautrix/id"
)

// commentThread links a channel post with its copy in the channel's discussion group, which is the
// root that comments reply to.
type commentThread struct {
	ChannelID int64
	PostID    int
	GroupID   int64
	CopyID    int
}

func (tc *TelegramClient) saveCommentThread(ctx context.Context, t commentThread) error {
	_, err := tc.main.Store.Exec(ctx, `
		INSERT INTO fork_comment_thread (channel_id, post_id, group_id, copy_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (channel_id, post_id) DO UPDATE SET group_id=excluded.group_id, copy_id=excluded.copy_id`,
		t.ChannelID, t.PostID, t.GroupID, t.CopyID)
	return err
}

func (tc *TelegramClient) scanCommentThread(row interface{ Scan(...any) error }) (*commentThread, error) {
	var t commentThread
	err := row.Scan(&t.ChannelID, &t.PostID, &t.GroupID, &t.CopyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &t, err
}

func (tc *TelegramClient) getCommentThreadByCopy(ctx context.Context, groupID int64, copyID int) (*commentThread, error) {
	return tc.scanCommentThread(tc.main.Store.QueryRow(ctx,
		`SELECT channel_id, post_id, group_id, copy_id FROM fork_comment_thread WHERE group_id=$1 AND copy_id=$2`,
		groupID, copyID))
}

func (tc *TelegramClient) getCommentThreadByPost(ctx context.Context, channelID int64, postID int) (*commentThread, error) {
	return tc.scanCommentThread(tc.main.Store.QueryRow(ctx,
		`SELECT channel_id, post_id, group_id, copy_id FROM fork_comment_thread WHERE channel_id=$1 AND post_id=$2`,
		channelID, postID))
}

// commentMirror is a comment sent to the channel room as a thread message.
type commentMirror struct {
	GroupID int64
	MsgID   int
	CopyID  int
	RoomID  id.RoomID
	EventID id.EventID
}

func (tc *TelegramClient) saveCommentMirror(ctx context.Context, m commentMirror) error {
	_, err := tc.main.Store.Exec(ctx, `
		INSERT INTO fork_comment_mirror (group_id, msg_id, copy_id, room_id, event_id) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id, msg_id, room_id) DO NOTHING`,
		m.GroupID, m.MsgID, m.CopyID, m.RoomID, m.EventID)
	return err
}

func scanCommentMirror(row interface{ Scan(...any) error }) (*commentMirror, error) {
	var m commentMirror
	err := row.Scan(&m.GroupID, &m.MsgID, &m.CopyID, &m.RoomID, &m.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// getCommentMirror returns the mirror of a comment in a channel room. With personal channel rooms a
// comment is mirrored into the room of every user subscribed to the channel.
func (tc *TelegramClient) getCommentMirror(ctx context.Context, room id.RoomID, groupID int64, msgID int) (*commentMirror, error) {
	return scanCommentMirror(tc.main.Store.QueryRow(ctx,
		`SELECT group_id, msg_id, copy_id, room_id, event_id FROM fork_comment_mirror WHERE group_id=$1 AND msg_id=$2 AND room_id=$3`,
		groupID, msgID, room))
}

func (tc *TelegramClient) getCommentMirrorByEvent(ctx context.Context, eventID id.EventID) (*commentMirror, error) {
	return scanCommentMirror(tc.main.Store.QueryRow(ctx,
		`SELECT group_id, msg_id, copy_id, room_id, event_id FROM fork_comment_mirror WHERE event_id=$1`,
		eventID))
}

func (tc *TelegramClient) deleteCommentMirror(ctx context.Context, room id.RoomID, groupID int64, msgID int) error {
	_, err := tc.main.Store.Exec(ctx, `DELETE FROM fork_comment_mirror WHERE group_id=$1 AND msg_id=$2 AND room_id=$3`, groupID, msgID, room)
	return err
}

// lastMirroredComment returns the newest comment of a thread already in a channel room, 0 if none.
func (tc *TelegramClient) lastMirroredComment(ctx context.Context, room id.RoomID, groupID int64, copyID int) (int, error) {
	var maxID sql.NullInt64
	err := tc.main.Store.QueryRow(ctx,
		`SELECT MAX(msg_id) FROM fork_comment_mirror WHERE room_id=$1 AND group_id=$2 AND copy_id=$3`, room, groupID, copyID).Scan(&maxID)
	return int(maxID.Int64), err
}

func (tc *TelegramClient) getCommentEditDate(ctx context.Context, room id.RoomID, groupID int64, msgID int) (int, error) {
	var date sql.NullInt64
	err := tc.main.Store.QueryRow(ctx, `SELECT edit_date FROM fork_comment_edit WHERE group_id=$1 AND msg_id=$2 AND room_id=$3`, groupID, msgID, room).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return int(date.Int64), err
}

func (tc *TelegramClient) saveCommentEditDate(ctx context.Context, room id.RoomID, groupID int64, msgID, editDate int) error {
	if editDate == 0 {
		return nil
	}
	_, err := tc.main.Store.Exec(ctx, `
		INSERT INTO fork_comment_edit (group_id, msg_id, room_id, edit_date) VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id, msg_id, room_id) DO UPDATE SET edit_date=excluded.edit_date`, groupID, msgID, room, editDate)
	return err
}

// markCommentParticipation records that the user commented in a thread.
func (tc *TelegramClient) markCommentParticipation(ctx context.Context, groupID int64, copyID int) error {
	_, err := tc.main.Store.Exec(ctx, `INSERT INTO fork_comment_participation (login_id, group_id, copy_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		tc.userLogin.ID, groupID, copyID)
	return err
}

func (tc *TelegramClient) isParticipatingInComments(ctx context.Context, groupID int64, copyID int) bool {
	var one int
	err := tc.main.Store.QueryRow(ctx, `SELECT 1 FROM fork_comment_participation WHERE login_id=$1 AND group_id=$2 AND copy_id=$3`,
		tc.userLogin.ID, groupID, copyID).Scan(&one)
	return err == nil
}

// channelOfGroup returns the channel a discussion group belongs to, 0 if unknown.
func (tc *TelegramClient) channelOfGroup(ctx context.Context, groupID int64) (int64, error) {
	var channelID int64
	err := tc.main.Store.QueryRow(ctx, `SELECT channel_id FROM fork_comment_thread WHERE group_id=$1 LIMIT 1`, groupID).Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return channelID, err
}

// latestCommentPerThread returns, for every comment thread, its room, post and newest comment event.
func (tc *TelegramClient) latestCommentPerThread(ctx context.Context) (out [][4]any, err error) {
	rows, err := tc.main.Store.Query(ctx, `
		SELECT m.room_id, t.channel_id, t.post_id, m.event_id
		FROM (SELECT room_id, group_id, copy_id, MAX(msg_id) AS msg_id FROM fork_comment_mirror
		      GROUP BY room_id, group_id, copy_id) latest
		JOIN fork_comment_mirror m ON m.room_id=latest.room_id AND m.group_id=latest.group_id AND m.msg_id=latest.msg_id
		JOIN fork_comment_thread t ON t.group_id=m.group_id AND t.copy_id=m.copy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var room, eventID string
		var channelID int64
		var postID int
		if err = rows.Scan(&room, &channelID, &postID, &eventID); err != nil {
			return nil, err
		}
		out = append(out, [4]any{id.RoomID(room), channelID, postID, id.EventID(eventID)})
	}
	return out, rows.Err()
}
