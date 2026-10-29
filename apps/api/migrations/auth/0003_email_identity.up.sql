CREATE TABLE login_identity (
    id VARBINARY(128) NOT NULL PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    provider_scope VARBINARY(64) NOT NULL,
    subject_enc VARBINARY(512) NOT NULL,
    subject_lookup_hmac BINARY(32) NOT NULL,
    key_id VARBINARY(64) NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    active_slot TINYINT GENERATED ALWAYS AS (CASE WHEN state = 'active' THEN 1 ELSE NULL END) STORED,
    verified_at DATETIME(6) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    UNIQUE KEY identity_active_subject (kind, provider_scope, subject_lookup_hmac, active_slot),
    UNIQUE KEY identity_active_kind (user_id, kind, active_slot),
    CONSTRAINT login_identity_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT login_identity_kind_valid CHECK (kind IN ('email', 'phone', 'wechat_mini')),
    CONSTRAINT login_identity_state_valid CHECK (state IN ('active', 'revoked'))
) ENGINE=InnoDB;

CREATE TABLE trusted_device (
    id VARBINARY(128) NOT NULL PRIMARY KEY,
    user_id BIGINT UNSIGNED NOT NULL,
    key_fingerprint BINARY(32) NOT NULL,
    public_x CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    public_y CHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    last_used_at DATETIME(6) NULL,
    UNIQUE KEY trusted_device_key (key_fingerprint),
    CONSTRAINT trusted_device_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT trusted_device_state_valid CHECK (state IN ('active', 'revoked'))
) ENGINE=InnoDB;
