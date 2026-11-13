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

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

func TestCommentRootID(t *testing.T) {
	direct := &tg.MessageReplyHeader{ReplyToMsgID: 10}
	nested := &tg.MessageReplyHeader{ReplyToMsgID: 15}
	nested.SetReplyToTopID(10)
	topic := &tg.MessageReplyHeader{ReplyToMsgID: 3, ForumTopic: true}

	assert.Equal(t, 10, commentRootID(&tg.Message{ReplyTo: direct}), "a comment replies to the post copy")
	assert.Equal(t, 10, commentRootID(&tg.Message{ReplyTo: nested}), "a reply to a comment keeps the thread root")
	assert.Equal(t, 0, commentRootID(&tg.Message{ReplyTo: topic}), "forum topics aren't comment threads")
	assert.Equal(t, 0, commentRootID(&tg.Message{}))
}

func TestPostIDsWithComments(t *testing.T) {
	withComments := func(msgID, count int) *tg.Message {
		msg := &tg.Message{ID: msgID}
		msg.SetReplies(tg.MessageReplies{Comments: true, Replies: count})
		return msg
	}
	replies := &tg.Message{ID: 4}
	replies.SetReplies(tg.MessageReplies{Replies: 5}) // replies in a group, not comments
	assert.Equal(t, []int{1}, postIDsWithComments([]*tg.Message{withComments(1, 3), withComments(2, 0), {ID: 3}, replies}))
}

func TestSentMessageID(t *testing.T) {
	assert.Equal(t, 42, sentMessageID(&tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 42}}}))
	assert.Equal(t, 7, sentMessageID(&tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 7}}}}))
	assert.Equal(t, 0, sentMessageID(&tg.UpdatesTooLong{}))
}
