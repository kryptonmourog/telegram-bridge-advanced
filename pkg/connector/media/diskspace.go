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

package media

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrNotEnoughDiskSpace is returned when a file wouldn't fit on the bridge's disk. Large files are
// kept in a temporary file while they're uploaded, and filling the disk would also break the
// bridge's database.
var ErrNotEnoughDiskSpace = errors.New("not enough disk space on the bridge server")

const (
	// Files up to this size are held in memory by the upload, not on disk.
	diskSpaceCheckMinSize = 5 * 1024 * 1024
	// Space that has to stay free besides the file, for the database and other uploads.
	diskSpaceReserve = 300 * 1024 * 1024
)

func checkTempDiskSpace(size int64) error {
	if size <= diskSpaceCheckMinSize {
		return nil
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &stat); err != nil {
		return nil
	}
	free := int64(stat.Bavail) * int64(stat.Bsize)
	if free < size+diskSpaceReserve {
		return fmt.Errorf("%w: %d MB free, file is %d MB", ErrNotEnoughDiskSpace, free/1024/1024, size/1024/1024)
	}
	return nil
}
