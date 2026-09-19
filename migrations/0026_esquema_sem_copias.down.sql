CREATE TYPE risk_type AS ENUM (
    'low_adherence','rapid_change','plateau','volume_spike','stalled_start','under_recovery'
);
CREATE TYPE severity AS ENUM ('info','warning','critical');

CREATE TABLE IF NOT EXISTS risk (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id uuid NOT NULL REFERENCES assessment(id) ON DELETE CASCADE,
    type          risk_type NOT NULL,
    severity      severity NOT NULL,
    detail        text NOT NULL
);

CREATE TABLE IF NOT EXISTS nutrition_cycle (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id  uuid NOT NULL REFERENCES nutrition_strategy(id) ON DELETE CASCADE,
    index        smallint NOT NULL,
    start_date   date NOT NULL,
    review_date  date NOT NULL,
    closed_at    timestamptz,
    UNIQUE (strategy_id, index)
);
