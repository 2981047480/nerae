ALTER TABLE auth_login_approval
    DROP CHECK auth_approval_state_valid,
    ADD COLUMN exchange_device_id VARBINARY(128) NULL,
    ADD COLUMN exchange_session_id VARBINARY(128) NULL,
    ADD COLUMN consumed_at DATETIME(6) NULL,
    ADD UNIQUE KEY auth_approval_exchange_session (exchange_session_id),
    ADD CONSTRAINT auth_approval_exchange_device_fk FOREIGN KEY (exchange_device_id) REFERENCES trusted_device (id),
    ADD CONSTRAINT auth_approval_exchange_session_fk FOREIGN KEY (exchange_session_id) REFERENCES auth_session (id),
    ADD CONSTRAINT auth_approval_state_valid CHECK (
        (state = 'pending' AND decision_session_id IS NULL AND decided_at IS NULL AND exchange_device_id IS NULL AND exchange_session_id IS NULL AND consumed_at IS NULL)
        OR (state IN ('approved', 'denied') AND decision_session_id IS NOT NULL AND decided_at IS NOT NULL AND exchange_device_id IS NULL AND exchange_session_id IS NULL AND consumed_at IS NULL)
        OR (state = 'consumed' AND decision_session_id IS NOT NULL AND decided_at IS NOT NULL AND exchange_device_id IS NOT NULL AND exchange_session_id IS NOT NULL AND consumed_at IS NOT NULL)
    );
