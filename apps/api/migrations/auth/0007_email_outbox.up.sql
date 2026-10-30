-- This epoch covers the independent secret used to derive email codes.
-- Rotation requires a separately reviewed migration and queue policy.
CREATE TABLE auth_email_code_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_email_code_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

-- Only a challenge reference is stored here; email and codes are not copied
-- into the queue. The challenge row contains the protected identity and digest.
CREATE TABLE auth_email_outbox (
    challenge_id VARBINARY(128) NOT NULL PRIMARY KEY,
    code_digits TINYINT UNSIGNED NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    claim_token BINARY(32) NULL,
    lease_until DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    terminal_at DATETIME(6) NULL,
    KEY auth_email_outbox_pending (state, created_at, challenge_id),
    KEY auth_email_outbox_leases (state, lease_until),
    CONSTRAINT auth_email_outbox_challenge_fk FOREIGN KEY (challenge_id) REFERENCES auth_email_login_challenge(challenge_id),
    CONSTRAINT auth_email_outbox_digits CHECK (code_digits BETWEEN 6 AND 10),
    CONSTRAINT auth_email_outbox_state CHECK (state IN ('pending','claimed','sending','accepted','failed','unknown','superseded','expired')),
    CONSTRAINT auth_email_outbox_shape CHECK (
        (state = 'pending' AND claim_token IS NULL AND lease_until IS NULL AND terminal_at IS NULL) OR
        (state IN ('claimed','sending') AND claim_token IS NOT NULL AND lease_until IS NOT NULL AND terminal_at IS NULL) OR
        (state IN ('accepted','failed','unknown','superseded','expired') AND claim_token IS NULL AND lease_until IS NULL AND terminal_at IS NOT NULL)
    )
) ENGINE=InnoDB;
