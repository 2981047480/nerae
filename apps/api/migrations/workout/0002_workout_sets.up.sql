-- An uninitialized singleton prevents writes until deployment provisions a key.
CREATE TABLE workout_crypto_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NULL,
    key_tag BINARY(32) NULL,
    CONSTRAINT workout_crypto_singleton CHECK (slot = 1),
    CONSTRAINT workout_crypto_initialized CHECK ((key_id IS NULL AND key_tag IS NULL) OR (key_id IS NOT NULL AND key_tag IS NOT NULL))
) ENGINE=InnoDB;
INSERT INTO workout_crypto_key_epoch (slot, key_id, key_tag) VALUES (1, NULL, NULL);

-- Only custom entries are admitted until a versioned, authorized exercise
-- catalog exists. Entry keys and group keys compare as exact UTF-8 bytes.
CREATE TABLE workout_exercise (
    session_id BIGINT UNSIGNED NOT NULL,
    exercise_entry_key VARBINARY(256) NOT NULL,
    source VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    name_snapshot_enc VARBINARY(300) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (session_id, exercise_entry_key),
    CONSTRAINT workout_exercise_session_fk FOREIGN KEY (session_id) REFERENCES workout_session (id),
    CONSTRAINT workout_exercise_custom_only CHECK (source = 'custom'),
    CONSTRAINT workout_exercise_key_size CHECK (OCTET_LENGTH(exercise_entry_key) BETWEEN 8 AND 256)
) ENGINE=InnoDB;

CREATE TABLE set_record_key (
    session_id BIGINT UNSIGNED NOT NULL,
    record_key VARBINARY(256) NOT NULL,
    exercise_entry_key VARBINARY(256) NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (session_id, record_key),
    CONSTRAINT set_key_exercise_fk FOREIGN KEY (session_id, exercise_entry_key) REFERENCES workout_exercise (session_id, exercise_entry_key),
    CONSTRAINT set_key_state_valid CHECK ((state = 'active' AND deleted_at IS NULL) OR (state = 'deleted' AND deleted_at IS NOT NULL)),
    CONSTRAINT set_record_key_size CHECK (OCTET_LENGTH(record_key) BETWEEN 8 AND 256)
) ENGINE=InnoDB;

CREATE TABLE set_record (
    session_id BIGINT UNSIGNED NOT NULL,
    record_key VARBINARY(256) NOT NULL,
    set_index BIGINT UNSIGNED NOT NULL,
    kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    weight_kg DECIMAL(8,2) NULL,
    reps SMALLINT UNSIGNED NULL,
    seconds SMALLINT UNSIGNED NULL,
    rpe DECIMAL(3,1) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (session_id, record_key),
    CONSTRAINT set_record_key_fk FOREIGN KEY (session_id, record_key) REFERENCES set_record_key (session_id, record_key),
    CONSTRAINT set_index_positive CHECK (set_index > 0),
    CONSTRAINT set_kind_valid CHECK (kind IN ('weight', 'perSide', 'bodyweight', 'bodyweightPerSide', 'duration', 'durationPerSide', 'breath')),
    CONSTRAINT set_rpe_valid CHECK (rpe IS NULL OR (rpe >= 1 AND rpe <= 10)),
    CONSTRAINT set_measurements_valid CHECK (
        (kind IN ('weight', 'perSide') AND weight_kg IS NOT NULL AND weight_kg > 0 AND weight_kg <= 300 AND reps IS NOT NULL AND reps BETWEEN 1 AND 100 AND seconds IS NULL)
        OR (kind IN ('bodyweight', 'bodyweightPerSide', 'breath') AND weight_kg IS NULL AND reps IS NOT NULL AND reps BETWEEN 1 AND 100 AND seconds IS NULL)
        OR (kind IN ('duration', 'durationPerSide') AND weight_kg IS NULL AND reps IS NULL AND seconds IS NOT NULL AND seconds BETWEEN 5 AND 600)
    )
) ENGINE=InnoDB;
