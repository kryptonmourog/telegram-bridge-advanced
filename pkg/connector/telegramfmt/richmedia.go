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

package telegramfmt

import (
	"context"
	"strconv"

	"golang.org/x/net/html"
	"maunium.net/go/mautrix/id"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// RichPhoto is a photo of a rich message that has been uploaded to Matrix, so that it can be shown
// inline with <img> where the rich message has it.
type RichPhoto struct {
	URL    id.ContentURIString
	Width  int
	Height int
}

// richPhotoMaxWidth is the width photos are scaled down to in the HTML, clients show them smaller
// if they need to.
const richPhotoMaxWidth = 480

// RichMedia lists the photos and documents (videos and audio) that the blocks of a rich message
// refer to, in the order they appear.
type RichMedia struct {
	PhotoIDs    []int64
	DocumentIDs []int64
}

// FindRichTextMedia finds the photos and documents shown by rich message blocks, including the ones
// inside covers, collages, slideshows and other nested blocks.
func FindRichTextMedia(blocks []tg.PageBlockClass) (out RichMedia) {
	findRichTextMediaInBlocks(&out, blocks)
	return
}

func findRichTextMediaInBlocks(out *RichMedia, blocks []tg.PageBlockClass) {
	for _, block := range blocks {
		switch typed := block.(type) {
		case *tg.PageBlockPhoto:
			out.PhotoIDs = append(out.PhotoIDs, typed.PhotoID)
		case *tg.PageBlockVideo:
			out.DocumentIDs = append(out.DocumentIDs, typed.VideoID)
		case *tg.PageBlockAudio:
			out.DocumentIDs = append(out.DocumentIDs, typed.AudioID)
		case *tg.PageBlockCover:
			findRichTextMediaInBlocks(out, []tg.PageBlockClass{typed.Cover})
		case *tg.PageBlockCollage:
			findRichTextMediaInBlocks(out, typed.Items)
		case *tg.PageBlockSlideshow:
			findRichTextMediaInBlocks(out, typed.Items)
		case blocksable:
			findRichTextMediaInBlocks(out, typed.GetBlocks())
		}
	}
}

// pageCaptionBlock is a block with a caption and credit (photos, videos, collages...), unlike the
// blocks with a plain rich text caption that captionable covers.
type pageCaptionBlock interface {
	GetCaption() tg.PageCaption
}

func (r *rtParser) writePageCaption(ctx context.Context, caption tg.PageCaption) {
	hasText := !isEmptyRichText(caption.Text)
	hasCredit := !isEmptyRichText(caption.Credit)
	if !hasText && !hasCredit {
		return
	}
	r.writeOpeningTag("p")
	r.writeOpeningTag("em")
	if hasText {
		r.parseItem(ctx, caption.Text)
	}
	if hasText && hasCredit {
		r.printf(" — ")
	}
	if hasCredit {
		r.parseItem(ctx, caption.Credit)
	}
	r.printf("</em></p>")
}

func isEmptyRichText(text tg.RichTextClass) bool {
	switch typed := text.(type) {
	case nil, *tg.TextEmpty:
		return true
	case *tg.TextPlain:
		return typed.Text == ""
	}
	return false
}

func (r *rtParser) writePhoto(ctx context.Context, block *tg.PageBlockPhoto) {
	photo, ok := r.RichPhotos[block.PhotoID]
	if !ok {
		r.printf("<p>🖼️ <em>Photo (open in Telegram)</em></p>")
		r.writePageCaption(ctx, block.Caption)
		return
	}
	width, height := photo.Width, photo.Height
	if width > richPhotoMaxWidth {
		height = height * richPhotoMaxWidth / width
		width = richPhotoMaxWidth
	}
	attrs := []html.Attribute{{Key: "src", Val: string(photo.URL)}, {Key: "alt", Val: "Photo"}}
	if width > 0 && height > 0 {
		attrs = append(attrs,
			html.Attribute{Key: "width", Val: strconv.Itoa(width)},
			html.Attribute{Key: "height", Val: strconv.Itoa(height)},
		)
	}
	r.writeOpeningTag("p")
	if block.Spoiler {
		r.writeOpeningTag("span", html.Attribute{Key: "data-mx-spoiler"})
	}
	if block.URL != "" {
		r.writeOpeningTag("a", html.Attribute{Key: "href", Val: block.URL})
	}
	r.writeTagAndAttrs("img", true, attrs...)
	if block.URL != "" {
		r.printf("</a>")
	}
	if block.Spoiler {
		r.printf("</span>")
	}
	r.printf("</p>")
	r.writePageCaption(ctx, block.Caption)
}

// writeAttachedMedia marks the place of a video or audio block. Clients can't show those inline, so
// the bridge sends them as separate messages right after the rich message.
func (r *rtParser) writeAttachedMedia(ctx context.Context, emoji, name string, caption tg.PageCaption) {
	r.printf("<p>%s <em>%s (sent below)</em></p>", emoji, name)
	r.writePageCaption(ctx, caption)
}
