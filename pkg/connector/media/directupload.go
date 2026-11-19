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
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"maunium.net/go/mautrix/mediaproxy"
)

// bridgev2 writes every file above the in-memory threshold to a temporary file (preallocated to the
// full size) before uploading it, so a bridge needs as much free disk as the largest file it bridges,
// up to 4 GB with Telegram. Large files that need no processing are instead streamed from Telegram
// straight into the upload request, encrypted on the fly in encrypted rooms (AES-CTR needs no look
// ahead, and the hash of the encrypted file is only needed in the event sent after the upload). If
// the upload fails, the file is downloaded from Telegram again on the next attempt instead of being
// re-read from disk.

// directUploadIntent returns the appservice intent to stream the file to and whether the room is
// encrypted, or nil if the file has to go through bridgev2's regular upload (small, converted or
// image files).
func (t *ReadyTransferer) directUploadIntent(ctx context.Context, intent bridgev2.MatrixAPI) (*matrix.ASIntent, bool) {
	asIntent, ok := intent.(*matrix.ASIntent)
	if !ok || asIntent.Connector == nil {
		return nil, false
	}
	size := int64(t.inner.fileInfo.Size)
	if size <= 0 || size < asIntent.Connector.Config.Matrix.UploadFileThreshold {
		return nil, false
	}
	// Stickers may be converted and images need their dimensions, both read the whole file.
	if t.expectedStickerConvertMime() != "" || t.inner.animatedStickerConfig != nil ||
		strings.HasPrefix(t.inner.fileInfo.MimeType, "image/") {
		return nil, false
	}
	// The same check bridgev2's upload uses, which also covers encryption.require.
	encrypted := false
	if t.inner.roomID != "" {
		var err error
		if encrypted, err = asIntent.Connector.IsRoomEncrypted(ctx, t.inner.roomID); err != nil {
			return nil, false
		}
	}
	return asIntent, encrypted
}

func (t *ReadyTransferer) uploadDirectly(ctx context.Context, asIntent *matrix.ASIntent, reader io.Reader, encrypt bool) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	size := int64(t.inner.fileInfo.Size)
	if limit := asIntent.Connector.MediaConfig.UploadSize; limit > 0 && size > limit {
		return "", nil, fmt.Errorf("%w (%.2f MB > %.2f MB)", bridgev2.ErrMediaTooLarge, float64(size)/1000/1000, float64(limit)/1000/1000)
	}
	req := mautrix.ReqUploadMedia{
		Content:       io.LimitReader(reader, size),
		ContentLength: size,
		ContentType:   t.inner.fileInfo.MimeType,
		FileName:      t.inner.filename,
	}
	var file *event.EncryptedFileInfo
	var encrypting io.ReadCloser
	if encrypt {
		// Like bridgev2's upload: the type and name of an encrypted file are only in the event.
		file = &event.EncryptedFileInfo{EncryptedFile: *attachment.NewEncryptedFile()}
		encrypting = file.EncryptStream(req.Content)
		req.Content = encrypting
		req.ContentType = "application/octet-stream"
		req.FileName = ""
	}
	zerolog.Ctx(ctx).Debug().Int64("size", size).Bool("encrypted", encrypt).Msg("Streaming file from Telegram to Matrix without a temporary file")
	resp, err := asIntent.Matrix().UploadMedia(ctx, req)
	if err != nil {
		return "", nil, err
	}
	if file != nil {
		// Closing fills in the hash of the encrypted file.
		if err = encrypting.Close(); err != nil {
			return "", nil, fmt.Errorf("failed to finish encrypting file: %w", err)
		}
		file.URL = resp.ContentURI.CUString()
		return "", file, nil
	}
	return resp.ContentURI.CUString(), nil, nil
}

// Encrypted direct media: in encrypted rooms a file can be served on request like in channels instead
// of being uploaded when the message is bridged. Clients check the hash of the encrypted file, which is
// in the event, so the file is downloaded once when the message is bridged to compute it (nothing is
// uploaded), and encrypted again with the same key when it's requested: AES-CTR with the same key and
// IV turns the same file into the same bytes.

// EncryptForDirectMedia downloads the file and encrypts it with a new key, only to compute the hash of
// the encrypted file.
func (t *ReadyTransferer) EncryptForDirectMedia(ctx context.Context) (*attachment.EncryptedFile, *event.FileInfo, error) {
	reader, _, size, err := t.Stream(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("downloading file failed: %w", err)
	}
	file := attachment.NewEncryptedFile()
	encrypting := file.EncryptStream(io.LimitReader(reader, int64(size)))
	n, err := io.Copy(io.Discard, encrypting)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encrypt file: %w", err)
	} else if n != int64(size) {
		return nil, nil, fmt.Errorf("file ended after %d of %d bytes", n, size)
	}
	// Closing fills in the hash.
	if err = encrypting.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to finish encrypting file: %w", err)
	}
	if t.inner.fileInfo.MimeType == "" {
		t.inner.fileInfo.MimeType = "application/octet-stream"
	}
	return file, &t.inner.fileInfo, nil
}

// FileInfo is what's known about the file without downloading it.
func (t *ReadyTransferer) FileInfo() *event.FileInfo {
	if t.inner.fileInfo.MimeType == "" {
		t.inner.fileInfo.MimeType = "application/octet-stream"
	}
	return &t.inner.fileInfo
}

// ToEncryptedDirectMediaResponse serves the file encrypted with the key it was bridged with.
func (t *ReadyTransferer) ToEncryptedDirectMediaResponse(ctx context.Context, file *attachment.EncryptedFile) (mediaproxy.GetMediaResponse, error) {
	reader, _, size, err := t.Stream(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to download media")
		return nil, err
	}
	return &mediaproxy.GetMediaResponseData{
		Reader:        file.EncryptStream(io.LimitReader(reader, int64(size))),
		ContentType:   "application/octet-stream",
		ContentLength: int64(size),
	}, nil
}

// defaultUploadSize is what bridgev2 assumes until it has fetched the homeserver's media config.
const defaultUploadSize = 50 * 1024 * 1024

// waitForMediaConfig waits for bridgev2 to fetch the homeserver's upload limit. It's fetched in the
// background on startup, and until then files over 50 MB would be rejected as too large even if the
// homeserver accepts them (backfill starts right away). Smaller files fit the default anyway.
func waitForMediaConfig(ctx context.Context, intent bridgev2.MatrixAPI, size int64) {
	asIntent, ok := intent.(*matrix.ASIntent)
	if !ok || asIntent.Connector == nil || size <= defaultUploadSize {
		return
	}
	// A cancelled context fails the transfer right after this anyway.
	_ = asIntent.Connector.WaitForMediaConfig(ctx)
}
