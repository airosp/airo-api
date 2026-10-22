-- A camada de inteligência: consentimentos, execuções, propostas, consumo e
-- auditoria.
--
-- ⚠️ **Nada daqui decide.** O modelo reconhece e estrutura; quem calcula
-- calorias, macros e prescrições continuam a ser os motores. Estas tabelas
-- guardam o caminho entre uma coisa e outra: o que foi pedido, com que
-- autorização, o que voltou, o que a validação aceitou e o que a pessoa
-- confirmou. Sem esse caminho escrito não há como explicar, meses depois,
-- porque é que um registo tem os valores que tem.
--
-- Tudo pende de `app_user` com `ON DELETE CASCADE`: apagar a conta apaga o
-- rasto da IA com ela, sem uma lista à parte que alguém se esquece de manter.

-- ── consentimentos ─────────────────────────────────────────────────────────
--
-- Um por finalidade, e não um genérico: autorizar a análise de fotografias
-- não é autorizar a leitura do progresso. Revogar não apaga a linha — fecha-a
-- com `revoked_at`. O histórico é o que permite provar que, no dia em que uma
-- fotografia foi analisada, havia autorização para isso.
CREATE TABLE ai_consent (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    purpose        text NOT NULL CHECK (purpose IN ('meal_photo','meal_text','progress','specialist_share')),
    -- A versão do texto que a pessoa leu. Mudar o texto obriga a pedir outra vez.
    policy_version text NOT NULL,
    granted_at     timestamptz NOT NULL DEFAULT now(),
    revoked_at     timestamptz,
    CHECK (revoked_at IS NULL OR revoked_at >= granted_at)
);
-- No máximo uma autorização em vigor por finalidade.
CREATE UNIQUE INDEX ai_consent_activo ON ai_consent (user_id, purpose) WHERE revoked_at IS NULL;

-- ── execuções ──────────────────────────────────────────────────────────────
--
-- A fila é esta tabela. O Postgres é a fonte de verdade e não o Redis: uma
-- mensagem volátil perdida num reinício era uma fotografia que ficava "a
-- analisar" para sempre. Um processo reserva uma linha com `lease_until`; se
-- morrer a meio, o prazo passa e outro apanha-a.
CREATE TABLE ai_job (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    task_type        text NOT NULL CHECK (task_type IN ('meal_photo','meal_text')),
    status           text NOT NULL DEFAULT 'queued' CHECK (status IN (
                         'queued','processing','validating','ready_for_review','needs_correction',
                         'applied','dismissed','failed','cancelled','expired')),
    schema_version   text NOT NULL,
    consent_id       uuid REFERENCES ai_consent(id) ON DELETE SET NULL,
    model_version    text,
    idempotency_key  text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    /*
     * O mínimo para o trabalho correr: o endereço da fotografia, ou o texto
     * escrito. O texto é apagado quando a execução termina — fica a referência
     * à proposta, que é o que se precisa para investigar.
     */
    input_ref        jsonb NOT NULL DEFAULT '{}'::jsonb,
    result_ref       jsonb,
    error_code       text,
    attempts         int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_until      timestamptz,
    -- Quando a próxima tentativa pode correr. Recuo após uma falha passageira.
    run_after        timestamptz NOT NULL DEFAULT now(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    UNIQUE (user_id, task_type, idempotency_key)
);
CREATE INDEX ai_job_user_status_idx ON ai_job (user_id, status, created_at DESC);
-- O que o executor procura: o que está à espera e já pode correr.
CREATE INDEX ai_job_fila_idx ON ai_job (run_after) WHERE status IN ('queued','processing');

-- ── propostas ──────────────────────────────────────────────────────────────
--
-- O rascunho que a pessoa revê. `version` sobe a cada mudança: confirmar uma
-- versão que já não é a última é recusado, para ninguém gravar o que não viu.
CREATE TABLE ai_proposal (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id        uuid NOT NULL UNIQUE REFERENCES ai_job(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    task_type     text NOT NULL,
    status        text NOT NULL CHECK (status IN ('ready_for_review','needs_correction','applied','dismissed','expired')),
    version       int NOT NULL DEFAULT 1 CHECK (version >= 1),
    schema_version text NOT NULL,
    payload       jsonb NOT NULL,
    -- O que a confirmação gravou: o identificador do registo no diário.
    applied_ref   text,
    applied_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    CHECK ((status = 'applied') = (applied_at IS NOT NULL))
);
CREATE INDEX ai_proposal_user_idx ON ai_proposal (user_id, created_at DESC);

-- ── consumo ────────────────────────────────────────────────────────────────
--
-- Uma linha por chamada ao fornecedor. É daqui que sai o orçamento do mês —
-- somado na base de dados, e não num contador em memória que cada réplica
-- tinha o seu.
--
-- A única excepção ao `CASCADE`: apagar a conta tira o `user_id` e deixa os
-- números. O que fica são tokens e cêntimos sem dono — e sem eles o orçamento
-- do mês descia sempre que alguém apagava a conta, como se a chamada não
-- tivesse custado nada.
CREATE TABLE ai_usage (
    id              bigserial PRIMARY KEY,
    user_id         uuid REFERENCES app_user(id) ON DELETE SET NULL,
    job_id          uuid REFERENCES ai_job(id) ON DELETE SET NULL,
    task_type       text NOT NULL,
    model           text NOT NULL,
    request_id      text,
    input_tokens    bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens   bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    -- Milionésimos de dólar: inteiros, para a soma do mês não acumular erro.
    cost_micro_usd  bigint NOT NULL DEFAULT 0 CHECK (cost_micro_usd >= 0),
    latency_ms      int NOT NULL DEFAULT 0,
    outcome         text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_usage_mes_idx ON ai_usage (created_at);

-- ── auditoria ──────────────────────────────────────────────────────────────
--
-- Referências e metadados, nunca o conteúdo: nem a fotografia, nem o texto
-- escrito, nem o pedido enviado ao modelo.
CREATE TABLE ai_audit_event (
    id           bigserial PRIMARY KEY,
    user_id      uuid REFERENCES app_user(id) ON DELETE CASCADE,
    job_id       uuid,
    proposal_id  uuid,
    kind         text NOT NULL,
    detail       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_audit_user_idx ON ai_audit_event (user_id, created_at DESC);
