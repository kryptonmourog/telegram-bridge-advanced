-- v3: Index for the newest comment of each thread
-- Finding the newest mirrored comment of a thread scanned the whole thread for every row.

DROP INDEX fork_comment_mirror_thread_idx;
CREATE INDEX fork_comment_mirror_thread_idx ON fork_comment_mirror (room_id, group_id, copy_id, msg_id);
