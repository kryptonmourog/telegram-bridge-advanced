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

// Package forkdb has the database migrations of this fork's own tables. They are versioned in their
// own table (fork_version), so that they never collide with upstream's migrations.
package forkdb

import (
	"embed"

	"go.mau.fi/util/dbutil"
)

//go:embed *.sql
var rawUpgrades embed.FS

var Table = dbutil.BuildUpgradeTable().WithFS(rawUpgrades).Finish()
