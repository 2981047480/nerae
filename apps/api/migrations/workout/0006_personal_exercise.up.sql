-- This independent key epoch prevents accidental reinitialization over an
-- existing library whose original encryption key has been lost.
CREATE TABLE workout_personal_exercise_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NULL,
    key_tag BINARY(32) NULL,
    CONSTRAINT personal_exercise_key_singleton CHECK (slot = 1),
    CONSTRAINT personal_exercise_key_initialized CHECK ((key_id IS NULL AND key_tag IS NULL) OR (key_id IS NOT NULL AND key_tag IS NOT NULL))
) ENGINE=InnoDB;
INSERT INTO workout_personal_exercise_key_epoch (slot, key_id, key_tag) VALUES (1, NULL, NULL);

-- Account-owned actions have stable random identities. Names and optional
-- tags are authenticated ciphertext; name equality uses a per-owner keyed
-- digest. Library data does not grant access to another account's records.
CREATE TABLE workout_personal_exercise (
    user_id BIGINT UNSIGNED NOT NULL,
    exercise_id BINARY(16) NOT NULL,
    version BIGINT UNSIGNED NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    name_digest BINARY(32) NOT NULL,
    snapshot_enc MEDIUMBLOB NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id, exercise_id),
    UNIQUE KEY personal_exercise_owner_name (user_id, name_digest),
    CONSTRAINT personal_exercise_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT personal_exercise_version_valid CHECK (version BETWEEN 1 AND 9007199254740991),
    CONSTRAINT personal_exercise_key_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT personal_exercise_cipher_size CHECK (OCTET_LENGTH(snapshot_enc) BETWEEN 29 AND 16384)
) ENGINE=InnoDB;
