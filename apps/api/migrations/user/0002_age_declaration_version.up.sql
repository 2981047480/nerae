ALTER TABLE user
    ADD COLUMN age_declaration_version BIGINT UNSIGNED NOT NULL DEFAULT 1,
    ADD CONSTRAINT user_age_declaration_version_range
        CHECK (age_declaration_version BETWEEN 1 AND 9007199254740991);
