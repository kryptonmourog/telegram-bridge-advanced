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
	"fmt"

	"maunium.net/go/mautrix/id"
)

// The comment tables are this fork's own and are created with IF NOT EXISTS instead of being a
// numbered migration, so that they don't collide with upstream's migrations.
var commentSchema = []string{
	`CREATE TABLE IF NOT EXISTS fork_comment_thread (
		channel_id BIGINT NOT NULL,
		post_id    BIGINT NOT NULL,
		group_id   BIGINT NOT NULL,
		copy_id    BIGINT NOT NULL,
		PRIMARY KEY (channel_id, post_id)
	)`,
	`CREATE INDEX IF NOT EXISTS fork_comment_thread_copy_idx ON fork_comment_thread (group_id, copy_id)`,
	`CREATE TABLE IF NOT EXISTS fork_comment_mirror (
		group_id BIGINT NOT NULL,
		msg_id   BIGINT NOT NULL,
		copy_id  BIGINT NOT NULL,
		room_id  TEXT   NOT NULL,
		event_id TEXT   NOT NULL,
		PRIMARY KEY (group_id, msg_id)
	)`,
	`CREATE INDEX IF NOT EXISTS fork_comment_mirror_event_idx ON fork_comment_mirror (event_id)`,
	`CREATE INDEX IF NOT EXISTS fork_comment_mirror_thread_idx ON fork_comment_mirror (group_id, copy_id)`,
	// Comment threads the user takes part in (commented in); others are marked read automatically.
	`CREATE TABLE IF NOT EXISTS fork_comment_participation (
		group_id BIGINT NOT NULL,
		copy_id  BIGINT NOT NULL,
		PRIMARY KEY (group_id, copy_id)
	)`,
	// Last edit of each mirrored comment that's in Matrix, to notice edits found by polling.
	`CREATE TABLE IF NOT EXISTS fork_comment_edit (
		group_id  BIGINT NOT NULL,
		msg_id    BIGINT NOT NULL,
		edit_date BIGINT NOT NULL,
		PRIMARY KEY (group_id, msg_id)
	)`,
}

func (tc *TelegramConnector) createCommentTables(ctx context.Context) error {
	for _, q := range commentSchema {
		if _, err := tc.Store.Exec(ctx, q); err != nil {
			return fmt.Errorf("failed to create comment tables: %w", err)
		}
	}
	return nil
}

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
		ON CONFLICT (group_id, msg_id) DO NOTHING`,
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

func (tc *TelegramClient) getCommentMirror(ctx context.Context, groupID int64, msgID int) (*commentMirror, error) {
	return scanCommentMirror(tc.main.Store.QueryRow(ctx,
		`SELECT group_id, msg_id, copy_id, room_id, event_id FROM fork_comment_mirror WHERE group_id=$1 AND msg_id=$2`,
		groupID, msgID))
}

func (tc *TelegramClient) getCommentMirrorByEvent(ctx context.Context, eventID id.EventID) (*commentMirror, error) {
	return scanCommentMirror(tc.main.Store.QueryRow(ctx,
		`SELECT group_id, msg_id, copy_id, room_id, event_id FROM fork_comment_mirror WHERE event_id=$1`,
		eventID))
}

func (tc *TelegramClient) deleteCommentMirror(ctx context.Context, groupID int64, msgID int) error {
	_, err := tc.main.Store.Exec(ctx, `DELETE FROM fork_comment_mirror WHERE group_id=$1 AND msg_id=$2`, groupID, msgID)
	return err
}

// lastMirroredComment returns the newest comment of a thread already in Matrix, 0 if none.
func (tc *TelegramClient) lastMirroredComment(ctx context.Context, groupID int64, copyID int) (int, error) {
	var maxID sql.NullInt64
	err := tc.main.Store.QueryRow(ctx,
		`SELECT MAX(msg_id) FROM fork_comment_mirror WHERE group_id=$1 AND copy_id=$2`, groupID, copyID).Scan(&maxID)
	return int(maxID.Int64), err
}

func (tc *TelegramClient) getCommentEditDate(ctx context.Context, groupID int64, msgID int) (int, error) {
	var date sql.NullInt64
	err := tc.main.Store.QueryRow(ctx, `SELECT edit_date FROM fork_comment_edit WHERE group_id=$1 AND msg_id=$2`, groupID, msgID).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return int(date.Int64), err
}

func (tc *TelegramClient) saveCommentEditDate(ctx context.Context, groupID int64, msgID, editDate int) error {
	if editDate == 0 {
		return nil
	}
	_, err := tc.main.Store.Exec(ctx, `
		INSERT INTO fork_comment_edit (group_id, msg_id, edit_date) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, msg_id) DO UPDATE SET edit_date=excluded.edit_date`, groupID, msgID, editDate)
	return err
}

func (tc *TelegramClient) markCommentParticipation(ctx context.Context, groupID int64, copyID int) error {
	_, err := tc.main.Store.Exec(ctx, `INSERT INTO fork_comment_participation (group_id, copy_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, groupID, copyID)
	return err
}

func (tc *TelegramClient) isParticipatingInComments(ctx context.Context, groupID int64, copyID int) bool {
	var one int
	err := tc.main.Store.QueryRow(ctx, `SELECT 1 FROM fork_comment_participation WHERE group_id=$1 AND copy_id=$2`, groupID, copyID).Scan(&one)
	return err == nil
}

// latestCommentPerThread returns, for every comment thread, its room, post and newest comment event.
func (tc *TelegramClient) latestCommentPerThread(ctx context.Context) (out [][4]any, err error) {
	rows, err := tc.main.Store.Query(ctx, `
		SELECT m.room_id, t.channel_id, t.post_id, m.event_id
		FROM fork_comment_mirror m JOIN fork_comment_thread t ON t.group_id=m.group_id AND t.copy_id=m.copy_id
		WHERE m.msg_id = (SELECT MAX(m2.msg_id) FROM fork_comment_mirror m2 WHERE m2.group_id=m.group_id AND m2.copy_id=m.copy_id)`)
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
