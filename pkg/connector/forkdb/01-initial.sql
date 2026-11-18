-- v1: Tables of the fork
-- Before the fork had its own migrations the tables were created with IF NOT EXISTS on every start,
-- so they may already exist.

DROP TABLE IF EXISTS fork_poll;

-- A poll can be in several messages: a channel post and its copy in the discussion group share it.
CREATE TABLE IF NOT EXISTS fork_poll_message (
    login_id   TEXT   NOT NULL,
    portal_id  TEXT   NOT NULL,
    receiver   TEXT   NOT NULL,
    msg_id     BIGINT NOT NULL,
    poll_id    BIGINT NOT NULL,
    poll       TEXT   NOT NULL,
    answer_map TEXT   NOT NULL DEFAULT '',
    own_vote   TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (login_id, portal_id, receiver, msg_id)
);
CREATE INDEX IF NOT EXISTS fork_poll_message_poll_idx ON fork_poll_message (login_id, poll_id);

CREATE TABLE IF NOT EXISTS fork_comment_thread (
    channel_id BIGINT NOT NULL,
    post_id    BIGINT NOT NULL,
    group_id   BIGINT NOT NULL,
    copy_id    BIGINT NOT NULL,
    PRIMARY KEY (channel_id, post_id)
);
CREATE INDEX IF NOT EXISTS fork_comment_thread_copy_idx ON fork_comment_thread (group_id, copy_id);

CREATE TABLE IF NOT EXISTS fork_comment_mirror (
    group_id BIGINT NOT NULL,
    msg_id   BIGINT NOT NULL,
    copy_id  BIGINT NOT NULL,
    room_id  TEXT   NOT NULL,
    event_id TEXT   NOT NULL,
    PRIMARY KEY (group_id, msg_id)
);
CREATE INDEX IF NOT EXISTS fork_comment_mirror_event_idx ON fork_comment_mirror (event_id);
CREATE INDEX IF NOT EXISTS fork_comment_mirror_thread_idx ON fork_comment_mirror (group_id, copy_id);

CREATE TABLE IF NOT EXISTS fork_comment_participation (
    group_id BIGINT NOT NULL,
    copy_id  BIGINT NOT NULL,
    PRIMARY KEY (group_id, copy_id)
);

CREATE TABLE IF NOT EXISTS fork_comment_edit (
    group_id  BIGINT NOT NULL,
    msg_id    BIGINT NOT NULL,
    edit_date BIGINT NOT NULL,
    PRIMARY KEY (group_id, msg_id)
);
