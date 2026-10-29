-- The first increment stores free-session identity only. Plan references,
-- actual sets and encrypted notes are added with their own transactions.
CREATE TABLE workout_session (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    source VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    date DATE NOT NULL,
    time_zone VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    -- Binary comparison preserves case and trailing bytes for up to 64 UTF-8 characters.
    client_event_id VARBINARY(256) NOT NULL,
    creation_request_hash BINARY(32) NOT NULL,
    started_at DATETIME(6) NULL,
    recorded_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY workout_user_event (user_id, client_event_id),
    CONSTRAINT workout_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT workout_source_free_only CHECK (source = 'free'),
    CONSTRAINT workout_status_valid CHECK (status IN ('in_progress', 'completed', 'skipped')),
    CONSTRAINT workout_version_positive CHECK (version >= 1),
    CONSTRAINT workout_event_size CHECK (OCTET_LENGTH(client_event_id) BETWEEN 8 AND 256)
) ENGINE=InnoDB;
