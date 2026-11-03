-- Non-secret first responses for anonymous approval polling. All identifiers
-- and request material are keyed digests; no proof, secret or token is stored.
CREATE TABLE auth_approval_poll_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_approval_poll_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

CREATE TABLE auth_approval_poll_receipt (
    approval_scope BINARY(32) NOT NULL,
    request_key_hmac BINARY(32) NOT NULL,
    subject_scope BINARY(32) NOT NULL,
    request_hmac BINARY(32) NOT NULL,
    status VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    PRIMARY KEY (approval_scope, request_key_hmac),
    KEY auth_approval_poll_receipt_expiry (expires_at),
    CONSTRAINT auth_approval_poll_receipt_status CHECK (status IN ('pending', 'denied', 'expired')),
    CONSTRAINT auth_approval_poll_receipt_key_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT auth_approval_poll_receipt_expiry CHECK (
        expires_at > created_at AND expires_at <= DATE_ADD(created_at, INTERVAL 24 HOUR)
    )
) ENGINE=InnoDB;
