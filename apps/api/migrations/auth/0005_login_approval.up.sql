CREATE TABLE auth_approval_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NULL,
    key_tag BINARY(32) NULL,
    CONSTRAINT auth_approval_epoch_singleton CHECK (slot = 1),
    CONSTRAINT auth_approval_epoch_pair CHECK ((key_id IS NULL AND key_tag IS NULL) OR (key_id IS NOT NULL AND key_tag IS NOT NULL))
) ENGINE=InnoDB;
INSERT INTO auth_approval_key_epoch (slot, key_id, key_tag) VALUES (1, NULL, NULL);

CREATE TABLE auth_login_approval (
    id VARBINARY(128) NOT NULL PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    identity_id VARBINARY(128) NOT NULL,
    challenge_id VARBINARY(128) NOT NULL,
    token_version BIGINT UNSIGNED NOT NULL,
    identity_version BIGINT UNSIGNED NOT NULL,
    target_fingerprint BINARY(32) NOT NULL,
    public_x CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    public_y CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    secret_digest BINARY(32) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    nonce VARBINARY(256) NOT NULL,
    comparison_code CHAR(6) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    decision_session_id VARBINARY(128) NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    decided_at DATETIME(6) NULL,
    UNIQUE KEY auth_approval_challenge (challenge_id),
    KEY auth_approval_user_state (user_id, state),
    CONSTRAINT auth_approval_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT auth_approval_identity_fk FOREIGN KEY (identity_id) REFERENCES login_identity (id),
    CONSTRAINT auth_approval_decision_session_fk FOREIGN KEY (decision_session_id) REFERENCES auth_session (id),
    CONSTRAINT auth_approval_time_valid CHECK (expires_at > created_at),
    CONSTRAINT auth_approval_versions_valid CHECK (token_version >= 1 AND identity_version >= 1),
    CONSTRAINT auth_approval_state_valid CHECK (
        (state = 'pending' AND decision_session_id IS NULL AND decided_at IS NULL)
        OR (state IN ('approved', 'denied') AND decision_session_id IS NOT NULL AND decided_at IS NOT NULL)
    )
) ENGINE=InnoDB;
