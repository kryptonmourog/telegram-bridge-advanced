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

	up "go.mau.fi/util/configupgrade"
	"gopkg.in/yaml.v3"
)

// The fork's options survive the config upgrade that runs on every start.
func TestConfigUpgradeKeepsForkOptions(t *testing.T) {
	var base, cfg yaml.Node
	if err := yaml.Unmarshal([]byte(ExampleConfig), &base); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte("max_initial_messages_override:\n    user: 300\n    saved: 1000\npersonal_channels: true\nencrypted_direct_media: large\ncomment_poll:\n    background_minutes: 5\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	upgradeConfig(up.NewHelper(&base, &cfg))
	out, err := yaml.Marshal(&base)
	if err != nil {
		t.Fatal(err)
	}
	var parsed TelegramConfig
	if err = yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("upgraded config doesn't parse: %v\n%s", err, out)
	}
	if parsed.MaxInitialMessagesOverride["user"] != 300 || parsed.MaxInitialMessagesOverride["saved"] != 1000 || !parsed.PersonalChannels ||
		parsed.EncryptedDirectMedia != EncryptedDirectMediaLarge || parsed.CommentPoll.BackgroundMinutes != 5 || parsed.CommentPoll.OnReadSeconds != 60 {
		t.Fatalf("options lost: %+v %v", parsed.MaxInitialMessagesOverride, parsed.PersonalChannels)
	}

	// An unchanged config keeps the empty default.
	base, cfg = yaml.Node{}, yaml.Node{}
	_ = yaml.Unmarshal([]byte(ExampleConfig), &base)
	_ = yaml.Unmarshal([]byte("personal_channels: false\n"), &cfg)
	upgradeConfig(up.NewHelper(&base, &cfg))
	out, _ = yaml.Marshal(&base)
	if err = yaml.Unmarshal(out, &TelegramConfig{}); err != nil {
		t.Fatalf("upgraded default config doesn't parse: %v", err)
	}
}
