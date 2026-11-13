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
	"testing"

	"github.com/stretchr/testify/assert"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

func TestChunkedBackfillHold(t *testing.T) {
	var cb chunkedBackfill
	a := networkid.PortalKey{ID: "channel:1"}
	b := networkid.PortalKey{ID: "channel:2"}

	assert.False(t, cb.hold(a, func() {}), "nothing is held without a running backfill")

	released := false
	cb.start(a, func() { released = true })
	cb.start(a, func() { released = true }) // a second chunk keeps the same state
	var got []int
	assert.True(t, cb.hold(a, func() { got = append(got, 1) }))
	assert.True(t, cb.hold(a, func() { got = append(got, 2) }))
	assert.False(t, cb.hold(b, func() {}), "other rooms are not held")

	for _, fn := range cb.finish(a) {
		fn()
	}
	assert.Equal(t, []int{1, 2}, got, "held events are released in order")
	assert.False(t, cb.hold(a, func() {}), "finished backfill doesn't hold any more")
	assert.Nil(t, cb.finish(a))
	assert.False(t, released, "the safety timer is stopped")
}

func TestIsLargeBackfillMedia(t *testing.T) {
	withMedia := func(media tg.MessageMediaClass) *tg.Message {
		msg := &tg.Message{}
		msg.SetMedia(media)
		return msg
	}
	video := func(size int64) *tg.Message {
		return withMedia(&tg.MessageMediaDocument{Document: &tg.Document{Size: size}})
	}
	const mb = 1024 * 1024
	assert.True(t, isLargeBackfillMedia(video(11*mb), 10*mb))
	assert.False(t, isLargeBackfillMedia(video(10*mb), 10*mb))
	assert.False(t, isLargeBackfillMedia(video(500*mb), 0), "splitting disabled")
	assert.False(t, isLargeBackfillMedia(&tg.Message{Message: "text"}, 10*mb))
	assert.False(t, isLargeBackfillMedia(withMedia(&tg.MessageMediaPhoto{}), 10*mb))
}
