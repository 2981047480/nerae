CREATE TABLE user (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    nickname VARCHAR(64) NULL,
    avatar_asset_id BIGINT UNSIGNED NULL,
    gender TINYINT UNSIGNED NOT NULL DEFAULT 0,
    birth_year SMALLINT UNSIGNED NULL,
    age_verified_at DATETIME(6) NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
    token_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    identity_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    last_active_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    CONSTRAINT user_gender_range CHECK (gender IN (0, 1, 2)),
    CONSTRAINT user_status_valid CHECK (status IN ('active', 'disabled')),
    CONSTRAINT user_versions_positive CHECK (token_version >= 1 AND identity_version >= 1)
) ENGINE=InnoDB;

CREATE TABLE user_role (
    user_id BIGINT UNSIGNED NOT NULL,
    role VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (user_id, role),
    CONSTRAINT user_role_user_fk FOREIGN KEY (user_id) REFERENCES user (id),
    CONSTRAINT user_role_valid CHECK (role IN ('student', 'coach') AND status IN ('active', 'disabled'))
) ENGINE=InnoDB;
