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
	"fmt"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Chat sync only bridged a forum's space and its General topic: other topics got a room only when a
// new message arrived in them. syncForumTopics lists the topics of a forum and creates (or catches up)
// a room for each of them, with the usual initial backfill.

// generalTopicID is the General topic, bridged as the forum's main room.
const generalTopicID = 1

func (tc *TelegramClient) syncForumTopics(ctx context.Context, channel *tg.Channel, allowCreate bool) {
	log := zerolog.Ctx(ctx).With().Int64("forum_id", channel.ID).Logger()
	ctx = log.WithContext(ctx)
	topics, err := tc.listForumTopics(ctx, channel)
	if err != nil {
		log.Err(err).Msg("Failed to list forum topics")
		return
	}
	queued := 0
	for _, topic := range topics {
		if topic.ID == generalTopicID {
			continue
		}
		key := tc.makePortalKeyFromID(ids.PeerTypeChannel, channel.ID, topic.ID)
		portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, key)
		if err != nil {
			log.Err(err).Int("topic_id", topic.ID).Msg("Failed to get topic portal")
			continue
		}
		exists := portal != nil && portal.MXID != ""
		if !exists && !allowCreate {
			continue
		}
		topMessage := topic.TopMessage
		res := tc.main.Bridge.QueueRemoteEvent(tc.userLogin, &simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				PortalKey:    key,
				CreatePortal: true,
				LogContext: func(c zerolog.Context) zerolog.Context {
					return c.Str("update", "forum topic sync").Int("topic_id", topic.ID)
				},
			},
			GetChatInfoFunc: tc.GetChatInfo,
			CheckNeedsBackfillFunc: func(ctx context.Context, latestMessage *database.Message) (bool, error) {
				if latestMessage == nil {
					return true, nil
				}
				_, latestMessageID, err := ids.ParseMessageID(latestMessage.ID)
				if err != nil {
					return true, nil
				}
				return topMessage > latestMessageID, nil
			},
		})
		if err = resultToError(res); err != nil {
			log.Err(err).Int("topic_id", topic.ID).Msg("Failed to sync forum topic")
			continue
		}
		queued++
	}
	log.Debug().Int("topics", len(topics)).Int("synced", queued).Msg("Synced forum topics")
}

// listForumTopics returns all topics of a forum.
func (tc *TelegramClient) listForumTopics(ctx context.Context, channel *tg.Channel) ([]*tg.ForumTopic, error) {
	peer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
	req := &tg.MessagesGetForumTopicsRequest{Peer: peer, Limit: 100}
	var out []*tg.ForumTopic
	seen := map[int]struct{}{}
	for page := 0; page < 100; page++ {
		resp, err := APICallWithUpdates(ctx, tc, func() (*tg.MessagesForumTopics, error) {
			return tc.client.API().MessagesGetForumTopics(ctx, req)
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get forum topics: %w", err)
		}
		var last *tg.ForumTopic
		for _, t := range resp.Topics {
			topic, ok := t.(*tg.ForumTopic)
			if !ok {
				continue
			}
			last = topic
			if _, dup := seen[topic.ID]; dup {
				continue
			}
			seen[topic.ID] = struct{}{}
			out = append(out, topic)
		}
		if len(resp.Topics) < req.Limit || last == nil || len(out) >= resp.Count {
			break
		}
		req.OffsetTopic = last.ID
		req.OffsetID = last.TopMessage
		req.OffsetDate = 0
		for _, m := range resp.Messages {
			if msg, ok := m.AsNotEmpty(); ok && msg.GetID() == last.TopMessage {
				req.OffsetDate = msg.GetDate()
				break
			}
		}
	}
	return out, nil
}
