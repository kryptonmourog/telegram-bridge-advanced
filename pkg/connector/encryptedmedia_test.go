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
	"crypto/rand"
	"encoding/json"
	"io"
	"testing"

	"maunium.net/go/mautrix/crypto/attachment"
)

func encryptAll(t *testing.T, file *attachment.EncryptedFile, data []byte) []byte {
	t.Helper()
	stream := file.EncryptStream(bytes.NewReader(data))
	out, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A file served on request is encrypted again with the stored key and must give the same bytes as when
// it was bridged, or clients reject it for its hash.
func TestEncryptedDirectMediaIsDeterministic(t *testing.T) {
	data := make([]byte, 3*1024*1024+17)
	_, _ = rand.Read(data)

	file := attachment.NewEncryptedFile()
	first := encryptAll(t, file, data)
	bridgedHash := file.Hashes.SHA256

	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	var stored attachment.EncryptedFile
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	second := encryptAll(t, &stored, data)
	if !bytes.Equal(first, second) || stored.Hashes.SHA256 != bridgedHash {
		t.Fatal("encrypting the file again gave different bytes")
	}

	// What a client does with the key and hash from the event.
	var client attachment.EncryptedFile
	if err = json.Unmarshal(raw, &client); err != nil {
		t.Fatal(err)
	}
	decrypted, err := client.Decrypt(second)
	if err != nil || !bytes.Equal(decrypted, data) {
		t.Fatalf("client can't decrypt the served file: %v", err)
	}
}
