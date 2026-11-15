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
	"bytes"
	"testing"

	"go.mau.fi/mautrix-telegram/pkg/gotd/tg"
)

func TestPollAnswerMapping(t *testing.T) {
	// A poll from Telegram: answer IDs are the encoded options.
	fromTG := &pollRow{}
	id := encodePollOption([]byte{0})
	if got, err := fromTG.optionForAnswerID(id); err != nil || !bytes.Equal(got, []byte{0}) {
		t.Errorf("optionForAnswerID(%q) = %v, %v", id, got, err)
	}
	if got := fromTG.answerIDForOption(id); got != id {
		t.Errorf("answerIDForOption(%q) = %q", id, got)
	}
	// A poll created in Matrix keeps the client's answer IDs.
	fromMatrix := &pollRow{AnswerMap: map[string]string{"ans-a": encodePollOption([]byte("0")), "ans-b": encodePollOption([]byte("1"))}}
	if got, err := fromMatrix.optionForAnswerID("ans-b"); err != nil || string(got) != "1" {
		t.Errorf("optionForAnswerID(ans-b) = %q, %v", got, err)
	}
	if got := fromMatrix.answerIDForOption(encodePollOption([]byte("0"))); got != "ans-a" {
		t.Errorf("answerIDForOption(0) = %q", got)
	}
}

func TestStorePoll(t *testing.T) {
	poll := &tg.Poll{
		Quiz:     true,
		Question: tg.TextWithEntities{Text: "Q?"},
		Answers: []tg.PollAnswerClass{
			&tg.PollAnswer{Text: tg.TextWithEntities{Text: "A"}, Option: []byte("0")},
			&tg.PollAnswer{Text: tg.TextWithEntities{Text: "B"}, Option: []byte("1")},
		},
	}
	sp := storePoll(poll)
	if sp.Question != "Q?" || !sp.Quiz || len(sp.Answers) != 2 || sp.Answers[1].Text != "B" || sp.Answers[1].Option != encodePollOption([]byte("1")) {
		t.Errorf("storePoll = %+v", sp)
	}
}

func TestSentMessageFromUpdates(t *testing.T) {
	msg := &tg.Message{ID: 42, Date: 1000}
	sent, id, date := sentMessageFromUpdates(&tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateMessageID{ID: 42, RandomID: 1},
		&tg.UpdateNewChannelMessage{Message: msg},
	}})
	if sent != msg || id != 42 || date != 1000 {
		t.Errorf("sentMessageFromUpdates = %v, %d, %d", sent, id, date)
	}
	if _, id, _ = sentMessageFromUpdates(&tg.UpdateShortSentMessage{ID: 7, Date: 5}); id != 7 {
		t.Errorf("short sent message id = %d", id)
	}
}
