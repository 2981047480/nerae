-- Stop all 0006 personal-exercise writers before this migration and keep them
-- stopped until every writer stores version snapshots. A mixed-version writer
-- could otherwise update the current row without recording its history.
-- Preserve the authenticated name/tag snapshot for each personal action
-- version. Existing 0006 rows can supply only their current version; earlier
-- values cannot be reconstructed and must never be invented.
CREATE TABLE workout_personal_exercise_snapshot (
    user_id BIGINT UNSIGNED NOT NULL,
    exercise_id BINARY(16) NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    name_digest BINARY(32) NOT NULL,
    snapshot_enc MEDIUMBLOB NOT NULL,
    PRIMARY KEY (user_id, exercise_id, version),
    CONSTRAINT personal_snapshot_exercise_fk FOREIGN KEY (user_id, exercise_id)
        REFERENCES workout_personal_exercise (user_id, exercise_id),
    CONSTRAINT personal_snapshot_version_valid CHECK (version BETWEEN 1 AND 9007199254740991),
    CONSTRAINT personal_snapshot_key_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT personal_snapshot_cipher_size CHECK (OCTET_LENGTH(snapshot_enc) BETWEEN 29 AND 16384)
) ENGINE=InnoDB;

INSERT INTO workout_personal_exercise_snapshot
    (user_id, exercise_id, version, key_id, name_digest, snapshot_enc)
SELECT user_id, exercise_id, version, key_id, name_digest, snapshot_enc
FROM workout_personal_exercise;
