-- ⚠️ Volta a `uuid`. Chaves que não sejam UUID **perdem-se** — não há forma de
-- as converter, e deixá-las bloquearia a migração para sempre.
UPDATE workout_session SET idempotency_key = NULL
 WHERE idempotency_key !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
UPDATE nutrition_log SET idempotency_key = NULL
 WHERE idempotency_key !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';

ALTER TABLE workout_session
    ALTER COLUMN idempotency_key TYPE uuid USING idempotency_key::uuid;
ALTER TABLE nutrition_log
    ALTER COLUMN idempotency_key TYPE uuid USING idempotency_key::uuid;
