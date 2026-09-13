DROP INDEX IF EXISTS adaptation_pendentes;
ALTER TABLE adaptation DROP COLUMN IF EXISTS journey_id;
ALTER TABLE adaptation DROP COLUMN IF EXISTS dismissed_at;
