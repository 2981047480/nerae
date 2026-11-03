-- One encrypted issued-session receipt per approval. Scopes and request
-- digests are keyed values; never store the approval secret, proof or token.
CREATE TABLE auth_approval_exchange_receipt_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_approval_exchange_receipt_key_singleton CHECK (slot = 1)
) ENGINE=InnoDB;

CREATE TABLE auth_approval_exchange_receipt (
    approval_scope BINARY(32) NOT NULL PRIMARY KEY,
    subject_scope BINARY(32) NOT NULL,
    request_key_hmac BINARY(32) NOT NULL,
    request_hmac BINARY(32) NOT NULL,
    session_reference VARBINARY(128) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    response_enc VARBINARY(8221) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    KEY auth_approval_exchange_receipt_expiry (expires_at),
    CONSTRAINT auth_approval_exchange_receipt_session_size CHECK (OCTET_LENGTH(session_reference) BETWEEN 22 AND 128),
    CONSTRAINT auth_approval_exchange_receipt_key_id_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT auth_approval_exchange_receipt_ciphertext_size CHECK (OCTET_LENGTH(response_enc) BETWEEN 29 AND 8221),
    CONSTRAINT auth_approval_exchange_receipt_expiry CHECK (
        expires_at > created_at AND expires_at <= DATE_ADD(created_at, INTERVAL 24 HOUR)
    )
) ENGINE=InnoDB;
