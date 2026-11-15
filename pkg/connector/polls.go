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
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-telegram/pkg/connector/ids"
	"go.mau.fi/mautrix-telegram/pkg/connector/matrixfmt"
	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

// Telegram polls are bridged as Matrix polls (MSC3381) in both directions.
//
// Matrix computes poll results from the votes of the room's members, while Telegram only tells the
// totals (and, in anonymous polls, never who voted). So a poll from Telegram is two parts: the Matrix
// poll, which is "undisclosed" so that Matrix doesn't show its own, meaningless counts, and a notice
// with Telegram's results that is edited as they change. The user's own vote is bridged both ways.
//
// Answer IDs of polls from Telegram are the base64 of Telegram's option bytes; polls created in
// Matrix keep the client's answer IDs, mapped to Telegram's options in fork_poll.

const (
	pollResultsPartID  networkid.PartID = "poll-results"
	pollVotePartPrefix                  = "vote:"
	// pollResultsDelay groups result updates of busy polls into one edit.
	pollResultsDelay = 15 * time.Second
)

var pollSchema = []string{
	`CREATE TABLE IF NOT EXISTS fork_poll (
		login_id   TEXT   NOT NULL,
		poll_id    BIGINT NOT NULL,
		portal_id  TEXT   NOT NULL,
		receiver   TEXT   NOT NULL,
		msg_id     BIGINT NOT NULL,
		poll       TEXT   NOT NULL,
		answer_map TEXT   NOT NULL DEFAULT '',
		own_vote   TEXT   NOT NULL DEFAULT '',
		PRIMARY KEY (login_id, poll_id)
	)`,
	`CREATE INDEX IF NOT EXISTS fork_poll_msg_idx ON fork_poll (login_id, portal_id, receiver, msg_id)`,
}

func (tc *TelegramConnector) createPollTables(ctx context.Context) error {
	for _, q := range pollSchema {
		if _, err := tc.Store.Exec(ctx, q); err != nil {
			return fmt.Errorf("failed to create poll tables: %w", err)
		}
	}
	return nil
}

// storedPoll is what's needed to render a poll's results when an update doesn't include the poll.
type storedPoll struct {
	Question string         `json:"question"`
	Answers  []storedAnswer `json:"answers"`
	Quiz     bool           `json:"quiz,omitempty"`
	Multiple bool           `json:"multiple,omitempty"`
	Closed   bool           `json:"closed,omitempty"`
}

type storedAnswer struct {
	Option string `json:"option"` // base64 of the option bytes
	Text   string `json:"text"`
}

type pollRow struct {
	PollID    int64
	PortalKey networkid.PortalKey
	MsgID     int
	Poll      storedPoll
	// AnswerMap maps Matrix answer IDs to options, for polls created in Matrix.
	AnswerMap map[string]string
	// OwnVote is the user's last known vote: sorted options joined with commas.
	OwnVote string
}

func storePoll(poll *tg.Poll) storedPoll {
	sp := storedPoll{
		Question: poll.Question.Text,
		Quiz:     poll.Quiz,
		Multiple: poll.MultipleChoice,
		Closed:   poll.Closed,
	}
	for _, a := range poll.Answers {
		if pa, ok := a.(*tg.PollAnswer); ok {
			sp.Answers = append(sp.Answers, storedAnswer{Option: encodePollOption(pa.Option), Text: pa.Text.Text})
		}
	}
	return sp
}

func encodePollOption(option []byte) string {
	return base64.RawURLEncoding.EncodeToString(option)
}

func (tc *TelegramClient) savePoll(ctx context.Context, row *pollRow) error {
	pollJSON, err := json.Marshal(row.Poll)
	if err != nil {
		return err
	}
	var answerMap string
	if len(row.AnswerMap) > 0 {
		data, err := json.Marshal(row.AnswerMap)
		if err != nil {
			return err
		}
		answerMap = string(data)
	}
	_, err = tc.main.Store.Exec(ctx, `
		INSERT INTO fork_poll (login_id, poll_id, portal_id, receiver, msg_id, poll, answer_map, own_vote)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (login_id, poll_id) DO UPDATE SET
			portal_id=excluded.portal_id, receiver=excluded.receiver, msg_id=excluded.msg_id, poll=excluded.poll,
			answer_map=CASE WHEN excluded.answer_map <> '' THEN excluded.answer_map ELSE fork_poll.answer_map END`,
		tc.userLogin.ID, row.PollID, row.PortalKey.ID, row.PortalKey.Receiver, row.MsgID, string(pollJSON), answerMap, row.OwnVote)
	return err
}

func (tc *TelegramClient) setPollOwnVote(ctx context.Context, pollID int64, vote string) error {
	_, err := tc.main.Store.Exec(ctx, `UPDATE fork_poll SET own_vote=$3 WHERE login_id=$1 AND poll_id=$2`,
		tc.userLogin.ID, pollID, vote)
	return err
}

func (tc *TelegramClient) scanPoll(row interface{ Scan(...any) error }) (*pollRow, error) {
	var p pollRow
	var portalID, receiver, pollJSON, answerMap string
	err := row.Scan(&p.PollID, &portalID, &receiver, &p.MsgID, &pollJSON, &answerMap, &p.OwnVote)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p.PortalKey = networkid.PortalKey{ID: networkid.PortalID(portalID), Receiver: networkid.UserLoginID(receiver)}
	if err = json.Unmarshal([]byte(pollJSON), &p.Poll); err != nil {
		return nil, fmt.Errorf("failed to parse stored poll: %w", err)
	}
	if answerMap != "" {
		if err = json.Unmarshal([]byte(answerMap), &p.AnswerMap); err != nil {
			return nil, fmt.Errorf("failed to parse poll answer map: %w", err)
		}
	}
	return &p, nil
}

const pollColumns = `poll_id, portal_id, receiver, msg_id, poll, answer_map, own_vote`

func (tc *TelegramClient) getPollByID(ctx context.Context, pollID int64) (*pollRow, error) {
	return tc.scanPoll(tc.main.Store.QueryRow(ctx,
		`SELECT `+pollColumns+` FROM fork_poll WHERE login_id=$1 AND poll_id=$2`, tc.userLogin.ID, pollID))
}

func (tc *TelegramClient) getPollByMessage(ctx context.Context, portalKey networkid.PortalKey, msgID int) (*pollRow, error) {
	return tc.scanPoll(tc.main.Store.QueryRow(ctx,
		`SELECT `+pollColumns+` FROM fork_poll WHERE login_id=$1 AND portal_id=$2 AND receiver=$3 AND msg_id=$4`,
		tc.userLogin.ID, portalKey.ID, portalKey.Receiver, msgID))
}

// pollState batches result updates per poll.
type pollState struct {
	lock    sync.Mutex
	pending map[int64]*pendingPollResults
}

type pendingPollResults struct {
	results tg.PollResults
}

// --- Telegram → Matrix ---

// pollHashID is the content hash part of a poll: results changes aren't edits of the poll itself.
func pollHashID(poll *tg.MessageMediaPoll) []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(poll.Poll.ID))
}

// convertPollToMatrix converts a Telegram poll into a Matrix poll; pollResultsPart adds the notice
// with its results.
func (tc *TelegramClient) convertPollToMatrix(ctx context.Context, portal *bridgev2.Portal, msg *tg.Message, media *tg.MessageMediaPoll) *bridgev2.ConvertedMessagePart {
	poll := &media.Poll
	maxSelections := 1
	var answers []event.PollOption
	var fallback strings.Builder
	fallback.WriteString(fmt.Sprintf(tc.tl("Poll: %s"), poll.Question.Text))
	for i, a := range poll.Answers {
		pa, ok := a.(*tg.PollAnswer)
		if !ok {
			continue
		}
		answers = append(answers, event.PollOption{
			ID:             encodePollOption(pa.Option),
			MSC1767Message: event.MSC1767Message{Text: pa.Text.Text},
		})
		fmt.Fprintf(&fallback, "\n%d. %s", i+1, pa.Text.Text)
	}
	if poll.MultipleChoice {
		maxSelections = len(answers)
	}
	pollStart := event.PollStart{
		Kind:          "org.matrix.msc3381.poll.undisclosed",
		MaxSelections: maxSelections,
		Question:      event.MSC1767Message{Text: poll.Question.Text},
		Answers:       answers,
	}
	start := &bridgev2.ConvertedMessagePart{
		Type:    event.EventUnstablePollStart,
		Content: &event.MessageEventContent{MsgType: event.MsgText, Body: fallback.String()},
		Extra: map[string]any{
			"org.matrix.msc3381.poll.start": pollStart,
			"org.matrix.msc1767.text":       fallback.String(),
		},
	}
	portalKey := portal.PortalKey
	err := tc.savePoll(ctx, &pollRow{PollID: poll.ID, PortalKey: portalKey, MsgID: msg.ID, Poll: storePoll(poll)})
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Int64("poll_id", poll.ID).Msg("Failed to save poll")
	}
	return start
}

func (tc *TelegramClient) pollResultsPart(media *tg.MessageMediaPoll) *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		ID:         pollResultsPartID,
		Type:       event.EventMessage,
		Content:    tc.pollResultsContent(storePoll(&media.Poll), &media.Results),
		DBMetadata: &MessageMetadata{},
	}
}

// pollResultsContent renders Telegram's results of a poll as a notice.
func (tc *TelegramClient) pollResultsContent(poll storedPoll, results *tg.PollResults) *event.MessageEventContent {
	voters := map[string]tg.PollAnswerVoters{}
	for _, r := range results.Results {
		voters[encodePollOption(r.Option)] = r
	}
	var sb strings.Builder
	if poll.Quiz {
		sb.WriteString(tc.tl("📊 Quiz results in Telegram"))
	} else {
		sb.WriteString(tc.tl("📊 Poll results in Telegram"))
	}
	fmt.Fprintf(&sb, tc.tl(" (votes: %d)"), results.TotalVoters)
	if poll.Closed {
		sb.WriteString(tc.tl(", final"))
	}
	sb.WriteString(":")
	for i, a := range poll.Answers {
		v, known := voters[a.Option]
		fmt.Fprintf(&sb, "\n%d. %s", i+1, a.Text)
		if known && results.TotalVoters > 0 {
			fmt.Fprintf(&sb, " — %d%% (%d)", (v.Voters*100+results.TotalVoters/2)/results.TotalVoters, v.Voters)
		}
		if v.Correct {
			sb.WriteString(" ✅")
		}
		if v.Chosen {
			sb.WriteString(tc.tl(" ← your vote"))
		}
	}
	if len(results.Results) == 0 && results.TotalVoters > 0 {
		sb.WriteString("\n")
		sb.WriteString(tc.tl("Telegram shows the results after you vote."))
	}
	if results.Solution != "" {
		sb.WriteString("\n💡 ")
		sb.WriteString(results.Solution)
	}
	return &event.MessageEventContent{MsgType: event.MsgNotice, Body: sb.String(), Mentions: &event.Mentions{}}
}

// onMessagePoll handles new results of a poll.
func (tc *TelegramClient) onMessagePoll(ctx context.Context, update *tg.UpdateMessagePoll) error {
	var poll *tg.Poll
	if p, ok := update.GetPoll(); ok {
		poll = &p
	}
	tc.handlePollResults(ctx, update.PollID, poll, update.Results)
	return nil
}

// handlePollResults bridges the user's own vote right away and schedules an edit of the results.
func (tc *TelegramClient) handlePollResults(ctx context.Context, pollID int64, poll *tg.Poll, results tg.PollResults) {
	log := zerolog.Ctx(ctx).With().Int64("poll_id", pollID).Logger()
	row, err := tc.getPollByID(ctx, pollID)
	if err != nil {
		log.Err(err).Msg("Failed to get poll")
		return
	} else if row == nil {
		return
	}
	if poll != nil {
		row.Poll = storePoll(poll)
		if err = tc.savePoll(ctx, row); err != nil {
			log.Err(err).Msg("Failed to save poll")
		}
	}
	if !results.Min {
		var chosen []string
		for _, r := range results.Results {
			if r.Chosen {
				chosen = append(chosen, encodePollOption(r.Option))
			}
		}
		slices.Sort(chosen)
		if vote := strings.Join(chosen, ","); vote != row.OwnVote {
			if err = tc.setPollOwnVote(ctx, pollID, vote); err != nil {
				log.Err(err).Msg("Failed to save own poll vote")
			}
			go tc.sendOwnPollVote(context.WithoutCancel(ctx), row, chosen)
		}
	}

	tc.polls.lock.Lock()
	defer tc.polls.lock.Unlock()
	if tc.polls.pending == nil {
		tc.polls.pending = map[int64]*pendingPollResults{}
	}
	if pending, ok := tc.polls.pending[pollID]; ok {
		pending.results = results
		return
	}
	tc.polls.pending[pollID] = &pendingPollResults{results: results}
	time.AfterFunc(pollResultsDelay, func() {
		tc.polls.lock.Lock()
		pending := tc.polls.pending[pollID]
		delete(tc.polls.pending, pollID)
		tc.polls.lock.Unlock()
		if pending != nil {
			tc.editPollResults(log.WithContext(context.Background()), pollID, pending.results)
		}
	})
}

// editPollResults edits the results notice of a poll.
func (tc *TelegramClient) editPollResults(ctx context.Context, pollID int64, results tg.PollResults) {
	log := zerolog.Ctx(ctx)
	row, err := tc.getPollByID(ctx, pollID)
	if err != nil || row == nil {
		return
	}
	messageID := ids.MakeMessageID(row.PortalKey, row.MsgID)
	part, err := tc.main.Bridge.DB.Message.GetPartByID(ctx, row.PortalKey.Receiver, messageID, pollResultsPartID)
	if err != nil {
		log.Err(err).Msg("Failed to get poll results message")
		return
	} else if part == nil {
		// Polls bridged before results were bridged, or created in Matrix before the notice was sent.
		return
	}
	content := tc.pollResultsContent(row.Poll, &results)
	sender := bridgev2.EventSender{Sender: part.SenderID}
	if part.SenderID == tc.userID {
		sender.IsFromMe = true
		sender.SenderLogin = tc.userLogin.ID
	}
	tc.queueRemoteEvent(&simplevent.Message[any]{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventEdit,
			PortalKey: row.PortalKey,
			Sender:    sender,
			Timestamp: time.Now(),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("action", "edit_poll_results").Int64("poll_id", pollID)
			},
		},
		ID:            messageID,
		TargetMessage: messageID,
		ConvertEditFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, existing []*database.Message, data any) (*bridgev2.ConvertedEdit, error) {
			for _, p := range existing {
				if p.PartID != pollResultsPartID {
					continue
				}
				return &bridgev2.ConvertedEdit{ModifiedParts: []*bridgev2.ConvertedEditPart{{
					Part:          p,
					Type:          event.EventMessage,
					Content:       content,
					TopLevelExtra: map[string]any{"com.beeper.dont_render_edited": true},
				}}}, nil
			}
			return nil, fmt.Errorf("%w (no poll results part)", bridgev2.ErrIgnoringRemoteEvent)
		},
	})
}

// sendOwnPollVote puts the user's vote from Telegram on the Matrix poll.
func (tc *TelegramClient) sendOwnPollVote(ctx context.Context, row *pollRow, options []string) {
	log := zerolog.Ctx(ctx).With().Int64("poll_id", row.PollID).Logger()
	dp := tc.userLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return
	}
	portal, err := tc.main.Bridge.GetExistingPortalByKey(ctx, row.PortalKey)
	if err != nil || portal == nil || portal.MXID == "" {
		return
	}
	start, err := tc.main.Bridge.DB.Message.GetFirstPartByID(ctx, row.PortalKey.Receiver, ids.MakeMessageID(row.PortalKey, row.MsgID))
	if err != nil || start == nil {
		return
	}
	answers := make([]string, 0, len(options))
	for _, option := range options {
		answers = append(answers, row.answerIDForOption(option))
	}
	content := &event.PollResponseEventContent{
		RelatesTo: event.RelatesTo{Type: event.RelReference, EventID: start.MXID},
		Response:  event.PollResponse{Answers: answers},
	}
	_, err = dp.SendMessage(ctx, portal.MXID, event.EventUnstablePollResponse, &event.Content{Parsed: content}, &bridgev2.MatrixSendExtra{Timestamp: time.Now()})
	if err != nil {
		log.Err(err).Msg("Failed to send own poll vote to Matrix")
	} else {
		log.Debug().Strs("answers", answers).Msg("Bridged own poll vote to Matrix")
	}
}

func (p *pollRow) answerIDForOption(option string) string {
	for answerID, o := range p.AnswerMap {
		if o == option {
			return answerID
		}
	}
	return option
}

func (p *pollRow) optionForAnswerID(answerID string) ([]byte, error) {
	if o, ok := p.AnswerMap[answerID]; ok {
		answerID = o
	}
	return base64.RawURLEncoding.DecodeString(answerID)
}

// --- Matrix → Telegram ---

var _ bridgev2.PollHandlingNetworkAPI = (*TelegramClient)(nil)

// HandleMatrixPollVote votes in the Telegram poll.
func (tc *TelegramClient) HandleMatrixPollVote(ctx context.Context, msg *bridgev2.MatrixPollVote) (*bridgev2.MatrixMessageResponse, error) {
	if err := tc.clientInitialized.Wait(ctx); err != nil {
		return nil, err
	}
	_, msgID, err := ids.ParseMessageID(msg.VoteTo.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse poll message ID: %w", err)
	}
	row, err := tc.getPollByMessage(ctx, msg.Portal.PortalKey, msgID)
	if err != nil {
		return nil, err
	} else if row == nil {
		return nil, bridgev2.ErrUnknownPoll
	}
	var options [][]byte
	var chosen []string
	for _, answerID := range msg.Content.Response.Answers {
		option, err := row.optionForAnswerID(answerID)
		if err != nil {
			return nil, fmt.Errorf("unknown poll answer %q", answerID)
		}
		options = append(options, option)
		chosen = append(chosen, encodePollOption(option))
	}
	slices.Sort(chosen)
	vote := strings.Join(chosen, ",")
	resp := &bridgev2.MatrixMessageResponse{DB: &database.Message{
		ID:        msg.VoteTo.ID,
		PartID:    networkid.PartID(pollVotePartPrefix + string(msg.Event.ID)),
		SenderID:  tc.userID,
		Timestamp: time.UnixMilli(msg.Event.Timestamp),
	}}
	if vote == row.OwnVote {
		// The vote came from Telegram (or didn't change).
		return resp, nil
	}
	peer, _, err := tc.inputPeerForPortalID(ctx, msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	if err = tc.setPollOwnVote(ctx, row.PollID, vote); err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to save own poll vote")
	}
	updates, err := tc.client.API().MessagesSendVote(ctx, &tg.MessagesSendVoteRequest{Peer: peer, MsgID: msgID, Options: options})
	if err != nil {
		_ = tc.setPollOwnVote(ctx, row.PollID, row.OwnVote)
		return nil, tc.humaniseSendError(err)
	}
	tc.handlePollUpdates(ctx, updates)
	return resp, nil
}

// handlePollUpdates handles the poll results in the response of a request.
func (tc *TelegramClient) handlePollUpdates(ctx context.Context, updates tg.UpdatesClass) {
	var list []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	case *tg.UpdateShort:
		list = []tg.UpdateClass{u.Update}
	}
	for _, update := range list {
		if mp, ok := update.(*tg.UpdateMessagePoll); ok {
			_ = tc.onMessagePoll(ctx, mp)
		}
	}
}

// HandleMatrixPollStart creates the poll in Telegram.
func (tc *TelegramClient) HandleMatrixPollStart(ctx context.Context, msg *bridgev2.MatrixPollStart) (*bridgev2.MatrixMessageResponse, error) {
	if err := tc.clientInitialized.Wait(ctx); err != nil {
		return nil, err
	}
	content := msg.Content.PollStart
	if len(content.Answers) < 2 {
		return nil, fmt.Errorf("a poll needs at least two answers")
	}
	peer, topicID, err := tc.inputPeerForPortalID(ctx, msg.Portal.ID)
	if err != nil {
		return nil, err
	}
	var randomPollID [8]byte
	_, _ = rand.Read(randomPollID[:])
	poll := tg.Poll{
		ID:             int64(binary.BigEndian.Uint64(randomPollID[:])),
		MultipleChoice: content.MaxSelections > 1,
		Question:       tg.TextWithEntities{Text: content.Question.GetText()},
	}
	for _, a := range content.Answers {
		poll.Answers = append(poll.Answers, &tg.InputPollAnswer{Text: tg.TextWithEntities{Text: a.GetText()}})
	}
	req := &tg.MessagesSendMediaRequest{
		Peer:     peer,
		Media:    &tg.InputMediaPoll{Poll: poll},
		RandomID: parseRandomID(msg.InputTransactionID),
	}
	if msg.ReplyTo != nil {
		if _, replyID, err := ids.ParseMessageID(msg.ReplyTo.ID); err == nil {
			req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyID}
		}
	}
	if topicID > 0 {
		if req.ReplyTo == nil {
			req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: topicID}
		} else {
			req.ReplyTo.(*tg.InputReplyToMessage).TopMsgID = topicID
		}
	}
	updates, err := tc.client.API().MessagesSendMedia(ctx, req)
	if err != nil {
		return nil, tc.humaniseSendError(err)
	}
	sent, sentID, date := sentMessageFromUpdates(updates)
	if sentID == 0 {
		return nil, fmt.Errorf("couldn't find the sent poll in the response")
	}
	row := &pollRow{PortalKey: msg.Portal.PortalKey, MsgID: sentID, AnswerMap: map[string]string{}}
	var sentPoll *tg.MessageMediaPoll
	if sent != nil {
		sentPoll, _ = sent.Media.(*tg.MessageMediaPoll)
	}
	if sentPoll != nil {
		row.PollID = sentPoll.Poll.ID
		row.Poll = storePoll(&sentPoll.Poll)
		for i, a := range content.Answers {
			if i < len(row.Poll.Answers) {
				row.AnswerMap[a.ID] = row.Poll.Answers[i].Option
			}
		}
		if err = tc.savePoll(ctx, row); err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to save sent poll")
		}
	} else {
		zerolog.Ctx(ctx).Warn().Msg("Sent poll not in the response, votes from Matrix won't work for it")
	}
	if date == 0 {
		date = int(time.Now().Unix())
	}
	messageID := ids.MakeMessageID(msg.Portal.PortalKey, sentID)
	resp := &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        messageID,
			SenderID:  tc.userID,
			Timestamp: time.Unix(int64(date), 0),
			Metadata:  &MessageMetadata{},
		},
		StreamOrder: int64(sentID),
	}
	if sentPoll != nil {
		results := sentPoll.Results
		resp.PostSave = func(ctx context.Context, message *database.Message) {
			go tc.sendOwnPollResults(context.WithoutCancel(ctx), msg.Portal, message, row, &results)
		}
	}
	return resp, nil
}

// sendOwnPollResults adds the results notice to a poll created in Matrix, sent as the user.
func (tc *TelegramClient) sendOwnPollResults(ctx context.Context, portal *bridgev2.Portal, start *database.Message, row *pollRow, results *tg.PollResults) {
	log := zerolog.Ctx(ctx).With().Int64("poll_id", row.PollID).Logger()
	dp := tc.userLogin.User.DoublePuppet(ctx)
	if dp == nil {
		return
	}
	content := tc.pollResultsContent(row.Poll, results)
	content.RelatesTo = (&event.RelatesTo{}).SetReplyTo(start.MXID)
	resp, err := dp.SendMessage(ctx, portal.MXID, event.EventMessage, &event.Content{Parsed: content}, &bridgev2.MatrixSendExtra{Timestamp: time.Now()})
	if err != nil {
		log.Err(err).Msg("Failed to send results of own poll")
		return
	}
	err = tc.main.Bridge.DB.Message.Insert(ctx, &database.Message{
		ID:         start.ID,
		PartID:     pollResultsPartID,
		MXID:       resp.EventID,
		Room:       portal.PortalKey,
		SenderID:   tc.userID,
		SenderMXID: tc.userLogin.UserMXID,
		Timestamp:  time.Now(),
		Metadata:   &MessageMetadata{},
	})
	if err != nil {
		log.Err(err).Msg("Failed to save results of own poll")
	}
}

// sentMessageFromUpdates finds the sent message in the response of a send request.
func sentMessageFromUpdates(updates tg.UpdatesClass) (sent *tg.Message, id, date int) {
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		return nil, u.ID, u.Date
	case *tg.Updates:
		for _, upd := range u.Updates {
			switch typed := upd.(type) {
			case *tg.UpdateMessageID:
				id = typed.ID
			case *tg.UpdateNewMessage:
				sent, _ = typed.Message.(*tg.Message)
			case *tg.UpdateNewChannelMessage:
				sent, _ = typed.Message.(*tg.Message)
			}
		}
		if sent != nil {
			if id == 0 {
				id = sent.ID
			}
			date = sent.Date
		} else {
			date = u.Date
		}
	}
	return sent, id, date
}

// Helpers from upstream's poll support, which this fork replaces with its own.

func telegramToMatrixAnswerID(meta *PollMetadata, option []byte) string {
	answerID := base64.RawURLEncoding.EncodeToString(option)
	if meta != nil {
		if mxID, ok := meta.MatrixAnswerIDs[answerID]; ok {
			return mxID
		}
	}
	return answerID
}

func isBroadcastPortal(portal *bridgev2.Portal) bool {
	peerType, _, _, err := ids.ParsePortalID(portal.ID)
	return err == nil && peerType == ids.PeerTypeChannel && !portal.Metadata.(*PortalMetadata).IsSuperGroup
}

func (tc *TelegramClient) matrixToTelegramText(ctx context.Context, portal *bridgev2.Portal, text *event.MSC1767Message) tg.TextWithEntities {
	content := &event.MessageEventContent{MsgType: event.MsgText, Body: text.GetText()}
	if html := text.GetHTML(); html != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = html
	}
	message, entities := matrixfmt.Parse(ctx, tc.matrixParser, content, portal, tc.getMaxMessageLength(ctx, false))
	return tg.TextWithEntities{Text: message, Entities: entities}
}

func (tc *TelegramClient) isBotUser(ctx context.Context, userID int64) bool {
	ghost, err := tc.main.Bridge.GetGhostByID(ctx, ids.MakeUserID(userID))
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Int64("user_id", userID).Msg("Failed to get ghost to check if user is a bot")
		return false
	}
	return ghost.IsBot
}
