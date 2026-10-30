-- Anonymous email challenge retries; plaintext email, OTP, request body, and
-- idempotency key must never be stored in this table.
CREATE TABLE auth_email_challenge_request_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_email_request_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

CREATE TABLE auth_email_challenge_request (
    email_index BINARY(32) NOT NULL,
    request_key_hmac BINARY(32) NOT NULL,
    request_hmac BINARY(32) NOT NULL,
    challenge_id VARBINARY(128) NOT NULL,
    response_enc VARBINARY(2048) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    PRIMARY KEY (email_index, request_key_hmac),
    KEY auth_email_request_expiry (expires_at),
    KEY auth_email_request_challenge (challenge_id),
    CONSTRAINT auth_email_request_challenge_fk FOREIGN KEY (challenge_id)
        REFERENCES auth_email_login_challenge(challenge_id),
    CONSTRAINT auth_email_request_valid_expiry CHECK (expires_at > created_at)
) ENGINE=InnoDB;
