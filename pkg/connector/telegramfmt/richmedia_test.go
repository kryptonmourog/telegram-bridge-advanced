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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

func plain(s string) tg.RichTextClass { return &tg.TextPlain{Text: s} }

var richMediaBlocks = []tg.PageBlockClass{
	&tg.PageBlockParagraph{Text: plain("before")},
	&tg.PageBlockPhoto{PhotoID: 1, Caption: tg.PageCaption{Text: plain("Books"), Credit: &tg.TextEmpty{}}},
	&tg.PageBlockParagraph{Text: plain("middle")},
	&tg.PageBlockPhoto{PhotoID: 2, Spoiler: true, Caption: tg.PageCaption{Text: &tg.TextEmpty{}, Credit: &tg.TextEmpty{}}},
	&tg.PageBlockCollage{
		Items: []tg.PageBlockClass{
			&tg.PageBlockPhoto{PhotoID: 3, Caption: tg.PageCaption{Text: &tg.TextEmpty{}, Credit: &tg.TextEmpty{}}},
			&tg.PageBlockVideo{VideoID: 10, Caption: tg.PageCaption{Text: &tg.TextEmpty{}, Credit: &tg.TextEmpty{}}},
		},
		Caption: tg.PageCaption{Text: &tg.TextCustomEmoji{DocumentID: 99, Alt: "😀"}, Credit: plain("me")},
	},
	&tg.PageBlockCover{Cover: &tg.PageBlockAudio{AudioID: 11, Caption: tg.PageCaption{Text: &tg.TextEmpty{}, Credit: &tg.TextEmpty{}}}},
	&tg.PageBlockParagraph{Text: plain("after")},
}

func TestFindRichTextMedia(t *testing.T) {
	media := FindRichTextMedia(richMediaBlocks)
	assert.Equal(t, []int64{1, 2, 3}, media.PhotoIDs)
	assert.Equal(t, []int64{10, 11}, media.DocumentIDs)
	assert.Equal(t, []int64{99}, FindRichTextCustomEmojis(richMediaBlocks), "emojis in page captions are found")
}

func TestParseRichText_Photos(t *testing.T) {
	params := FormatParams{}.WithRichPhotos(map[int64]RichPhoto{
		1: {URL: "mxc://example.com/one", Width: 1280, Height: 960},
		2: {URL: "mxc://example.com/two", Width: 200, Height: 100},
	})
	content := ParseRichText(context.Background(), &tg.RichMessage{Blocks: richMediaBlocks}, params)
	h := content.FormattedBody

	one := `<img src="mxc://example.com/one" alt="Photo" width="480" height="360">`
	assert.Contains(t, h, one, "large photos are scaled to the max width")
	assert.Contains(t, h, "<em>Books</em>")
	assert.Contains(t, h, `<span data-mx-spoiler><img src="mxc://example.com/two" alt="Photo" width="200" height="100"></span>`)
	assert.Contains(t, h, "Photo (open in Telegram)", "photo 3 wasn't uploaded")
	assert.Contains(t, h, "🎬 <em>Video (sent below)</em>")
	assert.Contains(t, h, "🎵 <em>Audio (sent below)</em>")
	assert.Contains(t, h, " — me</em>", "credit follows the caption")
	assert.NotContains(t, h, "Unsupported block type")

	// photos stay where the post has them
	order := []string{"before", one, "middle", "mxc://example.com/two", "open in Telegram", "Video", "Audio", "after"}
	last := -1
	for _, s := range order {
		i := strings.Index(h, s)
		assert.Greater(t, i, last, s)
		last = i
	}
}
