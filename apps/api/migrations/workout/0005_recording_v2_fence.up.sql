-- A short-lived response receipt must not allow an already committed v2 key
-- to execute again after expiry. The digest contains no raw key or body.
CREATE TABLE workout_recording_v2_fence (
    user_id BIGINT UNSIGNED NOT NULL,
    key_hash BINARY(32) NOT NULL,
    PRIMARY KEY (user_id, key_hash),
    CONSTRAINT recording_v2_fence_user_fk FOREIGN KEY (user_id) REFERENCES user (id)
) ENGINE=InnoDB;
