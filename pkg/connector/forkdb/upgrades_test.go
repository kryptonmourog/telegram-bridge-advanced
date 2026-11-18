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

package forkdb

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
)

// A database from before the fork had its own migrations: the tables were created on every start.
const legacySchema = `
CREATE TABLE user_login (bridge_id TEXT, id TEXT);
INSERT INTO user_login VALUES ('telegram', '111');
CREATE TABLE fork_comment_mirror (group_id BIGINT, msg_id BIGINT, copy_id BIGINT, room_id TEXT, event_id TEXT, PRIMARY KEY (group_id, msg_id));
INSERT INTO fork_comment_mirror VALUES (1, 10, 5, '!room', '$e10'), (1, 11, 5, '!room', '$e11');
CREATE TABLE fork_comment_edit (group_id BIGINT, msg_id BIGINT, edit_date BIGINT, PRIMARY KEY (group_id, msg_id));
INSERT INTO fork_comment_edit VALUES (1, 11, 1700000000);
CREATE TABLE fork_comment_participation (group_id BIGINT, copy_id BIGINT, PRIMARY KEY (group_id, copy_id));
INSERT INTO fork_comment_participation VALUES (1, 5);
`

func TestUpgradeLegacyTables(t *testing.T) {
	ctx := context.Background()
	db, err := dbutil.NewWithDialect("file::memory:?_foreign_keys=on", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db.RawDB.SetMaxOpenConns(1)
	if _, err = db.Exec(ctx, legacySchema); err != nil {
		t.Fatal(err)
	}
	fork := db.Child("fork_version", Table, dbutil.NoopLogger)
	if err = fork.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}

	var n int
	if err = db.QueryRow(ctx, `SELECT COUNT(*) FROM fork_comment_mirror WHERE room_id='!room'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("mirrors: %d, %v", n, err)
	}
	// The same comment can now be mirrored into another user's room.
	if _, err = db.Exec(ctx, `INSERT INTO fork_comment_mirror (group_id, msg_id, room_id, copy_id, event_id) VALUES (1, 10, '!other', 5, '$x')`); err != nil {
		t.Fatal(err)
	}
	var room string
	if err = db.QueryRow(ctx, `SELECT room_id FROM fork_comment_edit WHERE group_id=1 AND msg_id=11`).Scan(&room); err != nil || room != "!room" {
		t.Fatalf("edit room: %q, %v", room, err)
	}
	var login string
	if err = db.QueryRow(ctx, `SELECT login_id FROM fork_comment_participation WHERE group_id=1 AND copy_id=5`).Scan(&login); err != nil || login != "111" {
		t.Fatalf("participation login: %q, %v", login, err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO fork_channel_kind (channel_id, broadcast) VALUES (1, true)`); err != nil {
		t.Fatal(err)
	}
	// Upgrading again does nothing.
	if err = fork.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeFreshDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := dbutil.NewWithDialect("file::memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db.RawDB.SetMaxOpenConns(1)
	if _, err = db.Exec(ctx, `CREATE TABLE user_login (bridge_id TEXT, id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Child("fork_version", Table, dbutil.NoopLogger).Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
}
