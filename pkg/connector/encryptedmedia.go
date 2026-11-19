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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/connector/media"
)

// Encrypted direct media (encrypted_direct_media): files in encrypted rooms are served on request,
// encrypted with a key kept per media ID. See media/directupload.go for how the file is encrypted.

func encryptedMediaKey(mediaID networkid.MediaID) string {
	return base64.RawURLEncoding.EncodeToString(mediaID)
}

func (tc *TelegramConnector) getEncryptedMediaFile(ctx context.Context, mediaID networkid.MediaID) (*attachment.EncryptedFile, error) {
	var raw string
	err := tc.Store.QueryRow(ctx, `SELECT file FROM fork_encrypted_media WHERE media_id=$1`, encryptedMediaKey(mediaID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var file attachment.EncryptedFile
	if err = json.Unmarshal([]byte(raw), &file); err != nil {
		return nil, fmt.Errorf("failed to parse stored encryption info: %w", err)
	}
	return &file, nil
}

func (tc *TelegramConnector) saveEncryptedMediaFile(ctx context.Context, mediaID networkid.MediaID, file *attachment.EncryptedFile) error {
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	// The first key stays: events already sent carry it.
	_, err = tc.Store.Exec(ctx, `INSERT INTO fork_encrypted_media (media_id, file) VALUES ($1, $2) ON CONFLICT (media_id) DO NOTHING`,
		encryptedMediaKey(mediaID), string(raw))
	return err
}

// shouldUseEncryptedDirectMedia reports whether a file of the given size in the portal's room is
// served on request instead of being uploaded.
func (tc *TelegramClient) shouldUseEncryptedDirectMedia(ctx context.Context, portal *bridgev2.Portal, size int64) bool {
	mode := tc.main.Config.EncryptedDirectMedia
	if !tc.main.useDirectMedia || (mode != EncryptedDirectMediaLarge && mode != EncryptedDirectMediaAll) || portal.MXID == "" {
		return false
	}
	mc, ok := tc.main.Bridge.Matrix.(*matrix.Connector)
	if !ok {
		return false
	}
	if mode == EncryptedDirectMediaLarge && size < mc.Config.Matrix.UploadFileThreshold {
		return false
	}
	encrypted, err := mc.IsRoomEncrypted(ctx, portal.MXID)
	return err == nil && encrypted
}

// encryptedDirectMediaFile returns the encrypted file info for the event of a file served on request.
// A file bridged before keeps its key, so it's not downloaded again.
func (tc *TelegramClient) encryptedDirectMediaFile(ctx context.Context, transferer *media.ReadyTransferer, info ids.DirectMediaInfo) (*event.EncryptedFileInfo, *event.FileInfo, error) {
	info.Encrypted = true
	mediaID, err := info.AsMediaID()
	if err != nil {
		return nil, nil, err
	}
	file, err := tc.main.getEncryptedMediaFile(ctx, mediaID)
	if err != nil {
		return nil, nil, err
	}
	fileInfo := transferer.FileInfo()
	if file == nil {
		if file, fileInfo, err = transferer.EncryptForDirectMedia(ctx); err != nil {
			return nil, nil, err
		}
		if err = tc.main.saveEncryptedMediaFile(ctx, mediaID, file); err != nil {
			return nil, nil, fmt.Errorf("failed to save encryption info: %w", err)
		}
		// A concurrent bridging of the same file may have saved its key first.
		if file, err = tc.main.getEncryptedMediaFile(ctx, mediaID); err != nil || file == nil {
			return nil, nil, fmt.Errorf("failed to read back encryption info: %w", err)
		}
	}
	mxc, err := tc.main.Bridge.Matrix.GenerateContentURI(ctx, mediaID)
	if err != nil {
		return nil, nil, err
	}
	return &event.EncryptedFileInfo{EncryptedFile: *file, URL: mxc}, fileInfo, nil
}
