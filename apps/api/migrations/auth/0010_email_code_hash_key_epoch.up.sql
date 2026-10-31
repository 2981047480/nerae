-- The verifier key is independent of the code derivation key. Bind it before
-- email login traffic or mail processing starts; no secret is stored here.
-- Existing databases with challenge history require a reviewed recovery path.
CREATE TABLE auth_email_code_hash_key_epoch (
    slot TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    key_id VARBINARY(64) NOT NULL,
    key_tag BINARY(32) NOT NULL,
    CONSTRAINT auth_email_code_hash_key_singleton CHECK (slot = 1),
    CONSTRAINT auth_email_code_hash_key_id_size CHECK (OCTET_LENGTH(key_id) BETWEEN 1 AND 64)
) ENGINE=InnoDB;
