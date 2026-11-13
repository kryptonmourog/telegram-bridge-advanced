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
	"maunium.net/go/mautrix/id"
)

// bridgev2 writes every file above the in-memory threshold to a temporary file (preallocated to the
// full size) before uploading it, so a bridge needs as much free disk as the largest file it bridges,
// up to 4 GB with Telegram. Large files that need no processing are instead streamed from Telegram
// straight into the upload request. If the upload fails, the file is downloaded from Telegram again
// on the next attempt instead of being re-read from disk.

// directUploadIntent returns the appservice intent to stream the file to, or nil if the file has to
// go through bridgev2's regular upload (small, converted or image files, encrypted rooms).
func (t *ReadyTransferer) directUploadIntent(ctx context.Context, intent bridgev2.MatrixAPI) *matrix.ASIntent {
	asIntent, ok := intent.(*matrix.ASIntent)
	if !ok || asIntent.Connector == nil {
		return nil
	}
	size := int64(t.inner.fileInfo.Size)
	if size <= 0 || size < asIntent.Connector.Config.Matrix.UploadFileThreshold {
		return nil
	}
	// Stickers may be converted and images need their dimensions, both read the whole file.
	if t.expectedStickerConvertMime() != "" || t.inner.animatedStickerConfig != nil ||
		strings.HasPrefix(t.inner.fileInfo.MimeType, "image/") {
		return nil
	}
	if t.inner.roomID != "" {
		encrypted, err := asIntent.Connector.StateStore.IsEncrypted(ctx, t.inner.roomID)
		if err != nil || encrypted {
			return nil
		}
	}
	return asIntent
}

func (t *ReadyTransferer) uploadDirectly(ctx context.Context, asIntent *matrix.ASIntent, reader io.Reader) (id.ContentURIString, error) {
	size := int64(t.inner.fileInfo.Size)
	if limit := asIntent.Connector.MediaConfig.UploadSize; limit > 0 && size > limit {
		return "", fmt.Errorf("%w (%.2f MB > %.2f MB)", bridgev2.ErrMediaTooLarge, float64(size)/1000/1000, float64(limit)/1000/1000)
	}
	zerolog.Ctx(ctx).Debug().Int64("size", size).Msg("Streaming file from Telegram to Matrix without a temporary file")
	resp, err := asIntent.Matrix.UploadMedia(ctx, mautrix.ReqUploadMedia{
		Content:       io.LimitReader(reader, size),
		ContentLength: size,
		ContentType:   t.inner.fileInfo.MimeType,
		FileName:      t.inner.filename,
	})
	if err != nil {
		return "", err
	}
	return resp.ContentURI.CUString(), nil
}
