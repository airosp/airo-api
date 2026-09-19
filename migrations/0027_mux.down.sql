DROP INDEX IF EXISTS class_mux_asset;
ALTER TABLE workout_class DROP CONSTRAINT IF EXISTS class_publicada_tem_video;
ALTER TABLE workout_class ALTER COLUMN video_url DROP DEFAULT;
ALTER TABLE workout_class
    DROP COLUMN IF EXISTS mux_playback_id,
    DROP COLUMN IF EXISTS mux_asset_id,
    DROP COLUMN IF EXISTS mux_policy,
    DROP COLUMN IF EXISTS mux_ready;
