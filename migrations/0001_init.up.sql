-- =============================================================================
-- Airo — esquema PostgreSQL
--
-- Convenções:
--   · UUID v7 para chaves (ordenável por tempo, bom para índices B-tree)
--   · timestamptz em tudo; a app é multi-fuso e o treino é local
--   · ENUM nativo onde o conjunto é fechado e estável; texto + CHECK onde
--     ainda se espera crescimento (a taxonomia de exercício vai crescer)
--   · JSONB apenas para o que é genuinamente poliforme (métricas de série)
--   · valores derivados NUNCA são colunas — ver nota no fim
-- =============================================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";  -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "citext";    -- email de recuperação, sem sensibilidade a maiúsculas

-- ─────────────────────────────────────────────────────────── identidade ─────

-- A identidade é o NÚMERO DE TELEFONE, não o email. Autenticação por OTP via
-- WhatsApp — ver 08-autenticacao.md, onde vivem as tabelas de OTP, dispositivos
-- e tokens.
CREATE TABLE app_user (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Sempre E.164 normalizado. Sem normalização, o mesmo número cria contas
    -- diferentes conforme como foi escrito.
    phone_e164    text UNIQUE NOT NULL CHECK (phone_e164 ~ '^\+[1-9][0-9]{7,14}$'),
    phone_region  char(2) NOT NULL,
    -- Opcional, e só para recuperação. NUNCA como identidade.
    email         citext UNIQUE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz,
    deleted_at    timestamptz
);

CREATE TYPE sex AS ENUM ('female', 'male', 'unspecified');
CREATE TYPE experience AS ENUM ('beginner', 'intermediate', 'advanced');
CREATE TYPE budget AS ENUM ('low', 'medium', 'high');
CREATE TYPE diet_style AS ENUM ('omnivore', 'high_protein', 'vegetarian', 'vegan');
CREATE TYPE workout_time AS ENUM ('morning', 'midday', 'evening');

CREATE TABLE profile (
    user_id            uuid PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
    display_name       text NOT NULL,
    photo_url          text,
    birth_date         date,
    sex                sex NOT NULL DEFAULT 'unspecified',
    height_cm          numeric(5,1) NOT NULL,

    experience         experience NOT NULL,
    -- 0 = segunda-feira. Ver nota sobre o início da semana no fim do ficheiro.
    workout_days       smallint[] NOT NULL DEFAULT '{0,2,4}',
    workout_minutes    smallint NOT NULL DEFAULT 30,
    workout_time       workout_time NOT NULL DEFAULT 'evening',
    equipment          text[] NOT NULL DEFAULT '{}',

    diet_style         diet_style NOT NULL DEFAULT 'omnivore',
    meals_per_day      smallint NOT NULL DEFAULT 4 CHECK (meals_per_day BETWEEN 3 AND 5),
    food_budget        budget NOT NULL DEFAULT 'medium',
    food_exclusions    text[] NOT NULL DEFAULT '{}',
    meal_times         jsonb NOT NULL DEFAULT '{}',

    specialist_ids     text[] NOT NULL DEFAULT '{}',
    profile_complete   boolean NOT NULL DEFAULT false,
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- O peso é uma série temporal, não um campo do perfil. Tratá-lo como campo é o
-- que torna impossível calcular tendência.
CREATE TYPE metric_key AS ENUM (
    'body_weight', 'body_fat', 'waist', 'chest', 'hip', 'thigh', 'arm',
    'resting_hr', 'sessions_per_week', 'weekly_minutes'
);

CREATE TABLE measurement (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    metric       metric_key NOT NULL,
    value        numeric(8,2) NOT NULL,
    unit         text NOT NULL,
    recorded_at  timestamptz NOT NULL,
    source       text NOT NULL DEFAULT 'manual',
    note         text
);
CREATE INDEX measurement_series ON measurement (user_id, metric, recorded_at DESC);

-- ─────────────────────────────────────────────────── goal · journey ─────────

CREATE TYPE goal_type      AS ENUM ('outcome','behavior','performance','maintenance','hybrid');
CREATE TYPE goal_horizon   AS ENUM ('fixed','open_ended','review_based');
CREATE TYPE goal_direction AS ENUM ('lose_weight','gain_weight','maintain_weight');
CREATE TYPE goal_priority  AS ENUM ('weight','muscle','performance','health','appearance');
CREATE TYPE lifecycle      AS ENUM ('draft','active','paused','completed','abandoned','archived');

CREATE TABLE goal (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    type       goal_type NOT NULL,
    horizon    goal_horizon NOT NULL,
    direction  goal_direction NOT NULL,
    priority   goal_priority NOT NULL,
    status     lifecycle NOT NULL DEFAULT 'draft',
    created_at timestamptz NOT NULL DEFAULT now()
);
-- Um objetivo activo de cada vez por utilizador. Dois planos em simultâneo é a
-- receita para dois motores a discordar sobre o mesmo dia.
CREATE UNIQUE INDEX goal_one_active
    ON goal (user_id) WHERE status = 'active';

CREATE TABLE journey (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    goal_id     uuid NOT NULL REFERENCES goal(id) ON DELETE CASCADE,
    horizon     goal_horizon NOT NULL,
    start_date  date NOT NULL,
    target_date date,
    status      lifecycle NOT NULL DEFAULT 'active',
    -- Horizonte aberto corre em ciclos; ver journeyConfig.openEndedCycleWeeks.
    cycle_weeks smallint,
    created_at  timestamptz NOT NULL DEFAULT now(),

    -- ⚠️ A restrição que impede o modelo de mentir.
    -- Só o horizonte fechado tem data-alvo. Inventar uma data para uniformizar
    -- o esquema é exactamente o que a especificação rejeita: confundiria
    -- "quero perder 5 kg em 90 dias" com "quero treinar para sempre".
    CONSTRAINT journey_horizon_dates CHECK (
        (horizon = 'fixed'        AND target_date IS NOT NULL AND cycle_weeks IS NULL)
     OR (horizon = 'open_ended'   AND target_date IS NULL     AND cycle_weeks IS NOT NULL)
     OR (horizon = 'review_based' AND target_date IS NULL     AND cycle_weeks IS NOT NULL)
    ),
    CONSTRAINT journey_dates_ordered CHECK (target_date IS NULL OR target_date > start_date)
);
CREATE INDEX journey_by_goal ON journey (goal_id, status);

CREATE TABLE journey_pause (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id  uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    paused_at   timestamptz NOT NULL,
    resumed_at  timestamptz,
    reason      text,
    CONSTRAINT pause_ordered CHECK (resumed_at IS NULL OR resumed_at > paused_at)
);

CREATE TYPE target_direction AS ENUM ('increase','decrease','maintain');
CREATE TYPE target_status    AS ENUM ('pending','on_track','at_risk','reached','missed');

CREATE TABLE target (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    metric     metric_key NOT NULL,
    direction  target_direction NOT NULL,
    baseline   numeric(8,2) NOT NULL,
    value      numeric(8,2) NOT NULL,
    unit       text NOT NULL,
    -- NULL em horizonte aberto: o alvo existe, a data não.
    due_date   date,
    status     target_status NOT NULL DEFAULT 'pending'
);

-- Fases só existem em horizonte fechado. Um estilo de vida não tem "fase de
-- transição" — não está a caminhar para lado nenhum.
CREATE TYPE phase_kind AS ENUM ('adaptation','development','progression','transition','maintenance');

CREATE TABLE phase (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    kind       phase_kind NOT NULL,
    position   smallint NOT NULL,
    start_date date NOT NULL,
    end_date   date NOT NULL,
    CONSTRAINT phase_ordered CHECK (end_date > start_date),
    UNIQUE (journey_id, position)
);

-- Ciclos: a unidade de revisão do horizonte aberto e do review_based.
CREATE TABLE cycle (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id  uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    index       smallint NOT NULL,
    start_date  date NOT NULL,
    review_date date NOT NULL,
    closed_at   timestamptz,
    UNIQUE (journey_id, index),
    CONSTRAINT cycle_ordered CHECK (review_date > start_date)
);

-- ────────────────────────────────────────────────────────────── plano ───────

CREATE TYPE intensity      AS ENUM ('low','moderate','high');
CREATE TYPE progression    AS ENUM ('hold','linear','adaptive');
CREATE TYPE recovery_mode  AS ENUM ('standard','extra','deload');

CREATE TABLE plan (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id          uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    phase_id            uuid REFERENCES phase(id) ON DELETE SET NULL,
    cycle_id            uuid REFERENCES cycle(id) ON DELETE SET NULL,
    frequency_per_week  smallint NOT NULL,
    session_minutes     smallint NOT NULL,
    intensity           intensity NOT NULL,
    progression         progression NOT NULL,
    recovery            recovery_mode NOT NULL,
    effective_from      date NOT NULL,
    effective_to        date,
    superseded_by       uuid REFERENCES plan(id),
    created_at          timestamptz NOT NULL DEFAULT now()
);
-- Um plano por jornada em vigor num dado momento.
CREATE INDEX plan_active ON plan (journey_id, effective_from DESC)
    WHERE effective_to IS NULL;

-- ─────────────────────────────────────────────── catálogo de exercícios ─────

CREATE TYPE exercise_category AS ENUM (
    'strength','hypertrophy','muscular_endurance','cardio','hiit','power',
    'mobility','flexibility','core','balance','agility','recovery','mind_body'
);
CREATE TYPE difficulty  AS ENUM ('beginner','intermediate','advanced');
CREATE TYPE impact_level AS ENUM ('low','moderate','high');
CREATE TYPE mechanics   AS ENUM ('compound','isolation');
CREATE TYPE measure     AS ENUM ('reps','time','distance');

CREATE TABLE exercise (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug                 text UNIQUE NOT NULL,
    name                 text NOT NULL,
    category             exercise_category NOT NULL,
    subcategory          text,

    -- Listas porque um burpee é [squat, push, jump]. Guardar um só padrão
    -- obrigaria a escolher qual — e a escolha estaria errada para metade das
    -- consultas de substituição.
    movement_patterns    text[] NOT NULL DEFAULT '{}',
    goals                text[] NOT NULL DEFAULT '{}',
    primary_muscles      text[] NOT NULL DEFAULT '{}',
    secondary_muscles    text[] NOT NULL DEFAULT '{}',
    -- Vazio = só o corpo, logo sempre disponível. Basta ter UM dos listados.
    equipment            text[] NOT NULL DEFAULT '{}',

    difficulty           difficulty NOT NULL,
    technical_difficulty difficulty NOT NULL,
    physical_difficulty  difficulty NOT NULL,
    intensity            intensity NOT NULL,
    impact_level         impact_level NOT NULL,
    mechanics            mechanics NOT NULL,
    measure              measure NOT NULL,
    unilateral           boolean NOT NULL DEFAULT false,

    is_warmup            boolean NOT NULL DEFAULT false,
    is_cooldown          boolean NOT NULL DEFAULT false,

    cue                  text NOT NULL,
    instructions         text[] NOT NULL DEFAULT '{}',
    contraindications    text[] NOT NULL DEFAULT '{}',
    demo_url             text,
    demo_query           text,

    active               boolean NOT NULL DEFAULT true
);

-- GIN nos arrays: é como o Training Engine procura substituições.
CREATE INDEX exercise_patterns  ON exercise USING gin (movement_patterns);
CREATE INDEX exercise_equipment ON exercise USING gin (equipment);
CREATE INDEX exercise_goals     ON exercise USING gin (goals);
CREATE INDEX exercise_lookup    ON exercise (category, difficulty) WHERE active;

-- ───────────────────────────────────────────────────────── sessões ──────────

CREATE TYPE session_focus  AS ENUM ('upper','lower','cardio','full','mobility');
CREATE TYPE session_role   AS ENUM ('warmup','main','cooldown');
CREATE TYPE session_status AS ENUM ('planned','completed','skipped');

CREATE TABLE planned_session (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id      uuid NOT NULL REFERENCES plan(id) ON DELETE CASCADE,
    scheduled_on date NOT NULL,
    weekday      smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    focus        session_focus NOT NULL,
    label        text NOT NULL,
    minutes      smallint NOT NULL,
    UNIQUE (plan_id, scheduled_on)
);

CREATE TABLE workout_session (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    -- Nulos de propósito: uma sessão gravada antes de existir jornada continua
    -- a ser uma sessão. Era isto que faltava e deixava o histórico vazio.
    journey_id         uuid REFERENCES journey(id) ON DELETE SET NULL,
    planned_session_id uuid REFERENCES planned_session(id) ON DELETE SET NULL,

    title              text NOT NULL,
    focus              session_focus NOT NULL,
    status             session_status NOT NULL,
    occurred_at        timestamptz NOT NULL,
    local_day          date NOT NULL,

    planned_seconds    integer NOT NULL,
    duration_seconds   integer NOT NULL,
    sets_planned       smallint NOT NULL DEFAULT 0,
    sets_done          smallint NOT NULL DEFAULT 0,
    kcal               integer NOT NULL DEFAULT 0,
    exercise_count     smallint NOT NULL DEFAULT 0,

    -- Segundos por bloco. Sem isto o histórico sabe quanto durou mas não em que
    -- foi gasto — e o anel de composição fica vazio.
    warmup_seconds     integer,
    main_seconds       integer,
    cooldown_seconds   integer,

    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX session_history ON workout_session (user_id, occurred_at DESC);
CREATE INDEX session_by_day  ON workout_session (user_id, local_day);

CREATE TABLE exercise_prescription (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   uuid NOT NULL REFERENCES workout_session(id) ON DELETE CASCADE,
    exercise_id  uuid NOT NULL REFERENCES exercise(id),
    position     smallint NOT NULL,
    role         session_role NOT NULL,
    sets         smallint NOT NULL CHECK (sets >= 1),
    target       jsonb NOT NULL,
    rest_seconds smallint NOT NULL,
    tempo        text,
    UNIQUE (session_id, position)
);

CREATE TABLE exercise_set (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    prescription_id uuid NOT NULL REFERENCES exercise_prescription(id) ON DELETE CASCADE,
    index           smallint NOT NULL,
    -- Cópia deliberada da prescrição. Uma adaptação futura reescreve a
    -- prescrição; o histórico tem de continuar a dizer o que foi pedido
    -- NAQUELE dia. Sem a cópia, adaptar reescreveria o passado.
    target          jsonb NOT NULL,
    actual          jsonb,
    completed       boolean NOT NULL DEFAULT false,
    completed_at    timestamptz,
    UNIQUE (prescription_id, index)
);

-- Forma de `target` / `actual`:
--   {"type":"reps","reps":12}
--   {"type":"time","durationSeconds":45}
--   {"type":"load_reps","reps":10,"weightKg":40}
--   {"type":"distance","distanceMeters":400,"durationSeconds":95}
-- JSONB e não colunas porque a combinação é genuinamente poliforme; colunas
-- dariam seis anuláveis das quais quatro estão sempre vazias.

-- ─────────────────────────────────────────────────────────── nutrição ───────

CREATE TYPE nutrition_goal AS ENUM ('lose_fat','gain_muscle','maintain','performance','health');
CREATE TYPE meal_slot      AS ENUM ('breakfast','lunch','snack','dinner','supper');
CREATE TYPE meal_role      AS ENUM ('balanced','pre_workout','post_workout','light');
CREATE TYPE log_status     AS ENUM ('planned','eaten','partial','skipped','substituted','custom');
CREATE TYPE food_category  AS ENUM ('protein','carb','vegetable','fat','fruit','dairy');

CREATE TABLE food (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         text UNIQUE NOT NULL,
    name         text NOT NULL,
    category     food_category NOT NULL,
    -- Sempre por 100 g. Normalizar aqui evita converter em vinte sítios.
    kcal         numeric(6,1) NOT NULL,
    protein_g    numeric(5,1) NOT NULL,
    carbs_g      numeric(5,1) NOT NULL,
    fat_g        numeric(5,1) NOT NULL,
    fiber_g      numeric(5,1),
    serving_g    smallint NOT NULL,
    styles       text[] NOT NULL DEFAULT '{}',
    budget       budget NOT NULL,
    good_for     text[] NOT NULL DEFAULT '{}',
    -- Contexto regional, não exclusão: pondera, não filtra.
    region       text,
    active       boolean NOT NULL DEFAULT true
);
CREATE INDEX food_styles ON food USING gin (styles);
CREATE INDEX food_lookup ON food (category, budget) WHERE active;

CREATE TABLE recipe (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug     text UNIQUE NOT NULL,
    name     text NOT NULL,
    slots    text[] NOT NULL DEFAULT '{}',
    styles   text[] NOT NULL DEFAULT '{}',
    active   boolean NOT NULL DEFAULT true
);

CREATE TABLE recipe_ingredient (
    recipe_id uuid NOT NULL REFERENCES recipe(id) ON DELETE CASCADE,
    food_id   uuid NOT NULL REFERENCES food(id),
    grams     smallint NOT NULL,
    PRIMARY KEY (recipe_id, food_id)
);

CREATE TABLE nutrition_strategy (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    journey_id      uuid REFERENCES journey(id) ON DELETE SET NULL,
    goal            nutrition_goal NOT NULL,
    calorie_target  integer NOT NULL CHECK (calorie_target >= 1500),
    protein_g       smallint NOT NULL,
    carbs_g         smallint NOT NULL,
    fat_g           smallint NOT NULL,
    tdee_estimated  integer NOT NULL,
    tdee_observed   integer,
    effective_from  date NOT NULL,
    effective_to    date,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nutrition_cycle (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    strategy_id  uuid NOT NULL REFERENCES nutrition_strategy(id) ON DELETE CASCADE,
    index        smallint NOT NULL,
    start_date   date NOT NULL,
    review_date  date NOT NULL,
    closed_at    timestamptz,
    UNIQUE (strategy_id, index)
);

CREATE TABLE daily_plan (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    strategy_id uuid NOT NULL REFERENCES nutrition_strategy(id) ON DELETE CASCADE,
    local_day   date NOT NULL,
    UNIQUE (user_id, local_day)
);

CREATE TABLE planned_meal (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    daily_plan_id uuid NOT NULL REFERENCES daily_plan(id) ON DELETE CASCADE,
    slot          meal_slot NOT NULL,
    role          meal_role NOT NULL,
    title         text NOT NULL,
    kcal_target   smallint NOT NULL,
    protein_g     numeric(5,1) NOT NULL,
    carbs_g       numeric(5,1) NOT NULL,
    fat_g         numeric(5,1) NOT NULL,
    UNIQUE (daily_plan_id, slot)
);

CREATE TABLE meal_item (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    planned_meal_id uuid NOT NULL REFERENCES planned_meal(id) ON DELETE CASCADE,
    food_id         uuid NOT NULL REFERENCES food(id),
    grams           smallint NOT NULL,
    position        smallint NOT NULL,
    UNIQUE (planned_meal_id, position)
);

CREATE TABLE favourite_meal (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    slot       meal_slot NOT NULL,
    title      text NOT NULL,
    -- Guarda a COMBINAÇÃO, não as gramas: ao aplicar noutro lugar as porções
    -- reescalam para o alvo desse lugar.
    items      jsonb NOT NULL,
    kcal       smallint NOT NULL,
    saved_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nutrition_log (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    daily_plan_id uuid REFERENCES daily_plan(id) ON DELETE SET NULL,
    slot          meal_slot NOT NULL,
    status        log_status NOT NULL,
    -- Fração consumida. 0 é um registo válido: "não comi" não é "não registei".
    portion       numeric(3,2) NOT NULL CHECK (portion BETWEEN 0 AND 1),
    kcal          smallint NOT NULL,
    protein_g     numeric(5,1) NOT NULL,
    carbs_g       numeric(5,1) NOT NULL,
    fat_g         numeric(5,1) NOT NULL,
    source        text NOT NULL CHECK (source IN ('plan','manual','photo')),
    label         text,
    portion_label text,
    photo_url     text,
    recorded_at   timestamptz NOT NULL,
    local_day     date NOT NULL
);
CREATE INDEX nutrition_log_day ON nutrition_log (user_id, local_day);

-- ───────────────────────────────────── progresso · risco · adaptação ────────

CREATE TYPE risk_type AS ENUM (
    'low_adherence','rapid_change','plateau','volume_spike','stalled_start','under_recovery'
);
CREATE TYPE severity AS ENUM ('info','warning','critical');

CREATE TABLE assessment (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    journey_id   uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    cycle_id     uuid REFERENCES cycle(id) ON DELETE SET NULL,
    assessed_at  timestamptz NOT NULL DEFAULT now(),
    -- O instantâneo que produziu a decisão. Guardado inteiro porque sem ele não
    -- há como explicar à pessoa porque é que o plano mudou.
    snapshot     jsonb NOT NULL,
    adherence    numeric(4,3) NOT NULL,
    confidence   text NOT NULL CHECK (confidence IN ('low','medium','high'))
);

CREATE TABLE risk (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id uuid NOT NULL REFERENCES assessment(id) ON DELETE CASCADE,
    type          risk_type NOT NULL,
    severity      severity NOT NULL,
    detail        text NOT NULL
);

CREATE TYPE adaptation_kind AS ENUM (
    'reduce_frequency','increase_frequency','reduce_duration','increase_duration',
    'reduce_intensity','increase_intensity','deload','extend_timeline','hold',
    'calorie_up','calorie_down','recalibrate'
);

CREATE TABLE adaptation (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assessment_id uuid NOT NULL REFERENCES assessment(id) ON DELETE CASCADE,
    kind          adaptation_kind NOT NULL,
    payload       jsonb NOT NULL,
    -- Uma adaptação proposta não é uma adaptação aplicada. Nunca corre sozinha:
    -- é sempre o utilizador que aceita.
    applied_at    timestamptz,
    applied_by    text CHECK (applied_by IN ('user','system'))
);

-- ─────────────────────────────────────────────────────── eventos ────────────

-- Log append-only. É o que permite reconstruir a jornada e depurar uma
-- adaptação meses depois.
CREATE TABLE journey_event (
    id         bigserial PRIMARY KEY,
    journey_id uuid NOT NULL REFERENCES journey(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    payload    jsonb NOT NULL DEFAULT '{}',
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX journey_event_stream ON journey_event (journey_id, occurred_at);

-- =============================================================================
-- NOTAS
--
-- 1. VALORES DERIVADOS NÃO SÃO COLUNAS
--    Taxa de conclusão, sequência, adesão, tendência de peso, séries feitas
--    esta semana — tudo isto é agregação sobre as tabelas de facto. Guardá-los
--    como colunas cria um segundo sítio onde a verdade pode estar errada, e o
--    primeiro treino registado com atraso torna-os mentira.
--    Se a leitura ficar lenta: MATERIALIZED VIEW com refresh, não coluna.
--
-- 2. local_day EXISTE AO LADO DE occurred_at
--    Não é redundância. `occurred_at` é o instante absoluto; `local_day` é o
--    dia a que o utilizador atribui o treino. Quem treina às 23h30 e voa para
--    outro fuso continua a querer ver aquele treino nesse dia. Agregar por
--    `date(occurred_at AT TIME ZONE ...)` obriga a conhecer o fuso de então,
--    que já não se sabe.
--
-- 3. A SEMANA COMEÇA À SEGUNDA
--    `workout_days` e `weekday` usam 0 = segunda, para bater certo com
--    `weekdayShortNames` no cliente. O `EXTRACT(dow)` do Postgres usa
--    0 = domingo: converter SEMPRE com `((EXTRACT(dow FROM d)::int + 6) % 7)`.
--
-- 4. O QUE NÃO TEM CASCADE
--    `workout_session.journey_id` é ON DELETE SET NULL, não CASCADE. Apagar uma
--    jornada não pode apagar o histórico de treino: a pessoa treinou mesmo.
-- =============================================================================
