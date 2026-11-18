-- v2: Comments per channel room, comment participation per login, channel kinds
-- With personal channel rooms a comment is mirrored into the room of every user subscribed to the
-- channel, so mirrors and applied edits are kept per room.

CREATE TABLE fork_comment_mirror_new (
    group_id BIGINT NOT NULL,
    msg_id   BIGINT NOT NULL,
    room_id  TEXT   NOT NULL,
    copy_id  BIGINT NOT NULL,
    event_id TEXT   NOT NULL,
    PRIMARY KEY (group_id, msg_id, room_id)
);
INSERT INTO fork_comment_mirror_new (group_id, msg_id, room_id, copy_id, event_id)
SELECT group_id, msg_id, room_id, copy_id, event_id FROM fork_comment_mirror;
DROP TABLE fork_comment_mirror;
ALTER TABLE fork_comment_mirror_new RENAME TO fork_comment_mirror;
CREATE INDEX fork_comment_mirror_event_idx ON fork_comment_mirror (event_id);
CREATE INDEX fork_comment_mirror_thread_idx ON fork_comment_mirror (room_id, group_id, copy_id);

CREATE TABLE fork_comment_edit_new (
    group_id  BIGINT NOT NULL,
    msg_id    BIGINT NOT NULL,
    room_id   TEXT   NOT NULL,
    edit_date BIGINT NOT NULL,
    PRIMARY KEY (group_id, msg_id, room_id)
);
INSERT INTO fork_comment_edit_new (group_id, msg_id, room_id, edit_date)
SELECT e.group_id, e.msg_id, m.room_id, e.edit_date
FROM fork_comment_edit e JOIN fork_comment_mirror m ON m.group_id = e.group_id AND m.msg_id = e.msg_id;
DROP TABLE fork_comment_edit;
ALTER TABLE fork_comment_edit_new RENAME TO fork_comment_edit;

-- Participation wasn't recorded per user, it's kept for every login.
CREATE TABLE fork_comment_participation_new (
    login_id TEXT   NOT NULL,
    group_id BIGINT NOT NULL,
    copy_id  BIGINT NOT NULL,
    PRIMARY KEY (login_id, group_id, copy_id)
);
INSERT INTO fork_comment_participation_new (login_id, group_id, copy_id)
SELECT DISTINCT l.id, p.group_id, p.copy_id FROM fork_comment_participation p, user_login l;
DROP TABLE fork_comment_participation;
ALTER TABLE fork_comment_participation_new RENAME TO fork_comment_participation;

-- Whether a Telegram channel ID is a broadcast channel or a supergroup, to pick its portal key
-- before anything else is known about the chat.
CREATE TABLE fork_channel_kind (
    channel_id BIGINT  PRIMARY KEY,
    broadcast  BOOLEAN NOT NULL
);
