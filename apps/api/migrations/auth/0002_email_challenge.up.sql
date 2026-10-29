CREATE TABLE auth_email_login_slot (
    email_index BINARY(32) NOT NULL PRIMARY KEY,
    challenge_id VARBINARY(128) NOT NULL
) ENGINE=InnoDB;

CREATE TABLE auth_email_login_challenge (
    challenge_id VARBINARY(128) NOT NULL PRIMARY KEY,
    email_index BINARY(32) NOT NULL,
    nonce VARBINARY(256) NOT NULL,
    public_x CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    public_y CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    key_fingerprint BINARY(32) NOT NULL,
    code_digest BINARY(32) NOT NULL,
    attempts SMALLINT UNSIGNED NOT NULL,
    max_attempts SMALLINT UNSIGNED NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    consumed_at DATETIME(6) NULL,
    KEY auth_email_challenge_expiry (expires_at),
    CONSTRAINT auth_email_attempt_limit CHECK (max_attempts BETWEEN 1 AND 10 AND attempts <= max_attempts)
) ENGINE=InnoDB;
