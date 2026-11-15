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
	"regexp"
	"slices"
	"testing"
)

var formatVerb = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]|\$cmdprefix`)

// TestTranslationsKeepFormatVerbs checks that every translation has the same formatting verbs, in the
// same order, as its English text, since the arguments are applied to the translation.
func TestTranslationsKeepFormatVerbs(t *testing.T) {
	for lang, catalog := range translations {
		for en, tr := range catalog {
			want := formatVerb.FindAllString(en, -1)
			got := formatVerb.FindAllString(tr, -1)
			if !slices.Equal(want, got) {
				t.Errorf("%s: %q has verbs %v, translation %q has %v", lang, en, want, tr, got)
			}
		}
	}
}
