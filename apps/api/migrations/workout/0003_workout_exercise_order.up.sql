-- The true order of an existing actual set cannot be recovered from its ID,
-- timestamp, or set_index. Stop before creating the order table if any actual
-- records already exist. Upgrades must first quiesce writers and use an
-- explicitly reviewed recovery procedure; do not invent historical order.
ALTER TABLE set_record ADD CONSTRAINT workout_order_requires_empty_sets CHECK (1 = 0);
ALTER TABLE set_record DROP CHECK workout_order_requires_empty_sets;

CREATE TABLE workout_exercise_order (
    session_id BIGINT UNSIGNED NOT NULL,
    exercise_entry_key VARBINARY(256) NOT NULL,
    position SMALLINT UNSIGNED NOT NULL,
    PRIMARY KEY (session_id, exercise_entry_key),
    UNIQUE KEY workout_exercise_order_position (session_id, position),
    CONSTRAINT workout_exercise_order_entry_fk FOREIGN KEY (session_id, exercise_entry_key)
        REFERENCES workout_exercise (session_id, exercise_entry_key),
    CONSTRAINT workout_exercise_order_position_range CHECK (position BETWEEN 1 AND 2000)
) ENGINE=InnoDB;
