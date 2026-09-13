DROP INDEX IF EXISTS nutrition_log_client;
ALTER TABLE nutrition_log
    DROP COLUMN client_id,
    DROP COLUMN photo_thumb_url;
