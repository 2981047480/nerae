-- Anonymous email-login idempotency receipts. The keyed scope and request-key
-- digest are lookup metadata; the result is always encrypted by loginreceipt.
-- Never add plaintext email, OTP, request proof, or credential columns here.
CREATE TABLE auth_email_login_receipt_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_email_login_receipt_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

CREATE TABLE auth_email_login_receipt (
    email_scope BINARY(32) NOT NULL,
    request_key_hmac BINARY(32) NOT NULL,
    request_hmac BINARY(32) NOT NULL,
    challenge_id VARBINARY(128) NOT NULL,
    result_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    result_reference VARBINARY(256) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    response_enc VARBINARY(8221) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    PRIMARY KEY (email_scope, request_key_hmac),
    KEY auth_email_login_receipt_expiry (expires_at),
    KEY auth_email_login_receipt_challenge (challenge_id),
    CONSTRAINT auth_email_login_receipt_challenge_fk FOREIGN KEY (challenge_id)
        REFERENCES auth_email_login_challenge(challenge_id),
    CONSTRAINT auth_email_login_receipt_kind CHECK (result_kind IN ('authenticated','approval_required','rejected')),
    CONSTRAINT auth_email_login_receipt_challenge_size CHECK (OCTET_LENGTH(challenge_id) BETWEEN 22 AND 128),
    CONSTRAINT auth_email_login_receipt_reference_size CHECK (OCTET_LENGTH(result_reference) BETWEEN 1 AND 256),
    CONSTRAINT auth_email_login_receipt_key_id_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT auth_email_login_receipt_ciphertext_size CHECK (OCTET_LENGTH(response_enc) BETWEEN 29 AND 8221),
    CONSTRAINT auth_email_login_receipt_rejected_ref CHECK (result_kind <> 'rejected' OR result_reference = challenge_id),
    CONSTRAINT auth_email_login_receipt_expiry CHECK (
        expires_at > created_at AND expires_at <= DATE_ADD(created_at, INTERVAL 24 HOUR)
    )
) ENGINE=InnoDB;
