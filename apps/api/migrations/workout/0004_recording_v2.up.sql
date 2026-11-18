-- Additive only. Existing sessions remain format 1; format 2 may be used only
-- once every legacy read, write and receipt path rejects incompatible records.
ALTER TABLE workout_session
    ADD COLUMN record_format TINYINT UNSIGNED NOT NULL DEFAULT 1,
    ADD CONSTRAINT workout_record_format_valid CHECK (record_format IN (1, 2));

-- Sensitive measurements and deleted snapshots are authenticated ciphertext.
-- The session row and snapshot are changed in one application transaction.
CREATE TABLE workout_recording_v2 (
    session_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    version BIGINT UNSIGNED NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    snapshot_enc MEDIUMBLOB NOT NULL,
    CONSTRAINT recording_v2_session_fk FOREIGN KEY (session_id) REFERENCES workout_session (id),
    CONSTRAINT recording_v2_version_positive CHECK (version >= 1),
    CONSTRAINT recording_v2_key_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT recording_v2_cipher_size CHECK (OCTET_LENGTH(snapshot_enc) BETWEEN 29 AND 4194332)
) ENGINE=InnoDB;
