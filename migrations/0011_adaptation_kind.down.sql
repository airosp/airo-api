ALTER TABLE adaptation ALTER COLUMN kind TYPE text;
DROP TYPE adaptation_kind;
CREATE TYPE adaptation_kind AS ENUM (
    'reduce_frequency','increase_frequency','reduce_duration','increase_duration',
    'reduce_intensity','increase_intensity','deload','extend_timeline','hold',
    'calorie_up','calorie_down','recalibrate'
);
ALTER TABLE adaptation ALTER COLUMN kind TYPE adaptation_kind USING kind::adaptation_kind;
