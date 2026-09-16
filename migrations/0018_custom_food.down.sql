DROP INDEX IF EXISTS favourite_meal_owner;
ALTER TABLE favourite_meal ALTER COLUMN id TYPE uuid USING id::uuid;
ALTER TABLE favourite_meal ALTER COLUMN id SET DEFAULT gen_random_uuid();
DROP TABLE IF EXISTS custom_food;
