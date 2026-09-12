-- Os factos que o cliente acumula durante a sessão.
--
-- O Modo Foco corre offline: o cliente **acumula factos** e envia-os em lote
-- quando a rede volta — possivelmente fora de ordem, possivelmente duas vezes,
-- possivelmente horas depois. Nenhum deles conclui nada; quem decide é o
-- servidor, ao recebê-los.
--
-- ⚠️ `session_ended` traz `durationSeconds`, **não** `status`. Deixar o cliente
-- mandar "completed" devolvia-lhe a regra pela porta das traseiras.
CREATE TABLE session_event (
    id         bigserial PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES workout_session(id) ON DELETE CASCADE,

    -- step_advanced, set_completed, set_removed, set_added, session_ended
    kind       text NOT NULL,
    -- Índice do passo na linha do tempo. NULL em eventos de sessão.
    step_index smallint,
    payload    jsonb NOT NULL DEFAULT '{}',

    -- Quando aconteceu **no dispositivo**. Não é quando chegou: um evento de
    -- há três horas que chega agora aconteceu há três horas, e ordená-lo pela
    -- chegada faria a sessão parecer instantânea.
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- Reenviar o mesmo lote não duplica nada.
--
-- A chave natural de um evento é o passo, o tipo e o instante: o mesmo passo
-- avançado no mesmo milissegundo é o mesmo facto. Sem isto, uma sincronização
-- repetida contava cada série duas vezes.
CREATE UNIQUE INDEX session_event_unique
    ON session_event (session_id, kind, coalesce(step_index, -1), occurred_at);

-- A leitura é sempre por sessão e por ordem de acontecimento.
CREATE INDEX session_event_stream ON session_event (session_id, occurred_at);
