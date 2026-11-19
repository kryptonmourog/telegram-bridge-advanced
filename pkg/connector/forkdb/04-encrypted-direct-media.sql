-- v4: Keys of media served encrypted
-- Media in encrypted rooms can be served on request like in channels. The event carries the key and
-- the hash of the encrypted file, so they are fixed when the event is sent and kept for serving the file.

CREATE TABLE fork_encrypted_media (
    media_id TEXT PRIMARY KEY,
    file     TEXT NOT NULL
);
