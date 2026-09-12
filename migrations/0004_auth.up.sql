-- Identidade: telefone + OTP.
--
-- Estava em docs/backend/08-autenticacao.md §10 e não tinha migração. É o único
-- sítio do sistema onde um erro custa contas de utilizadores.

CREATE TYPE otp_channel AS ENUM ('whatsapp','sms');
CREATE TYPE otp_status  AS ENUM ('pending','verified','expired','exhausted','superseded');

CREATE TABLE otp_challenge (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_e164   text NOT NULL CHECK (phone_e164 ~ '^\+[1-9][0-9]{7,14}$'),

    -- ⚠️ HMAC-SHA256 com pepper de fora da base de dados.
    -- O código NUNCA é guardado: uma fuga só da BD deixa-o inútil.
    code_hash    bytea NOT NULL,

    channel      otp_channel NOT NULL,
    status       otp_status NOT NULL DEFAULT 'pending',
    attempts     smallint NOT NULL DEFAULT 0,
    max_attempts smallint NOT NULL DEFAULT 5,

    device_id    text,
    -- Hash: o IP em claro é dado pessoal.
    ip_hash      bytea,

    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    verified_at  timestamptz,

    -- Estado da entrega, vindo do webhook. É o que permite decidir o recurso ao
    -- SMS e detectar o número que nunca recebe — o sinal de "não tem WhatsApp".
    provider_message_id text,
    delivery_status     text,

    CONSTRAINT otp_expiry_future CHECK (expires_at > created_at)
);

-- Um desafio pendente por número. Pedir um novo marca o anterior 'superseded' —
-- senão, dois códigos válidos ao mesmo tempo duplicam as tentativas.
CREATE UNIQUE INDEX otp_one_pending
    ON otp_challenge (phone_e164) WHERE status = 'pending';
CREATE INDEX otp_cleanup ON otp_challenge (expires_at) WHERE status = 'pending';

CREATE TABLE device (
    id            text PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    platform      text NOT NULL CHECK (platform IN ('ios','android','web')),
    model         text,
    app_version   text,
    push_token    text,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE refresh_token (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    device_id  text NOT NULL REFERENCES device(id) ON DELETE CASCADE,

    -- SHA-256 do token opaco. O token em claro só existe no cliente.
    token_hash bytea UNIQUE NOT NULL,

    -- Família: a rotação mantém-na; reutilizar um token revoga a família toda.
    -- Sem isto, quem copia um refresh fica com acesso indefinido sem que se note.
    family_id   uuid NOT NULL,
    replaced_by uuid REFERENCES refresh_token(id),

    issued_at     timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoke_reason text
);
CREATE INDEX refresh_by_family ON refresh_token (family_id) WHERE revoked_at IS NULL;
CREATE INDEX refresh_by_user   ON refresh_token (user_id)   WHERE revoked_at IS NULL;

-- Trilho de auditoria. Append-only, e **sem o código**.
CREATE TABLE auth_event (
    id          bigserial PRIMARY KEY,
    user_id     uuid REFERENCES app_user(id) ON DELETE SET NULL,
    phone_e164  text,
    kind        text NOT NULL,
    device_id   text,
    ip_hash     bytea,
    meta        jsonb NOT NULL DEFAULT '{}',
    occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_event_phone ON auth_event (phone_e164, occurred_at DESC);
