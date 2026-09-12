DROP INDEX IF EXISTS nutrition_log_idem;
ALTER TABLE nutrition_log DROP COLUMN IF EXISTS idempotency_key;

DROP INDEX IF EXISTS workout_session_idem;
ALTER TABLE workout_session DROP COLUMN IF EXISTS idempotency_key;
