-- O painel da equipa.
--
-- Quem entra é a equipa da Airo, e só ela: contas próprias, que nada têm a ver
-- com as das pessoas da app. Uma pessoa da app nunca chega a ser membro, e um
-- membro não é uma pessoa da app — por isso tabelas separadas, e não uma
-- coluna `is_admin` em `app_user`, que bastaria esquecer num `WHERE` para abrir.

CREATE TYPE admin_role AS ENUM ('admin', 'content', 'nutrition', 'support', 'specialist');

CREATE TABLE admin_member (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext UNIQUE NOT NULL,
    name            text NOT NULL,
    role            admin_role NOT NULL,
    -- O especialista que este membro é, quando o é. Só vê as suas pessoas.
    specialist_id   text REFERENCES specialist(id) ON DELETE SET NULL,
    password_hash   text,
    -- O segredo da app autenticadora, cifrado. Nunca sai depois de activado.
    totp_secret     bytea,
    totp_enabled    boolean NOT NULL DEFAULT false,
    active          boolean NOT NULL DEFAULT true,
    failed_attempts smallint NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    -- O convite: o hash do token do link, e até quando vale.
    invite_hash     bytea UNIQUE,
    invite_expires  timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz,
    CONSTRAINT especialista_tem_ficha CHECK (role <> 'specialist' OR specialist_id IS NOT NULL)
);

-- Os dois passos da entrada. A palavra-passe certa dá um desafio; o código da
-- app autenticadora troca-o pela sessão.
CREATE TABLE admin_challenge (
    hash       bytea PRIMARY KEY,
    member_id  uuid NOT NULL REFERENCES admin_member(id) ON DELETE CASCADE,
    attempts   smallint NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL
);

CREATE TABLE admin_session (
    hash       bytea PRIMARY KEY,
    member_id  uuid NOT NULL REFERENCES admin_member(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX admin_session_member ON admin_session (member_id) WHERE revoked_at IS NULL;

-- A auditoria. Sem chave estrangeira para o membro de propósito: apagar um
-- membro não pode apagar o que ele fez.
CREATE TABLE admin_audit (
    id         bigserial PRIMARY KEY,
    member_id  uuid,
    actor_name text,
    action     text NOT NULL,
    target     text NOT NULL DEFAULT '',
    detail     text NOT NULL DEFAULT '',
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_recent ON admin_audit (at DESC);

-- O que se editou no painel deixa de ser do seed. Sem isto, o próximo
-- `migrate up` repunha o JSON por cima do que a nutricionista corrigiu.
ALTER TABLE exercise      ADD COLUMN admin_edited_at timestamptz;
ALTER TABLE food          ADD COLUMN admin_edited_at timestamptz;
ALTER TABLE recipe        ADD COLUMN admin_edited_at timestamptz;
ALTER TABLE specialist    ADD COLUMN admin_edited_at timestamptz;
ALTER TABLE workout_class ADD COLUMN admin_edited_at timestamptz;
ALTER TABLE playlist      ADD COLUMN admin_edited_at timestamptz;

-- O motor escolhe por semente sobre a ordem do catálogo: a ordem tem de ser
-- guardada, e o que nasce no painel entra no fim, para não mudar o treino de
-- quem já o tem.
ALTER TABLE exercise ADD COLUMN position integer;
ALTER TABLE exercise ADD COLUMN pattern text;
ALTER TABLE food     ADD COLUMN position integer;
ALTER TABLE recipe   ADD COLUMN position integer;
-- A ordem dos ingredientes é a do prato escrito, e é a que se mostra.
ALTER TABLE recipe_ingredient ADD COLUMN position smallint NOT NULL DEFAULT 0;

ALTER TABLE recipe ADD COLUMN validated_by text;
ALTER TABLE recipe ADD COLUMN validated_at timestamptz;

ALTER TABLE specialist ADD COLUMN photo_url text;
ALTER TABLE specialist ADD COLUMN max_clients integer CHECK (max_clients IS NULL OR max_clients > 0);

-- Modelos validados por profissionais. Guardam-se aqui; o motor continua a
-- gerar o plano de cada pessoa e, quando os usar, usa só os aprovados.
CREATE TABLE workout_template (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    focus      session_focus NOT NULL,
    level      experience NOT NULL,
    minutes    smallint NOT NULL CHECK (minutes BETWEEN 5 AND 180),
    goal       text NOT NULL DEFAULT '',
    status     text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved')),
    blocks     jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL DEFAULT ''
);

CREATE TABLE diet_model (
    id            text PRIMARY KEY,
    name          text NOT NULL,
    goal          nutrition_goal NOT NULL,
    meals_per_day smallint NOT NULL CHECK (meals_per_day BETWEEN 3 AND 5),
    status        text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'review', 'approved')),
    rules         jsonb NOT NULL DEFAULT '{}'::jsonb,
    week          jsonb NOT NULL DEFAULT '[]'::jsonb,
    comments      jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    text NOT NULL DEFAULT '',
    approved_by   text
);

-- O estado do vídeo como o painel o mostra. `mux_ready` diz se toca; isto diz
-- também que se está à espera do envio, ou que o Mux recusou o ficheiro.
ALTER TABLE workout_class ADD COLUMN video_status text NOT NULL DEFAULT 'none'
    CHECK (video_status IN ('none', 'waiting', 'ready', 'failed'));
UPDATE workout_class SET video_status = 'ready' WHERE mux_ready;
