-- Only authenticated, same-MySQL-transaction operations may use these rows.
-- Anonymous email challenges and external SMTP delivery need separate protocols.
CREATE TABLE transport_idempotency_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NULL,
    key_tag BINARY(32) NULL,
    CONSTRAINT transport_idempotency_epoch_singleton CHECK (slot = 1),
    CONSTRAINT transport_idempotency_epoch_pair CHECK ((key_id IS NULL) = (key_tag IS NULL))
) ENGINE=InnoDB;
INSERT INTO transport_idempotency_key_epoch (slot, key_id, key_tag) VALUES (1, NULL, NULL);

CREATE TABLE transport_idempotency_record (
    actor_id BIGINT UNSIGNED NOT NULL,
    method VARBINARY(8) NOT NULL,
    route VARBINARY(256) NOT NULL,
    request_key VARBINARY(512) NOT NULL,
    request_hmac BINARY(32) NOT NULL,
    claim_token BINARY(32) NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    response_status SMALLINT UNSIGNED NULL,
    response_enc MEDIUMBLOB NULL,
    key_id VARBINARY(64) NULL,
    result_ref VARBINARY(256) NULL,
    created_at DATETIME(6) NOT NULL,
    completed_at DATETIME(6) NULL,
    expires_at DATETIME(6) NOT NULL,
    PRIMARY KEY (actor_id, method, route, request_key),
    KEY transport_idempotency_expiry (state, expires_at),
    CONSTRAINT transport_idempotency_expiry_valid CHECK (expires_at > created_at),
    CONSTRAINT transport_idempotency_actor_fk FOREIGN KEY (actor_id) REFERENCES user (id),
    CONSTRAINT transport_idempotency_state_valid CHECK (state IN ('processing', 'succeeded', 'failed')),
    CONSTRAINT transport_idempotency_result_consistent CHECK (
        (state = 'processing' AND response_status IS NULL AND response_enc IS NULL AND key_id IS NULL AND result_ref IS NULL AND completed_at IS NULL)
        OR (state = 'succeeded' AND response_status IS NOT NULL AND response_status BETWEEN 200 AND 299 AND response_enc IS NOT NULL AND key_id IS NOT NULL AND result_ref IS NOT NULL AND completed_at IS NOT NULL)
        OR (state = 'failed' AND response_status IS NOT NULL AND response_status IN (409, 412, 422) AND response_enc IS NOT NULL AND key_id IS NOT NULL AND result_ref IS NOT NULL AND completed_at IS NOT NULL)
    )
) ENGINE=InnoDB;
