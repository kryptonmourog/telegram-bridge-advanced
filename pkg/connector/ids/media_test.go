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

package ids

import "testing"

func TestDirectMediaInfoRoundTrip(t *testing.T) {
	for _, info := range []DirectMediaInfo{
		{PeerType: PeerTypeChannel, PeerID: 1, UserID: 2, MessageID: 3, ID: 4},
		{PeerType: PeerTypeUser, PeerID: 5, UserID: 6, MessageID: 7, ID: 8, Thumbnail: true, Encrypted: true},
	} {
		mediaID, err := info.AsMediaID()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseDirectMediaInfo(mediaID)
		if err != nil || parsed != info {
			t.Fatalf("%+v: got %+v, %v", info, parsed, err)
		}
	}
	// Media IDs from before the encrypted version still parse as not encrypted.
	plain, _ := DirectMediaInfo{PeerType: PeerTypeUser, PeerID: 1, ID: 2}.AsMediaID()
	if plain[0] != 0 {
		t.Fatalf("plain media ID has version %d", plain[0])
	}
}
