CREATE TABLE auth_device_proof_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_device_proof_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

CREATE TABLE auth_device_proof_replay (
    device_fingerprint BINARY(32) NOT NULL,
    jti_digest BINARY(32) NOT NULL,
    retain_until DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (device_fingerprint, jti_digest),
    KEY auth_device_proof_replay_retention (retain_until)
) ENGINE=InnoDB;
